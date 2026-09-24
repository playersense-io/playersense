package rollup

import (
	"context"
	"fmt"
	"time"

	"playersense.dev/backend/internal/cohort"
)

// ExperimentDays is how many days from exposure an arm's outcomes are summed over: the exposure
// day and the six after it.
const ExperimentDays = 7

// DeriveExperiments rebuilds one month of experiment_daily, by exposure month, and swaps it in.
//
// No join (§29.1). flag_assignments and player_activity_daily are read as one UNION ALL and
// grouped by device, so each device becomes one row holding the arms it joined and the days it
// played, and the seven days after each exposure are summed from its own row. The memory this
// takes is one aggregation row per device active in the window, the same as the daily metrics'
// own collapse, and ClickHouse spills it to disk rather than growing past its limit.
//
// The activity read runs a week past the month, so a player exposed on its last day has their
// seven days counted; until those days have happened the row says how many had (mature_days), and
// tomorrow's pass fills the rest in.
func (d *Deriver) DeriveExperiments(ctx context.Context, m cohort.Month) error {
	part := m.Partition()
	first := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC)
	from := first.Format(time.DateOnly)
	to := first.AddDate(0, 1, ExperimentDays-1).Format(time.DateOnly)
	last := fmt.Sprint(ExperimentDays - 1)

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE experiment_daily_staging DROP PARTITION ?", part); err != nil {
		return fmt.Errorf("clear experiment staging (partition %s): %w", part, err)
	}

	if _, err := d.DB.ExecContext(ctx, `
		INSERT INTO experiment_daily_staging
		SELECT
		    project_seq,
		    environment_seq,
		    x_flag                                                   AS flag,
		    x_exposed                                                AS exposure_date,
		    x_variant                                                AS variant,
		    x_published                                              AS published,
		    count()                                                  AS players,
		    countIf(x_d1)                                            AS retained_d1,
		    countIf(x_d7)                                            AS retained_d7,
		    countIf(x_rev > 0)                                       AS payers,
		    sum(x_rev)                                               AS revenue_micros,
		    sum(toFloat64(x_rev) * x_rev)                            AS revenue_sq,
		    sum(x_ses)                                               AS sessions,
		    sum(toFloat64(x_ses) * x_ses)                            AS sessions_sq,
		    sum(x_pt)                                                AS playtime_ms,
		    sum(toFloat64(x_pt) * x_pt)                              AS playtime_sq,
		    toUInt8(least(greatest(dateDiff('day', x_exposed, today()), 0), `+fmt.Sprint(ExperimentDays)+`)) AS mature_days,
		    now()                                                    AS computed_at
		FROM
		(
		    SELECT
		        project_seq,
		        environment_seq,
		        a.1                                                                     AS x_flag,
		        a.2                                                                     AS x_variant,
		        a.3                                                                     AS x_published,
		        a.4                                                                     AS x_exposed,
		        arraySum(arrayMap(d -> if(d.1 >= a.4 AND d.1 <= a.4 + `+last+`, d.2, 0), days)) AS x_rev,
		        arraySum(arrayMap(d -> if(d.1 >= a.4 AND d.1 <= a.4 + `+last+`, d.3, 0), days)) AS x_ses,
		        arraySum(arrayMap(d -> if(d.1 >= a.4 AND d.1 <= a.4 + `+last+`, d.4, 0), days)) AS x_pt,
		        arrayExists(d -> d.1 = a.4 + 1, days)                                   AS x_d1,
		        arrayExists(d -> d.1 = a.4 + 7, days)                                   AS x_d7
		    FROM
		    (
		        SELECT
		            project_seq,
		            environment_seq,
		            device_id,
		            groupArrayIf((u_flag, u_variant, u_published, u_exposed), u_kind = 1) AS assigned,
		            groupArrayIf((u_day, u_rev, u_ses, u_pt), u_kind = 2)                 AS days
		        FROM
		        (
		            SELECT
		                project_seq, environment_seq, device_id,
		                toUInt8(1)         AS u_kind,
		                toString(flag)     AS u_flag,
		                f_arm.1            AS u_variant,
		                f_arm.2            AS u_published,
		                toDate(f_first)    AS u_exposed,
		                toDate(0)          AS u_day,
		                toInt64(0)         AS u_rev,
		                toUInt64(0)        AS u_ses,
		                toUInt64(0)        AS u_pt
		            FROM
		            (
		                SELECT project_seq, environment_seq, flag, device_id,
		                       min(first_exposed)  AS f_first,
		                       argMinMerge(arm)    AS f_arm
		                FROM flag_assignments
		                GROUP BY project_seq, environment_seq, flag, device_id
		                HAVING toYYYYMM(f_first) = ?
		            )
		            UNION ALL
		            SELECT
		                project_seq, environment_seq, device_id,
		                toUInt8(2), '', '', toUInt8(0), toDate(0),
		                activity_date, sum(revenue_micros), sum(sessions), sum(playtime_ms)
		            FROM player_activity_daily
		            WHERE activity_date BETWEEN ? AND ?
		            GROUP BY project_seq, environment_seq, device_id, activity_date
		        )
		        GROUP BY project_seq, environment_seq, device_id
		        HAVING notEmpty(assigned)
		    )
		    ARRAY JOIN assigned AS a
		)
		GROUP BY project_seq, environment_seq, flag, exposure_date, variant, published`,
		part, from, to); err != nil {
		return fmt.Errorf("build experiment_daily (partition %s): %w", part, err)
	}

	if err := cohort.SwapPartition(ctx, d.DB, "experiment_daily", "experiment_daily_staging", part); err != nil {
		return fmt.Errorf("swap experiment_daily: %w", err)
	}
	return nil
}
