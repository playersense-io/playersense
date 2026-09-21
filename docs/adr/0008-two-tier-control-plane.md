# 0008. The control plane is two databases under one rule

**Status:** Superseded by [0038](0038-one-api-one-database.md)
**Date:** 2026-09-06 (revised 2026-09-14: two databases, not two schemas; superseded 2026-09-21)
**Relates to:** project.md §11, §99

> **Superseded.** The split this record describes was built and then removed: there is one
> API and one database (see [0038](0038-one-api-one-database.md)). The reasoning below is
> kept because it is the argument to re-read when residency stops being hypothetical.

## Context

§6 makes the data plane cellular — ingestion, the queue, ClickHouse and the archive all
belong to a cell. The control plane had no such division, and that is where European
residency actually breaks.

Not in the analytics data, which is already cellular, but in PostgreSQL: `verified_purchases`,
`purchase_entitlements`, `data_deletion_requests`, `exports` and `archive_manifests` all
hold rows about identifiable players, sitting in the same database as `organizations` and
`billing_subscriptions`, which hold nothing about a player at all.

## Decision

Split the control plane in two, under one rule short enough to check in review:

> **A player identifier never appears in the global tier.**

**Global tier** — one database: users, organisations, projects, environments, keys,
settings, retention policies, cells, billing, feature flags, aggregate usage counts.

**Cell tier** — one database per cell: verified purchases, entitlements, deletion requests,
exports, archive manifests, and any audit record naming a player.

They are separate **databases**, not schemas in one, because in production they are
separate servers under separate jurisdictions. Locally they are two databases inside one
container, so the development stack does not grow while the code is shaped for the split
production has.

## Consequences

- Anything needing both an account and a purchase does two queries against two databases.
  There is no join across the boundary, and that is the boundary working.
- The billing meter computes its actor set inside the cell and sends the global tier a
  **count**. The global tier learns aggregates, never subjects — and every feature added
  later inherits that shape.
- Two generated ORM clients. A query written against the global client cannot name a cell
  entity because the type does not exist in that package, so the rule is a compile error
  rather than a review comment.
- One global tier means **one surrogate allocator**. Auto-increment ids become the
  `org_seq` / `project_seq` / `environment_seq` in ClickHouse's sort keys, so they must be
  globally unique; replicating the control plane per region would have required inventing
  an allocation scheme first. This is an independent reason the global tier stays singular.

## Alternatives considered

**One database, split by PostgreSQL schema with a pinned `search_path`.** This is what was
built first, and it was right for one server. It stops being right the moment the tiers
need to be in different countries, and the migration from schemas to databases is cheaper
now than after the first customer's purchase rows exist.

**Replicating the whole control plane per region.** Solves residency and creates a
distributed-consensus problem for account data, plus the surrogate allocation problem above.
Far more machinery for a tier that holds no player data anyway.

**Encrypting player-linked columns in a single global database.** Moves the question to key
custody without answering it: a US entity holding the keys can still be compelled to use
them, so the data is still reachable from the United States.

## Enforcement

- `apps/backend/ent/generate.go` — two generated clients from two schema packages.
- `apps/backend/internal/pgdb/pgdb.go` — `Global` and `Cell`, each with a tier marker table
  and an `AssertTier` a service calls at start-up.
- Each `Migrate` refuses to run against the other tier's database. Verified: pointing
  `migrate global up` at the cell database, and `migrate cell up` at the global database,
  both fail with a named error rather than creating a second control plane.
- `migrate verify` fails on any column classified `identifier` in the global tier (0007).
