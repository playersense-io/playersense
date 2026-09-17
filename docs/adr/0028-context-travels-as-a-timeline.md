# 0028. Context travels as a timeline and is materialized at ingestion

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §17, §19, §22, §26, §29.1, §29.3, §32.1, §39; [ADR 0026](0026-compressed-json-and-derived-event-ids.md), [ADR 0027](0027-targeting-is-evaluated-on-the-server.md); docs/sdk/context-and-targeting.md

## Context

[ADR 0027](0027-targeting-is-evaluated-on-the-server.md) gave studios a vocabulary for their
own game — `context.rank`, `context.progress.level`, `context.game_mode` — and used it for
targeting. It left analytics out, which made context strangely isolated: a studio could
target on rank and could not break a chart down by it.

Closing that gap runs straight into §29.1, which forbids a cohort query from joining on a
player key, and §32.1, which enforces that by giving the query compiler no join to offer. So
"look up what the player's rank was at the time" is not available, by construction and on
purpose.

Four options were considered, and the measurements below decided between them.

## Decision

**Context travels as a timeline inside the immutable batch, and the gateway materializes it
onto every event before storage.**

```json
{ "context": [ { "at": 0, "values": { "rank": "gold", "chapter": 4 } },
               { "at": 2, "set":    { "rank": "diamond" } } ],
  "events":  [ …, …, …, … ] }
```

**Frame 0 always carries a full snapshot.** A batch is therefore self-contained: the gateway
never has to remember where the previous batch left the context. That preserves the property
deduplication and retries already rest on — an immutable batch is a complete transport unit
(§21, ADR 0026) — and it means a redelivery reconstructs exactly what the first delivery did.

**`at` is the event's index in the batch as sent.** See Consequences; this is the rule that
would corrupt data if it were wrong.

**Application order within a frame is fixed:** `values` replaces, then `set` applies, then
`unset` removes.

**Expanded keys are prefixed `ctx.`** in the event's property maps, and an event's own
property wins a collision. A property describes the occurrence and is the more specific
statement; the prefix means the collision is all but impossible anyway.

**The billing meter is computed before expansion.** §39 bills on normalized logical bytes, and
the studio sent the context once for the batch.

**Event-time context and cohort context stay different dimensions.** `ctx.rank` is what was
true when the event happened. A cohort dimension freezes at cohort creation and never moves —
§29.3's sticky-dimension model extended to studio keys, writing into the already-denormalized
cohort fact. One field answering both questions is where this would get messy: "D7 retention
by rank" means the rank they *entered* with, and "sessions by rank" means the rank they had at
the time, and silently making one behave like the other would be worse than refusing either.

## The measurement

`cmd/wiresize`, 500 events, four context values, zstd:

| | raw | compressed | per event |
|---|---|---|---|
| no context | 119,863 | 3,954 | 7.9 B |
| **a timeline** | **119,964** | **4,036** | **8.1 B** |
| a copy on every event | 154,363 | 4,096 | 8.2 B |

**The compressed column nearly refutes the obvious argument.** Duplicating context across
every event costs 0.1 bytes an event on the wire, because zstd eats the repetition — the same
effect that makes a repeated `player_id` almost free and an `event_id` expensive (ADR 0026).
Anyone reaching for the timeline to save bandwidth is reaching for it for the wrong reason.

The **raw** column is the argument: **29% more logical bytes**. §39 bills on normalized
logical size, not on compressed size, so duplication would charge a studio 29% more for
something they transmitted once. Two supporting reasons: the gateway parses one frame rather
than 250 copies, and "the context at event N" is stated rather than inferred from repetition.

## Consequences

- **`at` must resolve against the event's index in the batch as sent, not against the
  surviving events.** The gateway drops malformed events per-event (§19), so resolving against
  the output would hand every event after a dropped one somebody else's context. That is a
  corruption rather than a loss, and nothing anywhere would report it.
  `TestADroppedEventDoesNotShiftTheContext` exists for exactly this, and was verified to fail
  when the index is taken from the output.
- **A context change applies to future events only, never to queued ones.** An event tracked
  while the player was gold stays gold even when the change lands in the same batch. The SDK
  therefore records the context as it stood at the *previous* seal, so frame 0 is a true
  snapshot whichever of the flush triggers fired. This is the same principle that already lets
  one batch straddle a sign-in.
- **The timeline must be ascending**, because it is resolved in one pass over the events. A
  frame out of order would silently never be applied, so the batch is refused instead.
- **Expanded context lands in the `props_*` Map columns**, which are cheap to store and
  expensive to `GROUP BY` — a map lookup per row. §26 already says raw scans are the fallback
  rather than the dashboard path, so a context key that is to be a *dashboard dimension* needs
  a real column in the rollups, and **each one multiplies that rollup's rows by its
  cardinality**. `events_1m` is about 72k rows per project per day today; `rank(5) × mode(3)`
  makes it 1.08M.
- **Therefore context keys are opt-in per use**, and the budget is §29.13's. A key can be an
  event dimension, a targeting attribute, a cohort dimension, or any combination, and a
  cardinality check must be able to refuse `player_id` as a cohort dimension.
- Context is bounded scalars — string, integer, float, boolean — and never arbitrary JSON.
  That restriction belongs to context and not to a LiveOps payload, which is deliberately
  opaque: somebody attaching a 70 KB inventory object to their context would destroy both the
  transport and the analytics cardinality.
- Server-derived attributes reach the row by the same mechanism, from the other side: the
  gateway resolves `geo.*` and `network.*` from the request address and can materialize them
  onto the event exactly as it does the studio's context — and the client never transmitted
  them, so it cannot forge them (ADR 0027).

## Alternatives considered

**No context on events.** Cheapest, and it leaves the feature half-built: a studio can target
on rank and cannot chart by it.

**A copy on every event.** Measured above: 0.1 bytes an event on the wire and 29% on the
bill. It also restates unchanged state on every event, which is a lot of bytes to say
something that did not change.

**A separate `context_changed` analytics event**, giving ClickHouse a per-device context
timeline. Cheap to write and expensive to read: reconstructing a breakdown from it is a join
on a player key, which §29.1 forbids and §32.1's compiler structurally cannot express. It
would have required abandoning the no-join rule to use the data it produced.

**Materializing at query time instead of at ingestion.** Same problem one layer later, and it
would make every dashboard query pay for it.

## Enforcement

- `sdk/core/src/context.rs` — `Frame`, `diff`.
- `sdk/core/src/client.rs` — `note_context_change` and `context_timeline`: the base/delta
  split, and the rule that a change never reaches back over queued events.
- `apps/backend/internal/protocol/envelope.go` — `ContextFrame`, and the documented
  application order.
- `apps/backend/internal/ingest/normalize.go` — `checkContext`, `applyFrame`, `applyContext`,
  and the metering that happens before expansion.
- `a_batch_carries_a_context_snapshot_and_its_deltas` walks the worked example end to end.
- `a_context_change_does_not_reach_back_over_queued_events` and
  `every_batch_opens_with_a_complete_snapshot` hold the two properties the design rests on.
- `TestADroppedEventDoesNotShiftTheContext` — verified to have teeth by resolving the index
  from the output, which mis-attributes the surviving event.
- `TestTheBillingMeterCountsWhatTheStudioSent` asserts `payload_bytes` is identical with and
  without context.
- `cmd/wiresize` reproduces the table above.
