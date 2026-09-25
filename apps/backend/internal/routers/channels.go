package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Channels are where a project's reports notify: reports.read to see them and their log,
// reports.write to change them or send a test.
func Channels() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("channels"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or channel does not exist"))
	base := "/organizations/{org}/projects/{project}/channels"
	bad := muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The destination cannot work: the message says why")

	r.Get(base, handlers.ListChannels,
		muzak.Summary("List notification channels"),
		muzak.Description("Each with where it sends (a webhook URL masked), whether it is on, and how its last test went."),
		core.Require(authz.ReportsRead))
	r.Post(base, handlers.CreateChannel,
		muzak.Summary("Set up a channel"),
		muzak.Description("An email list, a Slack, Discord or Microsoft Teams incoming webhook, a PagerDuty "+
			"service's Events API key, or a webhook of yours. Incoming webhooks rather than apps: the URL "+
			"is made in your own workspace and grants nothing else. A webhook channel is given a signing "+
			"secret, returned this once; every POST carries X-PlayerSense-Id, -Timestamp and -Signature."),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusCreated), bad)
	r.Post(base+"/test", handlers.TestDraftChannel,
		muzak.Summary("Test a channel before saving it"),
		core.Require(authz.ReportsWrite), bad)
	r.Get(base+"/deliveries", handlers.ListChannelDeliveries,
		muzak.Summary("What was sent to the channels"),
		muzak.Description("Every send, a test or a report's, newest first, with whether it arrived and what the "+
			"destination answered. Kept ninety days."),
		core.Require(authz.ReportsRead))
	r.Get(base+"/{channel}", handlers.ReadChannel,
		muzak.Summary("One channel"),
		core.Require(authz.ReportsRead))
	r.Put(base+"/{channel}", handlers.UpdateChannel,
		muzak.Summary("Change a channel"),
		muzak.Description("Its name, whether it is on, and where it sends. A secret not sent is kept."),
		core.Require(authz.ReportsWrite), bad)
	r.Delete(base+"/{channel}", handlers.DeleteChannel,
		muzak.Summary("Remove a channel"),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusNoContent))
	r.Post(base+"/{channel}/test", handlers.TestChannel,
		muzak.Summary("Send a test"),
		muzak.Description("Kept as the channel's last test, which is what its status says, and in its log."),
		core.Require(authz.ReportsWrite))
	return r
}
