// Command migrate creates the databases PlayerSense stores things in, and checks the
// invariants project.md states but that nothing else enforces.
//
//	migrate up            global up, then cell up
//	migrate global up     the one global control-plane tier (GLOBAL_POSTGRES_DSN)
//	migrate cell up       this cell: its registry entry, its own tier, its ClickHouse
//	migrate cell status   what this cell's ClickHouse has applied
//	migrate cell down     roll back the most recent ClickHouse migration
//	migrate cell reset    drop every ClickHouse object, including its Keeper metadata
//	migrate verify        §96 classification and §99 residency, across all three
//
// There are three databases and the split is the whole point (§99). The **global tier**
// is one PostgreSQL database holding accounts, projects and keys — and no player data at
// all. A **cell tier** is one PostgreSQL database per cell, holding everything that names
// a player. A cell's **ClickHouse** holds the events themselves.
//
// The target is named rather than inferred, which is what stops this command from growing
// a second control plane inside a cell's database — a mistake that does not fail, it just
// produces two of something there should be one of, and surfaces weeks later as two
// customers holding the same project id.
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

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/cell"
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
		// Both, in the order the dependency runs: the registry entry a cell writes lives
		// in the global tier, so the global tier has to exist first.
		if err := globalUp(ctx, settings); err != nil {
			return err
		}
		return cellUp(ctx, settings, *dir)

	case "global":
		if action != "up" {
			return fmt.Errorf("the global tier supports only `up`, not %q", action)
		}
		return globalUp(ctx, settings)

	case "cell":
		switch action {
		case "up":
			return cellUp(ctx, settings, *dir)
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
		default:
			return fmt.Errorf("unknown cell command %q; want up, down, status or reset", action)
		}

	case "verify":
		return verify(ctx, settings)

	default:
		return fmt.Errorf("unknown command %q; want up, global, cell or verify", target)
	}
}

// parseCommand splits `migrate <target> <action>`.
//
// The bare forms — `migrate status`, `migrate down`, `migrate reset` — mean the cell,
// because those three only ever addressed ClickHouse and typing `cell` in front of them
// would be ceremony. `up` is the exception and deliberately means both: a bare `up` that
// silently skipped the global tier would be the most confusing default available.
func parseCommand(args []string) (target, action string) {
	if len(args) == 0 {
		return "cell", "status"
	}
	switch args[0] {
	case "global", "cell":
		action = "up"
		if len(args) > 1 {
			action = args[1]
		}
		return args[0], action
	case "down", "status", "reset":
		return "cell", args[0]
	default:
		return args[0], ""
	}
}

// ---- global tier ------------------------------------------------------------

func globalUp(ctx context.Context, settings config.Settings) error {
	g := pgdb.NewGlobal(settings.GlobalPostgresDSN)
	if err := g.Start(ctx); err != nil {
		return err
	}
	defer g.Close()

	if err := g.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate the global tier: %w", err)
	}
	fmt.Println("ok   global tier (ent): accounts, projects, keys, cells")
	return nil
}

// ---- cell -------------------------------------------------------------------

// cellUp provisions everything this cell needs, in dependency order.
//
// Three stores, one command, because a cell is a unit: a regional database with no
// registry entry is invisible to the gateway, and a registry entry with no ClickHouse is
// an address that accepts batches it cannot store.
func cellUp(ctx context.Context, settings config.Settings, dir string) error {
	// 1. Announce the cell. This is the one place a cell command writes to the global
	// tier, and it is deliberate: the registry is what a service checks at start-up to
	// prove the cell it was configured as exists, and what the gateway reads to tell a
	// misdirected SDK where to go instead.
	g := pgdb.NewGlobal(settings.GlobalPostgresDSN)
	if err := g.Start(ctx); err != nil {
		return err
	}
	defer g.Close()
	if err := g.AssertTier(ctx); err != nil {
		return fmt.Errorf("GLOBAL_POSTGRES_DSN: %w", err)
	}
	if err := registerCell(ctx, g, settings); err != nil {
		return err
	}

	// 2. The cell's own PostgreSQL: everything that names a player, and it never leaves
	// the cell's region (§99).
	c := pgdb.NewCell(settings.Cell, settings.CellPostgresDSN)
	if err := c.Start(ctx); err != nil {
		return err
	}
	defer c.Close()
	if err := c.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate the %s tier: %w", settings.Cell, err)
	}
	fmt.Printf("ok   cell %s tier (ent): purchases, deletion requests, exports\n", settings.Cell)

	// 3. The cell's ClickHouse.
	return withClickHouse(settings, func(db *sql.DB) error {
		if err := goose.UpContext(ctx, db, dir); err != nil {
			return err
		}
		fmt.Printf("ok   cell %s analytics (goose)\n", settings.Cell)
		return nil
	})
}

// registerCell makes this cell known to the global registry, idempotently.
//
// The registry holds identity and the public endpoint, never credentials: a table of
// database passwords is a table of database passwords, so DSNs stay in each service's own
// environment and only the address an SDK may be redirected to is recorded here.
//
// The region is immutable and a mismatch is an error rather than an update. Changing it
// would silently reclassify every row the cell already holds as belonging to a different
// legal regime, which is not something a migration should be able to do by accident.
func registerCell(ctx context.Context, g *pgdb.Global, settings config.Settings) error {
	existing, err := g.Client.Cell.Query().Where(cell.Slug(settings.Cell)).Only(ctx)
	switch {
	case err == nil:
		if existing.Region != settings.CellRegion {
			return fmt.Errorf(
				"cell %s is registered in region %q but this process is configured as %q; "+
					"a cell's region is immutable, because changing it would reclassify every row it already holds",
				settings.Cell, existing.Region, settings.CellRegion)
		}
		if existing.IngestURL == settings.CellIngestURL {
			fmt.Printf("ok   cell %s already registered in region %s\n", settings.Cell, settings.CellRegion)
			return nil
		}
		if _, err := existing.Update().SetIngestURL(settings.CellIngestURL).Save(ctx); err != nil {
			return fmt.Errorf("update cell %s: %w", settings.Cell, err)
		}
		fmt.Printf("ok   cell %s ingest URL is now %s\n", settings.Cell, settings.CellIngestURL)
		return nil

	case global.IsNotFound(err):
		if _, err := g.Client.Cell.Create().
			SetSlug(settings.Cell).
			SetRegion(settings.CellRegion).
			SetIngestURL(settings.CellIngestURL).
			Save(ctx); err != nil {
			return fmt.Errorf("register cell %s: %w", settings.Cell, err)
		}
		fmt.Printf("ok   cell %s registered in region %s at %s\n",
			settings.Cell, settings.CellRegion, settings.CellIngestURL)
		return nil

	default:
		return fmt.Errorf("read the cell registry: %w", err)
	}
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
	if shard != settings.Cell {
		// Not fatal — a developer stack may predate the macro change — but worth saying,
		// because it means the Keeper paths are not the ones this cell's name implies.
		fmt.Fprintf(os.Stderr,
			"note: ClickHouse reports shard %q while CELL is %q; purging Keeper under %q\n",
			shard, settings.Cell, shard)
	}

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

// verify enforces the invariants project.md states but nothing else checks.
//
// Two rules, across all three databases:
//
//	§96  every column carries one of the five data classifications
//	§99  no player identifier appears in the global tier
//
// §99 is one line and this is the whole of its enforcement. It matters more now than when
// both tiers shared a server, because the global tier is hosted in the United States and a
// cell's is not: this check is the mechanical proof that no player data crosses.
func verify(ctx context.Context, settings config.Settings) error {
	var problems []string

	chProblems, chColumns, chTables, err := verifyClickHouse(ctx, settings)
	if err != nil {
		return err
	}
	problems = append(problems, chProblems...)

	g := pgdb.NewGlobal(settings.GlobalPostgresDSN)
	if err := g.Start(ctx); err != nil {
		return err
	}
	defer g.Close()
	if err := g.AssertTier(ctx); err != nil {
		problems = append(problems, fmt.Sprintf("GLOBAL_POSTGRES_DSN: %v", err))
	}
	// forbidIdentifier: this is §99 itself.
	gProblems, gColumns, gTables, err := verifyPostgres(ctx, g.DB(), "global", true)
	if err != nil {
		return err
	}
	problems = append(problems, gProblems...)

	c := pgdb.NewCell(settings.Cell, settings.CellPostgresDSN)
	if err := c.Start(ctx); err != nil {
		return err
	}
	defer c.Close()
	if err := c.AssertTier(ctx); err != nil {
		problems = append(problems, fmt.Sprintf("CELL_POSTGRES_DSN: %v", err))
	}
	cProblems, cColumns, cTables, err := verifyPostgres(ctx, c.DB(), "cell "+settings.Cell, false)
	if err != nil {
		return err
	}
	problems = append(problems, cProblems...)

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, " ", p)
		}
		return fmt.Errorf("%d invariant violation(s)", len(problems))
	}

	fmt.Printf("ok  global tier:   %d columns / %d tables, all classified (§96); no identifier anywhere in it (§99)\n",
		gColumns, gTables)
	fmt.Printf("ok  cell %-8s %d columns / %d tables, all classified (§96)\n",
		settings.Cell+":", cColumns, cTables)
	fmt.Printf("ok  clickhouse:    %d columns / %d tables, all classified (§96)\n",
		chColumns, chTables)
	return nil
}

// verifyPostgres reads the classification back out of pg_description.
//
// Out of the catalog rather than out of the schema files, because the question is what the
// database actually holds. A column added by hand, or left behind by a migration that was
// rolled forward, is exactly the column a schema-file check would miss.
func verifyPostgres(ctx context.Context, db *sql.DB, label string, forbidIdentifier bool) (problems []string, columns, tables int, err error) {
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
		if forbidIdentifier && class == "identifier" {
			problems = append(problems, fmt.Sprintf(
				"§99 player identifier in the global tier: %s.%s.%s", schemaName, table, column))
		}
	}
	return problems, columns, len(seen), rows.Err()
}

// verifyClickHouse checks §96 against the cell's analytics database.
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
