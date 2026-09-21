package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Members returns the routes that make an organization more than one person.
//
// # Each route has exactly one guard, and the framework enforces that
//
// Muzak keeps one provider per produced type, so [core.Require] and [core.RequireRoleManagement]
// cannot both guard one route — the second declaration would silently win. That is a useful
// constraint rather than a limitation: it forces every route to have one answer to "who may do
// this", and it is why changing somebody's role and changing which projects they reach are two
// routes rather than one request with two guards.
//
// # Why role management is not a permission
//
// A grantable `role.manage` would be silently equivalent to owner, because whoever held it could
// write themselves a role containing everything. So assigning a role and changing project access
// are reserved to the two system roles, which removes the escalation question rather than defending
// against it (internal/authz).
//
// `member.read`, `member.invite` and `member.remove` are permissions, because none of them widens
// the holder's own privileges: a studio can give an office manager the members screen without
// giving them the ability to make themselves an owner.
func Members() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("members"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization does not exist"))

	const (
		members = "/organizations/{org}/members"
		invites = "/organizations/{org}/invitations"
	)

	r.Get("/organizations/{org}/roles", handlers.ListRoles,
		muzak.Summary("List the roles a member can hold"),
		muzak.Description("The six seeded roles, and any the organization has added. A system "+
			"role's permissions are computed from the vocabulary rather than read from its "+
			"row, so this list is the authority on what it grants."),
		core.Require(authz.MemberRead))

	// ---- members ------------------------------------------------------------

	r.Get(members, handlers.ListMembers,
		muzak.Summary("List an organization's members"),
		muzak.Description("With the member ceiling and how much of it is used, because an "+
			"invitation that breaches it fails at accept time — in front of the invited "+
			"colleague rather than whoever sent it."),
		core.Require(authz.MemberRead))

	r.Get(members+"/{member_id}", handlers.ReadMember,
		muzak.Summary("Read one membership"),
		core.Require(authz.MemberRead))

	r.Put(members+"/{member_id}/role", handlers.SetMemberRole,
		muzak.Summary("Change what a member may do"),
		muzak.Description("Reserved to an owner or an admin: a grantable permission to assign "+
			"roles is equivalent to owner. The organization's last owner cannot be demoted."),
		core.RequireRoleManagement(),
		muzak.WithResponseDoc(http.StatusConflict,
			"That member is the organization's only owner"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"This organization has no role with that key"))

	r.Put(members+"/{member_id}/access", handlers.SetMemberAccess,
		muzak.Summary("Change which projects a member reaches"),
		muzak.Description("The second axis: the role says what, this says where. "+
			"`all_projects` is required rather than defaulted — it is the widest grant in "+
			"the product and must not be given by omitting a field."),
		core.RequireRoleManagement(),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"One of those projects does not exist in this organization"))

	r.Delete(members+"/{member_id}", handlers.RemoveMember,
		muzak.Summary("Remove a member"),
		muzak.Description("The row is deleted, unlike a revoked key or an archived flag: a "+
			"membership is not a record of what happened, it is a live grant of access to "+
			"player data. The organization's last owner cannot be removed."),
		core.Require(authz.MemberRemove),
		muzak.Status(http.StatusNoContent),
		muzak.WithResponseDoc(http.StatusConflict,
			"That member is the organization's only owner"))

	// ---- invitations --------------------------------------------------------

	r.Get(invites, handlers.ListInvitations,
		muzak.Summary("List an organization's invitations"),
		muzak.Description("Every status, because an accepted invitation is the record of how "+
			"somebody got in. No token is ever disclosed — only a digest is stored."),
		core.Require(authz.MemberRead))

	r.Post(invites, handlers.CreateInvitation,
		muzak.Summary("Invite somebody"),
		muzak.Description("Sends a link and stores only its digest. The invitation can be "+
			"accepted only by a Firebase account that has verified the address it was sent "+
			"to, so forwarding it does not let a colleague join in somebody else's place."),
		core.Require(authz.MemberInvite),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict,
			"That address is already a member, or already has an outstanding invitation"),
		muzak.WithResponseDoc(http.StatusTooManyRequests,
			"Too many invitations to that address or from that organization in the last hour"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"The email could not be sent; no invitation was created"))

	r.Delete(invites+"/{invitation_id}", handlers.RevokeInvitation,
		muzak.Summary("Revoke an invitation"),
		muzak.Description("The row is kept and marked revoked, so the token keeps matching "+
			"something that refuses it rather than matching nothing by luck."),
		core.Require(authz.MemberInvite),
		muzak.WithResponseDoc(http.StatusConflict, "That invitation is not outstanding"))

	// ---- the log -----------------------------------------------------------

	// Under `audit.read`, which only the two system roles hold among the seeded six. The log
	// names what every member has been doing, so showing it to all of them is a different
	// decision from showing them the members list — a contractor can see who their colleagues
	// are without seeing what each of them changed last Tuesday.
	r.Get("/organizations/{org}/audit", handlers.ReadAudit,
		muzak.Summary("Who did what"),
		muzak.Description("The organization's own log, newest first: memberships, roles, keys, "+
			"publishes, credit and configuration. Append-only — every field on the row is "+
			"immutable, so there is no code path that could change what an entry says. "+
			"Filterable to one action, because \"who published\" and \"who changed a role\" "+
			"are different questions."),
		core.Require(authz.AuditRead))

	return r
}

// Invitations returns the two routes that belong to the person being invited rather than to an
// organization.
//
// Separate from [Members] because neither names an organization in its path — which is the whole
// point: the caller is not a member of one yet, so there is no membership, no role, and nothing a
// permission could be checked against.
//
// What stands in for a permission is [core.RequireVerified] on the accept. An address Firebase has
// not confirmed must not be able to accept an invitation sent to that address; that is precisely
// how somebody would join an organization they were never invited to, and it is the same hole
// creating an organization walks through.
func Invitations() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("members"))

	r.Get("/invitations", handlers.ListPendingInvitations,
		muzak.Summary("List invitations waiting for you"),
		muzak.Description("Matched on the address your sign-in verified, so it shows only what "+
			"was sent to your own inbox. It carries no token and does not need to: accepting "+
			"one of these names the organization instead."),
		// No permission and no verification. Reading what was sent to your own verified
		// address changes nothing, and an unverified account being able to see that it was
		// invited is how it learns which link to look for.
		muzak.Needs(core.ResolveUser))

	r.Post("/invitations/accept", handlers.AcceptInvitation,
		muzak.Summary("Accept an invitation"),
		muzak.Description("Name either the token from the link or the organization, when one "+
			"is already waiting for your verified address. Accepting twice succeeds and says "+
			"so, because the intent is satisfied either way."),
		core.RequireVerified(),
		muzak.WithResponseDoc(http.StatusNotFound,
			"That invitation is unknown, already used, revoked or expired"),
		muzak.WithResponseDoc(http.StatusForbidden,
			"That invitation was sent to a different address, or this account has not verified its own"),
		muzak.WithResponseDoc(http.StatusConflict,
			"That organization has as many members as its plan allows"))

	return r
}
