package cohort

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Backfill repairs device-days whose cohort key the writer could not stamp (§65, §79).
//
// §65 promises that losing Valkey costs speed rather than data — "the nightly derivation
// resolves them from player_lifetime, which is the authority in any case". That sentence
// appears in four places in this repository and, until now, described nothing. The
// derivation did the opposite: `WHERE cohort_date != toDate(0)` **drops** an unstamped
// device-day rather than resolving it.
//
// The shape of the failure is worth stating, because it is silent and it points the wrong
// way. `cohort_sizes` is derived from `player_lifetime`, whose `first_seen` is
// `min(ingest_time)` and never needed Valkey at all — so a player whose activity rows were
// dropped is still counted in the denominator. The numerator loses them and the denominator
// keeps them, and retention reads **low**. Nothing errors, and the number is plausible.
//
// ## Why this is allowed to look up a device
//
// §29.1 forbids a *cohort query* from joining on an actor-cardinality key, and §32.1 makes
// that a property of the compiler rather than a rule it consults. This is neither: it is a
// repair job, and §32.1 explicitly contemplates one — a question that needs such a join may
// be "accepted as an asynchronous job (§34) that runs under its own resource budget where a
// spill costs latency rather than the ingestion path".
//
// It is also bounded by construction rather than by permission. The set it resolves is the
// devices that were active while Valkey could not answer, which is normally **empty** and at
// worst a few hours of one cell's actives. It is not one row per player; it is one row per
// player the outage touched. Every run reports the count, so "normally empty" stays an
// observation rather than an assumption.
type Backfill struct {
	Deriver *Deriver

	// MaxDevices bounds how many devices one run will resolve. An outage larger than this
	// is repaired over several nights, oldest arrival first.
	MaxDevices int

	// ChunkSize is how many device identifiers go into one mutation's IN list.
	//
	// A literal list rather than a subquery, because ClickHouse refuses a subquery in a
	// mutation outright — "ALTER UPDATE/ALTER DELETE statement with subquery may be
	// nondeterministic" — and it is right to: a mutation is evaluated per part, so a
	// subquery could see different rows for different parts and leave the table in a state
	// no single query would have produced. Chunking keeps the statement bounded without
	// reaching for allow_nondeterministic_mutations.
	ChunkSize int
}

// Result reports what a run found and fixed.
type Result struct {
	// DeviceDays is how many rows carried the sentinel before the run.
	DeviceDays int
	// Cohorts is how many distinct arrival days were repaired, and Devices how many
	// devices those days held.
	Cohorts int
	Devices int
	// Remaining is how many rows still carry it afterwards.
	//
	// Not necessarily zero, and not necessarily a bug. A device whose first sighting was
	// itself unstamped has no row in player_lifetime to resolve against, so there is
	// nothing to read — the number says how many players that is rather than pretending
	// the repair was total.
	Remaining int
}

// Run repairs one activity month.
//
// Idempotent, like everything else the scheduler runs: a device-day it has repaired no
// longer matches the sentinel, so a second run finds nothing. It must run **before** the
// derivation that reads the month, which is what makes the repaired rows count — the
// scheduler orders it that way.
func (b *Backfill) Run(ctx context.Context, m Month) (Result, error) {
	part := m.Partition()
	var out Result

	before, err := b.unstamped(ctx, part)
	if err != nil {
		return out, err
	}
	out.DeviceDays = before
	if before == 0 {
		return out, nil
	}

	// Resolve each affected device to the day it actually arrived.
	//
	// player_lifetime_v is the authority: its cohort_date is toDate(min(first_seen)), and
	// first_seen is min(ingest_time) — server-assigned, which is precisely what §29.4 says
	// a cohort day is and exactly what the writer would have stamped had Valkey answered.
	maxDevices := b.MaxDevices
	if maxDevices <= 0 {
		maxDevices = 500_000
	}
	chunk := b.ChunkSize
	if chunk <= 0 {
		chunk = 10_000
	}

	rows, err := b.Deriver.DB.QueryContext(ctx, `
		SELECT project_seq, environment_seq, cohort_date, device_id
		FROM player_lifetime_v
		WHERE (project_seq, environment_seq, device_id) IN (
		    SELECT project_seq, environment_seq, device_id
		    FROM player_activity_daily
		    WHERE toYYYYMM(activity_date) = ? AND cohort_date = toDate(0)
		)
		ORDER BY cohort_date
		LIMIT ?`, part, maxDevices)
	if err != nil {
		return out, fmt.Errorf("resolve cohort days for %s: %w", m, err)
	}

	type target struct {
		project uint32
		env     uint16
		cohort  time.Time
	}
	groups := map[target][]uint64{}
	for rows.Next() {
		var t target
		var device uint64
		if err := rows.Scan(&t.project, &t.env, &t.cohort, &device); err != nil {
			rows.Close()
			return out, err
		}
		groups[t] = append(groups[t], device)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}

	for t, devices := range groups {
		if t.cohort.IsZero() {
			continue
		}
		for start := 0; start < len(devices); start += chunk {
			end := min(start+chunk, len(devices))
			// A mutation, which ADR 0003 rejects as a *publication* mechanism and which is
			// the right tool for a repair: it corrects rows in place rather than deciding
			// what a reader sees, it is rare, and mutations_sync = 2 means the derivation
			// that follows reads the repaired values rather than racing them.
			if _, err := b.Deriver.DB.ExecContext(ctx, `
				ALTER TABLE player_activity_daily
				UPDATE cohort_date = ?
				WHERE toYYYYMM(activity_date) = ?
				  AND cohort_date = toDate(0)
				  AND project_seq = ?
				  AND environment_seq = ?
				  AND device_id IN (`+placeholders(end-start)+`)
				SETTINGS mutations_sync = 2`,
				args(t.cohort, part, t.project, t.env, devices[start:end])...); err != nil {
				return out, fmt.Errorf("repair %d devices in cohort %s of %s: %w",
					end-start, t.cohort.Format(time.DateOnly), m, err)
			}
			out.Devices += end - start
		}
		out.Cohorts++
	}

	if out.Remaining, err = b.unstamped(ctx, part); err != nil {
		return out, err
	}
	return out, nil
}

// placeholders renders an IN list of the given length.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// args flattens the mutation's parameters in statement order.
func args(cohort time.Time, part string, project uint32, env uint16, devices []uint64) []any {
	out := make([]any, 0, 4+len(devices))
	out = append(out, cohort, part, project, env)
	for _, d := range devices {
		out = append(out, d)
	}
	return out
}

// unstamped counts the device-days still carrying the sentinel.
func (b *Backfill) unstamped(ctx context.Context, part string) (int, error) {
	var n uint64
	if err := b.Deriver.DB.QueryRowContext(ctx, `
		SELECT count()
		FROM player_activity_daily
		WHERE toYYYYMM(activity_date) = ? AND cohort_date = toDate(0)`, part).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unstamped device-days in %s: %w", part, err)
	}
	return int(n), nil
}
