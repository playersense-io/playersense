// Package tombstone names the things that must stop being accepted (project.md §61, §06).
//
// Two of them, and they work the same way for different reasons.
//
// A **project** tombstone is written when a customer's project is deleted. A **actor**
// tombstone is written when a data subject asks to be erased. In both cases the data stops
// being *used* the moment the tombstone exists, hours before it stops *existing*: removal
// from ClickHouse is a rewrite of whole parts and removal from the archive is a rewrite of
// whole objects, and neither is something a request can wait on (§05).
//
// # The fast path is not the guarantee
//
// A tombstone lives in the cell's Valkey and therefore inherits Valkey's availability. If
// the cache cannot answer, the writer stores the batch and the asynchronous purge removes
// it: the purge is the guarantee and this is a fast path. Making the fast path the guarantee
// would mean a cache outage could silently resurrect deleted data, which is the wrong
// failure to choose (§65, §06).
//
// It follows that a tombstone has a **lease**, not a lifetime. It is refreshed by the worker
// for as long as the request that raised it is open, and it lapses on its own if the worker
// stops — because a stale tombstone that nothing is maintaining would drop a living player's
// events forever, and nothing would say why.
//
// # Why the keys are not hashed
//
// They carry a project identifier and an actor identifier in the clear. That is deliberate:
// the purge has to match the same value in ClickHouse and in the archive, so a hash here
// would have to be a hash everywhere. Valkey is cell-local — one per cell, in that cell's
// region (§6, §99) — so an identifier in it has not left the region that produced it, which
// is the property that actually matters.
package tombstone

import (
	"context"
	"time"

	"playersense.dev/backend/internal/valkey"
)

// Project names a project whose data has been deleted (§61).
func Project(cache *valkey.Client, projectID string) string {
	return cache.Key("project", "tombstoned", projectID)
}

// Actor names one erased data subject within a project (§61, §101).
//
// Scoped by project, because an actor key is the studio's own identifier and means nothing
// outside the project that issued it. Two studios can use `player-1` for two different
// people, and an erasure for one of them must not silence the other.
func Actor(cache *valkey.Client, projectID, actorKey string) string {
	return cache.Key("actor", "tombstoned", projectID, actorKey)
}

// Raise writes an actor tombstone, or renews one that already exists.
//
// The value carries nothing; presence is the whole message. Called by the surface that
// accepts the request — so the data stops being accepted in the same round trip the studio
// submitted it in — and again by the worker on every pass, which is what turns the TTL into
// a lease.
//
// It is raised **after** the request is recorded, never before. The two failure modes are not
// symmetrical: a record with no tombstone repairs itself on the worker's next pass, while a
// tombstone with no record drops a living player's events with nothing anywhere to say why.
func Raise(ctx context.Context, cache *valkey.Client, projectID, actorKey string, ttl time.Duration) error {
	return cache.Set(ctx, Actor(cache, projectID, actorKey), []byte("erased"), ttl)
}
