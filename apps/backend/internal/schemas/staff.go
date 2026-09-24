package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// StaffTicket is embedded in every operator input that reaches one customer's records (§104).
//
// core.StaffReads enforces it and writes the staff_access row; this field is what puts the header
// in the API's description, so a console knows to send it and a test can check every such route
// asks for it.
type StaffTicket struct {
	Ticket string `header:"X-Staff-Ticket" required:"true" doc:"A ticket reference for why this customer's data is being read: recorded in staff_access"`
	Note   string `header:"X-Staff-Note" doc:"An optional line beside the ticket"`
}

// OperatorOrganizationParams names one customer organization, for an operator route.
type OperatorOrganizationParams struct {
	StaffTicket
	Slug string `path:"org" doc:"The organization's slug"`
}

func (in *OperatorOrganizationParams) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
}

// StaffMemberOut is one member of PlayerSense staff.
type StaffMemberOut struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name,omitzero"`
	Roles         []string   `json:"roles" doc:"ops, support, billing or admin"`
	Active        bool       `json:"active"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitzero"`
	You           bool       `json:"you,omitzero"`
}

// StaffListOut is everybody who is, or was, staff: the access review §104 asks for.
type StaffListOut struct {
	Staff []StaffMemberOut `json:"staff"`
}

// AddStaffIn adds somebody to the console.
type AddStaffIn struct {
	StaffTicket
	Email string   `json:"email"`
	Name  string   `json:"name,omitzero"`
	Roles []string `json:"roles" doc:"At least one of ops, support, billing, admin"`
}

func (in *AddStaffIn) Validate(v *muzak.Validation) {
	v.String(&in.Email).Trim().Lower().Required().Email().MaxLen(254)
	v.String(&in.Name).Trim().MaxLen(120)
	v.Slice(&in.Roles).Required().MinItems(1).MaxItems(4)
}

// UpdateStaffIn changes a staff member's name, roles or whether they may sign in.
type UpdateStaffIn struct {
	StaffTicket
	StaffID uuid.UUID `path:"staff_id"`
	Name    *string   `json:"name,omitzero"`
	Roles   []string  `json:"roles,omitzero" doc:"Replaces every role; at least one"`
	Active  *bool     `json:"active,omitzero" doc:"false deactivates; the row stays for the access review"`
}

func (in *UpdateStaffIn) Validate(v *muzak.Validation) {
	if in.Roles != nil {
		v.Slice(&in.Roles).MinItems(1).MaxItems(4)
	}
}

// StaffAccessParams filters the record of staff reads.
type StaffAccessParams struct {
	Staff  string `query:"staff,omitzero" doc:"One staff member's address"`
	Org    string `query:"org,omitzero" doc:"One organization's slug"`
	Action string `query:"action,omitzero"`
	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit  int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *StaffAccessParams) Validate(v *muzak.Validation) {
	v.String(&in.Staff).Trim().Lower().MaxLen(254)
	v.String(&in.Org).Trim().Lower().MaxLen(64)
	v.String(&in.Action).Trim().MaxLen(64)
	v.String(&in.Before).Trim().MaxLen(64)
	v.Number(&in.Limit).Min(1).Max(200)
}

// StaffAccessOut is one page of the record.
type StaffAccessOut struct {
	Entries []StaffAccessEntryOut `json:"entries"`
	Next    string                `json:"next,omitzero"`
}

// StaffAccessEntryOut is one staff read or change: who, whose, what, why and when.
type StaffAccessEntryOut struct {
	ID           uuid.UUID `json:"id"`
	StaffEmail   string    `json:"staff_email"`
	Organization string    `json:"organization,omitzero" doc:"Its slug, or empty for a read that named no customer"`
	Project      string    `json:"project,omitzero" doc:"Its slug"`
	Action       string    `json:"action"`
	Ticket       string    `json:"ticket"`
	Note         string    `json:"note,omitzero"`
	IP           string    `json:"ip,omitzero"`
	At           time.Time `json:"at"`
}
