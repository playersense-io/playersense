# 0022. An archive object is named by what is inside it

**Status:** Accepted
**Date:** 2026-09-15
**Relates to:** project.md §10.4, §14.1, §20, §21, §66, Appendix C.2, C.6

## Context

The archive consumes an at-least-once stream (§21). The same batch will be delivered twice —
after a crash, after a redeploy, after any acknowledgement that did not land — and §21 lists
"idempotent archive object writing / manifest operations" among the mechanisms that make
that survivable.

An object named by a clock or a random id is not idempotent under redelivery: every delivery
produces a new object holding events that are already archived. Nothing errors. The archive
simply grows, §39 bills for the duplication, and §104's breach scoping starts answering a
larger number than the truth.

The obvious fix — check whether these events are already archived — is a lookup per batch
against something. The obvious place is the cell's Valkey, which §12 says must never be the
only copy of anything that cannot be reconstructed.

## Decision

**The object's name is a digest of the batches inside it.** A redelivery of the same content
produces the same path, the same bytes and an overwrite; the manifest insert conflicts on
its unique `(bucket, object_path)` index and does nothing.

**The manifest is written after the object, never before.** A row exists only if the bytes
do (Appendix C.2).

**Events are grouped per event, not per batch.** A batch straddling an hour is split, so an
object holds one project, one environment and one hour — §14.1's prefix exactly.

**No buffering across deliveries.** Rows are written before the handler returns nil.

**The path's first segment is the cell**, not the region. §14.1's example writes
`region=eu-1`, but `eu-1` is a cell and §6 — written later — separates the two. One bucket
belongs to one cell, so the cell is what distinguishes a prefix; the legal region is a
manifest column, where a query can use it.

## Consequences

- A whole redelivery is free. This is the common case: a crash between the object and the
  acknowledgement replays the same delivery.
- **A partial redelivery is not.** A different set of batches is a different digest and
  therefore a second object overlapping the first. The manifests make that visible as
  intersecting time ranges rather than hiding it, and a reader resolves it on `event_id`.
  §21 says to "design the observable result to be idempotent" rather than claim
  exactly-once, and this is where that sentence is doing work: the duplicate exists and the
  *answer* does not have to change. §34's cold-query path must deduplicate, which is listed
  as open rather than assumed.
- Light traffic produces small objects, because a delivery is the flush boundary. §10.4
  anticipates this ("compact small files later") and §10.7 owns the compaction.
- No Valkey dependency, and therefore no way for a cache outage to turn into either
  duplication or loss.

## Alternatives considered

**A Valkey set of archived dedup keys.** A lookup per batch, and §12 is explicit that Valkey
must not be the only copy of something unreconstructible. Losing it would silently reopen
the duplication this record closes, and the failure would be invisible.

**Buffer by project and hour, flush when the hour closes.** Produces well-sized objects and
the best names. It also means acknowledging messages whose effect is still in memory, which
Appendix C.2 forbids — a crash would lose events the queue believes were archived.

**Accept duplicates and deduplicate on read.** This is the fallback for the partial case
rather than a design for the common one. Making it the design would mean every reader pays
for every redelivery the system ever had.

**Record dedup keys in the manifest.** Durable and exact, and one row per batch per object
turns a manifest table into an event table.

## Enforcement

- `apps/backend/internal/archive/writer.go` — `digest` and `objectPath`; the manifest write
  follows the object write and uses `OnConflictColumns(...).DoNothing()`.
- `apps/backend/ent/schema/regionaltier/regional.go` — the unique index that makes the
  conflict possible.
- `TestARedeliveredBatchArchivesOnce` rewinds the archive's own offset — what a crash between
  the object and the acknowledgement leaves — and asserts one object and one manifest.
  Verified to have teeth by making the name non-deterministic, which produces two.
- `TestEachHourIsItsOwnObject` asserts a batch spanning an hour boundary becomes two objects.
- `TestTheArchiveHasItsOwnPosition` asserts the fan-out: the archive commits its own offset
  and none outside a group.
