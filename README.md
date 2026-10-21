# PlayerSense

> [!WARNING]
> **Under active development.** APIs, schemas, and SDK surfaces are still evolving and are not yet ready for production use.

Game analytics and LiveOps platform. `project.md` is the architecture source of truth; `docs/adr/` records why it is that way, and `docs/compliance/` maps obligations onto the code that meets them.

## Layout

```
apps/backend/     Go services and commands (module playersense.dev/backend)
apps/client/      Customer dashboard (Nuxt)
apps/admin/       Operations console for PlayerSense staff (Nuxt)
apps/landing/     Marketing and pricing site (Nuxt)
packages/         Shared TypeScript contracts, dashboard components, and UI primitives
sdk/core/         The SDK protocol in Rust, once, with no I/O of its own
sdk/ffi/          The C ABI over it, for every binding
sdk/unity/        The Unity package: a host, a P/Invoke layer and the API a game calls
db/clickhouse/    Goose migrations for the analytics and logging schema
db/postgres/      Ent owns the PostgreSQL schema
docs/adr/         Architecture decision records
docs/compliance/  Obligation -> mechanism -> code -> test
docs/sdk/         The language-neutral SDK protocol every client implements
infra/stage1/     Local compose stack
```

Requires Go 1.27 (Muzak uses generic methods and `encoding/json/v2`), Node >= 20 with `pnpm`, and Rust for the SDK.

## Local stack

Ports are deliberately **not** the service defaults — a `21xxx` block, below the macOS ephemeral range so nothing collides with an OS-assigned port. Change them in `apps/backend/.env` and nowhere else.

| Service | Port |
|---|---|
| PostgreSQL | 21432 |
| ClickHouse HTTP | 21123 |
| ClickHouse native | 21900 |
| Valkey | 21379 |

Each app keeps its own settings beside it: `apps/backend/.env` (which the local stack reads too), `apps/client/.env` and `apps/admin/.env`, each copied from the `.env.example` next to it.

```bash
cp apps/backend/.env.example apps/backend/.env
cp apps/client/.env.example apps/client/.env
cp apps/admin/.env.example apps/admin/.env
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml
export COMPOSE_ENV_FILES=apps/backend/.env
docker compose up -d
```

The cold volume of the hot/cold policy is a second local directory by default, which exercises the policy and the part movement for real. To point it at a GCS bucket instead, set `CH_COLD_ENDPOINT` and the HMAC pair in `apps/backend/.env` and add the overlay:

```bash
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml:infra/stage1/compose/docker-compose.cold-object.yml
```

It is an overlay rather than a flag because ClickHouse constructs every declared disk eagerly: an S3 disk with an empty endpoint is not an absent disk, it is a server that will not start.

## Migrations

Two databases: **PostgreSQL** for accounts, projects, keys, billing and the records that name a player, and **ClickHouse** for events and logs.

```bash
go run ./apps/backend/cmd/migrate up          # postgres, then clickhouse
go run ./apps/backend/cmd/migrate postgres    # postgres only
go run ./apps/backend/cmd/migrate up ch       # clickhouse only
go run ./apps/backend/cmd/migrate status      # which ClickHouse migrations are applied
go run ./apps/backend/cmd/migrate verify      # §96 classification, across both
go run ./apps/backend/cmd/migrate reset       # drop everything, Keeper metadata included
go run ./apps/backend/cmd/migrate backfill events_daily sessions   # fill new rollups from history
```

A materialized view only sees inserts made after it exists, so a rollup added by a migration starts empty. `backfill` runs each named view's own SELECT over the history already in `events_raw`, once, and fills anything chained from it the same way; it refuses a table that already holds rows, since running over those would count them twice. Stop analytics-writer while it runs.

`verify` reads the live catalog rather than the schema files, which is what makes it proof rather than documentation.

## Services

### api — `:21080`

```bash
go run ./apps/backend/cmd/api
```

One binary and one address ([ADR 0038](docs/adr/0038-one-api-one-database.md)):

- **Dashboard & Control Plane** — Organizations, roles and permissions, invitations, projects, API keys, LiveOps (segments, feature flags, scheduled events, experiments, versioned config drafts and rollbacks), custom report builder, notification channels and routing rules ([ADR 0051](docs/adr/0051-rules-route-events-to-channels.md)), outbound webhooks ([ADR 0049](docs/adr/0049-webhooks-hold-only-what-is-owed.md)), and an append-only decision audit log ([ADR 0037](docs/adr/0037-an-audit-log-records-decisions-not-requests.md)). Authenticated with Firebase ID tokens.
- **Ingestion & Player Config** — High-throughput gzip/zstd event batch and log/crash stream ingestion ([ADR 0026](docs/adr/0026-compressed-json-and-derived-event-ids.md), [ADR 0050](docs/adr/0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md)), server-evaluated player targeting and configuration ([ADR 0027](docs/adr/0027-targeting-is-evaluated-on-the-server.md)), and receipt verification across App Store, Google Play and Steam ([ADR 0048](docs/adr/0048-verified-purchases-win.md)).
- **Reporting & Analytics** — Pre-aggregated cohort, metric, funnel, session, revenue, log and experiment queries. Every page reads a fact built for it without joins ([ADR 0046](docs/adr/0046-every-page-reads-a-fact-built-for-it.md)) and enforces query-time disclosure thresholds ([ADR 0017](docs/adr/0017-aggregate-disclosure-threshold.md)).
- **Billing** — Lot-based credit wallets ([ADR 0036](docs/adr/0036-a-wallet-is-lots-because-credit-expires.md)), graduated rate cards, Stripe Checkout top-ups, automatic off-session top-ups, subscriptions ([ADR 0040](docs/adr/0040-stripe-funds-the-wallet.md), [ADR 0042](docs/adr/0042-plans-are-stripe-subscriptions.md)), and monthly invoicing for contracted organizations ([ADR 0045](docs/adr/0045-contracted-accounts-are-invoiced-monthly.md)).
- **Privacy** — Data-subject erasure and export requests (§101).
- **Operations Console** — Staff endpoints for tenant support (`X-Staff-Ticket`, `X-Staff-View-As`), catalogue and plan management, and pipeline/ClickHouse health. First admin is seeded via CLI:
  ```bash
  go run ./apps/backend/cmd/staff add you@playersense.dev --roles admin
  go run ./apps/backend/cmd/staff list
  ```

The full HTTP contract is served at `GET /openapi.json` (or emitted without starting a server via `go run ./apps/backend/cmd/api -openapi`), and TypeScript bindings in `packages/contracts` are generated directly from it (`pnpm contracts`).

### analytics-writer — `:21082` (health probe only)

```bash
go run ./apps/backend/cmd/analytics-writer
```

Consumes the event and log queues on separate subscriptions and stores batches in ClickHouse, stamping each event's cohort key on arrival so no cohort query ever needs a join ([ADR 0020](docs/adr/0020-a-cohort-is-assigned-on-arrival.md)) and fingerprinting log issues ([ADR 0050](docs/adr/0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md)). It never touches PostgreSQL.

### archive-writer — `:21084` (health probe only)

```bash
go run ./apps/backend/cmd/archive-writer
```

Consumes the event stream on its own subscription and writes the canonical Parquet archive, with a manifest per object in PostgreSQL. `ARCHIVE_STORE=local` writes under `ARCHIVE_DIR`; `ARCHIVE_STORE=gcs` writes to the bucket.

### billing-writer — `:21088` (health probe only)

```bash
go run ./apps/backend/cmd/billing-writer
```

Charges tracked players as they arrive on its own subscription ([ADR 0039](docs/adr/0039-players-are-charged-as-they-arrive.md)): counts who is new this month, queues what they cost on the plan's rate card, and settles the queue into wallets ([ADR 0044](docs/adr/0044-usage-settles-when-it-matters.md)). With `STRIPE_SECRET_KEY` set, it also runs automatic off-session top-ups ([ADR 0041](docs/adr/0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md)) and closes contracted organizations' months into Stripe invoices ([ADR 0045](docs/adr/0045-contracted-accounts-are-invoiced-monthly.md)). Ingestion never waits for it.

### worker — `:21086` (health probe only)

```bash
go run ./apps/backend/cmd/worker        # long-running: both loops, on their schedules
go run ./apps/backend/cmd/worker once   # one pass of each, then exit
```

Two loops on two cadences:

- **The derivation**, nightly at `WORKER_DERIVE_AT`. Rebuilds the cohort facts, their denominators, the behavioural buckets, the daily/monthly metrics, complete sessions by day, the lifecycle split of returning players and each experiment arm's outcomes: everything the reporting routes serve that a materialized view cannot build.
- **The lifecycle pass**, every `WORKER_JANITOR_EVERY`. Runs archive expiry (§60), data-subject erasures including archive Parquet rewrites ([ADR 0025](docs/adr/0025-an-erasure-rewrites-the-archive.md)), data-subject exports (§101), scheduled report runs, store refund sweeps, log metering and issue spike detection ([ADR 0050](docs/adr/0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md), [ADR 0051](docs/adr/0051-rules-route-events-to-channels.md)), and the daily staff growth digest ([ADR 0053](docs/adr/0053-staff-hear-of-the-business-through-two-slack-channels.md)).

### Frontend apps

```bash
pnpm install
pnpm dev:landing   # http://localhost:3000
pnpm dev:client    # http://localhost:3001
pnpm dev:admin     # http://localhost:3002
```

When changing API routes or schemas, regenerate the TypeScript client contract:

```bash
pnpm contracts
pnpm contracts:check
```

## SDK

The protocol is implemented **once**, in Rust, with no I/O of its own: sessions, consent, batching, the disk spool, retry and backoff, deduplication and the response table. A binding supplies a host — a clock, a file, an HTTP call — and a facade in its own idiom, so the same core runs on every platform rather than being reimplemented five times and drifting.

```bash
cd sdk && cargo test                  # the protocol, and the ABI over it
cd sdk && ./build-native.sh           # the native libraries, and what they export
cd sdk/unity && ./install-plugins.sh  # put them in the Unity package
cd sdk/unity && ./verify.sh           # compile the package and run its tests, in real Unity
```

`sdk/unity` is the first binding. A game pastes its project key into Project Settings and writes one line:

```csharp
PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12 });
PlayerSense.Context.Player.Set("rank", "diamond");
```

No `await`, no callback, no result to check, nothing to wrap in `try`/`catch` ([sdk/unity/README.md](sdk/unity/README.md)). The native libraries are not committed — an unstripped iOS archive is 21 MB against ~550 KB of linked code — so a checkout builds them with the two scripts above.

## Storage tiers

`events_raw`, `logs`, and `player_activity_daily` sit on a **hot/cold storage policy**, so the §33 hot window is cache residency rather than a retention limit: old parts move off local NVMe and stay queryable in place ([ADR 0024](docs/adr/0024-the-hot-window-is-a-cache-not-a-retention-limit.md)). Retention is unchanged and still per project — `expire_at` is derived from the row's own retention tier.

| Table | Moves to cold after | Why |
|---|---|---|
| `logs` | 2 days | High volume; rollups stay on hot disk, only old text searches reach cold ([ADR 0050](docs/adr/0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md)) |
| `events_raw` | 7 days | The largest analytics table; nothing on the dashboard path reads it |
| `player_activity_daily` | 90 days | §33.1's other big one. Three times clear of the derivation's two-month rebuild window |
| `player_lifetime` | never | `player_lifetime_v` is on the query path (§29.10) |

```bash
# which volume each partition is on
docker exec playersense-clickhouse clickhouse-client --port 9000 \
  -u playersense --password playersense_local -d playersense \
  -q "SELECT partition, disk_name, sum(rows) FROM system.parts
      WHERE table='events_raw' AND active GROUP BY partition, disk_name ORDER BY partition"
```

Locally `cold` is a second directory. In production it is a GCS bucket — see `infra/stage1/compose/clickhouse/config.d/storage.xml`, which carries the disk definition and reads its credentials from the environment.

## Tests

```bash
go test ./apps/backend/... -race
pnpm contracts:check && pnpm lint && pnpm typecheck && pnpm build
cd sdk && cargo test                      # the SDK protocol and its C ABI
cd sdk && cargo clippy --all-targets      # --all-targets, or the test fixtures go unlinted
cd sdk && ./build-native.sh               # native libraries, and what they export
go run ./apps/backend/cmd/cohortcheck     # 15 correctness checks against real derived data
go run ./apps/backend/cmd/archivecheck    # Appendix C.6: is anything about to expire unarchived?
go run ./apps/backend/cmd/wiresize        # what a batch weighs, four ways
```

Miri covers the C ABI's pointer and handle code:

```bash
cd sdk && cargo +nightly miri test -p playersense-ffi --test abi -- \
  --skip a_host_that_calls_back --skip a_spooled_batch_survives --skip tracking_survives \
  --skip a_dead_endpoint --skip a_202_reaches --skip context_set_through --skip a_401_stops \
  --skip a_batch_is_spooled --skip track_reports_consent_outcomes
```

The skips are the nine tests that compress a batch — gzip under an interpreter takes longer than the run is worth, and the unsafe code they add over the rest is one struct construction.

`cargo test` runs the protocol conformance suite — every scenario in [docs/sdk/protocol.md](docs/sdk/protocol.md) §13 against a fake host, so "the player quits after eight seconds with no network" and "the process is killed between spooling and the 202" are deterministic tests rather than a device farm. The ABI suite drives the same core through the real `extern "C"` symbols, which is the half a binding can get wrong.

A binding's own tests cover only what is left: the seam. `sdk/unity/verify.sh` builds a throwaway project around the package, compiles it and runs four play-mode tests — that the C# `[StructLayout]` declarations still match the native library, that the host's delegates survive a garbage collection, that the installation identifier lives where the platform will not back it up, and that every call is safe before the SDK has started. Each of those fails on a device and nowhere else, and none of them is reachable from Rust.

## Continuous integration

`.github/workflows/ci.yml`, on every push. Five jobs, because they fail for five different reasons:
1. **Rust** — the protocol and its ABI (`fmt`, `clippy --all-targets`, tests, and the `cbindgen` header-drift check).
2. **Miri** — pointer and handle boundary tests under the interpreter.
3. **Go** — `go vet`, `gofmt`, `go test -race`, and `migrate verify` against the real PostgreSQL, ClickHouse and Valkey stack in Docker.
4. **Native** — cross-compilation matrix across Apple (macOS arm64/x86_64, iOS), Linux/Android, and Windows MSVC runners ([docs/sdk/platforms.md](docs/sdk/platforms.md)).
5. **Node** — OpenAPI contract drift check (`pnpm contracts:check`), lint, typecheck, and build across `apps/*` and `packages/*`.

**What CI does not cover: Unity.** `verify.sh` needs a Unity editor and a licence, and the play-mode tests need the native plugin for whichever platform the runner is. It is run by hand before a release.
