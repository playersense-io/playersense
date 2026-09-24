package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// The customer's own log: who did what to their account.
//
// Not PlayerSense's. `StaffAccess` is the record of our engineers reading customer data, and it is a
// separate table with a ticket per read (§08) — one log holding both would be a screen where a
// studio's administrator and one of our staff appear as the same kind of event.

// AuditEventOut is one thing that happened.
type AuditEventOut struct {
	ID     uuid.UUID `json:"id"`
	Action string    `json:"action" doc:"A dotted name from the same family as the permissions: member.invite, liveops.publish"`

	// Actor is who did it, rendered. A name and address when a person did it, the key's label when
	// a credential did, and "PlayerSense staff" with their verified address when our staff did.
	Actor      string `json:"actor"`
	ActorEmail string `json:"actor_email,omitzero" doc:"Present only when a signed-in person did it"`

	TargetType string `json:"target_type,omitzero"`
	TargetID   string `json:"target_id,omitzero"`

	// Metadata is what makes the entry explicable a year later: the role granted, the previous
	// value, the version published. Shapeless by design.
	Metadata map[string]any `json:"metadata,omitzero"`

	IP string    `json:"ip,omitzero"`
	At time.Time `json:"at"`
}

// AuditListOut is a page of the log.
type AuditListOut struct {
	Events []AuditEventOut `json:"events"`
}

// AuditParams filters and pages the log.
type AuditParams struct {
	OrgSlug string `path:"org"`

	// Action narrows to one kind, because "who published" and "who changed a role" are different
	// questions and the second is buried under the first on a busy account.
	Action string `query:"action,omitzero" doc:"One action name, exactly"`

	Limit int `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *AuditParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Action).Trim().Lower().MaxLen(64)
	v.Number(&in.Limit).Min(1).Max(200)
}
