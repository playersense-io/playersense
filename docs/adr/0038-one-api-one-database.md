# 0038. One API and one database, until revenue says otherwise

**Status:** Accepted
**Date:** 2026-09-21
**Supersedes:** [0008](0008-two-tier-control-plane.md), [0009](0009-cells-region-and-residency.md), [0010](0010-us-control-plane-eu-cell.md)
**Relates to:** project.md §6, §10, §11, §99; [ADR 0007](0007-data-classification-in-the-schema.md), [ADR 0012](0012-three-tier-key-resolution.md)

## Context

Three earlier records built a shape for a company that had customers:

- **0008** split PostgreSQL in two, under the rule that a player identifier never appears
  in the global tier.
- **0009** separated region from cell and added a registry, so a project could be placed in
  a stack and a misdirected request could be told where to go.
- **0010** put the global tier in the United States and the first cell in Europe.

Each was correctly reasoned for the world it described. None of them is load-bearing today,
because that world has not arrived: there is one deployment, in one place, with no customer
whose procurement has asked where the bytes sit.

What they cost is not hypothetical:

- Two generated ORM clients, two connection pools, two marker tables, two `AssertTier`
  calls at every service's start-up, and a `migrate` command with a target grammar.
- Four HTTP binaries — `control-api`, `ingest-api`, `query-api`, `config-api` — each with
  its own address, its own public URL, its own settings block and its own composition
  root, three of which a cell would have run a copy of.
- A routing rule on every credential check. Ingestion, configuration and both query paths
  compared the key's cell with their own and answered `421` with an address, and the SDK
  carried the redirect, persisted the replacement and had conformance tests for it.
- A cell slug prefixing every Valkey key, every event-bus segment, every archive object
  path and the ClickHouse Keeper `{shard}` macro.

That is a large amount of machinery whose only function today is to be correct about a
situation that does not exist.

## Decision

**One API and one database**, until revenue makes the alternative worth paying for.

`cmd/api` serves the dashboard, the SDK's ingestion and configuration endpoints, and the
reporting and data-subject surfaces, on one address. One PostgreSQL database holds accounts,
projects, keys and billing alongside purchases, deletion requests, exports and archive
manifests. ClickHouse and Valkey are unchanged: they were never what the split was about.

**§96's per-column classification stays, and is the part that was doing the work.** Every
column still declares what it holds, and `migrate verify` still reads those declarations
back off the live catalog rather than out of the schema files. 0008's residency rule was
expressed *in terms of* that classification — "no column classified `identifier` in the
global tier" — so the classification is the primitive and the tier was the policy written
on top of it. Removing the policy leaves the primitive intact, and a future tier split can
be re-expressed against it in an afternoon.

## Consequences

- **The residency answer is now "one place", not "the part you care about is in Europe".**
  A customer who asks gets a simpler answer and a weaker one. This is the cost, it is
  accepted knowingly, and it is the thing to revisit first when a deal turns on it.
- **Ingestion is no longer separately deployable.** §10.2 wanted it to keep accepting
  batches while the control plane was down; now a bad deploy takes both. What survives is
  the property that mattered more: ingestion still never queries PostgreSQL per request,
  because the tenant comes from the key cache (0012) and the batch goes to the queue
  (0013). A PostgreSQL outage still does not stop a batch being accepted.
- **Scaling is now uniform.** The dashboard and the ingestion path scale together, so the
  cheap surface pays for the expensive one. At present traffic that is one small instance
  either way.
- **One rate limiter, published fail-open**, because the SDK paths dominate and §65's rule
  is that losing Valkey costs speed rather than data. The invitation route wants the
  opposite and takes it per call through `Limiter.Strict`, since an email cannot be taken
  back.
- **Archive object paths lost their `cell=` segment.** Objects written under the old prefix
  are not found by the new one. Nothing in production had any; a development stack needs
  `migrate reset` or an empty bucket.
- **The SDK's `421` handling is gone**, along with `Disposition::Redirect`. `ps.endpoint`
  still wins over the configured address on launch, because a shipped build whose endpoint
  later moves has no other way to be told.

## Alternatives considered

**Keep the tier split and merge only the binaries.** Half the machinery for none of the
benefit: two pools and two clients are most of what the split costs, and the residency
story needs both tiers *and* separate hosting to mean anything.

**Keep the four binaries and drop only the cells.** The binaries were split so a cell could
run three of them and so ingestion could fail independently. Without cells, the first
reason is gone and the second is one property, retained above by other means.

**Leave it all in place and pay the carrying cost.** Defensible if the customer were
imminent. The cost is not the code that exists but the code written against it: every
feature added while the split stands has to be placed on one side of it, and the placement
argument is the expensive part.

## Enforcement

- `apps/backend/ent/generate.go` — one generated client from one schema package.
- `apps/backend/internal/pgdb/pgdb.go` — one `DB`.
- `apps/backend/cmd/api/main.go` — the single composition root.
- `apps/backend/cmd/migrate/main.go` — `verify` still fails on any unclassified column, in
  PostgreSQL and in ClickHouse.
- `docs/compliance/03-residency-and-transfers.md` records what the residency answer is now.
