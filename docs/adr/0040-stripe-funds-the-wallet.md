# 0040. Stripe funds the wallet, and PlayerSense stores no billing information

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** project.md §40, §40.1; [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md), [ADR 0039](0039-players-are-charged-as-they-arrive.md)
**Supersedes in part:** project.md §40's Stripe meters and invoices

## Context

ADR 0039 made usage PlayerSense's to price and charge: tracked players are priced as they arrive and
drained from a prepaid wallet every few seconds. That left Stripe one job, which nothing did yet —
putting money *into* the wallet.

Two further decisions were made:

- **No billing information is stored here.** Paying, adding cards, choosing a default card and reading
  receipts all happen on Stripe's own pages — Checkout and the customer portal.
- **Use Stripe's SDK**, at the account's API version, 2026-04-22.dahlia.

And the promise that governs every line of this: a card is charged **once** per decision to charge
it, and a payment is credited **once** however it is reported.

## Decision

### Stripe's pages, not ours

A top-up is `POST …/billing/top-ups`, which opens a Checkout Session and returns its URL. Managing cards
is `POST …/billing/portal`, which returns a customer-portal URL. The Checkout Session saves the card for
off-session use (`setup_future_usage=off_session`), so the card a studio pays with is the card automatic
top-up can charge.

The only thing kept is the Stripe customer id. `default_payment_method` is dropped. Whether a card
exists is asked of Stripe when it matters — when automatic top-up is turned on, and when it is about
to charge — using the customer's default card, or the most recently saved one.

The browser coming back from Checkout proves nothing: anybody can load a success URL. Only a signed
webhook credits a wallet, and the dashboard shows the balance the webhook produced.

### Credit is keyed on the PaymentIntent

A Checkout payment is reported by `checkout.session.completed` and by `payment_intent.succeeded`, each
delivered at least once; an automatic top-up is also reported by its own API response. All of them
converge on one call that grants a `purchase` lot keyed `stripe:<PaymentIntent id>`, so whichever
arrives first credits and the rest find the lot. A purchase lot pays arrears before it becomes
spendable (ADR 0039), and never expires.

A payment is credited only if its metadata names a PlayerSense account **and** its Stripe customer is
that account's customer. The signature proves Stripe sent the event, not that this account paid.

### The webhook records before it acts

Every event is inserted into `stripe_events` by its unique id before it is handled. A redelivery of an
event already processed or ignored is answered 200 and left alone. A handling failure is answered 500 —
Stripe retries — with the reason on the row. Events nothing depends on (payment methods, account
updates, created or processing intents, expired sessions) are recorded as `ignored` and acknowledged.
The API version is not required to match the SDK's: the endpoint's version is set in Stripe's dashboard
and `stripe listen` forwards 2026-08-26.dahlia; the fields read are stable, and the version is kept on
the row.

### Automatic top-up claims before it charges

The billing writer finds accounts below their threshold and, for each:

1. **Claims** it: a fresh key, the amount, and the time, committed to the account row.
2. Sends the charge with that key as Stripe's Idempotency-Key.
3. Acts on the answer: succeeded → credit and release; declined or needing authentication → release,
   record why, and do not try again for `BILLING_AUTO_TOPUP_RETRY_AFTER`; processing → keep, the
   webhook finishes it; **no answer → keep**.

A kept claim is retried once stale with the **same key and amount**, so a charge whose response was
lost is answered by Stripe with the payment it already made. An account holding a claim is never
claimed again, so no second key exists. A claim older than Stripe's memory of keys is first looked for
among the customer's payments. If the customer changed cards between attempts, Stripe refuses the
reused key and the first attempt is looked up instead.

The charge is the configured amount, or the smallest multiple of it that brings the balance back above
the threshold — debt is covered in one charge, not one per pass — capped at `billing.MaxTopUp`.
Off-session charges name `payment_method_types=[card]` and `error_on_requires_action`: a card that
wants 3-D Secure fails at once rather than waiting for a person who is not there.

## Consequences

- **Stripe holds everything a card touches.** PlayerSense's PCI scope stays what using Stripe gives, and
  a customer's card can be changed in the portal without anything here going stale.
- **Asking Stripe costs a round trip** when auto top-up is enabled and before each automatic charge.
  Neither is on a hot path.
- **Refunds and disputes take the credit back** (added after this ADR was first written). A refund is
  taken when Stripe reports it succeeded, keyed on the refund id; a dispute when its funds are
  withdrawn, keyed on the dispute id. What is taken: what is left of the refunded top-up, then other
  purchased credit oldest first, then arrears — never promotional credit, which was not paid for. A
  refund that fails, or a dispute that is won, puts back exactly what was taken, once. Automatic top-up
  is switched off either way, so a refunded or disputed card is not charged again seconds later.
  Verified end to end in test mode with a real refund and Stripe's self-disputing test card.
- **Every top-up is invoiced** (added after this ADR was first written). A Checkout top-up has Stripe
  issue a paid invoice after the payment (`invoice_creation`). An automatic top-up is charged in three
  steps — its PaymentIntent created unconfirmed; an invoice created, given its line, finalized, and the
  PaymentIntent attached; then the PaymentIntent confirmed off-session — each with an idempotency key
  derived from the claim's, so a retried claim replays one invoice and one charge. A decline cancels
  the PaymentIntent and voids the invoice (Stripe refuses to void an invoice with an open payment on
  it). Stripe credits the attached payment to the invoice a few seconds after it succeeds. No tax is
  calculated; Stripe Tax can be turned on without changing this flow.
- **Invoice events never credit a wallet.** They are recorded and acknowledged; a payment is credited by
  its PaymentIntent, once, and an invoice describing it must not be a second report of it. Stripe's
  own default PaymentIntent on a finalized invoice carries no metadata and is canceled when ours pays;
  its cancellation is recorded and ignored.
- **The customer portal must be configured in Stripe** — test mode already is; live mode needs its
  default configuration saved once before `…/billing/portal` works there.
- **USD only.** A payment in another currency than the account's is recorded and not credited.

## Alternatives considered

**Stripe Elements on our own page.** Rejected: it puts a payment form, and every change to it, in
PlayerSense's scope, and the business asked for Stripe's pages.

**Store the default payment method id.** Rejected: it is a copy of Stripe's state that goes stale the
moment a studio changes cards in the portal, and nothing needs it between charges.

**Credit on the redirect back from Checkout.** Rejected: a URL anybody can load cannot move money.

**Credit on `checkout.session.completed` only.** Rejected: bank debits complete the session before the
money arrives, and automatic top-ups have no session. Crediting on any report of the PaymentIntent,
keyed on its id, covers all three.

**A new idempotency key per attempt.** Rejected: an attempt whose response was lost may have charged the
card, and a new key would charge it again. The key belongs to the decision, not to the request.

**Refuse events whose API version differs from the SDK's.** Rejected: the endpoint's version is set in
the dashboard, and a mismatch would answer 400 to every event until somebody noticed.

## Enforcement

- `apps/backend/internal/payments/` — gateway, credit, webhook, automatic top-up.
- `payments_test.go` — against the real database with a fake Stripe: a payment reported four ways
  credited once; forged, stale and wrongly-keyed signatures refused; another customer's or currency's
  payment not credited; a lost response retried as the same charge; a forgotten key found, not
  repeated; a decline waited out; Stripe down losing nothing; eight racing passes charging once.
  Minting a new key on retry, or ignoring an outstanding claim, fails it.
- `stripe_live_test.go` (`STRIPE_LIVE=1`) — Stripe's test mode: one customer per account, Checkout and
  portal URLs, the same key twice is one payment, a changed amount under a key is refused, a decline
  is an answer with a reason.
- `stripe_e2e_test.go` (`STRIPE_E2E=1`, with `stripe listen` and the API running) — a real automatic
  charge, and Stripe's own signed webhook for it processed without crediting twice.
- `apps/backend/cmd/api/control_payments_test.go` — the routes, their permission, their bounds, and a
  signed delivery through HTTP.
