package handlers

import (
	"errors"
	"net/http"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// QueryCohorts answers one cohort question (§10.5, §57.1).
//
// It reads its cell's ClickHouse and nothing else. The tenant arrives resolved from the
// key, so the scope the compiler injects is a property of the credential rather than of
// anything the caller sent (§69, Appendix C #3).
func QueryCohorts(ctx *muzak.Context, in schemas.CohortQueryIn) (protocol.CohortResult, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	engine := muzak.From[*query.Engine](ctx)

	// Residency, the same rule the gateway applies to a batch (§6, §99).
	//
	// A cell holds only its own projects' data, so a reporting key from another cell would
	// find nothing here — and "nothing" is the worst possible answer, because it reads as
	// "no players" rather than "wrong server". A studio would conclude their game is dead.
	if tenant.Cell != home.Slug {
		return protocol.CohortResult{}, misdirected(tenant)
	}

	result, err := engine.Run(ctx.Context(), scopeOf(tenant), in.CohortQuery)
	if err != nil {
		return protocol.CohortResult{}, answerFailed(err, "cohort")
	}
	return *result, nil
}

// QueryMetrics answers one time-series question (§26, §27, §57).
//
// The same service, the same credential and the same residency rule as the cohort endpoint;
// a different set of tables. §27 keeps them apart because they answer different questions —
// this one is keyed on when something happened, not on when a player arrived — and mixing
// them in one request would mean a planner choosing between two fact families per field.
func QueryMetrics(ctx *muzak.Context, in schemas.MetricQueryIn) (protocol.MetricResult, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	engine := muzak.From[*query.Engine](ctx)

	if tenant.Cell != home.Slug {
		return protocol.MetricResult{}, misdirected(tenant)
	}

	result, err := engine.RunSeries(ctx.Context(), scopeOf(tenant), in.MetricQuery)
	if err != nil {
		return protocol.MetricResult{}, answerFailed(err, "metric")
	}
	return *result, nil
}

// scopeOf is the only way a scope is built, so a handler cannot assemble one from anything
// but the resolved credential (§69, Appendix C #3).
func scopeOf(tenant *core.Tenant) query.Scope {
	return query.Scope{
		ProjectSeq:     tenant.ProjectSeq,
		EnvironmentSeq: tenant.EnvironmentSeq,
		PolicyTier:     tenant.PolicyTier,
	}
}

// answerFailed turns a planner refusal into an explanation and anything else into a 503.
func answerFailed(err error, kind string) error {
	var refusal *query.Refusal
	if errors.As(err, &refusal) {
		// 422 rather than 400: the request is well-formed and the credential is good. The
		// facts cannot answer this particular question, and §32.1 asks for that to be said
		// out loud rather than approximated.
		return muzak.UnprocessableEntity(refusal.Reason).
			WithCode(kind + "_not_answerable").
			WithDetails(muzak.ErrorDetail{
				Field:    refusal.Field,
				Location: "body",
				Issue:    refusal.Reason,
			})
	}
	return muzak.ServiceUnavailable(
		"the " + kind + " facts could not be read; retry shortly").Wrap(err)
}

// misdirected refuses a key belonging to another cell, and says where to go.
//
// 421 rather than 403 for the same reason ingestion uses it: the request is well-formed and
// the credential is good, it simply arrived somewhere that cannot serve it. A cell
// registered without a query endpoint is refused without directions rather than pointed at
// the wrong service.
func misdirected(tenant *core.Tenant) error {
	message := "this project belongs to cell " + tenant.Cell
	detail := muzak.ErrorDetail{Field: "query_url", Location: "header"}
	if tenant.QueryURL != "" {
		message += "; send its queries to " + tenant.QueryURL
		detail.Issue = tenant.QueryURL
	} else {
		message += ", which has no registered query endpoint"
		detail.Issue = "the cell registry holds no query_url for " + tenant.Cell
	}
	return muzak.NewHTTPError(http.StatusMisdirectedRequest, message).
		WithCode("wrong_cell").
		WithDetails(detail)
}
