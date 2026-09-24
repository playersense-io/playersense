package query

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// Funnels (project.md §31): exploratory, read from events_raw, and bounded at every level.
//
// §31 allows raw reads for exploratory funnels and asks for saved and heavy ones to be cached, so
// every answer here goes to the explorer cache keyed on its canonical question. What keeps one
// read from taking the node:
//
//   - it reads only the step events: event_name is in events_raw's sort key after the tenant and
//     the day, so a funnel over three events reads those three events' rows and nothing else;
//   - it keeps at most funnelEventsPerPlayer of each player's events, so the state per player is
//     bounded whatever the player did;
//   - it decides how far each player got with ClickHouse's own windowFunnel, which runs natively
//     over the player's events, and measures each step's wait along the player's first attempt
//     with one vectorised arrayFirst per step; there is no join and no second read of anything;
//   - it runs under the raw limits, and ClickHouse spills its GROUP BY to disk from half of the
//     memory ceiling, so a large funnel is slow rather than fatal. A funnel over the limits is
//     refused with what to narrow (overLimit).
//
// The outer statement then counts everything in one aggregation, so the per-step counts, the
// medians, the trend, the time to convert and the split are one scan of the events.
//
// Measured on five million events of 6,667 players over thirty days: a four-step funnel reads the
// 1.57 million step events in about a tenth of a second and 16 MiB for the levels. A sequential
// arrayFold that tracked every chain's timings took twelve times as long, which is why the timings
// follow the first attempt instead.

// funnelEventsPerPlayer bounds the events kept per player (or per session). A player who did a
// step event more often than this in the range is judged on the first thousand.
const funnelEventsPerPlayer = 1000

// ConvertLadder is the time-to-convert ladder in seconds: under a minute, then 5 and 15 minutes,
// 1 and 3 hours, 1, 3 and 7 days, and longer.
var ConvertLadder = []int64{60, 300, 900, 3600, 3 * 3600, 86400, 3 * 86400, 7 * 86400}

// FunnelQuery is a validated steps funnel.
type FunnelQuery struct {
	From, To  time.Time
	Window    string
	Steps     []schemas.FunnelStep
	Breakdown string
}

// Funnel answers a steps funnel.
func (e *Engine) Funnel(ctx context.Context, scope Scope, q FunnelQuery, now time.Time) (*schemas.FunnelOut, error) {
	if err := checkFunnelRange(q.From, q.To, now); err != nil {
		return nil, err
	}
	window := q.Window
	if window == "" {
		window = "1d"
	}
	seconds := schemas.FunnelWindows[window]

	key := ""
	if e.cache != nil {
		parts := []string{"funnel", date(q.From), date(q.To), window, q.Breakdown}
		for _, s := range q.Steps {
			parts = append(parts, "s="+s.Event)
			for _, c := range s.Where {
				parts = append(parts, "w="+c.Property+"="+c.Value)
			}
		}
		key = e.cache.pageKey("funnel", scope, parts...)
		if hit, ok := cached[schemas.FunnelOut](ctx, e.cache, key); ok {
			return hit, nil
		}
	}

	n := len(q.Steps)
	end := q.To.AddDate(0, 0, 1)
	// The scan runs past the entry range by the window, so a chain begun on the last day can
	// finish; a session is at most a day, so a session window reads one day more.
	scanEnd := end.Add(time.Duration(max(seconds, 86400)) * time.Second)
	if today := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1); scanEnd.After(today) {
		scanEnd = today
	}

	// The mask of each occurrence: bit k is set when it matches step k+1. The first step only
	// counts inside the entry range, so a player cannot enter after it.
	var terms []string
	var maskArgs []any
	names := map[string]bool{}
	for k, step := range q.Steps {
		cond, args := stepCondition(step)
		if k == 0 {
			cond = "(" + cond + " AND event_time < ?)"
			args = append(args, timestamp(end))
		}
		terms = append(terms, fmt.Sprintf("if(%s, %d, 0)", cond, 1<<k))
		maskArgs = append(maskArgs, args...)
		names[step.Event] = true
	}
	mask := "toUInt16(" + strings.Join(terms, " + ") + ")"

	// Per session, the inner names are its own, because the statement over it takes the best
	// session per player under the names the outer one reads, and an alias that matched a column
	// would be read in its place.
	ts, lvl, evs, t := "ts", "lvl", "evs", "t"
	if window == "session" {
		ts, lvl, evs, t = "session_ts", "session_lvl", "session_evs", "session_t"
	}

	// How far the player got: windowFunnel over the step conditions, natively.
	w := strconv.FormatInt(seconds, 10)
	if seconds == 0 {
		w = "4294967295"
	}
	var conds []string
	var condArgs []any
	for k, step := range q.Steps {
		cond, args := stepCondition(step)
		if k == 0 {
			cond = "(" + cond + " AND event_time < ?)"
			args = append(args, timestamp(end))
		}
		conds = append(conds, cond)
		condArgs = append(condArgs, args...)
	}

	inner := from(factRaw)
	inner.column("device_id", "device")
	if window == "session" {
		inner.column("session_id", "sess")
	}
	inner.column(breakdownColumn(q.Breakdown), "dim").
		columnWith("windowFunnel("+w+")(toDateTime(event_time), "+strings.Join(conds, ", ")+")", lvl, condArgs...).
		// The player's step events in order, at most funnelEventsPerPlayer of them. The mask is
		// written twice, the value kept and the filter on it, so its arguments are bound twice.
		columnWith("arraySort(x -> x.1, groupArrayIf("+strconv.Itoa(funnelEventsPerPlayer)+")("+
			"(toUInt32(toUnixTimestamp(event_time)), "+mask+"), "+mask+" > 0))", evs,
			append(slices.Clone(maskArgs), maskArgs...)...)

	// When the first attempt reached each step: the first entry, then for each later step the
	// first occurrence after the step before and inside the window. Each is one vectorised
	// arrayFirst, so the whole chain is a pass per step rather than a lambda per event.
	times := make([]string, n)
	for k := range n {
		name := t + strconv.Itoa(k+1)
		times[k] = name
		if k == 0 {
			inner.column("arrayFirst(e -> bitTest(e.2, 0), "+evs+").1", name)
			continue
		}
		prev := t + strconv.Itoa(k)
		inner.column(fmt.Sprintf("if(%s = 0, toUInt32(0), arrayFirst(e -> bitTest(e.2, %d) AND e.1 >= %s AND e.1 <= %s1 + %s, %s).1)",
			prev, k, prev, t, w, evs), name)
	}
	inner.column("tuple("+strings.Join(times, ", ")+")", ts)
	rawScope(inner, scope)
	inner.between("event_date", date(q.From), date(scanEnd)).
		in("event_name", strs(slices.Sorted(maps.Keys(names))))
	if window == "session" {
		inner.and("session_id != ''")
		inner.group("device", "sess")
	} else {
		inner.group("device")
	}

	players := inner
	if window == "session" {
		// A player's best session: the one that got furthest.
		players = over(inner)
		players.column("argMax(session_ts, session_lvl)", "ts").
			column("max(session_lvl)", "lvl").
			column("any(dim)", "dim").
			group("device")
	}

	ladder := ints64(ConvertLadder)
	last := strconv.Itoa(n)
	reached := make([]string, n)
	for k := range n {
		reached[k] = fmt.Sprintf("countIf(lvl >= %d)", k+1)
	}
	waits := make([]string, n)
	waits[0] = "toFloat64(0)"
	for k := 1; k < n; k++ {
		waits[k] = fmt.Sprintf("quantileIf(0.5)(ts.%d - ts.%d, lvl >= %d AND ts.%d != 0)", k+1, k, k+1, k+1)
	}
	entered := "sumMapIf([toDate(toDateTime(ts.1))], [toUInt64(1)], lvl >= 1 AND ts.1 != 0)"
	converted := "sumMapIf([toDate(toDateTime(ts.1))], [toUInt64(1)], lvl = " + last + " AND ts.1 != 0)"
	took := "ts." + last + " - ts.1"
	done := "lvl = " + last + " AND ts." + last + " != 0"
	convert := "sumMapIf([toUInt8(arrayCount(b -> " + took + " >= b, " + ladder + "))], [toUInt64(1)], " + done + ")"

	outer := over(players)
	outer.column("dim", "d").
		column("["+strings.Join(reached, ", ")+"]", "reached").
		column("["+strings.Join(waits, ", ")+"]", "waits").
		column("("+entered+").1", "entered_days").
		column("("+entered+").2", "entered_n").
		column("("+converted+").1", "converted_days").
		column("("+converted+").2", "converted_n").
		column("("+convert+").1", "rungs").
		column("("+convert+").2", "rung_n").
		column("quantileIf(0.5)("+took+", "+done+")", "median")
	outer.groupOn("d").withLimit(1000)

	out := &schemas.FunnelOut{From: q.From, To: q.To, Window: window, SuppressionThreshold: e.k}
	withheld := Suppressed{Threshold: e.k}

	trend := map[time.Time][2]uint64{}
	rungs := make([]uint64, len(ConvertLadder)+1)
	var total []uint64
	var medianNum, medianDen float64
	var slicesOut []schemas.FunnelSeriesOut
	waitSum := make([]float64, n)
	waitWeight := make([]float64, n)

	if err := e.readRaw(ctx, outer.statement(), func(rows driver.Rows) error {
		var (
			dim                    string
			reachedN               []uint64
			waitsN                 []float64
			enteredDays, convDays  []time.Time
			enteredN, convN, rungN []uint64
			rungKeys               []uint8
			median                 float64
		)
		if err := rows.Scan(&dim, &reachedN, &waitsN, &enteredDays, &enteredN, &convDays, &convN,
			&rungKeys, &rungN, &median); err != nil {
			return err
		}
		if total == nil {
			total = make([]uint64, n)
		}
		for k := range n {
			total[k] += reachedN[k]
			if k > 0 && !math.IsNaN(waitsN[k]) {
				// Combined across splits weighted by who reached the step: the median of a
				// union is not the median of the medians, and this is the honest approximation
				// the split allows.
				waitSum[k] += waitsN[k] * float64(reachedN[k])
				waitWeight[k] += float64(reachedN[k])
			}
		}
		for i, d := range enteredDays {
			v := trend[d.UTC()]
			v[0] += enteredN[i]
			trend[d.UTC()] = v
		}
		for i, d := range convDays {
			v := trend[d.UTC()]
			v[1] += convN[i]
			trend[d.UTC()] = v
		}
		for i, r := range rungKeys {
			if int(r) < len(rungs) {
				rungs[r] += rungN[i]
			}
		}
		if converted := reachedN[n-1]; converted > 0 && !math.IsNaN(median) {
			medianNum += median * float64(converted)
			medianDen += float64(converted)
		}
		if q.Breakdown != "" {
			if withheld.withheld(reachedN[0]) {
				return nil
			}
			slicesOut = append(slicesOut, funnelSeries(dim, reachedN, waitsN, &withheld))
		}
		return nil
	}); err != nil {
		return nil, failed("events_raw", err)
	}

	if total == nil {
		total = make([]uint64, n)
	}
	out.Total = schemas.FunnelSeriesOut{Players: make([]*uint64, n), Medians: make([]*float64, n)}
	for k := range n {
		out.Total.Players[k] = shown(&withheld, total[k])
		if k > 0 && waitWeight[k] > 0 && out.Total.Players[k] != nil {
			m := waitSum[k] / waitWeight[k]
			out.Total.Medians[k] = &m
		}
	}
	slices.SortFunc(slicesOut, func(a, b schemas.FunnelSeriesOut) int {
		return compareReached(b, a)
	})
	out.Breakdown = slicesOut

	for at := q.From; !at.After(q.To); at = at.AddDate(0, 0, 1) {
		v := trend[at]
		out.Trend = append(out.Trend, schemas.FunnelDayOut{
			Day: at, Entered: shown(&withheld, v[0]), Converted: shown(&withheld, v[1]),
		})
	}
	var lower int64
	for i, count := range rungs {
		rung := schemas.FunnelBucketOut{FromSeconds: lower, Players: shown(&withheld, count)}
		if i < len(ConvertLadder) {
			rung.ToSeconds = ConvertLadder[i]
			lower = ConvertLadder[i]
		}
		out.TimeToConvert = append(out.TimeToConvert, rung)
	}
	if medianDen > 0 && out.Total.Players[n-1] != nil {
		m := medianNum / medianDen
		out.MedianToConvert = &m
	}
	out.SuppressedCells = withheld.Cells

	if key != "" {
		keep(ctx, e.cache, key, out, e.cache.rawTTL(q.To, now))
	}
	return out, nil
}

// stepCondition is one step as a predicate: the event, and each condition on its properties.
//
// A property is compared as text in whichever map it landed in, because a studio typing "12" into
// a funnel does not know that the SDK sent it as an integer. mapContains first, so a missing
// property never matches an empty value.
func stepCondition(step schemas.FunnelStep) (string, []any) {
	parts := []string{"event_name = ?"}
	args := []any{step.Event}
	for _, c := range step.Where {
		parts = append(parts, "("+
			"(mapContains(props_string, ?) AND props_string[?] = ?) OR "+
			"(mapContains(props_int, ?) AND toString(props_int[?]) = ?) OR "+
			"(mapContains(props_float, ?) AND toString(props_float[?]) = ?) OR "+
			"(mapContains(props_bool, ?) AND if(props_bool[?] = 0, 'false', 'true') = ?))")
		for range 4 {
			args = append(args, c.Property, c.Property, c.Value)
		}
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

// breakdownColumn is the arrival dimension a funnel is split by, as stamped on every event.
func breakdownColumn(dimension string) string {
	switch dimension {
	case "install_platform", "install_country", "first_app_version":
		return "toString(any(" + dimension + "))"
	default:
		return "''"
	}
}

func funnelSeries(dim string, reached []uint64, waits []float64, withheld *Suppressed) schemas.FunnelSeriesOut {
	s := schemas.FunnelSeriesOut{Value: dim, Players: make([]*uint64, len(reached)), Medians: make([]*float64, len(reached))}
	for k := range reached {
		s.Players[k] = shown(withheld, reached[k])
		if k > 0 && s.Players[k] != nil && !math.IsNaN(waits[k]) {
			m := waits[k]
			s.Medians[k] = &m
		}
	}
	return s
}

func compareReached(a, b schemas.FunnelSeriesOut) int {
	av, bv := uint64(0), uint64(0)
	if len(a.Players) > 0 && a.Players[0] != nil {
		av = *a.Players[0]
	}
	if len(b.Players) > 0 && b.Players[0] != nil {
		bv = *b.Players[0]
	}
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	}
	return strings.Compare(b.Value, a.Value)
}

// checkFunnelRange bounds the entry range (§32): at most MaxFunnelDays, and not in the future.
func checkFunnelRange(from, to, now time.Time) error {
	days := int(to.Sub(from).Hours()/24) + 1
	if days > schemas.MaxFunnelDays {
		return refuse("to",
			"a funnel reads its events one by one, so its entry range covers at most %d days, "+
				"and this one covers %d", schemas.MaxFunnelDays, days)
	}
	if from.After(now) {
		return refuse("from", "the entry range starts in the future")
	}
	return nil
}

// Progression answers a progression funnel: how far up a numeric property players climbed, how
// many tries each value took, and how long from the first value to the last.
//
// One scan: the inner statement decides each player (their highest value, when they first
// reached the first and the last value, and how many occurrences at each value), and the outer
// counts all of it in one row.
func (e *Engine) Progression(ctx context.Context, scope Scope, first, last time.Time, p schemas.FunnelProgression, now time.Time) (*schemas.ProgressionOut, error) {
	if err := checkFunnelRange(first, last, now); err != nil {
		return nil, err
	}

	key := ""
	if e.cache != nil {
		parts := []string{"progression", date(first), date(last), p.Event, p.Property,
			strconv.FormatInt(p.From, 10), strconv.FormatInt(p.To, 10)}
		for _, c := range p.Where {
			parts = append(parts, "w="+c.Property+"="+c.Value)
		}
		key = e.cache.pageKey("funnel", scope, parts...)
		if hit, ok := cached[schemas.ProgressionOut](ctx, e.cache, key); ok {
			return hit, nil
		}
	}

	cond, args := stepCondition(schemas.FunnelStep{Event: p.Event, Where: p.Where})
	value := "props_int[?]"

	inner := from(factRaw)
	inner.columnWith("max("+value+")", "top", p.Property).
		columnWith("minIf(toUInt32(toUnixTimestamp(event_time)), "+value+" = ?)", "t0", p.Property, p.From).
		columnWith("minIf(toUInt32(toUnixTimestamp(event_time)), "+value+" = ?)", "t1", p.Property, p.To).
		columnWith("sumMap(["+value+"], [toUInt64(1)])", "tries", p.Property)
	rawScope(inner, scope)
	inner.between("event_date", date(first), date(last)).
		and(cond, args...).
		and("mapContains(props_int, ?)", p.Property).
		and(value+" BETWEEN ? AND ?", p.Property, p.From, p.To)
	inner.group("device_id")

	outer := over(inner)
	outer.column("(sumMap([top], [toUInt64(1)])).1", "tops").
		column("(sumMap([top], [toUInt64(1)])).2", "top_n").
		column("(sumMap(tries.1, tries.2)).1", "vals").
		column("(sumMap(tries.1, tries.2)).2", "ev_n").
		column("(sumMap(tries.1, arrayWithConstant(length(tries.1), toUInt64(1)))).2", "pl_n").
		column("quantileIf(0.5)(t1 - t0, t0 > 0 AND t1 >= t0)", "median")

	var tops, values []int64
	var topN, events, players []uint64
	var median float64
	if err := e.readRaw(ctx, outer.statement(), func(rows driver.Rows) error {
		return rows.Scan(&tops, &topN, &values, &events, &players, &median)
	}); err != nil {
		return nil, failed("events_raw", err)
	}

	withheld := Suppressed{Threshold: e.k}
	out := &schemas.ProgressionOut{From: first, To: last, SuppressionThreshold: e.k}

	// Reached a value is having a top at or above it: a suffix sum over the tops.
	atTop := map[int64]uint64{}
	for i, t := range tops {
		atTop[t] = topN[i]
	}
	tries := map[int64][2]uint64{}
	for i, v := range values {
		tries[v] = [2]uint64{events[i], players[i]}
	}
	var reached uint64
	rows := make([]schemas.ProgressionRowOut, 0, p.To-p.From+1)
	for v := p.To; v >= p.From; v-- {
		reached += atTop[v]
		row := schemas.ProgressionRowOut{Value: v, Reached: shown(&withheld, reached)}
		if t := tries[v]; t[1] > 0 && !withheld.withheld(t[1]) {
			r := float64(t[0]) / float64(t[1])
			row.Tries = &r
		}
		rows = append(rows, row)
	}
	slices.Reverse(rows)
	out.Rows = rows
	if !math.IsNaN(median) && median > 0 {
		out.MedianToLast = &median
	}
	out.SuppressedCells = withheld.Cells

	if key != "" {
		keep(ctx, e.cache, key, out, e.cache.rawTTL(last, now))
	}
	return out, nil
}

func ints64(values []int64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
