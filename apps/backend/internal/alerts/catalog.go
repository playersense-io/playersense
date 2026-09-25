// Package alerts decides who hears about what (ADR 0051): notification rules, which route events to
// the channels a studio has set up, and the queue that delivers them.
//
// Three layers, kept apart so each can change alone:
//
//	channels  where: an email list, a Slack channel, PagerDuty (internal/notify)
//	events    what can happen: one catalog, the webhooks' project events plus the organization's own
//	rules     the routing: these events, from these environments, to these channels, this often
//
// The path of one notification:
//
//	an event of the organization, in the transaction that made it (Publish -> notification_outboxes)
//	an event of a project, from the webhook dispatcher, which already takes every one (Sink)
//	-> one row per rule and channel, after the rule's cooldown
//	-> the worker sends it, logs it with the channel's deliveries, and retries a failure
//
// Some events are too important to leave to a rule somebody may never write: the money ones. Their
// kind says Owners, and the organization's owners, admins and billing members are emailed whatever
// the rules say, at most once a day per event type. A rule adds Slack or PagerDuty on top; it
// cannot take the email away.
package alerts

import (
	"strings"

	"playersense.dev/backend/internal/webhooks"
)

// Scope is whose events a kind is.
type Scope string

// The scopes.
const (
	// ScopeProject is an event of one project, heard by that project's rules.
	ScopeProject Scope = "project"
	// ScopeOrganization is an event of the organization, its account and its money, heard by the
	// organization's rules.
	ScopeOrganization Scope = "organization"
)

// Every is a rule's way of hearing every event of its scope, those added later included.
const Every = "*"

// Kind is one type of event a rule can hear.
type Kind struct {
	Type  string `json:"type"`
	Group string `json:"group"`
	// Title is the notification's headline: "Automatic top-up failed".
	Title string `json:"title"`
	// Summary is what happened, as a sentence.
	Summary string `json:"summary"`
	Scope   Scope  `json:"scope"`
	// Environments is whether the event belongs to an environment, so a rule's environments apply.
	Environments bool `json:"environments"`
	// Emitted is whether PlayerSense sends it yet. A rule may hear one that is not.
	Emitted bool `json:"emitted"`
	// Severity is how it is raised where that means something (PagerDuty): critical, error,
	// warning or info.
	Severity string `json:"severity"`
	// Owners is whether the organization's owners, admins and billing members are always emailed.
	Owners bool `json:"owners"`
}

// The organization's events.
const (
	TopUpFailed      = "billing.topup_failed"
	PaymentOverdue   = "billing.payment_overdue"
	AccountSuspended = "billing.account_suspended"
	AccountRestored  = "billing.account_restored"
)

var organizationKinds = []Kind{
	{Type: TopUpFailed, Group: "billing", Title: "Automatic top-up failed",
		Summary:  "A saved card could not be charged for an automatic top-up. It is tried again later.",
		Severity: "error", Owners: true, Emitted: true},
	{Type: PaymentOverdue, Group: "billing", Title: "Balance below zero",
		Summary: "The balance fell below zero, so the account is in its grace period. Top up before it " +
			"ends, or new events are refused.",
		Severity: "warning", Owners: true, Emitted: true},
	{Type: AccountSuspended, Group: "billing", Title: "Ingestion suspended",
		Summary: "The account owes too much, or for too long, so new events are refused until it is " +
			"paid. Devices keep them meanwhile.",
		Severity: "critical", Owners: true, Emitted: true},
	{Type: AccountRestored, Group: "billing", Title: "Ingestion restored",
		Summary:  "The account is paid up, and new events are accepted again.",
		Severity: "info", Owners: true, Emitted: true},
}

// titles are the headlines a project event's type does not spell well by itself.
var titles = map[string]string{
	"liveops.window.started":     "Live Ops window opened",
	"liveops.window.ended":       "Live Ops window closed",
	"api_key.created":            "API key created",
	"api_key.revoked":            "API key revoked",
	"usage.threshold_reached":    "Usage threshold reached",
	"subscription.billing_issue": "Subscription billing issue",
}

// severities are the project events worth more than "info" to an on-call rotation.
var severities = map[string]string{
	"purchase.rejected":          "warning",
	"subscription.billing_issue": "warning",
	"export.failed":              "error",
	"alert.triggered":            "error",
	"api_key.revoked":            "warning",
}

// Catalog is every kind, the organization's first, then the projects' in the webhooks' order.
var Catalog = func() []Kind {
	out := make([]Kind, 0, len(organizationKinds)+len(webhooks.Catalog))
	for _, k := range organizationKinds {
		k.Scope = ScopeOrganization
		out = append(out, k)
	}
	for _, e := range webhooks.Catalog {
		title := titles[e.Type]
		if title == "" {
			title = headline(e.Type)
		}
		severity := severities[e.Type]
		if severity == "" {
			severity = "info"
		}
		out = append(out, Kind{Type: e.Type, Group: e.Group, Title: title, Summary: e.Summary,
			Scope: ScopeProject, Environments: e.Environments, Emitted: e.Emitted, Severity: severity})
	}
	return out
}()

var byType = func() map[string]Kind {
	m := make(map[string]Kind, len(Catalog))
	for _, k := range Catalog {
		m[k.Type] = k
	}
	return m
}()

// Lookup is one kind by its type.
func Lookup(t string) (Kind, bool) {
	k, ok := byType[t]
	return k, ok
}

// Valid reports whether a rule of a scope may hear these types: every one known and of that scope,
// or "*".
func Valid(scope Scope, types []string) (string, bool) {
	for _, t := range types {
		if t == Every {
			continue
		}
		k, ok := byType[t]
		if !ok || k.Scope != scope {
			return t, false
		}
	}
	return "", true
}

// Hears reports whether a rule's events include a type.
func Hears(events []string, t string) bool {
	for _, e := range events {
		if e == Every || e == t {
			return true
		}
	}
	return false
}

// HearsFrom reports whether a rule's environments include an event's. None is every environment,
// and an event of no environment is heard whatever the rule says.
func HearsFrom(environments []string, env string) bool {
	if env == "" || len(environments) == 0 {
		return true
	}
	for _, e := range environments {
		if e == env {
			return true
		}
	}
	return false
}

// headline turns "config.rolled_back" into "Config rolled back".
func headline(t string) string {
	words := strings.FieldsFunc(t, func(r rune) bool { return r == '.' || r == '_' })
	s := strings.Join(words, " ")
	if s == "" {
		return t
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
