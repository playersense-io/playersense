# 0013. Ingestion writes to a queue, never to ClickHouse

**Status:** Accepted
**Date:** 2026-09-12
**Relates to:** project.md §3.6, §19, §20, §21, Appendix C.1, C.2

> **Amended by [0038](0038-one-api-one-database.md).** The message no longer carries a
> cell, the dedup key is derived from project, environment and batch id, and the local
> spool is no longer namespaced by cell. Everything else here stands.

## Context

The gateway's job is to accept a batch and answer quickly. If it wrote to ClickHouse
directly, its availability would be ClickHouse's availability, its latency would include a
merge, and a restart would lose whatever it was holding.

ClickHouse also wants batches, not rows (Appendix C.5) — so something has to accumulate
them, and that something must not be the thing answering HTTP requests.

## Decision

Ingestion **publishes to a durable queue and returns**. A `202` means exactly one thing:
the batch reached durable queueing (Appendix C.1).

`eventbus.Bus` is an interface, so the ingestion path is coupled to §3.6's abstraction
rather than to a product. Two implementations:

- **Pub/Sub**, the deployment default.
- **Local**, a durable on-disk NDJSON spool, fsynced before the publish returns.

One message is one **batch**, not one event: at the document's ceiling that is ten million
messages a day instead of a billion, and the writer wants batches anyway.

The tenant travels **on the message** — surrogates, UUIDs, policy tier, retention tier,
cell — so the writer never queries the control plane. That is what lets storage keep
working through a control-plane outage, not merely ingestion (§64).

A consumer acknowledges **only after its durable side effect completes** (Appendix C.2). A
message acknowledged before ClickHouse has flushed is a batch that was accepted and lost.

## Consequences

- Delivery is at-least-once, so a redelivered batch must land once. The message carries a
  `DedupKey` derived from cell, project, environment and batch id, used as ClickHouse's
  `insert_deduplication_token` — which works because `events_raw` is a `ReplicatedMergeTree`.
- The local implementation is honest about what it is: durable on the publish side, with no
  subscription, acknowledgement or redelivery until the writer needs them. It exists because
  the Pub/Sub emulator needs a Java runtime, and an ingestion path that cannot be exercised
  locally is one nobody exercises.
- The spool is namespaced by cell, so a developer running two cells does not deliver one
  cell's batches to the other's writer.

## Alternatives considered

**Write to ClickHouse from the gateway with `async_insert`.** Couples gateway availability
to ClickHouse and gives up the retry buffer. `async_insert` buffers in the *server*, so a
ClickHouse restart loses what it held — and the gateway has already said `202`.

**Kafka.** More capability than the shape needs, and a cluster to operate. The interface
means this can change later without touching a handler.

**An in-memory queue with a background flush.** A process restart loses acknowledged
batches, which makes the `202` a lie.

## Enforcement

- `apps/backend/internal/eventbus/` — the interface and both implementations.
- `apps/backend/internal/handlers/ingest.go` — publishes and returns; a publish failure is a
  `503` the SDK retries, never a `202`.
- `cmd/ingest-api/main_test.go:TestAcceptedBatchReachesTheBus` asserts the message is on the
  bus by the time the response is written, not at some point afterwards.
