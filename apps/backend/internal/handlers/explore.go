package handlers

import (
	"errors"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	environmentpkg "playersense.dev/backend/ent/environment"
	projectpkg "playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// The explorer's handlers (project.md §55): one page each, scoped exactly as the cohort and
// series routes are. The project comes from the path, checked against the caller's access list
// before any permission, and the environment is looked up inside it (scopeForMember).

// explore resolves the scope and the range every explorer route starts from.
func explore(ctx *muzak.Context, environment string, r schemas.AnalyticsRange) (*query.Engine, query.Scope, query.Window, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, environment)
	if err != nil {
		return nil, scope, query.Window{}, err
	}
	from, to, err := r.Resolve(time.Now())
	if err != nil {
		return nil, scope, query.Window{}, err
	}
	return engine, scope, query.NewWindow(from, to), nil
}

// exploreFailed turns an engine error into an answer: a refusal is a 422 with the reason, a thing
// that does not exist is a 404, and anything else is the facts being unreadable.
func exploreFailed(err error, what string) error {
	var refusal *query.Refusal
	switch {
	case errors.As(err, &refusal):
		return answerFailed(err, what)
	case errors.Is(err, query.ErrNotFound):
		return muzak.NotFound("this project has no such " + what)
	default:
		return muzak.ServiceUnavailable("the " + what + " facts could not be read; retry shortly").Wrap(err)
	}
}

// EventCatalogue lists every event the project sends, with its counts over the range.
func EventCatalogue(ctx *muzak.Context, in schemas.EventCatalogueParams) (schemas.EventCatalogueOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.EventCatalogueOut{}, err
	}
	out, err := engine.EventCatalogue(ctx.Context(), scope, w)
	if err != nil {
		return schemas.EventCatalogueOut{}, exploreFailed(err, "event")
	}
	return *out, nil
}

// EventDetail answers one event's page.
func EventDetail(ctx *muzak.Context, in schemas.EventDetailParams) (schemas.EventDetailOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.EventDetailOut{}, err
	}
	out, err := engine.EventDetail(ctx.Context(), scope, in.Name, w, in.Interval)
	if err != nil {
		return schemas.EventDetailOut{}, exploreFailed(err, "event")
	}
	return *out, nil
}

// EventOccurrences lists an event's newest occurrences, whole, as they were stored.
func EventOccurrences(ctx *muzak.Context, in schemas.EventOccurrencesParams) (schemas.EventOccurrencesOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return schemas.EventOccurrencesOut{}, err
	}
	out, err := engine.EventOccurrences(ctx.Context(), scope, in.Name, in.Limit, time.Now())
	if err != nil {
		return schemas.EventOccurrencesOut{}, exploreFailed(err, "event")
	}
	return *out, nil
}

// Sessions answers the Sessions page.
func Sessions(ctx *muzak.Context, in schemas.SessionsParams) (schemas.SessionsOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.SessionsOut{}, err
	}
	tz, err := timezone(in.Timezone)
	if err != nil {
		return schemas.SessionsOut{}, err
	}
	out, err := engine.Sessions(ctx.Context(), scope, w, tz)
	if err != nil {
		return schemas.SessionsOut{}, exploreFailed(err, "session")
	}
	return *out, nil
}

// RecentSessions lists the newest sessions.
func RecentSessions(ctx *muzak.Context, in schemas.RecentSessionsParams) (schemas.RecentSessionsOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return schemas.RecentSessionsOut{}, err
	}
	out, err := engine.RecentSessions(ctx.Context(), scope, in.Limit, time.Now())
	if err != nil {
		return schemas.RecentSessionsOut{}, exploreFailed(err, "session")
	}
	return *out, nil
}

// Players reads one page of the player directory.
func Players(ctx *muzak.Context, in schemas.PlayerListParams) (schemas.PlayerListOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return schemas.PlayerListOut{}, err
	}
	out, err := engine.Players(ctx.Context(), scope, query.PlayerFilter{
		Status: in.Status, Platform: in.Platform, Country: in.Country, Spend: in.Spend,
		Sort: in.Sort, Limit: in.Limit, Offset: in.Offset,
	}, time.Now())
	if err != nil {
		return schemas.PlayerListOut{}, exploreFailed(err, "player")
	}
	return *out, nil
}

// Player reads one player's page.
func Player(ctx *muzak.Context, in schemas.PlayerParams) (schemas.PlayerProfileOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return schemas.PlayerProfileOut{}, err
	}
	out, err := engine.Player(ctx.Context(), scope, in.ID, time.Now())
	if err != nil {
		return schemas.PlayerProfileOut{}, exploreFailed(err, "player")
	}
	return *out, nil
}

// timezone reads an optional IANA name, UTC when absent.
func timezone(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	tz, err := time.LoadLocation(name)
	if err != nil {
		return nil, muzak.BadRequest("tz is not an IANA timezone name")
	}
	return tz, nil
}

func revenueFilter(f schemas.RevenueFilter) query.RevenueFilter {
	stores, countries, products := f.Lists()
	return query.RevenueFilter{Stores: stores, Countries: countries, Products: products}
}

// Revenue answers the Revenue page.
func Revenue(ctx *muzak.Context, in schemas.RevenueParams) (schemas.RevenueOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.RevenueOut{}, err
	}
	tz, err := timezone(in.Timezone)
	if err != nil {
		return schemas.RevenueOut{}, err
	}
	out, err := engine.Revenue(ctx.Context(), scope, w, revenueFilter(in.RevenueFilter), tz)
	if err != nil {
		return schemas.RevenueOut{}, exploreFailed(err, "revenue")
	}
	return *out, nil
}

// Purchases reads one page of the purchases ledger.
func Purchases(ctx *muzak.Context, in schemas.PurchasesParams) (schemas.PurchasesOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.PurchasesOut{}, err
	}
	out, err := engine.Purchases(ctx.Context(), scope, w, revenueFilter(in.RevenueFilter), in.Limit, in.Before)
	if err != nil {
		return schemas.PurchasesOut{}, exploreFailed(err, "purchase")
	}
	return *out, nil
}

// FlagExposures lists how often every flag was read.
func FlagExposures(ctx *muzak.Context, in schemas.FlagExposureListParams) (schemas.FlagExposureListOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.FlagExposureListOut{}, err
	}
	out, err := engine.FlagExposures(ctx.Context(), scope, w)
	if err != nil {
		return schemas.FlagExposureListOut{}, exploreFailed(err, "flag")
	}
	return *out, nil
}

// FlagExposure answers one flag's insights.
func FlagExposure(ctx *muzak.Context, in schemas.FlagExposureParams) (schemas.FlagExposureOut, error) {
	engine, scope, w, err := explore(ctx, in.Environment, in.AnalyticsRange)
	if err != nil {
		return schemas.FlagExposureOut{}, err
	}
	out, err := engine.FlagExposure(ctx.Context(), scope, in.Flag, w, in.Version)
	if err != nil {
		return schemas.FlagExposureOut{}, exploreFailed(err, "flag exposure")
	}
	return *out, nil
}

// ProjectsOverview reads every readable project's headline figures.
//
// analytics.read is a project permission, so the route has no guard of its own: the projects are
// the organization's, kept to those the caller can reach and holds analytics.read on. Somebody who
// holds it on none gets an empty table, which is what they may see.
func ProjectsOverview(ctx *muzak.Context, in schemas.ProjectsOverviewParams) (schemas.ProjectsOverviewOut, error) {
	db := muzak.From[*core.DB](ctx)
	engine := muzak.From[*query.Engine](ctx)
	principal := muzak.From[*core.Principal](ctx)

	from, to, err := in.Resolve(time.Now())
	if err != nil {
		return schemas.ProjectsOverviewOut{}, err
	}

	projects, err := db.Client.Project.Query().
		Where(projectpkg.OrganizationID(principal.Organization.ID)).
		Order(ent.Asc(projectpkg.FieldSlug)).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectsOverviewOut{}, core.AsHTTPError(err)
	}
	var readable []*ent.Project
	var ids []int
	for _, p := range projects {
		if principal.Actor.Can(authz.AnalyticsRead, p.ID) {
			readable = append(readable, p)
			ids = append(ids, p.ID)
		}
	}
	envs, err := db.Client.Environment.Query().
		Where(environmentpkg.ProjectIDIn(ids...), environmentpkg.KindEQ(environmentpkg.Kind(in.Environment))).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectsOverviewOut{}, core.AsHTTPError(err)
	}
	envOf := map[int]int{}
	for _, env := range envs {
		envOf[env.ProjectID] = env.ID
	}

	refs := make([]query.ProjectRef, 0, len(readable))
	for _, p := range readable {
		env, ok := envOf[p.ID]
		if !ok {
			continue
		}
		refs = append(refs, query.ProjectRef{Slug: p.Slug, Name: p.Name, Scope: query.Scope{
			OrgSeq: uint32(principal.Organization.ID), ProjectSeq: uint32(p.ID),
			EnvironmentSeq: uint16(env), PolicyTier: string(p.PolicyTier),
		}})
	}

	out, err := engine.ProjectsOverview(ctx.Context(), refs, query.NewWindow(from, to))
	if err != nil {
		return schemas.ProjectsOverviewOut{}, exploreFailed(err, "overview")
	}
	return *out, nil
}

// QueryFunnel answers one funnel, from events_raw under the raw limits (§31).
func QueryFunnel(ctx *muzak.Context, in schemas.FunnelQueryIn) (schemas.FunnelAnswerOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return schemas.FunnelAnswerOut{}, err
	}
	from, to, err := schemas.AnalyticsRange{From: in.From, To: in.To}.Resolve(time.Now())
	if err != nil {
		return schemas.FunnelAnswerOut{}, err
	}

	out := schemas.FunnelAnswerOut{Kind: in.Kind}
	if in.Kind == "progression" {
		out.Progression, err = engine.Progression(ctx.Context(), scope, from, to, *in.Progression, time.Now())
	} else {
		out.Steps, err = engine.Funnel(ctx.Context(), scope, query.FunnelQuery{
			From: from, To: to, Window: in.Window, Steps: in.Steps, Breakdown: in.Breakdown,
		}, time.Now())
	}
	if err != nil {
		return schemas.FunnelAnswerOut{}, exploreFailed(err, "funnel")
	}
	return out, nil
}
