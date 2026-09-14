// Command migrate applies the ClickHouse schema and verifies the invariants that
// project.md states but that nothing else enforces.
//
//	migrate up        apply every pending ClickHouse migration
//	migrate down      roll back the most recent one
//	migrate status    show what is applied
//	migrate reset     drop everything, including the replica metadata in Keeper
//	migrate verify    check §96 classification and the §99 residency rule
//
// `up` applies both: goose for the ClickHouse analytics schema, Ent for the PostgreSQL
// control plane. `verify` checks both, because §96 and §99 are claims about the whole
// system rather than about one database.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/pressly/goose/v3"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/pgdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		envFile = flag.String("env", "", "path to a .env file (default: search upward from the working directory)")
		dir     = flag.String("dir", "", "migrations directory (default: <repo root>/db/clickhouse/migrations)")
	)
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		return err
	}
	if *envFile == "" {
		*envFile = filepath.Join(root, ".env")
	}
	if *dir == "" {
		*dir = filepath.Join(root, "db", "clickhouse", "migrations")
	}

	settings, err := config.Load(*envFile)
	if err != nil {
		return err
	}

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "status"
	}

	db, err := openClickHouse(settings.ClickHouseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := goose.SetDialect(string(goose.DialectClickHouse)); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	switch cmd {
	case "up":
		if err := goose.UpContext(ctx, db, *dir); err != nil {
			return err
		}
		return migratePostgres(ctx, settings)
	case "down":
		return goose.DownContext(ctx, db, *dir)
	case "status":
		return goose.StatusContext(ctx, db, *dir)
	case "reset":
		return reset(ctx, db, settings)
	case "verify":
		return verify(ctx, db, settings)
	default:
		return fmt.Errorf("unknown command %q; want up, down, status, reset or verify", cmd)
	}
}

// openClickHouse dials the analytics database through database/sql, which is what goose
// takes.
func openClickHouse(dsn string) (*sql.DB, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse CLICKHOUSE_DSN: %w", err)
	}
	db := clickhouse.OpenDB(opts)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connect to ClickHouse: %w", err)
	}
	return db, nil
}

// reset drops every object and the replica metadata behind it.
//
// A plain DROP on a Replicated table leaves its znode in Keeper, and recreating the table
// at the same path then replays the *old* schema — which surfaces much later as
// "Missing columns" naming a column the new DDL does not have. Anything that drops a
// Replicated table has to clean up after itself.
func reset(ctx context.Context, db *sql.DB, settings config.Settings) error {
	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		return err
	}
	database := opts.Auth.Database

	rows, err := db.QueryContext(ctx,
		`SELECT name, engine FROM system.tables WHERE database = ? ORDER BY engine LIKE '%View%' DESC`, database)
	if err != nil {
		return err
	}
	type object struct{ name, engine string }
	var objects []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.name, &o.engine); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, o := range objects {
		kind := "TABLE"
		if strings.Contains(o.engine, "View") {
			kind = "VIEW"
		}
		stmt := fmt.Sprintf("DROP %s IF EXISTS `%s`", kind, o.name)
		if kind == "TABLE" {
			stmt += " SYNC"
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop %s %s: %w", kind, o.name, err)
		}
		if strings.HasPrefix(o.engine, "Replicated") {
			// Best effort: the path is gone already if the SYNC drop cleaned it.
			_, _ = db.ExecContext(ctx, fmt.Sprintf(
				"SYSTEM DROP REPLICA 'stage1-01' FROM ZKPATH '/clickhouse/tables/01/%s/%s'", database, o.name))
		}
		fmt.Printf("dropped %s %s\n", strings.ToLower(kind), o.name)
	}
	return nil
}

// migratePostgres brings the control plane up to date. Ent owns this schema, so there
// are no .sql files for it — the Go schema definitions are the source.
func migratePostgres(ctx context.Context, settings config.Settings) error {
	control, err := pgdb.Open(settings.PostgresDSN)
	if err != nil {
		return err
	}
	defer control.Close()

	if err := control.Migrate(ctx); err != nil {
		return err
	}
	fmt.Printf("OK   control-plane schema (ent): %s\n", strings.Join(pgdb.Tiers, ", "))
	return nil
}

// verifyPostgres enforces §96 and §99 against the control plane.
//
// §99's rule is one line and this is the whole of its enforcement: a player identifier
// never appears in the global tier. It is a single query, which is the point — an
// invariant that costs a query to check has no excuse for going unchecked.
func verifyPostgres(ctx context.Context, settings config.Settings) ([]string, error) {
	control, err := pgdb.Open(settings.PostgresDSN)
	if err != nil {
		return nil, err
	}
	defer control.Close()

	var problems []string

	rows, err := control.DB().QueryContext(ctx, `
		SELECT c.table_schema, c.table_name, c.column_name,
		       coalesce(d.description, '') AS comment
		FROM information_schema.columns AS c
		JOIN pg_class     AS cl ON cl.relname = c.table_name
		JOIN pg_namespace AS ns ON ns.oid = cl.relnamespace AND ns.nspname = c.table_schema
		LEFT JOIN pg_description AS d
		       ON d.objoid = cl.oid AND d.objsubid = c.ordinal_position
		WHERE c.table_schema = ANY($1)
		ORDER BY c.table_schema, c.table_name, c.ordinal_position`, pgdb.Tiers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var schemaName, table, column, comment string
		if err := rows.Scan(&schemaName, &table, &column, &comment); err != nil {
			return nil, err
		}
		class, ok := strings.CutPrefix(comment, "class=")
		if !ok {
			problems = append(problems,
				fmt.Sprintf("§96 unclassified column: %s.%s.%s", schemaName, table, column))
			continue
		}
		switch class {
		case "identifier", "behavioural", "derived", "financial", "none":
		default:
			problems = append(problems,
				fmt.Sprintf("§96 unknown classification %q on %s.%s.%s", class, schemaName, table, column))
			continue
		}
		if schemaName == "global" && class == "identifier" {
			problems = append(problems, fmt.Sprintf(
				"§99 player identifier in the global tier: %s.%s.%s", schemaName, table, column))
		}
	}
	return problems, rows.Err()
}

// verify enforces the invariants project.md states but nothing else checks.
func verify(ctx context.Context, db *sql.DB, settings config.Settings) error {
	var problems []string

	// §96: a column cannot exist without a classification. Views are excluded because a
	// view column has no comment of its own — it inherits whatever the expression was.
	rows, err := db.QueryContext(ctx, `
		SELECT c.table, c.name
		FROM system.columns AS c
		INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table
		WHERE c.database = currentDatabase()
		  AND t.engine NOT LIKE '%View%'
		  AND c.table != 'goose_db_version'
		  AND c.comment NOT LIKE 'class=%'
		ORDER BY c.table, c.name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			rows.Close()
			return err
		}
		problems = append(problems, fmt.Sprintf("§96 unclassified column: %s.%s", table, name))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// §96: and the classification has to be one of the five the document defines.
	rows, err = db.QueryContext(ctx, `
		SELECT DISTINCT extract(c.comment, 'class=(\w+)') AS class
		FROM system.columns AS c
		INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table
		WHERE c.database = currentDatabase()
		  AND t.engine NOT LIKE '%View%'
		  AND c.table != 'goose_db_version'
		  AND class NOT IN ('identifier', 'behavioural', 'derived', 'financial', 'none')
		  AND class != ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var class string
		if err := rows.Scan(&class); err != nil {
			rows.Close()
			return err
		}
		problems = append(problems, fmt.Sprintf("§96 unknown classification %q", class))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	pgProblems, err := verifyPostgres(ctx, settings)
	if err != nil {
		return err
	}
	problems = append(problems, pgProblems...)

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, " ", p)
		}
		return fmt.Errorf("%d invariant violation(s)", len(problems))
	}

	var columns, tables uint64
	if err := db.QueryRowContext(ctx, `
		SELECT count(), uniqExact(c.table)
		FROM system.columns AS c
		INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table
		WHERE c.database = currentDatabase() AND t.engine NOT LIKE '%View%'
		  AND c.table != 'goose_db_version'`).
		Scan(&columns, &tables); err != nil {
		return err
	}
	var pgColumns, pgTables int
	if control, err := pgdb.Open(settings.PostgresDSN); err == nil {
		defer control.Close()
		_ = control.DB().QueryRowContext(ctx, `
			SELECT count(*), count(DISTINCT table_name)
			FROM information_schema.columns WHERE table_schema = ANY($1)`, pgdb.Tiers).
			Scan(&pgColumns, &pgTables)
	}
	fmt.Printf("ok  clickhouse: %d columns / %d tables, all classified (§96)\n", columns, tables)
	fmt.Printf("ok  postgres:   %d columns / %d tables, all classified (§96); no identifier in the global tier (§99)\n",
		pgColumns, pgTables)
	return nil
}

// repoRoot walks upward looking for go.work, so the command works from anywhere in the
// tree.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find the repository root (no go.work in any parent directory)")
		}
		dir = parent
	}
}
