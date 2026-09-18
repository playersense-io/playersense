package identity_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/organizationmember"
	"playersense.dev/backend/ent/global/role"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/testsupport"
)

// Signing up has to leave an organization somebody can actually administer. A row in
// `organization_members` pointing at no role is a person who is in the organization and may do
// nothing, and nobody would notice until they tried.
func TestAnOrganizationIsSeededWithItsRoles(t *testing.T) {
	db := testsupport.Global(t, testsupport.Settings(t))
	now := time.Now().UTC()

	user, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	orgs, err := identity.Organizations(t.Context(), db.Client, user.ID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("organizations: %v (%d)", err, len(orgs))
	}
	org := orgs[0]

	roles, err := db.Client.Role.Query().Where(role.OrganizationID(org.ID)).All(t.Context())
	if err != nil {
		t.Fatalf("read roles: %v", err)
	}
	if len(roles) != len(authz.Templates()) {
		t.Fatalf("got %d roles, want %d", len(roles), len(authz.Templates()))
	}

	byKey := make(map[string]*global.Role, len(roles))
	for _, r := range roles {
		byKey[r.Key] = r
	}
	for _, template := range authz.Templates() {
		seeded, found := byKey[template.Key]
		if !found {
			t.Fatalf("the %s role was not seeded", template.Key)
		}
		if seeded.System != template.System {
			t.Errorf("%s: system is %v, want %v", template.Key, seeded.System, template.System)
		}
		if len(seeded.Permissions) != len(template.Seed()) {
			t.Errorf("%s: %d permissions, want %d",
				template.Key, len(seeded.Permissions), len(template.Seed()))
		}
	}

	// The owner's role is computed from the vocabulary, so it has to cover all of it —
	// this is what fails the day somebody adds a permission and lists it by hand instead.
	if got, want := len(byKey[authz.RoleOwner].Permissions), len(authz.All()); got != want {
		t.Errorf("owner holds %d permissions, the vocabulary has %d", got, want)
	}

	// And the creator is that owner.
	member, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(org.ID)).WithRole().Only(t.Context())
	if err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if member.Edges.Role.Key != authz.RoleOwner {
		t.Errorf("the person who created the organization is a %s", member.Edges.Role.Key)
	}
	if !member.AllProjects {
		t.Error("the owner cannot see every project")
	}
}

// The vocabulary is closed, and a role naming something outside it would grant nothing and say
// nothing. Refused on the write, by a hook, so no path can skip it — not a handler, not a seed,
// not whatever the admin console turns out to be.
func TestARoleCannotNameAPermissionThatDoesNotExist(t *testing.T) {
	db := testsupport.Global(t, testsupport.Settings(t))
	now := time.Now().UTC()

	user, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	orgs, _ := identity.Organizations(t.Context(), db.Client, user.ID)

	_, err = db.Client.Role.Create().
		SetOrganizationID(orgs[0].ID).
		SetKey("typo").
		SetName("Typo").
		// One letter out. This is the realistic failure, not "nonsense".
		SetPermissions([]string{"liveops.publsh"}).
		Save(t.Context())
	if err == nil {
		t.Fatal("a role naming a permission that does not exist was saved")
	}
	if !strings.Contains(err.Error(), "liveops.publsh") {
		t.Errorf("the error does not name the permission: %v", err)
	}

	// An update has to be checked too: the hook must not only fire on create.
	existing, err := db.Client.Role.Query().
		Where(role.OrganizationID(orgs[0].ID), role.Key(authz.RoleDeveloper)).Only(t.Context())
	if err != nil {
		t.Fatalf("read the developer role: %v", err)
	}
	if _, err := existing.Update().SetPermissions([]string{"key.reed"}).Save(t.Context()); err == nil {
		t.Error("a role was updated to name a permission that does not exist")
	}
}

// An organization with no owner is unreachable: nobody can grant a role, nobody can delete it,
// and support has to edit the database by hand. Several owners are fine and removing one of
// them is fine — it is the last one that is refused.
func TestTheLastOwnerStays(t *testing.T) {
	db := testsupport.Global(t, testsupport.Settings(t))
	now := time.Now().UTC()

	founder, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	orgs, _ := identity.Organizations(t.Context(), db.Client, founder.ID)
	org := orgs[0]

	first, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(org.ID)).Only(t.Context())
	if err != nil {
		t.Fatalf("read membership: %v", err)
	}

	if err := identity.RemoveMember(t.Context(), db.Client, org.ID, first.ID); err == nil {
		t.Fatal("the only owner was removed")
	} else if !strings.Contains(err.Error(), "owner") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	developer := roleID(t, db.Client, org.ID, authz.RoleDeveloper)
	if _, err := identity.SetMemberRole(t.Context(), db.Client, org.ID, first.ID, developer); err == nil {
		t.Fatal("the only owner was demoted, which leaves the same dead end by a different door")
	}

	// A second owner, and now either of them can go.
	colleague, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatalf("second user: %v", err)
	}
	second, err := identity.AddMember(t.Context(), db.Client, org.ID, colleague.ID,
		roleID(t, db.Client, org.ID, authz.RoleOwner), now)
	if err != nil {
		t.Fatalf("add a second owner: %v", err)
	}
	if err := identity.RemoveMember(t.Context(), db.Client, org.ID, second.ID); err != nil {
		t.Errorf("one of two owners could not be removed: %v", err)
	}
}

// A role id from another organization must not be assignable. The lookup is scoped, so this is
// a property of SetMemberRole rather than of whoever calls it.
func TestARoleFromAnotherOrganizationIsNotAssignable(t *testing.T) {
	db := testsupport.Global(t, testsupport.Settings(t))
	now := time.Now().UTC()

	mine, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatal(err)
	}
	myOrgs, _ := identity.Organizations(t.Context(), db.Client, mine.ID)
	theirOrgs, _ := identity.Organizations(t.Context(), db.Client, theirs.ID)

	member, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(myOrgs[0].ID)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foreign := roleID(t, db.Client, theirOrgs[0].ID, authz.RoleBilling)

	if _, err := identity.SetMemberRole(t.Context(), db.Client, myOrgs[0].ID, member.ID, foreign); err == nil {
		t.Fatal("a role belonging to another organization was assigned")
	}
}

// §08: "a log the operator can edit proves nothing." The property is not that these are hard to
// edit — it is that the generated builder has no method that could.
//
// Reflection rather than a comment, because the day somebody drops `.Immutable()` from one
// field is the day the guarantee quietly stops being true, and nothing else would notice.
func TestAnAppendOnlyRowHasNoSetter(t *testing.T) {
	for _, builder := range []any{
		&global.AuditEventUpdate{}, &global.AuditEventUpdateOne{},
		&global.StaffAccessUpdate{}, &global.StaffAccessUpdateOne{},
		&global.LedgerEntryUpdate{}, &global.LedgerEntryUpdateOne{},
	} {
		typ := reflect.TypeOf(builder)
		for i := range typ.NumMethod() {
			name := typ.Method(i).Name
			if strings.HasPrefix(name, "Set") || strings.HasPrefix(name, "Add") ||
				strings.HasPrefix(name, "Clear") {
				t.Errorf("%s has %s, so an append-only row can be rewritten", typ, name)
			}
		}
	}
}
