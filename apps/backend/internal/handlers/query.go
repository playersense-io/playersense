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

	result, err := engine.Run(ctx.Context(), query.Scope{
		ProjectSeq:     tenant.ProjectSeq,
		EnvironmentSeq: tenant.EnvironmentSeq,
		PolicyTier:     tenant.PolicyTier,
	}, in.CohortQuery)
	if err != nil {
		var refusal *query.Refusal
		if errors.As(err, &refusal) {
			// 422 rather than 400: the request is well-formed and the credential is good.
			// The facts cannot answer this particular question, and §32.1 asks for that to
			// be said out loud rather than approximated.
			return protocol.CohortResult{}, muzak.UnprocessableEntity(refusal.Reason).
				WithCode("cohort_not_answerable").
				WithDetails(muzak.ErrorDetail{
					Field:    refusal.Field,
					Location: "body",
					Issue:    refusal.Reason,
				})
		}
		return protocol.CohortResult{}, muzak.ServiceUnavailable(
			"the cohort facts could not be read; retry shortly").Wrap(err)
	}
	return *result, nil
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
