package query

import (
	"slices"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

// The time-series path (project.md §26, §27, §57).
//
// It answers questions keyed on *when something happened*, where the cohort path answers
// questions keyed on *when a player arrived*. §27 draws the line and says which section a
// new metric belongs in; the two never share a table and never share a request.
//
// The same two properties hold as on the cohort side, for the same reasons: the builder has
// no join, and the tenant is a parameter of compilation rather than something a caller
// sends.

// SeriesPlan is a resolved time-series query.
type SeriesPlan struct {
	Fact      Fact
	Metric    string
	Interval  string
	Dimension string

	// Approximate marks a source whose player count is a sketch rather than a distinct
	// count, so the answer can say so (§27).
	Approximate bool

	Statement Statement
}

// CompileSeries resolves a validated metric query into one statement.
func CompileSeries(scope Scope, q protocol.MetricQuery, limits Limits) (*SeriesPlan, error) {
	interval := q.Interval
	if interval == "" {
		interval = schemas.IntervalDay
	}

	dimension, err := seriesDimension(q)
	if err != nil {
		return nil, err
	}

	plan := &SeriesPlan{Metric: q.Metric, Interval: interval, Dimension: dimension}

	switch interval {
	case schemas.IntervalMinute:
		plan.Fact, plan.Approximate = factEvents1m, true
	case schemas.IntervalHour:
		plan.Fact, plan.Approximate = factEvents1h, true
	case schemas.IntervalMonth:
		plan.Fact = factMetricsMonthly
	default:
		plan.Fact = factMetricsDaily
		if dimension != "" {
			plan.Fact = factMetricsBreakdown
		}
	}

	if err := checkSeriesShape(q, plan); err != nil {
		return nil, err
	}
	if err := checkSeriesTier(scope, q, plan); err != nil {
		return nil, err
	}

	plan.Statement = compileSeries(scope, q, plan, limits)
	return plan, nil
}

// seriesDimension applies the cohort path's rule to the summary tables, which have the same
// shape and therefore the same limit.
func seriesDimension(q protocol.MetricQuery) (string, error) {
	dimension := ""
	if len(q.GroupBy) > 0 {
		dimension = q.GroupBy[0]
	}
	for _, f := range q.Filters {
		if dimension == "" {
			dimension = f.Field
			continue
		}
		if f.Field != dimension {
			return "", refuse("filters",
				"a summary carries one dimension at a time (§29.13), so %q and %q cannot be "+
					"combined in one query; ask for one of them and filter the other in a second query",
				dimension, f.Field)
		}
	}
	return dimension, nil
}

// realtimeMetrics are the measures the minute and hour tiers actually hold.
//
// Those tiers are materialized views over events_raw keyed by event name. They know how many
// events happened and roughly how many devices sent them; they do not know whether a device
// was new, because that is a property of the player rather than of the block being inserted.
var realtimeMetrics = []string{
	schemas.MetricActivePlayers, schemas.MetricEvents, schemas.MetricPlaytime,
	schemas.MetricRevenue, schemas.MetricPurchases,
}

// checkSeriesShape refuses a question the chosen tier cannot answer.
//
// Refused with the reason and a grain that would work, never served as a flat line. §32.1
// asks for this on the cohort side and the argument is identical here: a chart of zeros is
// a claim about the game.
func checkSeriesShape(q protocol.MetricQuery, plan *SeriesPlan) error {
	realtime := plan.Fact == factEvents1m || plan.Fact == factEvents1h

	if realtime {
		if !slices.Contains(realtimeMetrics, q.Metric) {
			return refuse("metric",
				"%q is not available at a %s grain: the realtime summaries are built from event "+
					"blocks and cannot tell a new player from a returning one, or count a session. "+
					"Ask for it at a day grain",
				q.Metric, plan.Interval)
		}
		if plan.Dimension != "" {
			return refuse("group_by",
				"the realtime summaries are keyed by event name rather than by %s; "+
					"a breakdown by %s is available at a day grain",
				plan.Dimension, plan.Dimension)
		}
	} else if q.EventName != "" {
		return refuse("event_name",
			"a %s grain summarises every event together, so it cannot be narrowed to %q; "+
				"use an hour or minute grain for one event",
			plan.Interval, q.EventName)
	}

	if plan.Fact == factMetricsMonthly && q.Metric == schemas.MetricReturningPlayers {
		return refuse("metric",
			"returning players is a daily distinction: a player is new on the day they arrived "+
				"and returning on every other day they appear. Ask for it at a day grain")
	}
	return nil
}

// checkSeriesTier refuses what a collection policy tier cannot support (§29.15).
//
// §98's kids_strict gives every batch a fresh installation identifier, so a device cannot be
// recognised tomorrow. Three questions depend on exactly that and all three would return a
// confident, wrong number rather than an error: every player would look new every day, and a
// month's actives would be the sum of its days rather than a count of people.
func checkSeriesTier(scope Scope, q protocol.MetricQuery, plan *SeriesPlan) error {
	switch scope.PolicyTier {
	case "kids_strict":
		switch {
		case q.Metric == schemas.MetricNewPlayers || q.Metric == schemas.MetricReturningPlayers:
			return refuse("metric",
				"this project is on the kids_strict collection tier, which stores no persistent "+
					"identifier, so every player looks new every day and the new/returning split "+
					"is not computable; active players, sessions, events and revenue are available")
		case plan.Fact == factMetricsMonthly:
			return refuse("interval",
				"this project is on the kids_strict collection tier, which stores no persistent "+
					"identifier, so a month's active players would be the sum of its days rather "+
					"than a count of people; use a day grain")
		}
	}
	return nil
}

// compileSeries builds the one statement.
func compileSeries(scope Scope, q protocol.MetricQuery, plan *SeriesPlan, limits Limits) Statement {
	bucket := bucketColumn(plan.Fact)

	b := from(plan.Fact)
	b.column(bucket, "bucket")
	if plan.Dimension != "" {
		b.column("dim_value", "dim_value")
	} else {
		b.column("''", "dim_value")
	}

	switch plan.Fact {
	case factEvents1m, factEvents1h:
		// uniqMerge, so this is a sketch. The response says so rather than letting a
		// realtime number be compared against an invoice (§27).
		b.column("uniqMerge(players)", "active_players").
			column("toUInt64(0)", "new_players").
			column("toUInt64(0)", "returning_players").
			column("toUInt64(0)", "sessions").
			column("toUInt64(0)", "payers")
	case factMetricsMonthly:
		b.column("sum(active_players)", "active_players").
			column("sum(new_players)", "new_players").
			column("toUInt64(0)", "returning_players").
			column("sum(sessions)", "sessions").
			column("sum(payers)", "payers")
	default:
		b.column("sum(active_players)", "active_players").
			column("sum(new_players)", "new_players").
			column("sum(returning_players)", "returning_players").
			column("sum(sessions)", "sessions").
			column("sum(payers)", "payers")
	}
	b.column("sum(playtime_ms)", "playtime_ms").
		column("sum(events)", "events").
		column("sum(revenue_micros)", "revenue_micros").
		column("sum(purchases)", "purchases")

	scopeAnd(b, scope)

	low, high := seriesWindow(q, plan.Interval)
	b.gte(bucket, low).lt(bucket, high)

	switch plan.Fact {
	case factEvents1m, factEvents1h:
		if q.EventName != "" {
			b.eq("event_name", q.EventName)
		}
	case factMetricsBreakdown:
		b.eq("dim_name", plan.Dimension)
		applyFilters(b, "dim_value", q.Filters)
	}

	b.groupOn("bucket", "dim_value").withLimit(limits.MaxRows)
	return b.statement()
}

// bucketColumn is the time key each tier is stored on.
func bucketColumn(fact Fact) string {
	switch fact {
	case factEvents1m:
		return "minute"
	case factEvents1h:
		return "hour"
	case factMetricsMonthly:
		return "month"
	default:
		return "activity_date"
	}
}

// seriesWindow renders the range as a half-open interval, in the literal type the tier's
// time column actually is.
//
// Half-open rather than BETWEEN because the minute and hour tiers are keyed on a timestamp
// while the request names days: "up to and including the 14th" is `< the 15th`, and writing
// it as `BETWEEN the 1st AND the 14th` silently drops everything after midnight on the last
// day — twenty-three hours of the range a customer asked for.
//
// The layout is not cosmetic. events_1m and events_1h are keyed on DateTime; metrics_daily
// and metrics_monthly on Date. ClickHouse refuses to compare a Date against a timestamp
// literal rather than coercing it, which is the good outcome — the alternative would be a
// silent truncation somewhere in a window boundary.
func seriesWindow(q protocol.MetricQuery, interval string) (string, string) {
	const (
		timestamp = "2006-01-02 15:04:05"
		date      = "2006-01-02"
	)

	low := q.From.Time.UTC()
	high := q.To.Time.UTC().AddDate(0, 0, 1)

	switch interval {
	case schemas.IntervalMinute, schemas.IntervalHour:
		return low.Format(timestamp), high.Format(timestamp)
	case schemas.IntervalMonth:
		low = time.Date(low.Year(), low.Month(), 1, 0, 0, 0, 0, time.UTC)
		high = time.Date(q.To.Year(), q.To.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	}
	return low.Format(date), high.Format(date)
}

// seriesValue picks the number the request asked to plot.
//
// The row carries every measure the source had, because they came back in the same scan.
// This is only about which one the chart draws.
func seriesValue(metric string, row protocol.MetricRow) float64 {
	switch metric {
	case schemas.MetricActivePlayers:
		return float64(row.ActivePlayers)
	case schemas.MetricNewPlayers:
		return float64(row.NewPlayers)
	case schemas.MetricReturningPlayers:
		return float64(row.ReturningPlayers)
	case schemas.MetricSessions:
		return float64(row.Sessions)
	case schemas.MetricPlaytime:
		return float64(row.PlaytimeMs)
	case schemas.MetricEvents:
		return float64(row.Events)
	case schemas.MetricPurchases:
		return float64(row.Purchases)
	case schemas.MetricPayers:
		return float64(row.Payers)
	default:
		return float64(row.RevenueMicros)
	}
}
