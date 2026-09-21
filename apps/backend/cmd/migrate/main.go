// Command migrate creates the databases PlayerSense stores things in, and checks the
// invariants project.md states but that nothing else enforces.
//
//	migrate up        PostgreSQL, then ClickHouse
//	migrate postgres  the one PostgreSQL database (POSTGRES_DSN)
//	migrate up ch     ClickHouse only
//	migrate status    what ClickHouse has applied
//	migrate down      roll back the most recent ClickHouse migration
//	migrate reset     drop every ClickHouse object, including its Keeper metadata
//	migrate verify    §96 classification, across both
//
// Two databases. **PostgreSQL** holds accounts, projects, keys, billing and the records
// that name a player — purchases, deletion requests, exports, archive manifests.
// **ClickHouse** holds the events themselves.
//
// It was three, because the PostgreSQL half was split by whether a table could hold a
// player identifier and the halves were meant to sit in different jurisdictions. §96
// outlived that split and is the check that was doing the work: every column declares what
// it holds, and `verify` reads those declarations back off the live database rather than
// out of the schema files, so a column added by hand is exactly the one it catches.
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
	"playersense.dev/backend/internal/dataclass"
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
		envFile = flag.String("env", "", "path to a .env file (default: <repo root>/.env)")
		dir     = flag.String("dir", "", "ClickHouse migrations directory (default: <repo root>/db/clickhouse/migrations)")
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := goose.SetDialect(string(goose.DialectClickHouse)); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	target, action := parseCommand(flag.Args())
	switch target {
	case "up":
		if action == "ch" {
			return clickHouseUp(ctx, settings, *dir)
		}
		// Both, PostgreSQL first: it is the one a running service refuses to boot
		// without, so a half-finished `up` leaves the more useful half done.
		if err := postgresUp(ctx, settings); err != nil {
			return err
		}
		return clickHouseUp(ctx, settings, *dir)

	case "postgres":
		if action != "" && action != "up" {
			return fmt.Errorf("postgres supports only `up`, not %q", action)
		}
		return postgresUp(ctx, settings)

	case "down":
		return withClickHouse(settings, func(db *sql.DB) error {
			return goose.DownContext(ctx, db, *dir)
		})

	case "status":
		return withClickHouse(settings, func(db *sql.DB) error {
			return goose.StatusContext(ctx, db, *dir)
		})

	case "reset":
		return withClickHouse(settings, func(db *sql.DB) error {
			return resetClickHouse(ctx, db, settings)
		})

	case "verify":
		return verify(ctx, settings)

	default:
		return fmt.Errorf("unknown command %q; want up, postgres, status, down, reset or verify", target)
	}
}

// parseCommand splits `migrate <target> <action>`.
//
// `up` is the only one that takes an action, and it takes one word: `ch`, to migrate
// ClickHouse without touching PostgreSQL. Everything else is a bare verb, because with two
// databases and one of them addressed by three commands, naming a target in front of
// `status` would be ceremony.
func parseCommand(args []string) (target, action string) {
	if len(args) == 0 {
		return "status", ""
	}
	if len(args) > 1 {
		return args[0], args[1]
	}
	return args[0], ""
}

// ---- PostgreSQL -------------------------------------------------------------

func postgresUp(ctx context.Context, settings config.Settings) error {
	db := pgdb.New(settings.PostgresDSN)
	if err := db.Start(ctx); err != nil {
		return err
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}
	fmt.Println("ok   postgres (ent): accounts, projects, keys, purchases, requests, manifests")
	return nil
}

// clickHouseUp applies the event-store migrations.
//
// Opened per command rather than once at start-up, so `migrate postgres` does not need a
// ClickHouse to be running — the two are separately operable, and a command that demanded
// both would make that untrue in practice.
func clickHouseUp(ctx context.Context, settings config.Settings, dir string) error {
	return withClickHouse(settings, func(db *sql.DB) error {
		if err := goose.UpContext(ctx, db, dir); err != nil {
			return err
		}
		fmt.Println("ok   clickhouse (goose)")
		return nil
	})
}

// ---- ClickHouse -------------------------------------------------------------

// withClickHouse opens the cell's analytics database for one operation.
//
// Opened per command rather than once at start-up, so `migrate global up` does not need a
// ClickHouse to be running — the two tiers are separately operable, and a command that
// demanded all three would make that untrue in practice.
func withClickHouse(settings config.Settings, fn func(*sql.DB) error) error {
	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		return fmt.Errorf("parse CLICKHOUSE_DSN: %w", err)
	}
	db := clickhouse.OpenDB(opts)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("connect to ClickHouse: %w", err)
	}
	return fn(db)
}

// resetClickHouse drops every object and the replica metadata behind it.
//
// A plain DROP on a Replicated table leaves its znode in Keeper, and recreating the table
// at the same path then replays the *old* schema — which surfaces much later as
// "Missing columns" naming a column the new DDL does not have. Anything that drops a
// Replicated table has to clean up after itself.
//
// The path is built from the server's own macros rather than from a constant, because the
// `{shard}` macro is now the cell slug: two cells sharing a Keeper would otherwise collide
// on replica paths, and a reset built on a guess would purge the wrong cell's metadata.
func resetClickHouse(ctx context.Context, db *sql.DB, settings config.Settings) error {
	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		return err
	}
	database := opts.Auth.Database

	var shard, replica string
	if err := db.QueryRowContext(ctx,
		`SELECT getMacro('shard'), getMacro('replica')`).Scan(&shard, &replica); err != nil {
		return fmt.Errorf("read the shard and replica macros: %w", err)
	}
	fmt.Fprintf(os.Stderr, "note: purging Keeper metadata under shard %q, replica %q\n",
		shard, replica)

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
				"SYSTEM DROP REPLICA '%s' FROM ZKPATH '/clickhouse/tables/%s/%s/%s'",
				replica, shard, database, o.name))
		}
		fmt.Printf("dropped %s %s\n", strings.ToLower(kind), o.name)
	}
	return nil
}

// ---- verification -----------------------------------------------------------

// verify enforces the invariant project.md states but nothing else checks.
//
//	§96  every column carries one of the five data classifications
//
// Read out of the live catalog rather than out of the schema files, because the question is
// what the database actually holds.
//
// §99's residency rule used to be enforced here too, as "no player identifier appears in
// the global tier". There is one database now, so there is no tier for an identifier to be
// in the wrong half of; what survives is the classification itself, which is what made the
// residency check possible in the first place.
func verify(ctx context.Context, settings config.Settings) error {
	var problems []string

	chProblems, chColumns, chTables, err := verifyClickHouse(ctx, settings)
	if err != nil {
		return err
	}
	problems = append(problems, chProblems...)

	db := pgdb.New(settings.PostgresDSN)
	if err := db.Start(ctx); err != nil {
		return err
	}
	defer db.Close()

	pgProblems, pgColumns, pgTables, err := verifyPostgres(ctx, db.DB(), "postgres")
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

	fmt.Printf("ok  postgres:   %d columns / %d tables, all classified (§96)\n",
		pgColumns, pgTables)
	fmt.Printf("ok  clickhouse: %d columns / %d tables, all classified (§96)\n",
		chColumns, chTables)
	return nil
}

// verifyPostgres reads the classification back out of pg_description.
//
// Out of the catalog rather than out of the schema files, because the question is what the
// database actually holds. A column added by hand, or left behind by a migration that was
// rolled forward, is exactly the column a schema-file check would miss.
func verifyPostgres(ctx context.Context, db *sql.DB, label string) (problems []string, columns, tables int, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.table_schema, c.table_name, c.column_name,
		       coalesce(d.description, '') AS comment
		FROM information_schema.columns AS c
		JOIN pg_class     AS cl ON cl.relname = c.table_name
		JOIN pg_namespace AS ns ON ns.oid = cl.relnamespace AND ns.nspname = c.table_schema
		LEFT JOIN pg_description AS d
		       ON d.objoid = cl.oid AND d.objsubid = c.ordinal_position
		WHERE c.table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY c.table_schema, c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var schemaName, table, column, comment string
		if err := rows.Scan(&schemaName, &table, &column, &comment); err != nil {
			return nil, 0, 0, err
		}
		columns++
		seen[schemaName+"."+table] = struct{}{}

		class, ok := strings.CutPrefix(comment, "class=")
		if !ok {
			problems = append(problems,
				fmt.Sprintf("§96 unclassified column: %s %s.%s.%s", label, schemaName, table, column))
			continue
		}
		if !dataclass.Valid(class) {
			problems = append(problems,
				fmt.Sprintf("§96 unknown classification %q: %s %s.%s.%s", class, label, schemaName, table, column))
			continue
		}
	}
	return problems, columns, len(seen), rows.Err()
}

// verifyClickHouse checks §96 against the analytics database.
//
// Views are excluded because a view column has no comment of its own — it inherits
// whatever the expression was — so requiring one would report a violation nothing can fix.
func verifyClickHouse(ctx context.Context, settings config.Settings) (problems []string, columns, tables uint64, err error) {
	err = withClickHouse(settings, func(db *sql.DB) error {
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
			problems = append(problems, fmt.Sprintf("§96 unclassified column: clickhouse %s.%s", table, name))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

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
			problems = append(problems, fmt.Sprintf("§96 unknown classification %q: clickhouse", class))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		return db.QueryRowContext(ctx, `
			SELECT count(), uniqExact(c.table)
			FROM system.columns AS c
			INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table
			WHERE c.database = currentDatabase() AND t.engine NOT LIKE '%View%'
			  AND c.table != 'goose_db_version'`).Scan(&columns, &tables)
	})
	return problems, columns, tables, err
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
