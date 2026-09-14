// Package pgdb opens the control plane and applies its schema.
package pgdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver

	entglobal "playersense.dev/backend/ent/global"
	entregional "playersense.dev/backend/ent/regional"
)

// The two PostgreSQL schemas the control plane is split across (§99).
//
// Global holds nothing about a player. Regional holds everything that does and never
// leaves the region that produced it. They are schemas rather than servers because
// Stage 1 runs one cell (§6); keeping the boundary real now is what makes splitting them
// later a connection string rather than a data migration.
const (
	TierGlobal   = "global"
	TierRegional = "regional"
)

// Tiers is every tier, in the order they are created.
var Tiers = []string{TierGlobal, TierRegional}

// Control is the control plane: one client per tier, sharing one connection pool while
// the tiers share a server.
//
// Two clients is the point. A query written against Global cannot reach a regional
// entity, because the generated types do not exist in that package — §99's rule is
// enforced by the compiler rather than by review.
type Control struct {
	Global   *entglobal.Client
	Regional *entregional.Client

	globalDB   *sql.DB
	regionalDB *sql.DB
}

// tierDSN pins a connection to one tier's schema.
//
// search_path rather than a migration option, because that is the mechanism that
// actually places the tables — and because it is the same mechanism that will point a
// tier at a different server when the two are separated. Each tier already has its own
// pool here, so that separation is a DSN change and nothing else.
func tierDSN(dsn, tier string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse POSTGRES_DSN: %w", err)
	}
	q := u.Query()
	q.Set("search_path", tier)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func openTier(dsn, tier string) (*sql.DB, error) {
	scoped, err := tierDSN(dsn, tier)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", scoped)
	if err != nil {
		return nil, fmt.Errorf("open %s tier: %w", tier, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s tier: %w", tier, err)
	}
	return db, nil
}

// Open dials PostgreSQL and builds both tier clients.
func Open(dsn string) (*Control, error) {
	// The schemas have to exist before a connection can select them as its search_path.
	bootstrap, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	defer bootstrap.Close()
	if err := bootstrap.Ping(); err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	for _, tier := range Tiers {
		if _, err := bootstrap.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", tier)); err != nil {
			return nil, fmt.Errorf("create schema %s: %w", tier, err)
		}
	}

	globalDB, err := openTier(dsn, TierGlobal)
	if err != nil {
		return nil, err
	}
	regionalDB, err := openTier(dsn, TierRegional)
	if err != nil {
		globalDB.Close()
		return nil, err
	}
	return &Control{
		Global:     entglobal.NewClient(entglobal.Driver(entsql.OpenDB(dialect.Postgres, globalDB))),
		Regional:   entregional.NewClient(entregional.Driver(entsql.OpenDB(dialect.Postgres, regionalDB))),
		globalDB:   globalDB,
		regionalDB: regionalDB,
	}, nil
}

// DB exposes a pool for the verification queries, which read the catalog rather than any
// entity and so are indifferent to which tier answers them.
func (c *Control) DB() *sql.DB { return c.globalDB }

// Close releases both pools.
func (c *Control) Close() error {
	err := c.globalDB.Close()
	if rerr := c.regionalDB.Close(); err == nil {
		err = rerr
	}
	return err
}

// Migrate creates both tier schemas and brings each up to date.
//
// Each client holds a pool pinned to its tier's search_path, which is what puts global
// entities in `global` and regional ones in `regional`.
func (c *Control) Migrate(ctx context.Context) error {
	opts := []entschema.MigrateOption{
		entschema.WithDropColumn(true),
		entschema.WithDropIndex(true),
	}
	if err := c.Global.Schema.Create(ctx, opts...); err != nil {
		return fmt.Errorf("migrate global tier: %w", err)
	}
	if err := c.Regional.Schema.Create(ctx, opts...); err != nil {
		return fmt.Errorf("migrate regional tier: %w", err)
	}
	return nil
}
