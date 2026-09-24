package rollup

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"playersense.dev/backend/internal/cohort"
)

// SessionLengthBounds is the ladder sessions_daily.length_bucket indexes, in milliseconds.
//
// Bucket 0 is shorter than the first bound and bucket len(SessionLengthBounds) is at least the
// last one, so there are thirteen. Stored as an index rather than a label because the ladder is
// the one thing both sides must agree on and a label would be a second copy of it.
var SessionLengthBounds = []uint64{
	30_000,     // 30 seconds
	60_000,     // 1 minute
	120_000,    // 2 minutes
	300_000,    // 5 minutes
	600_000,    // 10 minutes
	900_000,    // 15 minutes
	1_200_000,  // 20 minutes
	1_800_000,  // 30 minutes
	2_700_000,  // 45 minutes
	3_600_000,  // 1 hour
	7_200_000,  // 2 hours
	14_400_000, // 4 hours
}

// DeriveSessions rebuilds one month of sessions_daily and swaps it in.
//
// Each session is put back together from its per-day rows first, which is what makes it one
// session rather than one per block or one per side of midnight, then counted under the day it
// started. The read takes a day either side of the month for that reason: a session begun on the
// last evening of August has its later events under the 1st of September.
//
// A session is only counted once its day is over. Today's are left for tomorrow's pass, so a day
// that appears here is a whole day and its lengths are final; one that has not appeared yet is
// absent rather than short.
func (d *Deriver) DeriveSessions(ctx context.Context, m cohort.Month) error {
	part := m.Partition()
	first := time.Date(m.Year, m.Month, 1, 0, 0, 0, 0, time.UTC)
	from := first.AddDate(0, 0, -1).Format(time.DateOnly)
	to := first.AddDate(0, 1, 0).Format(time.DateOnly)

	if _, err := d.DB.ExecContext(ctx,
		"ALTER TABLE sessions_daily_staging DROP PARTITION ?", part); err != nil {
		return fmt.Errorf("clear sessions staging (partition %s): %w", part, err)
	}

	if _, err := d.DB.ExecContext(ctx, `
		INSERT INTO sessions_daily_staging
		SELECT
		    project_seq,
		    environment_seq,
		    toDate(s_started)                                        AS session_date,
		    s_platform                                               AS platform,
		    toUInt8(arrayCount(b -> s_duration_ms >= b, `+lengthLadder()+`)) AS length_bucket,
		    if(s_ended = 1, 'ended', 'timed_out')                    AS outcome,
		    count()                                                  AS sessions,
		    sum(s_duration_ms)                                       AS duration_ms,
		    sum(s_playtime_ms)                                       AS playtime_ms,
		    sum(s_events)                                            AS events,
		    sum(s_revenue_micros)                                    AS revenue_micros,
		    now()                                                    AS computed_at
		FROM
		(
		    SELECT
		        project_seq,
		        environment_seq,
		        device_id,
		        session_key,
		        min(started_at)                                                        AS s_started,
		        toUInt64(greatest(dateDiff('millisecond', min(started_at), max(ended_at)), 0)) AS s_duration_ms,
		        sum(playtime_ms)                                                       AS s_playtime_ms,
		        sum(events)                                                            AS s_events,
		        sum(revenue_micros)                                                    AS s_revenue_micros,
		        max(ended)                                                             AS s_ended,
		        any(platform)                                                          AS s_platform
		    FROM sessions
		    WHERE activity_date BETWEEN ? AND ?
		    GROUP BY project_seq, environment_seq, device_id, session_key
		    HAVING toYYYYMM(s_started) = ? AND toDate(s_started) < today()
		)
		GROUP BY project_seq, environment_seq, session_date, platform, length_bucket, outcome`,
		from, to, part); err != nil {
		return fmt.Errorf("build sessions_daily (partition %s): %w", part, err)
	}

	if err := cohort.SwapPartition(ctx, d.DB, "sessions_daily", "sessions_daily_staging", part); err != nil {
		return fmt.Errorf("swap sessions_daily: %w", err)
	}
	return nil
}

// lengthLadder renders SessionLengthBounds as a ClickHouse array literal.
//
// Written into the statement rather than bound, because it is a constant of this package and a
// bound array would make the statement's meaning depend on an argument nobody reads.
func lengthLadder() string {
	parts := make([]string, len(SessionLengthBounds))
	for i, b := range SessionLengthBounds {
		parts[i] = strconv.FormatUint(b, 10)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
