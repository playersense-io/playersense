# 0036. A wallet is lots, because credit expires

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §36, §37, §38, §39, §40, §56; [ADR 0011](0011-api-key-format-and-storage.md), [ADR 0021](0021-two-metric-tiers.md), [ADR 0035](0035-an-invitation-is-addressed-not-merely-held.md)

## Context

The billing schema shipped complete and empty: `Plan`, `BillingAccount`, `LedgerEntry`,
`UsageDaily`, `BillingPeriod` and `StripeEvent` existed, **nothing wrote any of them**, and
`billing.read` and `billing.manage` reached no route. `billing.AutoTopUp.Validate` had no caller and
said so in its own comment: *"a rule that waits for its caller is a rule that gets re-remembered
differently."*

`BillingAccount.balance_micros` was one signed integer. That is enough for prepaid credit bought with
a card, and it is not enough for the product actually being sold: **promotional credit expires.**

A scalar cannot expire part of itself. Told that an account's $140 is $100 of trial credit lapsing on
Friday and $40 the customer paid for, one column has nowhere to keep the distinction and no way to
remove the right half when Friday comes. Every workaround is worse: a second column is two scalars
with the same problem, a nightly job that guesses which part to subtract is a job that subtracts a
customer's own money, and "expire the whole balance" is theft.

## Decision

### The balance is the sum of what is left in each grant

`CreditLot` is one grant: a kind, an amount, what remains of it, and an optional expiry. An
account's **available** credit is the sum of `remaining_micros` over every lot that has not lapsed,
computed on read.

`BillingAccount.balance_micros` keeps the meaning it always had underneath — the ledger's running
total — and both numbers are returned by the API. They agree once the expiry sweep has run, and the
difference between them is exactly credit that has lapsed and not yet been written off. Exposing
both is deliberate: a number that looks wrong should be explainable rather than merely defended.

### Credit is spent promos first, then purchases oldest first

1. **promotional credit, soonest expiry first**
2. **purchased credit, oldest grant first**

**Promos before purchases**, because a promo is the only credit that can be lost. Spending a
customer's own money while a trial credit ticks down to nothing is charging them for something they
had already been given, and the way they find out is by reading a statement weeks later.

**Soonest expiry first within promos.** The alternative wastes credit by construction: spend a
year-long promo while a week-long one is open and the week-long one lapses having paid for nothing.
Nobody asked would choose that, which is why it is not left to insertion order. A promo with no
expiry sorts *last* among promos — it is the one that cannot be wasted by waiting.

**Oldest grant first within purchases**, which is what a customer assumes and what keeps the most
recent top-up refundable for longest.

The order is **total**: kind, then expiry, then grant time, then row id. Two lots granted in the same
second with the same expiry must be consumed in a fixed order, or one charge produces two different
statements on two runs and neither can be reproduced.

### Ninety days, unless somebody says otherwise

A promo defaults to ninety days from the grant and accepts any later date instead. Long enough that a
studio evaluating through a quarter has the quarter; short enough that credit granted for a
conversation two years ago is not still sitting on an account nobody remembers.

**Purchased credit cannot be given an expiry at all** — the request is refused rather than ignored.
Money a customer paid for is not ours to put a clock on, and a grant that accepted a date would be a
wallet that can quietly delete somebody's money.

A date already past at the moment of the grant is refused too: it would create credit nobody can
spend and that the next sweep writes off, which looks exactly like the money being taken away.

### An expired lot is unspendable before anything writes it off

Expiry happens with the passage of time, not with a write. So the **spend path refuses a lapsed lot
on its own**, without waiting to be told — which is what makes the briefly-overstated running total
safe. Between midnight and the sweep the column says $100 and the wallet can spend nothing.

The sweep then writes off lapsed credit **one ledger entry per lot**, which is the opposite choice
from a spend. A customer asking "where did my $100 go" is asking about a *specific grant*, and an
aggregate would answer with a number matching nothing they were ever told.

### A spend is one ledger entry however many lots paid for it

The customer was charged $150 once, not three times. Which lots paid is the wallet's business —
visible in the lots and in the operator's response — and a statement listing three lines for one
charge is a statement nobody can reconcile against an invoice.

A charge larger than what is available is **refused whole**. A partial spend leaves three states that
each look like a different bug: a balance of zero, a charge that did not happen, and no record of the
difference.

### `promo` and `expiry` are their own ledger kinds

A promo is **not** a `topup`: a topup is money that arrived, it appears in revenue and it is
refundable. Adding the two together is how a month's revenue figure stops being one.

An expiry is **not** an `adjustment`: an adjustment is a human decision somebody can be asked about,
and a customer reading "adjustment −$40" on the day their trial credit lapsed would reasonably ask
who did that to them.

### Nothing about a grant is editable except what is left of it

`amount_micros`, `kind` and `expires_at` are immutable. An expiry date in particular: a lot whose
date moved is a lot whose lapse cannot be predicted from the row. **Extending a promo is revoking it
and granting another**, which the ledger records as two movements rather than as a column that used
to say something else.

`remaining_micros` is the one mutable field, because it is not a fact about the grant — it is what is
left of it — and every change to it is accompanied by a ledger entry in the same transaction.

### Granting credit is not a customer's privilege at any permission

A studio that could grant itself promotional credit would be a studio on an unlimited plan.
`billing.manage` is the widest thing a customer holds and all it configures is automatic charging: a
card *they* own, charged an amount *they* set.

### The meter reads the same fact the dashboard reads

`UsageDaily` is derived from `metrics_daily`, not from `events_raw`. §38 asks for exactly this:
*"the billing meter and the cohort tier are reading the same fact. Deriving the two independently
would eventually produce a month where the invoice and the dashboard disagree, and no amount of
investigation would establish which was right."*

A day is rewritten until it settles — §19.2 clamps timestamps up to seven days old, so yesterday's
number can still change tomorrow — and marked `final` at eight days, after which a re-run leaves it
alone. That is what makes a closed month reproducible.

**Tracked players is reported as a peak of daily figures, never a sum.** Adding DAU counts one person
once per day they played, which over a month is several times the truth and is the easiest possible
way to publish a usage page a studio disbelieves.

## Consequences

**Every read of a balance costs a query over the account's open lots** rather than a column read. An
account accumulates one row per top-up and per promo; an exhausted lot has `remaining_micros` of
zero and is excluded by the index. A customer with years of monthly top-ups has hundreds of rows, not
millions.

**Two numbers that can disagree.** `available_micros` and `ledger_balance_micros` differ between a
lapse and the sweep. Both are published with the reason, because the alternative — showing one and
hiding the other — is a support conversation where nobody can see the number the other is looking at.

**The month close does not exist.** `wallet.Spend` is called by the operator's charge route, not by a
scheduled job: that job needs overage *rates*, and §37 forbids hardcoding commercial values, so
pricing per extra MAU is a decision to be made rather than invented here. Until then a month is
charged by a person, which is how a small company bills anyway.

**Whether non-production traffic is billable is answered "yes" by omission.** `usage_daily` is keyed
by project and has no environment column, so development and staging count toward the meter. That is
a commercial decision the schema made before this commit; changing it is a WHERE clause. Recorded in
[10-open-items.md](../compliance/10-open-items.md) rather than left in an invoice for somebody to
find.

**The operator surface cannot say who acted.** It is guarded by the shared operator token, because
§56's admin console and the staff-access model behind it do not exist. `granted_by_user_id` is
therefore null on that path, so *"who gave this account $100"* has no answer inside the product —
which is the first question anybody asks when an account has credit nobody expected. The mitigations
that do hold: every grant carries a caller-supplied `reference` somebody has to be able to explain,
it is unreachable with a customer's credential at any permission, and the ledger records the movement
even though it cannot record the mover. Recorded as an open item beside the audit log it needs.

## Alternatives considered

**Keep one balance and expire by subtracting.** Rejected: nothing in a single number says which part
was promotional, so the subtraction is a guess, and the guess spends a customer's own money.

**Two columns, `promo_balance` and `purchased_balance`.** Rejected because promos expire on
*different dates*. Two $50 promos lapsing a month apart are one column that cannot say what to remove
in either month.

**Expire on read rather than with a sweep.** Rejected: a read that writes is a read that cannot be
served from a replica, and a customer opening a dashboard would be the thing that made their credit
disappear. The read *excludes* lapsed credit already; what the sweep adds is the statement line.

**Order by expiry across both kinds, ignoring the promo/purchase split.** Rejected. Purchased credit
has no expiry, so it would sort last and the behaviour would coincide with this decision — until
somebody granted an open-ended promo, at which point a customer's money would be spent ahead of free
credit and nothing would have changed to explain it. The rule is about which credit can be *lost*,
so that is what it is written over.

**Newest purchase first, so old credit is refundable.** Rejected: a refund is against a payment, and
the payment being refundable does not depend on which lot the wallet spent. Oldest-first is what a
customer assumes, and matching an assumption is worth more here than an operational convenience.

**Make `expires_at` mutable so an operator can extend a promo.** Rejected: a financial record whose
date can move is one whose lapse cannot be predicted from the row, and with no audit log the change
would leave no trace. Revoke and re-grant is two ledger movements and an explanation.

**Meter from `events_raw`.** Rejected by §38, quoted above. The invoice and the dashboard have to be
two readings of one number.

## Enforcement

`internal/wallet` holds the rules; `internal/metering` the meter; `internal/handlers/billing.go` and
`internal/routers/billing.go` the surface; `scheduler.CreditExpiry` the sweep, on the janitor's loop.

In `apps/backend/internal/wallet/wallet_test.go` — no database, because the order is arithmetic:

| Test | Fails when |
|---|---|
| `TestPromoCreditIsSpentBeforeMoneyTheCustomerPaid` | a purchase is spent while a promo is open |
| `TestTheSoonestPromoToExpireIsSpentFirst` | promos are spent in insertion or grant order |
| `TestPurchasedCreditIsSpentOldestFirst` | purchases are spent newest first |
| `TestExpiredCreditIsNotSpentEvenBeforeTheSweepRuns` | a lapsed lot is spendable, or the expiry boundary is inclusive |
| `TestAChargeTooLargeIsRefusedWholeRatherThanPartly` | a charge is partly applied, or a zero-amount one is accepted |
| `TestTheOrderIsTotal` | the order depends on input order |
| `TestNinetyDaysUnlessSomebodySaysOtherwise` | the default term moves, a past date is accepted, or purchased credit is given an expiry |
| `TestWhatExpiresSoon` | the warning list includes purchased or already-lapsed credit, or is not soonest-first |

In `internal/wallet/ledger_test.go`, against the real database: that a grant is a lot *and* an entry,
that a repeated key grants or charges once, that a spend is one entry across many lots, that the sweep
writes one entry per lot and leaves a spent lot alone, and that a charge against lapsed credit is
refused without writing anything.

In `apps/backend/cmd/control-api/billing_test.go`, through HTTP: the order end to end, the ninety-day
default and a named date, the expiring-soon warning, the two balances diverging before a sweep, a
repeated reference, that an owner holding every permission still cannot grant credit, that
`billing.read` and `billing.manage` are what decide, that automatic top-up refuses a configuration
which would charge in a loop and keeps its amounts when switched off, and that the usage page carries
the plan's allowances beside the numbers.
