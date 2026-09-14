# 0009. Region is a legal boundary, a cell is a stack inside it

**Status:** Accepted
**Date:** 2026-09-14
**Relates to:** project.md §6, §75, §99

## Context

The document had been using "region" and "cell" interchangeably, and a project carried a
free-text `home_region` string. A survey of the code found that string was **inert**: it
was written on create, echoed in responses, and read by nothing. No predicate, no routing,
no query. The validator accepted `"us-1"`, so the API could create a project in a region no
code could reach, and nobody would find out until someone asked where the data was.

Two further consequences of the same gap:

- Key verification never checked the region, so an EU project's key would authenticate
  against a US gateway and land EU events on US disks, silently.
- Nothing was namespaced by cell: the Valkey cache keys, the revocation tombstone, the
  rate-limit prefix, the spool filename, the ingest dedup key, and the ClickHouse Keeper
  `{shard}` macro — which was the constant `01`, so two cells sharing a Keeper would
  collide on every znode path.

## Decision

Separate the two words, because they split for different reasons.

**A region is a legal boundary** (`eu`, `us`): what law applies and where bytes may sit.
**A cell is one stack inside a region** (`eu-1`, `eu-2`): a unit of capacity and blast
radius. A region may hold several cells. Regions split for law; cells split for capacity.

A **cells registry** lives in the global tier: slug, region, status, public ingest URL. It
holds identity and endpoints and deliberately never credentials — a table of database
passwords is a table of database passwords. DSNs stay in each service's environment, and a
service asserts at start-up that its configured cell exists and agrees.

`Project.home_region` becomes an immutable edge to a registered cell, refused at creation
if the cell does not exist or is not `active`.

Everything keyed by cell is namespaced by it, and the Keeper `{shard}` macro becomes the
cell slug.

## Consequences

- A project cannot be created in a cell that does not exist. The typo fails at the API
  rather than at the point somebody asks a compliance question.
- Moving a project between cells is not supported. It is a real data migration across a
  jurisdiction boundary and deserves its own design; making the edge immutable says so
  rather than offering a field that half-works.
- A start-up check reads the registry. It refuses to boot when the registry **disagrees**
  (wrong region) but only warns when the registry is **unreachable** — an outage must not
  stop a gateway from serving what its cache already holds (0012).
- Changing the `{shard}` macro orphans the Keeper metadata of tables created under the old
  value. That was a one-off cost paid during this change.

## Alternatives considered

**Keep one axis and call it "region".** Then splitting a busy European cell for capacity
looks like a legal event, needs a legal opinion, and gets deferred.

**Put DSNs in the registry so services discover their own database.** One table, every
credential, reachable from the service that has the widest access. No.

**Derive the cell from the region plus an index.** Works until a region has cells with
different shapes, which is the first reason anyone splits one.

## Enforcement

- `apps/backend/ent/schema/globaltier/cell.go` — the registry; `slug` and `region` immutable.
- `apps/backend/ent/schema/globaltier/project.go` — the immutable required edge.
- `apps/backend/internal/handlers/control.go:findCell` — refuses an unknown or non-active cell.
- `apps/backend/internal/core/cell.go:CellCheck` — the start-up assertion.
- `apps/backend/internal/valkey/valkey.go:Key` — every key carries the cell prefix.
- `internal/valkey/ratelimit_test.go:TestCellsDoNotShareABudget` and `…ACache`.
