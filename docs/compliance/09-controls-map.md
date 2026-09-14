# 09. Controls map

**Obligation → mechanism → code → test.** This is the document to read if you want to know
whether a claim elsewhere in this directory is true.

A row with no test is a claim resting on review. A row marked *Designed* is not yet a claim
at all — it is an intention, and [10-open-items.md](10-open-items.md) lists them together so
nobody has to assemble that list themselves.

Paths are relative to the repository root.

## Lawful basis and consent

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| A row's lawful basis is provable, forever | Consent stamped onto every event as columns, never looked up at query time | `db/clickhouse/migrations/00001_events_raw.sql` | — | **Built** |
| No consent, no storage | Batch accepted and discarded; nothing published | `apps/backend/internal/handlers/ingest.go` | `TestConsentWithheldStoresNothing` | **Built** |
| Withdrawal can be scoped to a policy version | `consent_policy_version` on every row | `00001_events_raw.sql` | — | **Built** |
| ATT status recorded, not acted on | Column present; no branch reads it | `00001_events_raw.sql` | — | **Built** |

## Identifiers

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| No advertising identifier, ever | No field exists in the batch schema for one | `apps/backend/internal/schemas/ingest.go` | — | **Built** |
| No direct personal data about players | Property scan refuses email, phone and card patterns; the batch is rejected | `apps/backend/internal/ingest/normalize.go` | `TestDirectPersonalDataIsRefused` | **Built** |
| No fingerprinting | Nothing collects the attributes that would compose one | `internal/schemas/ingest.go` | — | **Built** |
| IP address never stored | Used for coarse country and rate-limit keying only | `internal/ingest/normalize.go` | — | **Built** |

## Children and platform rules

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| A kids_strict project has no persistent identifier | Gateway replaces `install_id` per batch, whatever the SDK sent | `apps/backend/internal/handlers/ingest.go` | `TestKidsStrictHasNoPersistentIdentifier` | **Built** |
| The tier is immutable after creation | Ent field marked immutable | `apps/backend/ent/schema/globaltier/project.go` | — | **Built** |
| The kids_strict limitation is stated at signup | The API announces it when a project is created on that tier | `apps/backend/internal/handlers/control.go` | `TestKidsStrictAnnouncesItsLimit` | **Built** |
| Apple privacy manifest, signed | Generated from the column classification | — | — | *Designed* |
| Play Data Safety reference | Generated from the same classification | — | — | *Designed* |

## Residency

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| No player identifier in the global tier | Two generated ORM clients — the type does not exist in the global package | `apps/backend/ent/generate.go` | compile | **Built** |
| …checked against the live database | `migrate verify` reads the catalog and fails on `class=identifier` in the global tier | `apps/backend/cmd/migrate/main.go` | manual negative test, documented below | **Built** |
| The control plane cannot fork into a cell database | Tier marker table; each migration refuses the other | `apps/backend/internal/pgdb/pgdb.go` | manual negative test, documented below | **Built** |
| A project's cell is real and active | Foreign key into the registry, refused at creation if unknown or not active | `apps/backend/internal/handlers/control.go` | — | **Built** |
| A project's cell never changes | Immutable required edge | `apps/backend/ent/schema/globaltier/project.go` | — | **Built** |
| A key from another cell is refused, with directions | 421 naming the correct `ingest_url` | `apps/backend/internal/handlers/ingest.go` | `TestAKeyFromAnotherCellIsRefused` | **Built** |
| A service cannot boot as a cell that disagrees with the registry | Start-up check; refuses a region mismatch, warns on an unreachable registry | `apps/backend/internal/core/cell.go` | — | **Built** |
| One cell cannot read another's cache | Every Valkey key carries the cell prefix | `apps/backend/internal/valkey/valkey.go` | `TestCellsDoNotShareACache` | **Built** |
| One cell cannot spend another's budget | Same prefix on rate-limit counters | `apps/backend/internal/valkey/ratelimit.go` | `TestCellsDoNotShareABudget` | **Built** |
| A message says which cell produced it | `Cell` on every bus message | `apps/backend/internal/eventbus/eventbus.go` | `TestTheAcceptedMessageCarriesItsCell` | **Built** |
| DPF or SCCs for EU→US transfer | Contractual | — | — | *Open* |

### The two negative tests, verbatim

Neither is a Go test, because both need a second database; both are reproducible in a
minute against the local stack.

```console
$ GLOBAL_POSTGRES_DSN=…/playersense_eu_1 go run ./cmd/migrate global up
migrate: migrate the global tier: wrong tier for this database: this database already
holds the cell tier, and a database holds exactly one

$ psql playersense_global -c "ALTER TABLE projects ADD COLUMN player_device_id text" \
    -c "COMMENT ON COLUMN projects.player_device_id IS 'class=identifier'"
$ go run ./cmd/migrate verify
  §99 player identifier in the global tier: public.projects.player_device_id
migrate: 1 invariant violation(s)
```

## Data classification

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Every column carries a class | `COMMENT` on every column, from a closed set | `apps/backend/internal/dataclass/`, every schema | `migrate verify` | **Built** |
| The class is read from reality, not from source | The verifier queries `pg_description` and `system.columns` | `apps/backend/cmd/migrate/main.go` | — | **Built** |
| Article 30 record generated from it | — | — | — | *Designed* |

## Credentials and access

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| No stored key can be presented as a credential | Argon2id digest of the private half only | `apps/backend/internal/core/apikey.go` | `TestCacheDigestRevealsNothing`, `TestVerifySecret` | **Built** |
| A key is shown once | Response contains the secret only at creation | `apps/backend/internal/handlers/control.go` | `TestAPIKeyIsDisclosedOnce` | **Built** |
| Every minted key is parseable | base32 random portion, separator-free | `apps/backend/internal/core/apikey.go` | `TestGeneratedKeysAlwaysParse` (300 iterations) | **Built** |
| A guessing flood is cheap to refuse | Malformed keys refused before any lookup or hash | `internal/core/apikey.go` | `TestMalformedKeysAreRefusedBeforeAnyLookup` | **Built** |
| Key class enforced per endpoint | Provider gates by kind | `apps/backend/internal/core/keyresolver.go` | `TestOnlyAProjectKeyMayIngest` | **Built** |
| Revocation is immediate | Tombstone on the public half, checked on every cache hit | `internal/core/keyresolver.go` | `TestRevocationTakesEffectImmediately` | **Built** |
| Digests upgrade as cost parameters rise | `NeedsRehash` on successful verification | `internal/core/apikey.go` | `TestNeedsRehash` | **Built** |
| Authentication fails closed | 503 when no tier can answer; never a success | `internal/core/keyresolver.go` | `TestAnUncachedKeyFailsClosedDuringAnOutage` | **Built** |
| Rate limiting fails closed | Storage error is returned, not swallowed | `apps/backend/internal/valkey/ratelimit.go` | `TestUnavailableStorageReportsAnError` | **Built** |
| Limits are shared across instances | Counters in Valkey | `internal/valkey/ratelimit.go` | `TestCountersAreSharedAcrossInstances` | **Built** |
| A limit window actually resets | `PEXPIRE` only on counter creation | `internal/valkey/ratelimit.go` | `TestWindowIsNotExtended` | **Built** |
| Nothing unauthenticated reaches the control plane | Guard on every non-operational route | `apps/backend/cmd/control-api/main.go` | `TestControlPlaneIsGuarded`, `TestIngestionIsGuarded` | **Built** |

## Integrity of what is stored

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| A `202` means durable queueing | Publish fsyncs before returning; a failure is a 503 | `apps/backend/internal/eventbus/local.go` | `TestAcceptedBatchReachesTheBus` | **Built** |
| One bad event does not fail a batch | Per-event validation with reasons | `apps/backend/internal/ingest/normalize.go` | `TestOneBadEventDoesNotFailTheBatch` | **Built** |
| A wrong device clock cannot poison a cohort | Timestamps clamped, original preserved | `internal/ingest/normalize.go` | `TestClientClockIsClamped` | **Built** |
| A redelivered batch lands once | `insert_deduplication_token` from the message's dedup key | — | — | *Designed* |
| A cohort query never joins | Cohort key and sticky dimensions stamped at write time | `db/clickhouse/migrations/00003_player_activity_daily.sql` | `cmd/cohortcheck` | **Built** |
| A rebuild cannot destroy older cells | Activity-month partitioning, whole-month rebuilds | `apps/backend/internal/cohort/derive.go` | `cmd/cohortcheck` | **Built** |

## Retention and rights

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Rows expire on the project's retention tier | `expire_at` MATERIALIZED, `TTL … DELETE`, `ttl_only_drop_parts` | `db/clickhouse/migrations/00001_events_raw.sql` | — | **Built** |
| Erasure is tracked to completion | `data_deletion_requests` with a status machine, in the cell tier | `apps/backend/ent/schema/regionaltier/regional.go` | — | **Built (schema)** |
| Erasure is executed | Tombstone, then asynchronous purge | — | — | *Designed* |
| Export and portability | Actor-scoped export job | — | — | *Designed* |
| A batch for a deleted project is not written | Project tombstone in the cell's Valkey | — | — | *Designed* |
| Restored backups have deletions reapplied | Operational procedure | — | — | *Designed* |

## Disclosure

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Thin aggregates are suppressed, not zeroed | Minimum cell size at the query layer | — | — | *Designed* |

## Audit

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Staff reads of customer data are logged with a reason | Admin console audit log, append-only, stored apart | — | — | *Designed* |
| Breach scoping is a query | Archive manifests recording project, environment and time range per object | — | — | *Designed* |
