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

	// The time-series tiers (§26, §27). Minute and hour are continuous materialized views
	// and carry an approximate player count; day and month are derived nightly and are
	// exact.
	factEvents1m         Fact = "events_1m"
	factEvents1h         Fact = "events_1h"
	factMetricsDaily     Fact = "metrics_daily"
	factMetricsBreakdown Fact = "metrics_breakdown_daily"
	factMetricsMonthly   Fact = "metrics_monthly"

	// The whole-project realtime tiers (00013). The minute and hour questions that name no
	// event read these rather than merging one sketch per event name.
	factActivity1m Fact = "activity_1m"
	factActivity1h Fact = "activity_1h"
	// factLifecycle splits a day's returners by how long they were away (00018).
	factLifecycle Fact = "lifecycle_daily"

	// The explorer's facts: the event catalogue, events by day and one attribute, and what
	// their properties hold (00014).
	factEventNames      Fact = "event_names"
	factEventsDaily     Fact = "events_daily"
	factEventProperties Fact = "event_properties_daily"

	// Sessions as they arrive, and complete sessions by day (00015).
	factSessions      Fact = "sessions"
	factSessionsDaily Fact = "sessions_daily"

	// Purchases, the devices that make them, and revenue by what was bought (00016).
	factPurchases    Fact = "purchases"
	factPayers       Fact = "payers"
	factRevenueDaily Fact = "revenue_daily"

	// Exposures by flag and value (00017), and each cohort's events (00018).
	factExposures    Fact = "feature_exposures_daily"
	factCohortEvents Fact = "cohort_events_daily"

	// factPlayerDays is the device-day fact, read here for one device at a time.
	factPlayerDays Fact = "player_activity_daily"
	// factPlayers and factAccounts are the lifetime facts, read directly rather than through
	// their views so a directory can aggregate in key order and never hold the whole project.
	factPlayers  Fact = "player_lifetime"
	factAccounts Fact = "account_lifetime"

	// factRaw is events_raw. §26 allows it for a player timeline and for ad hoc questions no
	// summary answers, and nothing else: every statement against it is bounded by the tenant
	// key prefix, a day range and a row limit, and runs under rawLimits.
	factRaw Fact = "events_raw"
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
	fact  Fact
	inner *builder
	final bool

	columns  []string
	prewhere []string
	where    []string
	groupBy  []string
	having   []string
	orderBy  []string
	limit    int
	offset   int

	// One argument list per clause, joined in the order the clauses are written, so a predicate
	// added to PREWHERE after one was added to WHERE still binds its own placeholders.
	columnArgs, preArgs, args, havingArgs []any
}

// from starts a statement against one fact. The signature is the enforcement: a Fact is the
// only thing that can be a source, and there is no way to supply two.
func from(f Fact) *builder { return &builder{fact: f} }

// over starts a statement whose source is another statement's result.
//
// Still one table: the inner builder was itself started with from() and can only read one Fact,
// so nesting adds a second aggregation over the same rows, never a second source. It exists for
// the two-level questions a single GROUP BY cannot ask, such as a funnel, which is decided per
// player and then counted.
func over(inner *builder) *builder { return &builder{fact: inner.fact, inner: inner} }

// column adds a select expression under an alias.
func (b *builder) column(expr, alias string) *builder {
	b.columns = append(b.columns, expr+" AS "+alias)
	return b
}

// columnWith adds a select expression that has placeholders of its own.
func (b *builder) columnWith(expr, alias string, args ...any) *builder {
	b.columns = append(b.columns, expr+" AS "+alias)
	b.columnArgs = append(b.columnArgs, args...)
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

// deduplicated reads a ReplacingMergeTree as its merged result (FINAL), so a row inserted twice
// is read once however far its parts have merged.
func (b *builder) deduplicated() *builder {
	b.final = true
	return b
}

// pre adds a PREWHERE predicate with bound arguments.
//
// For the raw reads, where the predicate is far narrower than the row: ClickHouse reads the
// PREWHERE columns first and the rest only for granules that pass, so a device's timeline reads
// the device_id column of a day and the property maps of a handful of granules.
func (b *builder) pre(predicate string, args ...any) *builder {
	b.prewhere = append(b.prewhere, predicate)
	b.preArgs = append(b.preArgs, args...)
	return b
}

// and adds an arbitrary predicate with its bound arguments.
func (b *builder) and(predicate string, args ...any) *builder {
	b.where = append(b.where, predicate)
	b.args = append(b.args, args...)
	return b
}

// between bounds a column inclusively.
func (b *builder) between(column string, low, high any) *builder {
	b.where = append(b.where, column+" BETWEEN ? AND ?")
	b.args = append(b.args, low, high)
	return b
}

// gte and lt bound a column from one side, for windows whose ends are not both inclusive.
func (b *builder) gte(column string, value any) *builder {
	b.where = append(b.where, column+" >= ?")
	b.args = append(b.args, value)
	return b
}

func (b *builder) lt(column string, value any) *builder {
	b.where = append(b.where, column+" < ?")
	b.args = append(b.args, value)
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

// group adds grouping keys without ordering on them, for statements ordered by something else.
func (b *builder) group(columns ...string) *builder {
	b.groupBy = append(b.groupBy, columns...)
	return b
}

// havingThat filters groups, with bound arguments.
func (b *builder) havingThat(predicate string, args ...any) *builder {
	b.having = append(b.having, predicate)
	b.havingArgs = append(b.havingArgs, args...)
	return b
}

// order sets an ordering that is not the grouping.
func (b *builder) order(terms ...string) *builder {
	b.orderBy = append(b.orderBy, terms...)
	return b
}

// withLimit caps the rows returned (§32's "maximum returned rows").
func (b *builder) withLimit(n int) *builder {
	b.limit = n
	return b
}

// page skips the first n rows, for a directory read a page at a time.
func (b *builder) page(n int) *builder {
	b.offset = n
	return b
}

// statement renders the SQL.
func (b *builder) statement() Statement {
	var sb strings.Builder
	args := append([]any(nil), b.columnArgs...)
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(b.columns, ", "))
	sb.WriteString(" FROM ")
	if b.inner != nil {
		// The inner statement's arguments come first, because its placeholders do.
		inner := b.inner.statement()
		sb.WriteString("(" + inner.SQL + ")")
		args = append(args, inner.Args...)
	} else {
		sb.WriteString(string(b.fact))
		if b.final {
			sb.WriteString(" FINAL")
		}
	}
	if len(b.prewhere) > 0 {
		sb.WriteString(" PREWHERE ")
		sb.WriteString(strings.Join(b.prewhere, " AND "))
	}
	if len(b.where) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(b.where, " AND "))
	}
	if len(b.groupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(b.groupBy, ", "))
	}
	if len(b.having) > 0 {
		sb.WriteString(" HAVING ")
		sb.WriteString(strings.Join(b.having, " AND "))
	}
	if len(b.orderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(b.orderBy, ", "))
	}
	if b.limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", b.limit)
	}
	if b.offset > 0 {
		fmt.Fprintf(&sb, " OFFSET %d", b.offset)
	}
	args = append(args, b.preArgs...)
	args = append(args, b.args...)
	args = append(args, b.havingArgs...)
	return Statement{SQL: sb.String(), Args: args}
}
