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
db/clickhouse/    Goose migrations for the analytics schema
db/postgres/      Ent owns the control-plane schema
docs/adr/         Architecture decision records
docs/compliance/  Obligation -> mechanism -> code -> test
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

## Tests

```bash
go test ./apps/backend/... -race
go run ./apps/backend/cmd/cohortcheck     # 15 correctness checks against real derived data
go run ./apps/backend/cmd/archivecheck   # Appendix C.6: is anything about to expire unarchived?
```

`cohortcheck` seeds synthetic events with a known answer and asserts the pipeline reproduces
it. It is the independent second opinion on every number the reporting API serves.

`archivecheck` compares what ClickHouse still holds against what the archive has, and fails
when a day would reach its TTL with no archive. Nothing can *enforce* that ordering — a
ClickHouse TTL is a declarative instruction to a background merge, with no hook to wait on —
so the invariant is held by margin, and this is what measures it.
