package schemas

import (
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework"
)

// The people in an organization, and the invitations that put them there.
//
// # Two axes, two requests
//
// A role says *what* somebody may do and the project access list says *where* (ADR: the
// `MemberProject` schema). They are separate routes rather than fields of one member update, and
// the reason is a footgun rather than taste: a whole-object write carrying `all_projects` would
// widen or narrow somebody's reach whenever a caller forgot to echo the field back, silently, with
// a 200. Access is a decision, so it is a request that only ever makes it.

// ---- roles ------------------------------------------------------------------

// RoleOut is one role a member can hold.
type RoleOut struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key" doc:"Stable; owner, admin, liveops_manager, developer, growth_manager or billing for the seeded six"`
	Name        string    `json:"name" doc:"What the dashboard shows. Editable, even for a system role"`
	Description string    `json:"description,omitzero"`

	// Permissions is what the role grants. For a system role it is computed from the
	// vocabulary rather than read from the row, so a permission added tomorrow reaches every
	// owner without a migration — which means this list is the authority and the column is
	// not.
	Permissions []string `json:"permissions"`

	System bool `json:"system" doc:"An owner or admin role. Its permissions are computed, and it cannot be created"`
}

// RoleListOut is an organization's roles.
type RoleListOut struct {
	Roles []RoleOut `json:"roles"`
}

// ---- members ----------------------------------------------------------------

// MemberOut is one person in an organization.
type MemberOut struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name,omitzero"`

	Role RoleOut `json:"role"`

	AllProjects bool `json:"all_projects" doc:"Every project, including ones created tomorrow"`

	// Projects is empty when all_projects is true, because there are no rows then: "every
	// project, now and later" is not a list, and materialising one would be a list that goes
	// stale the next time a project is created.
	Projects []ProjectRefOut `json:"projects,omitzero"`

	// JoinedAt is what an access review reads (§08). Distinct from the row's creation, so
	// that a membership recreated after a removal answers "since when has this person been
	// able to reach this data" about the current one.
	JoinedAt time.Time `json:"joined_at"`

	// You marks the caller's own row, so a dashboard can refuse to offer them the button
	// that removes themselves without having to compare addresses.
	You bool `json:"you,omitzero"`
}

// ProjectRefOut names a project without repeating everything about it.
type ProjectRefOut struct {
	ID   uuid.UUID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
}

// MemberListOut is an organization's people.
type MemberListOut struct {
	Members []MemberOut `json:"members"`

	// Limit and Used say how close the organization is to its member ceiling, because the
	// invitation that fails on it fails at accept time — in front of the invited colleague,
	// not the person who sent it.
	Limit int `json:"member_limit"`
	Used  int `json:"members_used"`
}

// MemberParams addresses one membership.
type MemberParams struct {
	OrgSlug  string    `path:"org"`
	MemberID uuid.UUID `path:"member_id" doc:"The membership's identifier, not the user's"`
}

func (in *MemberParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
}

// SetMemberRoleIn changes what one person may do.
type SetMemberRoleIn struct {
	OrgSlug  string    `path:"org"`
	MemberID uuid.UUID `path:"member_id"`

	// RoleKey rather than a role id, because a key is what a person writing this request
	// knows — `developer` — and an id is a lookup they would have to perform first. The role
	// is resolved inside the organization, so a key from another studio cannot be named.
	RoleKey string `json:"role" doc:"The role's key, resolved within this organization"`
}

func (in *SetMemberRoleIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.RoleKey).Trim().Lower().Required().MaxLen(64)
}

// SetMemberAccessIn replaces which projects one member can reach.
type SetMemberAccessIn struct {
	OrgSlug  string    `path:"org"`
	MemberID uuid.UUID `path:"member_id"`

	// AllProjects is required rather than defaulted. The whole point of this being its own
	// request is that the widest grant in the product cannot be given by omission, so the
	// caller says which of the two things they mean.
	AllProjects *bool `json:"all_projects" doc:"Required. true grants every project, now and later"`

	// Projects is the list when all_projects is false, by slug. Slugs for the same reason
	// the role is a key: they are what a person reading the request can check.
	Projects []string `json:"projects,omitzero" doc:"Project slugs. Ignored when all_projects is true"`
}

func (in *SetMemberAccessIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.Value(&in.AllProjects).Required()
	for i := range in.Projects {
		v.String(&in.Projects[i]).Trim().Lower().Required().MaxLen(64)
	}
}

// ---- invitations ------------------------------------------------------------

// InvitationOut is one outstanding invitation.
//
// The token is not a member of this type and never will be. It is returned once, in the link, and
// stored only as a digest — so there is no path by which a listing could disclose one, exactly as
// an API key's private half cannot be shown again.
type InvitationOut struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email" doc:"Who it was sent to. Compared with the verified address at accept time"`

	Role        RoleOut         `json:"role" doc:"What accepting grants. Fixed when it was sent"`
	AllProjects bool            `json:"all_projects"`
	Projects    []ProjectRefOut `json:"projects,omitzero"`

	Status    string    `json:"status" doc:"pending, accepted, revoked or expired"`
	InvitedBy string    `json:"invited_by,omitzero" doc:"Who sent it; absent if that account is gone"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`

	AcceptedAt *time.Time `json:"accepted_at,omitzero"`
}

// InvitationListOut is an organization's invitations.
type InvitationListOut struct {
	Invitations []InvitationOut `json:"invitations"`
}

// CreateInvitationIn invites one address.
type CreateInvitationIn struct {
	OrgSlug string `path:"org"`

	Email   string `json:"email" doc:"The address to invite. The invitation can only be accepted by a Firebase account that has verified it"`
	RoleKey string `json:"role" doc:"The role's key, resolved within this organization"`

	// AllProjects defaults to true, which is the common case and the one the schema's own
	// default records. Unlike an access *change*, an invitation is a new person with no
	// existing reach to widen by accident, so a default here cannot silently escalate
	// anybody.
	AllProjects *bool    `json:"all_projects,omitzero" default:"true" doc:"Defaults to true"`
	Projects    []string `json:"projects,omitzero" doc:"Project slugs, when all_projects is false"`
}

func (in *CreateInvitationIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	// Lowered as well as trimmed: the address is stored as it is compared, so that two
	// invitations to one inbox spelled differently are one invitation.
	v.String(&in.Email).Trim().Lower().Required().Email().MaxLen(254)
	v.String(&in.RoleKey).Trim().Lower().Required().MaxLen(64)
	for i := range in.Projects {
		v.String(&in.Projects[i]).Trim().Lower().Required().MaxLen(64)
	}
}

// InvitationCreatedOut is what the sender is told.
type InvitationCreatedOut struct {
	InvitationOut

	// Delivered says whether a provider accepted the message. False with no mail provider
	// configured, which is the state of every deployment today — said out loud so a dashboard
	// can show "copy the link" rather than "check your inbox" for a message that was never
	// sent.
	Delivered bool `json:"delivered"`
}

// InvitationParams addresses one invitation.
type InvitationParams struct {
	OrgSlug      string    `path:"org"`
	InvitationID uuid.UUID `path:"invitation_id"`
}

func (in *InvitationParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
}

// AcceptInvitationIn names what to accept: a token, or an organization.
//
// **Exactly one of the two.** A token is what the link carries, and it is in the body rather than
// the path because it is a bearer credential: in a URL it reaches the access log of every proxy
// between the browser and here, and a log outlives the invitation's own expiry.
//
// An organization slug is the other way in, for somebody who lost the email. It is no weaker: the
// address is the authorisation, Firebase has already vouched for it on this request, and the
// invitation was sent to exactly that address — so the token establishes nothing further. What it
// avoids is a dead end where a person can see on their dashboard that they were invited and has no
// way to act on it.
type AcceptInvitationIn struct {
	Token   string `json:"token,omitzero" doc:"The token from the invitation link"`
	OrgSlug string `json:"organization,omitzero" doc:"The organization to join, for an invitation already waiting for your verified address"`
}

func (in *AcceptInvitationIn) Validate(v *muzak.Validation) {
	// No format assertion on the token beyond a length: it is compared by digest, so a
	// malformed one is simply a digest that matches nothing. A pattern would describe today's
	// encoding and change no refusal.
	v.String(&in.Token).Trim().MaxLen(128)
	v.String(&in.OrgSlug).Trim().Lower().MaxLen(64)

	switch {
	case in.Token == "" && in.OrgSlug == "":
		v.Reject(&in.Token, "name either a token or an organization")
	case in.Token != "" && in.OrgSlug != "":
		// Refused rather than picking one. Two answers to "what am I accepting" is a
		// request whose meaning depends on an implementation detail, and the caller who
		// sent both does not know which they got.
		v.Reject(&in.OrgSlug, "name a token or an organization, not both")
	case in.Token != "" && len(in.Token) < 16:
		v.Reject(&in.Token, "is too short to be an invitation token")
	}
}

// AcceptedOut is what somebody joined.
type AcceptedOut struct {
	Organization OrganizationOut `json:"organization"`
	Role         RoleOut         `json:"role"`
	AllProjects  bool            `json:"all_projects"`
	Projects     []ProjectRefOut `json:"projects,omitzero"`

	// AlreadyMember distinguishes "you have joined" from "you were already in", for a link
	// clicked twice. Both are a 200: the person's intent is satisfied either way, and a 409
	// would send them to support about a button that worked.
	AlreadyMember bool `json:"already_member,omitzero"`
}

// PendingInvitationOut is an invitation addressed to the caller.
//
// A separate type from [InvitationOut] because it answers a different question and is read by a
// different person. An administrator's listing is about their organization; this is about the
// reader's own inbox, so it names the organization and omits everything about who else was
// invited.
type PendingInvitationOut struct {
	Organization string    `json:"organization" doc:"The organization's display name"`
	OrgSlug      string    `json:"organization_slug"`
	Role         string    `json:"role" doc:"The role's display name"`
	InvitedBy    string    `json:"invited_by,omitzero"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// PendingInvitationListOut is what is waiting for the caller.
//
// It carries no token and does not need to: accepting one of these names the organization instead,
// because the reader's address is already proved by the sign-in and is what the invitation was
// addressed to.
type PendingInvitationListOut struct {
	Invitations []PendingInvitationOut `json:"invitations"`
}
