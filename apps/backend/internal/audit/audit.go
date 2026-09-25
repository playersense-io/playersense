// Package audit records what people did, for the customer whose account they did it to.
//
// This is a studio's own log, shown in their dashboard, and it answers one question: **this looks
// wrong, who did this.** `StaffAccess` is the other table and a different thing — PlayerSense staff
// reading customer data, with a ticket per read (§08). Neither substitutes for the other, and mixing
// them would produce one log where a customer's own administrator and one of our engineers appear as
// the same kind of event.
//
// # Why entries are written by hand and not by middleware
//
// Middleware records **requests**; an audit log has to record **decisions**. "Ada's role changed from
// developer to owner" is not recoverable from a request log: the previous value was never in the
// request. Neither is "version 7 published, three flags, no history lookups" or "$100 of promotional
// credit, expiring 17 December" — every one of those is known only to the handler that did the work.
//
// The cost of writing them by hand is that one can be forgotten. That cost is paid down by a test
// rather than by discipline: `TestEveryMutatingRouteDecidesAboutAudit` reads the OpenAPI document,
// finds every POST, PUT, PATCH and DELETE, and fails unless each is either recorded here or listed
// as deliberately exempt with a reason. Adding a route that changes something and saying nothing
// about it does not compile past the suite.
//
// # Append-only is a compile error, not a policy
//
// §08: "a log the operator can edit proves nothing." Every field on the entity is immutable and the
// mixin omits `updated_at`, so ent generates an update builder with no setter on it at all. There is
// nothing to call that would change what a row says.
//
// # Every entry is atomic with the change it describes
//
// [Record] takes a transaction, so the entry and the thing it describes commit together: an action
// that happened is an action that was logged, and a refused request leaves nothing behind. That held
// for most routes from the start and not for four of them, whose domain call opened its own
// transaction — accepting an invitation, changing project access, sending one, and creating an
// organization. Each was refactored to accept a hook that runs inside that transaction instead, right
// before commit (see [identity.CreateOrganization] for the shape of it), which is what closes the gap
// rather than working around it. There is no remaining path in this codebase that writes an audit
// entry outside the transaction it describes.
package audit

import (
	"context"
	"fmt"

	"playersense.dev/backend/ent"
)

// Action is what happened.
//
// Dotted names from the same family as the permissions, and on purpose: `member.invite` in the log is
// the thing `member.invite` the permission allows, so a reader comparing a role against a log is
// comparing two spellings of one word. The column is a string rather than an enum because logging a
// new kind of action must not be a migration — the day that friction exists is the day something
// stops being logged.
type Action string

// The vocabulary. Every mutating route on the control plane appears here or in the exempt list the
// coverage test carries.
const (
	OrganizationCreated Action = "organization.create"

	ProjectCreated           Action = "project.create"
	ProjectUpdated           Action = "project.update"
	RetentionSet             Action = "project.retention"
	ProjectDeletionScheduled Action = "project.delete"
	ProjectDeletionCancelled Action = "project.keep"

	KeyCreated Action = "key.create"
	KeyRevoked Action = "key.revoke"
	KeyRolled  Action = "key.roll"

	MemberInvited       Action = "member.invite"
	MemberJoined        Action = "member.join"
	MemberRemoved       Action = "member.remove"
	MemberRoleChanged   Action = "member.role"
	MemberAccessChanged Action = "member.access"
	InvitationRevoked   Action = "invitation.revoke"

	SegmentWritten   Action = "segment.write"
	SegmentArchived  Action = "segment.archive"
	FlagWritten      Action = "flag.write"
	FlagArchived     Action = "flag.archive"
	EventWritten     Action = "liveops_event.write"
	EventArchived    Action = "liveops_event.archive"
	ConfigPublished  Action = "liveops.publish"
	ConfigRolledBack Action = "liveops.rollback"

	ExperimentWritten   Action = "experiment.write"
	ExperimentDeleted   Action = "experiment.delete"
	ExperimentStarted   Action = "experiment.start"
	ExperimentTraffic   Action = "experiment.traffic"
	ExperimentStopped   Action = "experiment.stop"
	ExperimentConcluded Action = "experiment.conclude"
	ExperimentShipped   Action = "experiment.ship"

	FunnelWritten Action = "funnel.write"
	EventDefined  Action = "event.define"

	ChannelWritten Action = "channel.write"
	ChannelDeleted Action = "channel.delete"
	ReportWritten  Action = "report.write"
	ReportDeleted  Action = "report.delete"
	FunnelDeleted  Action = "funnel.delete"

	// A store the project verifies purchases with: connected or changed, disconnected, and its
	// notification URL replaced.
	StoreConnected    Action = "store.connect"
	StoreDisconnected Action = "store.disconnect"
	StoreURLRotated   Action = "store.rotate_url"

	CreditGranted  Action = "credit.grant"
	CreditCharged  Action = "credit.charge"
	AutoTopUpSet   Action = "billing.auto_topup"
	PlanChanged    Action = "billing.plan"
	CreditLimitSet Action = "billing.credit_limit"

	// Staff changed what an organization may use: an entitlement override, its member limit or its
	// automatic top-ups a day.
	EntitlementsSet Action = "billing.entitlements"

	// ADR 0045: an organization put on new terms, an invoice marked paid by a transfer Stripe did
	// not see, and a credit note issued on an invoice.
	ContractSet     Action = "billing.contract"
	InvoicePaid     Action = "billing.invoice_paid"
	InvoiceCredited Action = "billing.credit_note"
	PrivacyErasure  Action = "privacy.erase"
	PrivacyExport   Action = "privacy.export"
)

// Actor is who did it, as one of three things.
//
// A user and a key are edges so a dashboard can show a name; the label is what the row says when
// neither resolves. Written at the time rather than resolved at read time, because an entry that
// becomes "unknown" when somebody leaves the company has lost the thing it was for.
type Actor struct {
	UserID int
	KeyID  int

	// Label is the fallback, and the only thing an operator action has. It carries its own
	// caveat where one applies — see [StaffActor].
	Label string
}

// StaffActor names a PlayerSense staff member acting on a customer's account.
//
// Their address comes from the console's verified sign-in (core.ResolveStaff), and the label says
// who they work for, because a studio reading its own log should be able to tell its colleagues'
// changes from ours at a glance.
func StaffActor(email string) Actor {
	return Actor{Label: "PlayerSense staff " + email}
}

// Entry is one thing that happened.
type Entry struct {
	OrganizationID int
	Action         Action
	Actor          Actor

	// Target is what it happened to, as a type and an id rather than an edge per kind: a log that
	// needed a foreign key per target type could not record the deletion of the thing it points
	// at.
	TargetType string
	TargetID   string

	// Metadata is whatever makes the action explicable a year later — the role granted, the
	// previous value, the version published. Deliberately shapeless: an entry that could not say
	// what changed would be a timestamp.
	Metadata map[string]any

	IP string
}

func (e Entry) validate() error {
	switch {
	case e.OrganizationID == 0:
		// Refused rather than written against nothing. An entry with no organisation is an
		// entry no dashboard shows and no retention removes, which is the worst of both.
		return fmt.Errorf("an audit entry needs the organization it happened to")
	case e.Action == "":
		return fmt.Errorf("an audit entry needs an action")
	}
	return nil
}

// Record appends one entry inside the caller's transaction.
//
// The entry and the thing it describes commit together, which is what makes "every action that
// happened was logged" a property rather than an intention.
func Record(ctx context.Context, tx *ent.Tx, entry Entry) (*ent.AuditEvent, error) {
	if err := entry.validate(); err != nil {
		return nil, err
	}
	return write(ctx, tx.AuditEvent.Create(), entry)
}

func write(ctx context.Context, create *ent.AuditEventCreate, entry Entry) (*ent.AuditEvent, error) {
	create = create.
		SetOrganizationID(entry.OrganizationID).
		SetAction(string(entry.Action)).
		SetTargetType(entry.TargetType).
		SetTargetID(entry.TargetID).
		SetActorLabel(entry.Actor.Label).
		SetIP(entry.IP)
	if entry.Metadata != nil {
		create = create.SetMetadata(entry.Metadata)
	}
	if entry.Actor.UserID != 0 {
		create = create.SetActorUserID(entry.Actor.UserID)
	}
	if entry.Actor.KeyID != 0 {
		create = create.SetActorKeyID(entry.Actor.KeyID)
	}

	row, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("append to the audit log: %w", err)
	}
	return row, nil
}
