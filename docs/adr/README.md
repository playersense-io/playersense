# Architecture decision records

A record here answers one question: **why is it like this, and what would break if it
weren't?**

It is written when the decision is made, while the alternatives are still fresh and the
reason is still embarrassing enough to be honest about. A decision reconstructed a year
later is a rationalisation, and the whole value of the record is that it is not one.

Each record carries an **Enforcement** section naming the code that makes the decision true
and the test that fails when it stops being true. A decision with no enforcement is a
preference, and saying so is more useful than pretending otherwise.

`project.md` remains the architecture: what the system is. These records are why it is that,
and what was rejected. Where the two disagree, `project.md` is wrong and should be fixed —
a record is never updated to match a drifting document, it is superseded by a new record.

## Status values

| Status | Meaning |
|---|---|
| Accepted | In force, and the code reflects it |
| Superseded by NNNN | Replaced; kept because the reasoning still explains the shape of the code |
| Proposed | Written, not yet built |

## Index

| # | Decision | Status |
|---|---|---|
| [0001](0001-storage-engines.md) | ClickHouse for events, PostgreSQL for state, Valkey for neither | Accepted |
| [0002](0002-cohorts-without-joins.md) | Cohorts are never computed with a join | Accepted |
| [0003](0003-cohort-facts-partitioned-by-activity-month.md) | Cohort facts are partitioned by activity month, not cohort month | Accepted |
| [0004](0004-installation-identity.md) | A reinstall is a new player, and the bias is documented rather than fixed | Accepted |
| [0005](0005-collection-policy-tiers.md) | A project declares a collection tier at creation, stamped onto every row | Accepted |
| [0006](0006-consent-carried-per-row.md) | Consent is stored on every event, not looked up at query time | Accepted |
| [0007](0007-data-classification-in-the-schema.md) | Every column carries a data class, and the build checks it | Accepted |
| [0008](0008-two-tier-control-plane.md) | The control plane is two databases under one rule | Superseded by 0038 |
| [0009](0009-cells-region-and-residency.md) | Region is a legal boundary, a cell is a stack inside it | Superseded by 0038 |
| [0010](0010-us-control-plane-eu-cell.md) | The control plane is US-hosted; the first cell is European | Superseded by 0038 |
| [0011](0011-api-key-format-and-storage.md) | A key is `public:private`, and only an Argon2id digest is stored | Accepted |
| [0012](0012-three-tier-key-resolution.md) | Keys resolve through three tiers and fail closed | Accepted |
| [0013](0013-an-event-bus-between-ingestion-and-storage.md) | Ingestion writes to a queue, never to ClickHouse | Accepted |
| [0014](0014-orm-for-postgres-sql-for-clickhouse.md) | An ORM for PostgreSQL, hand-written SQL for ClickHouse | Accepted |
| [0015](0015-rate-limiting-in-valkey.md) | Rate-limit counters live in Valkey | Accepted |
| [0016](0016-non-default-ports.md) | The local stack publishes no default port | Accepted |
| [0017](0017-aggregate-disclosure-threshold.md) | A thin aggregate is suppressed at query time, not at write time | Accepted |
| [0018](0018-writer-acknowledgement-and-deduplication.md) | The writer acknowledges after the insert, and splits deterministically | Accepted |
| [0019](0019-a-compiler-that-cannot-join.md) | The cohort compiler has no join to give | Accepted |
| [0020](0020-a-cohort-is-assigned-on-arrival.md) | A cohort is assigned when the server sees the batch | Accepted |
| [0021](0021-two-metric-tiers.md) | Two metric tiers: one immediate and approximate, one derived and exact | Accepted |
| [0022](0022-an-archive-object-is-named-by-its-contents.md) | An archive object is named by what is inside it | Accepted |
| [0023](0023-blocks-are-sized-for-merges-not-for-messages.md) | A block is sized for the merges, not for the message | Accepted |
| [0024](0024-the-hot-window-is-a-cache-not-a-retention-limit.md) | The hot window is a cache, not a retention limit | Accepted |
| [0025](0025-an-erasure-rewrites-the-archive.md) | An erasure rewrites an archive object rather than deleting it | Accepted |
| [0026](0026-compressed-json-and-derived-event-ids.md) | Compressed JSON with derived event ids, and not Protobuf | Accepted |
| [0027](0027-targeting-is-evaluated-on-the-server.md) | Targeting is evaluated on the server, and the client is told only the result | Accepted |
| [0028](0028-context-travels-as-a-timeline.md) | Context travels as a timeline and is materialized at ingestion | Accepted |
| [0029](0029-a-c-abi-over-an-opaque-handle.md) | A C ABI over an opaque handle, and unwinding panics | Accepted |
| [0030](0030-the-unity-surface-is-types-in-a-namespace.md) | The Unity binding: types in a namespace, no_backup, and importer settings in code | Accepted |
| [0031](0031-absence-is-null-not-the-empty-string.md) | Absence is NULL, not the empty string | Accepted |
| [0032](0032-a-liveops-event-carries-a-schedule.md) | A LiveOps event carries a schedule, and the server evaluates it | Accepted |
| [0033](0033-there-is-no-attribution-dimension.md) | There is no attribution dimension | Accepted |
| [0034](0034-a-rule-is-validated-when-it-is-written.md) | A rule is validated when it is written, not only when it is published | Accepted |
| [0035](0035-an-invitation-is-addressed-not-merely-held.md) | An invitation is addressed, not merely held | Accepted |
| [0036](0036-a-wallet-is-lots-because-credit-expires.md) | A wallet is lots, because credit expires | Accepted |
| [0037](0037-an-audit-log-records-decisions-not-requests.md) | An audit log records decisions, not requests | Accepted |
| [0038](0038-one-api-one-database.md) | One API and one database, until revenue says otherwise | Accepted |
| [0039](0039-players-are-charged-as-they-arrive.md) | Players are charged as they arrive, through a queue the wallet drains | Accepted |
| [0040](0040-stripe-funds-the-wallet.md) | Stripe funds the wallet, and PlayerSense stores no billing information | Accepted |
| [0041](0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md) | Automatic top-ups are queued by the charge; pricing is cached in two levels | Accepted |

## Template

```markdown
# NNNN. Title in the imperative

**Status:** Accepted
**Date:** YYYY-MM-DD
**Relates to:** project.md §N

## Context
What was true that forced a decision. Facts, not preferences.

## Decision
What was decided, stated so that a reader can tell whether the code obeys it.

## Consequences
What this costs, including the part nobody enjoys admitting.

## Alternatives considered
What was rejected and the specific reason, not a summary of its category.

## Enforcement
The code that makes it true and the test that fails when it stops being true.
```
