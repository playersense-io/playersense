# 0015. Rate-limit counters live in Valkey

**Status:** Accepted
**Date:** 2026-09-13
**Updated:** 2026-09-18
**Relates to:** project.md §12, §70, §71

## Context

The framework ships an in-process rate limiter. It is correct for one process and silently
wrong for several: counters held in one process are not shared with the next, so a limit of
three hundred a minute becomes three hundred a minute *per instance*, and the published
limit stops being the enforced one as soon as the service scales.

Ingestion is the service most likely to run many instances and the one whose credential is
assumed discoverable (§71), so this is not hypothetical.

## Decision

Counters live in **Valkey**, incremented by a Lua script so that the increment and the
expiry happen in one round trip:

```lua
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return {count, redis.call('PTTL', KEYS[1])}
```

`PEXPIRE` is set **only when the counter is new**. Extending an existing counter's life on
every request is the classic mistake here: a window that restarts on each request never
resets, and a client that keeps knocking is never let back in.

Counting happens in the SDK handlers, after authentication and binding establish the project
and installation. Device, address-in-project and project budgets are counted together with
`IncrementMany`, before the cell residency check. Requests rejected before reaching the
handler do not spend these budgets. See [rate-limits.md](../rate-limits.md) for the current
policy, which replaces the original framework-wide address limiter.

The address budget keys on an IPv6 `/64` rather than the exact address, because a `/64` is the
block most providers hand out and keying on the address gives such a client a fresh budget
per request.

**When the storage cannot answer, it reports an error rather than a count.** Ingestion and
configuration register the limiter with `FailOpen: true`, following §65: a Valkey outage
should degrade service rather than refuse every SDK request. This replaces the original
fail-closed policy. Authentication continues to require a valid credential.

Control and query APIs register no request limiter. Authentication emails are requested by
the dashboard directly from Firebase, which owns their sending quotas and abuse protections.

## Consequences

- Every SDK request reaching a handler costs one Valkey round trip for all its budgets.
  A counting failure allows the request; the storage remains ephemeral acceleration under §65.
- Shared counters mean one test suite spends the next one's budget. A `RATE_LIMIT_DISABLED`
  setting exists for tests, and the limiter itself is exercised by tests written for it —
  which is a better arrangement than every suite fighting for the same allowance.

## Alternatives considered

**In-process counters.** Wrong the moment there are two instances, and wrong invisibly.

**A token bucket in Valkey.** Smoother, and needs either a Lua script holding more state or
several round trips. Two fixed windows — burst and sustained — distinguish a person
clicking from a script that never stops, which is the distinction that matters here.

**Fail closed when Valkey is down.** The original policy refused requests on storage errors.
The SDK paths now fail open to preserve service during a cache outage under §65.

## Enforcement

- `apps/backend/internal/valkey/ratelimit.go` — the script and the `RateLimitStorage`.
- `internal/valkey/ratelimit_test.go` — `TestIncrementCountsFromOne`,
  `TestWindowIsNotExtended`, `TestCountersAreSharedAcrossInstances`,
  `TestUnavailableStorageReportsAnError`, and the two cell-isolation tests.
