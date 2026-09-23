# 0042. Plans are sold as Stripe subscriptions, and unlock features

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0040](0040-stripe-funds-the-wallet.md), [ADR 0041](0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md); project.md §37, §40.1

## Context

PlayerSense sells two plans. **Free**, and **Enterprise** at $99 a month or $999 a year, which unlocks
things Free does not: premium support in a shared Slack channel, a daily report builder that posts to
the studio's Slack, and access to PlayerSense's MCP server. None of the three is built yet; what they
need from billing is a reliable answer to "has this organization paid for it".

Tracked players are already charged in real time from a prepaid wallet on every plan (ADR 0039). A plan
fee is a different kind of money: a fixed amount, on a schedule, renewing until canceled.

## Decision

### Stripe runs the subscription; PlayerSense follows it

A plan's subscription is a Stripe subscription, billed to the studio's card on Stripe's schedule, with
Stripe's own invoices, retries and dunning, and canceled or switched in Stripe's customer portal. It is
separate from the wallet: the wallet pays for players, the subscription pays for the plan. PlayerSense
stores the subscription's id and its last-reported state and nothing about the card.

- Staff set a plan's monthly and yearly price with `PUT /v1/operator/plans/{plan}/subscription`, which
  creates the plan's Stripe Product once and a Price per interval. A Stripe Price cannot be edited, so a
  changed amount is a new Price: new subscribers pay it and existing ones keep theirs until they change.
- A studio subscribes with `POST /v1/organizations/{org}/billing/subscription`, which opens Checkout in
  subscription mode. An organization with a live subscription is sent to the portal instead.
- `customer.subscription.created/updated/deleted` move the account. A **live** subscription — active,
  trialing, or past_due while Stripe retries a failed renewal — puts it on the plan its Price belongs
  to; one that ends (canceled, unpaid, incomplete_expired) puts it back on the default plan.

Three rules keep that correct when webhooks misbehave:

- **Newer only.** An event is applied only if Stripe created it after the last one applied to the
  account, so an `updated` delivered after its `deleted` cannot bring a subscription back.
- **Only its own subscription ends a plan.** A subscription that was never the account's cannot move it —
  so a plan staff assigned by contract is not ended by a checkout that expired half-finished.
- **The plan is the entitlement.** Features are read from the account's current plan, and the plan is
  the subscription's exactly while the subscription is live. There is no second copy of "is paid" to
  drift from the first.

### Features are a closed vocabulary on the plan

`billing.Feature` is the set PlayerSense understands — `premium_support`, `slack_reports`,
`mcp_access` — and `plans.features` says which a plan has, validated when the plan is saved. A feature
asks `billing.HasFeature(plan.Features, feature)` before serving anyone. Adding a feature to a plan is an
update; adding a new feature is a constant and a release.

### The plan still prices players

Moving plans changes the rate card that prices the organization's next players (ADR 0039), so the
subscription webhook invalidates the organization's cached pricing (ADR 0041). Whether Enterprise prices
players differently from Free is data: publish a different card for the plan.

## Consequences

- A studio pays for Enterprise with the card it pays top-ups with, and sees both kinds of invoice in the
  same portal.
- While Stripe retries a failed renewal the studio keeps its features; when Stripe gives up, they go. How
  long Stripe retries is the Dashboard's subscription setting, not code.
- A studio that somehow holds two subscriptions — two Checkout tabs completed — has both billed; the
  newer is taken as its plan, and the older should be canceled by support. The dashboard refuses to open a
  second, which is what makes this rare rather than impossible.
- The customer portal must be configured in the Dashboard to allow canceling and switching plans.

## Alternatives considered

**Charge the plan fee from the wallet.** Rejected: a fixed fee on a schedule is exactly what Stripe
subscriptions do — renewals, proration, retries, invoices, cancellation — and rebuilding that on the
wallet would be rebuilding Stripe Billing, badly. The wallet stays for usage, which Stripe cannot price
in real time.

**Store "is paid" separately from the plan.** Rejected: two facts that must agree will eventually not.
The plan moving with the subscription makes the plan the single answer.

**Feature flags per organization.** Rejected for now: a plan is the unit of sale. A per-account override
can be added the way entitlement overrides were, if a contract ever needs one.

## Enforcement

- `apps/backend/internal/billing/features.go` — the vocabulary and `HasFeature`.
- `apps/backend/internal/payments/subscriptions.go` — the webhook; `payments_test.go` covers subscribing,
  canceling at period end, the end, a late event, a renewal being retried, a contract plan and an unknown
  price. Removing the ordering guard fails it.
- `apps/backend/cmd/api/control_payments_test.go` — pricing Enterprise, the public page, subscribing
  once, and refusing what cannot work.
- `stripe_live_test.go`, `stripe_e2e_test.go` — Stripe's test mode: prices created once per amount, a
  subscription checkout, and a real subscription moving an organization onto Enterprise and back to Free.
