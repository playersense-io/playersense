package protocol

import (
	"fmt"
	"time"
)

// The cohort query contract (project.md §57, §57.1).
//
// The client sends a *logical* query and never SQL. §10.5 is blunt about why: "the browser
// never receives ClickHouse credentials and never submits arbitrary SQL." The planner
// decides which fact answers the shape, what the SQL is, how it is parameterised and what
// the cache key is, which is what lets the storage change without the browser noticing.
//
// It lives beside the SDK envelope because it is the same kind of thing: a wire contract
// several clients implement, which §9 requires to survive the Stage 1 to Stage 2 move
// unchanged.

// QuerySchemaVersion is the query contract version this build speaks.
//
// It is part of the cache key (§58). Without it a deployment that changes what a metric
// means would keep serving the old numbers from Valkey until every entry expired.
const QuerySchemaVersion = 1

// CohortQuery is one logical cohort question.
//
// Note what is *not* here: no project, no environment, no organisation. Scope comes from
// the key and from nowhere else (§76, §69, Appendix C #3). A body that could name a tenant
// is a body somebody will eventually trust.
type CohortQuery struct {
	// Metric is what to measure: retention, playtime, revenue, ltv, payer_conversion or
	// bucket.
	Metric string `json:"metric" doc:"retention, playtime, revenue, ltv, payer_conversion or bucket"`

	// Basis is what a cohort is keyed on — install by default, player where the project
	// has accounts (§29.2).
	Basis string `json:"basis,omitzero" doc:"install (default) or player"`

	// Cohort is the range of cohort days to return, and how they are grouped.
	Cohort CohortRange `json:"cohort"`

	// Offsets are the day-offsets to return. Empty means the metric's default set.
	Offsets []int `json:"offsets,omitzero" doc:"Which day-offsets to return"`

	// RetentionBasis is classic or rolling (§29.10). Never inferred: the two answer
	// different questions and a studio comparing PlayerSense against a number they already
	// have needs to know which one they are looking at.
	RetentionBasis string `json:"retention_basis,omitzero" doc:"classic or rolling; required for retention"`

	// Breakdown is at most one sticky dimension (§29.13). Two would be a cartesian scan.
	Breakdown *Breakdown `json:"breakdown,omitzero"`

	// Bucket selects which behavioural segmentation to return, for metric=bucket (§29.11).
	Bucket string `json:"bucket,omitzero" doc:"playtime or spend; required for metric=bucket"`

	// Filters narrow the population by a sticky dimension.
	Filters []Filter `json:"filters,omitzero"`
}

// CohortRange is the span of cohort days a query covers.
type CohortRange struct {
	From  Day    `json:"from" doc:"First cohort day, inclusive"`
	To    Day    `json:"to" doc:"Last cohort day, inclusive"`
	Grain string `json:"grain,omitzero" doc:"day (default) or week"`
}

// Breakdown names the one dimension to split by.
type Breakdown struct {
	Dimension string `json:"dimension" doc:"install_platform, install_country, first_app_version or acquisition_source"`
}

// Filter is one equality or membership test on a sticky dimension.
//
// The operators are deliberately few. A filter language rich enough to express anything is
// a filter language rich enough to express a scan, and §32 wants the dimensions constrained
// rather than open.
type Filter struct {
	Field string   `json:"field" doc:"A sticky cohort dimension"`
	Op    string   `json:"op" doc:"eq, ne or in"`
	Value string   `json:"value,omitzero" doc:"The value, for eq and ne"`
	In    []string `json:"in,omitzero" doc:"The values, for in"`
}

// Day is a calendar date on the wire, rendered as 2006-01-02.
//
// A date rather than a timestamp because a cohort day is a day: giving it a time would
// invite a timezone, and a cohort that means something different in two timezones is a
// cohort two people cannot discuss. Everything in §29 is computed in UTC.
type Day struct{ time.Time }

// MarshalJSON renders the date alone.
func (d Day) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.Format(dayLayout) + `"`), nil
}

// UnmarshalJSON accepts the date alone, and nothing else.
//
// Deliberately strict: accepting a full timestamp here would silently discard the time
// somebody meant, and a query for "2026-06-01T12:00:00Z" that quietly becomes the whole of
// 1 June is worse than one that is refused.
func (d *Day) UnmarshalJSON(raw []byte) error {
	s := string(raw)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("a cohort date must be a string of the form %s", dayLayout)
	}
	t, err := time.Parse(dayLayout, s[1:len(s)-1])
	if err != nil {
		return fmt.Errorf("a cohort date must be of the form %s", dayLayout)
	}
	d.Time = t
	return nil
}

// IsZero reports an unset date, so a validator can tell "missing" from "the epoch".
func (d Day) IsZero() bool { return d.Time.IsZero() }

// NewDay truncates a time to its UTC calendar date.
func NewDay(t time.Time) Day {
	y, m, dd := t.UTC().Date()
	return Day{time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)}
}

const dayLayout = "2006-01-02"

// CohortResult is the answer, with the provenance the numbers need to be read correctly
// (§57.1).
//
// Every field beyond Rows exists because a cohort number is misleading without it. A curve
// still filling looks like a curve that fell; a suppressed cell looks like a zero. Saying
// so is cheaper than a support conversation about why retention "dropped".
type CohortResult struct {
	Rows []CohortRow `json:"rows"`

	// CohortMaturityDays is how much of the requested curve has actually had time to
	// happen, so a dashboard can mark a cohort still filling rather than drawing a partial
	// curve as if it were final.
	CohortMaturityDays int `json:"cohort_maturity_days"`

	// ComputedAt is when the derivation last rebuilt these facts — never when this
	// response was assembled. A cached answer must not claim freshness it does not have.
	ComputedAt time.Time `json:"computed_at"`

	// SuppressedCells is how many cells were withheld as too thin to disclose, and
	// SuppressionThreshold is the k they fell under (§32.2).
	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// CohortRow is one cell of the answer.
type CohortRow struct {
	CohortDate Day `json:"cohort_date"`
	DayOffset  int `json:"day_offset"`

	// DimValue is the breakdown value, or the bucket label for metric=bucket.
	DimValue string `json:"dim_value,omitzero"`

	// CohortSize is the denominator this row's rate was computed against — the size of
	// this dim_value's own cohort, never the whole cohort's (§29.9).
	CohortSize uint64 `json:"cohort_size"`

	// Players is the numerator: how many of them were active at this offset.
	Players uint64 `json:"players"`

	// Rate is Players over CohortSize, carried so every client does not reimplement the
	// division and disagree about rounding.
	Rate float64 `json:"rate"`

	PlaytimeMs    uint64 `json:"playtime_ms,omitzero"`
	RevenueMicros int64  `json:"revenue_micros,omitzero"`
	Purchases     uint64 `json:"purchases,omitzero"`
	Payers        uint64 `json:"payers,omitzero"`

	// RevenueMicrosCumulative is the running sum over day_offset, which is what LTV is.
	// Computed at read time because a cumulative value cannot live in a SummingMergeTree
	// (§29.12).
	RevenueMicrosCumulative int64 `json:"revenue_micros_cumulative,omitzero"`
}

// ---- ordinary metrics (§57) --------------------------------------------------

// MetricQuery is one logical question about what happened, rather than about who arrived.
//
// The distinction is §27's: a cohort fact is keyed on when a player *arrived*, crossed with
// how long after that something happened; these are keyed on when it happened. They read
// different tables and they cannot be mixed in one request, which is why this is a separate
// shape rather than a flag on CohortQuery.
//
// As with cohorts, no project, environment or organisation appears here. Scope comes from
// the key (§76, §69, Appendix C #3).
type MetricQuery struct {
	// Metric is what to plot: active_players, new_players, returning_players, sessions,
	// playtime, events, revenue, purchases or payers.
	Metric string `json:"metric" doc:"active_players, new_players, sessions, revenue…"`

	// From and To bound the window, inclusive.
	From Day `json:"from"`
	To   Day `json:"to"`

	// Interval is the output grain: minute, hour, day or month.
	//
	// It selects the source as well as the shape, and §26 bounds which is allowed for a
	// given range — a minute-grain chart over ninety days is 129,600 points, which is not a
	// chart.
	Interval string `json:"interval,omitzero" doc:"minute, hour, day (default) or month"`

	// GroupBy splits the series by at most one sticky dimension.
	//
	// An array because §57's shape has one, and capped at a single entry for §29.13's
	// reason: a summary carrying the product of its dimensions recreates raw cardinality.
	GroupBy []string `json:"group_by,omitzero" doc:"At most one dimension"`

	// EventName narrows to a single event, on the minute and hour tiers where the rollup
	// is keyed by it.
	EventName string `json:"event_name,omitzero" doc:"Only on minute and hour intervals"`

	// Filters narrow the population by a sticky dimension.
	Filters []Filter `json:"filters,omitzero"`
}

// MetricResult is a time series with the provenance a reader needs.
type MetricResult struct {
	Rows []MetricRow `json:"rows"`

	// Interval and Source say what was actually read, because §57's whole point is that
	// the planner chooses and the client does not have to know — which only works if the
	// client can still find out.
	Interval string `json:"interval"`
	Source   string `json:"source"`

	// Approximate marks a series whose player counts come from a sketch rather than a
	// distinct count. The realtime tiers trade exactness for immediacy (§27), and a number
	// that might be off by a fraction of a percent must say so rather than be compared
	// against an invoice.
	Approximate bool `json:"approximate"`

	// ComputedAt is when the facts were derived, and is zero for the realtime tiers, which
	// are continuous rather than derived.
	ComputedAt time.Time `json:"computed_at,omitzero"`

	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// MetricRow is one point of the series.
//
// It carries every measure the chosen source has, not only the requested one. They came
// back in the same scan, so a dashboard drawing active players, sessions and revenue on one
// screen makes one request rather than three — and the three are then guaranteed to describe
// the same rows, which three separate requests spanning a derivation are not.
type MetricRow struct {
	Bucket   time.Time `json:"bucket" doc:"Start of the interval, UTC"`
	DimValue string    `json:"dim_value,omitzero"`

	// Value is the metric the request named, as a number a chart can plot directly.
	Value float64 `json:"value"`

	ActivePlayers    uint64 `json:"active_players,omitzero"`
	NewPlayers       uint64 `json:"new_players,omitzero"`
	ReturningPlayers uint64 `json:"returning_players,omitzero"`
	Sessions         uint64 `json:"sessions,omitzero"`
	PlaytimeMs       uint64 `json:"playtime_ms,omitzero"`
	Events           uint64 `json:"events,omitzero"`
	RevenueMicros    int64  `json:"revenue_micros,omitzero"`
	Purchases        uint64 `json:"purchases,omitzero"`
	Payers           uint64 `json:"payers,omitzero"`
}
