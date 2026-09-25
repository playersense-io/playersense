// Package webhooks sends a project's events to the studio's own HTTPS endpoints (project.md
// §49-51, ADR 0049): what happened in the business, a verified purchase or a published config,
// never the raw analytics.
//
// The path of one event:
//
//	a change and its event, in one PostgreSQL transaction (Emit -> webhook_outbox)
//	-> the worker claims a batch of events (Dispatcher)
//	-> each is POSTed, signed, to every endpoint that hears it, at once
//	-> a try that fails, or an endpoint busy or failing, becomes a row in webhook_retries
//	-> every try is written to ClickHouse (AttemptLog), and the event's row is deleted
//
// PostgreSQL holds only what is in flight. A delivery that lands at its first try, which is almost
// every delivery, is never written to PostgreSQL at all, so millions a day cost ClickHouse rows
// and nothing else.
package webhooks

// APIVersion is the shape events are written in. One version: there is nothing live to keep an
// older one for.
const APIVersion = "2026-09-01"

// EveryEvent is an endpoint's way of hearing every event, those added later included.
const EveryEvent = "*"

// EventType is one kind of event.
type EventType struct {
	Type  string `json:"type"`
	Group string `json:"group"`
	// Summary is what happened, as a sentence.
	Summary string `json:"summary"`
	// Environments is whether the event belongs to an environment, and so is heard only by
	// endpoints listening to it. A project-level event is heard by every endpoint.
	Environments bool `json:"environments"`
	// Emitted is whether PlayerSense sends it yet. An endpoint may listen to one that is not, and
	// will hear it once it is.
	Emitted bool `json:"emitted"`
}

// Catalog is every event type, in the dashboard's order.
var Catalog = []EventType{
	{"purchase.verified", "purchases", "A purchase was confirmed with the store. Grant the item on this one.", true, true},
	{"purchase.rejected", "purchases", "A receipt did not check out with the store. Nothing should be granted.", true, true},
	{"purchase.refunded", "purchases", "The store refunded a purchase. Take back what it granted, if you do.", true, true},
	{"subscription.started", "subscriptions", "A player subscribed, trial included.", true, false},
	{"subscription.renewed", "subscriptions", "A subscription renewed for another period.", true, false},
	{"subscription.canceled", "subscriptions", "A player turned renewal off. It stays on until its period ends.", true, false},
	{"subscription.expired", "subscriptions", "A subscription ended and did not renew.", true, false},
	{"subscription.billing_issue", "subscriptions", "The store could not charge for a renewal and is retrying.", true, false},
	{"config.published", "publishing", "A config was published to an environment.", true, true},
	{"config.rolled_back", "publishing", "An environment was rolled back to an earlier version.", true, true},
	{"liveops.window.started", "liveops", "A Live Ops event came on: one of its windows opened.", true, false},
	{"liveops.window.ended", "liveops", "A Live Ops event's window closed.", true, false},
	{"issue.new", "issues", "An issue appeared that was never seen before: a new crash, exception or error.", true, true},
	{"issue.regressed", "issues", "An issue you resolved happened again, and is unresolved once more.", true, true},
	{"issue.spiking", "issues", "An issue is happening far more often than it usually does.", true, true},
	{"experiment.started", "experiments", "An experiment started taking players.", false, true},
	{"experiment.concluded", "experiments", "An experiment ended with a winner, or with none.", false, true},
	{"alert.triggered", "alerts", "A metric crossed a line an alert watches.", false, false},
	{"alert.resolved", "alerts", "A metric came back inside its line.", false, false},
	{"export.completed", "data", "An export finished. Its files are ready to download.", false, true},
	{"export.failed", "data", "An export could not be finished.", false, true},
	{"player.deleted", "data", "A player's data was erased, on their request or yours.", false, true},
	{"member.added", "project", "Somebody joined the project.", false, true},
	{"member.removed", "project", "Somebody left the project or was removed from it.", false, true},
	{"api_key.created", "project", "A key was made.", false, true},
	{"api_key.revoked", "project", "A key was revoked. Anything still using it is refused from now.", false, true},
	{"usage.threshold_reached", "project", "The project reached a share of its plan.", false, false},
}

var known = func() map[string]EventType {
	m := map[string]EventType{}
	for _, e := range Catalog {
		m[e.Type] = e
	}
	return m
}()

// Known reports whether a type is one PlayerSense has.
func Known(t string) bool { _, ok := known[t]; return ok }

// Hears reports whether an endpoint's event list includes a type.
func Hears(events []string, t string) bool {
	for _, e := range events {
		if e == EveryEvent || e == t {
			return true
		}
	}
	return false
}

// HearsFrom reports whether an endpoint's environments include an event's. An event of no
// environment is the project's, and every endpoint hears it.
func HearsFrom(environments []string, env string) bool {
	if env == "" {
		return true
	}
	for _, e := range environments {
		if e == env {
			return true
		}
	}
	return false
}
