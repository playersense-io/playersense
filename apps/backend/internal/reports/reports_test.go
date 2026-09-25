package reports

import (
	"context"
	"testing"
	"time"
)

func TestCronRunsWhenItSays(t *testing.T) {
	c, err := ParseCron("0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) // a Thursday
	if got := c.Next(after, time.UTC); !got.Equal(time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next Monday 09:00 is %s", got)
	}
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	if got := c.Next(after, istanbul); !got.Equal(time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("09:00 in Istanbul is %s UTC", got)
	}
	every, _ := ParseCron("*/15 8-10 1,15 * *")
	if got := every.Next(time.Date(2026, 9, 15, 10, 50, 0, 0, time.UTC), time.UTC); !got.Equal(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("after the last slot of the 15th: %s", got)
	}
	for _, bad := range []string{"", "* * *", "60 * * * *", "* * 0 * *", "*/0 * * * *", "a * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	never, _ := ParseCron("0 0 30 2 *")
	if got := never.Next(after, time.UTC); !got.IsZero() {
		t.Fatalf("the 30th of February came at %s", got)
	}
}

type fakeSources map[string][]Row

func (f fakeSources) Read(_ context.Context, source string, _ int, _ string, _ int, _ time.Time) ([]Row, error) {
	return f[source], nil
}

func graph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "start", Data: NodeData{Kind: KindStart}},
			{ID: "sched", Data: NodeData{Kind: KindSchedule, Config: Config{Cron: "0 9 * * 1", Timezone: "UTC"}}},
			{ID: "pur", Position: Position{Y: 0}, Data: NodeData{Kind: KindSource, Config: Config{Source: "purchases", Range: "30"}}},
			{ID: "pl", Position: Position{Y: 100}, Data: NodeData{Kind: KindSource, Config: Config{Source: "players", Range: "30"}}},
			{ID: "join", Data: NodeData{Kind: KindJoin, Config: Config{Mode: "inner"}}},
			{ID: "only", Data: NodeData{Kind: KindFilter, Config: Config{Conditions: []Condition{{Column: "payer", Op: "is", Value: "YES"}}}}},
			{ID: "grp", Data: NodeData{Kind: KindGroup, Config: Config{By: []string{"country"},
				Metrics: []Metric{{Fn: "sum", Column: "price", As: "revenue"}, {Fn: "count_distinct", Column: "player_id", As: "payers"}}}}},
			{ID: "per", Data: NodeData{Kind: KindFormula, Config: Config{Name: "per payer", Left: "revenue", Op: "/", Right: "payers", Format: "money"}}},
			{ID: "top", Data: NodeData{Kind: KindSort, Config: Config{Column: "revenue", Order: "desc"}}},
			{ID: "tell", Data: NodeData{Kind: KindNotify, Config: Config{Channels: []string{"c"}, When: "threshold",
				Threshold: &Threshold{Column: "revenue", Op: "gt", Value: "10"}, Subject: "{{report}}: {{first.country}} made {{first.revenue}}"}}},
			{ID: "stray", Data: NodeData{Kind: KindFilter}},
			{ID: "finish", Data: NodeData{Kind: KindFinish}},
		},
		Edges: []Edge{
			{ID: "e0", Source: "sched", Target: "start", TargetHandle: WhenHandle},
			{ID: "e1", Source: "pur", Target: "join", TargetHandle: "left"},
			{ID: "e2", Source: "pl", Target: "join", TargetHandle: "right"},
			{ID: "e3", Source: "join", Target: "only"},
			{ID: "e4", Source: "only", Target: "grp"},
			{ID: "e5", Source: "grp", Target: "per"},
			{ID: "e6", Source: "per", Target: "top"},
			{ID: "e7", Source: "top", Target: "tell"},
		},
	}
}

func TestAReportRunsItsStepsInOrder(t *testing.T) {
	g := graph()
	if err := g.Check(); err != nil {
		t.Fatal(err)
	}
	plan := g.Plan("")
	want := []string{"start", "pur", "pl", "join", "only", "grp", "per", "top", "tell", "finish"}
	if len(plan) != len(want) {
		t.Fatalf("plan %v, want %v", plan, want)
	}
	for i := range want {
		if plan[i] != want[i] {
			t.Fatalf("plan %v, want %v", plan, want)
		}
	}
	if s := g.Schedules(); len(s) != 1 || s[0].Cron != "0 9 * * 1" {
		t.Fatalf("schedules %+v", s)
	}

	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	sources := fakeSources{
		"purchases": {
			{"player_id": "a", "price": 9.99, "country": "TR"}, {"player_id": "a", "price": 4.99, "country": "TR"},
			{"player_id": "b", "price": 1.99, "country": "DE"}, {"player_id": "z", "price": 99.0, "country": "US"},
		},
		"players": {{"player_id": "a", "payer": "yes"}, {"player_id": "b", "payer": "yes"}, {"player_id": "z", "payer": "no"}},
	}
	ev := newEvaluator(g, sources, now)
	for _, id := range plan {
		n, _ := g.Node(id)
		ev.results[id] = ev.step(context.Background(), n)
	}
	top := ev.results["top"]
	if top.Err != "" || len(top.Rows) != 2 || top.Rows[0]["country"] != "TR" || top.Rows[0]["revenue"] != 14.98 ||
		top.Rows[0]["per_payer"] != 14.98 || top.Rows[1]["payers"] != 1.0 {
		t.Fatalf("top %+v %v", top.Rows, top.Err)
	}
	outcome := Decide(g.Nodes[9].Data.Config, "Revenue", ev.results["tell"], now)
	if !outcome.Send || outcome.Title != "Revenue: TR made $14.98" || len(outcome.Lines) != 2 {
		t.Fatalf("outcome %+v", outcome)
	}
	quiet := g.Nodes[9].Data.Config
	quiet.Threshold = &Threshold{Column: "revenue", Op: "gt", Value: "100"}
	if o := Decide(quiet, "Revenue", ev.results["tell"], now); o.Send {
		t.Fatalf("under the line and it sends: %+v", o)
	}
}

func TestABrokenStepSaysWhy(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "s", Data: NodeData{Kind: KindSource, Config: Config{Source: "players"}}},
		{ID: "f", Data: NodeData{Kind: KindFilter, Config: Config{Conditions: []Condition{{Column: "gone", Op: "is", Value: "x"}}}}},
		{ID: "after", Data: NodeData{Kind: KindSort}},
		{ID: "alone", Data: NodeData{Kind: KindTable}},
	}, Edges: []Edge{{ID: "1", Source: "s", Target: "f"}, {ID: "2", Source: "f", Target: "after"}}}
	ev := newEvaluator(g, fakeSources{}, time.Now())
	for _, id := range []string{"s", "f", "after", "alone"} {
		n, _ := g.Node(id)
		ev.results[id] = ev.step(context.Background(), n)
	}
	if ev.results["f"].Err != "gone is not a column here any more." || ev.results["after"].Err == "" ||
		ev.results["alone"].Err != "Connect a result into it." {
		t.Fatalf("%q %q %q", ev.results["f"].Err, ev.results["after"].Err, ev.results["alone"].Err)
	}
	if err := (Graph{Nodes: []Node{{ID: "x", Data: NodeData{Kind: "teleport"}}}}).Check(); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}
