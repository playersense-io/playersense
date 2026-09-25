// Package authz decides what a person may do, and where.
//
// The shape is the one the targeting engine uses, for the same reason: **the vocabulary is
// ours and closed, the composition is the organisation's and open.** We do not ship a list of
// job titles — a studio builds the roles it needs out of permissions we defined, exactly as it
// builds "whales" out of facts we defined.
//
// # Two axes, kept apart
//
// A role answers *what* somebody may do. A project access list answers *where*. Conflating
// them is what makes a permission system unreadable, so they are separate fields checked
// separately, and there is deliberately no `project.read` permission: whether you can see a
// project is the access list, not a permission.
//
// # Role management is not a permission
//
// Creating roles and assigning them is reserved to OWNER and ADMIN. If it were grantable it
// would be silently equivalent to OWNER — anyone holding it could write themselves a role
// containing everything — so the escalation question is removed rather than defended against.
package authz

import (
	"fmt"
	"sort"
	"strings"
)

// Permission is one thing somebody may do. The set is closed: a role naming something not in
// it is refused when it is saved, not ignored when it is checked.
type Permission string

// Scope says what a permission is about, so the checker knows whether a project applies
// rather than each call site remembering to pass one.
type Scope uint8

const (
	// ScopeOrganization covers the organisation itself: its members, its bill, its audit.
	ScopeOrganization Scope = iota
	// ScopeProject covers one game, and is meaningless without naming which.
	ScopeProject
)

// The vocabulary, namespaced by the part of the product it governs.
//
// The namespace is what "tool-level access" means in practice: `liveops.read` and
// `liveops.write` *are* "LiveOps viewer" and "LiveOps editor", without a second concept.
const (
	OrgRead   Permission = "org.read"
	OrgUpdate Permission = "org.update"
	OrgDelete Permission = "org.delete"

	MemberRead   Permission = "member.read"
	MemberInvite Permission = "member.invite"
	MemberRemove Permission = "member.remove"

	BillingRead   Permission = "billing.read"
	BillingManage Permission = "billing.manage"

	AuditRead Permission = "audit.read"

	ProjectCreate Permission = "project.create"
	ProjectUpdate Permission = "project.update"
	ProjectDelete Permission = "project.delete"

	KeyRead   Permission = "key.read"
	KeyCreate Permission = "key.create"
	KeyRevoke Permission = "key.revoke"

	AnalyticsRead Permission = "analytics.read"
	// Reading one player rather than a crowd: a directory of installs, one player's timeline,
	// the purchases ledger, the sessions and events as they arrive. Every answer names somebody
	// by the identifier the game gave them, so it is a separate grant from the aggregates, and a
	// studio can hand the numbers to somebody without handing over its players.
	PlayerRead Permission = "player.read"

	// Writing an event is reversible. Publishing one is not — players have already seen
	// it — so the two are separate permissions, and that separation is what replaces the
	// per-environment split: a junior drafts, a senior publishes.
	LiveOpsRead    Permission = "liveops.read"
	LiveOpsWrite   Permission = "liveops.write"
	LiveOpsPublish Permission = "liveops.publish"

	// Reports and the channels they notify: reading them, and building, running and
	// changing them. A report's rows are analytics, so running one needs analytics.read and
	// player.read as well; these say only who may shape and send them.
	ReportsRead  Permission = "reports.read"
	ReportsWrite Permission = "reports.write"

	PrivacyRead   Permission = "privacy.read"
	PrivacyErase  Permission = "privacy.erase"
	PrivacyExport Permission = "privacy.export"

	// Connecting the game's stores: the credentials its players' purchases are verified with.
	// Reading the purchases themselves is player.read.
	PurchasesConfigure Permission = "purchases.configure"

	// Webhook endpoints: reading them and their deliveries, and changing, testing and resending.
	// A delivery's body names players, so reading is its own grant.
	WebhooksRead  Permission = "webhooks.read"
	WebhooksWrite Permission = "webhooks.write"
)

// scopes is the vocabulary itself. A permission absent from this map does not exist.
var scopes = map[Permission]Scope{
	OrgRead:   ScopeOrganization,
	OrgUpdate: ScopeOrganization,
	OrgDelete: ScopeOrganization,

	MemberRead:   ScopeOrganization,
	MemberInvite: ScopeOrganization,
	MemberRemove: ScopeOrganization,

	BillingRead:   ScopeOrganization,
	BillingManage: ScopeOrganization,

	AuditRead: ScopeOrganization,

	// Creating a project is an organisation-level act — there is no project to name yet.
	// Updating and deleting one names it.
	ProjectCreate: ScopeOrganization,
	ProjectUpdate: ScopeProject,
	ProjectDelete: ScopeProject,

	KeyRead:   ScopeProject,
	KeyCreate: ScopeProject,
	KeyRevoke: ScopeProject,

	AnalyticsRead: ScopeProject,
	PlayerRead:    ScopeProject,

	LiveOpsRead:    ScopeProject,
	LiveOpsWrite:   ScopeProject,
	LiveOpsPublish: ScopeProject,

	PrivacyRead:   ScopeProject,
	PrivacyErase:  ScopeProject,
	PrivacyExport: ScopeProject,

	ReportsRead:  ScopeProject,
	ReportsWrite: ScopeProject,

	PurchasesConfigure: ScopeProject,

	WebhooksRead:  ScopeProject,
	WebhooksWrite: ScopeProject,
}

// reads is the half of the vocabulary that only looks.
//
// Derived from the name would be tempting — everything ending in `.read` — and wrong twice
// over: `privacy.export` does not end in read and writes a bundle of somebody's personal data,
// while a permission named `analytics.read_raw` would have to be remembered. A table is a
// table, and adding a permission without classifying it is a compile-time hole this file's
// own test closes.
//
// The distinction earns its place because an account can be **read-only**: somebody whose
// email address Firebase has not confirmed can sign in, open the dashboard and see their
// organisation, and change nothing until they click the link. A banner explaining that is a
// better first experience than a wall.
var reads = map[Permission]bool{
	OrgRead:       true,
	MemberRead:    true,
	BillingRead:   true,
	ReportsRead:   true,
	WebhooksRead:  true,
	AuditRead:     true,
	KeyRead:       true,
	AnalyticsRead: true,
	PlayerRead:    true,
	LiveOpsRead:   true,
	PrivacyRead:   true,
}

// Mutates reports whether exercising a permission changes something.
//
// Unknown permissions answer true. Anything outside the vocabulary is refused before this is
// reached, and "I do not recognise it, so it is safe" is the wrong direction to be wrong in.
func (p Permission) Mutates() bool { return !reads[p] }

// Known reports whether a permission is in the vocabulary.
func (p Permission) Known() bool {
	_, found := scopes[p]
	return found
}

// Scope returns what a permission is about. Meaningless for an unknown one, which is why
// every path that accepts a permission from outside validates it first.
func (p Permission) Scope() Scope { return scopes[p] }

// Namespace is the part before the dot — "liveops", "billing" — which is what a dashboard
// groups a role editor by.
func (p Permission) Namespace() string {
	name := string(p)
	if at := strings.IndexByte(name, '.'); at >= 0 {
		return name[:at]
	}
	return name
}

// All returns the vocabulary, sorted, for a role editor to render and for a test to assert
// against. Sorted rather than map order so two calls agree and a diff of a seeded role is
// readable.
func All() []Permission {
	out := make([]Permission, 0, len(scopes))
	for permission := range scopes {
		out = append(out, permission)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Validate checks a set of permissions against the vocabulary.
//
// Called when a role is saved, so a typo is an error a person sees while they are looking at
// the role that caused it. The alternative — accepting it and failing the check later — is a
// role that grants nothing and explains nothing, which is the failure this repository keeps
// finding in its own work.
func Validate(permissions []Permission) error {
	var unknown []string
	seen := make(map[Permission]bool, len(permissions))

	for _, permission := range permissions {
		if !permission.Known() {
			unknown = append(unknown, string(permission))
			continue
		}
		if seen[permission] {
			return fmt.Errorf("permission %q is listed twice", permission)
		}
		seen[permission] = true
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown permission(s): %s", strings.Join(unknown, ", "))
	}
	return nil
}
