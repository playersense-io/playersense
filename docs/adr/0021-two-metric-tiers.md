# 0021. Two metric tiers: one immediate and approximate, one derived and exact

**Status:** Accepted
**Date:** 2026-09-15
**Relates to:** project.md §13.1, §26, §27, §28, §29.14, §38

## Context

§27 asks for `events_1m`, `events_1h`, `players_1d`, `sessions_1d` and `revenue_1d`, and
§26 gives the policy that chooses between them by range. Building them raised three
questions the document answers in other sections rather than in §27.

**How many daily tables.** The three daily ones share a grain and a key. §13.1 already
resolved the identical question on the cohort side when it collapsed
`retention_cohort_daily` into `cohort_metrics_daily`, and gave the reason: "three tables of
the same shape cost three scans to answer one question."

**Where a daily fact comes from.** `events_raw` is the obvious source and the wrong one.
§38 requires the billing meter to be derived from `player_activity_daily` and says why —
"the billing meter and the cohort tier are reading the same fact. Deriving the two
independently would eventually produce a month where the invoice and the dashboard
disagree, and no amount of investigation would establish which was right."

**Whether a daily fact can be a materialized view.** §28 recommends incremental views for
summaries computable on newly inserted blocks, which reads like a yes. §29.14 shows it is
not: `player_activity_daily` is a `SummingMergeTree`, so until parts merge a device-day
exists as several partial rows, and a chained view counting `count()` sees the same player
once per block.

## Decision

**Two tiers with different guarantees, and the answer says which it came from.**

`events_1m` and `events_1h` are incremental materialized views over `events_raw`, per §28.
Their player counts are `uniq` states and therefore **approximate**, which §27 permits
("approximate unique players where appropriate"). Every response from them is marked
`approximate: true` and carries no `computed_at`, because a continuously updated view has no
last run.

`metrics_daily`, `metrics_breakdown_daily` and `metrics_monthly` are rebuilt by a nightly
job from `player_activity_daily`, collapsing to one row per device-day before counting.
Their counts are **exact**.

**One daily table, not three.** Following §13.1's precedent.

**MAU is its own distinct count.** A player active on the 3rd and the 9th is one monthly
active player and two daily ones, so a month cannot be summed from its days.

**`new + returning = active`, by construction.** A device whose cohort is unknown — §65's
null from a Valkey outage — is counted as returning. That mislabels some of them; the
alternative is a third bucket every chart has to explain, or two counts that silently fail
to sum to the total.

## Consequences

- A dashboard can show "right now" and "this quarter" on one screen, and the two are allowed
  to disagree slightly, because the one that is approximate says so.
- Nothing billable is ever computed from a sketch. §38's meter and the dashboard's MAU read
  the same rows.
- The daily tier is a day behind until the job runs. That is the cost of exactness and the
  reason the realtime tier exists beside it rather than instead of it.
- The daily breakdowns carry the *arrival* dimensions `player_activity_daily` holds:
  `install_country` is where a player installed, not where they are today, and
  `first_app_version` is what they installed, not what they are running. A version-adoption
  chart is a different question against a different source, and is listed as open rather
  than approximated with these columns.

## Alternatives considered

**Three daily tables, as §27 names them.** Rejected on §13.1's own reasoning. A dashboard
asking for DAU, sessions and revenue over September is asking one question.

**Derive the daily tier from `events_raw`.** A second, independent definition of "active
player". §38 rejects exactly this for the billing meter, and a dashboard that disagrees with
an invoice is the same failure.

**Make the daily tier a materialized view too.** §29.14's trap. DAU would drift upward as
parts went unmerged and nothing would report an error — and on small, already-merged data
the wrong query returns the right answer, so it would not be caught in development either.

**`uniq` everywhere, for one uniform tier.** Simpler, and it would put an approximate number
on an invoice.

## Enforcement

- `db/clickhouse/migrations/00007_event_rollups.sql`, `00008_metrics_daily.sql`.
- `apps/backend/internal/rollup/derive.go` — the job, with the collapse as a named constant
  every statement reads from.
- `cmd/cohortcheck` checks 11-15. **15 is the one that matters**: it writes a second partial
  row for a device-day that already exists — exactly what an unmerged part looks like — and
  asserts the active count does not move while the measures do. The uncollapsed derivation
  passes checks 11 to 14 and fails 15 with "a partial row for an existing device changed the
  active count from 100 to 101".
- `TestOnlyTheRealtimeTiersAreApproximate`, `TestEachIntervalReadsItsOwnTier`,
  `TestMonthlyActivesAreNotTheSumOfTheDays`, `TestADailySeriesIsReadBackCorrectly`.
