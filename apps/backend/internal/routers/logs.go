package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Logs returns Cloud Logging's dashboard routes (ADR 0050).
//
// Mounted beside Analytics and for the same reason: the records live in this cell's ClickHouse and
// are answered here. logs.read is its own grant, because a log line is free text the game wrote
// and may name a player; a player's own identifier additionally needs player.read, and is left out
// of every answer to somebody without it.
func Logs() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("logs"),
		muzak.WithResponseDoc(http.StatusNotFound,
			"The organization, project or environment does not exist, or you cannot see it"))

	const base = "/organizations/{org}/projects/{project}/logs/{environment}"
	unreadable := muzak.WithResponseDoc(http.StatusServiceUnavailable, "The logs could not be read")

	r.Get(base+"/histogram", handlers.LogHistogram,
		muzak.Summary("Count records over time"),
		muzak.Description("One bar per step across the range, about ninety of them, each split by level. "+
			"From per-minute counts when the search is only levels and kinds, from the records otherwise."),
		core.Require(authz.LogsRead), unreadable)

	r.Get(base+"/records", handlers.LogRecords,
		muzak.Summary("List records, newest first"),
		muzak.Description("One page of the records matching the search, newest first, with a cursor to the "+
			"next. Each message is cut at 400 bytes; read the record for the whole of it."),
		core.Require(authz.LogsRead), unreadable)

	r.Get(base+"/records/newer", handlers.LogNewer,
		muzak.Summary("Count records newer than the newest shown"),
		muzak.Description("What the live view says is waiting above the page being read."),
		core.Require(authz.LogsRead), unreadable)

	r.Get(base+"/records/{record}", handlers.LogRecord,
		muzak.Summary("Read one record whole"),
		muzak.Description("The whole message, the stack, the breadcrumbs, the device and every attribute."),
		core.Require(authz.LogsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No such record, or it is past the retention"),
		unreadable)

	r.Get(base+"/summary", handlers.LogSummary,
		muzak.Summary("Describe the range"),
		muzak.Description("Records, warnings, errors, exceptions, crashes and bytes, and the issues that "+
			"happened, against the range before it."),
		core.Require(authz.LogsRead), unreadable)

	r.Get(base+"/issues", handlers.LogIssues,
		muzak.Summary("List issues"),
		muzak.Description("Every exception, crash and error log grouped into issues, with how often each "+
			"happened in the range, to how many installs, and its trend."),
		core.Require(authz.LogsRead), unreadable)

	r.Get(base+"/issues/{issue}", handlers.LogIssue,
		muzak.Summary("Describe one issue"),
		muzak.Description("Its series over the range, where it happens (release, platform, OS, device, "+
			"country) and its newest record."),
		core.Require(authz.LogsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No such issue in this environment"),
		unreadable)

	r.Put(base+"/issues/{issue}", handlers.SetLogIssue,
		muzak.Summary("Resolve, ignore or reopen an issue"),
		muzak.Description("A resolved issue seen again afterwards is listed as regressed."),
		core.Require(authz.LogsWrite),
		muzak.MaxBodySize(1<<10))

	r.Get("/organizations/{org}/projects/{project}/log-storage", handlers.LogStorage,
		muzak.Summary("Describe a project's log storage"),
		muzak.Description("What the project keeps now, for how long, what it took in over the last "+
			"thirty days, and what log storage has cost the organization this month."),
		core.Require(authz.LogsRead), unreadable)

	r.Put("/organizations/{org}/projects/{project}/log-storage", handlers.SetLogRetention,
		muzak.Summary("Change how long a project keeps its logs"),
		muzak.Description("New records take the change within minutes. A shorter retention is enforced "+
			"on older records too, and billing follows it at once."),
		core.Require(authz.ProjectUpdate),
		muzak.MaxBodySize(1<<10))

	return r
}
