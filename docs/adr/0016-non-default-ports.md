# 0016. The local stack publishes no default port

**Status:** Accepted
**Date:** 2026-09-08
**Relates to:** project.md §7.1

## Context

A developer machine usually already has a PostgreSQL on 5432 and a Redis on 6379. A stack
that publishes those ports either collides or, worse, *appears* to work while the client is
talking to a completely different database — which is discovered later, by a query that
returns rows nobody wrote.

## Decision

Every published port comes from `.env`, and none of them is a service default. A `21xxx`
block, chosen to sit below the macOS ephemeral range (49152+) so nothing collides with an
OS-assigned port either.

```text
21080  control-api       21123  clickhouse http
21081  ingest-api        21900  clickhouse native
21082  analytics-writer  21379  valkey
21083  query-api         21432  postgres
                         21085  pubsub emulator (reserved)
```

Both `docker-compose.yml` and the Go configuration read the same variables, so a port is
changed in one place.

## Consequences

- Every connection string in `.env` carries its own port rather than composing one from a
  host and a port setting, so the ports declared there are the only place a port appears.
- Copy-pasted `psql` and `clickhouse-client` invocations from the internet do not work
  without the port flag. That is the point.

## Alternatives considered

**Defaults, with instructions to stop the local services first.** Works until somebody does
not, and the failure mode is silent.

**Docker networking with no published ports.** Correct for production and hostile to
development: no `psql`, no database GUI, no debugger attaching from the host.

## Enforcement

- `infra/stage1/compose/docker-compose.yml` — every port is `${VAR:-21xxx}`.
- `.env.example` — the block, with the reasoning at the top.
