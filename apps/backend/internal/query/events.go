package query

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// The Events page (00014): the catalogue, one event's page, and its latest occurrences.

// maxEventNames bounds the catalogue. ingest accepts any name matching its pattern, so a game
// that puts an id in its event names would otherwise make this page as long as its player list.
const maxEventNames = 5000

// maxHourlyDays bounds an hourly event series: fourteen days is 336 points, and events_1h's
// sketches are the heavy ones (00013's note), so an hourly read stays short.
const maxHourlyDays = 14

// EventCatalogue answers the Events page: every event name, its counts over the window and the
// window before it, its players, and how many property keys it carried.
//
// Three statements over three facts, joined in Go by event name: the daily counts, the
// catalogue of names (which holds the names that were quiet in the window too), and the
// property keys.
func (e *Engine) EventCatalogue(ctx context.Context, scope Scope, w Window) (*schemas.EventCatalogueOut, error) {
	prev := w.Previous()
	cut := date(w.From)

	type counts struct {
		events, prevEvents, players, prevPlayers uint64
	}
	byName := map[string]*counts{}

	b := from(factEventsDaily)
	b.column("toString(event_name)", "name").
		columnWith("sumIf(events, day >= ?)", "cur_events", cut).
		columnWith("sumIf(events, day < ?)", "prev_events", cut).
		columnWith("uniqCombinedMergeIf(14)(players, day >= ?)", "cur_players", cut).
		columnWith("uniqCombinedMergeIf(14)(players, day < ?)", "prev_players", cut)
	scopeAnd(b, scope)
	b.eq("dim_name", "__total__").between("day", date(prev.From), date(w.To))
	b.groupOn("name").withLimit(maxEventNames)
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var name string
		var c counts
		if err := rows.Scan(&name, &c.events, &c.prevEvents, &c.players, &c.prevPlayers); err != nil {
			return err
		}
		byName[name] = &c
		return nil
	}); err != nil {
		return nil, failed("events_daily", err)
	}

	names, err := e.eventNames(ctx, scope, "")
	if err != nil {
		return nil, err
	}

	properties := map[string]int{}
	p := from(factEventProperties)
	p.column("toString(event_name)", "name").column("toUInt64(uniqExact(property))", "properties")
	scopeAnd(p, scope)
	p.between("day", date(w.From), date(w.To))
	p.groupOn("name").withLimit(maxEventNames)
	if err := e.read(ctx, p.statement(), func(rows driver.Rows) error {
		var name string
		var n uint64
		if err := rows.Scan(&name, &n); err != nil {
			return err
		}
		properties[name] = int(n)
		return nil
	}); err != nil {
		return nil, failed("event_properties_daily", err)
	}

	out := &schemas.EventCatalogueOut{From: w.From, To: w.To, Approximate: true, SuppressionThreshold: e.k}
	withheld := Suppressed{Threshold: e.k}
	for name, meta := range names {
		c := byName[name]
		if c == nil {
			c = &counts{}
		}
		row := schemas.EventSummaryOut{
			Name: name, Type: meta.kind, FirstSeen: meta.first, LastSeen: meta.last,
			Events: c.events, PreviousEvents: c.prevEvents,
			Properties: properties[name],
		}
		row.Players = shown(&withheld, c.players)
		row.PreviousPlayers = shown(&withheld, c.prevPlayers)
		out.Events = append(out.Events, row)
	}
	slices.SortFunc(out.Events, func(a, b schemas.EventSummaryOut) int {
		if c := cmp.Compare(b.Events, a.Events); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	out.SuppressedCells = withheld.Cells
	return out, nil
}

// shown is a count of people, or nil when it is too thin to disclose. A zero is shown: it names
// nobody.
func shown(s *Suppressed, n uint64) *uint64 {
	if s.withheld(n) {
		return nil
	}
	return &n
}

type eventMeta struct {
	kind        string
	first, last time.Time
}

// eventNames reads the catalogue of names, or one name when only is set.
func (e *Engine) eventNames(ctx context.Context, scope Scope, only string) (map[string]eventMeta, error) {
	b := from(factEventNames)
	b.column("toString(event_name)", "name").
		column("anyLast(event_type)", "kind").
		column("min(first_seen)", "first").
		column("max(last_seen)", "last")
	scopeAnd(b, scope)
	if only != "" {
		b.eq("event_name", only)
	}
	b.groupOn("name").withLimit(maxEventNames)

	out := map[string]eventMeta{}
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var name string
		var m eventMeta
		if err := rows.Scan(&name, &m.kind, &m.first, &m.last); err != nil {
			return err
		}
		out[name] = m
		return nil
	}); err != nil {
		return nil, failed("event_names", err)
	}
	return out, nil
}

// EventDetail answers one event's page: its series against the window before, its split by the
// attributes it arrived with, and what its properties held.
func (e *Engine) EventDetail(ctx context.Context, scope Scope, name string, w Window, interval string) (*schemas.EventDetailOut, error) {
	if interval == "" {
		interval = "day"
	}
	if interval == "hour" && w.Days() > maxHourlyDays {
		return nil, refuse("interval",
			"an hourly series covers at most %d days, and this range covers %d; ask for a day "+
				"interval, or a shorter range", maxHourlyDays, w.Days())
	}

	names, err := e.eventNames(ctx, scope, name)
	if err != nil {
		return nil, err
	}
	meta, ok := names[name]
	if !ok {
		return nil, ErrNotFound
	}

	prev := w.Previous()
	out := &schemas.EventDetailOut{
		Name: name, Type: meta.kind, FirstSeen: meta.first, LastSeen: meta.last,
		From: w.From, To: w.To, Interval: interval,
		Breakdowns:           map[string][]schemas.EventSliceOut{},
		Approximate:          true,
		SuppressionThreshold: e.k,
	}
	withheld := Suppressed{Threshold: e.k}

	// The totals, both windows in one scan of the daily tier.
	cut := date(w.From)
	t := from(factEventsDaily)
	t.columnWith("sumIf(events, day >= ?)", "cur_events", cut).
		columnWith("sumIf(events, day < ?)", "prev_events", cut).
		columnWith("uniqCombinedMergeIf(14)(players, day >= ?)", "cur_players", cut).
		columnWith("uniqCombinedMergeIf(14)(players, day < ?)", "prev_players", cut)
	scopeAnd(t, scope)
	t.eq("dim_name", "__total__").eq("event_name", name).between("day", date(prev.From), date(w.To))
	var players, prevPlayers uint64
	if err := e.read(ctx, t.statement(), func(rows driver.Rows) error {
		return rows.Scan(&out.Totals.Events, &out.Totals.PreviousEvents, &players, &prevPlayers)
	}); err != nil {
		return nil, failed("events_daily", err)
	}
	out.Totals.Players = shown(&withheld, players)
	out.Totals.PreviousPlayers = shown(&withheld, prevPlayers)

	series, err := e.eventSeries(ctx, scope, name, w, interval)
	if err != nil {
		return nil, err
	}
	for i := range series {
		series[i].Players = shownPtr(&withheld, series[i].Players)
	}
	out.Series = series

	// The split, one arm per attribute, the busiest values first.
	s := from(factEventsDaily)
	s.column("toString(dim_name)", "dim").
		column("toString(dim_value)", "value").
		column("sum(events)", "n").
		column("uniqCombinedMerge(14)(players)", "players")
	scopeAnd(s, scope)
	s.and("dim_name != ?", "__total__").eq("event_name", name).between("day", date(w.From), date(w.To))
	s.group("dim", "value").order("dim", "n DESC", "value").withLimit(3000)
	if err := e.read(ctx, s.statement(), func(rows driver.Rows) error {
		var dim string
		var slice schemas.EventSliceOut
		var n uint64
		if err := rows.Scan(&dim, &slice.Value, &slice.Events, &n); err != nil {
			return err
		}
		if len(out.Breakdowns[dim]) >= 20 {
			return nil
		}
		slice.Players = shown(&withheld, n)
		out.Breakdowns[dim] = append(out.Breakdowns[dim], slice)
		return nil
	}); err != nil {
		return nil, failed("events_daily", err)
	}

	props, err := e.eventProperties(ctx, scope, name, w, out.Totals.Events)
	if err != nil {
		return nil, err
	}
	out.Properties = props
	out.SuppressedCells = withheld.Cells
	return out, nil
}

func shownPtr(s *Suppressed, n *uint64) *uint64 {
	if n == nil {
		return nil
	}
	return shown(s, *n)
}

// eventSeries is every bucket of the window, each beside the same bucket of the window before.
//
// Read as one range covering both windows, then laid out in Go, so a bucket nobody sent the
// event in is a zero on the chart rather than a gap.
func (e *Engine) eventSeries(ctx context.Context, scope Scope, name string, w Window, interval string) ([]schemas.EventBucketOut, error) {
	prev := w.Previous()
	step := 24 * time.Hour
	var b *builder
	if interval == "hour" {
		step = time.Hour
		b = from(factEvents1h)
		b.column("hour", "at").column("sum(events)", "n").column("uniqMerge(players)", "players")
		scopeAnd(b, scope)
		b.eq("event_name", name).gte("hour", timestamp(prev.From)).lt("hour", timestamp(w.end()))
	} else {
		b = from(factEventsDaily)
		b.column("toDateTime(day, 'UTC')", "at").column("sum(events)", "n").
			column("uniqCombinedMerge(14)(players)", "players")
		scopeAnd(b, scope)
		b.eq("dim_name", "__total__").eq("event_name", name).between("day", date(prev.From), date(w.To))
	}
	b.groupOn("at").withLimit(e.limits.MaxRows)

	type point struct{ events, players uint64 }
	points := map[time.Time]point{}
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var at time.Time
		var p point
		if err := rows.Scan(&at, &p.events, &p.players); err != nil {
			return err
		}
		points[at.UTC()] = p
		return nil
	}); err != nil {
		return nil, failed(string(b.fact), err)
	}

	span := w.From.Sub(prev.From)
	var out []schemas.EventBucketOut
	for at := w.From; at.Before(w.end()); at = at.Add(step) {
		cur, before := points[at], points[at.Add(-span)]
		players := cur.players
		out = append(out, schemas.EventBucketOut{
			At: at, Events: cur.events, PreviousEvents: before.events, Players: &players,
		})
	}
	return out, nil
}

// eventProperties reads what each property held over the window.
func (e *Engine) eventProperties(ctx context.Context, scope Scope, name string, w Window, occurrences uint64) ([]schemas.EventPropertyOut, error) {
	b := from(factEventProperties)
	b.column("toString(property)", "key").
		column("toString(value_type)", "type").
		column("sum(occurrences)", "n").
		column("uniqCombinedMerge(12)(distinct_values)", "distinct").
		// Values and their counts as two arrays, because the driver reads an array of plain
		// values and not an array of tuples.
		column("arrayMap(t -> t.1, approx_top_kMerge(10)(top_values))", "top_keys").
		column("arrayMap(t -> t.2, approx_top_kMerge(10)(top_values))", "top_counts")
	scopeAnd(b, scope)
	b.eq("event_name", name).between("day", date(w.From), date(w.To))
	b.group("key", "type").order("n DESC", "key").withLimit(200)

	var out []schemas.EventPropertyOut
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var p schemas.EventPropertyOut
		var keys []string
		var counts []uint64
		if err := rows.Scan(&p.Key, &p.Type, &p.Occurrences, &p.Distinct, &keys, &counts); err != nil {
			return err
		}
		if occurrences > 0 {
			p.Fill = min(1, float64(p.Occurrences)/float64(occurrences))
		}
		p.Top = make([]schemas.TopValueOut, 0, len(keys))
		for i, k := range keys {
			if i >= len(counts) {
				break
			}
			share := 0.0
			if p.Occurrences > 0 {
				share = float64(counts[i]) / float64(p.Occurrences)
			}
			p.Top = append(p.Top, schemas.TopValueOut{Value: k, Count: counts[i], Share: share})
		}
		out = append(out, p)
		return nil
	}); err != nil {
		return nil, failed("event_properties_daily", err)
	}
	return out, nil
}

// EventOccurrences reads an event's newest occurrences off events_raw (§26: raw for the ad hoc).
//
// The last day first, and the last seven only if that did not fill the page. The sort key is
// (org, project, environment, event_date, event_name, ...), so both reads touch exactly this
// event's rows on those days and nothing else; the first is the common case and reads a day.
func (e *Engine) EventOccurrences(ctx context.Context, scope Scope, name string, limit int, now time.Time) (*schemas.EventOccurrencesOut, error) {
	if err := refuseKids(scope); err != nil {
		return nil, err
	}
	today := now.UTC().Truncate(24 * time.Hour)
	out := &schemas.EventOccurrencesOut{}
	for _, days := range []int{1, 7} {
		b := rawOccurrences(scope)
		b.gte("event_date", date(today.AddDate(0, 0, -days))).eq("event_name", name)
		b.order("event_time DESC").withLimit(limit)
		found, err := e.occurrences(ctx, b)
		if err != nil {
			return nil, err
		}
		out.Occurrences = found
		if len(found) >= limit {
			break
		}
	}
	return out, nil
}

// rawOccurrences starts a read of whole events, scoped.
func rawOccurrences(scope Scope) *builder {
	b := from(factRaw)
	b.column("toString(event_id)", "event_id").
		column("toString(event_name)", "name").
		column("event_time", "at").
		column("ingest_time", "received").
		column("toString(install_id)", "install").
		column("ifNull(player_id, '')", "player").
		column("session_id", "session").
		column("toString(platform)", "platform").
		column("toString(app_version)", "version").
		column("toString(country_code)", "country").
		column("revenue_micros", "revenue").
		column("ifNull(currency, '')", "cur").
		column("props_string", "ps").
		column("props_int", "pi").
		column("props_float", "pf").
		column("props_bool", "pb")
	rawScope(b, scope)
	return b
}

func (e *Engine) occurrences(ctx context.Context, b *builder) ([]schemas.EventOccurrenceOut, error) {
	var out []schemas.EventOccurrenceOut
	if err := e.readRaw(ctx, b.statement(), func(rows driver.Rows) error {
		var o schemas.EventOccurrenceOut
		var ps map[string]string
		var pi map[string]int64
		var pf map[string]float64
		var pb map[string]uint8
		if err := rows.Scan(&o.EventID, &o.Name, &o.At, &o.ReceivedAt, &o.InstallID, &o.PlayerID,
			&o.SessionID, &o.Platform, &o.AppVersion, &o.Country, &o.Revenue, &o.Currency,
			&ps, &pi, &pf, &pb); err != nil {
			return err
		}
		o.Properties = properties(ps, pi, pf, pb)
		out = append(out, o)
		return nil
	}); err != nil {
		return nil, failed("events_raw", err)
	}
	return out, nil
}

// properties puts the four typed maps back into the one object the game sent.
func properties(ps map[string]string, pi map[string]int64, pf map[string]float64, pb map[string]uint8) map[string]any {
	n := len(ps) + len(pi) + len(pf) + len(pb)
	if n == 0 {
		return nil
	}
	out := make(map[string]any, n)
	for k, v := range ps {
		out[k] = v
	}
	for k, v := range pi {
		out[k] = v
	}
	for k, v := range pf {
		out[k] = v
	}
	for k, v := range pb {
		out[k] = v != 0
	}
	return out
}
