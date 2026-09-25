package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Notifications are rules that route events to channels (ADR 0051). A project's rules hear its
// events and go by the channels' permissions, reports.read and reports.write. The organization's
// rules hear its account's events, a failed top-up or a suspension, and go by billing.read and
// billing.manage: the people who deal with the money decide who else hears about it.
func Notifications() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("notifications"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or rule does not exist"))
	bad := muzak.WithResponseDoc(http.StatusUnprocessableEntity,
		"An event the rule's scope does not have, an unknown environment, or a channel it cannot reach")

	r.Get("/organizations/{org}/notification-events", handlers.ListNotificationKinds,
		muzak.Summary("What a notification rule can hear"),
		muzak.Description("Every event type, the organization's own (its account and money) and its projects', "+
			"with a headline, a sentence, and whether the owners are always emailed about it."),
		core.Require(authz.OrgRead))

	project := "/organizations/{org}/projects/{project}/notification-rules"
	r.Get(project, handlers.ListNotificationRules,
		muzak.Summary("List a project's notification rules"),
		core.Require(authz.ReportsRead))
	r.Post(project, handlers.CreateNotificationRule,
		muzak.Summary("Make a notification rule"),
		muzak.Description("These events, from these environments, to these channels, at most this often. "+
			"The channels are the project's own."),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusCreated), bad)
	r.Put(project+"/{rule}", handlers.UpdateNotificationRule,
		muzak.Summary("Change a notification rule"),
		core.Require(authz.ReportsWrite), bad)
	r.Delete(project+"/{rule}", handlers.DeleteNotificationRule,
		muzak.Summary("Remove a notification rule"),
		core.Require(authz.ReportsWrite), muzak.Status(http.StatusNoContent))
	r.Post(project+"/{rule}/test", handlers.TestNotificationRule,
		muzak.Summary("Send a rule's test"),
		muzak.Description("A sample of the rule's first event, sent now to each of its channels, marked as a "+
			"test and kept in each channel's log."),
		core.Require(authz.ReportsWrite), bad)

	org := "/organizations/{org}/notification-rules"
	r.Get(org, handlers.ListOrgNotificationRules,
		muzak.Summary("List the organization's notification rules"),
		core.Require(authz.BillingRead))
	r.Post(org, handlers.CreateOrgNotificationRule,
		muzak.Summary("Make a rule of the organization"),
		muzak.Description("For the organization's own events: a failed top-up, a balance below zero, a "+
			"suspension. Its channels may be any project's. The owners, admins and billing members are "+
			"emailed about these whatever the rules say."),
		core.Require(authz.BillingManage), muzak.Status(http.StatusCreated), bad)
	r.Put(org+"/{rule}", handlers.UpdateOrgNotificationRule,
		muzak.Summary("Change a rule of the organization"),
		core.Require(authz.BillingManage), bad)
	r.Delete(org+"/{rule}", handlers.DeleteOrgNotificationRule,
		muzak.Summary("Remove a rule of the organization"),
		core.Require(authz.BillingManage), muzak.Status(http.StatusNoContent))
	r.Post(org+"/{rule}/test", handlers.TestOrgNotificationRule,
		muzak.Summary("Send a test of a rule of the organization"),
		core.Require(authz.BillingManage), bad)
	r.Get("/organizations/{org}/notification-channels", handlers.ListOrgChannels,
		muzak.Summary("Channels a rule of the organization can notify"),
		muzak.Description("Every channel in the organization's projects, by name and type. Where each sends "+
			"is shown by its project."),
		core.Require(authz.BillingRead))
	return r
}
