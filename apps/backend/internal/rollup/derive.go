// Package rollup holds the daily and monthly product metrics derivation (project.md §27,
// §38, §79.1).
//
// Separate from the cohort derivation because the two answer different questions. A cohort
// fact is keyed on when a player *arrived*; these are keyed on when something *happened*.
// §27 draws the same line and says which section a new metric belongs in.
//
// Every statement here reads exactly one table — player_activity_daily — for the reason §38
// gives about the billing meter: that table already holds one row per device per day, so
// the daily metrics are a projection of it. Deriving them from events_raw instead would
// give the dashboard a second, independent definition of "active player", and the first
// month the two disagreed nobody could establish which was right.
package rollup

import (
	"context"
	"database/sql"
	"fmt"

	"playersense.dev/backend/internal/cohort"
)

// Deriver rebuilds the served metric tables.
type Deriver struct {
	DB *sql.DB
}

// collapsed is the inner read every statement here starts from.
//
// player_activity_daily is a SummingMergeTree, so until parts merge a device-day exists as
// several partial rows. Counting over those counts one active player more than once, and
// the error is upward and silent — DAU would simply drift. Collapsing to one row per
// device-day first is what makes these counts exact regardless of merge state, and exact is
// the whole reason this tier exists beside 00007's approximate one (§29.14).
const collapsed = `
	SELECT
	    project_seq, environment_seq, activity_date, device_id,
	    min(cohort_date)        AS cohort_date,
	    any(install_platform)   AS install_platform,
	    any(install_country)    AS install_country,
	    any(first_app_version)  AS first_app_version,
	    any(acquisition_source) AS acquisition_source,
	    sum(sessions)           AS d_sessions,
	    sum(playtime_ms)        AS d_playtime_ms,
	    sum(events)             AS d_events,
	    sum(revenue_micros)     AS d_revenue_micros,
	    sum(purchases)          AS d_purchases
	FROM player_activity_daily
	WHERE toYYYYMM(activity_date) = ?
	GROUP BY project_seq, environment_seq, activity_date, device_id`

// DeriveMonth rebuilds one whole month of daily metrics, their breakdowns, and the month's
// own totals, then swaps all three in.
//
// Whole months and a partition swap, like the cohort derivation and for the same reason:
// rebuilding in full is what makes REPLACE PARTITION safe and the job idempotent, and it is
// what absorbs a late event inside the month without double counting.
func (d *Deriver) DeriveMonth(ctx context.Context, m cohort.Month) error {
	part := m.Partition()

	steps := []struct {
		name string
		stmt string
		args []any

		// dest and staging mark a step as a partition swap, which goes through
		// cohort.SwapPartition rather than being executed directly — an empty staging
		// partition must be skipped rather than dropping what it would have replaced.
		dest    string
		staging string
	}{
		{
			name: "clear daily staging",
			stmt: "ALTER TABLE metrics_daily_staging DROP PARTITION ?",
			args: []any{part},
		},
		{
			// new + returning = active, exactly, by construction rather than by two
			// counts that have to agree.
			//
			// A device whose cohort_date is unknown — the null §65 leaves behind when
			// Valkey could not answer at write time — therefore lands in `returning`. That
			// is the wrong label for some of them, and it is the containable error: the
			// alternative is a third bucket that every chart has to explain, or two counts
			// that silently fail to sum to the total.
			name: "build metrics_daily",
			stmt: `
				INSERT INTO metrics_daily_staging
				SELECT
				    project_seq,
				    environment_seq,
				    activity_date,
				    count()                                       AS active_players,
				    countIf(cohort_date = activity_date)          AS new_players,
				    count() - countIf(cohort_date = activity_date) AS returning_players,
				    sum(d_sessions)                               AS sessions,
				    sum(d_playtime_ms)                            AS playtime_ms,
				    sum(d_events)                                 AS events,
				    sum(d_revenue_micros)                         AS revenue_micros,
				    sum(d_purchases)                              AS purchases,
				    countIf(d_revenue_micros > 0)                 AS payers,
				    now()                                         AS computed_at
				FROM (` + collapsed + `)
				GROUP BY project_seq, environment_seq, activity_date`,
			args: []any{part},
		},
		{
			name:    "swap metrics_daily",
			dest:    "metrics_daily",
			staging: "metrics_daily_staging",
		},
		{
			name: "clear breakdown staging",
			stmt: "ALTER TABLE metrics_breakdown_daily_staging DROP PARTITION ?",
			args: []any{part},
		},
		{
			// Four arms rather than a cartesian product (§29.13): additive in the sum of
			// the dimension sizes instead of multiplicative in their product.
			name: "build metrics_breakdown_daily",
			stmt: `
				INSERT INTO metrics_breakdown_daily_staging
				WITH days AS (` + collapsed + `)
				SELECT
				    project_seq,
				    environment_seq,
				    activity_date,
				    dim_name,
				    dim_value,
				    count()                                       AS active_players,
				    countIf(cohort_date = activity_date)          AS new_players,
				    count() - countIf(cohort_date = activity_date) AS returning_players,
				    sum(d_sessions)                               AS sessions,
				    sum(d_playtime_ms)                            AS playtime_ms,
				    sum(d_events)                                 AS events,
				    sum(d_revenue_micros)                         AS revenue_micros,
				    sum(d_purchases)                              AS purchases,
				    countIf(d_revenue_micros > 0)                 AS payers
				FROM
				(
				    SELECT *, 'install_platform'   AS dim_name, install_platform   AS dim_value FROM days
				    UNION ALL
				    SELECT *, 'install_country'    AS dim_name, install_country    AS dim_value FROM days
				    UNION ALL
				    SELECT *, 'first_app_version'  AS dim_name, first_app_version  AS dim_value FROM days
				    UNION ALL
				    SELECT *, 'acquisition_source' AS dim_name, acquisition_source AS dim_value FROM days
				)
				GROUP BY project_seq, environment_seq, activity_date, dim_name, dim_value`,
			args: []any{part},
		},
		{
			name:    "swap metrics_breakdown_daily",
			dest:    "metrics_breakdown_daily",
			staging: "metrics_breakdown_daily_staging",
		},
		{
			name: "clear monthly staging",
			stmt: "ALTER TABLE metrics_monthly_staging DROP PARTITION ?",
			args: []any{part},
		},
		{
			// A month's active players is not the sum of its days. A player active on the
			// 3rd and the 9th is one monthly active player and two daily ones, so this
			// needs its own distinct count over the whole month — uniqExact, not uniq,
			// because §38 bills on it.
			name: "build metrics_monthly",
			stmt: `
				INSERT INTO metrics_monthly_staging
				SELECT
				    project_seq,
				    environment_seq,
				    toStartOfMonth(activity_date)                            AS month,
				    uniqExact(device_id)                                     AS active_players,
				    uniqExactIf(device_id, cohort_date = activity_date)      AS new_players,
				    sum(d_sessions)                                          AS sessions,
				    sum(d_playtime_ms)                                       AS playtime_ms,
				    sum(d_events)                                            AS events,
				    sum(d_revenue_micros)                                    AS revenue_micros,
				    sum(d_purchases)                                         AS purchases,
				    uniqExactIf(device_id, d_revenue_micros > 0)             AS payers,
				    now()                                                    AS computed_at
				FROM (` + collapsed + `)
				GROUP BY project_seq, environment_seq, month`,
			args: []any{part},
		},
		{
			name:    "swap metrics_monthly",
			dest:    "metrics_monthly",
			staging: "metrics_monthly_staging",
		},
	}

	for _, s := range steps {
		if s.dest != "" {
			if err := cohort.SwapPartition(ctx, d.DB, s.dest, s.staging, part); err != nil {
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
