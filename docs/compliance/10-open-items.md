# 10. Open items

What is **designed and not built**, listed by name. A compliance document that describes
unbuilt controls is worse than none, because it is the document somebody will rely on.

Ordered by what would be needed first if a customer asked today.

## Contractual

| Item | Why it matters | Blocks |
|---|---|---|
| **DPA template** for customers | PlayerSense is a processor; the DPA is the instrument that says so | Any EU customer |
| **Standard contractual clauses** in that DPA, with a transfer impact assessment | The basis for EU personal data reachable from the United States | Any EU customer |
| **EU–US Data Privacy Framework** self-certification | A second, independent basis. Kept alongside SCCs rather than instead of them, because the framework's two predecessors were both invalidated | Nothing, but it shortens every procurement conversation |
| **Sub-processor list published**, with a notification mechanism | Customers must be told before one is added | Any EU customer |
| **Article 30 processing record** | Generated from the column classification, which exists | An audit |
| **Privacy policy and public documentation** of what the SDK collects | — | Launch |

None of these is architectural. They are listed here because the architecture is what makes
them writable without guessing, and because a compliance directory that only covers
engineering is the wrong shape.

## Engineering — the erasure path

The schema exists (`data_deletion_requests`, with its status machine, in the cell tier). The
machinery does not.

| Item | Notes |
|---|---|
| **Actor tombstone** | Stops the data being *used* immediately, while removal takes hours |
| **Purge job** | Across `events_raw`, `player_lifetime`, `player_activity_daily`, the cell tier's purchases, and the archive |
| **Export job** | Access and portability, actor-scoped |
| **Project tombstone written on deletion** | The writer already honours one and drops the batch; nothing writes it yet, so today a deleted project's queued batches are removed by the purge rather than skipped |
| **Restore-and-redact procedure** for backups | Written procedure plus the job that reapplies pending deletions |

## Engineering — storage lifecycle

| Item | Notes |
|---|---|
| **archive-writer** and the per-cell bucket | The cold copy, and the manifests breach scoping depends on |
| **Archive-before-expiry ordering** | Appendix C.6. Local data must not be expired before archive durability is confirmed |

`analytics-writer` and its insert deduplication are **built** — see
[09](09-controls-map.md).

## Engineering — disclosure and reporting

| Item | Notes |
|---|---|
| **Reporting API** | Not built |
| **Minimum cell size** at the query layer | Must be built *with* the reporting API, not after it — see [07](07-aggregate-disclosure.md) |

## Engineering — audit and access

| Item | Notes |
|---|---|
| **Audit log**, append-only, stored apart from the data it describes | — |
| **Admin console** with per-read reason capture | The highest-risk surface in the system; the rules it must be built under are in [08](08-security-and-sub-processors.md) |
| **Staff access review** | Who can reach a cell, and the record of when they did |

## SDKs

None of the SDKs is built. Two of their artifacts are release blockers for **customers**
rather than for PlayerSense, which makes them more urgent than their position in a roadmap
suggests:

| Item | Consequence of omission |
|---|---|
| **`PrivacyInfo.xcprivacy`, signed**, in the iOS and Unity SDKs | App Store review rejects *the customer's app* |
| **Play Data Safety reference**, published | The customer cannot complete their own declaration without guessing |

Both are generated from the column classification, which exists — see
[01](01-data-inventory.md).

## Explicitly not planned

| Item | Why |
|---|---|
| SOC 2 / ISO 27001 | Programmes rather than architecture. Startable when a customer requires one |
| PCI DSS beyond Stripe's coverage | No card data reaches PlayerSense |
| China PIPL | Needs a domestic entity and domestic infrastructure. Better out of scope than half-designed |
| Age verification | The studio determines whether its game is child-directed. A processor guessing at a player's age would collect more data in order to collect less |
