# PlayerSense

Game analytics platform. `project.md` is the architecture source of truth.

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

go run ./apps/backend/cmd/migrate up       # apply the ClickHouse schema
go run ./apps/backend/cmd/migrate verify   # §96 classification, §99 residency
go run ./apps/backend/cmd/migrate reset    # drop everything, Keeper metadata included
```

## Layout

```
apps/backend/     Go services and commands (module playersense.dev/backend)
db/clickhouse/    Goose migrations for the analytics schema
db/postgres/      Ent owns the control-plane schema
infra/stage1/     Local compose stack
```

Requires Go 1.27 (Muzak uses generic methods and `encoding/json/v2`).
