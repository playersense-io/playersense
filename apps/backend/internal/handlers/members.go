package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/invitation"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/role"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/schemas"
)

// Members, roles and invitations: how an organization stops being one person.
//
// Everything below existed as a table and a domain rule before it existed as a route. `identity`
// already held the two invariants the schema cannot express — an organization keeps at least one
// owner, and a member cap that is a ceiling rather than a lock — and the rbac suite reached around
// the HTTP layer with a `join` helper commented "which is what the invitation flow will do once it
// exists". This is that flow.
//
// # Role and member management is not a permission
//
// Assigning a role is guarded by [core.RequireRoleManagement] rather than by a permission, and
// that is a decision recorded in internal/authz: a grantable `role.manage` is silently equivalent
// to owner, because whoever holds it can write themselves a role containing everything. So it is
// reserved to the two system roles instead, which removes the escalation question rather than
// defending against it.
//
// `member.read`, `member.invite` and `member.remove` *are* permissions, because none of them can
// widen the holder's own privileges. Reading the list, inviting somebody to a role that already
// exists and removing a colleague are all things a studio can reasonably delegate to an office
// manager who is not an administrator.
//
// # Muzak keeps one Authorized provider per route
//
// Which means [core.Require] and [core.RequireRoleManagement] cannot both guard one route — the
// second declaration silently wins. That constraint is load-bearing here rather than annoying: it
// forces each route to have exactly one answer to "who may do this", and it is why changing a role
// and changing project access are two routes rather than one request with two guards.

// ---- roles ------------------------------------------------------------------

// ListRoles returns the roles a member of this organization can hold.
//
// Under `member.read` rather than a role permission, because this is the members screen's own
// vocabulary: you cannot invite somebody without naming a role, and a list of roles discloses
// nothing about anybody.
//
// A system role's permissions are computed rather than read from the row, so that a permission
// added tomorrow reaches every owner without a migration — which makes the response the authority
// and the column a convenience for whoever reads the table by hand.
func ListRoles(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.RoleListOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	rows, err := db.Client.Role.Query().
		Where(role.OrganizationID(org.ID)).
		Order(ent.Asc(role.FieldID)).
		All(ctx.Context())
	if err != nil {
		return schemas.RoleListOut{}, core.AsHTTPError(err)
	}
	out := schemas.RoleListOut{Roles: make([]schemas.RoleOut, 0, len(rows))}
	for _, row := range rows {
		out.Roles = append(out.Roles, roleOut(row))
	}
	return out, nil
}

// ---- members ----------------------------------------------------------------

// ListMembers returns the people in an organization.
//
// With the member ceiling and how much of it is used, because the invitation that breaches it
// fails at *accept* time — in front of the invited colleague rather than the person who sent it,
// which is the worst possible place to learn an organization is full.
func ListMembers(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.MemberListOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	org := principal.Organization

	rows, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(org.ID)).
		WithUser().
		WithRole().
		Order(ent.Asc(organizationmember.FieldJoinedAt)).
		All(ctx.Context())
	if err != nil {
		return schemas.MemberListOut{}, core.AsHTTPError(err)
	}

	out := schemas.MemberListOut{
		Members: make([]schemas.MemberOut, 0, len(rows)),
		Limit:   org.MemberLimit,
		Used:    len(rows),
	}
	for _, row := range rows {
		member, err := memberOut(ctx.Context(), db, row, principal.User.ID)
		if err != nil {
			return schemas.MemberListOut{}, err
		}
		out.Members = append(out.Members, member)
	}
	return out, nil
}

// ReadMember returns one membership.
func ReadMember(ctx *muzak.Context, in schemas.MemberParams) (schemas.MemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)

	row, err := findMember(ctx, in.MemberID)
	if err != nil {
		return schemas.MemberOut{}, err
	}
	return memberOut(ctx.Context(), db, row, principal.User.ID)
}

// SetMemberRole changes what one person may do.
//
// The last-owner rule lives in `identity` beside the member cap, because both are "how many are
// there now" questions that SQL cannot state as a column constraint. Demoting the only owner
// leaves an organization nobody owns: nobody can grant a role, nobody can delete it, and support
// has to edit the database by hand.
func SetMemberRole(ctx *muzak.Context, in schemas.SetMemberRoleIn) (schemas.MemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	org := principal.Organization

	member, err := findMember(ctx, in.MemberID)
	if err != nil {
		return schemas.MemberOut{}, err
	}
	wanted, err := findRole(ctx.Context(), db, org.ID, in.RoleKey)
	if err != nil {
		return schemas.MemberOut{}, err
	}

	// What it was, read before the change: "who made Ada an owner" is half the question and
	// "what was she before" is the other half, and nothing else in the product can answer it.
	was := ""
	if member.Edges.Role != nil {
		was = member.Edges.Role.Key
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.MemberOut{}, core.AsHTTPError(err)
	}
	updated, err := identity.SetMemberRole(ctx.Context(), tx.Client(), org.ID, member.ID, wanted.ID)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, identity.ErrLastOwner) {
			return schemas.MemberOut{}, muzak.Conflict(
				"this is the organization's only owner; make somebody else an owner first").
				WithCode("last_owner")
		}
		return schemas.MemberOut{}, core.AsHTTPError(err)
	}
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.MemberRoleChanged,
		"member", member.UID.String(), map[string]any{
			"email": memberEmail(member), "role": wanted.Key, "was": was,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.MemberOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return schemas.MemberOut{}, core.AsHTTPError(err)
	}
	return memberOut(ctx.Context(), db, withEdges(ctx.Context(), db, updated), principal.User.ID)
}

// SetMemberAccess replaces which projects one member can reach.
//
// Its own route, and `all_projects` is required rather than defaulted. This is the second axis —
// the role says what, this says where — and it is the widest grant in the product, so it cannot be
// given by omission. A combined member update carrying this field would widen or narrow somebody's
// reach whenever a caller forgot to echo it back, silently, with a 200.
func SetMemberAccess(ctx *muzak.Context, in schemas.SetMemberAccessIn) (schemas.MemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	org := principal.Organization

	member, err := findMember(ctx, in.MemberID)
	if err != nil {
		return schemas.MemberOut{}, err
	}

	all := in.AllProjects != nil && *in.AllProjects
	ids, err := projectIDs(ctx.Context(), db, org.ID, in.Projects)
	if err != nil {
		return schemas.MemberOut{}, err
	}
	// Compared against what was *asked for*, not against what resolved.
	//
	// `identity.SetMemberAccess` makes the same check, and pre-filtering here defeated it: it
	// received the already-resolved ids, so the two counts always matched and an unknown slug
	// was silently dropped. "Give this contractor these three games" quietly granting two is
	// precisely what an access review exists to catch and would not have.
	if !all && len(ids) != len(in.Projects) {
		return schemas.MemberOut{}, muzak.UnprocessableEntity(
			"one of those projects does not exist in this organization").
			WithCode("unknown_project")
	}

	// Written inside the same transaction as the grants themselves, so a failure between the two
	// cannot happen: either the access changed and the entry says so, or neither did.
	wasAll := member.AllProjects
	updated, _, err := identity.SetMemberAccess(ctx.Context(), db.Client,
		org.ID, member.ID, all, ids, nowUTC(),
		func(tx *ent.Tx, _ *ent.OrganizationMember, granted []*ent.Project) error {
			return auditFor(ctx).record(ctx.Context(), tx, audit.MemberAccessChanged,
				"member", member.UID.String(), map[string]any{
					"email": memberEmail(member), "all_projects": all, "was_all_projects": wasAll,
					"projects": slugsOf(granted),
				})
		})
	if err != nil {
		if errors.Is(err, identity.ErrUnknownProject) {
			return schemas.MemberOut{}, muzak.UnprocessableEntity(err.Error()).
				WithCode("unknown_project")
		}
		return schemas.MemberOut{}, core.AsHTTPError(err)
	}
	return memberOut(ctx.Context(), db, withEdges(ctx.Context(), db, updated), principal.User.ID)
}

// RemoveMember takes somebody out of an organization.
//
// Actually deleted, unlike almost everything else in this codebase — and the difference is worth
// stating, because the instinct after archiving flags and revoking keys is to keep the row. A
// membership is not a record of what happened; it is a live grant of access to a studio's player
// data. Keeping a disabled one would mean the answer to "who can reach this" depends on a boolean
// somebody has to remember to read.
//
// What survives is the audit trail: `AuditEvent` records the removal, and §08's access review asks
// who *can* reach the data, which is exactly the set of rows that remain.
func RemoveMember(ctx *muzak.Context, in schemas.MemberParams) (muzak.Empty, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)

	member, err := findMember(ctx, in.MemberID)
	if err != nil {
		return muzak.Empty{}, err
	}

	// The row is about to stop existing, so everything worth recording is read first — and this
	// is the entry that matters most in the whole log: a removed membership leaves nothing else
	// behind, so without it "who ended Ada's access, and when" has no answer anywhere.
	removed := map[string]any{"email": memberEmail(member), "all_projects": member.AllProjects}
	if member.Edges.Role != nil {
		removed["role"] = member.Edges.Role.Key
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	// Told before the membership goes: which projects they reached is part of what goes with it.
	if err := emitForMember(ctx.Context(), tx, principal.Organization.ID, member, "member.removed",
		map[string]any{"member": map[string]any{"email": memberEmail(member), "role": removed["role"],
			"removed_by": actorOf(ctx).Label}}); err != nil {
		_ = tx.Rollback()
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	if err := identity.RemoveMember(ctx.Context(), tx.Client(),
		principal.Organization.ID, member.ID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, identity.ErrLastOwner) {
			return muzak.Empty{}, muzak.Conflict(
				"this is the organization's only owner; an organization must keep one").
				WithCode("last_owner")
		}
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.MemberRemoved,
		"member", member.UID.String(), removed); err != nil {
		_ = tx.Rollback()
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	ctx.SetStatus(http.StatusNoContent)
	return muzak.Empty{}, nil
}

// ---- invitations ------------------------------------------------------------

// ListInvitations returns an organization's invitations.
//
// Every status, not only the outstanding ones: "did anybody ever invite this person" is a question
// the members screen has to answer, and an accepted invitation is the record of how somebody got
// in. The token is absent from the response type, so no listing can disclose one.
func ListInvitations(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.InvitationListOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	rows, err := db.Client.Invitation.Query().
		Where(invitation.OrganizationID(org.ID)).
		WithRole().
		WithInvitedBy().
		Order(ent.Desc(invitation.FieldCreatedAt)).
		All(ctx.Context())
	if err != nil {
		return schemas.InvitationListOut{}, core.AsHTTPError(err)
	}
	out := schemas.InvitationListOut{Invitations: make([]schemas.InvitationOut, 0, len(rows))}
	for _, row := range rows {
		one, err := invitationOut(ctx.Context(), db, row, nowUTC())
		if err != nil {
			return schemas.InvitationListOut{}, err
		}
		out.Invitations = append(out.Invitations, one)
	}
	return out, nil
}

// CreateInvitation sends one.
//
// # The token is generated here and never stored
//
// What is kept is a SHA-256 digest, the same reasoning as an API key's private half: an invitation
// link reaches an inbox, an inbox is not a secure channel, so the database must contain nothing
// that could reissue one. It is returned exactly once, inside the link, in the message.
//
// # Sending is part of the write
//
// A failed send fails the request and the row is rolled back. The alternative — a 201 with a row
// nobody can act on — is an invitation that exists on the members screen, never arrives, and
// cannot be re-sent because the address is already invited.
//
// # The rate limit here is not about load
//
// It is the one budget in the control plane, and the person it protects is not the one making the
// request: an invitation costs somebody else's attention and this deployment's sending reputation.
// It **fails closed**, unlike every SDK-path limit, because a message cannot be taken back.
func CreateInvitation(ctx *muzak.Context, in schemas.CreateInvitationIn) (schemas.InvitationCreatedOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	settings := muzak.From[config.Settings](ctx)
	org := principal.Organization

	if err := inviteWithinBudget(ctx, org.Slug, in.Email); err != nil {
		return schemas.InvitationCreatedOut{}, err
	}

	wanted, err := findRole(ctx.Context(), db, org.ID, in.RoleKey)
	if err != nil {
		return schemas.InvitationCreatedOut{}, err
	}

	// Somebody who is already in does not need an invitation, and sending one would produce a
	// pending row that can never be accepted into anything — the accept finds the membership
	// and reports it. Refused here so the sender is told rather than the invitee.
	member, err := db.Client.OrganizationMember.Query().
		Where(
			organizationmember.OrganizationID(org.ID),
			organizationmember.HasUserWith(user.EmailEqualFold(in.Email)),
		).
		Exist(ctx.Context())
	if err != nil {
		return schemas.InvitationCreatedOut{}, core.AsHTTPError(err)
	}
	if member {
		return schemas.InvitationCreatedOut{}, muzak.Conflict(
			in.Email + " is already a member of this organization").WithCode("already_member")
	}

	// One outstanding invitation per address. A second would mean two live tokens for one
	// inbox, and revoking "the" invitation would leave the other working.
	outstanding, err := db.Client.Invitation.Query().
		Where(
			invitation.OrganizationID(org.ID),
			invitation.EmailEqualFold(in.Email),
			invitation.StatusEQ(invitation.StatusPending),
			invitation.ExpiresAtGT(nowUTC()),
		).
		Exist(ctx.Context())
	if err != nil {
		return schemas.InvitationCreatedOut{}, core.AsHTTPError(err)
	}
	if outstanding {
		return schemas.InvitationCreatedOut{}, muzak.Conflict(
			in.Email + " already has an invitation to this organization; revoke it to send another").
			WithCode("already_invited")
	}

	all := in.AllProjects == nil || *in.AllProjects
	ids, err := projectIDs(ctx.Context(), db, org.ID, in.Projects)
	if err != nil {
		return schemas.InvitationCreatedOut{}, err
	}
	if !all && len(ids) != len(in.Projects) {
		return schemas.InvitationCreatedOut{}, muzak.UnprocessableEntity(
			"one of those projects does not exist in this organization").WithCode("unknown_project")
	}

	token, digest, err := identity.NewToken()
	if err != nil {
		return schemas.InvitationCreatedOut{}, core.AsHTTPError(err)
	}

	// Sent before anything is written. `invitationMessage` needs the token and the fields already
	// resolved above — nothing about the row itself — so the send can happen first, and a failure
	// here leaves nothing to clean up: no row, no audit entry, no compensating delete to get
	// wrong. The row this used to create-then-delete never existed in the first place.
	sender := muzak.From[mail.Sender](ctx)
	message := invitationMessage(settings, in.Email, org, principal.User, wanted, token)
	if err := sender.Send(ctx.Context(), message); err != nil {
		return schemas.InvitationCreatedOut{}, muzak.ServiceUnavailable(
			"could not send the invitation email; nothing was changed").Wrap(err)
	}

	// The row and its audit entry commit together, so a persisted invitation always has a matching
	// entry — the property the old create-then-send-then-audit ordering could not promise, because
	// the row was already durable before the send was even attempted.
	expiresAt := nowUTC().Add(settings.InvitationTTL)
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.Invitation, error) {
		row, err := tx.Invitation.Create().
			SetOrganizationID(org.ID).
			SetEmail(in.Email).
			SetTokenDigest(digest).
			SetInvitedByID(principal.User.ID).
			SetRoleID(wanted.ID).
			SetAllProjects(all).
			SetProjectIds(ids).
			SetExpiresAt(expiresAt).
			Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.MemberInvited,
			"invitation", row.UID.String(), map[string]any{
				"email": in.Email, "role": wanted.Key, "all_projects": all,
				"expires_at": row.ExpiresAt.Format(time.RFC3339),
			})
	})
	if err != nil {
		return schemas.InvitationCreatedOut{}, err
	}

	out, err := invitationOut(ctx.Context(), db, withInvitationEdges(row, wanted, principal.User), nowUTC())
	if err != nil {
		return schemas.InvitationCreatedOut{}, err
	}
	ctx.SetStatus(http.StatusCreated)
	// Whether anything was actually delivered, said out loud. Every deployment today runs the
	// placeholder sender, and a dashboard that told somebody to check their inbox for a
	// message that was only logged would be lying on our behalf.
	return schemas.InvitationCreatedOut{
		InvitationOut: out,
		Delivered:     mail.Delivers(sender),
	}, nil
}

// RevokeInvitation makes an outstanding token useless.
//
// The row is kept and marked revoked, because "who invited this person, and what happened" is the
// question the members screen answers — and because the digest has to stay to make sure the token
// cannot be reused: a deleted row is a token that matches nothing, which is the same outcome by
// luck rather than by design.
func RevokeInvitation(ctx *muzak.Context, in schemas.InvitationParams) (schemas.InvitationOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	row, err := db.Client.Invitation.Query().
		Where(invitation.UID(in.InvitationID), invitation.OrganizationID(org.ID)).
		WithRole().
		WithInvitedBy().
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.InvitationOut{}, muzak.NotFound("no such invitation")
		}
		return schemas.InvitationOut{}, core.AsHTTPError(err)
	}
	if row.Status != invitation.StatusPending {
		return schemas.InvitationOut{}, muzak.Conflict(
			"that invitation is already " + string(row.Status)).WithCode("not_pending")
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.InvitationOut{}, core.AsHTTPError(err)
	}
	updated, err := tx.Invitation.UpdateOneID(row.ID).
		SetStatus(invitation.StatusRevoked).Save(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		return schemas.InvitationOut{}, core.AsHTTPError(err)
	}
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.InvitationRevoked,
		"invitation", row.UID.String(), map[string]any{"email": row.Email}); err != nil {
		_ = tx.Rollback()
		return schemas.InvitationOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return schemas.InvitationOut{}, core.AsHTTPError(err)
	}
	updated.Edges = row.Edges
	return invitationOut(ctx.Context(), db, updated, nowUTC())
}

// ListPendingInvitations returns what is waiting for the caller's own address.
//
// No organization in the path, because the reader is not a member of any of them yet — that is the
// point. Matched on the verified address, so it shows only what was sent to the reader's own inbox.
func ListPendingInvitations(ctx *muzak.Context, _ muzak.Empty) (schemas.PendingInvitationListOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)

	rows, err := identity.PendingInvitationsFor(ctx.Context(), db.Client,
		principal.Claims.Email, nowUTC())
	if err != nil {
		return schemas.PendingInvitationListOut{}, core.AsHTTPError(err)
	}

	out := schemas.PendingInvitationListOut{
		Invitations: make([]schemas.PendingInvitationOut, 0, len(rows)),
	}
	for _, row := range rows {
		one := schemas.PendingInvitationOut{ExpiresAt: row.ExpiresAt}
		if row.Edges.Organization != nil {
			one.Organization = row.Edges.Organization.Name
			one.OrgSlug = row.Edges.Organization.Slug
		}
		if row.Edges.Role != nil {
			one.Role = row.Edges.Role.Name
		}
		if row.Edges.InvitedBy != nil {
			one.InvitedBy = row.Edges.InvitedBy.Email
		}
		out.Invitations = append(out.Invitations, one)
	}
	return out, nil
}

// AcceptInvitation joins an organization.
//
// # Why this route has no permission
//
// There is no organization in the path, so there is no membership, no role and nothing to check a
// permission against. The invitation is the authorisation — which is the same hole creating an
// organization walks through, and the same guard closes it: [core.RequireVerified], because an
// address Firebase has not confirmed must not be able to accept an invitation sent to that address.
// That is precisely how somebody would join an organization they were never invited to.
//
// # Accepting twice is a 200
//
// A link clicked twice, or clicked after joining another way, succeeds and says so. The person's
// intent — be in this organization — is satisfied either way, and a 409 would send them to support
// about a button that worked.
func AcceptInvitation(ctx *muzak.Context, in schemas.AcceptInvitationIn) (schemas.AcceptedOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)

	// Recorded against the organisation that gained a member, not against the person joining:
	// this log is the customer's, and "somebody joined" is their event rather than the joiner's.
	// Inside the same transaction as the membership itself — identity.accept calls this only on
	// the path that wrote one, never on the already-a-member branch, which committed nothing for
	// an entry to attach to.
	hook := func(tx *ent.Tx, accepted *identity.Accepted) error {
		if err := auditFor(ctx).forOrganization(accepted.Organization.ID).
			record(ctx.Context(), tx, audit.MemberJoined,
				"member", accepted.Member.UID.String(), map[string]any{
					"email": principal.Claims.Email,
					"role":  roleKeyOf(accepted.Role),
				}); err != nil {
			return err
		}
		return emitForMember(ctx.Context(), tx, accepted.Organization.ID, accepted.Member, "member.added",
			map[string]any{"member": map[string]any{"email": principal.Claims.Email, "role": roleKeyOf(accepted.Role)}})
	}

	var (
		accepted *identity.Accepted
		err      error
	)
	if in.Token != "" {
		accepted, err = identity.AcceptInvitation(ctx.Context(), db.Client,
			in.Token, principal.User, principal.Claims.Email, nowUTC(), hook)
	} else {
		accepted, err = identity.AcceptPendingInvitationFor(ctx.Context(), db.Client,
			in.OrgSlug, principal.User, principal.Claims.Email, nowUTC(), hook)
	}

	already := errors.Is(err, identity.ErrAlreadyMember)
	switch {
	case already:
		// Not a failure. Fall through with what was found.
	case errors.Is(err, identity.ErrNoInvitation):
		// 404 rather than 403, and one message for unknown, spent, revoked and expired
		// alike: telling somebody holding a token which of those it is tells them whether
		// it was ever real.
		return schemas.AcceptedOut{}, muzak.NotFound(
			"that invitation is not valid; ask for a new one").WithCode("no_invitation")
	case errors.Is(err, identity.ErrWrongAddress):
		return schemas.AcceptedOut{}, muzak.Forbidden(
			"that invitation was sent to a different email address").WithCode("wrong_address")
	case errors.Is(err, identity.ErrMemberLimit):
		return schemas.AcceptedOut{}, muzak.Conflict(
			"that organization has as many members as its plan allows").WithCode("member_limit")
	case err != nil:
		return schemas.AcceptedOut{}, core.AsHTTPError(err)
	}

	out := schemas.AcceptedOut{
		Organization:  organizationOut(accepted.Organization),
		AllProjects:   accepted.Member.AllProjects,
		AlreadyMember: already,
	}
	if accepted.Role != nil {
		out.Role = roleOut(accepted.Role)
	}
	for _, granted := range accepted.Projects {
		out.Projects = append(out.Projects, projectRefOut(granted))
	}
	return out, nil
}

// ---- the invitation email ---------------------------------------------------

// invitationMessage composes the one message PlayerSense sends itself.
//
// Plain text, and the link is on its own line so that every mail client makes it clickable and
// every one of them lets it be copied — which is the thing a person does when the click fails.
//
// It names who invited them and what they are being invited to. An invitation from an address
// somebody does not recognise, to an organization they have never heard of, is indistinguishable
// from phishing, and the recipient's correct response to that is to ignore it.
func invitationMessage(settings config.Settings, to string, org *ent.Organization,
	inviter *ent.User, wanted *ent.Role, token string,
) mail.Message {
	who := inviter.Email
	if inviter.Name != "" {
		who = inviter.Name + " (" + inviter.Email + ")"
	}

	link := strings.TrimRight(settings.DashboardURL, "/") + "/invitations/accept?token=" + token

	var body strings.Builder
	body.WriteString(who + " has invited you to join " + org.Name + " on PlayerSense")
	body.WriteString(" as " + wanted.Name + ".\n\n")
	body.WriteString(link + "\n\n")
	body.WriteString("The link expires in " + settings.InvitationTTL.String() + ".\n")
	body.WriteString("If you were not expecting this, you can ignore it — nothing happens until " +
		"you open the link and sign in with this address.\n")

	return mail.Message{
		Kind:    mail.Invitation,
		To:      to,
		Subject: "Join " + org.Name + " on PlayerSense",
		Body:    body.String(),
	}
}

// inviteWithinBudget counts one invitation against two budgets, and fails closed.
//
// Narrowest first, so the refusal names the address when one inbox is being hit repeatedly and the
// organization when somebody is working through a list. Both are counted even when the first is
// over, for the reason every other budget in the product is: a counter that stops being
// incremented stops being a measurement.
func inviteWithinBudget(ctx *muzak.Context, orgSlug, email string) error {
	limiter, found := muzak.TryFrom[*ratelimit.Limiter](ctx)
	if !found {
		return nil
	}
	return limiter.Strict().Allow(ctx, core.InvitationBudgets(orgSlug, email)...)
}

// memberEmail names a membership in a log line, falling back to its id.
//
// The address rather than the row id, because the person reading the entry a year from now knows who
// `ada@studio.example` was and has no way to look up member 41.
func memberEmail(member *ent.OrganizationMember) string {
	if member.Edges.User != nil {
		return member.Edges.User.Email
	}
	return member.UID.String()
}

func roleKeyOf(role *ent.Role) string {
	if role == nil {
		return ""
	}
	return role.Key
}

func slugsOf(projects []*ent.Project) []string {
	out := make([]string, 0, len(projects))
	for _, proj := range projects {
		out = append(out, proj.Slug)
	}
	return out
}

// ---- lookups and mapping ----------------------------------------------------

func findMember(ctx *muzak.Context, id uuid.UUID) (*ent.OrganizationMember, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	row, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.UID(id), organizationmember.OrganizationID(org.ID)).
		WithUser().
		WithRole().
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("no such member of this organization")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// findRole resolves a role key inside one organization.
//
// Scoped on purpose: a role id or key from another organization must not be assignable, and
// looking one up unscoped would make that a bug in whichever caller forgot to check.
func findRole(ctx context.Context, db *core.DB, orgID int, key string) (*ent.Role, error) {
	row, err := db.Client.Role.Query().
		Where(role.OrganizationID(orgID), role.Key(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.UnprocessableEntity(
				"this organization has no role called " + key).WithCode("unknown_role")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// projectIDs resolves project slugs inside one organization.
func projectIDs(ctx context.Context, db *core.DB, orgID int, slugs []string) ([]int, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	rows, err := db.Client.Project.Query().
		Where(project.SlugIn(slugs...), project.OrganizationID(orgID), project.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// withEdges reloads the edges an update dropped, so a response is built from one row.
func withEdges(ctx context.Context, db *core.DB, member *ent.OrganizationMember) *ent.OrganizationMember {
	reloaded, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.ID(member.ID)).
		WithUser().
		WithRole().
		Only(ctx)
	if err != nil {
		return member
	}
	return reloaded
}

func withInvitationEdges(row *ent.Invitation, wanted *ent.Role, inviter *ent.User) *ent.Invitation {
	row.Edges.Role = wanted
	row.Edges.InvitedBy = inviter
	return row
}

func roleOut(row *ent.Role) schemas.RoleOut {
	out := schemas.RoleOut{
		ID:          row.UID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		System:      row.System,
	}
	// A system role's permissions are computed from the vocabulary rather than read from the
	// column, so that a permission added tomorrow reaches every owner without a migration.
	// Reading the row here would answer with whatever was true on the day it was seeded.
	permissions := authz.SystemPermissions(row.Key)
	if !row.System {
		permissions = make([]authz.Permission, 0, len(row.Permissions))
		for _, name := range row.Permissions {
			permissions = append(permissions, authz.Permission(name))
		}
	}
	out.Permissions = make([]string, 0, len(permissions))
	for _, permission := range permissions {
		out.Permissions = append(out.Permissions, string(permission))
	}
	return out
}

func projectRefOut(row *ent.Project) schemas.ProjectRefOut {
	return schemas.ProjectRefOut{ID: row.UID, Slug: row.Slug, Name: row.Name}
}

func memberOut(ctx context.Context, db *core.DB, row *ent.OrganizationMember,
	callerID int,
) (schemas.MemberOut, error) {
	out := schemas.MemberOut{
		ID:          row.UID,
		AllProjects: row.AllProjects,
		JoinedAt:    row.JoinedAt,
		You:         row.UserID == callerID,
	}
	if row.Edges.User != nil {
		out.Email = row.Edges.User.Email
		out.Name = row.Edges.User.Name
	}
	if row.Edges.Role != nil {
		out.Role = roleOut(row.Edges.Role)
	}
	// Only when the list means something. `all_projects` is "every project, including ones
	// created tomorrow", which is not a list — and materialising one would go stale the next
	// time a project was created.
	if !row.AllProjects {
		granted, err := identity.MemberProjects(ctx, db.Client, row.ID)
		if err != nil {
			return schemas.MemberOut{}, core.AsHTTPError(err)
		}
		for _, one := range granted {
			out.Projects = append(out.Projects, projectRefOut(one))
		}
	}
	return out, nil
}

// invitationOut renders an invitation, reporting an elapsed one as expired.
//
// Computed rather than read, because nothing sweeps the table: the status column is only written
// when somebody touches the row, so a pending invitation whose moment has passed would otherwise
// be listed as outstanding forever.
func invitationOut(ctx context.Context, db *core.DB, row *ent.Invitation,
	now time.Time,
) (schemas.InvitationOut, error) {
	out := schemas.InvitationOut{
		ID:          row.UID,
		Email:       row.Email,
		AllProjects: row.AllProjects,
		Status:      string(row.Status),
		ExpiresAt:   row.ExpiresAt,
		CreatedAt:   row.CreatedAt,
		AcceptedAt:  row.AcceptedAt,
	}
	if row.Status == invitation.StatusPending && !now.Before(row.ExpiresAt) {
		out.Status = string(invitation.StatusExpired)
	}
	if row.Edges.Role != nil {
		out.Role = roleOut(row.Edges.Role)
	}
	if row.Edges.InvitedBy != nil {
		out.InvitedBy = row.Edges.InvitedBy.Email
	}
	if !row.AllProjects && len(row.ProjectIds) > 0 {
		rows, err := db.Client.Project.Query().
			Where(project.IDIn(row.ProjectIds...)).
			Order(ent.Asc(project.FieldSlug)).
			All(ctx)
		if err != nil {
			return schemas.InvitationOut{}, core.AsHTTPError(err)
		}
		for _, one := range rows {
			out.Projects = append(out.Projects, projectRefOut(one))
		}
	}
	return out, nil
}
