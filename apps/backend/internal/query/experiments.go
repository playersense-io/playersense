package query

import (
	"context"
	"math"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/rollup"
	"playersense.dev/backend/internal/schemas"
)

// Experiments (00019): one flag's arms, read from experiment_daily, whose sums and sums of squares
// add across exposure days so every statistic here is arithmetic on a handful of rows.

// Experiment compares a flag's arms for players first exposed in the window.
func (e *Engine) Experiment(ctx context.Context, scope Scope, flag string, w Window) (*schemas.ExperimentOut, error) {
	out := &schemas.ExperimentOut{Flag: flag, From: w.From, To: w.To, Days: rollup.ExperimentDays, SuppressionThreshold: e.k}
	withheld := Suppressed{Threshold: e.k}

	// A row's day 1 has happened when the exposure day plus one is before the day it was built;
	// day 7 and the whole seven-day window the same way.
	d1 := "exposure_date + 1 < toDate(computed_at)"
	d7 := "exposure_date + 7 < toDate(computed_at)"
	whole := "exposure_date + 6 < toDate(computed_at)"

	b := from(factExperiments)
	b.column("variant", "v").
		column("published", "pub").
		column("sum(players)", "n").
		column("sumIf(players, "+whole+")", "mature").
		column("sumIf(players, "+d1+")", "d1_n").
		column("sumIf(retained_d1, "+d1+")", "d1_k").
		column("sumIf(players, "+d7+")", "d7_n").
		column("sumIf(retained_d7, "+d7+")", "d7_k").
		column("sum(payers)", "pay").
		column("sum(revenue_micros)", "rev").
		column("sum(revenue_sq)", "rev_sq").
		column("sum(sessions)", "ses").
		column("sum(sessions_sq)", "ses_sq").
		column("sum(playtime_ms)", "pt").
		column("sum(playtime_sq)", "pt_sq").
		column("max(computed_at)", "at")
	scopeAnd(b, scope)
	b.eq("flag", flag).between("exposure_date", date(w.From), date(w.To))
	b.group("v", "pub").order("pub DESC", "n DESC", "v").withLimit(1000)

	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var (
			v                                   schemas.ExperimentVariantOut
			published                           uint8
			d1n, d1k, d7n, d7k, payers, ses, pt uint64
			rev                                 int64
			revSq, sesSq, ptSq                  float64
			at                                  time.Time
		)
		if err := rows.Scan(&v.Variant, &published, &v.Players, &v.MaturePlayers, &d1n, &d1k, &d7n, &d7k,
			&payers, &rev, &revSq, &ses, &sesSq, &pt, &ptSq, &at); err != nil {
			return err
		}
		if at.After(out.ComputedAt) {
			out.ComputedAt = at
		}
		if withheld.withheld(v.Players) {
			return nil
		}
		v.Published = published == 1
		v.D1Retention = observed(d1k, d1n)
		v.D7Retention = observed(d7k, d7n)
		v.PayerConversion = *proportion(payers, v.Players)
		v.RevenuePerPlayer = moments(v.Players, float64(rev), revSq, 1)
		v.SessionsPerPlayer = moments(v.Players, float64(ses), sesSq, 1)
		v.PlaytimeMinutes = moments(v.Players, float64(pt), ptSq, 60_000)
		out.Variants = append(out.Variants, v)
		return nil
	}); err != nil {
		return nil, failed("experiment_daily", err)
	}
	if len(out.Variants) == 0 && withheld.Cells == 0 {
		return nil, ErrNotFound
	}

	d := from(factExperiments)
	d.column("toDateTime(exposure_date, 'UTC')", "day").
		column("variant", "v").
		column("published", "pub").
		column("sum(players)", "n").
		column("sum(revenue_micros)", "rev").
		column("sum(payers)", "pay")
	scopeAnd(d, scope)
	d.eq("flag", flag).between("exposure_date", date(w.From), date(w.To))
	d.groupOn("day", "v", "pub").withLimit(e.limits.MaxRows)
	if err := e.read(ctx, d.statement(), func(rows driver.Rows) error {
		var r schemas.ExperimentDayOut
		var published uint8
		var rev int64
		var payers uint64
		if err := rows.Scan(&r.Day, &r.Variant, &published, &r.Players, &rev, &payers); err != nil {
			return err
		}
		if withheld.withheld(r.Players) {
			return nil
		}
		r.Published = published == 1
		r.RevenuePerPlayer = float64(rev) / float64(r.Players)
		r.PayerConversion = float64(payers) / float64(r.Players)
		out.Daily = append(out.Daily, r)
		return nil
	}); err != nil {
		return nil, failed("experiment_daily", err)
	}

	out.SuppressedCells = withheld.Cells
	return out, nil
}

// moments is a mean and a sample standard deviation from a count, a sum and a sum of squares,
// in units of scale.
func moments(n uint64, sum, sumSq, scale float64) schemas.ExperimentStatOut {
	if n == 0 {
		return schemas.ExperimentStatOut{}
	}
	mean := sum / float64(n)
	variance := 0.0
	if n > 1 {
		variance = max(0, (sumSq-float64(n)*mean*mean)/float64(n-1))
	}
	return schemas.ExperimentStatOut{N: n, Mean: mean / scale, SD: math.Sqrt(variance) / scale}
}

// proportion is a rate as a statistic: its mean is the share, its deviation a Bernoulli's.
func proportion(k, n uint64) *schemas.ExperimentStatOut {
	if n == 0 {
		return &schemas.ExperimentStatOut{}
	}
	p := float64(k) / float64(n)
	return &schemas.ExperimentStatOut{N: n, Mean: p, SD: math.Sqrt(p * (1 - p))}
}

// observed is a proportion, or nil when nobody's day has come yet.
func observed(k, n uint64) *schemas.ExperimentStatOut {
	if n == 0 {
		return nil
	}
	return proportion(k, n)
}
