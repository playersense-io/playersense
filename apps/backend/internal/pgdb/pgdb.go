// Package pgdb opens the PostgreSQL database.
//
// One database, holding accounts, organisations, projects, keys, billing and aggregate
// counts alongside the records that name a player: verified purchases, deletion requests,
// exports and archive manifests.
//
// It was two — a US-hosted global tier that could hold no player identifier, and one
// regional tier per cell that held nothing else — and the pair was enforced by marker
// tables, a package boundary and a residency check. That split bought a residency story
// nobody has yet asked for, at the cost of two pools, two clients and a rule every query
// had to be read against. §96's classification survives it: every column still declares
// what it holds, and `migrate verify` still reads those declarations back off the live
// database, which is the check that was actually doing the work.
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

	"playersense.dev/backend/ent"

	// A blank import, and it is load-bearing rather than tidy.
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
	_ "playersense.dev/backend/ent/runtime"
)

// ErrNotStarted reports use of a database that never connected.
var ErrNotStarted = errors.New("database is not started")

// DB is the PostgreSQL database.
type DB struct {
	dsn string

	Client *ent.Client
	db     *sql.DB
}

// New returns an unconnected database.
//
// Construction dials nothing. It implements muzak.Lifecycle, so an application opens it
// before accepting traffic and closes it after draining.
func New(dsn string) *DB { return &DB{dsn: dsn} }

// Name identifies the component in start-up and shutdown logs.
func (d *DB) Name() string { return "postgres" }

// Start dials the database, once.
//
// A second Start while the pool is open is a no-op. One *DB is often a singleton of more than one
// application in a process (the tests build two over one database), and each application starts
// its lifecycle components; opening a fresh pool each time replaced the open one without closing
// it, and every test that did so left its connections behind until the server refused new ones.
func (d *DB) Start(ctx context.Context) error {
	if d.db != nil {
		return nil
	}
	db, err := open(ctx, d.dsn, "database")
	if err != nil {
		return err
	}
	d.db = db
	d.Client = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return nil
}

// Stop releases the pool.
func (d *DB) Stop(context.Context) error { return d.Close() }

// Ready reports whether Start has run.
//
// Callers ask rather than assume: a database that never started has a nil client, and
// dereferencing one gives a panic instead of an error a caller can act on.
func (d *DB) Ready() bool { return d != nil && d.db != nil && d.Client != nil }

// DB exposes the pool for catalog queries, which read no entity.
func (d *DB) DB() *sql.DB { return d.db }

// Close releases the pool, tolerating a database that never started.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db, d.Client = nil, nil
	return err
}

// Migrate brings the schema up to date.
func (d *DB) Migrate(ctx context.Context) error {
	if !d.Ready() {
		return ErrNotStarted
	}
	return d.Client.Schema.Create(ctx,
		entschema.WithDropColumn(true), entschema.WithDropIndex(true))
}

// ---- failure reasons --------------------------------------------------------

// MaxFailureReason is the width of every `failure_reason` column.
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

// Pool bounds.
//
// database/sql defaults MaxOpenConns to *unlimited*, which is not a default any service
// pointed at a shared PostgreSQL should keep. A burst of requests then opens connections
// until the server refuses them — and the server refuses them for everything, so one
// service under load takes down every other service on that instance. PostgreSQL's own
// max_connections is typically 100, and the API, the worker, the writers and migrations
// share it here.
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
