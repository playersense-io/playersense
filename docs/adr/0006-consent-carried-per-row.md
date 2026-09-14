# 0006. Consent is stored on every event, not looked up at query time

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §17.2, §23, §97

## Context

Consent is the lawful basis for holding a row. The cheap implementation records it once per
player in PostgreSQL and joins or filters at read time.

That implementation has a property nobody notices until it matters: **consent cannot be
reconstructed after the fact.** Nothing in a stored event reveals what the player was shown
or agreed to. A row written without a basis can never acquire one later, and a mutable
per-player flag says only what is true *now* — which is the wrong question when the question
is "was this row lawfully collected in March".

## Decision

Consent reaches the gateway in the batch envelope and is **stamped onto every event as
columns**: whether analytics consent was granted, the policy version in force, and the ATT
status as reported.

A batch without analytics consent is accepted and discarded. Accepted, because an SDK that
retries a rejected batch forever is worse for everyone; discarded, because there is no
basis to store it. The response says plainly that nothing was stored.

## Consequences

- **Lawful basis is provable for any row**, including rows recovered from cold archive years
  later and rows reprocessed into a new schema.
- **Withdrawal is scopeable.** "Erase everything collected for this actor under policy
  version N or earlier" is a bounded, executable statement. Without the column, the only
  honest reading of a withdrawal is "erase everything" — more destructive than the player
  asked for, and harder to prove was done.
- **A policy change is visible in the data.** When terms are revised, the version on the row
  says which text applied.
- A few bytes per event, on the largest table in the system. This is the cost, and it is
  paid deliberately.

`att_status` is recorded rather than acted on, for the reason 0004 gives: PlayerSense does
not track, so ATT does not gate its collection. It is stored because a studio may need to
answer a question about it.

## Alternatives considered

**A consent table in PostgreSQL joined at query time.** Forbidden by 0002 at this
cardinality, and wrong even where a join were affordable: it answers "what does this player
consent to now", not "what applied when this row was written".

**A batch-level consent record alongside the events.** Better, but it separates the basis
from the data, and the two get separated further by every reprocessing, archive restore and
schema migration thereafter.

## Enforcement

- `db/clickhouse/migrations/00001_events_raw.sql` — the consent columns are on the event
  table itself.
- `apps/backend/internal/handlers/ingest.go` — a batch without analytics consent returns
  `202` with `dropped` set and publishes nothing.
- `cmd/ingest-api/main_test.go:TestConsentWithheldStoresNothing` asserts nothing reaches the
  bus.
