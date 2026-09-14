# 0018. The writer acknowledges after the insert, and splits deterministically

**Status:** Accepted
**Date:** 2026-09-14
**Relates to:** project.md §10.3, §21, §25, §86, Appendix C.2

## Context

ADR 0013 put a queue between ingestion and storage, which moves the interesting problem
rather than solving it. Delivery is at-least-once (§21), so the consumer decides what
"stored" means, and two mistakes are available:

- **Acknowledge before the insert lands.** The message is gone, the rows are not there, and
  the `202` promising otherwise is already in the client's log. This is the failure nobody
  notices until a customer asks why Tuesday is missing.
- **Let a redelivery double the rows.** Then every number the product sells is inflated by
  however many times a network hiccup happened, and there is no way to tell which.

## Decision

**Acknowledge only after the durable insert completes.** The handler returns nil only once
ClickHouse has the block; the bus advances its position only on a nil return. Shutdown
waits for the message in flight rather than cutting it off.

**Insert with `insert_deduplication_token`** set from the message's own dedup key, under
`async_insert=1, wait_for_async_insert=1`. The engine remembers recent block tokens and
discards a repeat, which works because `events_raw` is a `ReplicatedMergeTree` — the reason
§23 chose that engine for a single-node deployment in the first place.

**Split a large message deterministically**: `key#0`, `key#1`, … so a redelivery produces
the same blocks with the same tokens.

## Consequences

- The deduplication window is ClickHouse's, not infinite. A redelivery hours later inserts
  again. That is the correct trade: the alternative is a permanent index of every batch
  ever seen, which is a second database to keep consistent with the first.
- `wait_for_async_insert=1` is not optional. Without the wait, `async_insert` buffers in the
  **server**, so a ClickHouse restart loses rows this writer has already acknowledged —
  which is the first mistake above, reintroduced by a setting.
- The token is per message, which means the batch size setting is a ceiling on rows per
  block rather than a target. A message is already one SDK batch of up to a thousand
  events, so §86 is satisfied without grouping messages.

### The trap in the split

A token reused across two different blocks makes ClickHouse discard the second as a
duplicate of the first. That is a **silent loss**, which is worse than the duplicate it was
meant to prevent — so the index is part of the token, and it is derived from position
rather than from a counter that could restart differently.

## Alternatives considered

**Group several messages into one insert with a combined token.** Fewer round trips, and
wrong: at-least-once delivery can redeliver one message of the group alone, producing a
different token and inserting it again.

**Deduplicate by querying for the batch id before inserting.** A read per insert, against
the table being written, to answer a question the engine already answers for free.

**Exactly-once delivery.** Not a thing the queue provides, and §21 says so explicitly: the
design makes the observable result idempotent rather than claiming the delivery is unique.

## Enforcement

- `apps/backend/internal/writer/writer.go` — the settings, the token, the split.
- `apps/backend/internal/eventbus/local.go` — the offset committed after the handler, via a
  temporary file and a rename, so a crash leaves the previous offset rather than a
  truncated one.
- `cmd/analytics-writer/main_test.go:TestARedeliveredBatchLandsOnce` — rewinds the offset,
  which is exactly what a crash between the insert and the acknowledgement leaves behind,
  and asserts the row count does not move. Confirmed to have teeth by perturbing the token,
  which made it double.
