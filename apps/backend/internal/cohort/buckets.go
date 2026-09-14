package cohort

import (
	"context"
	"fmt"
	"strings"
)

// Behavioural buckets (project.md §29.11).
//
// A bucket is a cohort keyed on what a player accumulated rather than on a day: "of the
// players who installed on 3 June, how did the ones who went on to spend over $100 behave
// on day 7". That question needs the player's *lifetime* total, which a one-day read of
// player_activity_daily cannot produce — so the assignment comes from player_lifetime_v.
//
// This is the one place in the cohort model that resolves a device to a lifetime fact. It
// is legitimate here and forbidden at query time, which is the same trade §29 makes
// everywhere else: §29.1's rule names a cohort *query*, and the derivation exists precisely
// so the query does not have to do this. It runs nightly, over one month, under its own
// budget — §32.1's "accepted as an asynchronous job that runs under its own resource
// budget where a spill costs latency rather than the ingestion path".

// BucketScheme is a versioned set of boundaries.
//
// Versioned rather than free-form per project, following the precedent §24 sets for
// retention tiers. The version travels on every row, so raising a boundary later cannot
// silently reinterpret history: old rows keep meaning what they meant, and a chart that
// mixes two schemes is a question the reader can see rather than a number that quietly
// changed.
type BucketScheme struct {
	Version  uint16
	Playtime []Bucket
	Spend    []Bucket
}

// Bucket is one segment, named by a code that is stored and a label that is not.
//
// Max is exclusive. The last bucket in a set carries no Max and absorbs the rest, so the
// boundaries always cover the whole range and a player can never fall outside them.
type Bucket struct {
	Code  uint8
	Label string
	Max   int64
	Last  bool
}

// DefaultBucketScheme is scheme 1.
//
// The boundaries are ordinary game-analytics divisions rather than derived from anything:
// they exist to make the segments legible, and the whole point of versioning them is that
// the first guess does not have to be the last. Both sets open with an explicit zero
// bucket, because "never played past install" and "never paid" are the two segments a
// studio asks about first and neither is a small amount of the thing.
var DefaultBucketScheme = BucketScheme{
	Version: 1,
	Playtime: []Bucket{
		{Code: 0, Label: "none", Max: 1},
		{Code: 1, Label: "under 10m", Max: 10 * 60 * 1000},
		{Code: 2, Label: "10m to 1h", Max: 60 * 60 * 1000},
		{Code: 3, Label: "1h to 5h", Max: 5 * 60 * 60 * 1000},
		{Code: 4, Label: "5h to 20h", Max: 20 * 60 * 60 * 1000},
		{Code: 5, Label: "20h to 100h", Max: 100 * 60 * 60 * 1000},
		{Code: 6, Label: "100h and over", Last: true},
	},
	// Micros, so 5_000_000 is five units of the reporting currency. §29.11 says nothing
	// about currency normalization and neither does this: revenue_micros is summed as
	// stored, and a project trading in several currencies gets a segment that mixes them
	// until §27's FX normalization exists.
	Spend: []Bucket{
		{Code: 0, Label: "non-payer", Max: 1},
		{Code: 1, Label: "under 5", Max: 5_000_000},
		{Code: 2, Label: "5 to 20", Max: 20_000_000},
		{Code: 3, Label: "20 to 100", Max: 100_000_000},
		{Code: 4, Label: "100 to 500", Max: 500_000_000},
		{Code: 5, Label: "500 and over", Last: true},
	},
}

// codeExpr renders the boundaries as a multiIf over one column.
//
// Generated rather than written out in the SQL so the boundaries live in exactly one place.
// A scheme edited here and forgotten in a hand-written CASE is a silent reclassification of
// every player, which is the failure the version field exists to prevent — it should not be
// possible to cause it by editing one of two copies.
func codeExpr(column string, buckets []Bucket) string {
	var b strings.Builder
	b.WriteString("multiIf(")
	for _, bucket := range buckets {
		if bucket.Last {
			continue
		}
		fmt.Fprintf(&b, "%s < %d, %d, ", column, bucket.Max, bucket.Code)
	}
	last := buckets[len(buckets)-1]
	fmt.Fprintf(&b, "%d)", last.Code)
	return b.String()
}

// DeriveBuckets rebuilds the behavioural segments for one activity month.
//
// Whole months and a partition swap, for the same reason DeriveMonth uses them: every row
// belongs to exactly one activity-month partition, so rebuilding the month in full is what
// makes the job idempotent and the swap safe.
//
// Only playtime and spend are derived. The `progression` member of the enum stays declared
// and unwritten, because progression is a game-specific milestone the SDK does not yet
// report — deriving it would mean inventing a definition of progress on the studio's
// behalf. It is listed in the open items rather than guessed at.
func (d *Deriver) DeriveBuckets(ctx context.Context, m Month) error {
	part := m.Partition()
	scheme := DefaultBucketScheme

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE cohort_bucket_daily_staging DROP PARTITION ?", part); err != nil {
		return fmt.Errorf("clear bucket staging: %w", err)
	}

	// The inner GROUP BY on activity is the same collapse DeriveMonth needs and for the
	// same reason: player_activity_daily is a SummingMergeTree, so until parts merge a
	// device-day exists as several partial rows and count() over them counts one player
	// more than once.
	//
	// Two arms rather than a cartesian product, matching §29.13's treatment of the
	// breakdown fact: a row belongs to one bucket kind, never to a pair of them.
	stmt := fmt.Sprintf(`
		INSERT INTO cohort_bucket_daily_staging
		WITH
		assigned AS
		(
		    SELECT
		        project_seq,
		        environment_seq,
		        device_id,
		        cohort_date,
		        %s AS playtime_code,
		        %s AS spend_code
		    FROM player_lifetime_v
		),
		activity AS
		(
		    SELECT
		        project_seq, environment_seq, activity_date, device_id,
		        sum(playtime_ms)    AS d_playtime_ms,
		        sum(revenue_micros) AS d_revenue_micros,
		        sum(purchases)      AS d_purchases
		    FROM player_activity_daily
		    WHERE toYYYYMM(activity_date) = ?
		    GROUP BY project_seq, environment_seq, activity_date, device_id
		),
		labelled AS
		(
		    SELECT
		        a.project_seq     AS project_seq,
		        a.environment_seq AS environment_seq,
		        a.activity_date   AS activity_date,
		        s.cohort_date     AS cohort_date,
		        a.d_playtime_ms   AS d_playtime_ms,
		        a.d_revenue_micros AS d_revenue_micros,
		        a.d_purchases     AS d_purchases,
		        'playtime'        AS bucket_kind,
		        s.playtime_code   AS bucket_code
		    FROM activity AS a
		    INNER JOIN assigned AS s USING (project_seq, environment_seq, device_id)
		    UNION ALL
		    SELECT
		        a.project_seq, a.environment_seq, a.activity_date, s.cohort_date,
		        a.d_playtime_ms, a.d_revenue_micros, a.d_purchases,
		        'spend', s.spend_code
		    FROM activity AS a
		    INNER JOIN assigned AS s USING (project_seq, environment_seq, device_id)
		)
		SELECT
		    project_seq,
		    environment_seq,
		    bucket_kind,
		    toUInt16(?) AS bucket_scheme_version,
		    bucket_code,
		    cohort_date,
		    toUInt16(dateDiff('day', cohort_date, activity_date)) AS day_offset,
		    count()               AS players,
		    sum(d_playtime_ms)    AS playtime_ms,
		    sum(d_revenue_micros) AS revenue_micros,
		    sum(d_purchases)      AS purchases
		FROM labelled
		WHERE cohort_date != toDate(0) AND activity_date >= cohort_date
		GROUP BY project_seq, environment_seq, bucket_kind, bucket_code, cohort_date, day_offset`,
		codeExpr("playtime_ms", scheme.Playtime),
		codeExpr("revenue_micros", scheme.Spend))

	if _, err := d.DB.ExecContext(ctx, stmt, part, scheme.Version); err != nil {
		return fmt.Errorf("build cohort_bucket_daily (partition %s): %w", part, err)
	}

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE cohort_bucket_daily REPLACE PARTITION ? FROM cohort_bucket_daily_staging",
		part); err != nil {
		return fmt.Errorf("swap cohort_bucket_daily: %w", err)
	}
	return nil
}
