// Package pgdb opens the control plane's two databases.
//
// There are two, and they are in different places for different reasons (§99).
//
// The **global tier** is one database, US-hosted, holding accounts, organisations,
// projects, keys, billing and aggregate counts. It holds no player data at all, which is
// what lets there be exactly one of it — and what makes hosting it beside the company's
// own entity and its payment processor an unremarkable choice.
//
// The **cell tier** is one database per cell, hosted in that cell's region, holding
// everything that names a player: verified purchases, deletion requests, exports, archive
// manifests. It never leaves the region that produced it.
//
// One rule separates them, short enough to check in review:
//
//	a player identifier never appears in the global tier
//
// `migrate verify` enforces it mechanically. That check matters more now than when the
// two shared a server: it is the proof that no player data reaches the US.
package pgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver

	entglobal "playersense.dev/backend/ent/global"
	entregional "playersense.dev/backend/ent/regional"
	"playersense.dev/backend/internal/dataclass"

	// Blank imports, and they are load-bearing rather than tidy.
	//
	// ent moves a client's defaults, validators and hooks into a `runtime` subpackage the
	// moment any of its schemas declares a Hook — it has to, because the generated code and
	// the schema would otherwise import each other. Nothing references that package, so
	// without this line the linker drops it and every `DefaultUID`, `DefaultCreatedAt` and
	// hook stays a nil function pointer.
	//
	// The failure is a nil dereference inside a generated `defaults()`, on the first row
	// anything creates. Not at startup, not during migration, and not in a test that builds
	// its client through enttest — which imports this itself, and is why the identity suite
	// kept passing while the binaries would have panicked. It goes here, where both
	// production and tests open their clients, so that no main() can be the one that forgot.
	//
	// The regional client has no hooks today and still stitches inline, so its import does
	// nothing yet. It is here so that the first hook added over there does not reintroduce
	// this.
	_ "playersense.dev/backend/ent/global/runtime"
	_ "playersense.dev/backend/ent/regional/runtime"
)

var (
	// ErrNotStarted reports use of a database that never connected.
	ErrNotStarted = errors.New("database is not started")
	// ErrWrongTier reports an attempt to use one tier's database as the other's.
	ErrWrongTier = errors.New("wrong tier for this database")
	// ErrNoTier reports a database that has never been migrated, so it holds neither.
	ErrNoTier = errors.New("this database holds no tier yet")
)

// ---- global tier ------------------------------------------------------------

// Global is the single, global control-plane database.
type Global struct {
	dsn string

	Client *entglobal.Client
	db     *sql.DB
}

// NewGlobal returns an unconnected global tier.
//
// Construction dials nothing. It implements muzak.Lifecycle, so an application opens it
// before accepting traffic and closes it after draining.
func NewGlobal(dsn string) *Global { return &Global{dsn: dsn} }

// Name identifies the component in start-up and shutdown logs.
func (g *Global) Name() string { return "control-plane-global" }

// Start dials the global tier.
func (g *Global) Start(ctx context.Context) error {
	db, err := open(ctx, g.dsn, "global tier")
	if err != nil {
		return err
	}
	g.db = db
	g.Client = entglobal.NewClient(entglobal.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return nil
}

// Stop releases the pool.
func (g *Global) Stop(context.Context) error { return g.Close() }

// Ready reports whether Start has run.
//
// Callers ask rather than assume: a tier that never started has a nil client, and
// dereferencing one gives a panic instead of an error a caller can act on.
func (g *Global) Ready() bool { return g != nil && g.db != nil && g.Client != nil }

// DB exposes the pool for catalog queries, which read no entity.
func (g *Global) DB() *sql.DB { return g.db }

// Close releases the pool, tolerating a tier that never started.
func (g *Global) Close() error {
	if g == nil || g.db == nil {
		return nil
	}
	err := g.db.Close()
	g.db, g.Client = nil, nil
	return err
}

// Migrate brings the global tier up to date.
//
// It refuses to run against a database that already holds a cell tier. Without that check,
// pointing this at a cell's database would grow a second `organizations` and `projects`
// there — the control plane forking rather than erroring, which is the kind of mistake
// discovered weeks later by two customers holding the same project id.
func (g *Global) Migrate(ctx context.Context) error {
	if !g.Ready() {
		return ErrNotStarted
	}
	if err := assertTierAbsent(ctx, g.db, cellMarkerTable, "cell"); err != nil {
		return err
	}
	if err := ensureMarker(ctx, g.db, globalMarkerTable); err != nil {
		return err
	}
	return g.Client.Schema.Create(ctx,
		entschema.WithDropColumn(true), entschema.WithDropIndex(true))
}

// AssertTier confirms this database is the global tier and has been migrated.
//
// A service calls it at start-up, because the alternative to asking is finding out: a
// `GLOBAL_POSTGRES_DSN` copied from the wrong line of a secret store points at a cell's
// database, where every query returns nothing rather than failing, and a control plane
// that appears to have lost its customers is a worse morning than one that refused to
// boot.
func (g *Global) AssertTier(ctx context.Context) error {
	if !g.Ready() {
		return ErrNotStarted
	}
	return assertTier(ctx, g.db, globalMarkerTable, cellMarkerTable, "global")
}

// ---- cell tier --------------------------------------------------------------

// Cell is one cell's regional database.
type Cell struct {
	slug string
	dsn  string

	Client *entregional.Client
	db     *sql.DB
}

// NewCell returns an unconnected cell tier.
func NewCell(slug, dsn string) *Cell { return &Cell{slug: slug, dsn: dsn} }

// Name identifies the component in start-up and shutdown logs.
func (c *Cell) Name() string { return "control-plane-cell" }

// Slug is the cell this tier belongs to.
func (c *Cell) Slug() string { return c.slug }

// Start dials the cell tier.
func (c *Cell) Start(ctx context.Context) error {
	db, err := open(ctx, c.dsn, "cell tier "+c.slug)
	if err != nil {
		return err
	}
	c.db = db
	c.Client = entregional.NewClient(entregional.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return nil
}

// Stop releases the pool.
func (c *Cell) Stop(context.Context) error { return c.Close() }

// Ready reports whether Start has run.
func (c *Cell) Ready() bool { return c != nil && c.db != nil && c.Client != nil }

// DB exposes the pool for catalog queries.
func (c *Cell) DB() *sql.DB { return c.db }

// Close releases the pool, tolerating a tier that never started.
func (c *Cell) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db, c.Client = nil, nil
	return err
}

// Migrate brings this cell's regional tier up to date, refusing to run against the global
// database for the same reason Global.Migrate refuses the reverse.
func (c *Cell) Migrate(ctx context.Context) error {
	if !c.Ready() {
		return ErrNotStarted
	}
	if err := assertTierAbsent(ctx, c.db, globalMarkerTable, "global"); err != nil {
		return err
	}
	if err := ensureMarker(ctx, c.db, cellMarkerTable); err != nil {
		return err
	}
	return c.Client.Schema.Create(ctx,
		entschema.WithDropColumn(true), entschema.WithDropIndex(true))
}

// AssertTier confirms this database is a cell tier and has been migrated.
func (c *Cell) AssertTier(ctx context.Context) error {
	if !c.Ready() {
		return ErrNotStarted
	}
	return assertTier(ctx, c.db, cellMarkerTable, globalMarkerTable, "cell")
}

// ---- failure reasons --------------------------------------------------------

// MaxFailureReason is the width of every `failure_reason` column in the cell tier.
//
// One number, declared once, because three different jobs write to those columns — §60's
// expiry, §61's purge and §101's export — and a helper per package is three chances for one
// of them to write a string the database then refuses.
const MaxFailureReason = 512

// Reason fits an explanation into that column.
//
// Truncated rather than rejected: losing the tail of an explanation is much better than
// losing the record that something failed at all, which is what an INSERT that errors on
// length would cost. Cut on a rune boundary, so the stored value is still text.
func Reason(s string) string {
	if len(s) <= MaxFailureReason {
		return s
	}
	cut := MaxFailureReason - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// ---- tier markers -----------------------------------------------------------

// Each database carries a marker table naming the tier it holds.
//
// A marker rather than a heuristic over table names: "does `organizations` exist" answers
// the question only until somebody adds a table by that name for another reason. The
// marker is explicit, and it makes the refusal a one-line check.
const (
	globalMarkerTable = "playersense_global_tier"
	cellMarkerTable   = "playersense_cell_tier"
)

func ensureMarker(ctx context.Context, db *sql.DB, table string) error {
	if _, err := db.ExecContext(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %q (
			tier       text PRIMARY KEY,
			created_at timestamptz NOT NULL DEFAULT now()
		)`, table)); err != nil {
		return fmt.Errorf("create tier marker: %w", err)
	}
	// §96 says a column cannot exist without a classification, and this table is not
	// exempt from that because it happens to be written in SQL rather than declared in
	// Ent. `migrate verify` reads pg_description and would otherwise report two
	// unclassified columns that nobody could fix from a schema file.
	for _, column := range []string{"tier", "created_at"} {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`COMMENT ON COLUMN %q.%q IS '%s'`, table, column, dataclass.None)); err != nil {
			return fmt.Errorf("classify tier marker: %w", err)
		}
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %q (tier) VALUES ($1) ON CONFLICT DO NOTHING`, table), table)
	return err
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		table).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tier marker: %w", err)
	}
	return exists, nil
}

func assertTierAbsent(ctx context.Context, db *sql.DB, marker, otherTier string) error {
	exists, err := tableExists(ctx, db, marker)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: this database already holds the %s tier, and a database holds exactly one",
			ErrWrongTier, otherTier)
	}
	return nil
}

func assertTier(ctx context.Context, db *sql.DB, own, other, ownTier string) error {
	if err := assertTierAbsent(ctx, db, other, tierName(other)); err != nil {
		return err
	}
	exists, err := tableExists(ctx, db, own)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w; run `migrate %s up` against it", ErrNoTier, ownTier)
	}
	return nil
}

func tierName(marker string) string {
	if marker == globalMarkerTable {
		return "global"
	}
	return "cell"
}

// Pool bounds.
//
// database/sql defaults MaxOpenConns to *unlimited*, which is not a default any service
// pointed at a shared PostgreSQL should keep. A burst of requests then opens connections
// until the server refuses them — and the server refuses them for everything, so one
// service under load takes down every other service on that instance. PostgreSQL's own
// max_connections is typically 100, and four services plus migrations share it here.
//
// Bounded low rather than high on purpose. None of these services is supposed to be
// PostgreSQL-heavy: §3.2 keeps ingestion off it entirely, the key cache exists so that
// authentication does not query it per request, and the analytics path never touches it.
// A service that needs more than this is a service doing something §11 says it should not.
const (
	maxOpenConns    = 20
	maxIdleConns    = 5
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute
)

func open(ctx context.Context, dsn, what string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("no DSN configured for the %s", what)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", what, err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	// A lifetime as well as an idle timeout, so a failover, a rotated credential or a
	// resized instance is picked up without restarting the service.
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s: %w", what, err)
	}
	return db, nil
}
