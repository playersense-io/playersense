package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// backfill fills a rollup that was created after the events it summarises.
//
// A materialized view only sees inserts that happen after it exists, so a rollup added by a
// migration starts empty while events_raw already holds the history. This runs the view's own
// SELECT over that history once, which means the rollup and its backfill cannot disagree about
// what the rollup is: there is no second copy of the definition to drift.
//
// Two rules keep it from double counting, which is the only way a backfill can be wrong:
//
//   - The target, and every rollup chained from it, must be empty. Running it twice, or over a
//     rollup the view has already started filling, would count those rows twice, so it refuses
//     rather than guessing which rows are new. TRUNCATE them first if that is what is wanted.
//   - analytics-writer should be stopped while it runs. An insert landing between the view's
//     creation and this statement's snapshot is in both.
//
// Only views reading events_raw are backfilled directly. A rollup chained from another rollup
// (activity_1h from activity_1m) fills itself as the backfill inserts into its source, exactly as
// it does for live traffic.
func backfill(ctx context.Context, db *sql.DB, targets []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("name the rollup tables to backfill, e.g. `migrate backfill activity_1m`")
	}
	views, err := readViews(ctx, db)
	if err != nil {
		return err
	}

	for _, target := range targets {
		view, ok := views.feeding(target)
		if !ok {
			return fmt.Errorf("%s is not the target of a materialized view", target)
		}
		if view.source != "events_raw" {
			return fmt.Errorf("%s is chained from %s: backfill %s instead and it fills itself",
				target, view.source, view.source)
		}
		chain := append([]string{target}, views.downstream(target)...)
		for _, table := range chain {
			var rows uint64
			if err := db.QueryRowContext(ctx, "SELECT count() FROM "+quote(table)).Scan(&rows); err != nil {
				return fmt.Errorf("count %s: %w", table, err)
			}
			if rows > 0 {
				return fmt.Errorf("%s already holds %d rows; a backfill over it would count them twice "+
					"(TRUNCATE TABLE %s first if they should be rebuilt)", table, rows, strings.Join(chain, ", "))
			}
		}

		columns, err := outputColumns(ctx, db, view.query)
		if err != nil {
			return fmt.Errorf("read the columns %s produces: %w", view.name, err)
		}

		// A backfilled row was not stored by analytics-writer, so it has no writer lag. Measured
		// with now64() it would report every historical row as days late.
		query := strings.ReplaceAll(view.query, "now64(3)", "ingest_time")

		stmt := fmt.Sprintf("INSERT INTO %s (%s) %s", quote(target), strings.Join(columns, ", "), query)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("backfill %s: %w", target, err)
		}
		fmt.Printf("ok   backfilled %s (and %s)\n", target, orNothing(chain[1:]))
	}
	return nil
}

type view struct {
	name, source, target, query string
}

type viewGraph []view

var toClause = regexp.MustCompile("(?i)MATERIALIZED VIEW\\s+\\S+\\s+TO\\s+(?:`?\\w+`?\\.)?`?(\\w+)`?")

// readViews reads which view reads which table and writes which, off the live catalog.
func readViews(ctx context.Context, db *sql.DB) (viewGraph, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT v.name, s.name, v.create_table_query, v.as_select
		FROM system.tables AS v
		INNER JOIN (
		    SELECT name, arrayJoin(dependencies_table) AS dependent
		    FROM system.tables
		    WHERE database = currentDatabase()
		) AS s ON s.dependent = v.name
		WHERE v.database = currentDatabase() AND v.engine = 'MaterializedView'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var graph viewGraph
	for rows.Next() {
		var v view
		var create string
		if err := rows.Scan(&v.name, &v.source, &create, &v.query); err != nil {
			return nil, err
		}
		m := toClause.FindStringSubmatch(create)
		if m == nil {
			continue
		}
		v.target = m[1]
		graph = append(graph, v)
	}
	return graph, rows.Err()
}

func (g viewGraph) feeding(target string) (view, bool) {
	for _, v := range g {
		if v.target == target {
			return v, true
		}
	}
	return view{}, false
}

// downstream lists every table filled, directly or through a chain, by inserts into table.
func (g viewGraph) downstream(table string) []string {
	var out []string
	for _, v := range g {
		if v.source == table && !slices.Contains(out, v.target) {
			out = append(out, v.target)
			out = append(out, g.downstream(v.target)...)
		}
	}
	return out
}

// outputColumns names what a SELECT produces, which is how a view maps onto its target.
//
// DESCRIBE rather than running it with LIMIT 0: the driver cannot decode an aggregate state, and
// a description is only strings.
func outputColumns(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "DESCRIBE TABLE ("+query+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	described, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		fields := make([]any, len(described))
		for i := range fields {
			fields[i] = new(any)
		}
		if err := rows.Scan(fields...); err != nil {
			return nil, err
		}
		names = append(names, quote(fmt.Sprint(*fields[0].(*any))))
	}
	return names, rows.Err()
}

func quote(identifier string) string { return "`" + identifier + "`" }

func orNothing(tables []string) string {
	if len(tables) == 0 {
		return "nothing chained"
	}
	return strings.Join(tables, ", ")
}
