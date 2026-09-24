package handlers

import (
	"errors"
	"time"

	muzak "muzak.dev/framework"

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
