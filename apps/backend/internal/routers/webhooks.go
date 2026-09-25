package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// Webhooks is a project's endpoints and their delivery log (§49-51): webhooks.read to see them,
// webhooks.write to change, test and resend.
func Webhooks() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("webhooks"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project, endpoint or delivery does not exist"))
	base := "/organizations/{org}/projects/{project}/webhooks"
	bad := muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The endpoint cannot work: the message says why")
	unreadable := muzak.WithResponseDoc(http.StatusServiceUnavailable, "The delivery log could not be read")

	r.Get("/webhook-events", handlers.WebhookCatalog,
		muzak.Summary("Every webhook event type"),
		muzak.Description("What each is, which belong to an environment, and which PlayerSense sends yet."))

	r.Get(base, handlers.ListWebhooks,
		muzak.Summary("List webhook endpoints"),
		muzak.Description("Each with what it hears, whether it is on and healthy, what it is owed, and its last 24 hours."),
		core.Require(authz.WebhooksRead))
	r.Post(base, handlers.CreateWebhook,
		muzak.Summary("Add an endpoint"),
		muzak.Description("An https URL on a public address, the events it hears (* for every one, those added later "+
			"included) and the environments it hears them from. Its signing secret is returned this once. Every "+
			"event is POSTed as JSON with X-PlayerSense-Id (the event's id, the same on every try), "+
			"X-PlayerSense-Timestamp and X-PlayerSense-Signature: v1= and the HMAC-SHA256 of timestamp, a dot and "+
			"the body. An answer outside 2xx, or none in ten seconds, is tried again after 1 and 5 minutes, half "+
			"an hour, 2, 6 and 12 hours, then a day twice, and an endpoint that has answered nothing but failures for "+
			"three days is turned off and its owners told."),
		core.Require(authz.WebhooksWrite), muzak.Status(http.StatusCreated), bad)
	r.Get(base+"/{webhook}", handlers.ReadWebhook,
		muzak.Summary("One endpoint"),
		muzak.Description("With its last day and who changed it."),
		core.Require(authz.WebhooksRead))
	r.Put(base+"/{webhook}", handlers.UpdateWebhook,
		muzak.Summary("Change an endpoint"),
		muzak.Description("A secret header sent without a value keeps the one saved. Turning it off cancels the "+
			"deliveries it is owed; turning it back on gives it three days again."),
		core.Require(authz.WebhooksWrite), bad)
	r.Delete(base+"/{webhook}", handlers.DeleteWebhook,
		muzak.Summary("Remove an endpoint"),
		core.Require(authz.WebhooksWrite), muzak.Status(http.StatusNoContent))
	r.Get(base+"/{webhook}/secret", handlers.RevealWebhookSecret,
		muzak.Summary("Show the signing secret"),
		muzak.Description("Recorded in the audit log."),
		core.Require(authz.WebhooksWrite))
	r.Post(base+"/{webhook}/secret/roll", handlers.RollWebhookSecret,
		muzak.Summary("Roll the signing secret"),
		muzak.Description("A new secret, returned this once. The old one keeps signing beside it for "+
			"expire_after_hours (a day when absent, up to a week, 0 to stop it now), so both signatures are sent "+
			"comma-separated and a receiver can move over."),
		core.Require(authz.WebhooksWrite))
	r.Post(base+"/{webhook}/test", handlers.TestWebhook,
		muzak.Summary("Send a test event"),
		muzak.Description("A made-up event of a type, sent now: the answer is how the endpoint took it. Kept in the "+
			"delivery log, marked a test, and never retried."),
		core.Require(authz.WebhooksWrite), bad, unreadable)
	r.Get(base+"/{webhook}/health", handlers.WebhookHealth,
		muzak.Summary("An endpoint's health"),
		muzak.Description("Its last 24 hours against the 24 before: deliveries, the share landed at the first try, "+
			"the share of tries answered 2xx, how many gave up, and the median and slowest answers; with its "+
			"tries hour by hour and what it is owed now."),
		core.Require(authz.WebhooksRead), unreadable)
	r.Get(base+"/{webhook}/deliveries", handlers.ListWebhookDeliveries,
		muzak.Summary("An endpoint's deliveries"),
		muzak.Description("Newest first, thirty days of them, by status and event type."),
		core.Require(authz.WebhooksRead), unreadable)
	r.Get(base+"/{webhook}/deliveries/{delivery}", handlers.ReadWebhookDelivery,
		muzak.Summary("One delivery"),
		muzak.Description("Every try, with its answer, and the event exactly as it was sent."),
		core.Require(authz.WebhooksRead), unreadable)
	r.Post(base+"/{webhook}/deliveries/{delivery}/resend", handlers.ResendWebhookDelivery,
		muzak.Summary("Send a delivery again"),
		muzak.Description("Now, as one try outside its schedule, with the same event id. A delivery still retrying "+
			"keeps its schedule if this try fails."),
		core.Require(authz.WebhooksWrite), unreadable)
	r.Post(base+"/{webhook}/resend-failed", handlers.ResendFailedWebhooks,
		muzak.Summary("Send again everything that gave up"),
		muzak.Description("Every delivery to the endpoint that gave up since a moment (three days ago when absent), "+
			"queued for the worker: what the endpoint missed while it was down. Up to ten thousand."),
		core.Require(authz.WebhooksWrite), unreadable,
		muzak.WithResponseDoc(http.StatusConflict, "The endpoint is off"))
	return r
}

// OperatorWebhooks is webhooks as staff see them.
func OperatorWebhooks() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"))
	lookup := core.RequireStaff(staff.Support, staff.Ops)

	r.Get("/webhooks", handlers.OperatorWebhooks,
		lookup, core.StaffReads("webhooks.overview"),
		muzak.Summary("Webhooks across the platform"),
		muzak.Description("The last day's deliveries, the outbox and its lag, the retries owed and due, the endpoints "+
			"with the most failed tries, and those turned off for failing in the last week."))
	r.Get("/projects/{project}/webhooks/{webhook}/deliveries", handlers.OperatorWebhookDeliveries,
		lookup, core.StaffReads("webhooks.deliveries"),
		muzak.Summary("A customer endpoint's deliveries"))
	r.Get("/projects/{project}/webhooks/{webhook}/deliveries/{delivery}", handlers.OperatorWebhookDelivery,
		lookup, core.StaffReads("webhooks.delivery"),
		muzak.Summary("A customer endpoint's delivery"),
		muzak.Description("Every try and the event as it was sent."))
	return r
}
