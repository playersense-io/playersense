package query

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// DayTotals is one day of a project's own figures, from metrics_daily.
type DayTotals struct {
	Day        time.Time
	Players    uint64
	NewPlayers uint64
	Sessions   uint64
	PlaytimeMs uint64
	Revenue    int64
	Payers     uint64
	ComputedAt time.Time
}

// DailyTotals reads a project's days from metrics_daily, one row a day, for comparing a window
// with the weeks before it. Days the nightly derivation has not built yet are absent.
func (e *Engine) DailyTotals(ctx context.Context, scope Scope, first, last time.Time) ([]DayTotals, error) {
	b := from(factMetricsDaily)
	b.column("activity_date", "d").
		column("active_players", "p").
		column("new_players", "np").
		column("sessions", "s").
		column("playtime_ms", "pt").
		column("revenue_micros", "rev").
		column("payers", "pay").
		column("computed_at", "at")
	scopeAnd(b, scope)
	b.between("activity_date", date(first), date(last))
	b.order("d").withLimit(1000)

	var out []DayTotals
	err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		var d DayTotals
		if err := rows.Scan(&d.Day, &d.Players, &d.NewPlayers, &d.Sessions, &d.PlaytimeMs, &d.Revenue,
			&d.Payers, &d.ComputedAt); err != nil {
			return err
		}
		out = append(out, d)
		return nil
	})
	if err != nil {
		return nil, failed("metrics_daily", err)
	}
	return out, nil
}
