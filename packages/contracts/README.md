# `@playersense/contracts`

TypeScript types for the API, generated from the API.

```ts
import type { Project, ResponseOf, RequestOf } from '@playersense/contracts'

const projects: ResponseOf<'/v1/organizations/{org}/projects', 'get'> = await get(…)
const body: RequestOf<'/v1/organizations/{org}/projects', 'post'> = { name, slug }
```

## Why the document is committed

Two files are generated and both are in git:

```
openapi.json      the API's own OpenAPI document
src/schema.d.ts   the types, from openapi-typescript
```

Committing them buys three things. The apps need no build step to consume the package —
the same reason `@playersense/ui` is a Nuxt layer rather than a built package. A reviewer
sees the contract change in the diff of the pull request that caused it, rather than
learning about it when a page breaks. And CI can compare the committed files against the
live application, which is the check below.

## The drift check

```bash
pnpm --filter @playersense/contracts generate   # rewrite both files
pnpm --filter @playersense/contracts check      # regenerate in memory, fail on any difference
```

`check` is what CI runs, and failing it is not a problem with the check. It means a route's
shape changed and the contract was not regenerated, so `client` and `admin` are compiling
against an API that no longer exists. Regenerate, read the diff, and commit it alongside
the change that caused it.

It needs no running stack. `go run ./apps/backend/cmd/api -openapi` builds the application
and prints its OpenAPI description without opening a database connection, a ClickHouse
connection or a listening port — which is what lets this run on every push rather than only
in the job that starts the services.

Two settings are pinned while generating, because `info.title` and `servers[0]` are the only
parts of the document that come from configuration. Without pinning them, a developer whose
`API_PUBLIC_URL` differs would regenerate a "changed" document that had not changed. The
committed contract describes the public API, so it names the public address.

## What is not here

**A client.** A generated one would decide how authentication, retries and error handling
work for every caller, and those are decisions the apps should make once, together, in
their own fetch wrapper. This package is types only: it has no runtime, no dependencies at
runtime, and nothing that can be out of date while the process is running.

**Hand-written types.** Anything added here by hand is a second source of truth, and the
second one is always the one that is wrong. `src/index.ts` may name things — it aliases
`Project` to the generated `ProjectOut` — but it declares no shapes.

## `src/index.ts`

Aliases, so a page says `Project` rather than a path expression thirty characters wide, and
three helpers for reaching into a route. Anything not aliased is still reachable:

```ts
import type { Schema } from '@playersense/contracts'
type Usage = Schema<'UsageOut'>
```

The helpers are keyed on the **path and method**, not on an operation id, because the ids
are generated from the route — `GET /v1/organizations/{org}/projects` is
`get_v1_organizations_by_org_projects`. Naming one in a page means a route rename breaks
the page with a message about a missing operation id rather than a missing route.

`MethodsOf` narrows the method parameter to the verbs a path actually serves. The generated
`paths` declares every verb on every route and types the absent ones as `never`, so without
it `ResponseOf<'/v1/batches', 'get'>` resolved to a quietly useless type instead of being
refused. It is now a compile error.

## `src/assertions.test-d.ts`

Type-level assertions, checked by `tsc --noEmit`. There is nothing to execute: every
statement is a claim about a type and the check is whether the file compiles.

They guard the thin hand-written layer, not the generated types — each alias is a place
where a rename in Go becomes a name that silently resolves to `never`, which is not an
error until somebody uses it three files away. The `@ts-expect-error` cases are
self-verifying: TypeScript reports an unused directive, so a refusal that stops working
fails the build.
