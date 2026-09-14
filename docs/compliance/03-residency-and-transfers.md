# 03. Residency and transfers

## The shape

```text
                 ┌──────────────────────────────────────┐
                 │  Global control plane — UNITED STATES │
                 │                                       │
                 │  accounts, organisations, projects,    │
                 │  environments, API keys, billing,      │
                 │  feature flags, the cells registry,    │
                 │  aggregate usage counts                │
                 │                                       │
                 │  NO PLAYER IDENTIFIER, EVER            │
                 └──────────────────────────────────────┘
                            │ (aggregate counts only)
                            │
   ┌────────────────────────┴───────────────────────────┐
   │  Cell eu-1 — EUROPE                                 │
   │                                                     │
   │  ingest endpoint, queue, ClickHouse, Valkey,        │
   │  archive bucket, workers, and its own PostgreSQL:   │
   │  verified purchases, deletion requests, exports,    │
   │  archive manifests                                  │
   │                                                     │
   │  EVERYTHING THAT NAMES A PLAYER                     │
   └─────────────────────────────────────────────────────┘
```

A **region** is the legal boundary — what law applies and where bytes may sit. A **cell** is
one stack inside a region, a unit of capacity. They split for different reasons and are
separate concepts for that reason alone (ADR 0009).

A project is assigned to exactly one cell **at creation**, and the assignment is immutable.
Its data never leaves that cell.

## The rule

> **A player identifier never appears in the global tier.**

Everything else in this document rests on it. It is short enough to check in review and, more
importantly, it is checked mechanically:

1. **Two ORM clients.** Each tier's entities are generated into their own package, so a
   query written against the global client cannot name a cell entity — the type is not
   there. The rule is a compile error, not a review comment.
2. **Tier markers.** Each database carries a table naming the tier it holds, and each
   migration refuses to run against the other. Pointing the migrator at the wrong DSN would
   otherwise grow a second control plane inside a cell's database, silently.
3. **`migrate verify`.** Every column carries its class ([01](01-data-inventory.md)); the
   verifier reads them from the catalog and fails on any column classified `identifier` in
   the global tier.

Verified negatively: adding `player_device_id text` with `class=identifier` to the global
tier makes `migrate verify` exit non-zero and name the column.

The billing meter is the pattern everything else follows: it computes its actor set **inside
the cell** and sends the global tier a **count**. The global tier learns aggregates, never
subjects.

## Why the two tiers are in different places

**The global tier is US-hosted** because the company is a US entity, billing sits beside
Stripe, and — decisively — it holds no player data. Its rows describe direct customers who
signed the terms themselves, and there are thousands of them rather than millions.

**A cell's tier is in the cell's region** because it holds the player data, and that is the
part a customer's procurement asks about. "Frankfurt" is a better answer than "the United
States, under a framework". Ingest latency does not argue against it: batches are
fire-and-forget and answered `202`, so an extra hundred milliseconds is invisible.

## What this does not buy

This is the assumption the arrangement most invites, so it is stated first and plainly:

> **Hosting the control plane in the United States does not reduce GDPR exposure.**

GDPR attaches because EU players' data is processed and services are offered to EU studios,
not because of where a server sits. A US entity needs a transfer mechanism for EU personal
data it controls **wherever** that data is hosted, because US staff can reach it.

What actually keeps the transfer surface small is the rule above. Because no player
identifier reaches the global tier, the data that genuinely crosses is:

| What crosses | About whom | Volume |
|---|---|---|
| Customer account records | Studio staff who signed up | Thousands of people |
| Aggregate usage counts | Nobody identifiable | Numbers |
| Remote administrative access to the cell | Potentially player data, in support | Rare, logged, reasoned |

The third row is the honest one. US engineers can, in an incident, reach an EU cell. That
access is a transfer, it is covered by the same mechanism as the rest, and
[08](08-security-and-sub-processors.md) covers how it is logged.

## Transfer mechanism

For EU personal data reachable from the United States, the basis is one of:

- **EU–US Data Privacy Framework** self-certification, once registered; and
- **Standard contractual clauses** in the customer DPA, with a transfer impact assessment,
  which stand independently of the framework's status.

The SCCs are kept in place regardless of DPF registration, because the framework's
predecessors were both invalidated and a mechanism that depends on one instrument is a
mechanism with a single point of failure.

**Status: not yet registered.** See [10-open-items.md](10-open-items.md).

## Namespacing within a cell

Everything keyed by cell carries the cell slug: the Valkey cache keys, the revocation
tombstone, the rate-limit counters, the event-bus spool, the ingestion deduplication key,
and the ClickHouse Keeper replica path.

A cell has its own Valkey and its own ClickHouse, so this is belt and braces. Its value is
that a misrouted read is a **miss** rather than one cell silently serving another cell's
credential or spending its budget — and that two cells in a development stack cannot blend.

## The wrong-cell case

A gateway that receives a key belonging to another cell answers **421 Misdirected Request**
and names the `ingest_url` that can serve it.

This is the single most important line in the design. Without it, one wrong hostname in an
SDK configuration puts one region's player data on another region's disks — the exact
outcome the whole arrangement exists to prevent, defeated by a typo.

`421` rather than `403` because the request is well-formed and the credential is good; it
simply arrived somewhere that cannot serve it, and a misconfigured integration is better
told where to go than merely refused.

Tested by registering a second cell, placing a project in it, and asserting the refusal,
the named endpoint, and that nothing reached the local queue. The test was confirmed to have
teeth by removing the check, which made it fail.

## Adding a region later

An operational move, not a migration:

1. Provision the stack and run `migrate cell up`, which registers the cell and creates its
   PostgreSQL and ClickHouse.
2. Create projects with that cell.

What it is **not** is moving existing projects. That crosses a jurisdiction boundary with
live data and deserves its own design; the immutable cell edge says so rather than offering
a field that half-works.
