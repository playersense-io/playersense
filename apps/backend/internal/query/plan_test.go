package query

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

func scope() Scope {
	return Scope{ProjectSeq: 7, EnvironmentSeq: 3, PolicyTier: "standard"}
}

func limits() Limits { return Limits{MaxRows: 10000} }

func window() protocol.CohortRange {
	return protocol.CohortRange{
		From: protocol.NewDay(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		To:   protocol.NewDay(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)),
	}
}

// everyShape is every query the planner can be asked to resolve: each metric, each
// retention basis, with no breakdown, with each dimension, and with a filter.
func everyShape() []protocol.CohortQuery {
	var out []protocol.CohortQuery
	for _, metric := range schemas.CohortMetrics {
		bases := []string{""}
		buckets := []string{""}
		if metric == schemas.MetricRetention {
			bases = []string{schemas.RetentionClassic, schemas.RetentionRolling}
		}
		if metric == schemas.MetricBucket {
			buckets = []string{schemas.BucketPlaytime, schemas.BucketSpend}
		}
		for _, basis := range bases {
			for _, bucket := range buckets {
				base := protocol.CohortQuery{
					Metric: metric, Cohort: window(),
					RetentionBasis: basis, Bucket: bucket,
				}
				if metric == schemas.MetricEvent {
					base.Event = "level_complete"
				}
				out = append(out, base)
				for _, grain := range []string{schemas.GrainWeek, schemas.GrainMonth} {
					grained := base
					grained.Cohort.Grain = grain
					out = append(out, grained)
				}
				if metric == schemas.MetricBucket {
					continue // a bucket is already a segmentation (§29.11)
				}
				if metric == schemas.MetricEvent {
					continue // a cohort's events are kept for the cohort as a whole
				}
				for _, dim := range schemas.CohortDimensions {
					withBreakdown := base
					withBreakdown.Breakdown = &protocol.Breakdown{Dimension: dim}
					out = append(out, withBreakdown)

					withFilter := withBreakdown
					withFilter.Filters = []protocol.Filter{{Field: dim, Op: "eq", Value: "ios"}}
					out = append(out, withFilter)

					withIn := withBreakdown
					withIn.Filters = []protocol.Filter{{Field: dim, Op: "in", In: []string{"a", "b"}}}
					out = append(out, withIn)
				}
			}
		}
	}
	return out
}

// join matches a join in any form ClickHouse accepts: the explicit keyword, the implicit
// `FROM a, b` comma form, the USING shorthand, and the distributed GLOBAL variants.
//
// Written against the emitted text rather than the builder's fields on purpose. The builder
// having no join method is the structure; this is the check on the one thing structure
// cannot cover, which is what a hand-written column expression can contain.
var join = regexp.MustCompile(`(?i)\bjoin\b|\bfrom\s+\w+\s*,|\busing\s*\(|\bglobal\s`)

// TestACohortQueryNeverJoins is §32.1 and Appendix C #16.
//
// The builder has no join to call, so this is not testing a branch — it is testing that no
// hand-written column expression smuggled one in, which is the one hole the type system
// leaves open. It covers every shape the planner can produce, both statements of each.
func TestACohortQueryNeverJoins(t *testing.T) {
	shapes := everyShape()
	if len(shapes) < 40 {
		t.Fatalf("the matrix collapsed to %d shapes; it is meant to cover every metric", len(shapes))
	}
	for _, q := range shapes {
		plan, err := Compile(scope(), q, limits())
		if err != nil {
			t.Fatalf("compile %s/%s: %v", q.Metric, q.RetentionBasis, err)
		}
		for name, stmt := range map[string]Statement{
			"numerator":   plan.Numerator,
			"denominator": plan.Denominator,
		} {
			if join.MatchString(stmt.SQL) {
				t.Errorf("%s %s reads more than one table:\n%s", q.Metric, name, stmt.SQL)
			}
			if n := strings.Count(strings.ToUpper(stmt.SQL), " FROM "); n != 1 {
				t.Errorf("%s %s has %d FROM clauses:\n%s", q.Metric, name, n, stmt.SQL)
			}
		}
	}
}

// TestTenantScopeIsAlwaysInjected is Appendix C #3.
//
// Not "the handler remembers to pass it" — every statement the planner can emit, for every
// shape, carries both predicates bound to the scope's values and not to anything from the
// request.
func TestTenantScopeIsAlwaysInjected(t *testing.T) {
	for _, q := range everyShape() {
		plan, err := Compile(scope(), q, limits())
		if err != nil {
			t.Fatal(err)
		}
		for name, stmt := range map[string]Statement{
			"numerator":   plan.Numerator,
			"denominator": plan.Denominator,
		} {
			if !strings.Contains(stmt.SQL, "project_seq = ?") ||
				!strings.Contains(stmt.SQL, "environment_seq = ?") {
				t.Fatalf("%s %s is not tenant-scoped:\n%s", q.Metric, name, stmt.SQL)
			}
			if len(stmt.Args) < 2 || stmt.Args[0] != uint32(7) || stmt.Args[1] != uint16(3) {
				t.Fatalf("%s %s did not bind the scope first: %v", q.Metric, name, stmt.Args)
			}
		}
	}
}

// TestEachShapeResolvesToExactlyOneFact is §57.1's resolution table.
func TestEachShapeResolvesToExactlyOneFact(t *testing.T) {
	cases := []struct {
		name string
		q    protocol.CohortQuery
		want Fact
	}{
		{"no breakdown", protocol.CohortQuery{
			Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
			Cohort: window()}, factMetrics},
		{"one breakdown", protocol.CohortQuery{
			Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
			Cohort: window(), Breakdown: &protocol.Breakdown{Dimension: "install_country"}}, factBreakdown},
		{"rolling", protocol.CohortQuery{
			Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionRolling,
			Cohort: window()}, factLifetime},
		{"bucket", protocol.CohortQuery{
			Metric: schemas.MetricBucket, Bucket: schemas.BucketSpend,
			Cohort: window()}, factBuckets},
		{"a filter alone reaches the breakdown fact", protocol.CohortQuery{
			Metric: schemas.MetricRevenue, Cohort: window(),
			Filters: []protocol.Filter{{Field: "install_platform", Op: "eq", Value: "ios"}}}, factBreakdown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := Compile(scope(), c.q, limits())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Fact != c.want {
				t.Fatalf("resolved to %s, want %s", plan.Fact, c.want)
			}
			if !strings.Contains(plan.Numerator.SQL, " FROM "+string(c.want)) {
				t.Fatalf("statement does not read %s:\n%s", c.want, plan.Numerator.SQL)
			}
		})
	}
}

// TestTwoDimensionsAtOnceIsRefused is §29.13 reaching the planner.
//
// "iOS players broken down by country" is a cartesian cell, and materializing those is what
// §29.13 refuses to do. The refusal has to explain itself, so this checks the reason names
// both dimensions rather than merely failing.
func TestTwoDimensionsAtOnceIsRefused(t *testing.T) {
	_, err := Compile(scope(), protocol.CohortQuery{
		Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
		Cohort:    window(),
		Breakdown: &protocol.Breakdown{Dimension: "install_country"},
		Filters:   []protocol.Filter{{Field: "install_platform", Op: "eq", Value: "ios"}},
	}, limits())
	var refusal *Refusal
	if err == nil {
		t.Fatal("two dimensions were compiled rather than refused")
	}
	refusal, ok := err.(*Refusal)
	if !ok {
		t.Fatalf("want a Refusal a handler can turn into an explanation, got %T", err)
	}
	for _, want := range []string{"install_country", "install_platform"} {
		if !strings.Contains(refusal.Reason, want) {
			t.Errorf("the refusal does not name %s: %s", want, refusal.Reason)
		}
	}
}

// TestKidsStrictCannotAskForCrossDayRetention is §29.15.
//
// The gateway replaces the installation identifier on every batch, so the device is
// unlinkable across days by construction. An empty chart would say the players did not come
// back; the refusal says the question cannot be asked.
func TestKidsStrictCannotAskForCrossDayRetention(t *testing.T) {
	kids := Scope{ProjectSeq: 7, EnvironmentSeq: 3, PolicyTier: "kids_strict"}

	_, err := Compile(kids, protocol.CohortQuery{
		Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
		Cohort: window(), Offsets: []int{0, 1, 7},
	}, limits())
	if err == nil || !strings.Contains(err.Error(), "kids_strict") {
		t.Fatalf("want a refusal naming the tier, got %v", err)
	}

	// Same-day aggregates remain available, which is the half of the sentence that makes
	// the refusal actionable rather than a dead end.
	if _, err := Compile(kids, protocol.CohortQuery{
		Metric: schemas.MetricRevenue, Cohort: window(), Offsets: []int{0},
	}, limits()); err != nil {
		t.Fatalf("a same-day aggregate should still be answerable: %v", err)
	}
}

// A cohort is broken down by a property of the player's arrival, and the list of those is
// closed. The name this asks for is the one that used to be on it: PlayerSense ingests no
// MMP feed, so an install's source is not a thing it can know, and the honest answer is a
// refusal rather than a chart split by a column of empty strings.
//
// The request schema refuses it at the edge too. This is the planner's own check, because a
// planner that mapped an unrecognised name to some column would answer with the wrong axis,
// which is the one failure a chart cannot show.
func TestAnUnknownCohortDimensionIsRefused(t *testing.T) {
	scope := Scope{ProjectSeq: 7, EnvironmentSeq: 3, PolicyTier: "standard"}
	_, err := Compile(scope, protocol.CohortQuery{
		Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
		Cohort: window(), Breakdown: &protocol.Breakdown{Dimension: "acquisition_source"},
	}, limits())
	if err == nil {
		t.Fatal("a dimension that is not a cohort dimension compiled")
	}
	if !strings.Contains(err.Error(), "acquisition_source") ||
		!strings.Contains(err.Error(), "install_country") {
		t.Errorf("the refusal names neither what was asked nor what is available: %v", err)
	}
	for _, dim := range schemas.CohortDimensions {
		if _, err := Compile(scope, protocol.CohortQuery{
			Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
			Cohort: window(), Breakdown: &protocol.Breakdown{Dimension: dim},
		}, limits()); err != nil {
			t.Errorf("%s should be answerable: %v", dim, err)
		}
	}
}

// TestACumulativeMetricReadsEveryOffset is §29.12.
//
// LTV at D30 is the revenue of days 0 to 30. Reading only the requested offsets would
// understate it by whatever fell in the gaps, and the understatement would look like a
// flatter curve rather than an error.
func TestACumulativeMetricReadsEveryOffset(t *testing.T) {
	sparse := []int{0, 7, 30}

	ltv, err := Compile(scope(), protocol.CohortQuery{
		Metric: schemas.MetricLTV, Cohort: window(), Offsets: sparse,
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ltv.Numerator.SQL, "day_offset BETWEEN ? AND ?") {
		t.Fatalf("ltv sampled the offsets instead of spanning them:\n%s", ltv.Numerator.SQL)
	}

	// A non-cumulative metric reads only what was asked for, because it can.
	revenue, err := Compile(scope(), protocol.CohortQuery{
		Metric: schemas.MetricRevenue, Cohort: window(), Offsets: sparse,
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(revenue.Numerator.SQL, "day_offset IN (?, ?, ?)") {
		t.Fatalf("revenue did not restrict to the requested offsets:\n%s", revenue.Numerator.SQL)
	}
}

// TestTheDenominatorIsWithinTheBreakdownValue is §29.9.
//
// A broken-down curve divides by the size of *that value's* cohort. Dividing by the whole
// cohort's size understates every value; storing the whole cohort's size against each value
// multiplies the denominator the moment anyone sums across them.
func TestTheDenominatorIsWithinTheBreakdownValue(t *testing.T) {
	plan, err := Compile(scope(), protocol.CohortQuery{
		Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
		Cohort: window(), Breakdown: &protocol.Breakdown{Dimension: "install_country"},
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Denominator.SQL, "dim_name = ?") {
		t.Fatalf("the denominator is not scoped to the dimension:\n%s", plan.Denominator.SQL)
	}
	if !strings.Contains(plan.Denominator.SQL, "dim_value AS dim_value") {
		t.Fatalf("the denominator does not split by value:\n%s", plan.Denominator.SQL)
	}
	for _, arg := range plan.Denominator.Args {
		if arg == "__total__" {
			t.Fatal("a broken-down query divided by the whole cohort's size")
		}
	}
}

// TestAnEventCurveIsOneScanPerCohort is metric=event's shape.
//
// "Had done it by day N" is a distinct count over offsets 0..N. It has to be merged from the
// sketches in ClickHouse; per-offset rows summed afterwards would count a player once per day
// they did it. So the numerator groups by cohort alone and carries one merge per offset.
func TestAnEventCurveIsOneScanPerCohort(t *testing.T) {
	plan, err := Compile(scope(), protocol.CohortQuery{
		Metric: schemas.MetricEvent, Event: "level_complete", Cohort: window(),
		Offsets: []int{0, 1, 7},
	}, limits())
	if err != nil {
		t.Fatal(err)
	}
	sql := plan.Numerator.SQL
	if plan.Fact != factCohortEvents {
		t.Fatalf("an event curve read %s", plan.Fact)
	}
	for _, want := range []string{
		"uniqCombinedMergeIf(12)(players, day_offset <= 7)",
		"sumIf(events, day_offset = 1)",
		"event_name = ?",
		"GROUP BY cohort_date ORDER BY cohort_date",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the event numerator lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(plan.Denominator.SQL, "dim_name = ?") && plan.Denominator.Args[len(plan.Denominator.Args)-1] != "__total__" {
		t.Errorf("an event curve is not measured against the whole cohort: %v", plan.Denominator.Args)
	}
}

// TestQuestionsTheCohortFactsCannotAnswerAreRefused.
func TestQuestionsTheCohortFactsCannotAnswerAreRefused(t *testing.T) {
	cases := map[string]protocol.CohortQuery{
		// Accepted and answered per install until this was refused: the name promised accounts.
		"account-keyed cohorts": {Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
			Basis: schemas.BasisPlayer, Cohort: window()},
		"an event split by platform": {Metric: schemas.MetricEvent, Event: "level_complete", Cohort: window(),
			Breakdown: &protocol.Breakdown{Dimension: "install_platform"}},
		"an event filtered by country": {Metric: schemas.MetricEvent, Event: "level_complete", Cohort: window(),
			Filters: []protocol.Filter{{Field: "install_country", Op: "eq", Value: "TR"}}},
		"an event with no name": {Metric: schemas.MetricEvent, Cohort: window()},
	}
	for name, q := range cases {
		_, err := Compile(scope(), q, limits())
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("%s: got %v, want a refusal", name, err)
		}
	}
}

// TestAMonthCohortStartsOnTheFirst.
func TestAMonthCohortStartsOnTheFirst(t *testing.T) {
	q := protocol.CohortQuery{Metric: schemas.MetricPlaytime, Cohort: window()}
	q.Cohort.Grain = schemas.GrainMonth
	plan, err := Compile(scope(), q, limits())
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []Statement{plan.Numerator, plan.Denominator} {
		if !strings.Contains(stmt.SQL, "toStartOfMonth(cohort_date) AS cohort_date") {
			t.Errorf("a month cohort is not keyed on its first day:\n%s", stmt.SQL)
		}
	}
}
