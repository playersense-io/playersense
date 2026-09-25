package query

import (
	"context"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// Realtime is the last minutes of a project, minute by minute, against the same minutes a day
// earlier: events by name from events_1m, players and revenue from activity_1m. Two statements over
// minute tiers, each reading both windows at once. Events are visible within about ten seconds,
// so the current minute is partly filled, and says how far.
func (e *Engine) Realtime(ctx context.Context, scope Scope, minutes int, now time.Time) (*schemas.RealtimeOut, error) {
	end := now.UTC().Truncate(time.Minute).Add(time.Minute)
	start := end.Add(-time.Duration(minutes) * time.Minute)
	yStart, yEnd := start.Add(-24*time.Hour), end.Add(-24*time.Hour)
	withheld := Suppressed{Threshold: e.k}

	out := &schemas.RealtimeOut{At: now.UTC(), Minutes: []schemas.RealtimeMinuteOut{}, Yesterday: []schemas.RealtimeMinuteOut{},
		Elapsed: now.UTC().Sub(end.Add(-time.Minute)).Seconds() / 60, SuppressionThreshold: e.k}
	today := map[time.Time]*schemas.RealtimeMinuteOut{}
	past := map[time.Time]*schemas.RealtimeMinuteOut{}
	for at := start; at.Before(end); at = at.Add(time.Minute) {
		today[at] = &schemas.RealtimeMinuteOut{At: at, Events: map[string]uint64{}}
		past[at.Add(-24*time.Hour)] = &schemas.RealtimeMinuteOut{At: at.Add(-24 * time.Hour), Events: map[string]uint64{}}
	}
	slot := func(at time.Time) *schemas.RealtimeMinuteOut {
		if m := today[at]; m != nil {
			return m
		}
		return past[at]
	}

	a := from(factActivity1m)
	a.column("minute", "m").
		column("sum(events)", "n").
		column("uniqCombinedMerge(14)(players)", "p").
		column("sum(revenue_micros)", "rev")
	scopeAnd(a, scope)
	a.and("((minute >= ? AND minute < ?) OR (minute >= ? AND minute < ?))",
		timestamp(start), timestamp(end), timestamp(yStart), timestamp(yEnd))
	a.groupOn("m")
	if err := e.read(ctx, a.statement(), func(r driver.Rows) error {
		var at time.Time
		var n, p uint64
		var rev int64
		if err := r.Scan(&at, &n, &p, &rev); err != nil {
			return err
		}
		if m := slot(at.UTC()); m != nil {
			m.Total, m.Players, m.RevenueMicros = n, shown(&withheld, p), rev
		}
		return nil
	}); err != nil {
		return nil, failed("activity_1m", err)
	}

	ev := from(factEvents1m)
	ev.column("minute", "m").column("toString(event_name)", "name").column("sum(events)", "n")
	scopeAnd(ev, scope)
	ev.and("((minute >= ? AND minute < ?) OR (minute >= ? AND minute < ?))",
		timestamp(start), timestamp(end), timestamp(yStart), timestamp(yEnd))
	ev.groupOn("m", "name").withLimit(e.limits.MaxRows)
	totals := map[string]uint64{}
	if err := e.read(ctx, ev.statement(), func(r driver.Rows) error {
		var at time.Time
		var name string
		var n uint64
		if err := r.Scan(&at, &name, &n); err != nil {
			return err
		}
		if m := slot(at.UTC()); m != nil {
			m.Events[name] += n
			if _, isToday := today[at.UTC()]; isToday {
				totals[name] += n
			}
		}
		return nil
	}); err != nil {
		return nil, failed("events_1m", err)
	}

	// The busiest names today by name; the rest of each minute is "other", so a game sending two
	// hundred event names draws twelve lines and one more.
	for name, n := range totals {
		out.Names = append(out.Names, schemas.RealtimeNameOut{Name: name, Events: n})
	}
	sort.Slice(out.Names, func(i, j int) bool {
		if out.Names[i].Events != out.Names[j].Events {
			return out.Names[i].Events > out.Names[j].Events
		}
		return out.Names[i].Name < out.Names[j].Name
	})
	if len(out.Names) > RealtimeNames {
		out.Names = out.Names[:RealtimeNames]
	}
	shownNames := map[string]bool{}
	for _, n := range out.Names {
		shownNames[n.Name] = true
	}
	fold := func(m *schemas.RealtimeMinuteOut) schemas.RealtimeMinuteOut {
		var other uint64
		for name, n := range m.Events {
			if !shownNames[name] {
				other += n
				delete(m.Events, name)
			}
		}
		if other > 0 {
			m.Events["other"] = other
		}
		return *m
	}
	for at := start; at.Before(end); at = at.Add(time.Minute) {
		out.Minutes = append(out.Minutes, fold(today[at]))
		out.Yesterday = append(out.Yesterday, fold(past[at.Add(-24*time.Hour)]))
	}
	return out, nil
}

// RealtimeNames is how many event names the realtime page draws; the rest are "other".
const RealtimeNames = 12
