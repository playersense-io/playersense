package reports

import (
	"context"
	"time"

	"playersense.dev/backend/internal/query"
)

// EngineSources reads a report's sources from the query engine, for one project's environment.
type EngineSources struct {
	Engine *query.Engine
	Scope  query.Scope
}

// Read is SourceReader's one method.
func (s EngineSources) Read(ctx context.Context, source string, days int, event string, limit int, now time.Time) ([]Row, error) {
	return s.Engine.ReportSource(ctx, s.Scope, source, days, event, limit, now)
}

// NodePreview is what one node produced, for the builder's canvas.
type NodePreview struct {
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows" doc:"The first fifty"`
	Total   int      `json:"total"`
	Error   string   `json:"error,omitempty"`
	// Notify is what a Notify would do with this result: send or stay quiet, and what it says.
	Notify *Outcome `json:"notify,omitempty"`
}

// PreviewRows is how many rows of each node a preview returns.
const PreviewRows = 50

// Preview runs every node once, over the project's own rows, and sends nothing.
func Preview(ctx context.Context, g Graph, sources SourceReader, report string, now time.Time) map[string]NodePreview {
	ev := newEvaluator(g, sources, now)
	all := g.Plan("")
	seen := map[string]bool{}
	for _, id := range all {
		seen[id] = true
	}
	// Every node, not only those a run would reach: the canvas shows a step nothing reads yet.
	for _, n := range g.Nodes {
		if !seen[n.ID] {
			all = append(all, n.ID)
		}
	}
	ordered := orderByInputs(all, ev.edges)
	out := make(map[string]NodePreview, len(ordered))
	for _, id := range ordered {
		n, _ := g.Node(id)
		r := ev.step(ctx, n)
		ev.results[id] = r
		p := NodePreview{Columns: r.Columns, Total: len(r.Rows), Error: r.Err, Rows: r.Rows}
		if p.Columns == nil {
			p.Columns = []Column{}
		}
		if len(p.Rows) > PreviewRows {
			p.Rows = p.Rows[:PreviewRows]
		}
		if p.Rows == nil {
			p.Rows = []Row{}
		}
		if n.Data.Kind == KindNotify && r.Err == "" {
			o := Decide(n.Data.Config, report, r, now)
			p.Notify = &o
		}
		out[id] = p
	}
	return out
}

// orderByInputs puts every node after the nodes it reads from.
func orderByInputs(ids []string, edges []Edge) []string {
	placed := map[string]bool{}
	var out []string
	var visit func(string, map[string]bool)
	visit = func(id string, trail map[string]bool) {
		if placed[id] || trail[id] {
			return
		}
		trail[id] = true
		for _, e := range inputsOf(id, edges) {
			visit(e.Source, trail)
		}
		placed[id] = true
		out = append(out, id)
	}
	for _, id := range ids {
		visit(id, map[string]bool{})
	}
	return out
}
