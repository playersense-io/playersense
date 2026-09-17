package cohort

import (
	"context"
	"fmt"
	"time"
)

// Prune bounds player_lifetime, which is otherwise the one table that grows forever
// (project.md §29.6, §33.1, §59).
//
// Every other table has an end. `events_raw` expires on the project's retention tier,
// `player_activity_daily` on 400 days, the cohort facts are counts and are tiny. This one
// holds a row per device for as long as the product exists: §33.1 notes it "grows with the
// player base rather than with time", which is slower than the others and is not a bound.
//
// # Why this is a job and not a TTL
//
// A TTL is the obvious answer and it is wrong here, for the reason §29.6 already gives
// about reads: *nothing may read this table directly*, because it is an
// AggregatingMergeTree and, until parts merge, one device exists as several partial rows.
// A row-level TTL is a reader. It would evaluate each stored row's own `last_seen` — a
// partial maximum — so an active player's older fragment can look eighteen months stale
// while the merged truth is that they played this morning. Expiring it would take that
// fragment's `playtime_ms` and `revenue_micros` with it and silently understate the
// player's lifetime totals, which §29.11's behavioural buckets are computed from.
//
// Measured on the development stack while writing this: 171 stored rows for 170 devices.
// The hazard is not hypothetical, it is the steady state between merges.
//
// A partition-level TTL is not available either. The table has no partition key, and it
// cannot usefully have one: the obvious candidate is the arrival month, and `first_seen` is
// a `SimpleAggregateFunction(min)` whose value differs between a device's partial rows —
// so partitioning on it would file one device in two partitions and break the aggregation
// outright.
//
// So the prune reads `player_lifetime_v`, the canonical view that does the merging, and
// deletes by device identifier. The view is the only correct reader, and this is a reader.
type Prune struct {
	Deriver *Deriver

	// After is how long a device may go unseen before its row is removed.
	//
	// It keys on **last activity**, not on arrival, and that distinction is the whole
	// design: an active player is never pruned however long ago they installed, so their
	// lifetime playtime and spend survive for §29.11's buckets. Only genuinely dormant
	// devices go.
	After time.Duration

	// MaxDevices bounds one run, so a first prune over years of accumulated dormancy is
	// several nights of bounded work rather than one enormous mutation.
	MaxDevices int

	// ChunkSize is how many identifiers go into one DELETE's IN list, for the reason
	// Backfill gives: ClickHouse refuses a subquery inside a mutation.
	ChunkSize int
}

// PruneResult reports what a run removed.
type PruneResult struct {
	Dormant int
	Deleted int
}

// MinimumAfter is the shortest dormancy window that cannot corrupt a denominator.
//
// `DeriveSizes` rebuilds the trailing `WORKER_COHORT_MONTHS` cohort months from
// `player_lifetime_v`, so every device that arrived inside that window must still be
// present when it runs. Pruning one sooner shrinks the denominator of a cohort that is
// still being republished, and retention jumps **up** — the exact mirror of the bug the
// cohort backfill exists to fix, and just as silent.
//
// The floor is therefore the rebuild window in days, plus a month so that a delayed run or
// a month boundary cannot clip it.
func MinimumAfter(cohortMonths int) time.Duration {
	if cohortMonths <= 0 {
		cohortMonths = 13
	}
	return time.Duration(cohortMonths+1) * 31 * 24 * time.Hour
}

// Run removes devices unseen for longer than After.
func (p *Prune) Run(ctx context.Context, now time.Time) (PruneResult, error) {
	var out PruneResult

	if p.After <= 0 {
		return out, fmt.Errorf("a prune window of %s would delete active players", p.After)
	}
	cutoff := now.UTC().Add(-p.After)

	maxDevices := p.MaxDevices
	if maxDevices <= 0 {
		maxDevices = 500_000
	}
	chunk := p.ChunkSize
	if chunk <= 0 {
		chunk = 10_000
	}

	// Through the view, which is the only reader that sees a device's merged last_seen.
	rows, err := p.Deriver.DB.QueryContext(ctx, `
		SELECT project_seq, environment_seq, device_id
		FROM player_lifetime_v
		WHERE last_seen < ?
		ORDER BY last_seen
		LIMIT ?`, cutoff, maxDevices)
	if err != nil {
		return out, fmt.Errorf("find dormant devices: %w", err)
	}

	type tenant struct {
		project uint32
		env     uint16
	}
	groups := map[tenant][]uint64{}
	for rows.Next() {
		var t tenant
		var device uint64
		if err := rows.Scan(&t.project, &t.env, &device); err != nil {
			rows.Close()
			return out, err
		}
		groups[t] = append(groups[t], device)
		out.Dormant++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if out.Dormant == 0 {
		return out, nil
	}

	for t, devices := range groups {
		for start := 0; start < len(devices); start += chunk {
			end := min(start+chunk, len(devices))
			// Deleted by identifier alone, with no last_seen predicate.
			//
			// That is deliberate. A predicate on last_seen would read the stored partial
			// value — the very thing the view exists to avoid — so the decision is made
			// once, here, against merged truth, and the statement only carries out what
			// was decided.
			if _, err := p.Deriver.DB.ExecContext(ctx, `
				ALTER TABLE player_lifetime
				DELETE WHERE project_seq = ? AND environment_seq = ?
				  AND device_id IN (`+placeholders(end-start)+`)
				SETTINGS mutations_sync = 2`,
				pruneArgs(t.project, t.env, devices[start:end])...); err != nil {
				return out, fmt.Errorf("prune %d devices: %w", end-start, err)
			}
			out.Deleted += end - start
		}
	}
	return out, nil
}

func pruneArgs(project uint32, env uint16, devices []uint64) []any {
	out := make([]any, 0, 2+len(devices))
	out = append(out, project, env)
	for _, d := range devices {
		out = append(out, d)
	}
	return out
}
