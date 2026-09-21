package schemas

import (
	"fmt"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/protocol"
)

// CohortQueryIn is the cohort query as a bindable input.
//
// The wire shape lives in protocol because §9 makes it a contract clients implement; this
// type only adds the rules, so neither drifts silently. The rules here are the ones that
// can be decided from the request alone — what a *tenant* is allowed to ask, which depends
// on their collection policy tier, is decided in the handler where the resolved key is
// available (§29.15).
type CohortQueryIn struct {
	protocol.CohortQuery

	// Decoys. Scope comes from the key (§76, §69, Appendix C #3), so these are never read
	// — but a client that sends one is trying to scope a query and deserves to be told it
	// did not work. Silently ignoring them is how somebody ends up believing they queried
	// another project and got no rows because it was empty.
	ProjectID     string `json:"project_id,omitzero" doc:"Not accepted; scope comes from the key"`
	EnvironmentID string `json:"environment_id,omitzero" doc:"Not accepted; scope comes from the key"`
	OrgID         string `json:"org_id,omitzero" doc:"Not accepted; scope comes from the key"`
}

// Metrics, dimensions and bases, named once so the validator, the planner and the
// documentation cannot disagree about what exists.
const (
	MetricRetention       = "retention"
	MetricPlaytime        = "playtime"
	MetricRevenue         = "revenue"
	MetricLTV             = "ltv"
	MetricPayerConversion = "payer_conversion"
	MetricBucket          = "bucket"

	RetentionClassic = "classic"
	RetentionRolling = "rolling"

	BasisInstall = "install"
	BasisPlayer  = "player"

	GrainDay  = "day"
	GrainWeek = "week"

	BucketPlaytime = "playtime"
	BucketSpend    = "spend"
)

// CohortMetrics is every metric the planner can resolve.
var CohortMetrics = []string{
	MetricRetention, MetricPlaytime, MetricRevenue,
	MetricLTV, MetricPayerConversion, MetricBucket,
}

// CohortDimensions is the three sticky dimensions (§29.3). A dimension is sticky because it
// is a property of the player's arrival, which is what lets it be stamped at write time and
// never joined at read time.
//
// There is deliberately no acquisition dimension. PlayerSense measures a game, not an ad
// spend: it ingests no MMP feed, so an install's source is not something it can know.
var CohortDimensions = []string{
	"install_platform", "install_country", "first_app_version",
}

// MaxCohortDays bounds the cohort range a single query may span.
//
// 400 days because that is player_activity_daily's TTL: asking for a wider range is asking
// about days whose per-player facts have already expired, and answering it would return a
// curve that thins out for reasons that have nothing to do with the players.
const MaxCohortDays = 400

// MaxOffsets bounds how many day-offsets one query may request.
const MaxOffsets = 64

// MaxFilters bounds the filter list. §32 wants dimensions constrained rather than open, and
// a filter per dimension plus a couple of values is the whole useful range.
const MaxFilters = 8

// Validate bounds the query before anything is planned.
//
// Everything refusable is refused here rather than compiled and refused by ClickHouse.
// §29.13's cap on breakdowns is the clearest case: a request naming two dimensions is
// rejected at the contract, because the alternative is compiling a cartesian scan and
// finding out what it costs.
func (in *CohortQueryIn) Validate(v *muzak.Validation) {
	v.String(&in.Metric).Trim().Lower().Required().OneOf(CohortMetrics...)
	v.String(&in.Basis).Trim().Lower().OneOf(BasisInstall, BasisPlayer)
	v.String(&in.Cohort.Grain).As("cohort.grain").Trim().Lower().OneOf(GrainDay, GrainWeek)
	v.String(&in.RetentionBasis).As("retention_basis").Trim().Lower().
		OneOf(RetentionClassic, RetentionRolling)
	v.String(&in.Bucket).Trim().Lower().OneOf(BucketPlaytime, BucketSpend)

	if in.Breakdown != nil {
		v.String(&in.Breakdown.Dimension).As("breakdown.dimension").Trim().Lower().
			Required().OneOf(CohortDimensions...)
	}

	v.Slice(&in.Offsets).MaxItems(MaxOffsets).Unique()
	for i := range in.Offsets {
		v.Number(&in.Offsets[i]).As(fmt.Sprintf("offsets.%d", i)).Between(0, 365)
	}

	v.Slice(&in.Filters).MaxItems(MaxFilters)
	for i := range in.Filters {
		f := &in.Filters[i]
		v.String(&f.Field).As(fmt.Sprintf("filters.%d.field", i)).Trim().Lower().
			Required().OneOf(CohortDimensions...)
		v.String(&f.Op).As(fmt.Sprintf("filters.%d.op", i)).Trim().Lower().
			Required().OneOf("eq", "ne", "in")
		if f.Op == "in" {
			v.Slice(&f.In).As(fmt.Sprintf("filters.%d.in", i)).Required().MinItems(1).MaxItems(50)
		} else if f.Value == "" {
			v.Reject(&f.Value, "a value is required for the "+f.Op+" operator")
		}
	}

	in.validateRange(v)
	in.validateShape(v)
	in.rejectScopeInBody(v)
}

// validateRange bounds the cohort window.
func (in *CohortQueryIn) validateRange(v *muzak.Validation) {
	if in.Cohort.From.IsZero() {
		v.Reject(&in.Cohort.From, "a first cohort day is required")
		return
	}
	if in.Cohort.To.IsZero() {
		v.Reject(&in.Cohort.To, "a last cohort day is required")
		return
	}
	if in.Cohort.To.Before(in.Cohort.From.Time) {
		v.Reject(&in.Cohort.To, "the last cohort day is before the first")
		return
	}
	// +1 because both ends are inclusive.
	days := int(in.Cohort.To.Sub(in.Cohort.From.Time).Hours()/24) + 1
	if days > MaxCohortDays {
		v.Reject(&in.Cohort.To, fmt.Sprintf(
			"a cohort range covers at most %d days, and this one covers %d",
			MaxCohortDays, days))
	}
}

// validateShape enforces the rules that involve more than one field.
//
// Each of these is a question the planner would otherwise have to guess at, and §57.1 is
// explicit that it must not: retention_basis in particular is "never inferred", because
// classic and rolling answer different questions and a studio comparing our number against
// one they already have needs to know which they are looking at.
func (in *CohortQueryIn) validateShape(v *muzak.Validation) {
	switch in.Metric {
	case MetricRetention:
		if in.RetentionBasis == "" {
			v.Reject(&in.RetentionBasis,
				"retention needs a basis: classic counts players active on exactly that day, "+
					"rolling counts players active on that day or any day after")
		}
	case MetricBucket:
		if in.Bucket == "" {
			v.Reject(&in.Bucket, "a bucket query needs a bucket: playtime or spend")
		}
		if in.Breakdown != nil {
			v.Reject(&in.Breakdown.Dimension,
				"a bucket is already a segmentation; it cannot also be broken down")
		}
	default:
		if in.RetentionBasis != "" {
			v.Reject(&in.RetentionBasis, "only a retention query has a basis")
		}
		if in.Bucket != "" {
			v.Reject(&in.Bucket, "only a bucket query names a bucket")
		}
	}
}

// rejectScopeInBody refuses a request that tries to name its own tenant.
//
// Appendix C #3 and §69: the compiler injects the tenant from the credential, always. A
// body that could widen scope is a body somebody will eventually trust, and refusing is
// also the kinder answer — a client that sends project_id believing it selects a project
// otherwise gets somebody else's empty result and no explanation.
func (in *CohortQueryIn) rejectScopeInBody(v *muzak.Validation) {
	const why = "scope comes from the API key, never from the body; " +
		"use a key issued for the project and environment you mean"
	if in.ProjectID != "" {
		v.Reject(&in.ProjectID, why)
	}
	if in.EnvironmentID != "" {
		v.Reject(&in.EnvironmentID, why)
	}
	if in.OrgID != "" {
		v.Reject(&in.OrgID, why)
	}
}

// ---- ordinary metrics --------------------------------------------------------

// Metrics and intervals, named once.
const (
	MetricActivePlayers    = "active_players"
	MetricNewPlayers       = "new_players"
	MetricReturningPlayers = "returning_players"
	MetricSessions         = "sessions"
	MetricEvents           = "events"
	MetricPurchases        = "purchases"
	MetricPayers           = "payers"

	IntervalMinute = "minute"
	IntervalHour   = "hour"
	IntervalDay    = "day"
	IntervalMonth  = "month"
)

// SeriesMetrics is every metric the time-series planner can resolve.
//
// MetricRevenue and MetricPlaytime are shared with the cohort list rather than redeclared:
// "revenue" and "playtime" mean the same thing whether the question is keyed on arrival or
// on occurrence, and two constants for one word is how they drift apart.
var SeriesMetrics = []string{
	MetricActivePlayers, MetricNewPlayers, MetricReturningPlayers,
	MetricSessions, MetricPlaytime, MetricEvents,
	MetricRevenue, MetricPurchases, MetricPayers,
}

// Intervals is every output grain.
var Intervals = []string{IntervalMinute, IntervalHour, IntervalDay, IntervalMonth}

// Range ceilings per §26. A grain finer than the range can carry is refused rather than
// served as tens of thousands of points nobody can read and no browser can draw.
const (
	MaxMinuteDays = 1
	MaxHourDays   = 90
	MaxSeriesDays = 1095
)

// MetricQueryIn is the time-series query as a bindable input.
type MetricQueryIn struct {
	protocol.MetricQuery

	// The same decoys the cohort contract carries, and for the same reason.
	ProjectID     string `json:"project_id,omitzero" doc:"Not accepted; scope comes from the key"`
	EnvironmentID string `json:"environment_id,omitzero" doc:"Not accepted; scope comes from the key"`
	OrgID         string `json:"org_id,omitzero" doc:"Not accepted; scope comes from the key"`
}

// Validate bounds the query before anything is planned.
func (in *MetricQueryIn) Validate(v *muzak.Validation) {
	v.String(&in.Metric).Trim().Lower().Required().OneOf(SeriesMetrics...)
	v.String(&in.Interval).Trim().Lower().OneOf(Intervals...)
	v.String(&in.EventName).Trim().MaxLen(128)

	// §29.13, expressed as a cap rather than as a type this time, because §57's shape has
	// an array here and the contract should look like the document.
	v.Slice(&in.GroupBy).MaxItems(1).
		Message("a summary carries one dimension at a time; " +
			"the product of two dimensions recreates raw cardinality")
	for i := range in.GroupBy {
		v.String(&in.GroupBy[i]).As(fmt.Sprintf("group_by.%d", i)).
			Trim().Lower().Required().OneOf(CohortDimensions...)
	}

	v.Slice(&in.Filters).MaxItems(MaxFilters)
	for i := range in.Filters {
		f := &in.Filters[i]
		v.String(&f.Field).As(fmt.Sprintf("filters.%d.field", i)).Trim().Lower().
			Required().OneOf(CohortDimensions...)
		v.String(&f.Op).As(fmt.Sprintf("filters.%d.op", i)).Trim().Lower().
			Required().OneOf("eq", "ne", "in")
		if f.Op == "in" {
			v.Slice(&f.In).As(fmt.Sprintf("filters.%d.in", i)).Required().MinItems(1).MaxItems(50)
		} else if f.Value == "" {
			v.Reject(&f.Value, "a value is required for the "+f.Op+" operator")
		}
	}

	in.validateWindow(v)
	in.rejectScopeInBody(v)
}

// validateWindow bounds the range and enforces §26's grain policy.
func (in *MetricQueryIn) validateWindow(v *muzak.Validation) {
	if in.From.IsZero() {
		v.Reject(&in.From, "a first day is required")
		return
	}
	if in.To.IsZero() {
		v.Reject(&in.To, "a last day is required")
		return
	}
	if in.To.Before(in.From.Time) {
		v.Reject(&in.To, "the last day is before the first")
		return
	}

	days := int(in.To.Sub(in.From.Time).Hours()/24) + 1
	if days > MaxSeriesDays {
		v.Reject(&in.To, fmt.Sprintf("a range covers at most %d days, and this one covers %d",
			MaxSeriesDays, days))
		return
	}

	// §26's policy, enforced rather than advised. The ceiling is about what a chart can
	// mean: a minute grain over ninety days is 129,600 points.
	switch in.Interval {
	case IntervalMinute:
		if days > MaxMinuteDays {
			v.Reject(&in.Interval, fmt.Sprintf(
				"a minute grain covers at most %d day, and this range covers %d; "+
					"use an hour or a day grain", MaxMinuteDays, days))
		}
	case IntervalHour:
		if days > MaxHourDays {
			v.Reject(&in.Interval, fmt.Sprintf(
				"an hour grain covers at most %d days, and this range covers %d; "+
					"use a day grain", MaxHourDays, days))
		}
	}
}

func (in *MetricQueryIn) rejectScopeInBody(v *muzak.Validation) {
	const why = "scope comes from the API key, never from the body; " +
		"use a key issued for the project and environment you mean"
	if in.ProjectID != "" {
		v.Reject(&in.ProjectID, why)
	}
	if in.EnvironmentID != "" {
		v.Reject(&in.EnvironmentID, why)
	}
	if in.OrgID != "" {
		v.Reject(&in.OrgID, why)
	}
}

// ---- the dashboard's own queries --------------------------------------------

// A reporting key answers for a machine; these two answer for a person.
//
// # Why the dashboard cannot use the key
//
// §52 makes a reporting key a secret, and a browser cannot hold one: shipping it to the page means
// publishing every studio's analytics to anyone who opens the network tab. So the dashboard
// authenticates the way it authenticates everywhere else — a Firebase ID token — and the scope comes
// from the **path**, checked against the caller's membership and their project access list.
//
// Appendix C #3 still holds. "No customer query can execute without tenant scope" is a rule about
// where the scope comes from, not about which credential carries it: the project is resolved from
// the path by [core.ResolveUser] against the access list, the environment is looked up inside that
// project, and the body still cannot name any of the three. The decoy members below are inherited
// for exactly that reason.
//
// # Why the environment is in the path and not the body
//
// A reporting key is minted for one environment, so a machine's scope is fixed when the credential
// is. A person switches between development and production in a dashboard, so theirs has to be part
// of the request — and the path is where it belongs, because it identifies *which* data is being
// asked about rather than what is being asked. It is not a widening: all three environments belong
// to the project the access list already granted.

// MemberCohortQueryIn is a cohort question asked by a signed-in person.
type MemberCohortQueryIn struct {
	CohortQueryIn

	OrgSlug     string `path:"org" doc:"The organization's slug"`
	ProjectSlug string `path:"project" doc:"The project's slug"`
	Environment string `path:"environment" doc:"development, staging or production"`
}

func (in *MemberCohortQueryIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
	// The question's own rules, unchanged. Declared once so a query asked by a person and one
	// asked by a key cannot be bounded differently — which would make "it works with the key"
	// a real sentence somebody has to debug.
	v.Nested(&in.CohortQueryIn)
}

// MemberMetricQueryIn is a time-series question asked by a signed-in person.
type MemberMetricQueryIn struct {
	MetricQueryIn

	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
}

func (in *MemberMetricQueryIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
	v.Nested(&in.MetricQueryIn)
}
