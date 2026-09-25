// Package reports runs a report: a graph read left to right, where sources hand rows to steps,
// steps shape them, and outputs say what becomes of the result (project.md §31-33). The dashboard's
// report builder draws the graph and previews it over sample rows; this is the same graph run over
// the project's own, by hand or on a schedule.
//
// The rules are the builder's, so a report behaves the same on the canvas and here: a filter keeps
// matching rows, a join matches two inputs player by player, a group sums and counts by columns and
// time buckets, a formula adds a column, a sort orders and keeps the top. Sources are read from the
// facts built for them (query.ReportSource) under a row limit, and the steps run over those rows in
// memory the limit bounds.
package reports

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Node kinds.
const (
	KindStart    = "start"
	KindFinish   = "finish"
	KindSchedule = "schedule"
	KindSource   = "source"
	KindFilter   = "filter"
	KindJoin     = "join"
	KindGroup    = "group"
	KindFormula  = "formula"
	KindSort     = "sort"
	KindChart    = "chart"
	KindTable    = "table"
	KindExport   = "export"
	KindNotify   = "notify"
)

var kinds = map[string]string{
	KindStart: "frame", KindFinish: "frame", KindSchedule: "trigger", KindSource: "source",
	KindFilter: "step", KindJoin: "step", KindGroup: "step", KindFormula: "step", KindSort: "step",
	KindChart: "output", KindTable: "output", KindExport: "output", KindNotify: "output",
}

// Handles on edges.
const (
	WhenHandle   = "when"
	StartHandle  = "start"
	FinishHandle = "finish"
)

// Graph is a report as the builder saves it.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is one box on the canvas.
type Node struct {
	ID       string   `json:"id"`
	Position Position `json:"position"`
	Data     NodeData `json:"data"`
}

// Position is where it sits; a plan orders nodes of one depth top to bottom by it.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// NodeData is what a node is and how it is set.
type NodeData struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Config Config `json:"config"`
}

// Edge is a line between two nodes.
type Edge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceHandle string `json:"sourceHandle,omitempty"`
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle,omitempty"`
}

// Config is every setting any kind of node has, as the builder names them.
type Config struct {
	Source string `json:"source,omitempty"`
	Range  string `json:"range,omitempty"`
	Event  string `json:"event,omitempty"`

	Match      string      `json:"match,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`

	Mode string `json:"mode,omitempty"`

	By      []string `json:"by,omitempty"`
	Bucket  string   `json:"bucket,omitempty"`
	Metrics []Metric `json:"metrics,omitempty"`

	Name   string `json:"name,omitempty"`
	Left   string `json:"left,omitempty"`
	Op     string `json:"op,omitempty"`
	Right  string `json:"right,omitempty"`
	Format string `json:"format,omitempty"`

	Column string `json:"column,omitempty"`
	Order  string `json:"order,omitempty"`
	Limit  *int   `json:"limit,omitempty"`

	Chart string   `json:"chart,omitempty"`
	X     string   `json:"x,omitempty"`
	Y     []string `json:"y,omitempty"`

	File        string `json:"file,omitempty"`
	Destination string `json:"destination,omitempty"`
	Target      string `json:"target,omitempty"`

	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Paused   bool   `json:"paused,omitempty"`

	Channels  []string   `json:"channels,omitempty"`
	When      string     `json:"when,omitempty"`
	Threshold *Threshold `json:"threshold,omitempty"`
	Subject   string     `json:"subject,omitempty"`
	Body      string     `json:"body,omitempty"`
	Include   string     `json:"include,omitempty"`
	Severity  string     `json:"severity,omitempty"`
}

// Condition is one filter test.
type Condition struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

// Metric is one figure a group works out.
type Metric struct {
	Fn     string `json:"fn"`
	Column string `json:"column"`
	As     string `json:"as"`
}

// Threshold is a notify's line.
type Threshold struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

// Limits bound a graph a person may save.
const (
	MaxNodes = 60
	MaxEdges = 120
)

// Decode reads a graph and checks what a run relies on: known kinds, unique ids, lines between
// nodes that exist, one Start and one Finish at most.
func Decode(raw []byte) (Graph, error) {
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return g, fmt.Errorf("the report is not a graph: %w", err)
	}
	return g, g.Check()
}

// Check is Decode's checks on a graph already read.
func (g Graph) Check() error {
	if len(g.Nodes) > MaxNodes || len(g.Edges) > MaxEdges {
		return fmt.Errorf("a report has at most %d nodes and %d lines", MaxNodes, MaxEdges)
	}
	ids := map[string]bool{}
	frames := map[string]int{}
	for _, n := range g.Nodes {
		if n.ID == "" || ids[n.ID] {
			return fmt.Errorf("node ids must be present and unique; %q is not", n.ID)
		}
		ids[n.ID] = true
		if _, ok := kinds[n.Data.Kind]; !ok {
			return fmt.Errorf("node %q is a %q, which is not a kind of node", n.ID, n.Data.Kind)
		}
		if n.Data.Kind == KindStart || n.Data.Kind == KindFinish {
			frames[n.Data.Kind]++
		}
	}
	if frames[KindStart] > 1 || frames[KindFinish] > 1 {
		return fmt.Errorf("a report has one Start and one Finish")
	}
	for _, e := range g.Edges {
		if !ids[e.Source] || !ids[e.Target] {
			return fmt.Errorf("line %q joins a node that is not in the report", e.ID)
		}
	}
	return nil
}

// Node returns a node by id.
func (g Graph) Node(id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func (g Graph) first(kind string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.Data.Kind == kind {
			return n, true
		}
	}
	return Node{}, false
}

func isFrameLine(e Edge) bool { return strings.HasPrefix(e.ID, "frame:") }

// lines is every line a run follows: those drawn, and Start's and Finish's, which nobody draws.
func (g Graph) lines() []Edge {
	var drawn []Edge
	for _, e := range g.Edges {
		if !isFrameLine(e) {
			drawn = append(drawn, e)
		}
	}
	out := append([]Edge(nil), drawn...)
	fed := func(id string) bool {
		for _, e := range drawn {
			if e.Target == id && e.TargetHandle != WhenHandle {
				return true
			}
		}
		return false
	}
	if start, ok := g.first(KindStart); ok {
		for _, n := range g.Nodes {
			if n.Data.Kind == KindSource {
				out = append(out, Edge{ID: "frame:" + start.ID + ":" + n.ID, Source: start.ID, Target: n.ID, TargetHandle: StartHandle})
			}
		}
	}
	if finish, ok := g.first(KindFinish); ok {
		for _, n := range g.Nodes {
			if kinds[n.Data.Kind] == "output" && fed(n.ID) {
				out = append(out, Edge{ID: "frame:" + n.ID + ":" + finish.ID, Source: n.ID, SourceHandle: FinishHandle, Target: finish.ID})
			}
		}
	}
	return out
}

func inputsOf(id string, edges []Edge) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.Target == id && e.TargetHandle != WhenHandle {
			out = append(out, e)
		}
	}
	return out
}

// Plan is the nodes a run runs, each after its inputs: everything Finish waits for, or only focus
// and what it reads from. Schedules are left out, and so is a step nothing reads from.
func (g Graph) Plan(focus string) []string {
	edges := g.lines()
	needed := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if needed[id] {
			return
		}
		needed[id] = true
		for _, e := range inputsOf(id, edges) {
			visit(e.Source)
		}
	}
	finish, hasFinish := g.first(KindFinish)
	switch {
	case focus != "":
		visit(focus)
	case hasFinish:
		visit(finish.ID)
	}
	if hasFinish {
		needed[finish.ID] = true
	}

	depth := map[string]int{}
	var depthOf func(string, map[string]bool) int
	depthOf = func(id string, trail map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if trail[id] {
			return 0
		}
		trail[id] = true
		d := 0
		for _, e := range inputsOf(id, edges) {
			if v := depthOf(e.Source, trail) + 1; v > d {
				d = v
			}
		}
		depth[id] = d
		return d
	}
	y := map[string]float64{}
	for _, n := range g.Nodes {
		y[n.ID] = n.Position.Y
	}
	var out []string
	for id := range needed {
		if _, ok := y[id]; ok {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := depthOf(out[i], map[string]bool{}), depthOf(out[j], map[string]bool{})
		if di != dj {
			return di < dj
		}
		if y[out[i]] != y[out[j]] {
			return y[out[i]] < y[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Schedules is every Schedule joined to Start's top edge, and on: what runs the report on its own.
func (g Graph) Schedules() []Config {
	start, ok := g.first(KindStart)
	var out []Config
	for _, e := range g.Edges {
		if !ok || e.Target != start.ID || e.TargetHandle != WhenHandle {
			continue
		}
		if n, found := g.Node(e.Source); found && n.Data.Kind == KindSchedule && !n.Data.Config.Paused {
			out = append(out, n.Data.Config)
		}
	}
	return out
}
