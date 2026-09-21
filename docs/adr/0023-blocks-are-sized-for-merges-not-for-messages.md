# 0023. A block is sized for the merges, not for the message

**Status:** Accepted
**Date:** 2026-09-16
**Supersedes part of:** [0018](0018-writer-acknowledgement-and-deduplication.md)
**Relates to:** project.md §4, §20, §21, §25, §29.14, §86, Appendix C.2

> **Amended by [0038](0038-one-api-one-database.md).** The block token no longer includes
> a cell. It is still the partition, the chunk index and the sorted dedup keys, which is
> what makes it stable across redeliveries.

## Context

ADR 0018 gave every message its own `insert_deduplication_token` and inserted it under
`async_insert=1, wait_for_async_insert=1`. The reasoning was sound and the arithmetic was
never done.

`insert_deduplication_token` is part of an async insert's **grouping key**. Two inserts
carrying different tokens are never coalesced server-side, so `async_insert` — whose entire
purpose is to turn many small inserts into few parts — does nothing at all while the token
varies per insert. The setting bought the fragmentation and none of the benefit.

Measured on the local stack, 200 concurrent inserts of one row each:

| | parts created |
|---|---:|
| a distinct `insert_deduplication_token` per insert | **200** |
| no token | **9** |

So the shape was one part per SDK batch. §4 targets a billion accepted events a day; at
five hundred events a batch that is two million messages a day, landing in a handful of
daily partitions, against ClickHouse's default `parts_to_throw_insert` of three thousand.
Merges fall permanently behind, and then the server stops accepting inserts — the whole
node throttles, which is the failure the §25 insert strategy exists to prevent.

This was invisible in development because it only appears at volume, and invisible in the
tests because a test inserts a handful of batches and never reaches a merge limit.

## Decision

**A block is assembled from as many messages as the delivery holds, and named by what is
inside it.**

Three parts, in the two places that had to change.

**The writer coalesces across messages.** A delivery's rows are grouped by ClickHouse's own
partition key — `(retention_tier, toDate(event_time))` — and each group is written as one
block, split only when it would exceed `WRITER_BATCH_SIZE` (raised to 100,000 rows).
Grouping on the partition key rather than on arrival is what makes one insert one part: an
insert spanning two partitions writes a part in each.

**The queue hands over groups, sized in rows.** Pub/Sub's `Receive` delivers one message per
callback, so the writer had nothing to coalesce no matter what it did. A callback now parks
its message and returns; the group goes to the handler on whichever comes first —
`BUS_DELIVERY_MAX_ROWS` (100,000), `BUS_DELIVERY_MAX_MESSAGES` (1,000) or
`BUS_DELIVERY_MAX_WAIT` (2s). Messages are acknowledged only after the group's handler
returns nil, so Appendix C.2 is unchanged.

Rows rather than messages, because a message is not a size: §19.1 lets an SDK batch carry
one event or a thousand, so two hundred messages is two hundred rows from one integration
and two hundred thousand from another. The message cap is a memory bound and the backstop
for a producer that reported no count; the event count therefore travels on the message, so
grouping never has to decode a payload to learn how big it is.

**These three are what set the insert rate, not `WRITER_BATCH_SIZE`.** That is a ceiling
applied to whatever a delivery already contains — it splits a large delivery and never makes
a small one wait. A delivery of 120 rows is inserted as 120 rows.

**The token is derived from the contributing batches**, not from one of them: a digest of
the cell, the partition, the chunk index and the sorted dedup keys in the block. A
redelivery of the same messages produces the same token and ClickHouse discards the repeat.

`async_insert` is dropped. With the block already assembled in the writer there is nothing
left for the server to coalesce, and a plain synchronous insert is strictly stronger on
durability — it returns when the block is written, with no server-side buffer a restart
could lose. Appendix C.2 is then satisfied by construction rather than by remembering a
second setting.

**A delivery that fails is retried, isolated, then parked.** Coalescing raises the stakes
on failure: one unstorable message now fails a delivery of hundreds, so a failure path that
was already wrong became load-bearing. A delivery is retried whole `WRITER_MAX_ATTEMPTS`
times (5); after that the writer stores its messages one at a time, and a message that still
fails is dead-lettered — **but only after probing ClickHouse and finding it healthy**.

The probe is the part that matters. Attempt counting alone cannot tell a poisoned message
from an unavailable database, and a writer that parks on attempt count would move an entire
backlog to the dead-letter topic during an outage and call it poison, turning a recoverable
incident into a data-loss one. Asking the database whether it is answering is what makes
"this data is unstorable" a fact rather than an inference.

Two bugs were found on the way and are fixed here rather than separately, because both are
this path:

- **A handler error used to end the local subscription outright.** `Subscribe` returned the
  error, the writer logged "the subscription stopped", and the process then sat there
  consuming nothing while its health probe kept answering. One failed insert killed a writer
  until somebody restarted it. A failed delivery is now retried with a widening backoff and
  the subscription never returns except on cancellation.
- **§20's `analytics-events-dlq` topic had nothing writing to it.** `Bus` gains `DeadLetter`;
  the local bus parks messages in a `.dead/` spool beside the live one and Pub/Sub publishes
  to the topic §20 already named.

Pub/Sub's own dead-letter policy is deliberately not the mechanism. It moves a message after
a fixed number of delivery attempts, which is the right behaviour for a consumer that keeps
crashing and the wrong one here, for the same reason attempt counting is: it cannot see the
difference the probe can. It remains worth configuring generously as a backstop.

## Consequences

The one that must be said plainly, because it is a real weakening rather than a free win:

> **Deduplication is now per delivery, not per batch.** A redelivery whose *membership*
> differs from the original — a different set of messages grouped together — produces a
> different token and inserts again.

The dominant failure is unaffected. A writer that crashes between the insert and the
acknowledgement replays the whole group, because nothing in it was acknowledged: same
messages, same token, discarded. That is the case ADR 0018 was written for and it still
works. What is no longer covered is a redelivery that arrives interleaved with new
messages, which Pub/Sub permits and an offset-based bus does not.

Block-level deduplication is also the **only** mechanism available here, which is worth
stating because it looks like there are alternatives. A materialized view fires on the
inserted block, so anything that removes duplicates after the fact — a `ReplacingMergeTree`
on `event_id`, a dedupe on read — leaves `player_lifetime` and `player_activity_daily`
already inflated. The duplicate has to be rejected before the insert completes or not at
all.

Two consequences that are improvements:

- **The deduplication window gets longer in wall-clock terms.** ClickHouse remembers a
  fixed number of recent block tokens, so a hundred blocks covered about four seconds at two
  million parts a day and covers minutes at ten thousand.
- **Memory becomes a bound worth watching.** A block is assembled in the writer before it is
  sent, so `WRITER_BATCH_SIZE` times the average row size is what one insert costs in RAM.
  That is the trade for the merge pressure, and it is the direction to trade in: RAM is a
  number you can provision and a merge backlog is not.

The cost on a quiet cell is `BUS_DELIVERY_MAX_WAIT` of added latency before a partly-filled
group is handed over, and §80 budgets event visibility at ten seconds p95, so two is inside
it.

The arithmetic is worth writing down, because the wait is what actually binds at scale and
it is easy to assume the row target does. At §4's ceiling — a billion events a day, about
11,500 a second — a 100,000 row target is roughly nine seconds away, so a two-second timer
fires first and produces about 23,000 rows an insert and 43,000 inserts a day. That is close
to ClickHouse's own guidance of an insert a second per table, with blocks an order of
magnitude above the point where merge pressure becomes the constraint. Raising the wait
buys bigger blocks and spends visibility; lowering it does the reverse. Neither is a number
to guess at once real traffic exists, which is why all three are settings.

## Alternatives considered

**Keep a token per message and accept the parts.** This is the status quo and it is the
thing that does not work. Two million parts a day is not a tuning problem.

**Keep `async_insert` and drop the token.** Coalescing returns and deduplication goes
entirely, so every redelivery — which §21 says to expect — double-counts every number the
product sells. Worse than the problem.

**Group several messages but keep a token per message.** Not expressible: one token applies
to one insert block.

**Deduplicate by querying for the batch id before inserting.** A read per insert against the
table being written, to answer a question the engine answers for free, and it races with
concurrent writers.

**A durable write-ahead record of each group, so a replay reproduces its membership
exactly.** This would restore the per-batch guarantee. It is a second durable store on the
hot path, which §12 refuses for Valkey and ADR 0022 refuses for the archive's dedup for the
same reason. Revisit if partial redelivery is ever observed to matter.

## Enforcement

- `apps/backend/internal/writer/writer.go` — `plan` groups by `blockKey`, `blockToken`
  names the block, `insert` writes rows from many messages.
- `apps/backend/internal/eventbus/pubsub.go` — `grouper` parks messages and settles the
  group together.
- `internal/eventbus/grouper_test.go` — `TestAGroupIsSizedInRows` (the row target binds
  before the message cap), `TestAPartlyFilledGroupIsNotHeld` (a small group is handed over
  rather than waiting for a target it will never reach), `TestAGroupWithoutRowCountsStillHandsOver`
  (the message cap is a real backstop), `TestAnIdleGroupProducesNothing`.
- `cmd/analytics-writer/main_test.go:TestADeliveryBecomesOneBlockPerPartition` — 40 batches
  published before the writer starts, asserted to become one insert rather than forty.
- `…:TestABlockNeverSpansTwoPartitions` — rows spanning two days become two blocks, so the
  coalescing does not trade one source of small parts for another.
- `…:TestARedeliveredBatchLandsOnce` — unchanged from 0018 and still passing: the offset is
  rewound, the same delivery replays, and the row count does not move.
- `…:TestAPoisonedMessageIsParkedAndTheQueueMovesOn` — a batch with an unparseable
  `event_id` sits between two healthy ones; both healthy batches land, the poisoned one is
  in the dead-letter spool with its payload intact.
- `…:TestAnOutageDoesNotDeadLetterTheBacklog` — ClickHouse is closed underneath a running
  writer and nothing is parked. Verified to have teeth by removing the probe, which parks
  all five.
