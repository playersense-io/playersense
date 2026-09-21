package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/invitation"
	"playersense.dev/backend/ent/memberproject"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/project"
)

// The rules about accepting an invitation, which the schema cannot express either.
//
// The token is the authorisation and the address is the check on it. Neither alone is enough:
// a token proves somebody was sent a link, an address proves who they are, and an invitation
// forwarded to a colleague satisfies the first without the second.

// Errors an accept can fail with, each a different sentence to the person holding the link.
var (
	// ErrNoInvitation is an unknown, already used, revoked or expired token — one error for
	// all four, deliberately. Distinguishing them tells somebody holding a token they
	// should not have whether it was ever real.
	ErrNoInvitation = errors.New("that invitation is not valid")

	// ErrWrongAddress is a valid invitation being accepted by somebody else. Named
	// separately because the person reading it is almost always a real colleague who was
	// forwarded the link, and "not valid" would send them to ask for a new one that will
	// fail the same way.
	ErrWrongAddress = errors.New("that invitation was sent to a different email address")

	// ErrAlreadyMember is the accept that has nothing to do. Not an error the caller has to
	// treat as a failure — see AcceptInvitation.
	ErrAlreadyMember = errors.New("already a member of that organization")
)

// Accepted is what accepting produced.
type Accepted struct {
	Organization *ent.Organization
	Member       *ent.OrganizationMember
	Role         *ent.Role

	// Projects is what was actually granted, which can be fewer than the invitation named:
	// a project deleted between sending and accepting drops that one grant rather than
	// failing the accept (see the schema's note on `project_ids`).
	Projects []*ent.Project
}

// AcceptInvitation turns a token and a verified address into a membership.
//
// # Everything or nothing
//
// One transaction covers marking the invitation accepted, creating the membership and writing
// the project grants. Each of the three is useless without the others, and the partial states are
// all bad in a way nobody would notice: an invitation marked accepted with no membership is a
// person who cannot join and cannot be re-invited, and a membership with no grants is somebody
// who joined and can see nothing, which is indistinguishable from a deliberately restricted
// account.
//
// # The address is checked here, not at the door
//
// `person` is the signed-in account and `verifiedEmail` is the address Firebase vouched for on
// *this* request. They are compared separately on purpose: the row's own address is a projection
// of an earlier sign-in, and the question here is who is holding the link now.
//
// The comparison is case-insensitive, because an email address's domain is
// case-insensitive by RFC and every real provider treats the local part that way too — a studio
// inviting `Ada@studio.example` and a person signing in as `ada@studio.example` is not an
// impersonation attempt.
//
// # Accepting twice is success
//
// A link clicked twice, or clicked after joining another way, returns [ErrAlreadyMember] with the
// membership that already exists. The caller decides: the HTTP layer answers 200 with the
// organization, because the person's intent — "be in this organization" — is satisfied, and a
// 409 would send them to support about a button that worked.
//
// `also`, when given, runs inside the same transaction as the membership it just created, right
// before commit — see [CreateOrganization] for why. It is not called on the already-a-member
// path, because nothing was written there for it to attach to.
func AcceptInvitation(ctx context.Context, db *ent.Client, token string,
	person *ent.User, verifiedEmail string, now time.Time,
	also func(tx *ent.Tx, accepted *Accepted) error,
) (*Accepted, error) {
	found, err := db.Invitation.Query().
		Where(invitation.TokenDigest(Digest(token))).
		WithOrganization().
		WithRole().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNoInvitation
		}
		return nil, fmt.Errorf("look up invitation: %w", err)
	}
	return accept(ctx, db, found, person, verifiedEmail, now, also)
}

// AcceptPendingInvitationFor accepts the invitation waiting for this person at one organization,
// without a token.
//
// # Why this exists, given that the token is the credential
//
// Because the address is the authorisation and the token is only how the link works. Firebase has
// vouched for `verifiedEmail` on this request, and the invitation was sent to exactly that
// address — so a token adds no fact that is not already established. What it would add is a dead
// end: somebody who deleted the email, or whose client mangled the link, can see on their
// dashboard that they were invited and has no way to act on it. The fix for that should not be
// "ask an administrator to revoke and re-send".
//
// The narrow reading — that possession of the message is itself a check — protects against one
// case: an address that Firebase considers verified but that the person does not actually control.
// That is a Firebase compromise, and an attacker holding it can read the inbox too, so the token
// does not survive the threat it would be defending against.
//
// [PendingInvitationsFor] is what lists these, and it deliberately carries no token, because none
// is needed.
func AcceptPendingInvitationFor(ctx context.Context, db *ent.Client, orgSlug string,
	person *ent.User, verifiedEmail string, now time.Time,
	also func(tx *ent.Tx, accepted *Accepted) error,
) (*Accepted, error) {
	found, err := db.Invitation.Query().
		Where(
			invitation.StatusEQ(invitation.StatusPending),
			invitation.EmailEqualFold(strings.TrimSpace(verifiedEmail)),
			invitation.HasOrganizationWith(organization.Slug(orgSlug)),
		).
		WithOrganization().
		WithRole().
		Order(ent.Desc(invitation.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNoInvitation
		}
		return nil, fmt.Errorf("look up invitation: %w", err)
	}
	return accept(ctx, db, found, person, verifiedEmail, now, also)
}

// PendingInvitationsFor lists what is waiting for one verified address.
//
// Matched on the address rather than on a token, so it shows only what was sent to the reader's
// own inbox — an address the sign-in already proved. Expired rows are left out rather than
// returned with a status, because an invitation nobody can accept is not news.
func PendingInvitationsFor(ctx context.Context, db *ent.Client, verifiedEmail string,
	now time.Time,
) ([]*ent.Invitation, error) {
	return db.Invitation.Query().
		Where(
			invitation.StatusEQ(invitation.StatusPending),
			invitation.EmailEqualFold(strings.TrimSpace(verifiedEmail)),
			invitation.ExpiresAtGT(now),
		).
		WithOrganization().
		WithRole().
		WithInvitedBy().
		Order(ent.Asc(invitation.FieldCreatedAt)).
		All(ctx)
}

// accept is the half both entry points share: the checks, then the transaction.
func accept(ctx context.Context, db *ent.Client, found *ent.Invitation,
	person *ent.User, verifiedEmail string, now time.Time,
	also func(tx *ent.Tx, accepted *Accepted) error,
) (*Accepted, error) {
	switch {
	case found.Status != invitation.StatusPending:
		return nil, ErrNoInvitation
	case !now.Before(found.ExpiresAt):
		// Marked expired as it is discovered, so the members screen stops showing it as
		// outstanding. Best effort: failing to write this must not fail the accept, and
		// the status is recomputed from expires_at anyway.
		_, _ = db.Invitation.UpdateOneID(found.ID).
			SetStatus(invitation.StatusExpired).Save(ctx)
		return nil, ErrNoInvitation
	case !strings.EqualFold(strings.TrimSpace(verifiedEmail), found.Email):
		return nil, ErrWrongAddress
	case found.Edges.Organization == nil || found.Edges.Role == nil:
		// The organization or the role was deleted under it. Not a valid invitation any
		// more, and there is nothing the holder can do about it.
		return nil, ErrNoInvitation
	}

	existing, err := db.OrganizationMember.Query().
		Where(
			organizationmember.OrganizationID(found.OrganizationID),
			organizationmember.UserID(person.ID),
		).
		WithRole().
		Only(ctx)
	if err == nil {
		// Already in. The invitation is spent rather than left outstanding, so the members
		// screen does not show a pending row for somebody who is already listed above it.
		if found.Status == invitation.StatusPending {
			_, _ = db.Invitation.UpdateOneID(found.ID).
				SetStatus(invitation.StatusAccepted).SetAcceptedAt(now).Save(ctx)
		}
		return &Accepted{
			Organization: found.Edges.Organization,
			Member:       existing,
			Role:         existing.Edges.Role,
		}, ErrAlreadyMember
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("look up existing membership: %w", err)
	}

	// Which of the named projects still exist. Resolved before the transaction so that a
	// deleted project costs a read rather than a rollback.
	var granted []*ent.Project
	if !found.AllProjects && len(found.ProjectIds) > 0 {
		granted, err = db.Project.Query().
			Where(
				project.IDIn(found.ProjectIds...),
				project.OrganizationID(found.OrganizationID),
			).
			Order(ent.Asc(project.FieldSlug)).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("look up invited projects: %w", err)
		}
	}

	// The member cap is the one check that cannot move inside the transaction without
	// serialising every accept in the product, and AddMember explains why that is a ceiling
	// rather than a lock. Read it here so a full organization is refused before anything is
	// written.
	org, err := db.Organization.Get(ctx, found.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("look up organization: %w", err)
	}
	members, err := db.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(org.ID)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}
	if members >= org.MemberLimit {
		return nil, fmt.Errorf("%w: %d of %d", ErrMemberLimit, members, org.MemberLimit)
	}

	tx, err := db.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}

	member, err := tx.OrganizationMember.Create().
		SetOrganizationID(found.OrganizationID).
		SetUserID(person.ID).
		SetRoleID(found.RoleID).
		SetAllProjects(found.AllProjects).
		SetJoinedAt(now).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("create membership: %w", err)
	}

	for _, granted := range granted {
		if _, err := tx.MemberProject.Create().
			SetMemberID(member.ID).
			SetProjectID(granted.ID).
			SetGrantedAt(now).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("grant project access: %w", err)
		}
	}

	if _, err := tx.Invitation.UpdateOneID(found.ID).
		SetStatus(invitation.StatusAccepted).
		SetAcceptedAt(now).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("mark the invitation accepted: %w", err)
	}

	accepted := &Accepted{
		Organization: found.Edges.Organization,
		Member:       member,
		Role:         found.Edges.Role,
		Projects:     granted,
	}

	// Only on this path — never for the already-a-member branch above, which committed nothing
	// of its own for an entry to attach to.
	if also != nil {
		if err := also(tx, accepted); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return accepted, nil
}

// SetMemberAccess replaces which projects one member can reach.
//
// Its own operation rather than a field on a member update, and the reason is a footgun: a
// whole-object write that carried `all_projects` would widen or narrow somebody's access whenever
// a caller forgot to echo the field back. Access is a decision, so it is a request of its own.
//
// Granting everything deletes the rows rather than keeping them, because `all_projects` means
// "every project, including ones created tomorrow" — which stale rows would contradict the first
// time somebody read them instead of the boolean.
//
// `also`, when given, runs inside the same transaction as the grants it just wrote, right before
// commit — see [CreateOrganization] for why.
func SetMemberAccess(ctx context.Context, db *ent.Client, orgID, memberID int,
	allProjects bool, projectIDs []int, now time.Time,
	also func(tx *ent.Tx, updated *ent.OrganizationMember, granted []*ent.Project) error,
) (*ent.OrganizationMember, []*ent.Project, error) {
	member, err := db.OrganizationMember.Query().
		Where(
			organizationmember.ID(memberID),
			organizationmember.OrganizationID(orgID),
		).
		Only(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("look up member: %w", err)
	}

	var wanted []*ent.Project
	if !allProjects && len(projectIDs) > 0 {
		// Scoped to the organization, so an id from another studio's project cannot be
		// granted by passing it here.
		wanted, err = db.Project.Query().
			Where(project.IDIn(projectIDs...), project.OrganizationID(orgID)).
			Order(ent.Asc(project.FieldSlug)).
			All(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("look up projects: %w", err)
		}
		if len(wanted) != len(projectIDs) {
			// Refused rather than partially applied. "Give this contractor these three
			// games" that silently grants two is the kind of quiet failure an access
			// review is supposed to catch and would not.
			return nil, nil, fmt.Errorf("%w: %d of %d projects exist in this organization",
				ErrUnknownProject, len(wanted), len(projectIDs))
		}
	}

	tx, err := db.Tx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}

	if _, err := tx.MemberProject.Delete().
		Where(memberproject.MemberID(member.ID)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, nil, fmt.Errorf("clear project access: %w", err)
	}
	for _, grant := range wanted {
		if _, err := tx.MemberProject.Create().
			SetMemberID(member.ID).
			SetProjectID(grant.ID).
			SetGrantedAt(now).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, nil, fmt.Errorf("grant project access: %w", err)
		}
	}
	updated, err := tx.OrganizationMember.UpdateOneID(member.ID).
		SetAllProjects(allProjects).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, fmt.Errorf("set all_projects: %w", err)
	}

	if also != nil {
		if err := also(tx, updated, wanted); err != nil {
			_ = tx.Rollback()
			return nil, nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}
	return updated, wanted, nil
}

// ErrUnknownProject is a grant naming a project this organization does not have.
var ErrUnknownProject = errors.New("no such project in this organization")

// MemberProjects reads which projects one member has been granted.
func MemberProjects(ctx context.Context, db *ent.Client, memberID int) ([]*ent.Project, error) {
	rows, err := db.MemberProject.Query().
		Where(memberproject.MemberID(memberID)).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read project access: %w", err)
	}
	out := make([]*ent.Project, 0, len(rows))
	for _, row := range rows {
		if row.Edges.Project != nil {
			out = append(out, row.Edges.Project)
		}
	}
	return out, nil
}
