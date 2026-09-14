// Package query compiles a logical cohort question into ClickHouse SQL (project.md §57).
//
// The one rule that shapes everything here is §32.1:
//
//	The compiler must be structurally unable to emit a join whose key is a player,
//	device or session identifier. This is not a guideline the query planner consults:
//	it is a property of the compiler, because a limit that can be exceeded under load
//	is a limit that will be.
//
// So the builder below has no join. Not a disabled one, not one behind a flag — there is no
// method that adds a second table, and the FROM clause can only be a value of the closed
// Fact type. A planner that wanted a join could not express it.
//
// One hole is left, and naming it is more useful than pretending it is closed: a
// hand-written column expression is a string, and a string can contain the word JOIN.
// TestACohortQueryNeverJoins compiles every shape the planner can produce and reads the
// output, which is what covers that.
//
// The denominator is the place this rule usually gets bent. §29.9 would permit joining
// cohort_sizes — it is bounded, a few hundred thousand rows, not one per player — and
// cohortcheck does exactly that. This package instead reads it as a second single-table
// query and divides in Go. The cost is one round trip; the benefit is that "no cohort query
// joins" needs no exception attached to it, which is the difference Appendix C #16 draws
// when it adds "and no resident dictionary stands in for such a join".
package query

import (
	"fmt"
	"strings"
)

// Fact is a table the planner may read.
//
// A closed set, and the only thing that can appear in a FROM clause. Adding a table here is
// a deliberate act with a code review attached, which is the point: §26's rollup selection
// is a decision about which of these answers a shape, never about assembling one.
type Fact string

const (
	// factMetrics answers an unbroken-down cohort curve.
	factMetrics Fact = "cohort_metrics_daily"
	// factBreakdown answers a curve split by exactly one sticky dimension (§29.13).
	factBreakdown Fact = "cohort_metrics_breakdown_daily"
	// factSizes holds the denominators, read separately and divided in Go (§29.9).
	factSizes Fact = "cohort_sizes"
	// factBuckets answers a behavioural segment (§29.11).
	factBuckets Fact = "cohort_bucket_daily"
	// factLifetime answers rolling retention, which per-offset counters cannot (§29.10).
	factLifetime Fact = "player_lifetime_v"
)

// Statement is compiled SQL and its arguments, never interpolated values.
type Statement struct {
	SQL  string
	Args []any
}

// builder assembles one single-table SELECT.
//
// Deliberately small. It can select expressions, filter, group and order, and that is the
// whole of it — there is no FROM list, no join, no subquery and no UNION, because the
// planner needs none of them and every one of them is a way to reach a second table.
type builder struct {
	fact    Fact
	columns []string
	where   []string
	args    []any
	groupBy []string
	orderBy []string
	limit   int
}

// from starts a statement against one fact. The signature is the enforcement: a Fact is the
// only thing that can be a source, and there is no way to supply two.
func from(f Fact) *builder { return &builder{fact: f} }

// column adds a select expression under an alias.
func (b *builder) column(expr, alias string) *builder {
	b.columns = append(b.columns, expr+" AS "+alias)
	return b
}

// eq adds an equality predicate with a bound argument.
func (b *builder) eq(column string, value any) *builder {
	b.where = append(b.where, column+" = ?")
	b.args = append(b.args, value)
	return b
}

// notEq adds an inequality predicate with a bound argument.
func (b *builder) notEq(column string, value any) *builder {
	b.where = append(b.where, column+" != ?")
	b.args = append(b.args, value)
	return b
}

// between bounds a column inclusively.
func (b *builder) between(column string, low, high any) *builder {
	b.where = append(b.where, column+" BETWEEN ? AND ?")
	b.args = append(b.args, low, high)
	return b
}

// in bounds a column to a set.
//
// Placeholders are written out rather than passing the slice whole, so the shape of the
// statement is visible in the SQL and an empty set is impossible to express by accident —
// a caller with nothing to match should not call this.
func (b *builder) in(column string, values []any) *builder {
	if len(values) == 0 {
		return b
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(values)), ", ")
	b.where = append(b.where, column+" IN ("+marks+")")
	b.args = append(b.args, values...)
	return b
}

// groupOn adds grouping keys, which are also the ordering keys.
//
// The two are the same on purpose: every statement this package emits is a cohort curve
// read in cohort-then-offset order, and an ordering that does not follow the grouping is a
// sort ClickHouse has to do for no reason.
func (b *builder) groupOn(columns ...string) *builder {
	b.groupBy = append(b.groupBy, columns...)
	b.orderBy = append(b.orderBy, columns...)
	return b
}

// withLimit caps the rows returned (§32's "maximum returned rows").
func (b *builder) withLimit(n int) *builder {
	b.limit = n
	return b
}

// statement renders the SQL.
func (b *builder) statement() Statement {
	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(b.columns, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(string(b.fact))
	if len(b.where) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(b.where, " AND "))
	}
	if len(b.groupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(b.groupBy, ", "))
	}
	if len(b.orderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(b.orderBy, ", "))
	}
	if b.limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", b.limit)
	}
	return Statement{SQL: sb.String(), Args: b.args}
}
