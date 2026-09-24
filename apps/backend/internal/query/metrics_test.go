package query

import (
	"errors"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

func seriesWindowQ() (protocol.Day, protocol.Day) {
	return protocol.NewDay(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		protocol.NewDay(time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC))
}

// everySeriesShape is every time-series query the planner will accept.
func everySeriesShape() []protocol.MetricQuery {
	from, to := seriesWindowQ()
	var out []protocol.MetricQuery
	for _, interval := range schemas.Intervals {
		for _, metric := range schemas.SeriesMetrics {
			base := protocol.MetricQuery{Metric: metric, From: from, To: to, Interval: interval}
			out = append(out, base)
			for _, dim := range schemas.SeriesGroups {
				grouped := base
				grouped.GroupBy = []string{dim}
				out = append(out, grouped)
				if dim == schemas.GroupEventName {
					continue // not a filterable field; the contract refuses it
				}

				filtered := grouped
				filtered.Filters = []protocol.Filter{{Field: dim, Op: "eq", Value: "ios"}}
				out = append(out, filtered)
			}
			named := base
			named.EventName = "session_start"
			out = append(out, named)
		}
	}
	return out
}

// TestASeriesQueryNeverJoins is §32.1 over the second family of facts.
//
// The cohort compiler and the series compiler are different code producing different SQL
// against different tables. The guarantee has to hold for both or it holds for neither —
// and the series tables are the ones a dashboard hits on every page load.
func TestASeriesQueryNeverJoins(t *testing.T) {
	compiled := 0
	for _, q := range everySeriesShape() {
		plan, err := CompileSeries(scope(), q, limits())
		if err != nil {
			continue // a shape the tier cannot answer; refused rather than compiled
		}
		compiled++
		if join.MatchString(plan.Statement.SQL) {
			t.Errorf("%s/%s reads more than one table:\n%s", q.Metric, q.Interval, plan.Statement.SQL)
		}
		if n := strings.Count(strings.ToUpper(plan.Statement.SQL), " FROM "); n != 1 {
			t.Errorf("%s/%s has %d FROM clauses:\n%s", q.Metric, q.Interval, n, plan.Statement.SQL)
		}
		if !strings.Contains(plan.Statement.SQL, "project_seq = ?") ||
			!strings.Contains(plan.Statement.SQL, "environment_seq = ?") {
			t.Errorf("%s/%s is not tenant-scoped:\n%s", q.Metric, q.Interval, plan.Statement.SQL)
		}
	}
	if compiled < 40 {
		t.Fatalf("only %d shapes compiled; the matrix is not covering the planner", compiled)
	}
}

// TestEachIntervalReadsItsOwnTier is §26's rollup policy.
func TestEachIntervalReadsItsOwnTier(t *testing.T) {
	from, to := seriesWindowQ()
	cases := []struct {
		interval string
		groupBy  []string
		event    string
		want     Fact
	}{
		// The whole project, from the tiers that carry its total.
		{schemas.IntervalMinute, nil, "", factActivity1m},
		{schemas.IntervalHour, nil, "", factActivity1h},
		// One event, or a split by event, from the tiers keyed by event name.
		{schemas.IntervalMinute, nil, "session_start", factEvents1m},
		{schemas.IntervalHour, []string{schemas.GroupEventName}, "", factEvents1h},
		{schemas.IntervalDay, nil, "", factMetricsDaily},
		{schemas.IntervalDay, []string{"install_country"}, "", factMetricsBreakdown},
		{schemas.IntervalMonth, nil, "", factMetricsMonthly},
	}
	for _, c := range cases {
		plan, err := CompileSeries(scope(), protocol.MetricQuery{
			Metric: schemas.MetricActivePlayers, From: from, To: to,
			Interval: c.interval, GroupBy: c.groupBy, EventName: c.event,
		}, limits())
		if err != nil {
			t.Fatalf("%s: %v", c.interval, err)
		}
		if plan.Fact != c.want {
			t.Errorf("%s resolved to %s, want %s", c.interval, plan.Fact, c.want)
		}
	}
}

// TestOnlyTheRealtimeTiersAreApproximate.
//
// The marking has to follow the source rather than the request, because it is the source
// that decides whether the number is a distinct count or a sketch.
func TestOnlyTheRealtimeTiersAreApproximate(t *testing.T) {
	from, to := seriesWindowQ()
	for interval, wantApprox := range map[string]bool{
		schemas.IntervalMinute: true,
		schemas.IntervalHour:   true,
		schemas.IntervalDay:    false,
		schemas.IntervalMonth:  false,
	} {
		plan, err := CompileSeries(scope(), protocol.MetricQuery{
			Metric: schemas.MetricActivePlayers, From: from, To: to, Interval: interval,
		}, limits())
		if err != nil {
			t.Fatalf("%s: %v", interval, err)
		}
		if plan.Approximate != wantApprox {
			t.Errorf("%s reports approximate=%v, want %v", interval, plan.Approximate, wantApprox)
		}
	}
}

// TestTheWindowIsHalfOpen guards twenty-three hours of a customer's range.
//
// The request names days. On a timestamp-keyed tier, "up to and including the 2nd" has to
// compile to "< the 3rd"; writing it as "<= the 2nd" would silently drop everything after
// midnight on the last day, and the chart would simply end early.
func TestTheWindowIsHalfOpen(t *testing.T) {
	from, to := seriesWindowQ()
	plan, err := CompileSeries(scope(), protocol.MetricQuery{
		Metric: schemas.MetricEvents, From: from, To: to, Interval: schemas.IntervalHour,
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Statement.SQL, "hour >= ?") || !strings.Contains(plan.Statement.SQL, "hour < ?") {
		t.Fatalf("the window is not half-open:\n%s", plan.Statement.SQL)
	}
	var bounds []string
	for _, arg := range plan.Statement.Args {
		if s, ok := arg.(string); ok && strings.Contains(s, ":") {
			bounds = append(bounds, s)
		}
	}
	if len(bounds) != 2 || bounds[1] != "2026-06-03 00:00:00" {
		t.Fatalf("the exclusive upper bound is %v, want the start of the 3rd", bounds)
	}

	// A Date-keyed tier must not be handed a timestamp literal; ClickHouse refuses to
	// compare the two rather than coercing, which is how this was caught the first time.
	daily, err := CompileSeries(scope(), protocol.MetricQuery{
		Metric: schemas.MetricActivePlayers, From: from, To: to, Interval: schemas.IntervalDay,
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range daily.Statement.Args {
		if s, ok := arg.(string); ok && strings.Contains(s, ":") {
			t.Fatalf("a Date column was given the timestamp literal %q", s)
		}
	}
}

// TestAWholeProjectRealtimeQueryMergesOneSketchPerBucket is the reason activity_1m exists.
//
// Read from the per-event tiers, "players this minute" merges one uniq state per event name, and
// a busy event's state is a quarter of a megabyte. The whole-project tier holds one state per
// bucket, so the statement must not group or filter by event at all.
func TestAWholeProjectRealtimeQueryMergesOneSketchPerBucket(t *testing.T) {
	from, to := seriesWindowQ()
	plan, err := CompileSeries(scope(), protocol.MetricQuery{
		Metric: schemas.MetricSessions, From: from, To: to, Interval: schemas.IntervalMinute,
	}, limits())
	if err != nil {
		t.Fatalf("sessions per minute for the whole project: %v", err)
	}
	if strings.Contains(plan.Statement.SQL, "event_name") {
		t.Fatalf("a whole-project minute series mentions event_name:\n%s", plan.Statement.SQL)
	}
	if !strings.Contains(plan.Statement.SQL, "uniqCombinedMerge(14)(players)") {
		t.Fatalf("the player count is not read from the project's own sketch:\n%s", plan.Statement.SQL)
	}
}

// TestASeriesSplitByEventIsOneLinePerEvent is the realtime page's top events.
func TestASeriesSplitByEventIsOneLinePerEvent(t *testing.T) {
	from, to := seriesWindowQ()
	plan, err := CompileSeries(scope(), protocol.MetricQuery{
		Metric: schemas.MetricEvents, From: from, To: to, Interval: schemas.IntervalMinute,
		GroupBy: []string{schemas.GroupEventName},
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fact != factEvents1m || !strings.Contains(plan.Statement.SQL, "toString(event_name) AS dim_value") {
		t.Fatalf("a split by event does not put the event in dim_value:\n%s", plan.Statement.SQL)
	}
}

// TestQuestionsATierCannotAnswerAreRefusedWithAReason.
//
// Each of these used to either compile into SQL ClickHouse rejected, which reached the caller as
// a 503 that said nothing, or would have drawn a flat line of zeros.
func TestQuestionsATierCannotAnswerAreRefusedWithAReason(t *testing.T) {
	from, to := seriesWindowQ()
	cases := map[string]protocol.MetricQuery{
		// metrics_monthly has no dim_value; this reached ClickHouse and failed there.
		"a month split by platform": {Metric: schemas.MetricActivePlayers, Interval: schemas.IntervalMonth,
			GroupBy: []string{"install_platform"}},
		"a month filtered by country": {Metric: schemas.MetricActivePlayers, Interval: schemas.IntervalMonth,
			Filters: []protocol.Filter{{Field: "install_country", Op: "eq", Value: "TR"}}},
		"a day split by event": {Metric: schemas.MetricEvents, Interval: schemas.IntervalDay,
			GroupBy: []string{schemas.GroupEventName}},
		"sessions of one event": {Metric: schemas.MetricSessions, Interval: schemas.IntervalHour,
			EventName: "level_complete"},
		"a lifecycle split by platform": {Metric: schemas.MetricReactivatedPlayers,
			Interval: schemas.IntervalDay, GroupBy: []string{"install_platform"}},
		"a lifecycle by the month": {Metric: schemas.MetricContinuingPlayers,
			Interval: schemas.IntervalMonth},
		"new players by the minute": {Metric: schemas.MetricNewPlayers, Interval: schemas.IntervalMinute},
	}
	for name, q := range cases {
		q.From, q.To = from, to
		_, err := CompileSeries(scope(), q, limits())
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("%s: got %v, want a refusal", name, err)
			continue
		}
		if refusal.Reason == "" || refusal.Field == "" {
			t.Errorf("%s: the refusal does not say what or why: %+v", name, refusal)
		}
	}
}

// TestTheLifecycleSplitReadsItsOwnFact.
func TestTheLifecycleSplitReadsItsOwnFact(t *testing.T) {
	from, to := seriesWindowQ()
	for _, metric := range []string{schemas.MetricContinuingPlayers, schemas.MetricReactivatedPlayers} {
		plan, err := CompileSeries(scope(), protocol.MetricQuery{
			Metric: metric, From: from, To: to,
		}, limits())
		if err != nil {
			t.Fatalf("%s: %v", metric, err)
		}
		if plan.Fact != factLifecycle || plan.Approximate {
			t.Errorf("%s resolved to %s (approximate=%v), want exact lifecycle_daily", metric, plan.Fact, plan.Approximate)
		}
	}
}
