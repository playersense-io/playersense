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
