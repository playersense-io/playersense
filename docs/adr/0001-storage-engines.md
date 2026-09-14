# 0001. ClickHouse for events, PostgreSQL for state, Valkey for neither

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §3, §11, §12, §13

## Context

PlayerSense stores two kinds of thing that look similar in a diagram and behave nothing
alike in production.

**Events** are immutable facts about something that happened, arriving in large batches,
never updated, queried in aggregate over ranges of time, and measured in billions of rows.
**State** is accounts, projects, keys, subscriptions and entitlements: thousands of rows,
updated constantly, read transactionally, and wrong in an expensive way if a write is lost.

A single store for both is possible and always ends the same way. PostgreSQL holding a
billion event rows spends its life on autovacuum and index maintenance for queries that
scan everything anyway. ClickHouse holding accounts has no transactions worth the name and
no unique constraint that can be relied on, so "one project per slug" becomes a hope.

## Decision

Three stores, each doing the thing it is good at, with a rule for each.

**ClickHouse** holds every event and every aggregate derived from one. Nothing else.
**PostgreSQL** is the source of truth for product state. No raw event ever reaches it.
**Valkey** holds nothing that cannot be rebuilt: cache entries, rate-limit counters, device
state. Every path that reads it works when it is gone, more slowly.

## Consequences

- There is no single query that joins an account to its events. Analytics queries carry the
  tenant as integer surrogates stamped at write time, which is why the ingestion path
  resolves them once from the key rather than per row.
- Two migration systems, because the schemas are owned by different tools (see 0014).
- Losing Valkey costs latency and never data. That is a constraint on every feature that
  wants to put something there, and it has already refused one: the key cache is a cache,
  so a cache miss reaches PostgreSQL rather than refusing the request.

## Alternatives considered

**PostgreSQL alone, with partitioning and BRIN indexes.** Workable to roughly the first
hundred million rows. The failure is not storage, it is that a retention cohort query over
a year of data is a sequential scan measured in minutes, and the product is a dashboard.

**BigQuery.** Removes the operational burden and adds a per-query cost that scales with how
curious a customer is. A dashboard that gets more expensive the more it is used is a
dashboard with a usage warning on it.

**ClickHouse alone.** Attractive until the first `UPDATE` on a subscription. Mutations are
asynchronous rewrites of whole parts; there is no transaction spanning two tables; and a
uniqueness constraint is a `ReplacingMergeTree` that is eventually unique, which is not
what a billing row needs.

## Enforcement

- Appendix C.4 and C.5 are the invariants: no raw event in PostgreSQL, no single-row insert
  into ClickHouse.
- `apps/backend/internal/pgdb/pgdb.go` exposes only the two control-plane clients; nothing
  in the ingestion path has a handle to either on the request path.
- `apps/backend/internal/valkey/valkey.go` — every accessor checks `Ready()` and returns
  `ErrUnavailable` rather than panicking, and every caller has a path that continues.
