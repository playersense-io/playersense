package rollup

import (
	"context"
	"fmt"
	"time"

	"playersense.dev/backend/internal/cohort"
)

// LapseDays is how long a player may be away and still be continuing rather than reactivated.
//
// Seven, because it is where the Players page draws "lapsed": a player the page would have shown
// as lapsed yesterday and is active today has been reactivated, and the two readings must agree.
const LapseDays = 7

// lookbackDays is how far before the month the job reads to find each returner's previous visit.
// A player whose previous visit is further back than this is reactivated whichever way the gap is
// measured, so reading more would change no count.
const lookbackDays = 30

// DeriveLifecycle rebuilds one month of lifecycle_daily and swaps it in.
//
// Each device's active days are gathered into one sorted list, then walked in pairs: a day and
// the one before it. New is the device's arrival day, continuing is a gap of at most LapseDays,
// and anything longer, or no earlier day in the window, is reactivated. The three are counted
// from the same rows, so they sum to the day's active players exactly.
func (d *Deriver) DeriveLifecycle(ctx context.Context, m cohort.Month) error {
	part := m.Partition()
	first := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC)
	from := first.AddDate(0, 0, -lookbackDays).Format(time.DateOnly)
	to := first.AddDate(0, 1, -1).Format(time.DateOnly)

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE lifecycle_daily_staging DROP PARTITION ?", part); err != nil {
		return fmt.Errorf("clear lifecycle staging (partition %s): %w", part, err)
	}

	if _, err := d.DB.ExecContext(ctx, `
		INSERT INTO lifecycle_daily_staging
		SELECT
		    project_seq,
		    environment_seq,
		    day                                                                  AS activity_date,
		    count()                                                              AS active_players,
		    countIf(day = arrived)                                               AS new_players,
		    countIf(day != arrived AND previous != toDate(0)
		            AND dateDiff('day', previous, day) <= ?)                     AS continuing_players,
		    countIf(day != arrived AND (previous = toDate(0)
		            OR dateDiff('day', previous, day) > ?))                      AS reactivated_players,
		    now()                                                                AS computed_at
		FROM
		(
		    SELECT
		        project_seq,
		        environment_seq,
		        device_id,
		        min(cohort_date)                         AS arrived,
		        arraySort(groupUniqArray(activity_date)) AS days
		    FROM player_activity_daily
		    WHERE activity_date BETWEEN ? AND ?
		    GROUP BY project_seq, environment_seq, device_id
		)
		ARRAY JOIN
		    days AS day,
		    arrayPushFront(arrayPopBack(days), toDate(0)) AS previous
		WHERE toYYYYMM(day) = ?
		GROUP BY project_seq, environment_seq, activity_date`,
		LapseDays, LapseDays, from, to, part); err != nil {
		return fmt.Errorf("build lifecycle_daily (partition %s): %w", part, err)
	}

	if err := cohort.SwapPartition(ctx, d.DB, "lifecycle_daily", "lifecycle_daily_staging", part); err != nil {
		return fmt.Errorf("swap lifecycle_daily: %w", err)
	}
	return nil
}
