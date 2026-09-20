package identity_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/memberproject"
	"playersense.dev/backend/ent/global/organizationmember"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/testsupport"
)

// The atomicity these functions were refactored for: a hook that runs inside the same transaction
// as the write, and a hook that fails has to take the write down with it. This is the mechanism
// `apps/backend/cmd/control-api`'s audit tests exercise indirectly through HTTP; these test it
// directly, at the one seam that can prove it — a hook the caller controls.

// fixture is one organization with a project, for the two tests below.
type fixture struct {
	db     *global.Client
	orgID  int
	roleID int
	projID int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)

	owner, err := identity.EnsureUser(t.Context(), db.Client, claims(t), time.Now())
	if err != nil {
		t.Fatalf("ensure the owner: %v", err)
	}
	orgs, err := identity.Organizations(t.Context(), db.Client, owner.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("the owner's personal organization is missing: %v", err)
	}
	org := orgs[0]

	cell := testsupport.SeedCell(t, db, core.CellFrom(settings))
	proj, err := db.Client.Project.Create().
		SetName("Test Game").SetSlug(fmt.Sprintf("game-%d", time.Now().UnixNano())).
		SetOrganizationID(org.ID).
		SetCellID(cell.ID).
		Save(t.Context())
	if err != nil {
		t.Fatalf("seed a project: %v", err)
	}

	return &fixture{
		db: db.Client, orgID: org.ID,
		roleID: roleID(t, db.Client, org.ID, authz.RoleDeveloper),
		projID: proj.ID,
	}
}

// addMember puts a fresh person in the organization with all_projects true, and returns the
// membership.
func (f *fixture) addMember(t *testing.T) *global.OrganizationMember {
	t.Helper()
	person, err := identity.EnsureUser(t.Context(), f.db, claims(t), time.Now())
	if err != nil {
		t.Fatalf("ensure a member: %v", err)
	}
	member, err := identity.AddMember(t.Context(), f.db, f.orgID, person.ID, f.roleID, time.Now())
	if err != nil {
		t.Fatalf("add the member: %v", err)
	}
	return member
}

// TestSetMemberAccessIsAllOrNothing is [TestCreateOrganizationIsAllOrNothing]'s counterpart for
// the write that replaces a member's project grants.
func TestSetMemberAccessIsAllOrNothing(t *testing.T) {
	f := newFixture(t)
	member := f.addMember(t)

	refusal := errors.New("the hook refused")
	_, _, err := identity.SetMemberAccess(t.Context(), f.db, f.orgID, member.ID,
		false, []int{f.projID}, time.Now(),
		func(tx *global.Tx, updated *global.OrganizationMember, granted []*global.Project) error {
			return refusal
		})
	if !errors.Is(err, refusal) {
		t.Fatalf("SetMemberAccess = %v, want the hook's own error", err)
	}

	// Neither half of the write survived: all_projects is still true, and no grant row exists
	// naming this project — either would mean the rollback missed something.
	after, err := f.db.OrganizationMember.Get(t.Context(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.AllProjects {
		t.Error("all_projects was narrowed despite the hook refusing")
	}
	if exists, err := f.db.MemberProject.Query().
		Where(memberproject.MemberID(member.ID)).Exist(t.Context()); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Error("a project grant exists despite the hook refusing")
	}
}

// TestAcceptInvitationIsAllOrNothing is the same property for the write that turns a token into a
// membership — the one the removal ADR and the audit ADR both named as needing it.
func TestAcceptInvitationIsAllOrNothing(t *testing.T) {
	f := newFixture(t)

	invited := identity.Claims{
		UID:           fmt.Sprintf("firebase-%d", time.Now().UnixNano()),
		Email:         fmt.Sprintf("invited-%d@studio.example", time.Now().UnixNano()),
		EmailVerified: true, Name: "New Colleague", Provider: "google.com",
	}
	person, err := identity.EnsureUser(t.Context(), f.db, invited, time.Now())
	if err != nil {
		t.Fatalf("ensure the invited person: %v", err)
	}

	token, digest, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	inv, err := f.db.Invitation.Create().
		SetOrganizationID(f.orgID).
		SetEmail(invited.Email).
		SetTokenDigest(digest).
		SetRoleID(f.roleID).
		SetAllProjects(true).
		SetExpiresAt(time.Now().Add(time.Hour)).
		Save(t.Context())
	if err != nil {
		t.Fatalf("seed the invitation: %v", err)
	}

	refusal := errors.New("the hook refused")
	_, err = identity.AcceptInvitation(t.Context(), f.db, token, person, invited.Email, time.Now(),
		func(tx *global.Tx, accepted *identity.Accepted) error { return refusal })
	if !errors.Is(err, refusal) {
		t.Fatalf("AcceptInvitation = %v, want the hook's own error", err)
	}

	// No membership was created, and the invitation is still pending rather than accepted — a
	// person left in this state by a real failure could still use the same link again.
	if exists, err := f.db.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(f.orgID), organizationmember.UserID(person.ID)).
		Exist(t.Context()); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Error("a membership exists despite the hook refusing")
	}
	after, err := f.db.Invitation.Get(t.Context(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "pending" {
		t.Errorf("invitation status = %q, want pending — the token must still work", after.Status)
	}
}
