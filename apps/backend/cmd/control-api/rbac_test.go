package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/organization"
	"playersense.dev/backend/ent/global/organizationmember"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/ent/global/role"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/pgdb"
)

// stack is one application plus the things a test needs to reach around it: the fake Google
// that mints tokens, and the database, because member management has no endpoints yet.
type stack struct {
	settings config.Settings
	app      *muzak.App
	google   *fbtest.Google
	db       *pgdb.Global
}

func newStack(t *testing.T) *stack {
	t.Helper()
	settings := testSettings(t)
	google := fbtest.New(t, settings.FirebaseProjectID)
	settings.FirebaseCertificateURL = google.URL

	return &stack{
		settings: settings, app: build(settings), google: google,
		db: globalTier(t, settings),
	}
}

// globalTier opens the control plane a test reaches around the HTTP layer with.
//
// Shared with the members suite, which builds its own stack so that it can substitute a mail
// recorder for the sender — and would otherwise have copied the skip-if-absent handling that makes
// this suite run on a machine with no local stack.
func globalTier(t *testing.T, settings config.Settings) *pgdb.Global {
	t.Helper()
	db := pgdb.NewGlobal(settings.GlobalPostgresDSN)
	if err := db.Start(t.Context()); err != nil {
		t.Skipf("no control plane: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// as returns a client signed in as one person.
func (s *stack) as(t *testing.T, who fbtest.Person) *testclient.Client {
	t.Helper()
	return testclient.New(t, s.app,
		testclient.WithHeader("Authorization", "Bearer "+s.google.Token(who)))
}

// join adds a person to an organization with one of its seeded roles, which is what the
// invitation flow will do once it exists.
func (s *stack) join(t *testing.T, orgSlug string, who fbtest.Person, roleKey string, allProjects bool) {
	t.Helper()

	person, err := identity.EnsureUser(t.Context(), s.db.Client, identity.Claims{
		UID: who.UID, Email: who.Email, EmailVerified: true, Name: who.Name,
	}, time.Now())
	if err != nil {
		t.Fatalf("create %s: %v", who.Email, err)
	}

	org, err := s.db.Client.Organization.Query().
		Where(organization.Slug(orgSlug)).Only(t.Context())
	if err != nil {
		t.Fatalf("find %s: %v", orgSlug, err)
	}
	wanted, err := s.db.Client.Role.Query().
		Where(role.OrganizationID(org.ID), role.Key(roleKey)).Only(t.Context())
	if err != nil {
		t.Fatalf("find the %s role: %v", roleKey, err)
	}

	member, err := identity.AddMember(t.Context(), s.db.Client, org.ID, person.ID, wanted.ID, time.Now())
	if err != nil {
		t.Fatalf("add %s as %s: %v", who.Email, roleKey, err)
	}
	if !allProjects {
		if _, err := s.db.Client.OrganizationMember.UpdateOneID(member.ID).
			SetAllProjects(false).Save(t.Context()); err != nil {
			t.Fatalf("restrict %s: %v", who.Email, err)
		}
	}
}

// grant gives a restricted member access to one project.
func (s *stack) grant(t *testing.T, orgSlug, projectSlug string, who fbtest.Person) {
	t.Helper()
	member := s.member(t, orgSlug, who)
	proj, err := s.db.Client.Project.Query().
		Where(project.Slug(projectSlug), project.OrganizationID(member.OrganizationID)).
		Only(t.Context())
	if err != nil {
		t.Fatalf("find project %s: %v", projectSlug, err)
	}
	if _, err := s.db.Client.MemberProject.Create().
		SetMemberID(member.ID).SetProjectID(proj.ID).Save(t.Context()); err != nil {
		t.Fatalf("grant access: %v", err)
	}
}

func (s *stack) member(t *testing.T, orgSlug string, who fbtest.Person) *global.OrganizationMember {
	t.Helper()
	org, err := s.db.Client.Organization.Query().
		Where(organization.Slug(orgSlug)).Only(t.Context())
	if err != nil {
		t.Fatalf("find %s: %v", orgSlug, err)
	}
	members, err := s.db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(org.ID)).WithUser().All(t.Context())
	if err != nil {
		t.Fatalf("read members: %v", err)
	}
	for _, m := range members {
		if m.Edges.User != nil && m.Edges.User.FirebaseUID == who.UID {
			return m
		}
	}
	t.Fatalf("%s is not a member of %s", who.Email, orgSlug)
	return nil
}

// createOrg signs somebody up and has them create an organization, which makes them its owner.
func (s *stack) createOrg(t *testing.T, owner fbtest.Person) string {
	t.Helper()
	slug := unique("org")
	s.as(t, owner).Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Pixel Forge", "slug": slug})).
		AssertStatus(http.StatusCreated)
	return slug
}

func (s *stack) createProject(t *testing.T, owner fbtest.Person, orgSlug string) string {
	t.Helper()
	slug := unique("game")
	s.as(t, owner).Post("/v1/organizations/"+orgSlug+"/projects",
		testclient.JSON(map[string]string{"name": "Nebula", "slug": slug})).
		AssertStatus(http.StatusCreated)
	return slug
}

// Creating an organization makes you its owner and seeds its roles. Without that, the first
// thing anybody does leaves an organization nobody can administer.
func TestCreatingAnOrganizationMakesYouItsOwner(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)

	// An owner holds every permission, which is what minting a key proves.
	projectSlug := s.createProject(t, owner, slug)
	s.as(t, owner).Post("/v1/organizations/"+slug+"/projects/"+projectSlug+"/keys",
		testclient.JSON(map[string]string{"environment": "development", "kind": "project", "name": "ci"})).
		AssertStatus(http.StatusCreated)

	org, err := s.db.Client.Organization.Query().Where(organization.Slug(slug)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	roles, err := s.db.Client.Role.Query().Where(role.OrganizationID(org.ID)).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if roles != len(authz.Templates()) {
		t.Errorf("the new organization has %d roles, want %d", roles, len(authz.Templates()))
	}
}

// The role decides the answer, and nothing else does: two people in the same organization with
// access to the same project get different answers to the same request.
//
// DEVELOPER integrates the SDK, so it holds key.*. GROWTH MANAGER reads numbers and changes
// nothing, so it holds neither key.read nor key.create — which is the pair worth testing,
// because it is the same URL, the same project and the same access list.
func TestARoleDecidesWhatYouCanDo(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	orgSlug := s.createOrg(t, owner)
	projectSlug := s.createProject(t, owner, orgSlug)

	developer := person(t)
	s.join(t, orgSlug, developer, authz.RoleDeveloper, true)
	growth := person(t)
	s.join(t, orgSlug, growth, authz.RoleGrowthManager, true)

	keys := "/v1/organizations/" + orgSlug + "/projects/" + projectSlug + "/keys"
	newKey := testclient.JSON(map[string]string{
		"environment": "development", "kind": "project", "name": "ci"})

	s.as(t, developer).Get(keys).AssertStatus(http.StatusOK)
	s.as(t, developer).Post(keys, newKey).AssertStatus(http.StatusCreated)

	// The same two requests, the same project, a different role.
	refused := s.as(t, growth).Get(keys).AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.KeyRead)) {
		// Named because the person reading it is a studio's own developer rather than an
		// attacker probing for what exists, and "forbidden" with no reason is a support
		// ticket.
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}
	refused = s.as(t, growth).Post(keys, newKey).AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.KeyCreate)) {
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}

	// And a project the growth manager may not touch is still one they can see: the access
	// list and the role are separate axes, and this is the one place that is observable.
	s.as(t, growth).Get("/v1/organizations/" + orgSlug + "/projects/" + projectSlug).
		AssertStatus(http.StatusOK)
}

// A LIVE OPS MANAGER is LiveOps and nothing else. Reading what every player did is a different
// privilege from scheduling what they will see, and a role whose name says "LiveOps" must not
// quietly also mint credentials.
func TestTheLiveOpsManagerIsLiveOpsOnly(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	orgSlug := s.createOrg(t, owner)
	projectSlug := s.createProject(t, owner, orgSlug)

	manager := person(t)
	s.join(t, orgSlug, manager, authz.RoleLiveOpsManager, true)

	keys := "/v1/organizations/" + orgSlug + "/projects/" + projectSlug + "/keys"
	refusal := s.as(t, manager).Get(keys).AssertStatus(http.StatusForbidden)
	if !strings.Contains(refusal.String(), string(authz.KeyRead)) {
		t.Errorf("the refusal does not name the missing permission: %s", refusal)
	}
	s.as(t, manager).Post(keys,
		testclient.JSON(map[string]string{"environment": "development", "kind": "project", "name": "ci"})).
		AssertStatus(http.StatusForbidden)
}

// Somebody else's organization is not forbidden, it does not exist. A 403 confirms the slug is
// real, which turns this endpoint into a way to enumerate customers by guessing names.
func TestAnotherStudiosOrganizationIsInvisible(t *testing.T) {
	s := newStack(t)
	orgSlug := s.createOrg(t, person(t))

	stranger := person(t)
	s.as(t, stranger).Get("/v1/organizations/" + orgSlug).
		AssertStatus(http.StatusNotFound)
	s.as(t, stranger).Get("/v1/organizations/" + orgSlug + "/projects").
		AssertStatus(http.StatusNotFound)

	// And the listing shows only their own — the personal organization signing up created.
	var listed struct {
		Organizations []struct {
			Slug string `json:"slug"`
		} `json:"organizations"`
	}
	s.as(t, stranger).Get("/v1/organizations").AssertStatus(http.StatusOK).JSON(&listed)
	for _, org := range listed.Organizations {
		if org.Slug == orgSlug {
			t.Fatal("the listing includes an organization the caller does not belong to")
		}
	}
	if len(listed.Organizations) != 1 {
		t.Errorf("a new person belongs to %d organizations, want 1 (their own)",
			len(listed.Organizations))
	}
}

// "The owner chooses which people see which apps." A project outside somebody's access list is
// absent from the listing and answers 404 when opened — not 403, which would leak the names of
// the games they were not shown.
func TestAProjectOutsideTheAccessListIsInvisible(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	orgSlug := s.createOrg(t, owner)
	visible := s.createProject(t, owner, orgSlug)
	hidden := s.createProject(t, owner, orgSlug)

	contractor := person(t)
	s.join(t, orgSlug, contractor, authz.RoleDeveloper, false)
	s.grant(t, orgSlug, visible, contractor)

	client := s.as(t, contractor)
	client.Get("/v1/organizations/" + orgSlug + "/projects/" + visible).AssertStatus(http.StatusOK)
	client.Get("/v1/organizations/" + orgSlug + "/projects/" + hidden).AssertStatus(http.StatusNotFound)

	// Even with the permission: the role says what, the access list says where, and the
	// second one is checked first.
	client.Get("/v1/organizations/" + orgSlug + "/projects/" + hidden + "/keys").
		AssertStatus(http.StatusNotFound)

	var listed struct {
		Projects []struct {
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	client.Get("/v1/organizations/" + orgSlug + "/projects").
		AssertStatus(http.StatusOK).JSON(&listed)
	if len(listed.Projects) != 1 || listed.Projects[0].Slug != visible {
		t.Errorf("the listing shows %v, want only %s", listed.Projects, visible)
	}
}

// An unverified account signs in, sees the dashboard, and changes nothing.
//
// Both halves matter. A wall at the door teaches nobody what to do about it, and the banner
// the dashboard shows has to sit on top of something they can actually look at. But an
// invitation is sent to an address, and accepting one for an address Firebase has not
// confirmed is how somebody joins an organization they were never invited to — so every
// write is withheld until the link is clicked.
func TestAnUnverifiedAccountIsReadOnly(t *testing.T) {
	s := newStack(t)

	// Their organization is created while verified, then they lose the verified claim —
	// which is what somebody who has just changed their email address looks like.
	owner := person(t)
	orgSlug := s.createOrg(t, owner)
	projectSlug := s.createProject(t, owner, orgSlug)

	unverified := owner
	unverified.EmailVerified = false
	client := s.as(t, unverified)

	// Reads: the whole dashboard.
	client.Get("/v1/organizations").AssertStatus(http.StatusOK)
	client.Get("/v1/organizations/" + orgSlug).AssertStatus(http.StatusOK)
	client.Get("/v1/organizations/" + orgSlug + "/projects").AssertStatus(http.StatusOK)
	client.Get("/v1/organizations/" + orgSlug + "/projects/" + projectSlug).AssertStatus(http.StatusOK)
	client.Get("/v1/organizations/" + orgSlug + "/projects/" + projectSlug + "/keys").
		AssertStatus(http.StatusOK)

	// Writes: none, even though this is the organization's owner.
	refusal := client.Post("/v1/organizations/"+orgSlug+"/projects/"+projectSlug+"/keys",
		testclient.JSON(map[string]string{
			"environment": "development", "kind": "project", "name": "ci"})).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refusal.String(), "verified") {
		t.Errorf("the refusal does not say it is about verification: %s", refusal)
	}

	client.Post("/v1/organizations/"+orgSlug+"/projects",
		testclient.JSON(map[string]string{"name": "Second", "slug": unique("game")})).
		AssertStatus(http.StatusForbidden)

	// And the one write with no permission behind it, which Actor.ReadOnly cannot reach
	// because there is no organization yet to hold a role.
	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Another", "slug": unique("org")})).
		AssertStatus(http.StatusForbidden)

	// The same person, verified, can do all three.
	verified := s.as(t, owner)
	verified.Post("/v1/organizations/"+orgSlug+"/projects/"+projectSlug+"/keys",
		testclient.JSON(map[string]string{
			"environment": "development", "kind": "project", "name": "ci"})).
		AssertStatus(http.StatusCreated)
	verified.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Another", "slug": unique("org")})).
		AssertStatus(http.StatusCreated)
}
