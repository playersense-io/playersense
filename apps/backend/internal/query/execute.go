package query

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

// Engine runs compiled cohort queries against a cell's ClickHouse.
//
// It holds no tenant state. A Scope is passed to every call, which is what makes it
// impossible to serve a request against a scope left over from the last one.
type Engine struct {
	ch     *chdb.Conn
	limits Limits
	k      int
}

// NewEngine returns an engine reading through conn.
func NewEngine(conn *chdb.Conn, limits Limits, threshold int) *Engine {
	if limits.MaxRows <= 0 {
		limits = DefaultLimits()
	}
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &Engine{ch: conn, limits: limits, k: threshold}
}

// Threshold is the minimum cell size in force, published in every response so a client can
// say what was withheld rather than guessing (§57.1).
func (e *Engine) Threshold() int { return e.k }

// Run answers one cohort question.
//
// Two statements, never one with a join: the measures, then the denominators. The division
// happens here rather than in ClickHouse, which is what lets the compiler have no join at
// all rather than one exception (see the package note and Appendix C #16).
func (e *Engine) Run(ctx context.Context, scope Scope, q protocol.CohortQuery) (*protocol.CohortResult, error) {
	plan, err := Compile(scope, q, e.limits)
	if err != nil {
		return nil, err
	}

	ctx = e.bound(ctx)

	sizes, maturity, computedAt, err := e.denominators(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("read the cohort sizes: %w", err)
	}
	measures, err := e.measures(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("read the cohort facts: %w", err)
	}
	if plan.Rolling {
		measures = rollingSuffixSum(measures, plan.Offsets)
	}
	if isCumulative(plan.Metric) {
		measures = cumulate(measures, plan.Offsets)
	}

	rows := make([]protocol.CohortRow, 0, len(measures))
	for _, m := range measures {
		row := protocol.CohortRow{
			CohortDate:              protocol.NewDay(m.cohortDate),
			DayOffset:               int(m.dayOffset),
			DimValue:                m.dimValue,
			Players:                 m.players,
			Payers:                  m.payers,
			PlaytimeMs:              m.playtimeMs,
			RevenueMicros:           m.revenueMicros,
			Purchases:               m.purchases,
			RevenueMicrosCumulative: m.revenueCumulative,
		}
		row.CohortSize = sizes[sizeKey{m.cohortDate, m.dimValue}]
		row.Rate = rate(plan.Metric, row)
		rows = append(rows, row)
	}

	kept, suppressed := Suppression{K: e.k, Metric: plan.Metric}.Apply(rows)

	return &protocol.CohortResult{
		Rows:                 kept,
		CohortMaturityDays:   maturity,
		ComputedAt:           computedAt,
		SuppressedCells:      suppressed,
		SuppressionThreshold: e.k,
	}, nil
}

// bound attaches the per-query resource limits (§32).
//
// Set on the query rather than on the connection, because a limit that lives on a shared
// handle is a limit one careless caller removes for everybody. A query that exceeds one is
// refused by ClickHouse rather than served slowly, which is the behaviour §80's latency
// targets actually need.
func (e *Engine) bound(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"max_execution_time":   e.limits.seconds(),
		"max_rows_to_read":     e.limits.MaxRowsToRead,
		"max_bytes_to_read":    e.limits.MaxBytesToRead,
		"max_result_rows":      uint64(e.limits.MaxRows),
		"result_overflow_mode": "throw",
		// The credential this runs under is a reporting key, which reads and never writes
		// (§52). Saying so to ClickHouse as well means a statement that somehow acquired a
		// mutation is refused by the server rather than trusted to be harmless.
		"readonly": 1,
	}))
}

type sizeKey struct {
	cohortDate time.Time
	dimValue   string
}

// denominators reads cohort_sizes and the provenance that travels with it.
func (e *Engine) denominators(ctx context.Context, plan *Plan) (map[sizeKey]uint64, int, time.Time, error) {
	conn, err := e.ch.DB()
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	rows, err := conn.Query(ctx, plan.Denominator.SQL, plan.Denominator.Args...)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	defer rows.Close()

	sizes := map[sizeKey]uint64{}
	maturity := 0
	var computedAt time.Time
	for rows.Next() {
		var (
			cohortDate time.Time
			dimValue   string
			size       uint64
			days       uint16
			at         time.Time
		)
		if err := rows.Scan(&cohortDate, &dimValue, &size, &days, &at); err != nil {
			return nil, 0, time.Time{}, err
		}
		sizes[sizeKey{cohortDate, dimValue}] = size
		// The least mature cohort in the range decides what the whole answer can claim: a
		// curve is only as settled as its youngest cohort, and reporting the oldest would
		// describe the response as more final than it is.
		if maturity == 0 || int(days) < maturity {
			maturity = int(days)
		}
		if at.After(computedAt) {
			computedAt = at
		}
	}
	return sizes, maturity, computedAt, rows.Err()
}

// measureRow is one cell as ClickHouse returns it, before the denominator is attached.
type measureRow struct {
	cohortDate    time.Time
	dayOffset     uint16
	dimValue      string
	players       uint64
	payers        uint64
	playtimeMs    uint64
	revenueMicros int64
	purchases     uint64

	revenueCumulative int64
}

func (e *Engine) measures(ctx context.Context, plan *Plan) ([]measureRow, error) {
	conn, err := e.ch.DB()
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, plan.Numerator.SQL, plan.Numerator.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []measureRow
	for rows.Next() {
		var m measureRow
		if err := rows.Scan(&m.cohortDate, &m.dayOffset, &m.dimValue,
			&m.players, &m.payers, &m.playtimeMs, &m.revenueMicros, &m.purchases); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// rollingSuffixSum turns a last-activity histogram into a rolling retention curve (§29.10).
//
// Rolling retention at offset N is the number of players whose *final* activity fell on N
// or later. It is a suffix sum over this histogram and cannot be a sum of per-offset
// counters: a player active on days 3 and 5 appears under both there and would be counted
// twice. That is the whole reason player_lifetime carries max(last_seen).
func rollingSuffixSum(rows []measureRow, offsets []int) []measureRow {
	type series struct {
		cohortDate time.Time
		dimValue   string
	}
	histograms := map[series]map[uint16]uint64{}
	for _, r := range rows {
		key := series{r.cohortDate, r.dimValue}
		if histograms[key] == nil {
			histograms[key] = map[uint16]uint64{}
		}
		histograms[key][r.dayOffset] += r.players
	}

	out := make([]measureRow, 0, len(histograms)*len(offsets))
	for key, hist := range histograms {
		for _, offset := range offsets {
			var total uint64
			for last, n := range hist {
				if int(last) >= offset {
					total += n
				}
			}
			out = append(out, measureRow{
				cohortDate: key.cohortDate,
				dayOffset:  uint16(offset),
				dimValue:   key.dimValue,
				players:    total,
			})
		}
	}
	sortRows(out)
	return out
}

// cumulate turns per-offset revenue into a running total, then keeps only what was asked
// for (§29.12).
//
// The statement read every offset up to the highest requested precisely so this sum is
// complete. Filtering happens after, because filtering first is how a lifetime-value curve
// silently loses whatever fell in the gaps.
func cumulate(rows []measureRow, offsets []int) []measureRow {
	sortRows(rows)

	type series struct {
		cohortDate time.Time
		dimValue   string
	}
	running := map[series]int64{}
	wanted := make(map[int]bool, len(offsets))
	for _, o := range offsets {
		wanted[o] = true
	}

	out := make([]measureRow, 0, len(rows))
	for _, r := range rows {
		key := series{r.cohortDate, r.dimValue}
		running[key] += r.revenueMicros
		r.revenueCumulative = running[key]
		if wanted[int(r.dayOffset)] {
			out = append(out, r)
		}
	}
	return out
}

func sortRows(rows []measureRow) {
	slices.SortFunc(rows, func(a, b measureRow) int {
		if c := a.cohortDate.Compare(b.cohortDate); c != 0 {
			return c
		}
		if a.dimValue != b.dimValue {
			if a.dimValue < b.dimValue {
				return -1
			}
			return 1
		}
		return int(a.dayOffset) - int(b.dayOffset)
	})
}

// rate is the row's headline proportion.
//
// Which count is the numerator depends on the question: payer conversion is payers over the
// cohort, everything else is active players over the cohort. Carried on the row so that
// every client does not reimplement the division and disagree about rounding.
func rate(metric string, row protocol.CohortRow) float64 {
	if row.CohortSize == 0 {
		return 0
	}
	numerator := row.Players
	if metric == schemas.MetricPayerConversion {
		numerator = row.Payers
	}
	return float64(numerator) / float64(row.CohortSize)
}
