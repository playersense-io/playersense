package query

import (
	"context"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// ProjectRef is one project the caller may read, and the environment of it being asked about.
type ProjectRef struct {
	Slug, Name string
	Scope      Scope
}

// ProjectsOverview reads every project's headline figures from metrics_daily in one scan.
//
// The projects are the ones the handler found the caller may read; the statement is bounded to
// exactly those (project, environment) pairs, so the tenant is still injected rather than
// requested. metrics_daily is keyed on the project first, so each pair is its own key range.
func (e *Engine) ProjectsOverview(ctx context.Context, projects []ProjectRef, w Window) (*schemas.ProjectsOverviewOut, error) {
	out := &schemas.ProjectsOverviewOut{From: w.From, To: w.To, SuppressionThreshold: e.k}
	if len(projects) == 0 {
		return out, nil
	}
	withheld := Suppressed{Threshold: e.k}

	type sums struct {
		days, prevDays, fresh, sessions, playtime, events, payers uint64
		rev, prevRev                                              int64
	}
	found := map[uint32]sums{}

	cut := date(w.From)
	b := from(factMetricsDaily)
	b.column("project_seq", "p").
		columnWith("sumIf(active_players, activity_date >= ?)", "pd", cut).
		columnWith("sumIf(active_players, activity_date < ?)", "prev_pd", cut).
		columnWith("sumIf(new_players, activity_date >= ?)", "fresh", cut).
		columnWith("sumIf(sessions, activity_date >= ?)", "s", cut).
		columnWith("sumIf(playtime_ms, activity_date >= ?)", "pt", cut).
		columnWith("sumIf(events, activity_date >= ?)", "ev", cut).
		columnWith("sumIf(payers, activity_date >= ?)", "pay", cut).
		columnWith("sumIf(revenue_micros, activity_date >= ?)", "rev", cut).
		columnWith("sumIf(revenue_micros, activity_date < ?)", "prev_rev", cut)
	marks := make([]string, len(projects))
	args := make([]any, 0, 2*len(projects))
	for i, p := range projects {
		marks[i] = "(?, ?)"
		args = append(args, p.Scope.ProjectSeq, p.Scope.EnvironmentSeq)
	}
	b.and("(project_seq, environment_seq) IN ("+strings.Join(marks, ", ")+")", args...)
	b.between("activity_date", date(w.Previous().From), date(w.To))
	b.groupOn("p")
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var p uint32
		var s sums
		if err := rows.Scan(&p, &s.days, &s.prevDays, &s.fresh, &s.sessions, &s.playtime, &s.events,
			&s.payers, &s.rev, &s.prevRev); err != nil {
			return err
		}
		found[p] = s
		return nil
	}); err != nil {
		return nil, failed("metrics_daily", err)
	}

	for _, p := range projects {
		s := found[p.Scope.ProjectSeq]
		row := schemas.ProjectOverviewOut{
			Slug: p.Slug, Name: p.Name,
			PlayerDays: shown(&withheld, s.days), PreviousPlayerDays: shown(&withheld, s.prevDays),
			NewPlayers: s.fresh, Sessions: s.sessions, PlaytimeMs: s.playtime, Events: s.events,
		}
		// Money only where the payers are enough to disclose, as on every other page.
		if !withheld.withheld(s.payers) {
			row.RevenueMicros, row.PreviousRevenueMicros, row.PayerDays = s.rev, s.prevRev, s.payers
		}
		out.Projects = append(out.Projects, row)
	}
	out.SuppressedCells = withheld.Cells
	return out, nil
}
