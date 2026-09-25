package query

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

// Scope is the tenant, taken from the credential and never from the request body.
//
// Appendix C #3 and §69: the compiler injects these predicates, always. There is no code
// path that compiles a statement without them, because Compile cannot be called without a
// Scope and every builder it creates is given one before anything else.
type Scope struct {
	// OrgSeq leads events_raw's sort key, so a raw read without it scans every tenant's
	// granules for the day. The summaries are keyed on the project alone.
	OrgSeq         uint32
	ProjectSeq     uint32
	EnvironmentSeq uint16

	// PolicyTier decides which cohorts exist at all for this project (§29.15).
	PolicyTier string

	// Verified is the stores the project verifies purchases with, which the money facts count
	// from their verified purchases (ADR 0048). Read by the report builder's purchases source;
	// the Revenue and Purchases pages take it through RevenueFilter.
	Verified []VerifiedStore
}

// Refusal is a question the cohort facts cannot answer.
//
// §32.1 asks for exactly this rather than a best effort: "a question that can only be
// answered by such a join is refused with an explanation". The explanation is the payload —
// a refusal a studio cannot act on is a bug report.
type Refusal struct {
	Field  string
	Reason string
}

func (r *Refusal) Error() string { return r.Reason }

func refuse(field, format string, args ...any) *Refusal {
	return &Refusal{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// Plan is a resolved query: one fact, one numerator statement, one denominator statement.
type Plan struct {
	Fact      Fact
	Metric    string
	Dimension string
	Rolling   bool
	Bucket    string

	// Offsets is what the caller asked for. It is not always what the numerator statement
	// reads — see the note on cumulative metrics in compileNumerator.
	Offsets []int

	Numerator   Statement
	Denominator Statement

	// EventOffsets is set for metric=event, whose numerator answers every requested offset in
	// one row per cohort rather than one row per cell; see compileEventNumerator.
	EventOffsets []int
}

// DefaultOffsets is §29.12's dense set: every day to 30, then 60, 90, 180 and 365.
//
// Dense at the start because a cumulative curve cannot be reconstructed from sparse
// samples, and a lifetime-value chart built on gaps renders as a jagged line that means
// nothing. Sparse afterwards because nobody reads D183 off a chart.
func DefaultOffsets() []int {
	offsets := make([]int, 0, 35)
	for d := 0; d <= 30; d++ {
		offsets = append(offsets, d)
	}
	return append(offsets, 60, 90, 180, 365)
}

// Limits bounds what one statement may read (§32).
//
// Every one of these is a ceiling rather than a target. §32's list exists because an
// analytics API without them has exactly one failure mode — a customer discovers the
// expensive query and everybody else's dashboard stops loading — and because a query that
// would exceed a ceiling is better refused than served slowly (§80).
type Limits struct {
	// MaxRows caps the result. Hit, it is an error rather than a truncation: a silently
	// truncated cohort curve is a curve that appears to end.
	MaxRows int

	// MaxExecutionTime bounds wall-clock time in ClickHouse.
	MaxExecutionTime time.Duration

	// MaxRowsToRead and MaxBytesToRead bound the *scan*, not the result. They are what
	// actually protects the node: a query returning ten rows can still read the table.
	MaxRowsToRead  uint64
	MaxBytesToRead uint64

	// MaxMemoryUsage bounds what the statement may hold. Zero leaves the server's own limit.
	// ClickHouse spills a GROUP BY and a sort to disk from half of it
	// (max_bytes_ratio_before_external_group_by), so reaching it is slow rather than fatal.
	MaxMemoryUsage uint64
}

// DefaultLimits are the ceilings a cohort read runs under.
//
// Generous against the cohort facts and ungenerous against anything else, which is the
// point: the facts are small by construction (§29.12 puts cohort_metrics_daily at about 34k
// rows per project per environment over three years), so a query that reads hundreds of
// millions of rows is a query that has escaped them.
func DefaultLimits() Limits {
	return Limits{
		MaxRows:          50_000,
		MaxExecutionTime: 10 * time.Second,
		MaxRowsToRead:    200_000_000,
		MaxBytesToRead:   4 << 30,
		MaxMemoryUsage:   2 << 30,
	}
}

// DefaultRawLimits are the ceilings for the reads that go to events_raw (§26: a player timeline,
// an ad hoc question). Every one of them is also bounded by the tenant key prefix and a day
// range in the statement itself; these are what hold if that was not enough.
func DefaultRawLimits() Limits {
	return Limits{
		MaxRows:          50_000,
		MaxExecutionTime: 20 * time.Second,
		MaxRowsToRead:    100_000_000,
		MaxBytesToRead:   8 << 30,
		MaxMemoryUsage:   4 << 30,
	}
}

// seconds renders the execution ceiling the way ClickHouse's setting expects it.
func (l Limits) seconds() int {
	if l.MaxExecutionTime <= 0 {
		return 10
	}
	return int(l.MaxExecutionTime.Seconds())
}

// Compile resolves a validated query into statements.
//
// The shape has already passed the contract, so everything refused here is a question the
// *facts* cannot answer rather than a malformed request — which is why these are Refusals
// carrying an explanation rather than validation errors.
func Compile(scope Scope, q protocol.CohortQuery, limits Limits) (*Plan, error) {
	dimension, err := resolveDimension(q)
	if err != nil {
		return nil, err
	}
	if err := checkPolicyTier(scope, q); err != nil {
		return nil, err
	}
	if q.Basis == schemas.BasisPlayer {
		// Accepted by the contract and, until this refusal, answered per install anyway: a
		// studio asking for account-keyed cohorts was shown device-keyed ones under the name
		// it asked for. The facts are keyed on the device (§29.2), and there is no
		// account-keyed cohort fact to read instead.
		return nil, refuse("basis",
			"cohorts are kept per install (§29.2): a reinstall or a second device is a new "+
				"member of a cohort. There is no account-keyed cohort fact, so basis=player "+
				"cannot be answered; ask with basis=install or leave it out")
	}
	if q.Metric == schemas.MetricEvent {
		if q.Event == "" {
			return nil, refuse("event", "an event query needs the event it counts")
		}
		if dimension != "" {
			return nil, refuse("breakdown",
				"a cohort's events are kept for the cohort as a whole, not per %s: carrying every "+
					"dimension for every event would multiply the fact by both. Split the cohort "+
					"by %s with metric=retention, or count the event without the split", dimension, dimension)
		}
	}

	plan := &Plan{
		Metric:    q.Metric,
		Dimension: dimension,
		Rolling:   q.Metric == schemas.MetricRetention && q.RetentionBasis == schemas.RetentionRolling,
		Bucket:    q.Bucket,
		Offsets:   q.Offsets,
	}
	if len(plan.Offsets) == 0 {
		plan.Offsets = DefaultOffsets()
	}
	slices.Sort(plan.Offsets)

	switch {
	case plan.Rolling:
		plan.Fact = factLifetime
	case q.Metric == schemas.MetricBucket:
		plan.Fact = factBuckets
	case q.Metric == schemas.MetricEvent:
		plan.Fact = factCohortEvents
		plan.EventOffsets = plan.Offsets
	case dimension != "":
		plan.Fact = factBreakdown
	default:
		plan.Fact = factMetrics
	}

	plan.Numerator = compileNumerator(scope, q, plan, limits)
	plan.Denominator = compileDenominator(scope, q, plan, limits)
	return plan, nil
}

// resolveDimension decides which single dimension, if any, this query is about.
//
// A filter is not a cheaper breakdown: cohort_metrics_daily carries no dimension columns at
// all, so filtering by platform means reading the breakdown fact and restricting dim_value.
// That fact carries **one dimension at a time** (§29.13), which makes two conclusions
// unavoidable and worth stating rather than discovering:
//
//   - every filter in a query must name the same dimension;
//   - if there is a breakdown, the filters must be on that same dimension.
//
// "iOS players, broken down by country" is therefore not answerable from these facts. It is
// a cartesian cell — platform × country — and materializing those is precisely what §29.13
// refuses to do, because the product of the dimensions runs to hundreds of millions of
// cells per cohort-offset. Refused with the reason, per §32.1.
func resolveDimension(q protocol.CohortQuery) (string, error) {
	dimension := ""
	if q.Breakdown != nil {
		dimension = q.Breakdown.Dimension
	}
	for _, f := range q.Filters {
		if dimension == "" {
			dimension = f.Field
			continue
		}
		if f.Field != dimension {
			return "", refuse("filters",
				"a cohort fact carries one dimension at a time (§29.13), so %q and %q cannot be "+
					"combined in one query; ask for one of them and filter the other in a second query",
				dimension, f.Field)
		}
	}

	// The list is closed, and this is the second place that is true — the request schema
	// says OneOf(CohortDimensions) and refuses it at the edge. Checked again because a
	// planner that silently mapped an unrecognised name to some column would answer the
	// question with the wrong axis, which is the one failure a chart cannot show.
	if dimension != "" && !slices.Contains(schemas.CohortDimensions, dimension) {
		return "", refuse("breakdown.dimension",
			"%q is not a cohort dimension; a cohort is broken down by a property of the "+
				"player's arrival, which is one of %s",
			dimension, strings.Join(schemas.CohortDimensions, ", "))
	}
	return dimension, nil
}

// checkPolicyTier refuses the cohorts a project's collection tier cannot have (§29.15).
//
// Refused with the tier named rather than served as an empty chart. §29.15 is explicit
// about why: "A studio in Apple's Kids Category must not discover it by opening an empty
// retention page." An empty chart says the players did not come back; this says the
// question cannot be asked.
func checkPolicyTier(scope Scope, q protocol.CohortQuery) error {
	switch scope.PolicyTier {
	case "kids_strict":
		// The gateway replaces the installation identifier on every batch (§98), so two
		// days of the same device are unlinkable by construction. Nothing downstream can
		// repair that, and nothing should: it is the guarantee the tier exists to make.
		if wantsCrossDay(q) {
			return refuse("offsets",
				"this project is on the kids_strict collection tier, which stores no persistent "+
					"identifier, so a device cannot be recognised across days and cross-day "+
					"retention is not computable; same-day aggregates are available")
		}
	}
	return nil
}

// wantsCrossDay reports whether the query needs a device recognised on more than one day.
func wantsCrossDay(q protocol.CohortQuery) bool {
	if q.RetentionBasis == schemas.RetentionRolling {
		return true
	}
	if len(q.Offsets) == 0 {
		return true // the default set runs to 365
	}
	return slices.ContainsFunc(q.Offsets, func(o int) bool { return o > 0 })
}

// compileNumerator builds the measure statement.
func compileNumerator(scope Scope, q protocol.CohortQuery, plan *Plan, limits Limits) Statement {
	if plan.Rolling {
		return compileRolling(scope, q, plan, limits)
	}
	if plan.Fact == factCohortEvents {
		return compileEventNumerator(scope, q, plan, limits)
	}

	b := from(plan.Fact)
	b.column(cohortExpr(q.Cohort.Grain), "cohort_date").
		column("day_offset", "day_offset")

	switch plan.Fact {
	case factBuckets:
		b.column("toString(bucket_code)", "dim_value").
			column("sum(players)", "players").
			column("toUInt64(0)", "payers").
			column("toUInt64(0)", "sessions")
	case factBreakdown:
		b.column("dim_value", "dim_value").
			column("sum(returned_players)", "players").
			column("sum(payers)", "payers").
			column("sum(sessions)", "sessions")
	default:
		b.column("''", "dim_value").
			column("sum(returned_players)", "players").
			column("sum(payers)", "payers").
			column("sum(sessions)", "sessions")
	}
	b.column("sum(playtime_ms)", "playtime_ms").
		column("sum(revenue_micros)", "revenue_micros").
		column("sum(purchases)", "purchases")

	scopeAnd(b, scope)
	cohortRange(b, q)

	switch plan.Fact {
	case factBuckets:
		b.eq("bucket_kind", q.Bucket)
		b.eq("bucket_scheme_version", bucketSchemeVersion)
	case factBreakdown:
		b.eq("dim_name", plan.Dimension)
		applyFilters(b, "dim_value", q.Filters)
	}

	// A cumulative metric reads every offset up to the highest one asked for, not only the
	// offsets asked for. LTV at D30 is the revenue of days 0 to 30; computing it from a
	// sparse sample would silently understate it by whatever fell in the gaps (§29.12).
	// The requested offsets are applied after the running sum, in Go.
	if isCumulative(q.Metric) {
		b.between("day_offset", 0, plan.Offsets[len(plan.Offsets)-1])
	} else {
		b.in("day_offset", ints(plan.Offsets))
	}

	b.groupOn("cohort_date", "day_offset", "dim_value").withLimit(limits.MaxRows)
	return b.statement()
}

// compileRolling builds the last-activity histogram rolling retention is a suffix sum over.
//
// It cannot come from the per-offset counters: a device active on days 3 and 5 appears
// under both offsets there, and summing would count it twice. What rolling retention needs
// is each device's *final* activity day, which is why player_lifetime carries max(last_seen)
// and why a one-day read of player_activity_daily cannot produce this at all (§29.10).
//
// Still one table. The suffix sum happens in Go.
func compileRolling(scope Scope, q protocol.CohortQuery, plan *Plan, limits Limits) Statement {
	b := from(factLifetime)
	b.column(cohortExpr(q.Cohort.Grain), "cohort_date").
		column("toUInt16(dateDiff('day', cohort_date, toDate(last_seen)))", "day_offset")
	if plan.Dimension != "" {
		b.column(lifetimeDimension(plan.Dimension), "dim_value")
	} else {
		b.column("''", "dim_value")
	}
	b.column("count()", "players").
		column("toUInt64(0)", "payers").
		column("toUInt64(0)", "sessions").
		column("toUInt64(0)", "playtime_ms").
		column("toInt64(0)", "revenue_micros").
		column("toUInt64(0)", "purchases")

	scopeAnd(b, scope)
	cohortRange(b, q)
	if plan.Dimension != "" {
		applyFilters(b, lifetimeDimension(plan.Dimension), q.Filters)
	}

	b.groupOn("cohort_date", "day_offset", "dim_value").withLimit(limits.MaxRows)
	return b.statement()
}

// compileEventNumerator counts one event across each cohort's days (00018).
//
// One row per cohort carrying three arrays, one entry per requested offset, rather than one row
// per cell: "had done it by day N" is a distinct count over offsets 0..N, which is a merge of
// the per-offset sketches and cannot be summed from per-offset rows afterwards. Each entry is
// its own conditional merge over the same rows, so the whole curve is one scan.
//
// The offsets are written into the statement as integers. They were validated as 0..365 at the
// contract, so there is nothing for them to carry but a number.
func compileEventNumerator(scope Scope, q protocol.CohortQuery, plan *Plan, limits Limits) Statement {
	at := make([]string, len(plan.Offsets))
	events := make([]string, len(plan.Offsets))
	by := make([]string, len(plan.Offsets))
	for i, o := range plan.Offsets {
		n := strconv.Itoa(o)
		at[i] = "uniqCombinedMergeIf(12)(players, day_offset = " + n + ")"
		events[i] = "sumIf(events, day_offset = " + n + ")"
		by[i] = "uniqCombinedMergeIf(12)(players, day_offset <= " + n + ")"
	}

	b := from(factCohortEvents)
	b.column(cohortExpr(q.Cohort.Grain), "cohort_date").
		column("["+strings.Join(at, ", ")+"]", "players_at").
		column("["+strings.Join(events, ", ")+"]", "events_at").
		column("["+strings.Join(by, ", ")+"]", "players_by")

	scopeAnd(b, scope)
	b.eq("event_name", q.Event)
	cohortRange(b, q)
	b.between("day_offset", 0, plan.Offsets[len(plan.Offsets)-1])

	b.groupOn("cohort_date").withLimit(limits.MaxRows)
	return b.statement()
}

// compileDenominator reads the cohort sizes (§29.9).
//
// A second statement rather than a join, per this package's opening note. It also carries
// the provenance the response needs — cohort_maturity_days and computed_at are columns on
// this table, so the freshness a client is told about is the derivation's own and not the
// moment the response was assembled.
func compileDenominator(scope Scope, q protocol.CohortQuery, plan *Plan, limits Limits) Statement {
	b := from(factSizes)
	b.column(cohortExpr(q.Cohort.Grain), "cohort_date")

	// A bucket is measured against the whole cohort: "of the players who arrived that day,
	// how many are in this segment and still active". The segment's own size is not a
	// denominator anybody asks for.
	dimension := plan.Dimension
	if plan.Fact == factBuckets {
		dimension = ""
	}
	if dimension == "" {
		b.column("''", "dim_value")
	} else {
		b.column("dim_value", "dim_value")
	}
	b.column("sum(cohort_size)", "cohort_size").
		column("max(cohort_maturity_days)", "cohort_maturity_days").
		column("max(computed_at)", "computed_at")

	scopeAnd(b, scope)
	cohortRange(b, q)
	if dimension == "" {
		b.eq("dim_name", "__total__")
	} else {
		b.eq("dim_name", dimension)
		applyFilters(b, "dim_value", q.Filters)
	}

	b.groupOn("cohort_date", "dim_value").withLimit(limits.MaxRows)
	return b.statement()
}

// scopeAnd injects the tenant predicates. Every statement in this package goes through it.
func scopeAnd(b *builder, scope Scope) {
	b.eq("project_seq", scope.ProjectSeq).eq("environment_seq", scope.EnvironmentSeq)
}

// applyFilters narrows a dimension column.
func applyFilters(b *builder, column string, filters []protocol.Filter) {
	for _, f := range filters {
		switch f.Op {
		case "eq":
			b.eq(column, f.Value)
		case "ne":
			b.notEq(column, f.Value)
		case "in":
			b.in(column, strs(f.In))
		}
	}
}

// cohortRange bounds a statement to the requested cohort days.
//
// Qualified with the table, because the SELECT list names its bucketed day cohort_date too, and
// ClickHouse resolves an unqualified name in WHERE to the alias before the column: a month asked
// for from the 18th would be filtered on its bucket, the 1st, and match nothing.
func cohortRange(b *builder, q protocol.CohortQuery) {
	b.between(string(b.fact)+".cohort_date", day(q.Cohort.From), day(q.Cohort.To))
}

// cohortExpr buckets the cohort day to the requested grain.
//
// Only the grouping. The range is always on the cohort day itself, so from and to mean the same
// days at every grain and a month asked for from the 18th holds the 18th onwards rather than
// nothing, which is what filtering on the bucket did: the 18th's bucket is the 1st.
func cohortExpr(grain string) string {
	switch grain {
	case schemas.GrainWeek:
		return "toMonday(cohort_date)"
	case schemas.GrainMonth:
		return "toStartOfMonth(cohort_date)"
	default:
		return "cohort_date"
	}
}

// lifetimeDimension maps a sticky dimension onto player_lifetime_v's columns.
//
// The three arrival attributes live in one argMin tuple so they always come from the same
// event (§29.3) — a player whose country and version were read from different events would
// be in a cohort that never existed.
func lifetimeDimension(dimension string) string {
	switch dimension {
	case "install_platform":
		return "first_attrs.1"
	case "install_country":
		return "first_attrs.2"
	default:
		// first_app_version, the last of the three. A default rather than a case and an
		// unreachable panic: the dimension was checked against schemas.CohortDimensions
		// before planning, and a query path that crashes is worse than one that could in
		// principle chart the wrong axis.
		return "first_attrs.3"
	}
}

// isCumulative reports a metric whose value at an offset depends on every earlier offset.
func isCumulative(metric string) bool { return metric == schemas.MetricLTV }

// bucketSchemeVersion is the scheme the reader expects. Pinned rather than "the latest",
// so a derivation running a newer scheme cannot silently change what a chart means
// mid-rollout (§29.11).
const bucketSchemeVersion = 1

func day(d protocol.Day) string { return d.Format("2006-01-02") }

func ints(values []int) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func strs(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = strings.TrimSpace(v)
	}
	return out
}
