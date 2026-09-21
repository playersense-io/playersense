// Package identity turns a verified sign-in into a row, and holds the two rules the schema
// cannot express.
//
// # What it deliberately does not do
//
// It does not verify anything. `Claims` is what a caller has *already* proved — the HTTP layer
// checks the Firebase ID token's signature, issuer, audience and expiry, and passes the result
// here. Splitting it that way is the same split `geoip` uses: the domain takes a value that has
// been established, not a request it has to interrogate, so every rule below is testable
// without a network.
//
// It does not decide what a permission *means*, either — internal/authz owns that vocabulary.
// What this package does own is placing a person in an organization with a role, and the two
// rules about roles that the schema cannot express: an organization is created with the six
// seeded roles, and its last owner cannot be removed or demoted.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/role"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/authz"
)

// Claims is a Firebase ID token that somebody has already verified.
//
// PlayerSense never holds a credential: no password column, no reset flow, no session store.
// This is the projection of an identity Firebase vouched for.
type Claims struct {
	// UID is Firebase's own, and the only identifier that is stable. An email address
	// changes — somebody marries, a studio renames its domain — and matching on one would
	// silently turn a user into a different person.
	UID string

	Email string
	// EmailVerified is the only claim that separates a passwordless sign-in from a
	// password one, given Provider cannot: an email-link sign-in arrives already verified,
	// because clicking the link is what proves control of the address, while a fresh
	// password sign-up arrives false.
	EmailVerified bool
	Name          string
	AvatarURL     string

	// Provider is Firebase's `sign_in_provider`: "password", "google.com",
	// "apple.com", "microsoft.com", "github.com".
	//
	// **"emailLink" is not one of them**, which is worth stating because it looks like it
	// should be. Email/password and email-link are two *methods* of one provider in
	// Firebase's model — EmailAuthProvider.PROVIDER_ID is "password" for both — and the
	// token records the provider, not the method. Measured against Firebase's own
	// emulator: an email-link sign-in reports "password", identically to a password one.
	//
	// So this field cannot tell a passwordless sign-in from a password one, and nothing
	// should try. See EmailVerified.
	Provider string
}

// Valid reports whether the claims carry enough to act on.
func (c Claims) Valid() error {
	switch {
	case strings.TrimSpace(c.UID) == "":
		return errors.New("the token has no uid")
	case strings.TrimSpace(c.Email) == "":
		return errors.New("the token has no email address")
	}
	return nil
}

// ErrMemberLimit is returned when an organization is full.
var ErrMemberLimit = errors.New("this organization has as many members as it may have")

// EnsureUser projects a verified sign-in onto a row, and guarantees the invariant.
//
// # Every user belongs to at least one organization
//
// Signing up creates a personal one, so there is no state where somebody is signed in with
// nowhere to be. That invariant is what lets every project, key and invoice hang off an
// organization rather than off a user — and it removes the empty-state screen that would
// otherwise have to exist and be got right.
//
// Idempotent: the same token twice updates the profile and creates nothing.
func EnsureUser(ctx context.Context, db *ent.Client, claims Claims, now time.Time) (*ent.User, error) {
	if err := claims.Valid(); err != nil {
		return nil, err
	}

	existing, err := db.User.Query().Where(user.FirebaseUID(claims.UID)).Only(ctx)
	switch {
	case err == nil:
		// Mirrored from the token on every sign-in rather than stored once: a person who
		// changes their Google avatar or verifies their address should not have to be
		// told to update a profile page.
		update := existing.Update().
			SetEmail(claims.Email).
			SetEmailVerified(claims.EmailVerified).
			SetLastSeenAt(now)
		if claims.Name != "" {
			update.SetName(claims.Name)
		}
		if claims.AvatarURL != "" {
			update.SetAvatarURL(claims.AvatarURL)
		}
		if claims.Provider != "" {
			update.SetLastProvider(claims.Provider)
		}
		return update.Save(ctx)

	case !ent.IsNotFound(err):
		return nil, fmt.Errorf("look up user: %w", err)
	}

	created, err := db.User.Create().
		SetFirebaseUID(claims.UID).
		SetEmail(claims.Email).
		SetEmailVerified(claims.EmailVerified).
		SetName(claims.Name).
		SetAvatarURL(claims.AvatarURL).
		SetLastProvider(claims.Provider).
		SetLastSeenAt(now).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	if _, err := createPersonalOrganization(ctx, db, created, now); err != nil {
		return nil, err
	}
	return created, nil
}

// createPersonalOrganization gives a new user somewhere to be.
func createPersonalOrganization(ctx context.Context, db *ent.Client, owner *ent.User, now time.Time) (*ent.Organization, error) {
	name := owner.Name
	if name == "" {
		name = strings.SplitN(owner.Email, "@", 2)[0]
	}
	// nil: nobody will ever read this organization's log but the person who just created it by
	// signing up, and they already know. Left unaudited on purpose rather than by omission.
	return CreateOrganization(ctx, db, name, uniqueSlug(ctx, db, name), owner.ID, now, nil)
}

// CreateOrganization creates one, seeds its roles, and makes the caller its owner.
//
// All three or none of them. An organization that exists with no owner cannot be administered
// by anybody — nobody can grant a role, nobody can delete it — and one with no roles cannot
// have a member added. Either is a repair somebody does by hand against the database, which is
// why this is a transaction rather than three statements that usually all succeed.
//
// `also`, when given, runs inside that same transaction, right before commit, with the
// organization this call just created. It exists for the audit entry: "an organization was
// created" has to be true exactly when the row is, and a caller writing it afterwards, against a
// different connection, cannot promise that. Pass nil when there is nothing to add.
func CreateOrganization(ctx context.Context, db *ent.Client, name, slug string, ownerID int,
	now time.Time, also func(tx *ent.Tx, org *ent.Organization) error,
) (*ent.Organization, error) {
	tx, err := db.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	org, err := tx.Organization.Create().
		SetName(name).
		SetSlug(slug).
		Save(ctx)
	if err != nil {
		// Returned unwrapped so that a duplicate slug still reads as a constraint
		// violation to core.AsHTTPError, which turns it into a 409 rather than a 500.
		return nil, err
	}

	roles, err := SeedRoles(ctx, tx.Client(), org.ID)
	if err != nil {
		return nil, err
	}

	if _, err := tx.OrganizationMember.Create().
		SetOrganizationID(org.ID).
		SetUserID(ownerID).
		SetRoleID(roles[authz.RoleOwner].ID).
		SetAllProjects(true).
		SetJoinedAt(now).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("make the creator its owner: %w", err)
	}

	if also != nil {
		if err := also(tx, org); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return org, nil
}

// SeedRoles writes the six roles a new organization starts with, keyed by role key.
//
// Rows rather than constants, so the four non-system ones are a starting point rather than a
// ruling: a studio that does not run experiments deletes GROWTH MANAGER and one that splits
// LiveOps differently edits it. The two system roles are written too, even though their
// permissions are computed rather than read back — a table where OWNER is absent is a table
// that does not explain itself to the person reading it.
func SeedRoles(ctx context.Context, db *ent.Client, orgID int) (map[string]*ent.Role, error) {
	out := make(map[string]*ent.Role, len(authz.Templates()))
	for _, template := range authz.Templates() {
		permissions := template.Seed()
		names := make([]string, len(permissions))
		for i, permission := range permissions {
			names[i] = string(permission)
		}

		created, err := db.Role.Create().
			SetOrganizationID(orgID).
			SetKey(template.Key).
			SetName(template.Name).
			SetSystem(template.System).
			SetPermissions(names).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("seed role %q: %w", template.Key, err)
		}
		out[template.Key] = created
	}
	return out, nil
}

// ErrLastOwner is returned when the only owner would stop being one.
var ErrLastOwner = errors.New("an organization must keep at least one owner")

// RemoveMember takes a person out of an organization, refusing to remove its last owner.
//
// The rule lives here beside the member cap, which is the other invariant the schema cannot
// express. Both are "how many are there now" questions, and SQL can state neither as a column
// constraint — a trigger could, and would put the rule somewhere nobody reading this package
// would find it.
//
// Several owners are allowed and removing one of them is fine. It is the *last* one that is
// refused, because an organization with no owner is unreachable: nobody can grant a role,
// nobody can delete it, and support has to edit the database by hand.
func RemoveMember(ctx context.Context, db *ent.Client, orgID, memberID int) error {
	member, err := db.OrganizationMember.Query().
		Where(
			organizationmember.ID(memberID),
			organizationmember.OrganizationID(orgID),
		).
		WithRole().
		Only(ctx)
	if err != nil {
		return fmt.Errorf("look up member: %w", err)
	}

	if member.Edges.Role != nil && member.Edges.Role.Key == authz.RoleOwner {
		owners, err := countOwners(ctx, db, orgID)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	return db.OrganizationMember.DeleteOneID(memberID).Exec(ctx)
}

// SetMemberRole changes what one person may do, refusing to demote the last owner.
//
// The same rule as RemoveMember and for the same reason: demoting the last owner leaves an
// organization nobody owns, which is the identical dead end reached by a different door. A
// check in only one of the two is a check that holds until somebody uses the other.
func SetMemberRole(ctx context.Context, db *ent.Client, orgID, memberID, roleID int) (*ent.OrganizationMember, error) {
	member, err := db.OrganizationMember.Query().
		Where(
			organizationmember.ID(memberID),
			organizationmember.OrganizationID(orgID),
		).
		WithRole().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("look up member: %w", err)
	}

	target, err := db.Role.Query().
		Where(role.ID(roleID), role.OrganizationID(orgID)).
		Only(ctx)
	if err != nil {
		// Scoped to the organization on purpose: a role id from another organization
		// must not be assignable, and looking it up unscoped would make that a bug in
		// whichever caller forgot to check.
		return nil, fmt.Errorf("look up role: %w", err)
	}

	wasOwner := member.Edges.Role != nil && member.Edges.Role.Key == authz.RoleOwner
	if wasOwner && target.Key != authz.RoleOwner {
		owners, err := countOwners(ctx, db, orgID)
		if err != nil {
			return nil, err
		}
		if owners <= 1 {
			return nil, ErrLastOwner
		}
	}

	return db.OrganizationMember.UpdateOneID(memberID).SetRoleID(target.ID).Save(ctx)
}

func countOwners(ctx context.Context, db *ent.Client, orgID int) (int, error) {
	owners, err := db.OrganizationMember.Query().
		Where(
			organizationmember.OrganizationID(orgID),
			organizationmember.HasRoleWith(role.Key(authz.RoleOwner)),
		).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return owners, nil
}

// AddMember places a user in an organization, refusing to exceed its ceiling.
//
// Checked here rather than as a database constraint, because the rule is "how many are there
// now", which is not something a column constraint can say. A trigger could, and would put the
// rule somewhere nobody reading the Go would find it.
//
// The race is real and bounded: two concurrent adds can both see 49. An organization of 51 is
// not a security failure and the next add is refused, so this is a ceiling rather than a lock —
// and taking one would serialise every invitation accept in the product.
func AddMember(ctx context.Context, db *ent.Client, orgID, userID, roleID int, now time.Time) (*ent.OrganizationMember, error) {
	org, err := db.Organization.Get(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("look up organization: %w", err)
	}

	members, err := db.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(orgID)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}
	if members >= org.MemberLimit {
		return nil, fmt.Errorf("%w: %d of %d", ErrMemberLimit, members, org.MemberLimit)
	}

	return db.OrganizationMember.Create().
		SetOrganizationID(orgID).
		SetUserID(userID).
		SetRoleID(roleID).
		SetJoinedAt(now).
		Save(ctx)
}

// Organizations lists what a user belongs to.
func Organizations(ctx context.Context, db *ent.Client, userID int) ([]*ent.Organization, error) {
	return db.Organization.Query().
		Where(organization.HasMembersWith(organizationmember.UserID(userID))).
		Order(ent.Asc(organization.FieldName)).
		All(ctx)
}

// ---- invitation tokens -------------------------------------------------------

// TokenBytes is the length of an invitation token before encoding.
//
// 32 bytes of crypto/rand. There is nothing to guess and nothing to brute-force, which is why
// the digest below is a plain SHA-256 rather than the Argon2id an API key's private half uses:
// a KDF exists to slow an attacker down against a secret a *person* chose, and this is not one.
const TokenBytes = 32

// NewToken returns a token and the digest to store.
//
// The token is returned once, put in a link, and never stored. An invitation link reaches an
// inbox, an inbox is not a secure channel, and the database must not contain anything that
// could reissue one.
func NewToken() (token string, digest string, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate invitation token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, Digest(token), nil
}

// Digest is what is stored for a token.
func Digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// uniqueSlug turns a name into a URL-safe slug that no organization is using.
//
// Slugs are unique across the whole system, and two people called "studio" will sign up. The
// suffix is random rather than a counter: a counter would tell the second person how many
// organizations of that name exist, and it needs a read-modify-write that two concurrent
// signups would lose.
//
// A collision after the suffix is possible and is handled by trying again rather than by
// widening the suffix — four random characters over a namespace this small collide about once
// in a million, and a retry costs one query in that case.
func uniqueSlug(ctx context.Context, db *ent.Client, name string) string {
	base := slugify(name)
	if base == "" {
		base = "org"
	}

	for range 5 {
		candidate := base + "-" + randomSuffix()
		exists, err := db.Organization.Query().
			Where(organization.Slug(candidate)).Exist(ctx)
		if err == nil && !exists {
			return candidate
		}
	}
	// Five collisions in a row is not a thing that happens; falling back to a longer
	// suffix beats returning an empty slug and failing the signup.
	return base + "-" + randomSuffix() + randomSuffix()
}

func slugify(name string) string {
	var out strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			previousDash = false
		case !previousDash && out.Len() > 0:
			out.WriteByte('-')
			previousDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func randomSuffix() string {
	raw := make([]byte, 3)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail in practice, and a signup must not.
		return fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
