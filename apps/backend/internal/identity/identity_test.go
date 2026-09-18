package identity_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/role"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/testsupport"
)

// roleID finds one of an organization's seeded roles by key.
func roleID(t *testing.T, db *global.Client, orgID int, key string) int {
	t.Helper()
	found, err := db.Role.Query().
		Where(role.OrganizationID(orgID), role.Key(key)).Only(t.Context())
	if err != nil {
		t.Fatalf("look up the %s role: %v", key, err)
	}
	return found.ID
}

func claims(t *testing.T) identity.Claims {
	t.Helper()
	unique := rand.Int63()
	return identity.Claims{
		UID:           fmt.Sprintf("firebase-%d", unique),
		Email:         fmt.Sprintf("dev-%d@studio.example", unique),
		EmailVerified: true,
		Name:          "Ada Lovelace",
		Provider:      "google.com",
	}
}

// The invariant everything else rests on: nobody is ever signed in with nowhere to be.
//
// Without it, every project, key and invoice would have to cope with a user who belongs to no
// organization — and the dashboard would need an empty state that has to be got right.
func TestSigningUpCreatesSomewhereToBe(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)
	now := time.Now().UTC()

	user, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	orgs, err := identity.Organizations(t.Context(), db.Client, user.ID)
	if err != nil {
		t.Fatalf("list organizations: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("a new user belongs to %d organizations, want exactly 1", len(orgs))
	}
	if orgs[0].MemberLimit <= 0 {
		t.Errorf("member limit is %d", orgs[0].MemberLimit)
	}
}

// Firebase is the authority and the uid is the join. Signing in again updates the profile and
// creates nothing — otherwise every sign-in would leave a duplicate organization behind.
func TestSigningInAgainUpdatesRatherThanDuplicates(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)
	now := time.Now().UTC()

	first := claims(t)
	created, err := identity.EnsureUser(t.Context(), db.Client, first, now)
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	// They change their display name at Google and sign in with a different provider.
	second := first
	second.Name = "Ada King"
	second.Provider = "github.com"

	again, err := identity.EnsureUser(t.Context(), db.Client, second, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if again.ID != created.ID {
		t.Fatalf("a second sign-in created a second user (%d then %d)", created.ID, again.ID)
	}
	if again.Name != "Ada King" {
		t.Errorf("name = %q, want the one on the current token", again.Name)
	}
	if again.LastProvider != "github.com" {
		t.Errorf("provider = %q, want github.com", again.LastProvider)
	}

	orgs, err := identity.Organizations(t.Context(), db.Client, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 {
		t.Errorf("signing in twice produced %d organizations", len(orgs))
	}
}

// An email address is a label, not a key: people marry and studios rename their domains. A
// user matched on email would silently become a different person.
func TestTheUidIsTheKeyAndEmailIsALabel(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)
	now := time.Now().UTC()

	original := claims(t)
	created, err := identity.EnsureUser(t.Context(), db.Client, original, now)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	renamed := original
	renamed.Email = strings.Replace(original.Email, "@studio.example", "@newstudio.example", 1)

	same, err := identity.EnsureUser(t.Context(), db.Client, renamed, now)
	if err != nil {
		t.Fatalf("sign in after an email change: %v", err)
	}
	if same.ID != created.ID {
		t.Error("changing an email address created a second person")
	}
	if same.Email != renamed.Email {
		t.Errorf("email = %q, want the new one", same.Email)
	}
}

// An organization is the billing and ownership boundary, and one that grows without bound is
// one where nobody knows who can reach the data (§08).
func TestAnOrganizationRefusesToExceedItsCeiling(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)
	now := time.Now().UTC()

	owner, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatal(err)
	}
	orgs, err := identity.Organizations(t.Context(), db.Client, owner.ID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("setup: %v, %d organizations", err, len(orgs))
	}
	org := orgs[0]

	// A small ceiling, so the test is about the rule rather than about creating 50 people.
	if _, err := org.Update().SetMemberLimit(3).Save(t.Context()); err != nil {
		t.Fatalf("set limit: %v", err)
	}

	// The owner is already one of the three.
	for i := range 2 {
		joiner, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
		if err != nil {
			t.Fatalf("create joiner %d: %v", i, err)
		}
		if _, err := identity.AddMember(t.Context(), db.Client, org.ID, joiner.ID,
			roleID(t, db.Client, org.ID, authz.RoleDeveloper), now); err != nil {
			t.Fatalf("add member %d: %v", i, err)
		}
	}

	overflow, err := identity.EnsureUser(t.Context(), db.Client, claims(t), now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = identity.AddMember(t.Context(), db.Client, org.ID, overflow.ID,
		roleID(t, db.Client, org.ID, authz.RoleDeveloper), now)
	if err == nil {
		t.Fatal("a fourth member joined an organization limited to three")
	}
	if !strings.Contains(err.Error(), "as many members") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// A token is returned once and stored only as a digest: an invitation link reaches an inbox,
// and the database must not contain anything that could reissue one.
func TestAnInvitationTokenIsNotRecoverableFromWhatIsStored(t *testing.T) {
	token, digest, err := identity.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if token == "" || digest == "" {
		t.Fatal("empty token or digest")
	}
	if strings.Contains(digest, token) {
		t.Error("the stored digest contains the token")
	}
	if identity.Digest(token) != digest {
		t.Error("the digest is not reproducible from the token")
	}

	// Two tokens are never the same, which is the whole security property.
	other, _, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if other == token {
		t.Fatal("two invitations produced the same token")
	}
	if identity.Digest(other) == digest {
		t.Error("two different tokens share a digest")
	}
}

func TestATokenlessOrEmaillessSignInIsRefused(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)

	for name, broken := range map[string]identity.Claims{
		"no uid":   {Email: "someone@studio.example"},
		"no email": {UID: "firebase-abc"},
		"blank":    {},
	} {
		if _, err := identity.EnsureUser(t.Context(), db.Client, broken, time.Now()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
