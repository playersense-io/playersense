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

	"github.com/google/uuid"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/cell"
	"playersense.dev/backend/ent/global/environment"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
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

	generated, err := core.NewAPIKey(core.KindProject, "production")
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if _, err := db.Client.APIKey.Create().
		SetKind("project").
		SetPublicID(generated.PublicID).
		SetSecretHash(generated.Hash).
		SetEnvironmentID(env.ID).Save(ctx); err != nil {
		t.Fatalf("store key: %v", err)
	}

	return Project{
		OrgSeq:        org.ID,
		ProjectSeq:    proj.ID,
		EnvironmentID: env.ID,
		ProjectUID:    proj.UID.String(),
		Secret:        generated.Secret,
	}
}
