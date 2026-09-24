# 0046. Every dashboard page reads a fact built for it, and none of them joins

**Status:** Accepted
**Date:** 2026-09-24
**Relates to:** [ADR 0002](0002-cohorts-without-joins.md), [ADR 0017](0017-aggregate-disclosure-threshold.md), [ADR 0019](0019-a-compiler-that-cannot-join.md), [ADR 0021](0021-two-metric-tiers.md); project.md §13.1, §26, §27, §29.1, §30, §31, §32, §44, §55, §56

## Context

The reporting surface answered two questions, `cohorts/query` and `metrics/query`, and the dashboards
asked for a great deal more: an event catalogue with each event's properties and values, sessions with
their lengths and endings, a directory of players and one player's timeline, revenue by product,
store and country, a purchases ledger, flag exposures, experiments, funnels, realtime top events, a
lifecycle split of returning players, and an operations console that could see ingestion, the
ClickHouse node and storage across every tenant. Every one of those pages ran on sample data.

§13.1 had already named most of the missing facts (`sessions_final`, `purchase_events`,
`feature_exposures`) and §29.1 had already said the one thing each of them had to obey: nothing on
the query path joins on a player, device or session key, because at this document's scale that is a
hash build over hundreds of millions of keys on the node that is also ingesting. Most of the missing
questions are, on their face, joins: an exposure paired with the activity after it, a purchase with
whether it was the player's first, an account with its installs.

Two existing reads were also heavier than they looked. A whole-project realtime or hourly chart merged
one `uniq` state per event name per bucket, and a busy event's hourly `uniq` state is a quarter of a
megabyte, so a 90-day hourly chart of active players grew with traffic. And `metrics/query` with a
month interval and a dimension compiled into SQL that read a column `metrics_monthly` does not have.

## Decision

**Each page reads facts built for it, and each fact is built without a join.** Migrations 00013 to
00019 add them:

| Fact | Built by | Answers |
|---|---|---|
| `activity_1m`, `activity_1h` | view; 1h chained from 1m | whole-project realtime and hourly totals; ingestion bytes, late events and writer lag |
| `event_names`, `events_daily`, `event_properties_daily` | views | the event catalogue, each event by platform, country and build, property fill and values |
| `sessions` → `sessions_daily` | view, then nightly | recent sessions; lengths on a fixed ladder, endings, by platform |
| `purchases` → `payers`, `revenue_daily` | view; two chained from it | the ledger, first-time payers, spender tiers, revenue by product, store, country and price |
| `feature_exposures_daily` | view | flag insights |
| `cohort_events_daily`, `lifecycle_daily` | view; nightly | an event per cohort and offset; new, continuing and reactivated players |
| `flag_assignments` → `experiment_daily` | view, then nightly | each arm's outcomes over seven days from exposure |

The constructions that take the place of a join:

- **Stamp, then group.** The cohort key is already on every event (§29.5), so an event per cohort
  and offset is a subtraction inside a view, and a funnel split by an arrival dimension reads it off
  the step events.
- **Union, then group.** An experiment arm is a device's first exposure and its outcomes are that
  device's days. The nightly derivation reads `flag_assignments` and `player_activity_daily` as one
  `UNION ALL` grouped by device: each device is one aggregation row holding both, and nothing is
  built on one side and probed from the other. The fact keeps counts, sums and sums of squares,
  which add across exposure days, so a mean and a standard deviation are arithmetic.
- **Two bounded statements.** Whether a purchase was the install's first is a second read of
  `payers` for the page's devices, at most two hundred keys, never a join of the two tables. An
  account's installs are read from `sessions` first and its days and events looked up by them.
- **A player's whole answer in one row.** A funnel reads only its step events, keeps at most a
  thousand per player, decides how far each player got with ClickHouse's native `windowFunnel`, and
  measures each step's wait along the first attempt with one vectorised `arrayFirst` per step.

**Sketches are bounded and sized for what they count.** Every new player count is `uniqCombined`:
`(14)` where a chart reads it (about 12 KiB and 0.8% at most, exact while small) and `(12)` where
many cells each hold one (about 3 KiB, 1.6%). A property's top values are an `approx_top_k(10)`
state, so a transaction id costs the same as a level number. The whole-project realtime and hourly
questions read `activity_1m` and `activity_1h`, one state per bucket.

**Every read is bounded, and a read over its bounds is refused, not failed.** Dashboard statements
carry a memory ceiling above half of which ClickHouse spills GROUP BY and sorts to disk. The few
reads of `events_raw` (an event's latest occurrences, a player's timeline, a funnel) run under
tighter raw limits, are bounded by the whole sort-key prefix and a day range, and read the last day
first where a page can be filled from it. A statement that reaches any limit is answered 422 with
what to narrow, since it would fail the same way on retry.

**Reading one player is its own permission.** `player.read` guards the directory, a player's page,
the ledger, the newest sessions and an event's latest occurrences; `analytics.read` stays a grant to
aggregates with thin cells withheld. None of the player-level pages is served for a `kids_strict`
project. The operations console is a separate package, `ops`, behind the operator token, because it
reads every tenant or ClickHouse's system tables and `query` must never be able to leave its tenant
out.

## Consequences

- Eighteen views run on every insert where there were five. On five million events laid out as SDK
  batches are, they processed about 400,000 events a second per core together; at §4's ceiling of
  roughly 11,500 events a second that is a few percent of one core. The property catalogue is the
  heaviest of the new ones, because it expands every event's properties, and context properties are
  left out of it for that reason.
- On the same data every explorer page answered in 30 to 250 milliseconds and under 100 MiB, the
  funnel included.
- Player counts on these pages are sketches above a few hundred and say so (`approximate`). The
  daily metrics and cohort facts stay exact (ADR 0021).
- A funnel's waits follow each player's first attempt, where its levels follow any attempt, so a
  player who failed once and succeeded later is counted as converted and timed on the first try.
- A rollup created after the events it summarises starts empty. `migrate backfill <table>` runs the
  view's own SELECT over `events_raw` once, and refuses a table, or anything chained from it, that
  already holds rows.
- `sessions` and `purchases` name players, so they keep the project's raw retention (`sessions` at
  most ninety days) and, with `payers` and `flag_assignments`, are purged by an erasure.
- Revenue is what the game reported with `TrackRevenue`, in the currency it reported. Verified
  purchases (§46) and FX conversion do not exist, and every revenue answer says `verified: false` and
  which currencies it summed.
- Ad revenue, crashes, acquisition source and a segment's reach have no fact behind them, because
  nothing ingests them.

## Alternatives considered

- **Answering the pages from `events_raw` with limits.** Rejected for everything a page reads on
  load: a catalogue or a sessions chart over thirty days would scan a month of every event, on every
  page view, and grow with the game.
- **One wide rollup per page carrying every dimension.** Rejected by §27's own warning: the product
  of event name, platform, country and build approaches raw cardinality. `events_daily` carries one
  dimension per row instead.
- **A join to a bounded dimension for experiments** (exposed devices as an `IN` set). Rejected: the set
  is a player-cardinality set, which is what §29.1 forbids, and the union construction answers the
  same question with the memory of an ordinary per-device aggregation.
- **`arrayFold` to track every funnel chain's timings.** Built first, then measured: it ran a lambda
  per event, sequentially, and took twelve times as long as `windowFunnel` over the same events.

## Enforcement

- `internal/query/nojoin_test.go` parses every string literal in package `query`, where every
  statement's SQL comes from, and fails on a join in any form ClickHouse accepts.
  `TestACohortQueryNeverJoins` and `TestASeriesQueryNeverJoins` still cover the planner's shapes.
- The builder in `internal/query/sql.go` has no join method; its sources are the closed `Fact` set,
  and `over()` nests a statement over the same fact rather than adding one.
- `cmd/api/explore_main_test.go` seeds `events_raw` and reads every page back through the dashboard's
  routes: suppression, `player.read`, the `kids_strict` refusals, the funnel's windows, an experiment's
  statistics.
- `internal/erasure/erasure_test.go` proves `sessions`, `purchases` and `payers` are purged.
- `cmd/api/ops_main_test.go` covers the console behind the operator token.
