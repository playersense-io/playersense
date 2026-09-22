# 0039. Players are charged as they arrive, through a queue the wallet drains

**Status:** Accepted
**Date:** 2026-09-22
**Relates to:** project.md §36, §37, §38, §40, §40.1, §64, §65, §98; [ADR 0013](0013-an-event-bus-between-ingestion-and-storage.md), [ADR 0015](0015-rate-limiting-in-valkey.md), [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md)
**Supersedes in part:** project.md §40's month-end flow; ADR 0036's "a charge larger than what is available is refused whole", for usage only

## Context

§40 describes a month-end bill: a daily usage ledger, an immutable period snapshot, meter events to
Stripe, a Stripe invoice. None of the month-end half was built. `wallet.Spend` was called by an
operator by hand, because a scheduled close needed overage *rates* and §37 forbids hardcoding them —
and the entitlement vocabulary could only say what a plan *includes*, never what one more player
costs. No production code created a `BillingAccount` at all.

The business then decided three things:

- **Charge organizations in real time**, not at the end of the day or the month.
- **Price tracked players in volume tiers** — $0.0005 each to 100,000 a month, $0.0004 to 500,000,
  $0.00035 to 1,000,000, $0.0003 above. (Doubled before launch to $1 per 1,000 players, falling to
  $0.60 — which is the point of the next line: the numbers are data, and the README shows the
  current ones.)
- **Keep the prices in the database**, so staff change them from the admin console without a deploy.

And four constraints on how: never charge twice, never lose a charge that failed, never make the
database the bottleneck, and never block an organization's SDK because of any of it.

The existing wallet already models the money correctly — one account per organization, each top-up
its own non-expiring lot, promos expiring (90 days by default), spent promos first and then top-ups
oldest first. What was missing is everything between a batch arriving and that wallet.

## Decision

### Prices are immutable rate cards

A `rate_cards` row is a plan's tiers for one meter from one moment (`effective_from`). Staff never
edit a card; a price change is a new card. Each player is priced at the card in force when they
arrived, so a mid-month change is exact and history is never repriced. A card cannot start in the
past, and its tiers are refused if they leave a gap, overlap, or bound the top band.

A charge for k new players is always `Total(n + k) − Total(n)`, never `k × current price`. That
prices a batch straddling a boundary correctly on both sides, makes a volume card's retroactive
discount fall out of the same formula (as a credit), and guarantees a month's charges sum to exactly
its total however arrivals were split — in integer micros, so nothing drifts. Graduated is the
default; volume is supported and has a cliff (100,001 players cost less than 100,000), which is why
it is not the default. Included players are free and still hold positions: the 100,000 boundary does
not move because the first 10,000 were free.

### Two stages joined by a durable queue

**Accrue**, per bus delivery, in `billing-writer` — a third consumer group with its own position:

1. Each batch names one device. Its digest is a keyed HMAC over (month, project, environment,
   install id) — the identity `metrics_monthly` counts, so the invoice and the dashboard count the
   same players (§38).
2. Valkey is asked, in one MGET, which digests are already counted this month. Those cost nothing
   more.
3. The rest go to PostgreSQL in one transaction per account-month: lock `monthly_usage`, then
   `INSERT … ON CONFLICT DO NOTHING RETURNING` into `billable_player`. **The rows actually inserted
   are the new players** — that is the entire definition of "new".
4. They are priced at their positions on the card in force and queued as a `usage_charges` row, in
   the same transaction. The wallet is not touched.
5. After commit, and only then, Valkey is told.

**Drain**, every five seconds: for each account with unsettled charges (`SKIP LOCKED`), net them per
month into one `ChargeUsage` (or `CreditUsage`) and mark them settled, in one transaction.

### Usage is owed, never refused

The players were served before they were priced — their batches were answered 202 and stored — so
refusing the charge would lose money without undoing anything. `ChargeUsage` takes what credit there
is, in the usual order, and the shortfall becomes `arrears_micros`. Any grant pays arrears before any
of it becomes spendable. An account with arrears moves `active → grace_period`, and back when it has
paid. **Nothing escalates further automatically**: `restricted` and `suspended` stop things a studio
depends on, and when to do that is a commercial policy not yet decided.

### Every wallet movement locks the account

`balance_after_micros` is computed from the account row. With the drain spending every five seconds,
two movements computing it from the same row — a top-up and a drain — would erase one of them from
the running total. Every movement now takes the row lock first and checks its idempotency key after.

## Consequences

**The four promises, and what keeps each:**

| Promise | Mechanism |
|---|---|
| Never twice | `billable_player` unique per (account, month, digest); positions under the month's row lock; `usage_charges` unique on (account, month, first_unit); ledger idempotency keys |
| Never lost | Counted and queued in one transaction; paid and marked in one transaction; an empty wallet owes rather than refuses; unpriceable batches dead-lettered, never dropped |
| Not the bottleneck | Known players cost one Valkey MGET; one INSERT per account per delivery; drain bounded, over a partial index, `SKIP LOCKED` |
| Never blocks the SDK | Separate consumer group and binary; the ingestion path reads no balance, price or state |

**Stripe's role shrinks.** Stripe no longer needs usage meters or invoices for tracked players:
PlayerSense prices and charges usage itself. What Stripe does is **fund the wallet** — card top-ups,
automatic top-up when the balance falls below a threshold, and paying off arrears — with webhooks
granting `purchase` lots. That is the next piece of work, and `StripeEvent` already exists for it.

**The month is keyed on arrival, not on event time.** An SDK can backdate an event by seven days, and
pricing on that would let a client move a player into a month already paid for. So a batch from the
31st that arrives on the 1st is a player in the new month here and in the old one in
`metrics_monthly`. The two counts agree except at that seam.

**Environments are all billable**, as the daily meter already was: pricing only production would let a
studio ship its game on a development key and pay nothing. **kids_strict projects are not billed by
tracked players** — the gateway replaces their install id per batch, so there is no persistent player
to count, and counting batches would bill a children's game per session.

**A digest is personal data.** `billable_player.digest` is classified `identifier`. It is kept for the
current and previous month and then deleted (§38's "discarded"); an erasure request does not yet
remove a player's digest from the current month. The HMAC key must not rotate mid-month — every
player would become new and be charged again.

**A new consumer starts from where its subscription starts.** Deploying the billing writer bills
players from then on, not retroactively.

## Alternatives considered

**Charge per batch in the ingestion request.** Rejected: it puts a PostgreSQL transaction on the SDK's
hottest path and makes a billing outage an ingestion outage — the one thing forbidden outright.

**Charge in analytics-writer.** Rejected: that writer deliberately holds no PostgreSQL handle (§64), so
that storage survives a control-plane outage. Giving it one to bill would trade that away.

**Keep the counters in Valkey.** Rejected as the source of truth: Valkey is ephemeral (ADR 0015, §65),
and "is this player new" decides money. It is the filter in front of PostgreSQL, written only after
PostgreSQL commits, so losing it costs lookups and never a charge.

**Spend from the wallet in the accrual transaction.** Rejected: every batch would lock the account and
its lots, and a top-up would contend with ingestion traffic. The queue separates pricing, paced by
traffic, from spending, paced by a clock.

**Refuse usage the wallet cannot cover, as operator charges are refused.** Rejected: the data is
already stored; refusing only loses the money and leaves no record of the debt.

**Editable rate cards.** Rejected: a price edited in place reprices players already charged, and the
change would leave no trace. Publishing a new card is the edit, and the list of cards is the history.

**Price per unit times the current price.** Rejected: it misprices every batch that straddles a band
boundary, and a volume card's repricing has no expression in it at all.

## Enforcement

- `apps/backend/internal/billing/pricing.go` — the arithmetic; `pricing_test.go` proves every band,
  included units, and that any split of a month sums to its total.
- `apps/backend/internal/charging/` — accrue, drain, prune; `charging_test.go` runs against the real
  stack: redelivery with and without the cache and after eviction, eight racing consumers, six racing
  drains, a drain rolled back midway, a locked account skipped, an empty wallet, a poisoned batch, a
  price change mid-month, a volume credit. Removing the month lock or the `RETURNING` count fails it.
- `apps/backend/internal/wallet/usage.go`, `ledger.go` — owing, arrears, locking;
  `TestConcurrentTopUpsAndUsageNeverEraseEachOther` fails when the row lock is removed.
- `apps/backend/cmd/api/ingest_billing_test.go` — a suspended account in arrears still gets a 202 and
  its player is still counted.
- `apps/backend/cmd/api/control_pricing_test.go` — the catalogue over HTTP, and that it is staff-only.
- `apps/backend/cmd/billing-writer` — the deployable.
