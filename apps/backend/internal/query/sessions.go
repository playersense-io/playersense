package query

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/rollup"
	"playersense.dev/backend/internal/schemas"
)

// The Sessions page (00015): complete sessions by day from sessions_daily, session starts by
// hour from activity_1h, and the newest sessions from sessions itself.

// openFor is how long after its last event a session still counts as going on. The SDK closes a
// session after thirty seconds in the background (SDK protocol §4); a foreground session can sit
// quiet far longer, and thirty minutes is where a live list stops calling it live.
const openFor = 30 * time.Minute

// Sessions answers the Sessions page.
func (e *Engine) Sessions(ctx context.Context, scope Scope, w Window, tz *time.Location) (*schemas.SessionsOut, error) {
	prev := w.Previous()
	out := &schemas.SessionsOut{From: w.From, To: w.To, Timezone: tz.String()}

	// Each day of both windows, laid out below so a quiet day is a zero rather than a gap.
	type dayRow struct{ sessions, duration, playtime, events uint64 }
	days := map[time.Time]dayRow{}
	d := from(factSessionsDaily)
	d.column("toDateTime(session_date, 'UTC')", "d").
		column("sum(sessions)", "n").
		column("sum(duration_ms)", "duration").
		column("sum(playtime_ms)", "playtime").
		column("sum(events)", "ev")
	scopeAnd(d, scope)
	d.between("session_date", date(prev.From), date(w.To))
	d.groupOn("d").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, d.statement(), func(rows driver.Rows) error {
		var at time.Time
		var r dayRow
		if err := rows.Scan(&at, &r.sessions, &r.duration, &r.playtime, &r.events); err != nil {
			return err
		}
		days[at.UTC()] = r
		return nil
	}); err != nil {
		return nil, failed("sessions_daily", err)
	}

	actives, err := e.dailyActives(ctx, scope, w)
	if err != nil {
		return nil, err
	}

	span := w.From.Sub(prev.From)
	for at := w.From; !at.After(w.To); at = at.AddDate(0, 0, 1) {
		cur, before := days[at], days[at.Add(-span)]
		out.Days = append(out.Days, schemas.SessionDayOut{
			Day: at, Sessions: cur.sessions, DurationMs: cur.duration, PlaytimeMs: cur.playtime,
			ActivePlayers:    actives[at],
			PreviousSessions: before.sessions, PreviousDurationMs: before.duration,
		})
		out.Totals.Sessions += cur.sessions
		out.Totals.DurationMs += cur.duration
		out.Totals.PlaytimeMs += cur.playtime
		out.Totals.Events += cur.events
		out.Totals.PlayerDays += actives[at]
		out.Totals.PreviousSessions += before.sessions
		out.Totals.PreviousDurationMs += before.duration
	}

	// The ladder, whole, with every rung present.
	counts := make([]uint64, len(rollup.SessionLengthBounds)+1)
	l := from(factSessionsDaily)
	l.column("length_bucket", "rung").column("sum(sessions)", "n")
	scopeAnd(l, scope)
	l.between("session_date", date(w.From), date(w.To))
	l.groupOn("rung")
	if err := e.read(ctx, l.statement(), func(rows driver.Rows) error {
		var rung uint8
		var n uint64
		if err := rows.Scan(&rung, &n); err != nil {
			return err
		}
		if int(rung) < len(counts) {
			counts[rung] = n
		}
		return nil
	}); err != nil {
		return nil, failed("sessions_daily", err)
	}
	var lower uint64
	for i, n := range counts {
		rung := schemas.SessionLengthOut{FromMs: lower, Sessions: n}
		if i < len(rollup.SessionLengthBounds) {
			rung.ToMs = rollup.SessionLengthBounds[i]
			lower = rung.ToMs
		}
		out.Lengths = append(out.Lengths, rung)
	}
	out.Totals.MedianMs = ladderQuantile(out.Lengths, 0.5)
	out.Totals.P90Ms = ladderQuantile(out.Lengths, 0.9)

	o := from(factSessionsDaily)
	o.column("toString(outcome)", "outcome").column("sum(sessions)", "n")
	scopeAnd(o, scope)
	o.between("session_date", date(w.From), date(w.To))
	o.groupOn("outcome")
	if err := e.read(ctx, o.statement(), func(rows driver.Rows) error {
		var r schemas.SessionOutcomeOut
		if err := rows.Scan(&r.Outcome, &r.Sessions); err != nil {
			return err
		}
		out.Outcomes = append(out.Outcomes, r)
		return nil
	}); err != nil {
		return nil, failed("sessions_daily", err)
	}

	playerDays, err := e.platformActives(ctx, scope, w)
	if err != nil {
		return nil, err
	}
	p := from(factSessionsDaily)
	p.column("toString(platform)", "platform").
		column("sum(sessions)", "n").
		column("sum(duration_ms)", "duration").
		column("sum(playtime_ms)", "playtime")
	scopeAnd(p, scope)
	p.between("session_date", date(w.From), date(w.To))
	p.group("platform").order("n DESC", "platform").withLimit(100)
	if err := e.read(ctx, p.statement(), func(rows driver.Rows) error {
		var r schemas.SessionPlatformOut
		if err := rows.Scan(&r.Platform, &r.Sessions, &r.DurationMs, &r.PlaytimeMs); err != nil {
			return err
		}
		r.PlayerDays = playerDays[r.Platform]
		out.Platforms = append(out.Platforms, r)
		return nil
	}); err != nil {
		return nil, failed("sessions_daily", err)
	}

	// Session starts by weekday and hour, from the hourly tier's session_start count, in the
	// caller's timezone. An hour-grain source, so a timezone with a half-hour offset lands each
	// hour in the nearest one.
	h := from(factActivity1h)
	h.columnWith("toDayOfWeek(hour, 0, ?)", "weekday", tz.String()).
		columnWith("toHour(toTimeZone(hour, ?))", "clock", tz.String()).
		column("sum(sessions)", "n")
	scopeAnd(h, scope)
	h.gte("hour", timestamp(w.From)).lt("hour", timestamp(w.end()))
	h.groupOn("weekday", "clock")
	if err := e.read(ctx, h.statement(), func(rows driver.Rows) error {
		var weekday, clock uint8
		var n uint64
		if err := rows.Scan(&weekday, &clock, &n); err != nil {
			return err
		}
		if weekday >= 1 && weekday <= 7 && clock < 24 {
			out.Heatmap[weekday-1][clock] += n
		}
		return nil
	}); err != nil {
		return nil, failed("activity_1h", err)
	}

	c := from(factSessionsDaily)
	c.column("max(session_date)", "through").column("max(computed_at)", "at")
	scopeAnd(c, scope)
	if err := e.read(ctx, c.statement(), func(rows driver.Rows) error {
		return rows.Scan(&out.CompleteThrough, &out.ComputedAt)
	}); err != nil {
		return nil, failed("sessions_daily", err)
	}
	return out, nil
}

// dailyActives reads each day's active players from metrics_daily, the exact count.
func (e *Engine) dailyActives(ctx context.Context, scope Scope, w Window) (map[time.Time]uint64, error) {
	out := map[time.Time]uint64{}
	b := from(factMetricsDaily)
	b.column("toDateTime(activity_date, 'UTC')", "d").column("sum(active_players)", "n")
	scopeAnd(b, scope)
	b.between("activity_date", date(w.From), date(w.To))
	b.groupOn("d").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var at time.Time
		var n uint64
		if err := rows.Scan(&at, &n); err != nil {
			return err
		}
		out[at.UTC()] = n
		return nil
	}); err != nil {
		return nil, failed("metrics_daily", err)
	}
	return out, nil
}

// platformActives sums each install platform's daily active players over the window.
func (e *Engine) platformActives(ctx context.Context, scope Scope, w Window) (map[string]uint64, error) {
	out := map[string]uint64{}
	b := from(factMetricsBreakdown)
	b.column("toString(dim_value)", "platform").column("sum(active_players)", "n")
	scopeAnd(b, scope)
	b.eq("dim_name", "install_platform").between("activity_date", date(w.From), date(w.To))
	b.groupOn("platform").withLimit(100)
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var platform string
		var n uint64
		if err := rows.Scan(&platform, &n); err != nil {
			return err
		}
		out[platform] = n
		return nil
	}); err != nil {
		return nil, failed("metrics_breakdown_daily", err)
	}
	return out, nil
}

// ladderQuantile reads a quantile off the length ladder, interpolating inside its rung. The last
// rung has no end, so a quantile that falls in it is reported at its start.
func ladderQuantile(ladder []schemas.SessionLengthOut, q float64) uint64 {
	var total uint64
	for _, r := range ladder {
		total += r.Sessions
	}
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var seen float64
	for _, r := range ladder {
		next := seen + float64(r.Sessions)
		if next >= target && r.Sessions > 0 {
			if r.ToMs == 0 {
				return r.FromMs
			}
			within := (target - seen) / float64(r.Sessions)
			return r.FromMs + uint64(within*float64(r.ToMs-r.FromMs))
		}
		seen = next
	}
	return ladder[len(ladder)-1].FromMs
}

// RecentSessions lists the newest sessions, put back together from their per-block rows.
//
// Today first, and yesterday only if today did not fill the page: sessions is keyed on the day, so
// each read is a couple of days' rows of one project, and grouping them by (device_id,
// session_key) is what turns a session's partial rows into one.
func (e *Engine) RecentSessions(ctx context.Context, scope Scope, limit int, now time.Time) (*schemas.RecentSessionsOut, error) {
	today := now.UTC().Truncate(24 * time.Hour)
	out := &schemas.RecentSessionsOut{}
	for _, back := range []int{0, 1} {
		b := from(factSessions)
		b.column("any(session_id)", "sid").
			column("toString(any(install_id))", "install").
			column("ifNull(anyLast(player_id), '')", "player").
			column("any(platform)", "p").
			column("any(app_version)", "v").
			column("any(country_code)", "c").
			column("min(started_at)", "started").
			column("max(ended_at)", "last").
			column("sum(events)", "ev").
			column("sum(playtime_ms)", "pt").
			column("sum(revenue_micros)", "rev").
			column("max(ended)", "closed")
		scopeAnd(b, scope)
		// A day further back than the page shows, so a session played across midnight is read
		// whole rather than from its second half; HAVING then keeps the ones still going on the
		// days asked for.
		b.gte("activity_date", date(today.AddDate(0, 0, -back-1)))
		b.group("device_id", "session_key").
			havingThat("last >= ?", timestamp(today.AddDate(0, 0, -back))).
			order("started DESC").withLimit(limit)

		var found []schemas.SessionOut
		if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
			var s schemas.SessionOut
			var closed uint8
			if err := rows.Scan(&s.SessionID, &s.InstallID, &s.PlayerID, &s.Platform, &s.AppVersion,
				&s.Country, &s.StartedAt, &s.LastEventAt, &s.Events, &s.PlaytimeMs, &s.RevenueMicros,
				&closed); err != nil {
				return err
			}
			if d := s.LastEventAt.Sub(s.StartedAt); d > 0 {
				s.DurationMs = uint64(d.Milliseconds())
			}
			switch {
			case closed == 1:
				s.Outcome = "ended"
			case now.Sub(s.LastEventAt) < openFor:
				s.Outcome = "open"
			default:
				s.Outcome = "timed_out"
			}
			found = append(found, s)
			return nil
		}); err != nil {
			return nil, failed("sessions", err)
		}
		out.Sessions = found
		if len(found) >= limit {
			break
		}
	}
	return out, nil
}
