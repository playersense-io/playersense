# 01. Data inventory and classification

## The classification

Every column in all three databases carries one of five classes, in its column comment:

| Class | Meaning | Example |
|---|---|---|
| `identifier` | Identifies a person or device | `device_id`, `actor_key`, `player_id` |
| `behavioural` | What a person did | `event_name`, `event_time`, `session_id` |
| `derived` | Computed from behaviour, still attributable | `cohort_date`, `days_active` |
| `financial` | Revenue, purchases, entitlements | `revenue_micros`, `amount_micros` |
| `none` | Organisation, project, infrastructure | `project_slug`, `ingest_url` |

`migrate verify` reads them back out of `pg_description` and `system.columns.comment`. A
column with no classification, or one outside the set, fails. It reads the **catalog**, not
the schema files, because the question is what the database actually holds.

This is the single source the Article 30 processing record, the Play Data Safety
declaration and Apple's privacy manifest are generated from. Maintained by hand those three
drift apart within a release; generated, they cannot describe a field that does not exist
or omit one that does. See ADR 0007.

Current state, from a live `migrate verify`:

```text
postgres     319 columns / 26 tables, all classified
clickhouse   261 columns / 20 tables, all classified
```

## What is collected about a player

Everything below arrives in a batch envelope from the SDK. Nothing is inferred, enriched or
bought.

### Identifiers

| Field | Class | Notes |
|---|---|---|
| `install_id` | identifier | Random UUID, generated on first launch, deleted with the app. A reinstall produces a new one (ADR 0004) |
| `actor_key` | identifier | The studio's own player identifier, **if the studio supplies one**. Optional |
| `session_id` | identifier | Scoped to one play session |

No advertising identifier. No vendor identifier. No fingerprint. No identifier shared across
apps or vendors. These are permanent constraints, not defaults (ADR 0004).

Under the `kids_strict` policy tier there is **no persistent identifier at all**: the
gateway replaces the installation identifier with a value scoped to a single batch, before
anything downstream sees it ([04](04-children-and-platform-rules.md)).

### Behaviour

| Field | Class |
|---|---|
| `event_name`, `event_type` | behavioural |
| `event_time`, `client_event_time` | behavioural |
| `session_id` | identifier |
| Typed event properties (`props_string`, `props_int`, `props_bool`, `props_float`) | behavioural |

Event properties are **studio-defined**, which is the one place a studio could put something
it should not. The ingestion path scans them and **refuses** batches carrying direct
personal data — email addresses, phone numbers, card numbers. A studio that sends an email
address as a property would otherwise make PlayerSense a processor of data neither party
intended it to hold.

### Device and app context

| Field | Class |
|---|---|
| `platform`, `os_version`, `device_model` | behavioural |
| `app_version`, `app_build` | behavioural |
| `country` | behavioural — coarse only, derived from the request address. **The derivation is currently a stub returning empty**, so no country is stored today. The address itself is never stored either way |
| `sdk_name`, `sdk_version` | none |

### Consent

| Field | Class |
|---|---|
| `consent_analytics` | behavioural |
| `consent_policy_version` | behavioural |
| `att_status` | behavioural — recorded, not acted on (ADR 0004) |

### Financial

| Field | Class |
|---|---|
| `revenue_micros`, `currency` | financial |
| `store_transaction_id` | identifier |

## Where each thing lives

### ClickHouse

Events and everything derived from them. Never any account data.

```text
events_raw                       every accepted event, TTL by retention tier
player_lifetime                  first/last seen and sticky install dimensions, per device
player_activity_daily            one row per device per active day, carrying its cohort key

cohort_sizes                     denominators
cohort_metrics_daily             published cohort numerators
cohort_metrics_breakdown_daily   the same, split by one sticky dimension at a time
cohort_bucket_daily              playtime and spend buckets

events_1m, events_1h             realtime event summaries; approximate player counts
metrics_daily                    active players, sessions, events and revenue per day
metrics_breakdown_daily          the same, split by one dimension at a time
metrics_monthly                  monthly actives, its own distinct count

*_staging                        rebuild targets, published by REPLACE PARTITION
```

### PostgreSQL — exactly one

Accounts, organisations, projects, environments, API keys, settings, retention policies,
billing, feature flags and aggregate usage counts, alongside the four tables that name a
player:

```text
verified_purchases       player_id, store_transaction_id
data_deletion_requests   actor_key
data_export_requests     actor_key
archive_manifests
```

The two request tables outlive the data they name, and deliberately. Proving an erasure
happened needs a record that it happened ([06](06-retention-and-deletion.md)), and that
record has to carry the identifier it was about. Neither holds anything else about the
person: what an erasure removed is recorded as counts, not as a copy of what was removed.

These four used to sit in a separate database, under the rule that a player identifier
never appeared beside the account records. That split is gone
([ADR 0038](../adr/0038-one-api-one-database.md)); the classification that made it
checkable is not, and it is what keeps this inventory accurate
([03](03-residency-and-transfers.md)).

### Valkey — ephemeral

Key cache entries, rate-limit counters, device state for cohort stamping, revocation
tombstones. Everything in it expires and everything in it can be rebuilt.

### Google Cloud Storage

Cold archive of raw events, as compressed Parquet. Objects expire on
the project's retention tier, swept from the manifests rather than by a bucket lifecycle rule
— retention is a per-project term and a lifecycle rule can only express one age for the whole
bucket ([06](06-retention-and-deletion.md)).

Under a separate `export/` prefix, the bundles produced by an access request
([05](05-data-subject-rights.md)). Each is a copy of everything held about one person, so
each has its own short expiry and is removed sooner if that person asks to be erased.

## What is never stored

- Card numbers or any payment instrument — Stripe holds those.
- IP addresses. The request address is used to key rate limits and, once the derivation is
  implemented, to produce a coarse country. It is not written to any table, and the
  function that reads it returns a country rather than an address by construction.
- Email addresses or phone numbers of **players**. Refused at ingestion.
- Advertising identifiers.
- Precise location, health, biometric, or any other special-category data.
