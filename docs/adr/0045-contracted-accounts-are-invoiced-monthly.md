# 0045. Contracted accounts are invoiced monthly

**Status:** Accepted
**Date:** 2026-09-23
**Relates to:** [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md), [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0040](0040-stripe-funds-the-wallet.md), [ADR 0042](0042-plans-are-stripe-subscriptions.md), [ADR 0043](0043-a-bounded-tolerance-for-accounts-that-owe.md), [ADR 0044](0044-usage-settles-when-it-matters.md); project.md §40, §40.1
**Supersedes in part:** ADR 0036's "the month close does not exist"; ADR 0040's "invoice events never credit a wallet", for PlayerSense's own usage invoices only; ADR 0042's plan fee, for invoiced accounts; ADR 0043's grace clock, for invoiced accounts

## Context

Every organization is prepaid. Players are charged from a wallet as they arrive (ADR 0039), the wallet
is funded by card through Stripe (ADR 0040), and an account that owes is suspended after seven days or at
its credit limit (ADR 0043).

That is the right default and the wrong contract for a large studio. A publisher's finance team buys on
a purchase order, pays by bank transfer thirty days after an invoice arrives, and expects one invoice a
month that says what it is for: the platform fee they negotiated, the players they had, and — when they
committed to a minimum — the difference. Under ADR 0043 that customer is suspended on the seventh day
of every month, because every dollar it has not yet been invoiced for looks like a dollar it failed to
pay.

The problem is one word doing two jobs. `arrears_micros` means **overdue**: it starts the grace clock
and it ends when the account pays. For a contracted account most of what it owes is not overdue at
all — it is not yet due, and not yet billed.

## Decision

### Two ways to collect, chosen per organization by staff

`billing_accounts.collection` is `prepaid` (the default, and everything ADRs 0039–0044 describe) or
`invoiced`. Only staff move an organization between them, through a **contract**: an append-only,
versioned row that says how the organization is invoiced —

- payment terms (`terms_days`, net 30 by default) and the grace after the due date before suspension
  (`overdue_grace_days`, 14 by default);
- a monthly platform fee, and a monthly minimum commitment, both optional;
- the credit limit, which becomes required: it is the contract's cap on exposure;
- a PO number and a memo, printed on every invoice.

A contract is never edited. New terms are a new row from a moment on, and a month's invoice uses the
contract in force when it ends, so "what did we agree in March" is answerable in September.

### Owed but not due is a receivable, not arrears

`billing_accounts.receivable_micros` is what an invoiced account has used and not yet paid for, whether
or not it has been invoiced. The wallet still runs underneath: credit lots are spent first, in the order
ADR 0036 fixes — a contract with launch credit spends it — and only what credit cannot cover becomes
receivable instead of arrears. The invariant grows by one term and still holds at every commit:

    balance_micros = Σ lots.remaining_micros − arrears_micros − receivable_micros

Arrears keeps its meaning, so the prepaid state machine does not change. Usage stays owed and never
refused (ADR 0039), every movement still goes through the wallet under the account's row lock, and the
ingest path still reads nothing.

### An invoice bills a stretch of the ledger

Each ledger entry now carries two more facts: the month a usage charge belongs to (`period_start`,
by arrival as ADR 0039 keys it), and how much of the entry became receivable (`receivable_micros`).
An invoice bills every receivable **accrual** since the previous invoice — usage, the fee, the
commitment top-up — and records the last ledger entry it covered. Settlements (a payment, a credit
note) name the invoice they settle and are never billed.

So an invoice is never recomputed from anywhere else, and a charge is never lost at a month boundary:
usage for May that settles after May closed is on June's invoice as a "prior period" line. Summed
over all invoices, what is billed for a month is what `monthly_usage.charged_micros` said that month
cost — the number the dashboard showed (ADR 0036, 0044). The close checks the two and records any
difference on the invoice, where staff can see it, rather than billing a number nobody can explain.

**Sub-cent amounts carry forward.** Stripe bills cents; the ledger keeps micros. An invoice bills the
unbilled total rounded down to the cent and carries the remainder to the next, so no rounding entry is
ever written and nothing is lost or invented.

### The month close

`billing-writer` closes each invoiced account's month `BILLING_INVOICE_CLOSE_AFTER` (2 hours) after
it ends, and only once no usage for that month is still queued. Under the account's lock it:

1. posts the platform fee as a `subscription` entry, prorated by the day in a contract's first and last
   month;
2. posts the minimum-commitment top-up as a `commitment` entry: what the commitment exceeds the gross
   usage billed on this invoice by, and nothing when usage exceeded it;
3. writes the invoice with its lines — platform fee, usage, prior-period usage, credits applied,
   commitment, adjustments — as a draft.

The fee and the top-up go straight to receivable. A promotional credit is a discount on usage, not on
the contract's minimum; letting one pay the commitment would quietly lower the commitment.

The draft is then **issued** exactly as an automatic top-up is charged (ADR 0040, 0041): the invoice
row is the claim, its key is committed before Stripe is asked, and every Stripe step carries that key
plus a suffix. The invoice is created with `collection_method=send_invoice`, `days_until_due` from the
contract and our metadata, given its lines, finalized and sent. A retry finds the invoice by its
metadata and continues from wherever the last attempt stopped, so a lost response never becomes a
second invoice. A zero invoice is recorded and not sent.

A contract fee billed this way replaces the Stripe subscription for that account: an invoiced account
cannot hold a subscription (ADR 0042), and its plan is assigned by staff. One invoice, not two.

### Money moves on Stripe's word, and only for our invoices

`invoice.paid` for an invoice carrying `playersense_kind=usage_invoice` settles it: the receivable
falls by what was paid, keyed on the Stripe invoice, and anything paid beyond it becomes purchased
credit. Top-up and subscription invoices still move nothing, and ADR 0040's rule stands for them — a
top-up is credited by its PaymentIntent, once. A usage invoice's PaymentIntent carries no metadata of
ours, so it can never be credited a second time through the top-up path.

Staff marking an invoice paid for a wire received outside Stripe, and staff issuing a credit note, are
requests to Stripe. What changes the ledger is the webhook Stripe sends back, so an action taken in
Stripe's own dashboard is applied exactly as one taken through the API. A credit note reduces what an
open invoice asks for, and the receivable with it; an invoice voided or marked uncollectible in Stripe
is written off the receivable as an adjustment.

A dispute on an invoice payment reopens the receivable and the invoice: the bank took the money back,
so it is owed again, and the invoice is overdue at once. A won dispute settles it again.

### Overdue, not owing, starts the clock

An open invoice past its due date makes the account **overdue** (`overdue_since`) and puts it in its
grace period. `overdue_grace_days` after the first missed due date, ingestion is suspended exactly as
ADR 0043 suspends it — 429 with `Retry-After`, nothing lost, the dashboard still working. Paying every
overdue invoice ends it.

The credit limit bounds exposure in between: unbilled usage, open invoices and queued usage, less any
credit, reaching the contract's limit suspends ingestion at once. That is the same `Owed` ADR 0043
computes, because the balance now nets the receivable. For an invoiced account the suspension job alone
decides both ways — suspending, and letting back in once it is paid and under its limit — so a usage
charge landing on a suspended account cannot reactivate it by accident.

### Switching back

Moving an invoiced account to prepaid issues a final invoice at once for everything unbilled. Its open
invoices stay collectable, and one of them going overdue still suspends it. Arrears an account had when
it moved to invoiced become receivable and are billed on its first invoice.

## Consequences

- A contracted studio pays one invoice a month, by bank transfer or card on Stripe's hosted page, and is
  never suspended for usage it has not been billed for.
- The ledger, the billing page and the invoice agree, because the invoice is a range of the ledger and
  the ledger is what the dashboard's month total is made of.
- `BillingPeriod` — §40's snapshot, which nothing ever wrote — is removed, with `ledger_entries.period_id`.
  The invoice is the snapshot, and an unused table with an `invoiced` status would be read as the
  thing that invoices.
- Stripe's invoice emails and reminders are Stripe's Dashboard settings, as they are for top-ups.
- No tax is calculated, and everything is in USD — the same as every other charge (ADR 0040). Both are
  open items.
- A credit note on an invoice already paid, and a refund of an invoice payment, are recorded and flagged
  for staff rather than applied: whether money returned lowers what was owed or was simply a return of
  an overpayment is a person's decision, and the ledger is corrected with an operator adjustment.
- The admin and customer billing pages are still placeholders; the API and the generated contracts
  carry everything they need.

## Alternatives considered

**A long grace period and a large credit limit.** Rejected: an account that is billed monthly and paid
net 30 legitimately owes for up to two months, so the grace period would have to be two months for
every contracted account — and an account that genuinely stopped paying would keep ingesting for two
months too. The clock has to start at a due date, and a grace period has no due date.

**Keep invoiced usage in arrears and branch every reader.** Rejected: `arrears_micros` is read by the
state machine, the credit limit, the suspension job and the live balance, each with the meaning
"overdue". A second meaning behind a flag in four places is four places to get it wrong once.

**Stripe meters and usage-based subscriptions (§40's original plan).** Rejected for the same reason ADR
0039 rejected them: players are priced here in real time, and a second pricing engine at Stripe would be
a second answer to "what did this cost". Stripe issues and collects the invoice; PlayerSense decides what
is on it.

**Recompute the invoice from `monthly_usage` at close.** Rejected: late usage would fall between two
months, and credit covering part of a month would have to be re-derived from lots whose history is the
ledger anyway. The ledger already is the sum; the invoice is a window onto it.

**Let credit pay the platform fee and commitment.** Rejected: promotional credit would then lower a
negotiated minimum, which is a discount nobody agreed to.

## Enforcement

- `apps/backend/internal/wallet/` — receivable accrual, settlement and the invariant; `usage_test.go`
  and `ledger_test.go` cover usage becoming receivable for an invoiced account, settling and
  overpaying, a dispute reopening it, and `balance = lots − arrears − receivable` after each.
- `apps/backend/internal/invoicing/` — the close and the issue; its tests cover the lines, a late charge
  rolling into the next invoice, the sub-cent carry, the fee's proration, the commitment top-up, no close
  while usage is queued, a lost response resumed as the same invoice, and a zero invoice not sent.
- `apps/backend/internal/payments/invoices.go` — settling from Stripe's events;
  `TestInvoiceIssuingAndReviewEventsNeverMoveMoney` still holds for top-up invoices, and the invoice
  tests cover paying once, credit notes, write-offs and disputes.
- `apps/backend/internal/suspension/` — overdue grace, the exposure cap, and letting an invoiced account
  back in.
- `apps/backend/cmd/api/control_invoicing_test.go` — staff putting an organization on a contract,
  audited; customers refused top-ups and subscriptions; a month closing, listed, marked paid and settled.
