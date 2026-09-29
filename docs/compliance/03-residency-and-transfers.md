# 03. Residency and transfers

> **This chapter changed on 2026-09-21.** PlayerSense used to run a US-hosted control plane
> and a European cell holding everything that named a player, under the rule that a player
> identifier never appeared in the global tier. That arrangement was built and then removed
> ([ADR 0038](../adr/0038-one-api-one-database.md)). The honest current answer is simpler
> and weaker, and it is stated below rather than left as an out-of-date diagram.

## The shape

```text
   ┌─────────────────────────────────────────────────────┐
   │  One deployment                                      │
   │                                                      │
   │  api, workers, queue, ClickHouse, Valkey,            │
   │  archive bucket, and one PostgreSQL:                 │
   │  accounts, organisations, projects, keys, billing,   │
   │  verified purchases, deletion requests, exports,     │
   │  archive manifests                                   │
   └─────────────────────────────────────────────────────┘
```

There is one region, one API and one database. A project is not assigned anywhere, because
there is nowhere to assign it to.

## What is promised about location

**One place, named in the DPA.** A customer asking where their players' data sits gets the
region this deployment runs in, and that is the whole answer. There is no split between
account data and player data, and no per-project choice.

This is a weaker answer than the one the previous architecture could give, and the trade was
made deliberately: the machinery that produced the stronger answer — two databases, two ORM
clients, tier markers, a cell registry, a `421` on every credential check — was carrying a
residency story no customer had yet asked for. [ADR 0038](../adr/0038-one-api-one-database.md)
records the reasoning and the cost.

**It is the first thing to revisit when a deal turns on it.** The superseded records
([0008](../adr/0008-two-tier-control-plane.md),
[0009](../adr/0009-cells-region-and-residency.md),
[0010](../adr/0010-us-control-plane-eu-cell.md)) are kept for exactly that reason. They
describe a split that was built once and can be rebuilt against the classification below,
which is still in force.

## What survives: every column declares what it holds

The rule "a player identifier never appears in the global tier" was written in terms of
§96's per-column classification, not the other way round. The tier is gone; the
classification is not.

1. **Every column carries its class** ([01](01-data-inventory.md)): `identifier`,
   `behavioural`, `financial`, `derived` or `none`. In PostgreSQL the class is emitted into
   `pg_description` by the schema itself; in ClickHouse it is a column comment.
2. **`migrate verify` reads them back out of the live catalog** — not out of the schema
   files, which is the point: a column added by hand, or left behind by a migration rolled
   forward, is exactly the one a schema-file check would miss. Any unclassified column, or
   any class the vocabulary does not know, fails the command.

Verified negatively: adding an uncommented column to either database makes `migrate verify`
exit non-zero and name it.

What that buys today is the inventory in [01](01-data-inventory.md) being accurate rather
than aspirational — which is what a data-subject request, a retention schedule and a breach
assessment are all built on. What it bought before was the residency proof, and it can buy
that again without being rebuilt.

## Transfers

The company is a US entity ([00](00-roles-and-scope.md)), so staff in the United States can
reach the deployment whatever region it runs in. That access is a transfer and needs a
mechanism regardless of where the servers are:

> **Hosting a server in Europe does not, on its own, remove GDPR transfer exposure.**

GDPR attaches because EU players' data is processed and services are offered to EU studios.
A US entity needs a transfer mechanism for EU personal data it controls **wherever** that
data is hosted, because US staff can reach it. That was true under the previous arrangement
too, and it was stated there in the same words.

| What crosses | About whom | Volume |
|---|---|---|
| Customer account records | Studio staff who signed up | Thousands of people |
| Aggregate usage counts | Nobody identifiable | Numbers |
| Administrative access to the deployment | Potentially player data, in support | Rare, logged, reasoned |

The third row is the honest one, and it is larger than it used to be: the same access now
reaches the account records and the player data together, where the split used to separate
them. [08](08-security-and-sub-processors.md) covers how it is logged, and `staff_access`
requires a ticket per read ([ADR 0037](../adr/0037-an-audit-log-records-decisions-not-requests.md)).

## Transfer mechanism

For EU personal data reachable from the United States, the basis is one of:

- **EU–US Data Privacy Framework** self-certification, once registered; and
- **Standard contractual clauses** in the customer DPA, with a transfer impact assessment,
  which stand independently of the framework's status.

The SCCs are kept in place regardless of DPF registration, because the framework's
predecessors were both invalidated and a mechanism that depends on one instrument is a
mechanism with a single point of failure.

**Status: not yet registered.** See [10-open-items.md](10-open-items.md).

## Adding a region later

Not an operational move any more. It is the work [ADR 0038](../adr/0038-one-api-one-database.md)
deferred: splitting the database by classification, restoring a placement for a project, and
giving a misdirected request somewhere to be sent. The records that describe how to do it are
kept in the ADR directory, marked superseded.

Until then, a customer who needs their players' data in a specific jurisdiction is a customer
this deployment cannot serve, and saying so is better than an arrangement that half-works.

**A customer who will not send data to a third party at all** has a different answer, proposed
in [ADR 0052](../adr/0052-a-customer-hosted-deployment-is-a-whole-instance.md): the whole stack
runs as an instance in their own cloud account. Their game data never reaches PlayerSense. Only
a license and a signed count of tracked players do, which is aggregate and names nobody. It is
**Proposed, not built**, and it does not need the split described above. Until it is built, the
answer to this customer is the same as the one in the paragraph before it.
