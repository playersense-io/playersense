package routers

import (
	"net/http"

	"muzak.dev/framework"

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

	return r
}
