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
			name: "swap cohort_metrics_daily",
			stmt: "ALTER TABLE cohort_metrics_daily REPLACE PARTITION ? FROM cohort_metrics_daily_staging",
			args: []any{part},
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
				        any(acquisition_source) AS acquisition_source,
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
				    UNION ALL
				    SELECT *, 'acquisition_source' AS dim_name, acquisition_source AS dim_value FROM collapsed
				)
				GROUP BY project_seq, environment_seq, dim_name, dim_value, cohort_date, day_offset`,
			args: []any{part},
		},
		{
			name: "swap cohort_metrics_breakdown_daily",
			stmt: "ALTER TABLE cohort_metrics_breakdown_daily REPLACE PARTITION ? FROM cohort_metrics_breakdown_daily_staging",
			args: []any{part},
		},
	}

	for _, s := range steps {
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

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE cohort_sizes REPLACE PARTITION ? FROM cohort_sizes_staging", part); err != nil {
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
