# 0014. An ORM for PostgreSQL, hand-written SQL for ClickHouse

**Status:** Accepted
**Date:** 2026-09-11
**Relates to:** project.md §11, §13, §25, §29

> **Amended by [0038](0038-one-api-one-database.md).** There is one PostgreSQL database
> and one generated client, so the commands are `migrate postgres` and `migrate up ch`.
> The two-tool split this record is about — Ent for PostgreSQL, goose for ClickHouse — is
> unchanged.

## Context

The instruction was to pick an ORM and stop writing raw SQL. That is right for one of the
two databases and impossible for the other, and the honest answer is to say which is which
rather than apply one rule to both.

## Decision

**PostgreSQL uses Ent.** The control-plane schema is defined in Go, the client is generated
from it, and migrations come from the schema rather than from hand-written DDL. Two schema
packages produce two clients, which is what makes 0008's residency rule a compile error.

**ClickHouse uses hand-written SQL, versioned with Goose.** There is no ORM for it, and the
reason is not that nobody has written one — it is that the features the schema depends on
have no equivalent in any ORM's model:

- engine selection per table: `ReplicatedMergeTree`, `AggregatingMergeTree`,
  `SummingMergeTree` with explicit column lists;
- aggregate **state** types — `argMinState`, `argMinIfState`, `SimpleAggregateFunction` —
  which are values, not columns an ORM has a concept for;
- `MATERIALIZED` columns participating in sort keys;
- materialized views as insert triggers;
- `TTL … DELETE` with `ttl_only_drop_parts`;
- `REPLACE PARTITION` as a publication mechanism (0003).

An ORM over that surface would be a string builder wearing a costume, and the costume is
what makes it dangerous: it implies a portability and a type safety that are not there.

## Consequences

- Two migration tools. `migrate global up` and `migrate cell up` name which is which, and
  each refuses to run against the wrong database (0008).
- ClickHouse queries are reviewed as SQL, which is the right review for them — the
  interesting bugs in `internal/cohort/derive.go` were alias shadowing and partial-row
  over-counting, and both are visible in SQL and invisible through an abstraction.
- Ent's generated client is committed, so a clone builds without a codegen step.

## Alternatives considered

**An ORM for both.** Tried against the real DDL; the schema cannot be expressed. See the
list above — the first casualty is the aggregate state types, which are load-bearing for
`player_lifetime`.

**`sqlc` for ClickHouse.** Generates types from queries rather than the reverse, which is
closer to workable. It does not understand ClickHouse's dialect, and the mismatch surfaces
as a type that compiles and a query that does not run.

**Raw SQL for PostgreSQL too, for consistency.** Consistency at the cost of the one
mechanism that makes the §99 boundary a compile error. Not worth it.

## Enforcement

- `apps/backend/ent/generate.go` — two targets, two schema packages.
- `db/clickhouse/migrations/` — Goose, `DialectClickHouse`.
- `apps/backend/cmd/migrate/main.go` — both, behind named targets.
