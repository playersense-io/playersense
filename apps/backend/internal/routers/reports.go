package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Reports are the builder's graphs (reports.read to see them; reports.write to change and run
// them). A preview and a run read the project's rows, player by player, so they need
// analytics.read and player.read as well.
func Reports() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("reports"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project, report or run does not exist"))
	base := "/organizations/{org}/projects/{project}/reports"
	one := base + "/{report}"
	runs := core.Require(authz.ReportsWrite, authz.AnalyticsRead, authz.PlayerRead)
	bad := muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The graph is not one a report can be: the message says why")

	r.Get(base, handlers.ListReports,
		muzak.Summary("List reports"), muzak.Description("Each with its last run and when a schedule next runs it."),
		core.Require(authz.ReportsRead))
	r.Post(base, handlers.CreateReport,
		muzak.Summary("Save a report"),
		muzak.Description("A graph of sources (events, players, purchases, sessions), steps (filter, join, "+
			"group, formula, sort) and outputs (chart, table, export, notify), with the schedules that run it: "+
			"five-field cron lines in a timezone, joined to Start's top edge."),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusCreated), bad)
	r.Post(base+"/preview", handlers.PreviewReport,
		muzak.Summary("Preview a graph over the project's rows"),
		muzak.Description("Every node run once, sending nothing: its first fifty rows, how many there were, "+
			"why it cannot run if it cannot, and for a Notify whether it would send and what. Each source "+
			"reads at most fifty thousand rows of the last 7, 30 or 90 days."),
		runs, bad)
	r.Get(one, handlers.ReadReport, muzak.Summary("One report"), core.Require(authz.ReportsRead))
	r.Put(one, handlers.UpdateReport, muzak.Summary("Change a report"), core.Require(authz.ReportsWrite), bad)
	r.Delete(one, handlers.DeleteReport, muzak.Summary("Delete a report and its runs"),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusNoContent))

	r.Post(one+"/runs", handlers.StartRun,
		muzak.Summary("Run a report now"),
		muzak.Description("As a test: it sends for real, marked as a test, unless dry, which sends nothing and "+
			"says what it would have. focus runs one Notify and what it reads. Answered at once; follow "+
			"stream_url for its events."),
		runs, muzak.Status(http.StatusAccepted),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Too many reports are running"))
	r.Get(one+"/runs", handlers.ListRuns, muzak.Summary("A report's latest runs"), core.Require(authz.ReportsRead))
	r.Get(one+"/runs/{run}", handlers.ReadRun, muzak.Summary("One run, with the files it wrote"),
		core.Require(authz.ReportsRead))
	r.Post(one+"/runs/{run}/cancel", handlers.CancelRun,
		muzak.Summary("Stop a run"), muzak.Description("Where it is; what it already sent stays sent."),
		core.Require(authz.ReportsWrite), muzak.WithResponseDoc(http.StatusConflict, "The run has finished"))
	r.Get(one+"/runs/{run}/exports/{node}", handlers.DownloadReportExport,
		muzak.Summary("Download a file a run wrote"), muzak.Description("Kept seven days."), runs)
	r.SSE(one+"/runs/{run}/stream", handlers.FollowRunSignedIn,
		muzak.Summary("Follow a run, signed in"),
		muzak.Description("The same events as stream_url, for a client that sends an Authorization header."),
		core.Require(authz.ReportsRead))
	return r
}

// ReportStreams is a run's event stream opened with the token its start returned, since a
// browser's EventSource cannot send an Authorization header. Mounted outside the signed-in
// routes; the token is the credential, for one run and an hour.
func ReportStreams() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("reports"))
	r.SSE("/report-runs/{run}/events", handlers.FollowRun,
		muzak.Summary("Follow a run"),
		muzak.Description("Server-sent events named run_started, node_started, node_finished, node_failed, "+
			"node_skipped, delivery and run_finished, numbered so a reconnecting browser resumes from its "+
			"Last-Event-ID, until the run finishes."),
		muzak.Needs(handlers.StreamAccess),
		muzak.WithResponseDoc(http.StatusUnauthorized, "The token is missing, lapsed, or for another run"))
	return r
}
