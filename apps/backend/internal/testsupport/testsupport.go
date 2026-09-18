// Package testsupport builds the fixtures more than one binary's tests need.
//
// It exists because the interesting tests span services: a batch accepted by the gateway
// and stored by the writer is one story told by two packages, and a story told twice is a
// story that drifts. Nothing here is imported by anything that ships.
//
// Everything runs against the real stack. The three-tier key resolver, the Valkey
// namespacing and ClickHouse's insert deduplication are the things under test, and a fake
// for any of them would remove most of what is worth testing.
package testsupport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/apikey"
	"playersense.dev/backend/ent/global/cell"
	"playersense.dev/backend/ent/global/environment"
	"playersense.dev/backend/ent/global/plan"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/ent/global/role"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// Settings loads the local environment, skipping the test when there is none.
//
// Skip rather than fail: a machine with no local stack should report "not run" rather than
// a wall of failures that say nothing about the code.
func Settings(t *testing.T) config.Settings {
	t.Helper()
	settings, err := config.Load(filepath.Join(RepoRoot(t), ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	settings.LogFormat = "none"
	settings.RateLimitDisabled = true
	return settings
}

// RepoRoot walks upward for go.work.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the repository root")
		}
		dir = parent
	}
}

// Global opens the global control-plane tier, skipping when it is unreachable.
func Global(t *testing.T, settings config.Settings) *pgdb.Global {
	t.Helper()
	db := pgdb.NewGlobal(settings.GlobalPostgresDSN)
	if err := db.Start(t.Context()); err != nil {
		t.Skipf("no control plane: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// CellDB opens this cell's regional tier, skipping the test if it is unreachable.
//
// The first fixture for the cell tier. Everything before the archive lived in the global
// tier or in ClickHouse; a manifest is the first row a service writes to the database that
// sits in the cell's own region (§99).
func CellDB(t *testing.T, settings config.Settings) *pgdb.Cell {
	t.Helper()
	db := pgdb.NewCell(settings.Cell, settings.CellPostgresDSN)
	if err := db.Start(context.Background()); err != nil {
		t.Skipf("cell tier unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Cache opens this cell's Valkey, skipping when it is unreachable.
func Cache(t *testing.T, settings config.Settings) *valkey.Client {
	t.Helper()
	c := valkey.New(settings.Cell, settings.ValkeyDSN)
	if err := c.Start(t.Context()); err != nil {
		t.Skipf("no valkey: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

// ClickHouse opens this cell's analytics database, skipping when it is unreachable.
func ClickHouse(t *testing.T, settings config.Settings) *chdb.Conn {
	t.Helper()
	conn := chdb.New(settings.ClickHouseDSN)
	if err := conn.Start(t.Context()); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = conn.Stop(context.Background()) })
	return conn
}

// SeedCell makes sure a cell is in the registry and returns it.
//
// An upsert rather than a create: the registry is shared by every test in a package, so
// whichever runs first provisions it and the rest find it.
func SeedCell(t *testing.T, db *pgdb.Global, identity core.CellIdentity) *global.Cell {
	t.Helper()
	ctx := context.Background()

	found, err := db.Client.Cell.Query().Where(cell.Slug(identity.Slug)).Only(ctx)
	if err == nil {
		return found
	}
	if !global.IsNotFound(err) {
		t.Fatalf("read the cell registry: %v", err)
	}
	created, err := db.Client.Cell.Create().
		SetSlug(identity.Slug).
		SetRegion(identity.Region).
		SetIngestURL(identity.IngestURL).
		SetQueryURL(identity.QueryURL).
		SetConfigURL(identity.ConfigURL).
		Save(ctx)
	if err != nil {
		if found, qerr := db.Client.Cell.Query().Where(cell.Slug(identity.Slug)).Only(ctx); qerr == nil {
			return found
		}
		t.Fatalf("seed cell %s: %v", identity.Slug, err)
	}
	return created
}

// Project is one seeded project with a publishable key.
type Project struct {
	OrgSeq        int
	ProjectSeq    int
	EnvironmentID int

	ProjectUID string
	Secret     string

	// The slugs, because a route that names a project in its path needs them. A reporting key
	// carries its scope, so the tests that predate the dashboard's own analytics routes never
	// had to know what the project was called.
	OrgSlug     string
	ProjectSlug string
}

// SeedProject creates an organization, a project in the given cell and a project key.
//
// The environment id is returned rather than looked up later: a key minted for a test has
// to belong to *that* test's project, and "the first environment in the table" stops being
// that as soon as a test registers a project in a second cell.
func SeedProject(t *testing.T, db *pgdb.Global, tier string, cellID int) Project {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	org, err := db.Client.Organization.Create().
		SetName("Test Org").SetSlug("t-" + suffix).Save(ctx)
	if err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	proj, err := db.Client.Project.Create().
		SetName("Test Game").SetSlug("p-" + suffix).
		SetCellID(cellID).
		SetPolicyTier(project.PolicyTier(tier)).
		SetRetentionDays(30).
		SetOrganizationID(org.ID).Save(ctx)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	env, err := db.Client.Environment.Create().
		SetKind(environment.KindProduction).SetProjectID(proj.ID).Save(ctx)
	if err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	return Project{
		OrgSeq:        org.ID,
		ProjectSeq:    proj.ID,
		EnvironmentID: env.ID,
		ProjectUID:    proj.UID.String(),
		OrgSlug:       org.Slug,
		ProjectSlug:   proj.Slug,
		Secret:        SeedKey(t, db, core.KindProject, env.ID),
	}
}

// SeedMember puts a person in a seeded organization with one of its roles.
//
// The roles are seeded here rather than by [SeedProject], because a project created directly in the
// database has none: `identity.CreateOrganization` is what normally writes them, and a fixture that
// bypasses the control plane bypasses that too. Idempotent, so two members cost one seeding.
//
// It exists for the routes that authenticate a *person* rather than a key. A reporting key carries
// its own scope, so every test before those had no reason for an organization to contain anybody.
func SeedMember(t *testing.T, db *pgdb.Global, orgID int, claims identity.Claims, roleKey string) *global.OrganizationMember {
	t.Helper()
	ctx := context.Background()

	roles, err := organizationRoles(ctx, db, orgID)
	if err != nil {
		t.Fatalf("read roles: %v", err)
	}
	if len(roles) == 0 {
		if roles, err = identity.SeedRoles(ctx, db.Client, orgID); err != nil {
			t.Fatalf("seed roles: %v", err)
		}
	}
	wanted, found := roles[roleKey]
	if !found {
		t.Fatalf("no seeded role called %q", roleKey)
	}

	person, err := identity.EnsureUser(ctx, db.Client, claims, time.Now())
	if err != nil {
		t.Fatalf("seed user %s: %v", claims.Email, err)
	}
	member, err := identity.AddMember(ctx, db.Client, orgID, person.ID, wanted.ID, time.Now())
	if err != nil {
		t.Fatalf("add %s to the organization: %v", claims.Email, err)
	}
	return member
}

// organizationRoles reads what an organization already has, keyed the way SeedRoles returns them.
//
// Read before seeding because `identity.SeedRoles` is not idempotent — it inserts, and the unique
// index on (key, organization) refuses the second call. That is right for the control plane, where
// seeding happens once inside the transaction that creates an organization, and it means a fixture
// adding a second member has to check rather than retry.
func organizationRoles(ctx context.Context, db *pgdb.Global, orgID int) (map[string]*global.Role, error) {
	rows, err := db.Client.Role.Query().Where(role.OrganizationID(orgID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*global.Role, len(rows))
	for _, row := range rows {
		out[row.Key] = row
	}
	return out, nil
}

// SeedKey mints one key of the given class for an existing environment and returns the
// plaintext secret.
//
// The environment is passed rather than looked up, for the reason SeedProject gives about
// its own: a key that belongs to some other test's project authenticates perfectly and
// answers questions about the wrong data, which is the kind of test failure that reads as a
// product bug.
func SeedKey(t *testing.T, db *pgdb.Global, kind string, envID int) string {
	t.Helper()
	generated, err := core.NewAPIKey(kind, "production")
	if err != nil {
		t.Fatalf("mint %s key: %v", kind, err)
	}
	if _, err := db.Client.APIKey.Create().
		SetKind(apikey.Kind(kind)).
		SetPublicID(generated.PublicID).
		SetSecretHash(generated.Hash).
		SetEnvironmentID(envID).Save(context.Background()); err != nil {
		t.Fatalf("store %s key: %v", kind, err)
	}
	return generated.Secret
}

// SeedBillingAccount gives an organization a plan and a billing account.
//
// Both, because an account without a plan is not a state the product has: `plan_id` is required, and
// every organization gets the free plan the moment it exists. The plan is an upsert on its key, so
// whichever test runs first provisions it and the rest find it — the same reasoning [SeedCell] uses
// for the registry.
func SeedBillingAccount(t *testing.T, db *pgdb.Global, orgID int) *global.BillingAccount {
	t.Helper()
	ctx := context.Background()

	free, err := db.Client.Plan.Query().Where(plan.Key("free")).Only(ctx)
	if err != nil {
		if !global.IsNotFound(err) {
			t.Fatalf("read the plan table: %v", err)
		}
		free, err = db.Client.Plan.Create().
			SetKey("free").SetName("Free").
			SetEntitlements(map[string]int64{
				string(billing.IncludedMAU):            10_000,
				string(billing.IncludedEvents):         5_000_000,
				string(billing.IncludedStorageGBMonth): 1,
			}).
			Save(ctx)
		if err != nil {
			// Another test created it between the read and the write.
			if found, qerr := db.Client.Plan.Query().Where(plan.Key("free")).Only(ctx); qerr == nil {
				free = found
			} else {
				t.Fatalf("seed the free plan: %v", err)
			}
		}
	}

	account, err := db.Client.BillingAccount.Create().
		SetOrganizationID(orgID).SetPlanID(free.ID).Save(ctx)
	if err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	return account
}
