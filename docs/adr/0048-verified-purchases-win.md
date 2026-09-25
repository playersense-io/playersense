# 0048. A store's verified purchases outrank what the game reports

**Status:** Accepted
**Date:** 2026-09-25
**Builds on:** project.md §46-48, [ADR 0046](0046-every-page-reads-a-fact-built-for-it.md)

## Context

Until now the purchases ledger and the Revenue page held only what the game reported: TrackRevenue
events, analytics about a purchase that a modified client can invent. §47 asks for the money fact
instead, a receipt checked with its store server to server, and it is now built for the App Store,
Google Play and Steam. A game that verifies usually keeps reporting too, for funnels, so the same
purchase can arrive twice by two roads, and the pages must count it once without a join (§32.1).

## Decision

**Verification.** `POST /v1/purchases/verify` takes the game's project key (so a game without a
server can act on the answer) or the server key (§47's preference for granting). PlayerSense asks
the store every time, or for a StoreKit 2 transaction checks Apple's signature against Apple Root CA
G3, which needs no call. The answer's status says what to do: grant on `verified` or `sandbox`, keyed
by the purchase id; never on `rejected`. `(platform, store_transaction_id)` is unique, so a retry
answers the same purchase with `duplicate` set, and the same transaction sent for another player is
rejected as `replayed`. Every call is logged in `purchase_attempts` with a step-by-step trace, kept
thirty days.

**Refunds.** Apple's Server Notifications V2 and Google's Real-time Developer Notifications post to a
per-project URL whose token is the credential (Apple's are also signed, and checked). The worker also
sweeps every connected store every six hours (Apple's notification history, Google's voided
purchases, Steam's GetReport), which catches a missed notification and fills in the reasons Google's
notifications leave out. A refund heard twice changes the purchase once.

**Into ClickHouse.** Each change to what a purchase counts for (verified, refunded, reinstated, a
receipt rejected) is written to `purchase_changes` in the transaction that made it. The worker claims
a batch with `FOR UPDATE SKIP LOCKED`, inserts it into `purchases` with the batch id as the
deduplication token, and marks it shipped; a batch claimed and not marked goes again with the same
token, so ClickHouse and every rollup chained from it store it once. The ledger replaces on `version`
(the change id), so a FINAL read shows a purchase's latest state; the rollups count what each row
*changed*, so a refund adds to refunds and nothing to purchases.

**Verified wins.** For a store the project has connected, from the day it was connected, the money
pages count that store's verified rows; every other store still counts what the game reported. It is
one predicate on columns `purchases`, `payers` and `revenue_daily` already carry (`source`, `store`,
the day), never a join:

```sql
if((store = 'app_store' AND day >= '2026-09-25') OR ..., source = 'verified', source = 'reported')
```

Disconnecting a store puts its money back on what the game reports.

## Consequences

- Revenue by default is the stores' word where the studio has connected a store. Test purchases and
  rejected receipts are counted beside it (`sandbox`, `rejected`) and never as revenue; refunds are
  booked on the day the store gave the money back.
- `payers` has no day, so for a verified store a device's lifetime is its verified purchases alone:
  payer history from before the store was connected does not carry over for that store.
- Cohort revenue and the weekday-by-hour heatmap still read `events_raw`, which is what the game
  reported; they move to verified purchases when they are rebuilt on `purchases`.
- Google's answer has no price, so a Google purchase records the price the game showed
  (`price_from_store` false); Apple's and Steam's prices are the stores'.
- An erasure removes the person's purchases, their attempts, and their rows in ClickHouse, including
  a purchase verified for a player alone, which sits on a stand-in device derived from the player id.
  A receipt replayed after that erasure would verify again; the law's erasure outranks the replay
  check.
- Subscriptions are not verified yet: Apple's arrive as transactions and are recorded as such, and
  Google's need the subscriptions API.
- The web shop (a studio's own Stripe) is not verified yet and stays reported-only. Adding it means a
  restricted key per project and a PaymentIntent lookup, behind the same adapter interface.

## Enforcement

- `internal/stores` checks each store's answer; `TestAStoreKitTransactionIsCheckedAgainstApplesChain`
  fails if a forged, re-signed, wrong-app or Xcode transaction is accepted, and
  `TestTheEmbeddedRootIsApplesG3` if the embedded root is not Apple's.
- `internal/purchases` records a purchase once and ships its changes through the outbox;
  `TestPurchasesAreVerifiedRecordedOnceAndRefunded` (cmd/api) verifies, retries, replays, refunds by
  notification and by sweep, ships to ClickHouse twice, and fails if a reported App Store purchase is
  counted beside a verified one or a shipped batch lands twice.
- `RevenueFilter.counted` and `payersCounted` (internal/query/revenue.go) are the only places the
  choice is made, and the no-join test covers every statement they add to.
