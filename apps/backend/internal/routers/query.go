package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Cohorts returns the reporting router (§10.5).
//
// POST rather than GET, because the query is a document: §57.1's shape nests a cohort
// range, an offset list, a breakdown and filters, and flattening that into a query string
// produces something no one can read and every proxy truncates differently. The cost is
// that the result is not cacheable by an intermediary, which is the right trade — §58 puts
// the cache in Valkey, where it can be keyed on the tenant and invalidated on a derivation.
func Cohorts() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("reporting"))

	r.Post("/v1/cohorts/query", handlers.QueryCohorts,
		muzak.Summary("Answer a cohort question"),
		muzak.Description("Resolves to exactly one pre-aggregated cohort fact and never joins "+
			"on a player key. Cells describing too few players are suppressed rather than "+
			"returned as zero, and the response says how many were withheld."),
		muzak.Status(http.StatusOK),

		// A reporting key and no other class (§52). A project key is shipped inside a game
		// and therefore public; accepting one here would publish every studio's analytics
		// to anyone who unpacked their APK.
		muzak.Needs(core.ResolveReportingKey),

		// A query is a small document. Anything larger is not a cohort question.
		muzak.MaxBodySize(64<<10),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "This endpoint needs a reporting key"),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the query"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The cohort facts cannot answer this question, and the body explains why"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The cohort facts could not be read"),
	)

	r.Post("/v1/metrics/query", handlers.QueryMetrics,
		muzak.Summary("Answer a time-series question"),
		muzak.Description("Active players, sessions, events and revenue over time. The planner "+
			"chooses the summary table from the requested grain (§26); a grain finer than the "+
			"range can carry is refused rather than returned as tens of thousands of points."),
		muzak.Status(http.StatusOK),

		muzak.Needs(core.ResolveReportingKey),
		muzak.MaxBodySize(64<<10),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "This endpoint needs a reporting key"),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the query"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The summaries cannot answer this question, and the body explains why"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The summaries could not be read"),
	)

	return r
}

// Analytics returns the reporting routes a signed-in person uses (§55.1).
//
// The same two questions [Cohorts] answers, reached with the credential a browser can actually
// hold. §52 makes a reporting key a secret, so shipping one to a page would publish every studio's
// analytics to anybody who opened the network tab — which left the dashboard with no way to read
// the numbers the whole product exists to produce, and `analytics.read` defined and guarding
// nothing.
//
// # Why these live here and not on control-api
//
// Because the data does. `query-api` is cell-local: it holds this cell's ClickHouse, and a chart
// has to be answered by the cell that stores the rows. The alternative — proxying through the
// US-hosted control plane — would mean every EU query passing through American infrastructure,
// which is the thing the cell design exists to avoid (ADR 0009, ADR 0010).
//
// The cost is that a cell now verifies Firebase tokens and reads the global tier for memberships.
// It does not *write* there: [core.ResolveKnownUser] withholds the account creation
// [core.ResolveUser] performs, so a cell cannot bring a user row or a personal organization into
// existence in the control plane.
//
// # The scope is still not the caller's to choose
//
// Appendix C #3 holds. The project comes from the path and is checked against the caller's project
// access list before any permission is considered — a project they were not given is "no such
// project", not a refusal — and the environment is looked up inside that project. The body's
// `project_id`, `environment_id` and `org_id` members remain decoys that refuse the request.
func Analytics() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("reporting"),
		muzak.WithResponseDoc(http.StatusNotFound,
			"The organization, project or environment does not exist, or you cannot see it"))

	const base = "/organizations/{org}/projects/{project}/analytics/{environment}"

	r.Post(base+"/cohorts/query", handlers.QueryCohortsForMember,
		muzak.Summary("Answer a cohort question for a signed-in person"),
		muzak.Description("The same planner, facts and suppression threshold the reporting-key "+
			"endpoint uses. The scope is the project in the path, checked against your access "+
			"list, and the environment named beside it."),
		core.Require(authz.AnalyticsRead),
		muzak.MaxBodySize(64<<10),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the query"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The cohort facts cannot answer this question, and the body explains why"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The cohort facts could not be read"))

	r.Post(base+"/metrics/query", handlers.QueryMetricsForMember,
		muzak.Summary("Answer a time-series question for a signed-in person"),
		muzak.Description("Active players, sessions, events and revenue over time, for one "+
			"environment of one project."),
		core.Require(authz.AnalyticsRead),
		muzak.MaxBodySize(64<<10),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the query"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The summaries cannot answer this question, and the body explains why"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The summaries could not be read"))

	return r
}
