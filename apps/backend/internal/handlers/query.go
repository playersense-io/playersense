package handlers

import (
	"errors"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	environmentpkg "playersense.dev/backend/ent/environment"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// QueryCohorts answers one cohort question (§10.5, §57.1).
//
// The tenant arrives resolved from the key, so the scope the compiler injects is a
// property of the credential rather than of anything the caller sent (§69, Appendix C #3).
func QueryCohorts(ctx *muzak.Context, in schemas.CohortQueryIn) (protocol.CohortResult, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	engine := muzak.From[*query.Engine](ctx)

	result, err := engine.Run(ctx.Context(), scopeOf(tenant), in.CohortQuery)
	if err != nil {
		return protocol.CohortResult{}, answerFailed(err, "cohort")
	}
	return *result, nil
}

// QueryMetrics answers one time-series question (§26, §27, §57).
//
// The same service and the same credential as the cohort endpoint; a different set of
// tables. §27 keeps them apart because they answer different questions —
// this one is keyed on when something happened, not on when a player arrived — and mixing
// them in one request would mean a planner choosing between two fact families per field.
func QueryMetrics(ctx *muzak.Context, in schemas.MetricQueryIn) (protocol.MetricResult, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	engine := muzak.From[*query.Engine](ctx)

	result, err := engine.RunSeries(ctx.Context(), scopeOf(tenant), in.MetricQuery)
	if err != nil {
		return protocol.MetricResult{}, answerFailed(err, "metric")
	}
	return *result, nil
}

// There are exactly two ways a scope is built, and neither reads the request body.
//
// That is what Appendix C #3 asks for — "no customer query can execute without tenant scope" is a
// rule about where the scope comes from, not about which credential carries it. One is a resolved
// reporting key; the other is a resolved membership plus a named environment. Both are established
// before a handler runs, and `query.Scope` has no other constructor in the codebase.

// scopeOf builds the scope from a resolved reporting key (§69, Appendix C #3).
func scopeOf(tenant *core.Tenant) query.Scope {
	return query.Scope{
		OrgSeq:         tenant.OrgSeq,
		ProjectSeq:     tenant.ProjectSeq,
		EnvironmentSeq: tenant.EnvironmentSeq,
		PolicyTier:     tenant.PolicyTier,
	}
}

// scopeForMember builds the scope from a resolved membership and the environment in the path.
//
// Every part comes from state the request could not choose. The project is the one
// [core.ResolveUser] resolved against the caller's access list — a project they cannot see was
// already refused as "no such project" — and the policy tier is read off that row, so §29.15's
// constraints apply to a person exactly as they apply to a key. The environment is looked up
// *inside* that project, which is why naming one is not a widening: all three belong to the project
// the access list already granted.
func scopeForMember(ctx *muzak.Context, environment string) (query.Scope, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	proj := principal.Project

	env, err := db.Client.Environment.Query().
		Where(environmentpkg.ProjectID(proj.ID),
			environmentpkg.KindEQ(environmentpkg.Kind(environment))).
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return query.Scope{}, muzak.NotFound(
				"this project has no " + environment + " environment")
		}
		return query.Scope{}, core.AsHTTPError(err)
	}

	return query.Scope{
		OrgSeq:         uint32(principal.Organization.ID),
		ProjectSeq:     uint32(proj.ID),
		EnvironmentSeq: uint16(env.ID),
		PolicyTier:     string(proj.PolicyTier),
	}, nil
}

// QueryCohortsForMember answers a cohort question for a signed-in person (§55.1).
//
// The dashboard's own path. A reporting key is a secret and a browser cannot hold one, so the
// analytics a studio looks at has to be reachable with the credential they already have — and
// `analytics.read` is the permission that decides, which is what makes GROWTH MANAGER a role that
// reads numbers and changes nothing.
func QueryCohortsForMember(ctx *muzak.Context, in schemas.MemberCohortQueryIn) (protocol.CohortResult, error) {
	engine := muzak.From[*query.Engine](ctx)

	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return protocol.CohortResult{}, err
	}

	result, err := engine.Run(ctx.Context(), scope, in.CohortQuery)
	if err != nil {
		return protocol.CohortResult{}, answerFailed(err, "cohort")
	}
	return *result, nil
}

// QueryMetricsForMember answers a time-series question for a signed-in person.
func QueryMetricsForMember(ctx *muzak.Context, in schemas.MemberMetricQueryIn) (protocol.MetricResult, error) {
	engine := muzak.From[*query.Engine](ctx)

	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return protocol.MetricResult{}, err
	}

	result, err := engine.RunSeries(ctx.Context(), scope, in.MetricQuery)
	if err != nil {
		return protocol.MetricResult{}, answerFailed(err, "metric")
	}
	return *result, nil
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
