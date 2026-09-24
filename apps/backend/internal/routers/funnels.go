package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// SavedFunnels is a project's funnels kept by name (§31). Saving one is a view of the data rather
// than a change to it, so reading the numbers is what it takes: analytics.read.
func SavedFunnels() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("analytics"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or funnel does not exist"))
	base := "/organizations/{org}/projects/{project}/funnels"
	taken := muzak.WithResponseDoc(http.StatusConflict, "The project already has a funnel by that name")

	r.Get(base, handlers.ListSavedFunnels,
		muzak.Summary("List saved funnels"),
		core.Require(authz.AnalyticsRead))
	r.Post(base, handlers.CreateSavedFunnel,
		muzak.Summary("Save a funnel"),
		muzak.Description("The definition a funnel query takes, without its range: kind, window, steps "+
			"or progression, and breakdown. Checked as the query checks it."),
		core.Require(authz.AnalyticsRead), muzak.Status(http.StatusCreated), taken)
	r.Get(base+"/{funnel}", handlers.ReadSavedFunnel,
		muzak.Summary("One saved funnel"),
		core.Require(authz.AnalyticsRead))
	r.Put(base+"/{funnel}", handlers.UpdateSavedFunnel,
		muzak.Summary("Change a saved funnel"),
		core.Require(authz.AnalyticsRead), taken)
	r.Delete(base+"/{funnel}", handlers.DeleteSavedFunnel,
		muzak.Summary("Forget a saved funnel"),
		core.Require(authz.AnalyticsRead), muzak.Status(http.StatusNoContent))
	return r
}
