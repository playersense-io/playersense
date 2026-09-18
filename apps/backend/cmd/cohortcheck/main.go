// Command cohortcheck seeds synthetic events with a known answer and asserts that the
// cohort pipeline reproduces it.
//
// This is not a smoke test. Migrations applying cleanly proves the DDL parses; it proves
// nothing about whether the arithmetic is right. Each check below corresponds to a claim
// project.md §29 makes, and several correspond to bugs that were found and fixed while
// the design was being reviewed — those exist so that reverting the fix fails loudly.
//
//	go run ./apps/backend/cmd/cohortcheck
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/cohort"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/rollup"
)

const (
	projectSeq     uint32 = 1
	environmentSeq uint16 = 1
)

// The seeded population. Every number here is an expected answer.
var (
	cohortA = date(2026, 9, 1) // 100 devices: 60 back on D1, 30 on D3, 10 on D7
	cohortB = date(2026, 9, 5) // 50 devices:  25 back on D1
	cohortC = date(2026, 9, 2) // 20 devices that sign in mid-life

	sizeA, backA1, backA3, backA7 = 100, 60, 30, 10
	sizeB, backB1                 = 50, 25
	sizeC                         = 20
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

type check struct {
	name string
	err  error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cohortcheck:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	settings, err := config.Load(filepath.Join(root, ".env"))
	if err != nil {
		return err
	}
	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		return err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sqlDB := clickhouse.OpenDB(opts)
	defer sqlDB.Close()
	deriver := &cohort.Deriver{DB: sqlDB}

	fmt.Println("seeding…")
	if err := truncateAll(ctx, conn); err != nil {
		return err
	}
	if err := seed(ctx, conn); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	month := cohort.MonthOf(cohortA)
	if err := deriver.DeriveMonth(ctx, month); err != nil {
		return err
	}
	if err := deriver.DeriveSizes(ctx, month, date(2026, 9, 30)); err != nil {
		return err
	}
	if err := deriver.DeriveBuckets(ctx, month); err != nil {
		return err
	}
	rollups := &rollup.Deriver{DB: sqlDB}
	if err := rollups.DeriveMonth(ctx, month); err != nil {
		return err
	}

	checks := []check{
		{"1. classic retention matches the seeded construction", checkClassicRetention(ctx, conn)},
		{"2. cohorts are a disjoint partition of the devices", checkDisjoint(ctx, conn)},
		{"3. a mid-life sign-in does not split a device across cohorts", checkSignIn(ctx, conn)},
		{"4. REPLACE PARTITION rebuilds a month without losing offsets", checkPartitionSwap(ctx, conn, deriver, month)},
		{"5. re-derivation is idempotent", checkIdempotent(ctx, conn, deriver, month)},
		{"6. a late event is absorbed without double counting", checkLateEvent(ctx, conn, deriver, month)},
		{"7. no cell has returned_players > cohort_size", checkDenominator(ctx, conn)},
		{"8. rolling retention is a correct suffix sum", checkRolling(ctx, conn, deriver)},
		{"9. every breakdown cell has a within-value denominator", checkBreakdownDenominator(ctx, conn)},
		{"10. a bucket kind partitions the returners at every offset", checkBuckets(ctx, conn, deriver, month)},
		{"11. daily active players is an exact distinct count", checkDailyActive(ctx, conn, rollups, month)},
		{"12. new and returning players sum to active", checkNewAndReturning(ctx, conn)},
		{"13. a metric breakdown partitions the active players", checkMetricBreakdown(ctx, conn)},
		{"14. monthly active players is not the sum of the days", checkMonthlyActive(ctx, conn)},
		{"15. an unmerged partial row does not inflate the active count", checkPartialRow(ctx, conn, rollups, month)},
	}

	fmt.Println()
	failed := 0
	for _, c := range checks {
		if c.err != nil {
			failed++
			fmt.Printf("  FAIL  %s\n        %v\n", c.name, c.err)
		} else {
			fmt.Printf("  ok    %s\n", c.name)
		}
	}
	fmt.Println()
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(checks))
	}
	fmt.Printf("all %d checks passed\n", len(checks))
	return nil
}

func truncateAll(ctx context.Context, conn driver.Conn) error {
	for _, t := range []string{
		"events_raw", "player_lifetime", "player_activity_daily",
		"cohort_metrics_daily", "cohort_metrics_breakdown_daily", "cohort_sizes",
		"cohort_metrics_daily_staging", "cohort_metrics_breakdown_daily_staging", "cohort_sizes_staging",
	} {
		if err := conn.Exec(ctx, "TRUNCATE TABLE IF EXISTS "+t); err != nil {
			return fmt.Errorf("truncate %s: %w", t, err)
		}
	}
	return nil
}

// deviceUUID maps a device index to a stable install identifier.
func deviceUUID(idx int) uuid.UUID {
	var u uuid.UUID
	u[0] = 0x01
	u[6] = 0x40
	u[8] = 0x80
	u[12] = byte(idx >> 24)
	u[13] = byte(idx >> 16)
	u[14] = byte(idx >> 8)
	u[15] = byte(idx)
	return u
}

const insertColumns = `INSERT INTO events_raw (
	org_seq, project_seq, environment_seq, org_id, project_id, environment_id,
	retention_tier, policy_tier, event_time, ingest_time, received_at,
	event_id, batch_id, session_id, player_id, install_id,
	event_name, event_type, platform, country_code, app_version, sdk_name, sdk_version,
	cohort_date, install_platform, install_country, first_app_version,
	consent_analytics, consent_policy_version, att_status,
	revenue_micros, currency, playtime_ms, payload_bytes)`

// appendEvent writes one session event for a device on a day.
//
// cohort_date is stamped here, the way analytics-writer stamps it from the Valkey
// device-state store (§29.5). Nothing downstream resolves it, which is what keeps the
// derivation a single-table GROUP BY.
func appendEvent(batch driver.Batch, idx int, day, cohortDay time.Time, playerID string, purchase bool) error {
	platform := []string{"ios", "android"}[idx%2]
	country := []string{"TR", "US", "DE"}[idx%3]
	name, typ := "session_start", "session"
	var revenue int64
	if purchase {
		name, typ, revenue = "purchase", "purchase", 990000
	}
	ts := day.Add(10 * time.Hour)
	return batch.Append(
		uint32(1), projectSeq, environmentSeq, uuid.Nil, uuid.Nil, uuid.Nil,
		uint16(30), "standard", ts, ts, ts,
		uuid.New(), uuid.New(), fmt.Sprintf("s-%d-%s", idx, day.Format("0102")), playerID, deviceUUID(idx),
		name, typ, platform, country, "2.7.1", "unity", "1.4.0",
		cohortDay, platform, country, "2.7.1",
		uint8(1), "2026-01-15", "not_determined",
		revenue, "USD", uint64(60000), uint32(512),
	)
}

func seed(ctx context.Context, conn driver.Conn) error {
	batch, err := conn.PrepareBatch(ctx, insertColumns)
	if err != nil {
		return err
	}

	// Cohort A — the retention curve under test.
	for i := 0; i < sizeA; i++ {
		idx := 1000 + i
		if err := appendEvent(batch, idx, cohortA, cohortA, "", i%10 == 0); err != nil {
			return err
		}
		for _, r := range []struct {
			offset int
			count  int
		}{{1, backA1}, {3, backA3}, {7, backA7}} {
			if i < r.count {
				if err := appendEvent(batch, idx, cohortA.AddDate(0, 0, r.offset), cohortA, "", false); err != nil {
					return err
				}
			}
		}
	}

	// Cohort B — a second cohort, so disjointness is testable.
	for i := 0; i < sizeB; i++ {
		idx := 2000 + i
		if err := appendEvent(batch, idx, cohortB, cohortB, "", false); err != nil {
			return err
		}
		if i < backB1 {
			if err := appendEvent(batch, idx, cohortB.AddDate(0, 0, 1), cohortB, "", false); err != nil {
				return err
			}
		}
	}

	// Cohort C — anonymous for two days, then signed in. actor_key changes; device_key
	// does not, and the cohort must follow the device (§29.2).
	for i := 0; i < sizeC; i++ {
		idx := 3000 + i
		if err := appendEvent(batch, idx, cohortC, cohortC, "", false); err != nil {
			return err
		}
		if err := appendEvent(batch, idx, cohortC.AddDate(0, 0, 1), cohortC, "", false); err != nil {
			return err
		}
		if err := appendEvent(batch, idx, cohortC.AddDate(0, 0, 2), cohortC, fmt.Sprintf("player-%d", idx), false); err != nil {
			return err
		}
	}

	return batch.Send()
}

// ---- checks -----------------------------------------------------------------

func scalar[T any](ctx context.Context, conn driver.Conn, query string, args ...any) (T, error) {
	var v T
	err := conn.QueryRow(ctx, query, args...).Scan(&v)
	return v, err
}

func checkClassicRetention(ctx context.Context, conn driver.Conn) error {
	for _, want := range []struct {
		offset uint16
		count  int
	}{{0, sizeA}, {1, backA1}, {3, backA3}, {7, backA7}} {
		got, err := scalar[uint64](ctx, conn, `
			SELECT sum(returned_players) FROM cohort_metrics_daily
			WHERE project_seq = ? AND environment_seq = ? AND cohort_date = ? AND day_offset = ?`,
			projectSeq, environmentSeq, cohortA, want.offset)
		if err != nil {
			return err
		}
		if got != uint64(want.count) {
			return fmt.Errorf("cohort %s D%d: got %d returners, want %d",
				cohortA.Format("2006-01-02"), want.offset, got, want.count)
		}
	}
	// A day nobody was seeded for must be absent rather than wrong.
	got, err := scalar[uint64](ctx, conn, `
		SELECT sum(returned_players) FROM cohort_metrics_daily
		WHERE cohort_date = ? AND day_offset = 2`, cohortA)
	if err != nil {
		return err
	}
	if got != 0 {
		return fmt.Errorf("cohort %s D2: got %d returners, want 0", cohortA.Format("2006-01-02"), got)
	}
	return nil
}

func checkDisjoint(ctx context.Context, conn driver.Conn) error {
	// Every device has exactly one cohort_date in the lifetime view.
	multi, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
			SELECT device_id, uniqExact(cohort_date) AS n FROM player_lifetime_v
			GROUP BY device_id HAVING n > 1)`)
	if err != nil {
		return err
	}
	if multi != 0 {
		return fmt.Errorf("%d devices appear in more than one cohort", multi)
	}

	// Summing D0 across cohorts equals the total device population, which only holds
	// because cohorts partition the devices rather than overlapping.
	summed, err := scalar[uint64](ctx, conn, `
		SELECT sum(returned_players) FROM cohort_metrics_daily WHERE day_offset = 0`)
	if err != nil {
		return err
	}
	total, err := scalar[uint64](ctx, conn, `SELECT count() FROM player_lifetime_v`)
	if err != nil {
		return err
	}
	if summed != total {
		return fmt.Errorf("sum of D0 across cohorts = %d, but there are %d devices", summed, total)
	}
	return nil
}

func checkSignIn(ctx context.Context, conn driver.Conn) error {
	// The 20 sign-in devices must all sit in cohort C, on D0, D1 and D2.
	for _, offset := range []uint16{0, 1, 2} {
		got, err := scalar[uint64](ctx, conn, `
			SELECT sum(returned_players) FROM cohort_metrics_daily
			WHERE cohort_date = ? AND day_offset = ?`, cohortC, offset)
		if err != nil {
			return err
		}
		if got != uint64(sizeC) {
			return fmt.Errorf("cohort %s D%d: got %d, want %d — a sign-in split the device",
				cohortC.Format("2006-01-02"), offset, got, sizeC)
		}
	}
	// And they must not have produced a phantom cohort on the day they signed in.
	phantom, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM cohort_sizes WHERE cohort_date = ?`, cohortC.AddDate(0, 0, 2))
	if err != nil {
		return err
	}
	if phantom != 0 {
		return fmt.Errorf("a phantom cohort was created on the sign-in day")
	}
	return nil
}

// snapshot returns the per-cell counts, so two derivations can be compared exactly.
func snapshot(ctx context.Context, conn driver.Conn) (map[string]uint64, error) {
	rows, err := conn.Query(ctx, `
		SELECT cohort_date, day_offset, sum(returned_players)
		FROM cohort_metrics_daily GROUP BY cohort_date, day_offset`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var d time.Time
		var offset uint16
		var n uint64
		if err := rows.Scan(&d, &offset, &n); err != nil {
			return nil, err
		}
		out[fmt.Sprintf("%s/%d", d.Format("2006-01-02"), offset)] = n
	}
	return out, rows.Err()
}

// checkPartitionSwap is the assertion that matters most.
//
// Partitioning a cohort fact on the cohort month and rebuilding it from a trailing
// activity window destroys data: one activity day contributes a thin slice to thirteen
// cohort months, so the swap deletes every offset the slice does not contain. This
// asserts the partitioning is on the activity month by rebuilding one month and
// requiring every cell to survive.
func checkPartitionSwap(ctx context.Context, conn driver.Conn, d *cohort.Deriver, m cohort.Month) error {
	before, err := snapshot(ctx, conn)
	if err != nil {
		return err
	}
	if len(before) == 0 {
		return errors.New("no cells to compare — the derivation produced nothing")
	}
	if err := d.DeriveMonth(ctx, m); err != nil {
		return err
	}
	after, err := snapshot(ctx, conn)
	if err != nil {
		return err
	}
	for cell, want := range before {
		got, ok := after[cell]
		if !ok {
			return fmt.Errorf("cell %s was deleted by the partition swap", cell)
		}
		if got != want {
			return fmt.Errorf("cell %s changed from %d to %d across a rebuild", cell, want, got)
		}
	}
	if len(after) != len(before) {
		return fmt.Errorf("cell count changed from %d to %d across a rebuild", len(before), len(after))
	}
	return nil
}

func checkIdempotent(ctx context.Context, conn driver.Conn, d *cohort.Deriver, m cohort.Month) error {
	first, err := snapshot(ctx, conn)
	if err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if err := d.DeriveMonth(ctx, m); err != nil {
			return err
		}
	}
	second, err := snapshot(ctx, conn)
	if err != nil {
		return err
	}
	for cell, want := range first {
		if second[cell] != want {
			return fmt.Errorf("cell %s: %d before, %d after two more derivations", cell, want, second[cell])
		}
	}
	return nil
}

func checkLateEvent(ctx context.Context, conn driver.Conn, d *cohort.Deriver, m cohort.Month) error {
	const offset = 3
	before, err := scalar[uint64](ctx, conn, `
		SELECT sum(returned_players) FROM cohort_metrics_daily
		WHERE cohort_date = ? AND day_offset = ?`, cohortA, uint16(offset))
	if err != nil {
		return err
	}

	// A device from cohort A that had not been seen on D3 arrives late.
	batch, err := conn.PrepareBatch(ctx, insertColumns)
	if err != nil {
		return err
	}
	if err := appendEvent(batch, 1000+backA3, cohortA.AddDate(0, 0, offset), cohortA, "", false); err != nil {
		return err
	}
	if err := batch.Send(); err != nil {
		return err
	}

	if err := d.DeriveMonth(ctx, m); err != nil {
		return err
	}
	after, err := scalar[uint64](ctx, conn, `
		SELECT sum(returned_players) FROM cohort_metrics_daily
		WHERE cohort_date = ? AND day_offset = ?`, cohortA, uint16(offset))
	if err != nil {
		return err
	}
	if after != before+1 {
		return fmt.Errorf("D%d went from %d to %d after one late event; want %d", offset, before, after, before+1)
	}
	return nil
}

func checkDenominator(ctx context.Context, conn driver.Conn) error {
	// The numerator and denominator live in different tables on different partition
	// cadences. Reading them together is the one place a bounded lookup is allowed —
	// cohort_sizes is a few hundred thousand rows, not one row per player (§29.1).
	bad, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
			SELECT m.cohort_date AS cd, m.day_offset AS off,
			       sum(m.returned_players) AS returned,
			       any(s.cohort_size) AS size
			FROM cohort_metrics_daily AS m
			INNER JOIN (
			    SELECT cohort_date, sum(cohort_size) AS cohort_size
			    FROM cohort_sizes WHERE dim_name = '__total__'
			    GROUP BY cohort_date
			) AS s ON s.cohort_date = m.cohort_date
			GROUP BY cd, off
			HAVING returned > size)`)
	if err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("%d cells have more returners than the cohort had members", bad)
	}
	return nil
}

// checkBreakdownDenominator proves the two properties §29.9 asks of a breakdown size, both
// of which are silent when they break.
//
// A broken-down retention chart divides a numerator from cohort_metrics_breakdown_daily by
// a size from cohort_sizes carrying the same (dim_name, dim_value). If the size is missing
// the rate is undefined; if it is the *whole* cohort's size rather than the value's own,
// every value reads low and they no longer sum to the unbroken-down curve. Neither shows up
// as an error — the chart just renders a wrong number.
func checkBreakdownDenominator(ctx context.Context, conn driver.Conn) error {
	// 1. Every numerator cell has a denominator, and never exceeds it.
	//
	// The lookup is bounded — cohort_sizes is a few hundred thousand rows per project, not
	// one row per player — so §29.1 permits it here for the same reason checkDenominator
	// does it for the total.
	bad, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM cohort_metrics_breakdown_daily AS b
		LEFT JOIN cohort_sizes AS s
		    USING (project_seq, environment_seq, cohort_date, dim_name, dim_value)
		WHERE s.cohort_size = 0 OR b.returned_players > s.cohort_size`)
	if err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("%d breakdown cells have no denominator or more returners than members", bad)
	}

	// 2. Each dimension partitions the cohort exactly once.
	//
	// Every device has exactly one value per dimension, so summing the sizes within a
	// dimension must reproduce the total. A mismatch means either a device was counted
	// under two values or the whole cohort's size was stored against each value — the
	// failure that multiplies the denominator the moment anyone sums across values.
	split, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
		    SELECT dim_name, cohort_date, sum(cohort_size) AS n
		    FROM cohort_sizes WHERE dim_name != '__total__'
		    GROUP BY dim_name, cohort_date
		) AS d
		INNER JOIN (
		    SELECT cohort_date, sum(cohort_size) AS total
		    FROM cohort_sizes WHERE dim_name = '__total__'
		    GROUP BY cohort_date
		) AS t USING (cohort_date)
		WHERE d.n != t.total`)
	if err != nil {
		return err
	}
	if split != 0 {
		return fmt.Errorf("%d (dimension, cohort) pairs do not sum to the cohort size", split)
	}
	return nil
}

// checkBuckets proves a behavioural segment is a partition, not a filter.
//
// Every device active at (cohort_date, day_offset) falls into exactly one playtime bucket
// and exactly one spend bucket, so summing `players` across the codes of one kind must
// reproduce cohort_metrics_daily's returned_players for that cell. The two tables are
// derived from different sources — buckets resolve the device against player_lifetime_v,
// the metrics fact reads player_activity_daily alone — so agreeing is a real check on the
// join rather than a restatement of it.
//
// Off-by-one in a boundary would still partition, so this does not catch a wrong boundary.
// It catches the failures that matter operationally: a device dropped by the join, a device
// counted under two codes, and a bucket set that does not cover the whole range.
func checkBuckets(ctx context.Context, conn driver.Conn, d *cohort.Deriver, m cohort.Month) error {
	// Re-derived first, because the checks above deliberately mutate the month: 6 adds a
	// late event and rebuilds the metrics fact. Comparing a bucket table derived before
	// that against a metrics table derived after it measures the gap between two
	// derivations rather than anything about buckets. Re-running also means this asserts
	// that a late event is absorbed here too, which is the property 6 checks for the
	// metrics fact.
	if err := d.DeriveBuckets(ctx, m); err != nil {
		return err
	}

	total, err := scalar[uint64](ctx, conn, "SELECT count() FROM cohort_bucket_daily")
	if err != nil {
		return err
	}
	if total == 0 {
		return fmt.Errorf("cohort_bucket_daily is empty; the derivation wrote nothing")
	}

	bad, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
		    SELECT bucket_kind, cohort_date, day_offset, sum(players) AS n
		    FROM cohort_bucket_daily
		    GROUP BY bucket_kind, cohort_date, day_offset
		) AS b
		INNER JOIN (
		    SELECT cohort_date, day_offset, sum(returned_players) AS returned
		    FROM cohort_metrics_daily
		    GROUP BY cohort_date, day_offset
		) AS m USING (cohort_date, day_offset)
		WHERE b.n != m.returned`)
	if err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("%d (kind, cohort, offset) cells do not sum to the returners", bad)
	}
	return nil
}

// checkDailyActive is the one number this whole tier exists to get right.
//
// metrics_daily.active_players must equal the exact distinct devices in
// player_activity_daily for that day. The failure this guards is not a wrong query — it is
// §29.14's trap: player_activity_daily is a SummingMergeTree, so a device-day exists as
// several partial rows until parts merge, and counting without collapsing first counts one
// player more than once. The drift is upward, gradual, and reports no error.
func checkDailyActive(ctx context.Context, conn driver.Conn, d *rollup.Deriver, m cohort.Month) error {
	// Re-derived first, for the reason check 10 gives: the checks above deliberately mutate
	// the month, and comparing a rollup built before check 6's late event against a source
	// that now includes it measures the gap between two derivations. Re-running also
	// asserts that a late event is absorbed here, which is check 6's property for the
	// cohort tier.
	if err := d.DeriveMonth(ctx, m); err != nil {
		return err
	}

	bad, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
		    SELECT activity_date, sum(active_players) AS reported
		    FROM metrics_daily GROUP BY activity_date
		) AS m
		INNER JOIN (
		    SELECT activity_date, uniqExact(device_id) AS actual
		    FROM player_activity_daily GROUP BY activity_date
		) AS a USING (activity_date)
		WHERE m.reported != a.actual`)
	if err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("%d days report an active-player count that is not the distinct device count", bad)
	}
	return nil
}

// checkNewAndReturning holds the invariant by construction rather than by two counts that
// happen to agree.
//
// A player is new on the day they arrived and returning on every other day they appear.
// There is no third state — a device whose cohort is unknown because Valkey was down at
// write time is counted as returning, deliberately, so this identity never breaks.
func checkNewAndReturning(ctx context.Context, conn driver.Conn) error {
	for _, table := range []string{"metrics_daily", "metrics_breakdown_daily"} {
		bad, err := scalar[uint64](ctx, conn, fmt.Sprintf(`
			SELECT count() FROM %s
			WHERE new_players + returning_players != active_players`, table))
		if err != nil {
			return err
		}
		if bad != 0 {
			return fmt.Errorf("%d rows of %s do not split into new and returning", bad, table)
		}
	}
	return nil
}

// checkMetricBreakdown is the daily tier's version of check 9.
//
// Every device has exactly one value per dimension, so summing a dimension's active players
// must reproduce the day's total. A mismatch means a device was counted under two values or
// dropped by one of the four UNION arms.
func checkMetricBreakdown(ctx context.Context, conn driver.Conn) error {
	bad, err := scalar[uint64](ctx, conn, `
		SELECT count() FROM (
		    SELECT dim_name, activity_date, sum(active_players) AS n
		    FROM metrics_breakdown_daily GROUP BY dim_name, activity_date
		) AS b
		INNER JOIN (
		    SELECT activity_date, sum(active_players) AS total
		    FROM metrics_daily GROUP BY activity_date
		) AS m USING (activity_date)
		WHERE b.n != m.total`)
	if err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("%d (dimension, day) pairs do not sum to the day's active players", bad)
	}
	return nil
}

// checkMonthlyActive proves MAU is its own distinct count rather than an aggregate of DAU.
//
// A player active on the 3rd and the 9th is one monthly active player and two daily ones.
// So MAU must be at least the largest single day and at most the sum of the days, and on
// any real data it is strictly between them — a MAU equal to the sum would mean nobody ever
// came back twice, which is exactly what summing the days would produce.
func checkMonthlyActive(ctx context.Context, conn driver.Conn) error {
	type row struct{ mau, peak, summed uint64 }
	var got row
	if err := conn.QueryRow(ctx, `
		SELECT
		    (SELECT sum(active_players) FROM metrics_monthly),
		    (SELECT max(active_players) FROM metrics_daily),
		    (SELECT sum(active_players) FROM metrics_daily)`).
		Scan(&got.mau, &got.peak, &got.summed); err != nil {
		return err
	}
	if got.mau == 0 {
		return fmt.Errorf("no monthly actives were derived")
	}
	if got.mau < got.peak {
		return fmt.Errorf("MAU %d is smaller than the busiest day %d", got.mau, got.peak)
	}
	if got.mau > got.summed {
		return fmt.Errorf("MAU %d exceeds the sum of the days %d", got.mau, got.summed)
	}
	if got.mau == got.summed && got.summed != got.peak {
		return fmt.Errorf("MAU %d equals the sum of the days, so returning players were counted twice", got.mau)
	}
	return nil
}

// checkPartialRow forces §29.14's trap instead of hoping to observe it.
//
// player_activity_daily is a SummingMergeTree: a device-day exists as several partial rows
// until parts merge, and the derivation must collapse them before counting or it counts one
// player once per part. The trap is that on small, already-merged data the wrong query
// gives the right answer — so a test that merely reads whatever is there proves nothing.
// This writes a second partial row for a device-day that already exists, which is exactly
// what an unmerged part looks like, and then asserts two things at once:
//
//   - the active count does not move, because it is still one player;
//   - the measures do move, because the partial row's sessions are real.
//
// Getting only the first right is a derivation that drops data. Getting only the second
// right is DAU that drifts upward and reports no error.
func checkPartialRow(ctx context.Context, conn driver.Conn, d *rollup.Deriver, m cohort.Month) error {
	var (
		day      time.Time
		deviceID uint64
	)
	if err := conn.QueryRow(ctx, `
		SELECT activity_date, device_id FROM player_activity_daily
		ORDER BY activity_date, device_id LIMIT 1`).Scan(&day, &deviceID); err != nil {
		return fmt.Errorf("find a device-day to duplicate: %w", err)
	}

	before, err := dailyRow(ctx, conn, day)
	if err != nil {
		return err
	}

	const extraSessions = 3
	if err := conn.Exec(ctx, `
		INSERT INTO player_activity_daily
		    (project_seq, environment_seq, activity_date, device_id, cohort_date,
		     install_platform, install_country, first_app_version,
		     sessions, playtime_ms, events, revenue_micros, purchases)
		VALUES (?, ?, ?, ?, ?, '', '', '', ?, 0, 0, 0, 0)`,
		projectSeq, environmentSeq, day, deviceID, day, uint64(extraSessions)); err != nil {
		return fmt.Errorf("write a partial row: %w", err)
	}
	if err := d.DeriveMonth(ctx, m); err != nil {
		return err
	}

	after, err := dailyRow(ctx, conn, day)
	if err != nil {
		return err
	}
	if after.active != before.active {
		return fmt.Errorf("a partial row for an existing device changed the active count from %d to %d",
			before.active, after.active)
	}
	if after.sessions != before.sessions+extraSessions {
		return fmt.Errorf("the partial row's %d sessions were not absorbed: %d -> %d",
			extraSessions, before.sessions, after.sessions)
	}
	return nil
}

type dailyTotals struct{ active, sessions uint64 }

func dailyRow(ctx context.Context, conn driver.Conn, day time.Time) (dailyTotals, error) {
	var out dailyTotals
	err := conn.QueryRow(ctx, `
		SELECT sum(active_players), sum(sessions) FROM metrics_daily WHERE activity_date = ?`,
		day).Scan(&out.active, &out.sessions)
	return out, err
}

func checkRolling(ctx context.Context, conn driver.Conn, d *cohort.Deriver) error {
	hist, err := d.RollingRetention(ctx, projectSeq, environmentSeq, cohortA)
	if err != nil {
		return err
	}
	// Rolling D1 = devices whose last activity was on offset >= 1. In cohort A that is
	// everyone who came back at all, which is the D1 group.
	var rolling1 uint64
	for offset, n := range hist {
		if offset >= 1 {
			rolling1 += n
		}
	}
	if rolling1 != uint64(backA1) {
		return fmt.Errorf("rolling D1 = %d, want %d", rolling1, backA1)
	}
	// Rolling D4 = only the devices that reached D7.
	var rolling4 uint64
	for offset, n := range hist {
		if offset >= 4 {
			rolling4 += n
		}
	}
	if rolling4 != uint64(backA7) {
		return fmt.Errorf("rolling D4 = %d, want %d", rolling4, backA7)
	}
	// And rolling must never be below classic at the same offset.
	classic1, err := scalar[uint64](ctx, conn, `
		SELECT sum(returned_players) FROM cohort_metrics_daily
		WHERE cohort_date = ? AND day_offset = 1`, cohortA)
	if err != nil {
		return err
	}
	if rolling1 < classic1 {
		return fmt.Errorf("rolling D1 (%d) is below classic D1 (%d), which is impossible", rolling1, classic1)
	}
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find the repository root")
		}
		dir = parent
	}
}
