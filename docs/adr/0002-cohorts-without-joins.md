# 0002. Cohorts are never computed with a join

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §29, Appendix C.16, C.17

## Context

The natural way to write "day-7 retention for the players who installed in March" is a
join: a table of devices with their cohort date, joined to a table of daily activity.

At PlayerSense's scale that query is the single most effective way to take the cluster
down. The right-hand side of a ClickHouse join is built in memory on the initiating node.
A million installs is a hash table of a million entries; ten million is ten million; and
the query is per project, per breakdown, on a dashboard that autorefreshes. Memory limits
turn it into an error for the customer with the most data — which is the customer paying
the most.

The obvious escape hatches are worse. A dictionary keyed on device is the same hash table
with a longer lifetime and a startup cost. `IN (SELECT …)` is the same set materialised
under a different name.

## Decision

**A cohort query is a single-table `GROUP BY` with no join and no dictionary keyed at
actor cardinality.**

This is bought by stamping the cohort key, and the dimensions a cohort is broken down by,
onto every row **at write time**:

```text
cohort_date          the day this device was first seen
install_platform     sticky: the value as of the install, never the current value
install_country
first_app_version
acquisition_source
```

Because the values are fixed at install, cohorts are disjoint: a device belongs to exactly
one `(cohort_date, platform, country, version, source)` bucket for its whole life. Counting
disjoint sets needs no `uniqExact` and no join — plain counters sum correctly.

The denominator is the cohort size, which is a bounded table: one row per cohort per
breakdown, not one per device.

## Consequences

- A dimension that changes over a device's life (current app version, current country) is
  **not** a cohort dimension. Asking "retention of players who are on version 3 today"
  needs a different mechanism, and pretending the sticky column answers it would be
  quietly wrong.
- The writer needs the device's cohort key at insert time, which is why it reads device
  state from Valkey (0013). A miss means this is the device's first batch.
- If Valkey is unavailable the writer stamps nulls and the nightly derivation backfills
  them. Losing Valkey costs accuracy until the job runs, never rows.
- Breakdown combinations multiply. The write-time stamp is what makes them affordable, and
  §29 caps how many are published.

## Alternatives considered

**Join, with a memory limit and a friendly error.** The error lands on the largest
customer, at the moment they are most engaged. That is not a degradation, it is a feature
that does not work for the people who need it.

**A dictionary of device → cohort_date.** Same cardinality, now resident. It also has to be
reloaded, and a dashboard that is briefly wrong after a reload is worse than one that is
slow.

**Compute retention entirely in the nightly job and store only the answer.** This is in
fact what happens for the published aggregates — but the raw daily table still has to be
groupable on a cohort key, or an ad-hoc breakdown is impossible and every new question
needs a new job.

## Enforcement

- `db/clickhouse/migrations/00003_player_activity_daily.sql` carries `cohort_date` and the
  sticky dimensions in the table itself.
- `apps/backend/internal/cohort/derive.go` contains the derivation, and every statement in
  it is a single-table aggregate.
- `apps/backend/cmd/cohortcheck/main.go` asserts the correctness properties against seeded
  data — including that the counts produced without a join match a slow reference
  computation.
