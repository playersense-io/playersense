package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// impactMetrics is how each LiveOps metric is read from a day's totals. Retention is not a day's
// figure, so it is left out rather than drawn from something else.
var impactMetrics = map[string]func(query.DayTotals) (float64, bool){
	"players":             func(d query.DayTotals) (float64, bool) { return float64(d.Players), true },
	"new_players":         func(d query.DayTotals) (float64, bool) { return float64(d.NewPlayers), true },
	"revenue":             func(d query.DayTotals) (float64, bool) { return float64(d.Revenue) / 1e6, true },
	"arpdau":              perPlayer(func(d query.DayTotals) float64 { return float64(d.Revenue) / 1e6 }),
	"payer_conversion":    perPlayer(func(d query.DayTotals) float64 { return float64(d.Payers) }),
	"sessions_per_player": perPlayer(func(d query.DayTotals) float64 { return float64(d.Sessions) }),
	"playtime_minutes":    perPlayer(func(d query.DayTotals) float64 { return float64(d.PlaytimeMs) / 60000 }),
}

func perPlayer(of func(query.DayTotals) float64) func(query.DayTotals) (float64, bool) {
	return func(d query.DayTotals) (float64, bool) {
		if d.Players == 0 {
			return 0, false
		}
		return of(d) / float64(d.Players), true
	}
}

// EventImpact reads what one of a LiveOps event's windows did to the project's days.
func EventImpact(ctx *muzak.Context, in schemas.EventImpactParams) (schemas.EventImpactOut, error) {
	row, err := findLiveOpsEvent(ctx, in.Key)
	if err != nil {
		return schemas.EventImpactOut{}, err
	}
	engine, scope, _, err := explore(ctx, in.Environment, schemas.AnalyticsRange{})
	if err != nil {
		return schemas.EventImpactOut{}, err
	}
	now := time.Now().UTC()
	windows := row.Schedule.Occurrences(now.AddDate(-1, 0, 0), now)
	out := schemas.EventImpactOut{Event: row.Key, Method: "baseline", SuppressionThreshold: engine.Threshold(),
		Occurrences: []schemas.WindowOut{}, Daily: map[string]schemas.ImpactSeriesOut{},
		Metrics: map[string]schemas.MetricImpactOut{}, Days: []time.Time{}}
	for i := len(windows) - 1; i >= 0 && len(out.Occurrences) < 10; i-- {
		out.Occurrences = append(out.Occurrences, schemas.WindowOut{Start: windows[i].Start, End: windows[i].End})
	}
	if len(out.Occurrences) == 0 {
		return out, muzak.NotFound("this event has not been on in the last year").WithCode("never_on")
	}
	chosen := out.Occurrences[0]
	if in.At != "" {
		at, err := time.Parse(time.RFC3339, in.At)
		if err != nil {
			return out, muzak.UnprocessableEntity("at is not an RFC 3339 time")
		}
		found := false
		for _, w := range out.Occurrences {
			if w.Start.Equal(at) {
				chosen, found = w, true
			}
		}
		if !found {
			return out, muzak.NotFound("the event has no window starting then").WithCode("no_such_window")
		}
	}
	out.Occurrence = chosen
	out.Live = now.Before(chosen.End)

	day := func(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }
	start, end := day(chosen.Start), day(chosen.End.Add(-time.Nanosecond))
	baselineFrom := start.AddDate(0, 0, -28)
	shown := start.AddDate(0, 0, -14)
	last := end.AddDate(0, 0, 7)
	if today := day(now); last.After(today) {
		last = today
	}
	totals, err := engine.DailyTotals(ctx.Context(), scope, baselineFrom, last)
	if err != nil {
		return out, exploreFailed(err, "daily figures")
	}
	byDay := map[time.Time]query.DayTotals{}
	for _, d := range totals {
		byDay[day(d.Day)] = d
		if d.ComputedAt.After(out.ComputedAt) {
			out.ComputedAt = d.ComputedAt
		}
	}
	for d := shown; !d.After(last); d = d.AddDate(0, 0, 1) {
		out.Days = append(out.Days, d)
	}
	out.Daily, out.Metrics = compareWindow(byDay, out.Days, start, end, uint64(engine.Threshold()))
	return out, nil
}

// compareWindow sets each shown day's figures against what the same weekday of the four weeks
// before the window said, and the window's mean against the mean expected. A day under the
// suppression threshold, or not built, is null and left out of both means.
func compareWindow(byDay map[time.Time]query.DayTotals, days []time.Time, start, end time.Time, k uint64,
) (map[string]schemas.ImpactSeriesOut, map[string]schemas.MetricImpactOut) {
	daily := map[string]schemas.ImpactSeriesOut{}
	metrics := map[string]schemas.MetricImpactOut{}
	for metric, read := range impactMetrics {
		series := schemas.ImpactSeriesOut{Actual: make([]*float64, len(days)), Expected: make([]*float64, len(days))}
		var inSum, exSum float64
		n := 0
		for i, d := range days {
			if t, found := byDay[d]; found && t.Players >= k {
				if v, ok := read(t); ok {
					series.Actual[i] = &v
				}
			}
			// The same weekday in each of the four weeks before the window: the latest such day
			// before it starts, and the three before that.
			offset := (int(start.Weekday()) - int(d.Weekday()) + 7) % 7
			if offset == 0 {
				offset = 7
			}
			nearest := start.AddDate(0, 0, -offset)
			var sum float64
			found := 0
			for w := range 4 {
				if t, ok := byDay[nearest.AddDate(0, 0, -7*w)]; ok && t.Players >= k {
					if v, ok := read(t); ok {
						sum += v
						found++
					}
				}
			}
			if found > 0 {
				e := sum / float64(found)
				series.Expected[i] = &e
			}
			if !d.Before(start) && !d.After(end) && series.Actual[i] != nil && series.Expected[i] != nil {
				inSum += *series.Actual[i]
				exSum += *series.Expected[i]
				n++
			}
		}
		daily[metric] = series
		summary := schemas.MetricImpactOut{}
		if n > 0 {
			a, e := inSum/float64(n), exSum/float64(n)
			summary.Actual, summary.Expected = &a, &e
			if e != 0 {
				lift := a/e - 1
				summary.Lift = &lift
			}
		}
		metrics[metric] = summary
	}
	return daily, metrics
}
