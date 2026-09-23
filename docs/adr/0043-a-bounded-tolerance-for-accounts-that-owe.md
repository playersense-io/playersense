# 0043. A bounded tolerance for accounts that owe

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0041](0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md), [ADR 0015](0015-rate-limiting-in-valkey.md); project.md §40.1, §65; docs/sdk/protocol.md §9

## Context

ADR 0039 made usage owed rather than refused: the players were served before they were priced, so an
empty wallet becomes arrears and the account enters its grace period. Nothing moved an account further.
An account with no card — or a card that kept declining — could owe forever while PlayerSense kept
storing its data.

That is not only unpaid debt. **Owing does not grow with events**: only new players are charged, so an
account in debt can send unlimited events from players it already has while its debt stands still —
and every accepted event costs compute and storage. A bound on the debt alone would not bound the cost.

The business wants tolerance — time for a studio to fix a card — but not unlimited data.

## Decision

### Two bounds, whichever comes first

- **Time.** `BILLING_GRACE_PERIOD` (7 days), counted from when the account first owed.
- **Money.** Its **credit limit**:

      limit = max(floor, 20% of its paid usage in the last 30 days)

  scaled so a studio spending $5,000 a month may owe $1,000 and one spending $100 may owe the floor. The
  floor is the plan's `credit_limit_micros` or `BILLING_DEFAULT_CREDIT_LIMIT` ($50), so a new or small
  studio is not cut off over a few dollars and Enterprise can carry more. **Staff can set an exact limit
  for one organization** (`PUT /v1/operator/organizations/{org}/credit-limit`, in its audit log), which
  wins over the rule; clearing it returns to the rule.

  **Paid** usage: the last 30 days' usage minus what is still owed, so a debt never raises its own
  limit. A studio that paid for $5,000 of usage and then owes may owe exactly $1,000; a $5,000 top-up
  after that pays the $1,000 first and leaves $4,000 (ADR 0039). An account that never pays is suspended
  at the floor.

### Suspended means ingestion refused, and nothing lost

Past either bound, `billing-writer` moves the account to `suspended`. `POST /v1/batches` answers **429
with `Retry-After: 3600`** and code `payment_required`. That is the answer the SDK keeps its batch for and
retries later (protocol §9; `sdk/core/src/transport.rs`, `a_429_honours_retry_after`), so a studio's data
waits on its players' devices — within the SDK's seven-day spool — rather than being dropped. The
configuration endpoint keeps answering (the game still runs), stored data is untouched, and the dashboard
keeps working. Paying the debt makes the account active again (the wallet), and ingestion resumes
within about half a minute.

### Never a database read on the ingestion path

PostgreSQL decides; Valkey carries the decision. The job marks each suspended organization in Valkey with
a 90-second lifetime and renews the marks every 30 seconds, unmarking promptly any organization that
paid — found from the database (any account that left `suspended` recently), not from the job's own
memory, so a mark set by another billing writer or before a restart is still removed. The gate on the
ingestion path remembers each organization's answer for 15 seconds, so a request
costs a map lookup and one Valkey `EXISTS` per organization every 15 seconds. If Valkey is unreachable,
or the job stops, the gate **fails open**: a Valkey outage must not become an outage for every customer,
and a few minutes of unbilled ingestion from a handful of suspended accounts is the smaller cost.

## Consequences

- A studio that owes has, by default, a week and a spend-scaled allowance to pay; the billing page shows
  `suspends_at`, `credit_limit_micros` and where the limit came from.
- A suspended studio's players keep playing; their events wait on the device for up to seven days, and
  anything older than that is dropped by the SDK, not by PlayerSense.
- Nobody is emailed yet when an account enters grace or is suspended (todos.md, "Failure emails").
- `restricted` remains an unused state — room for a dashboard lock between grace and suspension, if one
  is ever wanted.

## Alternatives considered

**Refuse at a zero balance.** Rejected: a card that expired on a Saturday would cost a studio its live
data, which it cannot get back.

**A flat credit limit.** Rejected: the same number is reckless for a small studio and petty for a large
one.

**402 Payment Required.** Rejected for now: the shipped SDK has no rule for it, and an unknown 4xx risks
the batch being dropped. 429 with `Retry-After` is already "keep it and come back later".

**Check the balance per request.** Rejected: a PostgreSQL read on the hottest path, for an answer that
changes a few times a month per account.

**Fail closed when Valkey is down.** Rejected: it would turn a cache outage into a total ingestion outage.

## Enforcement

- `apps/backend/internal/suspension/` — the gate, the job and the limit; `suspension_test.go` covers the
  grace period, the time bound, the money bound, a plan's larger floor, scaling with spend, the same debt
  tolerated for a large spender only, an override, paying back in, failing open, `suspends_at`, and the
  whole scenario: $5,000 paid, suspended at exactly $1,000 owed, $4,000 after a $5,000 top-up, and let
  back in by a job that did not set the mark.
- `apps/backend/cmd/api/ingest_billing_test.go` — a suspended organization is answered 429 with
  `Retry-After: 3600`, nothing reaches the queue, and after paying its batches are accepted; an account
  in grace is still accepted and charged.
- `apps/backend/cmd/api/control_payments_test.go` — setting and clearing an organization's limit, audited,
  staff-only.
