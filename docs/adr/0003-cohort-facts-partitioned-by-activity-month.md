# 0003. Cohort facts are partitioned by activity month, not cohort month

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §29.8, Appendix C.18

## Context

The cohort fact tables are rebuilt by a nightly job. Rebuilding in place with `INSERT`
double-counts on a rerun, so the job publishes whole partitions with
`REPLACE PARTITION` from a staging table — atomic, and idempotent by construction.

That makes the partition key a correctness decision rather than a storage one, and the
intuitive choice is wrong.

Partitioning by **cohort month** looks right: a cohort is the thing being measured, and a
January cohort's numbers live together. But a January cohort accumulates new cells every
day *forever* — day 1 in January, day 30 in February, day 90 in April. Replacing the
January partition while recomputing only the last few days of activity therefore replaces
a partition containing months of cells with one containing a handful.

This was not reasoned out in advance. It was found by running it: a rebuild covering a
recent window **destroyed seven of nine cells** in a test fixture, because the replacement
partition was complete only for the days the job had recomputed.

## Decision

**Partition cohort facts by the month of the *activity*, and rebuild in whole months.**

A day's activity contributes to exactly one activity month. A rebuild of that month
recomputes every cell in it, for every cohort, so the replacement partition is complete and
`REPLACE PARTITION` is safe.

## Consequences

- The job's unit of work is a month, not a day. A late-arriving event for last month means
  recomputing last month, which is bounded and cheap enough to be ordinary.
- Numerator and denominator are recomputed and published **together** (Appendix C.19).
  Publishing one without the other produces a retention rate that is briefly, plausibly
  wrong — the worst kind.
- Reading one cohort's whole life touches several partitions. That is the right trade:
  reads are cheap and a corrupted rebuild is not.

## Alternatives considered

**Cohort-month partitioning with a full historical rebuild.** Correct and unbounded: every
night's job grows with the product's whole history.

**`INSERT` with a `ReplacingMergeTree` and dedupe on read.** Moves the cost to every query
forever, and "eventually replaced" is not a property a published number should have.

**Mutations (`ALTER TABLE … DELETE`).** Asynchronous rewrites of whole parts, with no way
to know when they are done. Not a publication mechanism.

## Enforcement

- `db/clickhouse/migrations/00004_cohort_facts.sql` — `PARTITION BY toYYYYMM(activity_date)`.
- `apps/backend/internal/cohort/derive.go` — the staging tables and the `REPLACE PARTITION`
  publication, one month at a time.
- `apps/backend/cmd/cohortcheck/main.go` — the assertion that a rebuild of a recent window
  leaves older cells intact. This is the check that would have caught the original bug.
