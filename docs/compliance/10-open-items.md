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

The actor tombstone, the purge and the export are **built** — see [09](09-controls-map.md).
An erasure is submitted on the API's privacy routes, tombstoned inside that request, and
removed asynchronously from `events_raw`, both device-keyed summaries, the purchases, any
export bundle, and the archive objects holding the person
([ADR 0025](../adr/0025-an-erasure-rewrites-the-archive.md)).

What it does not yet cover:

| Item | Notes |
|---|---|
| **Project tombstone written on deletion** | The writer already honours one and drops the batch; nothing writes it yet, so today a deleted project's queued batches are removed by the purge rather than skipped |
| **Restore-and-redact procedure** for backups | Written procedure plus the job that reapplies pending deletions |
| **Multiple identifiers on one request** | A request names one actor key or one install. A person who reinstalled has several and the studio has to submit one request per identifier, which is a worse experience than it needs to be — the machinery already handles each of them correctly |
| **A bundle larger than one object** | An export is written as a single object and bounded by `EXPORT_MAX_ROWS`; a subject past the bound is refused with the number rather than truncated. Splitting across objects is the fix, and it needs the request to name more than one path |

## Engineering — storage lifecycle

**archive-writer**, the archive bucket and **§60's retention worker** are **built** — see
[09](09-controls-map.md). Every manifest's `delete_after` is swept on the worker's lifecycle
loop, the object is deleted and the row is kept so the days it existed remain billable.

What it does not yet cover:

| Item | Notes |
|---|---|
| **Compaction** | §10.4 and §10.7. A delivery is the flush boundary, so light traffic produces small objects. Nothing merges them yet. When it is built it should reuse the erasure's read-filter-write path rather than the other way round |
| **Read-side deduplication** | A *partial* redelivery produces a second, overlapping object — see [ADR 0022](../adr/0022-an-archive-object-is-named-by-its-contents.md). The manifests make the overlap visible; §34's cold-query path has to resolve it on `event_id`, and §34 is not built |
| **Archive-before-expiry, enforced** | Appendix C.6 cannot be enforced: a ClickHouse TTL has no hook. `archivecheck` measures the margin and fails when it runs out, which is the strongest available form |
| **Project deletion reaching the archive** | §61's "rewrite or remove affected archive objects", for a whole project rather than one person. The per-actor path is built; this is the same machinery with a different predicate and no object left to write back |

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
| **Asynchronous cold queries** | §34. §101's data-subject export no longer waits on it: [ADR 0024](../adr/0024-the-hot-window-is-a-cache-not-a-retention-limit.md) put the whole retention tier back inside `events_raw`, so the export reads the table rather than the objects — see [05](05-data-subject-rights.md) for the residue that leaves |
| **Per-caller concurrency limits** | Rate limits are built and documented in [rate-limits.md](../rate-limits.md) — per device, per address-in-project and per project on the SDK paths, and on the dashboard exactly one: sending an invitation, per address and per organization, failing closed because a message cannot be taken back. Authentication emails go directly to Firebase, which enforces sending protections. What is not built is a *concurrency* limit as distinct from a rate one: a request in flight is not counted. `query-api`'s per-query resource bounds are what stops a slow query holding a connection |
| **§29.13's first cap** | `first_app_version` is not capped to the top 20 values per project. The second cap no longer applies — there is no acquisition dimension to split from campaign ([ADR 0033](../adr/0033-there-is-no-attribution-dimension.md)) — and the third, only materializing pairs that exist, falls out of the GROUP BY. Alarm at 27M rows per project, per §29.13 |

## Engineering — billing

The **usage meter**, the **wallet** and its **expiry sweep** are built. A day is derived from
`metrics_daily` — the same fact the dashboard reads, which is what §38 asks for — rewritten until it
settles and marked final at eight days. Credit is held as lots, spent promotional-first and
oldest-purchase-next, and lapsed credit is written off one statement line at a time
([ADR 0036](../adr/0036-a-wallet-is-lots-because-credit-expires.md)).

What it does not yet cover:

| Item | Notes |
|---|---|
| **The month close, and Stripe** | `wallet.Spend` is called by an operator rather than by a scheduled job, because a job needs overage *rates* and §37 forbids hardcoding commercial values. Nothing reports a meter to Stripe yet either, so §40's seam exists on our side only |
| **Who granted credit** | The operator surface is guarded by the shared operator token, so `granted_by_user_id` is null on that path and "who gave this account $100" has no answer inside the product. It needs the audit log below and §56's console. Every grant does carry a caller-supplied reference, and no customer credential can reach the route at any permission |
| **Whether non-production traffic is billable** | `usage_daily` is keyed by project and has no environment column, so development and staging count toward the meter. A commercial decision the schema made before the meter existed; changing it is a WHERE clause in `internal/metering` |
| **Monthly tracked players, exactly** | The usage page reports the busiest day rather than a sum, because adding daily figures counts one player once per day. §36's meter is the distinct monthly figure, which `metrics_monthly` holds and the month close will read |
| **Subscription fees and refunds** | `subscription` and `refund` are ledger kinds nothing writes. The plan's `monthly_price_micros` is stored and never charged |

## Engineering — audit and access

Who is in an organization and what they may do **is** built: roles, the project access list,
invitations and removal, on `control-api` under `member.*` — with role assignment reserved to the
two system roles rather than made grantable
([ADR 0035](../adr/0035-an-invitation-is-addressed-not-merely-held.md)). §08's access review reads
the memberships and their grants, which is the question "who can reach this data".

The **audit log** is built. Every route that changes something writes an entry:
memberships, roles, project access, keys, invitations, configuration, publishes, credit and the
billing settings. Entries are append-only by construction — every field on the row is immutable and
the mixin omits `updated_at`, so ent generates an update builder with no setter on it at all, and
§08's "a log the operator can edit proves nothing" is a compile error rather than a policy.

**Every entry commits in the same transaction as the change it describes.** The four routes whose
domain call opened its own transaction — accepting an invitation, changing project access, sending
one, and creating an organization — were refactored to accept a hook that runs inside that
transaction instead, right before commit, rather than writing separately afterwards. There is no
longer a route that writes an audit entry outside the transaction it describes.
It is readable at `GET /organizations/{org}/audit` under `audit.read`, which only the two system
roles hold ([ADR 0037](../adr/0037-an-audit-log-records-decisions-not-requests.md)).

What it does not yet cover:

| Item | Notes |
|---|---|
| **Held apart from the data it describes** | §08 asks for this and neither log satisfies it. `audit_events` always sat in the same database as the organizations and memberships it describes; `staff_access` used to be in a separate database under a separate jurisdiction and, since [ADR 0038](../adr/0038-one-api-one-database.md), no longer is. Append-only removes the edit; it does not remove the delete, and an operator with that database can still drop rows |
| **Who granted credit, verifiably** | The operator surface is guarded by the shared token, so the actor is a **self-asserted** name the entry labels as such. Required, so every grant carries one, and no customer credential can reach the route — but a name somebody typed is not proof, and what replaces it is a verified staff identity |
| **Admin console** with per-read reason capture | The highest-risk surface in the system; the rules it must be built under are in [08](08-security-and-sub-processors.md). `staff_access` is the table it will write, and nothing writes it yet |
| **Staff access review** | Who can reach the deployment, and the record of when they did. Distinct from a customer's own member list, which is built: this is PlayerSense's staff, not a studio's |
| **Retention and export of the log** | Entries go when their organization does, because of the foreign key. Nothing bounds how long they are kept while it exists, and nothing lets a customer export them |
| **Custom roles** | The schema supports them — a role is a row with a permission set, and `system` is what separates the two seeded ones. Nothing creates or edits one yet, so an organization has the six it was seeded with, renameable but not extensible |

## SDKs

The **shared core, the C ABI over it and the Unity package are built.** The protocol
[§13 asks for](../sdk/protocol.md) sessions, identity, consent transitions, the disk spool,
retries and a 35-case conformance suite, and every case is covered. Most are proved once in
`sdk/core` against a fake host; the three §13 says cannot be are where it says they belong —
case 3 (the identifier is outside a platform backup) and the language-boundary half of case 31
in `sdk/unity/Tests/Runtime/BoundaryTests.cs`, and case 8, one set of rows from two 202s, in the
writer's own deduplication tests. The Unity package is at `0.1.0`, **unreleased**.

Platform coverage is the live constraint and is recorded where it can be kept current, in
[docs/sdk/platforms.md](../sdk/platforms.md): iOS, Android and Apple-silicon macOS ship; Windows
x64, Linux x64 and Intel macOS do not, which is a Steam release rather than a compliance
question. **Unreal and Godot are not built.**

What this changes for the two store artifacts below is their urgency, not their status. They
were previously waiting on there being an SDK at all. There is one, and it builds for iOS today
— so each is now a release blocker for **a customer** with nothing in front of it:

| Item | Consequence of omission |
|---|---|
| **`PrivacyInfo.xcprivacy`, signed**, in the Unity package | App Store review rejects *the customer's app*. Nothing in the repository declares one |
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
