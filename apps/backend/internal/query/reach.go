package query

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/internal/schemas"
)

// ReachSample reads up to limit installs seen since a moment, with their lifetime facts, for
// estimating how many players a segment holds.
//
// In key order and unsorted, so the read aggregates each device as it streams past and stops
// at the limit rather than finishing the project. The key's last part is device_id, a hash of
// the install, so the first N in key order are as good as a random N.
func (e *Engine) ReachSample(ctx context.Context, scope Scope, since time.Time, limit int, now time.Time) ([]schemas.PlayerSummaryOut, error) {
	b := from(factPlayers)
	lifetimeColumns(b)
	scopeAnd(b, scope)
	b.group("project_seq", "environment_seq", "device_id")
	b.havingThat("last >= ?", timestamp(since))
	b.withLimit(limit)

	var out []schemas.PlayerSummaryOut
	if err := e.readInOrder(ctx, b.statement(), func(rows driver.Rows) error {
		p, err := scanLifetime(rows, now)
		if err != nil {
			return err
		}
		out = append(out, p)
		return nil
	}); err != nil {
		return nil, failed("player_lifetime", err)
	}
	return out, nil
}

// PlayersSince is how many players the project has seen since a moment, from the hourly
// sketches (00013): an estimate within about a percent, read from a few hundred rows.
func (e *Engine) PlayersSince(ctx context.Context, scope Scope, since time.Time) (uint64, error) {
	b := from(factActivity1h)
	b.column("uniqCombinedMerge(14)(players)", "n")
	scopeAnd(b, scope)
	b.and("hour >= ?", timestamp(since.Truncate(time.Hour)))
	var n uint64
	if err := e.read(ctx, b.statement(), func(rows driver.Rows) error {
		return rows.Scan(&n)
	}); err != nil {
		return 0, failed("activity_1h", err)
	}
	return n, nil
}
