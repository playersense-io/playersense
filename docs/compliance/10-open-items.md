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
**archive-writer** and the per-cell bucket are **built** — see [09](09-controls-map.md).
What is not:

| Item | Notes |
|---|---|
| **Compaction** | §10.4 and §10.7. A delivery is the flush boundary, so light traffic produces small objects. Nothing merges them yet |
| **The retention worker** | §60. `delete_after` is written on every manifest and nothing sweeps it, so no archive object has ever been expired |
| **Read-side deduplication** | A *partial* redelivery produces a second, overlapping object — see [ADR 0022](../adr/0022-an-archive-object-is-named-by-its-contents.md). The manifests make the overlap visible; §34's cold-query path has to resolve it on `event_id`, and §34 is not built |
| **Archive-before-expiry, enforced** | Appendix C.6 cannot be enforced: a ClickHouse TTL has no hook. `archivecheck` measures the margin and fails when it runs out, which is the strongest available form |
| **Project deletion reaching the archive** | §61's "rewrite or remove affected archive objects". The archive writer honours no project tombstone, deliberately — §61 assigns this to the compaction/deletion job |

`analytics-writer` and its insert deduplication are **built** — see
[09](09-controls-map.md).

## Engineering — disclosure and reporting

The **reporting API** and the **minimum cell size** are **built** — see
[09](09-controls-map.md). The threshold shipped with the API rather than after it, which is
what [07](07-aggregate-disclosure.md) asked for. Both families of question are answerable:
cohorts (§29) and the ordinary time series (§26, §27).

What it does not yet cover:

| Item | Notes |
|---|---|
| **Current-version and current-country breakdowns** | The daily summaries carry the *arrival* dimensions `player_activity_daily` holds, so a breakdown by `first_app_version` answers "how many active players installed on version X", not "how many are running it". A version-adoption chart needs a different source — see [ADR 0021](../adr/0021-two-metric-tiers.md) |
| **Session duration percentiles** | §27 asks for median/pXX. They need `sessions_final` (§30's sessionization), which is not built; the daily tier carries session counts and total playtime only |
| **Funnels, the event explorer and the player timeline** | §31, §3.5. The last of these is the one query that legitimately reads raw |
| **Progression buckets** | The enum member exists and nothing writes it. Progression is a game-specific milestone the SDK does not report, and deriving it would mean inventing a definition of progress on the studio's behalf |
| **Asynchronous cold queries and exports** | §34. The machinery §101's data-subject export also depends on |
| **Per-organisation concurrency quotas** | §32 asks for these alongside the per-caller rate limit, which is built |
| **§29.13's first two caps** | `first_app_version` is not capped to the top 20 values per project, and `acquisition_source` is not split from campaign. The third cap — only materializing pairs that exist — falls out of the GROUP BY. Alarm at 27M rows per project, per §29.13 |

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
