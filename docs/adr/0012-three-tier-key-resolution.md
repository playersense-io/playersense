# 0012. Keys resolve through three tiers and fail closed

**Status:** Accepted
**Date:** 2026-09-13
**Relates to:** project.md §3.2, §64, §65

## Context

Three sentences in the architecture constrain this, and each rules out an otherwise
reasonable design:

- **§3.2** forbids the ingestion path from querying the control plane per request.
- **§64** requires ingestion to keep accepting batches through a PostgreSQL outage.
- **§65** requires losing Valkey to cost speed rather than data.

Together they say: the authority must not be on the request path, the cache must not be the
only thing between the request and the authority, and neither being gone may turn a valid
request into a wrong answer.

## Decision

Three tiers, each covering the failure of the one below it:

1. **In-process cache**, a few seconds. Survives both Valkey and PostgreSQL being
   unreachable.
2. **Valkey**, minutes. Shared by every instance, so a key verified once is verified for
   the fleet.
3. **PostgreSQL plus one Argon2id comparison** (0011). The authority.

Rejections are cached too, with a shorter TTL, because a publishable key is assumed
discoverable and wrong keys arrive in volume — none of them should become a second query.

**And it fails closed.** When no tier can answer, the response is `503`, never a success.
An outage is reported as an outage; it is not remembered as a rejection, because an outage
cached as a rejection turns a blip into an hour of refused traffic.

## Consequences

- Revoking a key evicts it from Valkey immediately, but the in-process tier is not reachable
  from the control plane. Its TTL is therefore measured in **seconds**, and that window is
  the honest cost of tier 1 existing.
- A cached tenant carries everything a request needs — surrogates, UUIDs, policy tier,
  retention tier, cell — so that a warm path touches no database at all.
- The in-process map is keyed by something a client influences, so it is bounded and evicted.

## Alternatives considered

**Valkey only.** Fails §64: Valkey and PostgreSQL are both unreachable during a network
partition, and the gateway stops.

**Long-lived in-process cache only.** Fails §53: a revoked key stays usable for the
lifetime of the cache, across every instance, with no way to cut it short.

**Signed, self-describing tokens (JWT-style) needing no lookup.** Genuinely attractive, and
it makes revocation the hard problem instead — which for a credential embedded in shipped
game binaries is the wrong problem to make hard.

## Enforcement

- `apps/backend/internal/core/keyresolver.go` — the three tiers, the negative cache, and the
  distinction between `ErrUnknownKey` and an unreachable authority.
- `cmd/ingest-api/main_test.go:TestIngestionSurvivesAControlPlaneOutage` — closes the pools
  underneath a running application and asserts batches are still accepted.
- `…:TestAnUncachedKeyFailsClosedDuringAnOutage` — the other half: a key nothing has seen
  gets `503`, not `202`. Verified to have teeth by making the resolver fabricate a tenant,
  which made the test fail.
- `…:TestValkeyAnswersWhenPostgresIsDown` — the middle tier earning its place.
