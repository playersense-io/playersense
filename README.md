# PlayerSense

Game analytics platform. `project.md` is the architecture source of truth; `docs/adr/`
records why it is that way, and `docs/compliance/` maps obligations onto the code that
meets them.

## Local stack

Ports are deliberately **not** the service defaults — a `21xxx` block, below the macOS
ephemeral range so nothing collides with an OS-assigned port. Change them in `.env` and
nowhere else.

| Service | Port |
|---|---|
| PostgreSQL | 21432 |
| ClickHouse HTTP | 21123 |
| ClickHouse native | 21900 |
| Valkey | 21379 |

```bash
cp .env.example .env
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml
docker compose up -d
```

The cold volume of the hot/cold policy is a second local directory by default, which
exercises the policy and the part movement for real. To point it at a GCS bucket instead, set
`CH_COLD_ENDPOINT` and the HMAC pair in `.env` and add the overlay:

```bash
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml:infra/stage1/compose/docker-compose.cold-object.yml
```

It is an overlay rather than a flag because ClickHouse constructs every declared disk eagerly:
an S3 disk with an empty endpoint is not an absent disk, it is a server that will not start.

## Migrations

The control plane is **two databases** (§99): one global tier holding accounts, projects and
keys, and one tier per cell holding anything that names a player. Each refuses to be
migrated as the other, so pointing the wrong DSN at `migrate` fails by name rather than
growing a second control plane.

```bash
go run ./apps/backend/cmd/migrate up          # global, then this cell
go run ./apps/backend/cmd/migrate global up   # the global tier only
go run ./apps/backend/cmd/migrate cell up     # this cell: its tier + its ClickHouse
go run ./apps/backend/cmd/migrate cell status # which ClickHouse migrations are applied
go run ./apps/backend/cmd/migrate verify      # §96 classification, §99 residency
go run ./apps/backend/cmd/migrate cell reset  # drop everything, Keeper metadata included
```

`verify` reads the live catalog rather than the schema files, which is what makes it proof
rather than documentation.

## Layout

```
apps/backend/     Go services and commands (module playersense.dev/backend)
sdk/core/         The SDK protocol in Rust, once, with no I/O of its own
sdk/ffi/          The C ABI over it, for every binding
sdk/unity/        The Unity package: a host, a P/Invoke layer and the API a game calls
db/clickhouse/    Goose migrations for the analytics schema
db/postgres/      Ent owns the control-plane schema
docs/adr/         Architecture decision records
docs/compliance/  Obligation -> mechanism -> code -> test
docs/sdk/         The language-neutral SDK protocol every client implements
infra/stage1/     Local compose stack
```

Requires Go 1.27 (Muzak uses generic methods and `encoding/json/v2`).

## Services

### control-api — `:21080`

```bash
go run ./apps/backend/cmd/control-api
```

`GET /openapi.json` describes it. Everything under `/v1` needs
`Authorization: Bearer $CONTROL_API_ADMIN_TOKEN`; `/healthz` does not.

### ingest-api — `:21081`

```bash
go run ./apps/backend/cmd/ingest-api
```

`POST /v1/batches` with `Authorization: Bearer ps_project_live_...`. Answers 202 once the batch
is durably queued. `EVENT_BUS=local` writes an fsynced NDJSON spool under `EVENT_BUS_DIR`;
`EVENT_BUS=pubsub` publishes to Google Pub/Sub.

The body may be sent `Content-Encoding: gzip` or `zstd`, and an SDK should leave `event_id`
out and let the gateway derive it. Together those take a 500-event batch from 290 bytes an
event to 8.1 — see [ADR 0026](docs/adr/0026-compressed-json-and-derived-event-ids.md), and
`go run ./apps/backend/cmd/wiresize` to reproduce the numbers.

What a client must actually do — sessions, consent, the disk spool, retries, and what happens
when the player quits after eight seconds with no network — is
[docs/sdk/protocol.md](docs/sdk/protocol.md), implemented once in `sdk/core` and shared by
every binding. How a studio describes its game for targeting, and why geography is resolved
from the connection rather than sent by the client, is
[docs/sdk/context-and-targeting.md](docs/sdk/context-and-targeting.md).

### analytics-writer — `:21082` (health probe only)

```bash
go run ./apps/backend/cmd/analytics-writer
```

Consumes the cell's queue and stores batches in ClickHouse, stamping each one's cohort key
so no cohort query ever needs a join. It never touches the control plane.

### archive-writer — `:21084` (health probe only)

```bash
go run ./apps/backend/cmd/archive-writer
```

Consumes the cell's event stream on its own subscription and writes the canonical Parquet
archive, with a manifest per object in the cell tier. `ARCHIVE_STORE=local` writes under
`ARCHIVE_DIR`; `ARCHIVE_STORE=gcs` writes to the bucket.

### worker — `:21086` (health probe only)

```bash
go run ./apps/backend/cmd/worker        # long-running: both loops, on their schedules
go run ./apps/backend/cmd/worker once   # one pass of each, then exit
```

Two loops on two cadences, because §79's list is two kinds of work.

**The derivation**, nightly at `WORKER_DERIVE_AT`. Rebuilds the cohort facts, their
denominators, the behavioural buckets and the daily/monthly metrics — everything `query-api`
serves. Until it runs, every chart reads zero while ingestion is perfectly healthy.

Whole months, never a trailing window: each row of a cohort fact belongs to exactly one
activity-month partition, and `REPLACE PARTITION` swaps a partition wholesale. Denominators
are rebuilt before numerators, so a returner can never be divided by a cohort size computed
before they arrived. Running it twice produces the same tables.

**The lifecycle pass**, every `WORKER_JANITOR_EVERY`. Three jobs that are each waiting on
something, which is why they do not wait for the night:

- **Archive expiry** (§60) sweeps `delete_after` on the manifests, deletes the object and
  keeps the row — an object deleted on the twentieth is still billable for the twenty days it
  existed.
- **Erasures** (§61) purge one actor from `events_raw`, both device-keyed summaries, the cell
  tier's purchases and the archive objects holding them. An archive object is *rewritten*
  without the person rather than deleted, because it holds an hour of everyone else too
  ([ADR 0025](docs/adr/0025-an-erasure-rewrites-the-archive.md)).
- **Exports** (§101) assemble one actor's data into a bundle in the cell's own bucket, and
  remove it when its term is up.

None of the three can stop the others: a bucket outage that blocks expiry must not block an
erasure with a legal deadline on it, so a pass reports every failure rather than the first.

`once` is what a scheduled Stage 2 deployment uses — a Cloud Run Job beats a process
holding a timer.

### query-api — `:21083`

```bash
go run ./apps/backend/cmd/query-api
```

Two shapes, both with `Authorization: Bearer ps_reporting_live_...`:

- `POST /v1/cohorts/query` — retention, playtime, revenue, LTV, payer conversion and
  behavioural segments, keyed on when a player arrived (§29).
- `POST /v1/metrics/query` — active players, sessions, events and revenue over time, keyed
  on when something happened (§26, §27).

Both read pre-aggregated facts and never join on a player key. Cells describing too few
players are suppressed rather than returned as zero, and every answer says how many were
withheld. A time series also says which summary tier answered it and whether its player
count is exact or a sketch.

It also carries this cell's **data-subject requests** (§101), with
`Authorization: Bearer ps_server_live_...` — a server key, not a reporting one, because
erasing a studio's player is not the same privilege as reading its aggregates:

- `POST /v1/privacy/erasures` — records the request and tombstones the actor in the same
  round trip; removal runs in the worker over the following hours.
- `POST /v1/privacy/exports` — everything held about one actor, collected from
  `GET /v1/privacy/exports/{id}/download` as newline-delimited JSON.

They live here rather than on `control-api` for a residency reason: both name a player, and a
player identifier never reaches the US-hosted control plane (§99).

## SDK

The protocol is implemented **once**, in Rust, with no I/O of its own: sessions, consent,
batching, the disk spool, retry and backoff, deduplication and the response table. A binding
supplies a host — a clock, a file, an HTTP call — and a facade in its own idiom, so the same
core runs on every platform rather than being reimplemented five times and drifting.

```bash
cd sdk && cargo test                  # the protocol, and the ABI over it
cd sdk && ./build-native.sh           # the native libraries, and what they export
cd sdk/unity && ./install-plugins.sh  # put them in the Unity package
cd sdk/unity && ./verify.sh           # compile the package and run its tests, in real Unity
```

`sdk/unity` is the first binding. A game pastes its project key into Project Settings and
writes one line:

```csharp
PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12 });
PlayerSense.Context.Player.Set("rank", "diamond");
```

No `await`, no callback, no result to check, nothing to wrap in `try`/`catch`
([sdk/unity/README.md](sdk/unity/README.md)). The native libraries are not committed — an
unstripped iOS archive is 21 MB against ~550 KB of linked code — so a checkout builds them
with the two scripts above.

## Storage tiers

`events_raw` and `player_activity_daily` sit on a **hot/cold storage policy**, so the §33
hot window is cache residency rather than a retention limit: old parts move off local NVMe
and stay queryable in place. Retention is unchanged and still per project — `expire_at` is
derived from the row's own retention tier.

| Table | Moves to cold after | Why |
|---|---|---|
| `events_raw` | 7 days | The largest table; nothing on the dashboard path reads it |
| `player_activity_daily` | 90 days | §33.1's other big one. Three times clear of the derivation's two-month rebuild window |
| `player_lifetime` | never | `player_lifetime_v` is on the query path (§29.10) |

```bash
# which volume each partition is on
docker exec playersense-clickhouse clickhouse-client --port 9000 \
  -u playersense --password playersense_local -d playersense \
  -q "SELECT partition, disk_name, sum(rows) FROM system.parts
      WHERE table='events_raw' AND active GROUP BY partition, disk_name ORDER BY partition"
```

Locally `cold` is a second directory. In production it is a GCS bucket — see
`infra/stage1/compose/clickhouse/config.d/storage.xml`, which carries the disk definition
and reads its credentials from the environment. See [ADR 0024](docs/adr/0024-the-hot-window-is-a-cache-not-a-retention-limit.md).

## Tests

```bash
go test ./apps/backend/... -race
cd sdk && cargo test                      # the SDK protocol and its C ABI
cd sdk && cargo clippy --all-targets      # --all-targets, or the test fixtures go unlinted
cd sdk && ./build-native.sh               # native libraries, and what they export
go run ./apps/backend/cmd/cohortcheck     # 15 correctness checks against real derived data
go run ./apps/backend/cmd/archivecheck   # Appendix C.6: is anything about to expire unarchived?
go run ./apps/backend/cmd/wiresize        # what a batch weighs, four ways
```

Miri covers the C ABI's pointer and handle code:

```bash
cd sdk && cargo +nightly miri test -p playersense-ffi --test abi -- \
  --skip a_host_that_calls_back --skip a_spooled_batch_survives --skip tracking_survives \
  --skip a_dead_endpoint --skip a_202_reaches --skip context_set_through --skip a_401_stops \
  --skip a_batch_is_spooled --skip track_reports_consent_outcomes
```

The skips are the nine tests that compress a batch — gzip under an interpreter takes longer
than the run is worth, and the unsafe code they add over the rest is one struct construction.

`cargo test` runs the protocol conformance suite — every scenario in
[docs/sdk/protocol.md](docs/sdk/protocol.md) §13 against a fake host, so "the player quits
after eight seconds with no network" and "the process is killed between spooling and the 202"
are deterministic tests rather than a device farm. The ABI suite drives the same core through
the real `extern "C"` symbols, which is the half a binding can get wrong.

A binding's own tests cover only what is left: the seam. `sdk/unity/verify.sh` builds a
throwaway project around the package, compiles it and runs four play-mode tests — that the C#
`[StructLayout]` declarations still match the native library, that the host's delegates survive
a garbage collection, that the installation identifier lives where the platform will not back
it up, and that every call is safe before the SDK has started. Each of those fails on a device
and nowhere else, and none of them is reachable from Rust.

The layout one is the one to keep honest: add a field to `Native.PsConfig` and it fails with
*"PsConfig: the binding says 272 and this binary says 264"*, and the SDK refuses to start
rather than writing past the end of a struct.

## Continuous integration

`.github/workflows/ci.yml`, on every push. Four jobs, because they fail for four different
reasons: the Rust protocol and its ABI (fmt, clippy `--all-targets`, tests, and the
header-drift check), Miri over the boundary code, Go against the real stack in Docker, and a
native matrix across macOS, Linux and Windows runners.

That last job is the one that changes what can ship. Windows, Linux and Intel macOS cannot be
built on an Apple-silicon developer machine — no MSVC toolchain, no Linux linker — so until
now those three libraries did not exist and the Unity package could not ship to PC at all.
See [docs/sdk/platforms.md](docs/sdk/platforms.md).

**What CI does not cover: Unity.** `verify.sh` needs a Unity editor and a licence, and the
play-mode tests need the native plugin for whichever platform the runner is. It is run by hand
before a release, and a workflow that had never executed would be worse than admitting that.

`cohortcheck` seeds synthetic events with a known answer and asserts the pipeline reproduces
it. It is the independent second opinion on every number the reporting API serves.

`archivecheck` compares what ClickHouse still holds against what the archive has, and fails
when a day would reach its TTL with no archive. Nothing can *enforce* that ordering — a
ClickHouse TTL is a declarative instruction to a background merge, with no hook to wait on —
so the invariant is held by margin, and this is what measures it.
