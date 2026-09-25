# 0047. The billing page shows Stripe's own description of cards, invoice details and receipts

**Status:** Accepted
**Date:** 2026-09-25
**Amends:** [ADR 0040](0040-stripe-funds-the-wallet.md)'s "no billing information is stored here"

## Context

ADR 0040 kept nothing of Stripe's side but the customer id: cards, the default card, invoice details
and receipts were all Stripe's, reached through Checkout and the customer portal. That kept
PlayerSense out of anything a card touches, and it still does. What it cost is a billing page that
could not say which card automatic top-up would charge, whether it had expired, what the invoices
print, or list the receipts, without sending the person to Stripe to find out.

## Decision

Keep a copy of Stripe's own description of those things, for showing:

- a saved card's **brand, last four digits, expiry, funding and whether it is the default**, keyed by
  Stripe's payment method id (`payment_cards`); never a number, which Stripe does not give out;
- the **invoice details** Stripe prints, name, email, address lines, country and tax ids, as a
  snapshot on the billing account;
- each **top-up's and subscription's invoice**, with its number, total, status and Stripe's page and
  PDF (`receipts`). Usage invoices stay the `invoices` table's, which bills them.

Stripe stays the source and the editor. The copy is read from Stripe the first time the page asks and
on a refresh (`DescribeCustomer`), and kept in step by `payment_method.*`, `customer.updated`,
`customer.tax_id.*` and `invoice.*`; a late event cannot undo a newer one, and a card Stripe no longer
lists is marked detached rather than deleted, so a receipt that names it still can. A new card is
saved through Checkout in setup mode; choosing the default and editing details stay in the portal.
Nothing read from the copy ever decides a charge: `ChargeableCard` still asks Stripe.

## Consequences

- The PCI scope is unchanged: brand, last four and expiry are what Stripe returns to any merchant.
- The page can be wrong for as long as a webhook takes, seconds, and a refresh asks Stripe directly.
- Four more event families to subscribe to, listed in the README's `stripe listen` line.

## Enforcement

`TestTheBillingPageFollowsStripe` reads a customer from Stripe once, then saves, defaults and removes
cards, changes details and adds a tax id by webhook, and checks the page, and that a top-up's invoice
is a receipt and a usage invoice is not.
