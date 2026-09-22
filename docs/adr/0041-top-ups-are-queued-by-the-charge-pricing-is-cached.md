# 0041. Automatic top-ups are queued by the charge; pricing is cached in two levels

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** [ADR 0015](0015-rate-limiting-in-valkey.md), [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0040](0040-stripe-funds-the-wallet.md); project.md §65

## Context

Two things were true after ADR 0040, and both would have hurt at volume.

**Charging and automatic top-up were not connected.** The usage drain spent from wallets; separately,
every thirty seconds, a scan of every billing account looked for balances below their threshold. A
studio that ran out waited up to half a minute, and the scan's cost grew with the number of accounts
rather than with the number that needed anything.

**Every delivery with new players asked PostgreSQL the same three questions** — which account, which
plan, which rate card — for every organization in it, although the answers change a few times a year.

And a third requirement arrived: automatic charges must be limited per day, so a card is not charged
often enough to look like abuse to a card network.

## Decision

### The charge queues the top-up

`billing_accounts.auto_topup_requested_at` is a queue made of a column. The wallet's `record` — the one
UPDATE every movement of money already makes, on a row it already holds locked — stamps it when the
balance ends below the threshold and clears it when the balance ends above. The settings route does
the same when automatic top-up is switched on or off. So the signal:

- **costs nothing**: no extra read (the row is locked and in hand) and no extra statement;
- **cannot be lost or spurious**: it commits with the charge that caused it, or not at all.

The billing writer works the queue every two seconds through a partial index that holds only flagged
rows, oldest first, so the interval costs the same with ten accounts as with ten million. The full scan
remains every five minutes as a safety net for anything the queue could not see — a threshold edited in
the database, an account restored from a backup.

Settings are **not** cached for this decision. The drain already holds the account row, locked, to
move money; a cached copy would add a round trip and could be stale in the one direction that matters
— charging a card whose owner has just switched automatic top-up off. The claim re-reads the row under
its lock before anything is sent.

### At most three automatic charges in any 24 hours

`auto_topup_daily_limit` (default 3, per account so staff can raise it) bounds charges in a rolling
24-hour window, counted under the row lock when a charge is **claimed** — declined or not, because a
decline is what a bank counts — and never for a retry of a claim, which is the same request to Stripe.
At the limit nothing is sent, the reason is recorded, and the request waits in the queue for the window
to reopen. Since one charge already covers a whole debt (ADR 0040), a legitimate account never needs more.

### Pricing comes from memory, then Valkey, then PostgreSQL

The accrual resolves an organization's pricing before its transaction opens, from:

| level | lifetime | what it saves |
|---|---|---|
| this process | 30 s | the network, for a busy organization |
| Valkey | 10 min | the database, across writers and restarts |
| PostgreSQL | — | read on a miss only |

Two entries, because they change for different reasons and at different scales:
`billing:pricing:org:<seq>` (account, currency, plan, overrides) and `billing:pricing:plan:<id>`
(allowance and **every** rate card — past, current and scheduled). Moving an organization, editing a
plan and publishing a card delete the shared entry.

Two rules make the cache exact rather than merely fast:

- **A scheduled price needs no invalidation.** The plan entry holds every card; the one in force is
  chosen in memory by the batch's arrival time, at the exact second it changes.
- **A card cannot start sooner than a minute after it is published** (`charging.CardHorizon`), longer
  than any process keeps its copy (`LocalTTL`), so every charger has the card before it applies. "Now"
  means "in a minute", and the response says when.

An organization with no account is never cached, so one provisioned a moment ago is billed at once.
Losing Valkey costs speed, never correctness (§65).

### A delivery's organizations accrue in parallel

Each account-month takes exactly one lock, its own row, so accruals for different organizations cannot
deadlock and have no reason to wait in line. A delivery runs eight at a time, bounded under the pool.

## Consequences

- A studio that runs out is topped up within seconds of the charge that emptied it.
- Pricing a delivery with new players costs no reads in the steady state; a plan edited mid-month
  reaches every charger within `LocalTTL`, and its allowance applies to players after that.
- The daily limit can leave an account in arrears for up to a day. Ingestion is not affected, and the
  account is in its grace period (ADR 0039).
- The daily limit is editable per account only in the database until the admin console exposes it.

Measured on a laptop against the local stack (`TestLoad`, `BILLING_LOAD=1`): a million batches from
250,000 players across 200 organizations, four writers, the drain and automatic top-up running
alongside — 16,200 batches/s, every organization's books exact, 748k of 750k repeat sightings answered
by Valkey without PostgreSQL, 395 pricing reads from the database in total.

## Alternatives considered

**Cache the automatic top-up settings in Valkey for the drain.** Rejected: the drain reads the locked row
anyway, so the cache saves nothing and adds staleness to a decision that charges a card.

**A signal in Valkey (a list or a stream).** Rejected as the queue itself: it can be lost between the
charge committing and the signal being written, and then the account is never topped up. A column
written by the same statement cannot be.

**LISTEN/NOTIFY to wake the worker.** Not needed: a two-second poll over a partial index is cheaper to
operate and fast enough.

**Invalidate every organization on the plan when a card is published.** Rejected: a plan is shared by
thousands of organizations. The plan entry is the one key to delete.

**Allow a card to start immediately.** Rejected: another process's memory could still price the next
players at the old card for up to `LocalTTL`, which is a wrong charge rather than a slow one.

## Enforcement

- `apps/backend/internal/wallet/ledger.go` — `record` writes the signal; `WantsTopUp` is the rule.
- `apps/backend/internal/payments/autotopup.go` — the queue, the sweep, the daily limit;
  `payments_test.go` covers the queue from a charge, the sweep, unqueueing, the limit across declines,
  a retry not spending a slot, and eight workers at the limit's edge. Removing the limit fails it.
- `apps/backend/internal/charging/pricing.go` — the cache; `charging_test.go` covers a busy
  organization read once, a second writer warmed by Valkey, a price published through the horizon, a
  scheduled price switching on the second, and pricing without Valkey.
- `apps/backend/internal/handlers/pricing.go` — the horizon and the invalidations;
  `control_pricing_test.go` refuses a card starting sooner.
- `apps/backend/internal/charging/load_test.go` — the million-batch books.
