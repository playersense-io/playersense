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
| …and is told so rather than shown an empty chart | Cross-day retention is refused, naming the tier and pointing at same-day aggregates | `apps/backend/internal/query/plan.go` | `TestKidsStrictIsToldWhyRatherThanShownNothing` | **Built** |
| …including on the time series | New/returning and MAU are refused: every player would look new every day | `apps/backend/internal/query/metrics.go` | `TestKidsStrictHasNoNewReturningSplit` | **Built** |
| A child_directed project has no acquisition cohorts | The breakdown and the filter are both refused, naming the tier | `internal/query/plan.go` | `TestChildDirectedHasNoAcquisitionCohorts` | **Built** |
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
| …and a `202` is never answered for a batch storage cannot key | `batch_id` and a supplied `event_id` are validated as UUIDs at the gateway, where the SDK can see the refusal, rather than in the writer where it was a log line | `apps/backend/internal/schemas/ingest.go`, `internal/ingest/normalize.go` | `TestBatchIDMustBeAUUID`, `TestASuppliedEventIDMustBeAUUID` | **Built** |
| A compressed batch cannot exhaust the gateway | Two limits: the wire body is capped before anything is decompressed, and the decoded body by the route's own limit while binding | `apps/backend/internal/transport/decompress.go` | `TestADecompressionBombIsRefusedBeforeItIsRead`, `TestAnOversizedCompressedBodyIsRefusedOnTheWire` | **Built** |
| An SDK's bytes are the player's mobile data | gzip and zstd request bodies, and an event identity derived rather than sent: 290 bytes an event to 8.1 | `internal/transport/`, `internal/ingest/normalize.go` | `TestACompressedBatchIsAccepted`, `cmd/wiresize` | **Built** ([ADR 0026](../adr/0026-compressed-json-and-derived-event-ids.md)) |
| One bad event does not fail a batch | Per-event validation with reasons | `apps/backend/internal/ingest/normalize.go` | `TestOneBadEventDoesNotFailTheBatch` | **Built** |
| A wrong device clock cannot poison a cohort | Timestamps clamped, original preserved | `internal/ingest/normalize.go` | `TestClientClockIsClamped` | **Built** |
| A redelivered batch lands once | `insert_deduplication_token` derived from the batches in the block, split deterministically by partition and chunk | `apps/backend/internal/writer/writer.go` | `TestARedeliveredBatchLandsOnce` | **Built** (per delivery, not per batch — [ADR 0023](../adr/0023-blocks-are-sized-for-merges-not-for-messages.md)) |
| A message nothing can store does not stall the queue | Retried whole, then isolated, then dead-lettered — to a `.dead/` spool or §20's DLQ topic | `internal/writer/writer.go`, `internal/eventbus/` | `TestAPoisonedMessageIsParkedAndTheQueueMovesOn` | **Built** |
| …and an outage is not mistaken for poison | ClickHouse is probed before anything is parked, so attempt count alone never dead-letters | `internal/writer/writer.go` | `TestAnOutageDoesNotDeadLetterTheBacklog` | **Built** |
| A failed delivery does not kill the consumer | The subscription retries with backoff instead of returning | `internal/eventbus/local.go` | same two | **Built** |
| Storage keeps up at the scale envelope | Rows from many messages are coalesced into one block per ClickHouse partition, so inserts do not become a part per SDK batch | `internal/writer/writer.go`, `internal/eventbus/pubsub.go` | `TestADeliveryBecomesOneBlockPerPartition`, `TestABlockNeverSpansTwoPartitions` | **Built** |
| Nothing is acknowledged before it is durable | The handler returns nil only after the insert block is sent; the bus advances only on nil | `apps/backend/internal/writer/writer.go`, `internal/eventbus/local.go` | `TestABatchTravelsFromTheGatewayToClickHouse` | **Built** |
| Losing Valkey costs accuracy, not rows | The writer stamps null cohort keys and the nightly derivation backfills | `apps/backend/internal/writer/device.go` | `TestRowsAreStoredWithValkeyDown` | **Built** |
| One cell's data cannot reach another's ClickHouse | The writer drops a message whose cell is not its own | `apps/backend/internal/writer/writer.go` | `TestAMessageFromAnotherCellIsNotStored` | **Built** |
| A cohort query never joins | Cohort key and sticky dimensions stamped at write time | `db/clickhouse/migrations/00003_player_activity_daily.sql` | `cmd/cohortcheck` | **Built** |
| A cohort is assigned by the server, not the device | `cohort_date` from `ingest_time`; a wrong clock cannot make a 1970 cohort, and a late event cannot move a reported one | `apps/backend/internal/writer/device.go` | `TestABackdatedEventKeepsTodaysCohort` | **Built** |
| A breakdown divides by its own value's cohort | `cohort_sizes` carries a row per (dimension, value), counted within the value | `apps/backend/internal/cohort/derive.go` | `cmd/cohortcheck` check 9 | **Built** |
| Rolling retention counts a player once | A suffix sum over last-activity offsets, not a sum of per-offset counters | `apps/backend/internal/query/execute.go` | `TestRollingRetentionCountsAPlayerOnce` | **Built** |
| Daily actives are an exact distinct count | The derivation collapses partial rows before counting; a chained view would drift upward silently | `apps/backend/internal/rollup/derive.go` | `cmd/cohortcheck` checks 11 and 15 | **Built** |
| Monthly actives are not the sum of the days | MAU is its own distinct count over the month | `internal/rollup/derive.go` | `cmd/cohortcheck` check 14, `TestMonthlyActivesAreNotTheSumOfTheDays` | **Built** |
| The dashboard and the invoice read one fact | Both derive from `player_activity_daily`, per §38 | `internal/rollup/derive.go` | — | **Built** |
| A rebuild cannot destroy older cells | Activity-month partitioning, whole-month rebuilds | `apps/backend/internal/cohort/derive.go` | `cmd/cohortcheck` | **Built** |

## Retention and rights

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Rows expire on the project's retention tier | `expire_at` MATERIALIZED, `TTL … DELETE`, `ttl_only_drop_parts` | `db/clickhouse/migrations/00001_events_raw.sql` | — | **Built** |
| Archive objects expire on the project's retention tier | The retention worker sweeps `delete_after`, deletes the object and marks the manifest | `apps/backend/internal/retention/` | `TestAnExpiredObjectIsRemovedAndItsManifestKept` | **Built** |
| …and nothing expires early | Only a manifest past its own `delete_after` is swept, and one stamped with an impossible term is refused rather than obeyed | `internal/retention/retention.go` | `TestAnUnexpiredObjectIsLeftAlone`, `TestAManifestStampedBornExpiredIsRefused` | **Built** |
| …and an object still has to be billable after it is gone | The manifest row survives the object it described, carrying `object_size_bytes` | `internal/retention/retention.go` | `TestAnExpiredObjectIsRemovedAndItsManifestKept` | **Built** |
| Erasure is tracked to completion | `data_deletion_requests` with a status machine, in the cell tier | `apps/backend/ent/schema/regionaltier/regional.go` | — | **Built** |
| Erasure is executed | Tombstone, then asynchronous purge across `events_raw`, both device-keyed summaries, the cell tier's purchases and the archive | `apps/backend/internal/erasure/` | `TestAnErasureRemovesThePersonAndKeepsEveryoneElse` | **Built** |
| …reaching the archive without destroying anyone else's events | The object is read, the person's rows dropped, and what remains written back under a content-derived name; the old manifest is marked `redacted` | `internal/erasure/archive.go` | same, and [ADR 0025](../adr/0025-an-erasure-rewrites-the-archive.md) | **Built** |
| …and a request can be submitted at all | Cell-local surface on the reporting service, server key only | `apps/backend/internal/routers/privacy.go` | `TestAnErasureIsRecordedAndTombstonedInOneRoundTrip`, `TestAReportingKeyCannotEraseAnyone` | **Built** |
| …idempotently, because a studio will retry | An open request for the same actor is returned rather than duplicated | `apps/backend/internal/handlers/privacy.go` | `TestASecondErasureForTheSamePersonIsTheFirstOne` | **Built** |
| An erased actor's events stop being accepted | Actor tombstone in the cell's Valkey, checked per event rather than per message | `apps/backend/internal/writer/writer.go` | `TestAnErasedActorsEventsAreDropped` | **Built** |
| `completed` means nothing has arrived since | The purge re-runs for a drain window sized on the queue, and only then completes | `internal/erasure/erasure.go` | `TestCompletionWaitsForTheDrainWindow` | **Built** |
| An interrupted erasure can simply be run again | Content-addressed rewrites, idempotent mutations, and an order that deletes the events it resolves from last | `internal/erasure/` | `TestAnErasureIsIdempotent` | **Built** |
| A bounded archive pass resumes rather than skipping | The scan is bounded per pass and resumes by manifest id; a request that filled its bound cannot complete | `internal/erasure/archive.go` | `TestABoundedArchiveScanResumesRatherThanSkipping` | **Built** |
| Export and portability | Actor-scoped bundle of events, device records, daily activity and purchases, in the cell's own region | `apps/backend/internal/export/` | `TestAnExportHoldsTheSubjectAndNobodyElse` | **Built** |
| …without over-answering | The identifier is read narrowly for access and widely for erasure, so a bundle never carries a second person's events | `apps/backend/internal/subject/subject.go` | `TestAnExportHoldsTheSubjectAndNobodyElse` | **Built** |
| …and a truncated answer is recognisable | The bundle opens with its caveats and closes with a summary; a subject too large for one bundle is refused with a number | `internal/export/` | `TestABundleSaysWhatItIsAndWhereItStops`, `TestASubjectLargerThanOneBundleIsRefused` | **Built** |
| …and can be collected | Served through the API on the caller's own credential, never a signed bucket URL | `apps/backend/internal/handlers/privacy.go` | `TestAnExportCanBeCollected` | **Built** |
| An export does not outlive its term | `expires_at` on the request; the same job that writes the bundle removes it | `internal/export/export.go` | `TestABundleIsRemovedWhenItsTermIsUp` | **Built** |
| …and an erasure removes it sooner | The purge deletes any bundle held for the actor and marks the record | `internal/erasure/clickhouse.go` | `TestAnErasureRemovesTheExportBundleToo` | **Built** |
| One studio cannot reach another's request | Every read is scoped by the credential's project, and a miss is a 404 either way | `apps/backend/internal/handlers/privacy.go` | `TestOneStudioCannotReadAnothersRequest` | **Built** |
| A batch for a deleted project is not written | The writer reads a project tombstone from the cell's Valkey and drops the batch | `apps/backend/internal/writer/writer.go` | `TestABatchForADeletedProjectIsDropped` | **Built** |
| …and the tombstone is written when a project is deleted | Control plane, on deletion | — | — | *Designed* |
| A cache outage cannot resurrect deleted data | When Valkey cannot answer the batch **is** written and the purge removes it — the purge is the guarantee, the tombstone a fast path | `internal/writer/writer.go` | — | **Built** |
| Restored backups have deletions reapplied | Operational procedure | — | — | *Designed* |

## Disclosure

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Thin aggregates are suppressed, not zeroed | Minimum cell size applied to the response, `k = 5` by default | `apps/backend/internal/query/suppress.go` | `TestAThinCellIsSuppressedNotZeroed` | **Built** |
| …and a suppressed cell still rolls up | Suppression at query time, never in the derivation, so the stored cells stay complete | `internal/query/suppress.go` | `TestAThinBreakdownValueIsSuppressedButStillRollsUp` | **Built** |
| A thin cohort is withheld at every offset | The denominator gates the whole row | `internal/query/suppress.go` | `TestAThinCohortWithholdsEveryOffset` | **Built** |
| A thin payer subgroup inside a thick cell | The financial measures are dropped; the population number stays | `internal/query/suppress.go` | `TestAThinPayerCountDropsOnlyTheMoney` | **Built** |
| A genuine zero is still shown | Zero names nobody, and a gap cannot be told from a withholding | `internal/query/suppress.go` | `TestAZeroIsNotSuppressed` | **Built** |
| The response says what was withheld | `suppressed_cells` and `suppression_threshold` on every answer | `apps/backend/internal/protocol/query.go` | `TestTheResponseSaysHowFreshAndHowCensoredItIs` | **Built** |

## Query safety

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| No customer query runs without tenant scope | The compiler injects both predicates from the credential; `Compile` cannot be called without a `Scope` | `apps/backend/internal/query/plan.go` | `TestTenantScopeIsAlwaysInjected` | **Built** |
| A body cannot widen scope | `project_id`, `environment_id` and `org_id` are refused, not ignored | `apps/backend/internal/schemas/query.go` | `TestScopeInTheBodyIsRefused` | **Built** |
| No cohort query joins on a player key | The builder has no join method; `FROM` holds a closed type | `apps/backend/internal/query/sql.go` | `TestACohortQueryNeverJoins` (67 shapes) | **Built** |
| …and no dictionary stands in for one | Denominators are a second single-table read, divided in Go | `internal/query/plan.go` | same | **Built** |
| Only a reporting key may read analytics | Provider gates by kind; a project key ships in a game and is public | `apps/backend/internal/routers/query.go` | `TestOnlyAReportingKeyMayQuery` | **Built** |
| A query cannot read the node out from under everyone | `max_execution_time`, `max_rows_to_read`, `max_bytes_to_read`, `readonly` per query | `apps/backend/internal/query/execute.go` | — | **Built** |
| An unanswerable question is refused with a reason | 422 naming the fields, never an empty chart | `apps/backend/internal/handlers/query.go` | `TestTwoDimensionsAtOnceIsRefusedWithAReason` | **Built** |
| A cached answer cannot claim false freshness | `computed_at` is the derivation's own timestamp and is stored with the answer | `apps/backend/internal/query/cache.go` | `TestACachedAnswerKeepsTheDerivationsTimestamp` | **Built** |
| One cell cannot read another's answers | The cache key carries the cell | `internal/query/cache.go` | `TestOneCellCannotReadAnothersAnswers` | **Built** |
| Reporting honours residency | 421 naming the cell's `query_url` | `apps/backend/internal/handlers/query.go` | — | **Built** |
| A time series is suppressed on the same rule | An active-player count is a count of people whatever it is keyed on | `apps/backend/internal/query/execute.go` | `TestAMetricBreakdownSuppressesAThinValue` | **Built** |
| An approximate count says it is approximate | The realtime tiers are marked, and carry no derivation time | `apps/backend/internal/query/metrics.go` | `TestARealtimeSeriesSaysItIsApproximate`, `TestOnlyTheRealtimeTiersAreApproximate` | **Built** |
| A grain finer than its range is refused | §26's policy enforced, not advised | `apps/backend/internal/schemas/query.go` | `TestAGrainFinerThanItsRangeIsRefused` | **Built** |
| A series query never joins either | The same builder, over the summary tables | `apps/backend/internal/query/metrics.go` | `TestASeriesQueryNeverJoins` | **Built** |

## The archive

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| A canonical copy outlives the hot window | Compressed Parquet in the cell's own bucket, in its own region | `apps/backend/internal/archive/`, `internal/blobstore/` | `TestABatchBecomesAnArchiveObject` | **Built** |
| The lawful basis survives into the archive | Consent columns on every archived row, not only on the ClickHouse row | `internal/archive/row.go` | `TestABatchBecomesAnArchiveObject` | **Built** |
| A redelivered batch is archived once | The object is named by a digest of the batches inside it; the manifest conflicts | `internal/archive/writer.go` | `TestARedeliveredBatchArchivesOnce` | **Built** |
| Nothing is acknowledged before object *and* manifest are durable | The handler returns nil only after both | `internal/archive/writer.go` | — | **Built** |
| A slow archive cannot hold storage back | Its own subscription and its own position (§20) | `apps/backend/internal/eventbus/` | `TestTheArchiveHasItsOwnPosition` | **Built** |
| Storage is billed on a recorded size | `object_size_bytes` on the manifest, written with the object | `internal/archive/writer.go` | `TestTheManifestDescribesItsObject` | **Built** |
| Local data is not expired before it is archived | **Unenforceable** — a ClickHouse TTL is a declarative instruction to a background merge with no hook. Held by margin, and the margin is measured | `apps/backend/cmd/archivecheck` | `archivecheck` fails when a day would expire inside the warning window | **Measured, not enforced** |
| The archive stays in its cell's region | One bucket per cell; the region is recorded on every manifest | `internal/blobstore/gcs.go` | `TestTheManifestDescribesItsObject` | **Built** |

## Audit

| Obligation | Mechanism | Code | Test | Status |
|---|---|---|---|---|
| Staff reads of customer data are logged with a reason | Admin console audit log, append-only, stored apart | — | — | *Designed* |
| Breach scoping is a query | Archive manifests recording project, environment and time range per object | `apps/backend/ent/schema/regionaltier/regional.go`, `internal/archive/writer.go` | `TestTheManifestDescribesItsObject` | **Built** |
| …and the ranges are tight enough to be useful | An object holds one project, one environment and one hour; a batch spanning two is split | `internal/archive/writer.go` | `TestEachHourIsItsOwnObject` | **Built** |
