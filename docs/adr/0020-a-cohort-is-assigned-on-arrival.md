# 0020. A cohort is assigned when the server sees the batch

**Status:** Accepted
**Date:** 2026-09-14
**Relates to:** project.md §29.4, §19.2, §29.9

## Context

Every player belongs to exactly one cohort, and the cohort is a day. Which day is a choice
between two timestamps that usually agree: the `event_time` the device reported, and the
`ingest_time` the server assigned when it accepted the batch.

§29.4 chose `ingest_time` and gave two reasons. The implementation then used `event_time`
anyway, with a reasonable-sounding argument in its comment — a batch spooled offline for two
days belongs to the day it happened — and nothing caught it for two services, because
nothing had yet compared a numerator against a denominator.

This record exists because the code drifted from the document once and would have again.

## Decision

`cohort_date` is derived from the server-assigned `ingest_time`. The client's clock never
decides a cohort.

## Consequences

- A batch spooled offline is assigned to the day it arrived rather than the day it happened.
  This is a real inaccuracy and it is bounded: §19.2 clamps `event_time` into
  `[ingest_time - 7d, ingest_time + 1h]`, so the error cannot exceed the spool window.
- A late event can never relocate a player into an earlier cohort. Cohorts that have been
  reported stay the size they were reported at.
- A device with a wrong clock cannot land in a 1970 cohort with a `day_offset` around 20,700.
- **The numerator and the denominator describe the same cohort.** `cohort_sizes` is derived
  from `player_lifetime`, whose `first_seen` is `min(ingest_time)`. Keying the numerator on
  event time made every retention rate divide by a denominator belonging to a different
  cohort — and it did not surface as an error, because the denominator was simply absent and
  the rate read as zero.

## Alternatives considered

**`min(event_time)` from the client.** The offline-spool case it improves is bounded to
seven days by the clamp. The cases it breaks — retroactive cohort movement, 1970 cohorts,
and a denominator that does not match — are unbounded and silent.

**Both: event time for the cohort, ingest time for the denominator.** This is what the code
accidentally did. It is not a design.

**Reconciling in the nightly derivation.** The derivation already backfills a null
`cohort_date` (§65). Making it also *correct* a stamped one would mean cohorts that move
overnight, which is the thing §29.4 is avoiding.

## Enforcement

- `apps/backend/internal/writer/device.go` — `firstCohortDate` reads `batch.IngestTime`.
- `TestABackdatedEventKeepsTodaysCohort` posts an event dated five days ago and asserts the
  stamped cohort is the arrival date. Verified to have teeth by restoring the event-time
  derivation.
- `cmd/cohortcheck` check 7 (`no cell has returned_players > cohort_size`) and check 9
  (`every breakdown cell has a within-value denominator`) both fail if the two tiers stop
  agreeing about which day a player arrived.
