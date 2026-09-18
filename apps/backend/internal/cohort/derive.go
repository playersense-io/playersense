// Package cohort holds the cohort derivation (project.md §79.1).
//
// Every statement here reads exactly one table. That is the §29.1 rule, and it is a
// property of this file rather than a convention: the cohort key already sits on
// player_activity_daily, stamped through from events_raw by analytics-writer (§29.5), so
// there is nothing to resolve at derivation time.
package cohort

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Deriver rebuilds the served cohort facts.
type Deriver struct {
	DB *sql.DB
}

// SwapPartition publishes a rebuilt staging partition, unless there is nothing to publish.
//
// The emptiness check is not an optimisation; without it the derivation cannot run at all.
// Recent ClickHouse refuses `REPLACE PARTITION` from an empty source, because the previous
// behaviour was to drop the destination partition silently — which is ADR 0003's bug in a
// different costume. Any month with no activity therefore fails the swap outright, and on
// a young deployment that is almost every month in the window.
//
// Skipping rather than enabling `allow_replace_partition_from_empty_source`, and the
// difference matters. "Staging is empty" has two causes that look identical here:
//
//   - the month genuinely has no activity, in which case the destination is already empty
//     and dropping it changes nothing;
//   - the *source* no longer holds the month. `player_activity_daily` has a 400-day TTL
//     (§29.7) while the cohort facts are aggregate retention and are meant to outlive it
//     (§59) — a studio on a 30-day raw tier still sees a 365-day retention curve. Dropping
//     the destination there destroys served facts that nothing can rebuild, because the
//     rows they were derived from are gone.
//
// The second is the one that cannot be undone, so the empty source is treated as "nothing
// to say" rather than "say nothing". Emptying a partition on purpose belongs to the
// deletion and retention paths (§60, §61), which know why they are doing it.
func SwapPartition(ctx context.Context, db *sql.DB, dest, staging, part string) error {
	var parts uint64
	if err := db.QueryRowContext(ctx, `
		SELECT count()
		FROM system.parts
		WHERE database = currentDatabase() AND table = ? AND partition_id = ? AND active`,
		staging, part).Scan(&parts); err != nil {
		return fmt.Errorf("inspect %s partition %s: %w", staging, part, err)
	}
	if parts == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE "+dest+" REPLACE PARTITION ? FROM "+staging, part); err != nil {
		return fmt.Errorf("swap %s partition %s: %w", dest, part, err)
	}
	return nil
}

// Month is an activity month, as ClickHouse partitions name them.
type Month struct {
	Year  int
	Month time.Month
}

// MonthOf returns the activity month a date belongs to.
func MonthOf(t time.Time) Month { return Month{Year: t.Year(), Month: t.Month()} }

// Partition renders the month as a ClickHouse partition id, e.g. 202609.
func (m Month) Partition() string { return fmt.Sprintf("%04d%02d", m.Year, int(m.Month)) }

func (m Month) String() string { return m.Partition() }

// DeriveMonth rebuilds one whole activity month and swaps it in.
//
// Whole months, never a trailing window. Each row of a cohort fact belongs to exactly one
// activity-month partition (§29.8), so a partial rebuild swapped into a whole partition
// deletes every row it does not contain. Rebuilding the entire month is what makes
// REPLACE PARTITION safe, and it is what makes the job idempotent: running it twice
// produces the same table, and late events inside the month are absorbed without double
// counting.
func (d *Deriver) DeriveMonth(ctx context.Context, m Month) error {
	part := m.Partition()

	steps := []struct {
		name string
		stmt string
		args []any

		// dest and staging mark a step as a partition swap, which goes through
		// SwapPartition rather than being executed directly — an empty staging partition
		// must be skipped rather than dropping what it would have replaced.
		dest    string
		staging string
	}{
		{
			name: "clear metrics staging",
			stmt: "ALTER TABLE cohort_metrics_daily_staging DROP PARTITION ?",
			args: []any{part},
		},
		{
			// Single table. The cohort key is a column, so there is nothing to join to.
			//
			// The inner GROUP BY is load-bearing. player_activity_daily is a
			// SummingMergeTree, so until parts merge a device-day exists as several
			// partial rows — and count() over those counts one returner more than once.
			// Collapsing to one row per device-day first makes the result exact
			// regardless of merge state, which is what lets this run at any time.
			name: "build cohort_metrics_daily",
			stmt: `
				INSERT INTO cohort_metrics_daily_staging
				SELECT
				    project_seq,
				    environment_seq,
				    cohort_date,
				    toUInt16(dateDiff('day', cohort_date, activity_date)) AS day_offset,
				    count()                        AS returned_players,
				    sum(d_sessions)                AS sessions,
				    sum(d_playtime_ms)             AS playtime_ms,
				    sum(d_revenue_micros)          AS revenue_micros,
				    sum(d_purchases)               AS purchases,
				    countIf(d_revenue_micros > 0)  AS payers,
				    now()                          AS computed_at
				FROM
				(
				    -- The inner aliases are prefixed because an output alias shadows the
				    -- source column of the same name, and the outer sum() would then
				    -- resolve to sum(sum(x)) and be rejected.
				    SELECT
				        project_seq, environment_seq, activity_date, device_id,
				        min(cohort_date)    AS cohort_date,
				        sum(sessions)       AS d_sessions,
				        sum(playtime_ms)    AS d_playtime_ms,
				        sum(revenue_micros) AS d_revenue_micros,
				        sum(purchases)      AS d_purchases
				    FROM player_activity_daily
				    WHERE toYYYYMM(activity_date) = ?
				    GROUP BY project_seq, environment_seq, activity_date, device_id
				)
				WHERE cohort_date != toDate(0) AND activity_date >= cohort_date
				GROUP BY project_seq, environment_seq, cohort_date, day_offset`,
			args: []any{part},
		},
		{
			name:    "swap cohort_metrics_daily",
			dest:    "cohort_metrics_daily",
			staging: "cohort_metrics_daily_staging",
		},
		{
			name: "clear breakdown staging",
			stmt: "ALTER TABLE cohort_metrics_breakdown_daily_staging DROP PARTITION ?",
			args: []any{part},
		},
		{
			// One dimension at a time (§29.13): four arms of a UNION ALL rather than a
			// cartesian product of the dimensions, so growth is additive.
			name: "build cohort_metrics_breakdown_daily",
			stmt: `
				INSERT INTO cohort_metrics_breakdown_daily_staging
				WITH collapsed AS
				(
				    SELECT
				        project_seq, environment_seq, activity_date, device_id,
				        min(cohort_date)      AS cohort_date,
				        any(install_platform) AS install_platform,
				        any(install_country)  AS install_country,
				        any(first_app_version) AS first_app_version,
				        sum(playtime_ms)      AS d_playtime_ms,
				        sum(revenue_micros)   AS d_revenue_micros,
				        sum(purchases)        AS d_purchases
				    FROM player_activity_daily
				    WHERE toYYYYMM(activity_date) = ?
				    GROUP BY project_seq, environment_seq, activity_date, device_id
				    HAVING cohort_date != toDate(0) AND activity_date >= cohort_date
				)
				SELECT
				    project_seq, environment_seq, dim_name, dim_value, cohort_date,
				    toUInt16(dateDiff('day', cohort_date, activity_date)) AS day_offset,
				    count()                       AS returned_players,
				    sum(d_playtime_ms)            AS playtime_ms,
				    sum(d_revenue_micros)         AS revenue_micros,
				    sum(d_purchases)              AS purchases,
				    countIf(d_revenue_micros > 0) AS payers
				FROM
				(
				    SELECT *, 'install_platform'   AS dim_name, install_platform   AS dim_value FROM collapsed
				    UNION ALL
				    SELECT *, 'install_country'    AS dim_name, install_country    AS dim_value FROM collapsed
				    UNION ALL
				    SELECT *, 'first_app_version'  AS dim_name, first_app_version  AS dim_value FROM collapsed
				)
				GROUP BY project_seq, environment_seq, dim_name, dim_value, cohort_date, day_offset`,
			args: []any{part},
		},
		{
			name:    "swap cohort_metrics_breakdown_daily",
			dest:    "cohort_metrics_breakdown_daily",
			staging: "cohort_metrics_breakdown_daily_staging",
		},
	}

	for _, s := range steps {
		if s.dest != "" {
			if err := SwapPartition(ctx, d.DB, s.dest, s.staging, part); err != nil {
				return fmt.Errorf("%s: %w", s.name, err)
			}
			continue
		}
		if _, err := d.DB.ExecContext(ctx, s.stmt, s.args...); err != nil {
			return fmt.Errorf("%s (partition %s): %w", s.name, part, err)
		}
	}
	return nil
}

// DeriveSizes rebuilds the denominators for one cohort month.
//
// Partitioned on the cohort month rather than the activity month, which is what lets the
// two rebuild on their own cadences without fighting (§29.9). The denominator is never
// frozen: freezing it while the numerator keeps absorbing late players is what produces
// retention above 100%.
//
// Two statements, because §29.9 asks for two kinds of denominator and getting the second
// wrong is the classic way a breakdown chart lies:
//
//  1. `__total__`, one row per cohort day — the denominator of an unbroken-down curve;
//  2. one row per (dimension, value) — the denominator of a broken-down curve, counted
//     **within** the value.
//
// The second is not optional and it is not derivable from the first. Dividing a
// breakdown numerator by the whole cohort's size understates every value, and reusing the
// whole cohort's size against each value multiplies the denominator by the number of
// values the moment anyone sums across them. Both produce a chart that looks plausible.
func (d *Deriver) DeriveSizes(ctx context.Context, cohortMonth Month, asOf time.Time) error {
	part := cohortMonth.Partition()

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE cohort_sizes_staging DROP PARTITION ?", part); err != nil {
		return fmt.Errorf("clear sizes staging: %w", err)
	}

	// player_lifetime_v is one table as far as this statement is concerned; the -Merge
	// happens inside the view.
	if _, err := d.DB.ExecContext(ctx, `
		INSERT INTO cohort_sizes_staging
		SELECT
		    project_seq,
		    environment_seq,
		    cohort_date,
		    '__total__'                                   AS dim_name,
		    ''                                            AS dim_value,
		    count()                                       AS cohort_size,
		    toUInt16(dateDiff('day', cohort_date, ?))     AS cohort_maturity_days,
		    now()                                         AS computed_at
		FROM player_lifetime_v
		WHERE toYYYYMM(cohort_date) = ?
		GROUP BY project_seq, environment_seq, cohort_date`, asOf, part); err != nil {
		return fmt.Errorf("build cohort_sizes: %w", err)
	}

	// The four sticky dimensions, one arm each (§29.13) — additive in the sum of the
	// dimension sizes rather than multiplicative in their product.
	//
	// Same source table as the total above, so a cohort's size and its composition can
	// never disagree about which devices are in it. The attributes come from
	// player_lifetime's argMin state, which is the arrival fact: a cohort's composition is
	// a property of the players who arrived, not of what they did afterwards.
	//
	// A GROUP BY materializes only the pairs that exist, which is §29.13's third cap —
	// on any given day only a handful of versions and perhaps eighty countries actually
	// produce installs.
	if _, err := d.DB.ExecContext(ctx, `
		INSERT INTO cohort_sizes_staging
		WITH arrivals AS
		(
		    SELECT
		        project_seq,
		        environment_seq,
		        cohort_date,
		        first_attrs.1      AS install_platform,
		        first_attrs.2      AS install_country,
		        first_attrs.3      AS first_app_version
		    FROM player_lifetime_v
		    WHERE toYYYYMM(cohort_date) = ?
		)
		SELECT
		    project_seq,
		    environment_seq,
		    cohort_date,
		    dim_name,
		    dim_value,
		    count()                                   AS cohort_size,
		    toUInt16(dateDiff('day', cohort_date, ?)) AS cohort_maturity_days,
		    now()                                     AS computed_at
		FROM
		(
		    SELECT *, 'install_platform'   AS dim_name, install_platform   AS dim_value FROM arrivals
		    UNION ALL
		    SELECT *, 'install_country'    AS dim_name, install_country    AS dim_value FROM arrivals
		    UNION ALL
		    SELECT *, 'first_app_version'  AS dim_name, first_app_version  AS dim_value FROM arrivals
		)
		GROUP BY project_seq, environment_seq, cohort_date, dim_name, dim_value`,
		part, asOf); err != nil {
		return fmt.Errorf("build cohort_sizes breakdown: %w", err)
	}

	// One swap for both statements. The partition is replaced whole, so the totals and the
	// per-dimension sizes become visible in the same instant — §29.9's rule that the
	// numerator and denominator are published from one snapshot applies just as much to
	// the two halves of the denominator.
	if err := SwapPartition(ctx, d.DB, "cohort_sizes", "cohort_sizes_staging", part); err != nil {
		return fmt.Errorf("swap cohort_sizes: %w", err)
	}
	return nil
}

// RollingRetention reports, for one cohort, how many devices were last seen at each
// offset.
//
// Rolling retention at offset N is the suffix sum of this histogram. It cannot come from
// the per-offset counters in cohort_metrics_daily, because a device active on two days
// would be counted twice — it needs each device's *final* activity day, which is why
// player_lifetime carries max(last_seen) (§29.10).
func (d *Deriver) RollingRetention(ctx context.Context, projectSeq uint32, environmentSeq uint16, cohortDate time.Time) (map[uint16]uint64, error) {
	rows, err := d.DB.QueryContext(ctx, `
		SELECT
		    toUInt16(dateDiff('day', cohort_date, toDate(last_seen))) AS last_offset,
		    count() AS devices
		FROM player_lifetime_v
		WHERE project_seq = ? AND environment_seq = ? AND cohort_date = ?
		GROUP BY last_offset
		ORDER BY last_offset`, projectSeq, environmentSeq, cohortDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uint16]uint64{}
	for rows.Next() {
		var offset uint16
		var devices uint64
		if err := rows.Scan(&offset, &devices); err != nil {
			return nil, err
		}
		out[offset] = devices
	}
	return out, rows.Err()
}
