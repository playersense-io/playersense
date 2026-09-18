package facts

import (
	"context"
	"database/sql"
	"fmt"
)

// Source is where the answers actually live.
//
// The authority is ClickHouse. A cache sits in front of it, but the cache is an optimisation
// and this is the truth, so the two are separate types rather than one with a flag.
type Source interface {
	Lookup(ctx context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool, error)
}

// ClickHouse reads one player's history from the lifetime aggregates.
//
// # Why this is a point lookup and not a scan
//
// Both tables are `ORDER BY (project_seq, environment_seq, <surrogate>)`, so a query naming
// all three is answered from the sparse primary index: ClickHouse prunes to a granule and
// reads it. **Measured on this machine at 5,000,000 rows: 2–3 ms.**
//
// That number is the whole architecture. An earlier version of this package preloaded every
// active player into Valkey overnight precisely because a columnar point lookup was assumed
// to be the wrong shape — which would have cost 19 GB of memory at 50M monthly actives to
// serve a population most of whom never ask for a config. Measuring it turned the design
// inside out: the cache now holds only players who actually requested something, which is
// daily-active rather than monthly-active and about a tenth of the memory.
type ClickHouse struct {
	pool Pool
}

// Pool is however the caller gets a connection, asked for per query rather than held.
//
// An interface rather than a *sql.DB because a service builds its dependency graph before it
// dials anything: the pool does not exist until the application starts, and a source
// constructed with a nil one would be a nil dereference on the first player who asked for a
// config. Asking per lookup costs a method call and removes the ordering problem entirely.
type Pool interface {
	DB() (*sql.DB, error)
}

func NewClickHouse(pool Pool) *ClickHouse { return &ClickHouse{pool: pool} }

// Lookup reads one subject's record.
//
// `identifier` is the installation id for SubjectInstall and the studio's player id for
// SubjectPlayer.
//
// The surrogate is computed **by ClickHouse**, not in Go. `device_id` is
// `sipHash64(project_seq, environment_seq, toString(install_id))`, and reimplementing that
// hash here would be a second definition of a key that has to agree exactly or read another
// player's history. Passing the components and letting the database hash them keeps one
// definition, and the expression is constant so the sort key still prunes.
func (c *ClickHouse) Lookup(ctx context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool, error) {
	var (
		query   string
		surface string
	)
	switch subject {
	case SubjectInstall:
		surface = "player_lifetime_v"
		query = `
			SELECT
				revenue_micros,
				toInt64(purchases),
				dateDiff('day', first_purchase_at, now64(3)),
				dateDiff('day', last_purchase_at,  now64(3)),
				toInt64(active_days),
				toInt64(sessions),
				toInt64(intDiv(playtime_ms, 1000)),
				dateDiff('day', last_seen,  now64(3)),
				dateDiff('day', first_seen, now64(3))
			FROM player_lifetime_v
			WHERE project_seq = ? AND environment_seq = ?
			  AND device_id = sipHash64(?, ?, ?)`
	case SubjectPlayer:
		surface = "account_lifetime_v"
		query = `
			SELECT
				revenue_micros,
				toInt64(purchases),
				dateDiff('day', first_purchase_at, now64(3)),
				dateDiff('day', last_purchase_at,  now64(3)),
				toInt64(active_days),
				toInt64(sessions),
				toInt64(intDiv(playtime_ms, 1000)),
				dateDiff('day', last_seen,  now64(3)),
				dateDiff('day', first_seen, now64(3))
			FROM account_lifetime_v
			WHERE project_seq = ? AND environment_seq = ?
			  AND account_id = sipHash64(?, ?, ?)`
	default:
		return Record{}, false, fmt.Errorf("unknown subject %q", subject)
	}

	var (
		record        Record
		firstPurchase sql.NullInt64
		lastPurchase  sql.NullInt64
	)
	db, err := c.pool.DB()
	if err != nil {
		return Record{}, false, err
	}

	err = db.QueryRowContext(ctx, query,
		projectSeq, environmentSeq, projectSeq, environmentSeq, identifier).
		Scan(
			&record.SpendMicros, &record.Purchases,
			&firstPurchase, &lastPurchase,
			&record.DaysActive, &record.Sessions, &record.PlaytimeSeconds,
			&record.DaysSinceSeen, &record.DaysSinceFirst,
		)
	switch {
	case err == sql.ErrNoRows:
		// Not an error. A player nobody has seen before has no history, which is an
		// answer rather than a failure — and it is the common case on a first session.
		return Record{}, false, nil
	case err != nil:
		return Record{}, false, fmt.Errorf("read %s: %w", surface, err)
	}

	// A player who has never bought has no purchase dates, and the record keeps that
	// distinction rather than flattening it to zero (ADR 0031).
	if firstPurchase.Valid {
		days := firstPurchase.Int64
		record.DaysSinceFirstPurchase = &days
	}
	if lastPurchase.Valid {
		days := lastPurchase.Int64
		record.DaysSinceLastPurchase = &days
	}
	return record, true, nil
}
