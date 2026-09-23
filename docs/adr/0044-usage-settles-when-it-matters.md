# 0044. Usage settles when it matters, and the balance a studio sees is live

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0041](0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md), [ADR 0043](0043-a-bounded-tolerance-for-accounts-that-owe.md), [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md)
**Supersedes in part:** ADR 0039's "the queue is drained into the wallet every few seconds"

## Context

ADR 0039 queued a row per delivery that brought new players, and drained every account's queue into
its wallet every `BILLING_DRAIN_EVERY` (5 seconds). That worked, and the load test kept exact books. But
the row counts it implied do not scale:

- **The ledger.** An active organization got a statement line every drain that found something — up
  to 17,280 a day. Across a few hundred busy organizations that is millions of `ledger_entries` a day,
  in a table that is append-only and kept for years, and a statement nobody can read.
- **The queue.** One `usage_charges` row per delivery, and settled rows were never deleted.

A statement line every five minutes carries the same money. What five seconds actually bought was
elsewhere: a top-up charged as soon as a balance runs low (ADR 0041), debt noticed as soon as it
starts (ADR 0043), and a balance on the billing page that is never stale.

## Decision

### Settle when it matters, otherwise every five minutes

The drain still looks every `BILLING_DRAIN_EVERY`. An account's queue is settled onto its ledger when
any of these holds:

- its oldest unsettled charge is `BILLING_SETTLE_AFTER` old (5 minutes);
- the queue has reached `BILLING_SETTLE_CAP` ($1,000), so no amount of money waits on the clock;
- the queue would take a funded account **below zero**, so the debt and the grace period start now;
- the queue would take an account with automatic top-up **below its threshold**, so the top-up is
  queued (ADR 0041) and the card charged in seconds, as before.

The last two compare the queue with the balance on the account row, in the same query that picks the
accounts, off the partial index over unsettled rows. An account already in debt has already crossed
zero, so it settles on the clock like any other: a debtor cannot fill the ledger either.

### One open row per account, month and card

A delivery's charge extends the account's open row for that month and rate card — the one ending where
these players begin — instead of adding a row. So a queue row is one settlement, not one delivery. The
extension's `WHERE` carries the whole condition (unsettled, same card, contiguous), and the drain reads
the queue `FOR UPDATE`: an extension either commits first and is paid with the row, or waits for the
drain, finds the row settled, and opens the next one. `first_unit` stays immutable; `last_unit` and
`amount_micros` may grow only while the row is unsettled. A price change opens a new row, so each row
is priced by one card.

### The balance a studio sees is live, from PostgreSQL

`GET /v1/organizations/{org}/billing` adds `live`: the ledger balance minus pending usage, with pending
spent in the wallet's own order — promo first, then the oldest purchase — so its promo and purchased
figures are what the wallet will hold once the queue settles, and anything past the credit shows as
owed. Its `available_micros` — or `owed_micros`, when an account owes — is the headline; the
promo, purchased and available figures skip lapsed credit, as the spend path does. `balance_micros`
is the ledger total minus pending, so like `ledger_balance_micros` it still counts promotional credit
that has lapsed and not yet been written off (project.md §55.3): the explained number, not the
headline. It is one indexed sum over the account's few unsettled rows, read when the page is read. No
second copy of the balance in Valkey: a cache of money that disagreed with the ledger would be a bug
with no owner.

### Debt counts what is pending

The suspension job (ADR 0043) counts an account's debt as its arrears plus the part of its pending
usage its balance will not cover. Five minutes of queued usage cannot carry an account past its credit
limit unnoticed.

### Settled rows are pruned

Settled `usage_charges` rows are deleted 395 days after settlement — a year and a month, past any
statement a studio can still query — in chunks, by the pruner that already deletes old player sets.
The ledger line each row settled into is kept; it is the record.

## Consequences

- In the load test (1,000,000 deliveries, 200 organizations, 5,000 deliveries each) an organization
  ends with about one queue row and one ledger line, down from thousands, at the same 16,000
  batches a second.
- `ledger_entries` grows with time and boundaries crossed, not with traffic: 288 usage lines per
  account per day on the clock at the very most, plus one per boundary crossed (a top-up threshold, zero,
  the cap) — usually far fewer.
- The ledger balance can be up to five minutes behind the usage. Every figure a studio reads that
  matters — `live`, `usage.pending_micros`, the suspension decision — already includes pending.
- A statement line now covers the players settled together, e.g. "Tracked players, September 2026:
  #1,001–#4,380".

## Alternatives considered

**A live balance in Valkey, decremented per charge.** Rejected: a second balance to reconcile with the
ledger, lost on a Valkey restart, for a number PostgreSQL computes from a handful of rows when the
page is read.

**Settle strictly every five minutes.** Rejected: an automatic top-up would wait up to five minutes
after the balance crossed its threshold, and debt would start late.

**Aggregate ledger lines daily.** Rejected: the balance would be a day stale, and top-ups and
suspension would need a second source of truth for "now".

## Enforcement

- `apps/backend/internal/charging/charging_test.go` — deliveries extend one open row; a settled row is
  never extended; a funded account's usage waits five minutes and then settles; crossing zero and
  crossing the top-up threshold settle at once (the latter queuing the top-up); an account already in
  debt settles on the clock; extending and settling concurrently keep the books exact (it fails without
  the drain's `FOR UPDATE`); settled rows are pruned after retention and unsettled ones never.
- `apps/backend/internal/suspension/suspension_test.go` — queued usage counts toward the credit limit,
  and only past what the wallet holds.
- `apps/backend/cmd/api/control_live_balance_test.go` — the live balance through HTTP, promo spent
  first, and pending past the credit shown as owed.
- `apps/backend/internal/charging/load_test.go` (`BILLING_LOAD=1`) — rows per organization, and exact
  books, under load.
