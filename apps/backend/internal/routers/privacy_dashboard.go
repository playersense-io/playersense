package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// PrivacyRequests is §61 and §101 from the dashboard: the same erasures and exports a server key
// asks for, by a signed-in member with a reason, and the list of them. privacy.read to read,
// privacy.erase and privacy.export to ask.
func PrivacyRequests() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("privacy"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or request does not exist"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The database is unavailable"))
	base := "/organizations/{org}/projects/{project}/privacy"

	r.Get(base, handlers.ListPrivacyRequests,
		muzak.Summary("The project's data-subject requests"),
		muzak.Description("Erasures and exports, newest first, with who asked and why, where each has "+
			"got to, and a completed export's download."),
		core.Require(authz.PrivacyRead))

	r.Post(base+"/erasures", handlers.RequestErasure,
		muzak.Summary("Erase one player's data"),
		muzak.Description("Recorded and tombstoned at once, so the player's events stop being accepted "+
			"or served; removed by the worker over the following hours. An open request for the "+
			"same player is returned rather than a second made."),
		core.Require(authz.PrivacyErase), muzak.Status(http.StatusAccepted))

	r.Post(base+"/exports", handlers.RequestExport,
		muzak.Summary("Export one player's data"),
		muzak.Description("Everything held about the player, collected by the worker into one bundle "+
			"to download here."),
		core.Require(authz.PrivacyExport), muzak.Status(http.StatusAccepted),
		muzak.WithResponseDoc(http.StatusConflict, "The player has an erasure in progress"))

	r.Get(base+"/exports/{request_id}/download", handlers.DownloadDashboardExport,
		muzak.Summary("Download an export"),
		muzak.Description("Newline-delimited JSON, named after the request rather than the player."),
		core.Require(authz.PrivacyExport),
		muzak.WithResponseDoc(http.StatusConflict, "It is not ready yet"))
	return r
}
