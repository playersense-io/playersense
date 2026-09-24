package schema

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/dataclass"
)

// entitlementsHook refuses a name outside the vocabulary and an allowance below zero.
//
// Applied to the plan and to the per-account overrides, because the override map is the one a
// support engineer edits by hand at two in the morning to unblock a customer — which is
// exactly when "included_maus" gets typed and silently means nothing.
//
// A hook for the same reason Role's is one: ent's JSON fields take no validator, and this is
// the only place every write has to pass through.
func entitlementsHook(get func(ent.Mutation) (map[string]int64, bool)) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if raw, set := get(m); set {
				limits := make(billing.Limits, len(raw))
				for name, amount := range raw {
					limits[billing.Entitlement(name)] = amount
				}
				if err := limits.Validate(); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	}
}

func (Plan) Hooks() []ent.Hook {
	return []ent.Hook{
		entitlementsHook(func(m ent.Mutation) (map[string]int64, bool) {
			mutation, ok := m.(interface {
				Entitlements() (map[string]int64, bool)
			})
			if !ok {
				return nil, false
			}
			return mutation.Entitlements()
		}),
		// Features, against the closed vocabulary, for the same reason entitlements are: a
		// misspelled feature saves and unlocks nothing.
		func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if mutation, ok := m.(interface{ Features() ([]string, bool) }); ok {
					if names, set := mutation.Features(); set {
						if err := billing.ValidateFeatures(names); err != nil {
							return nil, err
						}
					}
				}
				return next.Mutate(ctx, m)
			})
		},
	}
}

func (BillingAccount) Hooks() []ent.Hook {
	return []ent.Hook{entitlementsHook(func(m ent.Mutation) (map[string]int64, bool) {
		mutation, ok := m.(interface {
			EntitlementOverrides() (map[string]int64, bool)
		})
		if !ok {
			return nil, false
		}
		return mutation.EntitlementOverrides()
	})}
}

// Plan is a commercial plan, as a row.
//
// §37: "do not hardcode commercial values in application logic". Raising the free tier from
// 20,000 to 25,000 tracked players is then an UPDATE, not a deploy — and, more to the point,
// the code that enforces the limit never learns what the number is, so there is no second
// place for it to drift.
//
// Prices are Stripe's. `stripe_price_id` points at one, and the amount here is what the
// dashboard renders so it can show a price without a network call. If the two disagree,
// Stripe is right: it is the thing that charged the card.
type Plan struct{ ent.Schema }

func (Plan) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Plan) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Plan) Fields() []ent.Field {
	return []ent.Field{
		// "free", "pro". Immutable: accounts point at the row, but a plan is also found
		// by key when one is seeded or repaired.
		field.String("key").NotEmpty().MaxLen(64).Unique().Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// What a subscription to this plan costs per month, before usage. Zero for free.
		field.Int64("monthly_price_micros").Default(0).Min(0).Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Default("usd").Comment(dataclass.Financial),

		// What a year of the subscription costs, when it can be paid yearly (ADR 0042). Zero when
		// the plan has no yearly price.
		field.Int64("yearly_price_micros").Default(0).Min(0).Comment(dataclass.Financial),

		// Stripe's Product and its two Prices for this plan's subscription. Created by the
		// operator route that sets the prices, never typed in: a Price is immutable at Stripe,
		// so changing a price makes a new one and points here at it. Existing subscribers keep
		// the Price they subscribed at until they change plan. Nil for a plan with no
		// subscription — Free.
		field.String("stripe_product_id").MaxLen(255).Optional().Nillable().Comment(dataclass.None),
		field.String("stripe_price_monthly_id").MaxLen(255).Optional().Nillable().Comment(dataclass.None),
		field.String("stripe_price_yearly_id").MaxLen(255).Optional().Nillable().Comment(dataclass.None),

		// What it unlocks, by billing.Feature: premium Slack support, the Slack report builder,
		// MCP access. Validated against the closed vocabulary by the hook below.
		//
		// A database default as well as a Go one, so the column can be added to a table that
		// already has plans in it.
		field.JSON("features", []string{}).Default([]string{}).
			Annotations(entsql.DefaultExpr("'[]'::jsonb")).Comment(dataclass.None),

		// What it includes, keyed by billing.Entitlement. Validated in Go against the
		// closed vocabulary, so a typo is refused when somebody saves the plan rather than
		// read back months later as a silent zero allowance.
		field.JSON("entitlements", map[string]int64{}).Default(map[string]int64{}).Comment(dataclass.None),

		// Withdrawn rather than deleted: accounts still point at a plan that is no longer
		// sold, and deleting it would leave them pointing at nothing on the one screen
		// where "what am I paying for" has to be answerable.
		field.Bool("active").Default(true).Comment(dataclass.None),

		// The plan a new organization is put on. At most one, enforced where it is set
		// (handlers.UpdatePlan unsets the others in the same transaction) — a partial unique
		// index would say the same thing and refuse the swap mid-transaction.
		field.Bool("is_default").Default(false).Comment(dataclass.None),
	}
}

func (Plan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("accounts", BillingAccount.Type),
		// Its price lists, one per meter per moment it changed (see RateCard).
		edge.To("rate_cards", RateCard.Type),
	}
}

// BillingAccount is one organisation's commercial relationship.
//
// One row per organisation, because §75 makes the organisation the billing boundary: projects
// are how a studio separates its games, not how it separates its invoices.
//
// # Stripe owns money, PlayerSense owns usage
//
// §40 sets the direction and this row is the seam. Usage is priced here (ADR 0039); cards, tax
// and dunning are Stripe's, and everything it decides arrives back as a webhook (see
// StripeEvent). The Stripe identifiers below are therefore pointers, not mirrors. The one
// invoice kept locally is PlayerSense's own usage invoice for a contracted account (ADR 0045) —
// because what it bills is decided here, and Stripe's copy is the delivery of it.
//
// # The balance is ours
//
// Prepaid credit, so a studio can run without a card being charged mid-incident. It is kept
// here rather than in Stripe's customer balance for one reason: the decision to top up depends
// on usage, and usage is computed here. Reading the balance from webhooks would mean deciding
// on a number that is eventually consistent with the thing that spends it.
type BillingAccount struct{ ent.Schema }

func (BillingAccount) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (BillingAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (BillingAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Unique().Immutable().Comment(dataclass.None),
		field.Int("plan_id").Comment(dataclass.None),

		// Stripe's own ids. Nillable because an organisation exists from the moment
		// somebody signs up, and a customer object is not created until there is something
		// to charge — a free account that never upgrades never gets one.
		field.String("stripe_customer_id").MaxLen(255).Optional().Nillable().Unique().
			Comment(dataclass.None),
		field.String("stripe_subscription_id").MaxLen(255).Optional().Nillable().
			Comment(dataclass.None),

		// The subscription as Stripe last reported it (ADR 0042). Stripe owns it — renewals,
		// retries, cancellation in the portal — and these are its latest word, applied only when
		// newer than the word before: webhooks arrive out of order, and an `updated` delivered
		// after a `deleted` must not bring a subscription back.
		//
		//	status       Stripe's: active, trialing, past_due, unpaid, canceled, incomplete, …
		//	interval     month or year
		//	period_end   when the current paid period ends — and, if canceling, when features stop
		field.String("subscription_status").MaxLen(32).Optional().Nillable().Comment(dataclass.None),
		field.Enum("subscription_interval").Values("month", "year").Optional().Nillable().
			Comment(dataclass.None),
		field.Time("subscription_period_end").Optional().Nillable().Comment(dataclass.None),
		field.Bool("subscription_cancel_at_period_end").Default(false).Comment(dataclass.None),
		field.Time("subscription_synced_at").Optional().Nillable().Comment(dataclass.None),

		// §40.1's states, and the reason they exist: a first failed payment must not stop
		// ingestion. Losing a studio's live telemetry over a card that expired is a
		// worse outcome for them than an unpaid invoice is for us, and it is unrecoverable
		// — the events were not buffered anywhere.
		//
		//	active        everything works
		//	grace_period  payment failed, nothing has changed yet
		//	restricted    dashboard reads, ingestion continues
		//	suspended     ingestion refused, data retained
		//	canceled      the relationship ended; retention follows the documented policy
		field.Enum("state").
			Values("active", "grace_period", "restricted", "suspended", "canceled").
			Default("active").Comment(dataclass.None),
		field.Time("state_changed_at").Default(now).Comment(dataclass.None),

		field.String("currency").MaxLen(3).Default("usd").Comment(dataclass.Financial),

		// Prepaid credit in micros. Signed, because a month can close against an account
		// that has run out and the debt is a real number somebody has to settle — clamping
		// it at zero would forgive it silently.
		field.Int64("balance_micros").Default(0).Comment(dataclass.Financial),

		// Usage the wallet could not cover when it was charged (ADR 0039). A real-time charge
		// is never refused — the players were already served — so what credit cannot pay
		// becomes this, and the next grant pays it before becoming spendable credit.
		//
		// Kept as its own number rather than inferred from a negative balance, because the
		// balance also moves with lapsed-but-unswept credit, and "how much does this account
		// owe" must not depend on whether the expiry sweep has run.
		//
		// Invariant: balance_micros = Σ lots.remaining_micros − arrears_micros − receivable_micros.
		field.Int64("arrears_micros").Default(0).Min(0).Comment(dataclass.Financial),

		// How this account pays (ADR 0045). `prepaid` is everything ADRs 0039–0044 describe: a
		// wallet funded by card. `invoiced` is a contracted account billed monthly with payment
		// terms, and only staff move an account to it — through a BillingContract, which says on
		// what terms.
		field.Enum("collection").Values("prepaid", "invoiced").Default("prepaid").
			Comment(dataclass.None),

		// What an invoiced account has used and not yet paid for, invoiced or not (ADR 0045).
		// Usage credit cannot cover lands here rather than in arrears, because it is owed and not
		// overdue: arrears starts the grace clock, and a receivable is only late once an invoice
		// for it passes its due date. Falls when Stripe reports an invoice paid.
		field.Int64("receivable_micros").Default(0).Min(0).Comment(dataclass.Financial),

		// When the first invoice still unpaid passed its due date. Nil while nothing is overdue.
		// Set and cleared by the suspension job and by the payment webhook; the grace before
		// suspension is counted from here, not from when the account first owed.
		field.Time("overdue_since").Optional().Nillable().Comment(dataclass.None),

		// Where the next invoice starts: the end of the last period invoiced, and the last ledger
		// entry that invoice covered. Written by the month close in the same transaction as the
		// invoice, so a close that runs twice finds the month already done.
		field.Time("invoiced_through").Optional().Nillable().Comment(dataclass.None),
		field.Int("invoiced_ledger_id").Default(0).Min(0).Comment(dataclass.None),

		// Automatic top-up. Off by default: a charge nobody authorised at the moment it
		// happens needs to be something a person turned on deliberately.
		field.Bool("auto_topup_enabled").Default(false).Comment(dataclass.None),

		// Charge this much when the balance falls below that much. Both bounded by
		// billing.MinTopUp and MaxTopUp — the floor because a $1 charge is mostly Stripe's
		// fixed fee, the ceiling because an automatic charge is one nobody is watching.
		//
		// Stored even when disabled, so turning it back on restores what was configured
		// rather than presenting an empty form.
		field.Int64("auto_topup_threshold_micros").Default(0).Min(0).
			Max(billing.MaxTopUp).Comment(dataclass.Financial),
		field.Int64("auto_topup_amount_micros").Default(0).Min(0).
			Max(billing.MaxTopUp).Comment(dataclass.Financial),

		// No payment method is stored here, and that is deliberate (ADR 0040). Cards are added,
		// replaced and removed in Stripe's own pages — Checkout and the customer portal — and
		// Stripe is asked which card to use at the moment one is needed. A copy here would be a
		// copy of somebody else's state, stale the moment a customer changed cards in the
		// portal, and a card reference PlayerSense never needed to hold.
		//
		// What *is* kept is the automatic top-up's own bookkeeping, and every column below exists
		// to make one promise: an automatic top-up charges a card at most once per claim.
		//
		// The pending key is written, committed, and only then sent to Stripe as the
		// Idempotency-Key. So a charge whose response was lost — a timeout, a crash between the
		// request and the reply — is retried with the *same* key, and Stripe answers with the
		// payment it already made rather than making a second. The key is cleared only when the
		// outcome is known: credited, declined, or needing the customer.
		field.String("auto_topup_pending_key").MaxLen(255).Optional().Nillable().
			Comment(dataclass.None),
		field.Time("auto_topup_pending_at").Optional().Nillable().Comment(dataclass.None),

		// What the claim charges. Kept with the key because a retry must send exactly the request
		// the key was first sent with — Stripe refuses a reused key with different parameters —
		// and the balance, and so the amount a fresh calculation would give, may have moved.
		field.Int64("auto_topup_pending_amount_micros").Optional().Nillable().Comment(dataclass.Financial),

		// After a decline, not before this. A card that was declined at 09:00 is declined at
		// 09:00:30 too, and asking again every pass is how a customer's bank flags an account.
		field.Time("auto_topup_retry_after").Optional().Nillable().Comment(dataclass.None),

		// Why the last automatic top-up did not happen, in words a customer can act on. Empty
		// once one succeeds.
		field.String("auto_topup_last_error").MaxLen(500).Default("").Comment(dataclass.None),

		// The signal that this account wants an automatic top-up (ADR 0041). Set by the same
		// UPDATE that moved the balance below the threshold — so it commits with the charge that
		// caused it, cannot be lost, and costs no extra statement — and cleared by the one that
		// moves it back above, or by turning automatic top-up off.
		//
		// A queue made of a column: the worker reads only flagged rows, through a partial index
		// that holds nothing else, so polling it every few seconds costs the same with ten
		// accounts as with ten million.
		field.Time("auto_topup_requested_at").Optional().Nillable().Comment(dataclass.None),

		// At most this many automatic charges in any 24 hours (ADR 0041). Three by default: a card
		// charged again and again in a day is what a card network reads as abuse, and one top-up
		// already covers a whole debt, so a legitimate account never needs more. Per account, so
		// staff can raise it for a studio whose volume does.
		field.Int("auto_topup_daily_limit").Default(3).Min(1).Max(48).Comment(dataclass.None),

		// The current window: when it opened and how many charges have been sent in it. Counted
		// when a charge is *claimed* — declined or not, because a decline is what a bank counts —
		// and never for a retry of a claim, which is the same request to Stripe.
		field.Time("auto_topup_window_started_at").Optional().Nillable().Comment(dataclass.None),
		field.Int("auto_topup_window_count").Default(0).Min(0).Comment(dataclass.None),

		// Where invoices go, when it is not the owner's own address. Optional: most
		// studios never set it, and defaulting it to a person's address would send the
		// bill to whoever happened to sign up first.
		field.String("billing_email").MaxLen(254).Optional().Nillable().Comment(dataclass.None),

		// Per-account entitlement overrides, layered onto the plan's. One enterprise
		// customer with ten times the included MAU is this map, not a plan nobody else is
		// on — and not a branch in code, which is where a discount that outlives its deal
		// comes from.
		field.JSON("entitlement_overrides", map[string]int64{}).Default(map[string]int64{}).Comment(dataclass.None),
	}
}

func (BillingAccount) Indexes() []ent.Index {
	return []ent.Index{
		// The automatic top-up queue: only accounts that are asking. Oldest request first.
		index.Fields("auto_topup_requested_at").
			Annotations(entsql.IndexWhere("auto_topup_requested_at IS NOT NULL")),
		// The month close: invoiced accounts, oldest period first.
		index.Fields("invoiced_through").
			Annotations(entsql.IndexWhere("collection = 'invoiced'")),
	}
}

func (BillingAccount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("billing_account").
			Field("organization_id").Unique().Required().Immutable(),
		edge.From("plan", Plan.Type).Ref("accounts").
			Field("plan_id").Unique().Required(),
		edge.To("ledger_entries", LedgerEntry.Type),
		edge.To("credit_lots", CreditLot.Type),
		edge.To("contracts", BillingContract.Type),
		edge.To("invoices", Invoice.Type),
		edge.To("stripe_events", StripeEvent.Type),
		edge.To("monthly_usage", MonthlyUsage.Type),
		edge.To("usage_charges", UsageCharge.Type),
	}
}

// LedgerEntry is one movement of money, and the only answer to "why is my balance this".
//
// Append-only by construction: every field is Immutable and the mixin omits `updated_at`, so
// the generated update builder carries no setter — there is nothing to call that would change
// an entry. A balance that can be edited is a balance nobody can reconcile, and the failure is
// not noticed until a customer disputes a number that has already been changed. A correction
// is a new entry of kind `adjustment`, which is what double-entry bookkeeping has always
// done.
//
// `balance_after_micros` is written rather than recomputed. It costs a column and it makes the
// row self-contained: a statement renders without summing history, and a sum that disagrees
// with the stored running total is a bug that announces itself instead of one that quietly
// produces a different answer each time the ledger is read.
type LedgerEntry struct{ ent.Schema }

func (LedgerEntry) Mixin() []ent.Mixin               { return []ent.Mixin{appendOnlyMixin{}} }
func (LedgerEntry) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (LedgerEntry) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),

		//	topup            money in, from a card
		//	promo            credit granted rather than bought, and therefore expiring
		//	usage            players charged
		//	subscription     the plan fee, or an invoiced contract's platform fee
		//	commitment       an invoiced contract's minimum, topped up at the month close
		//	invoice_payment  an invoice paid (ADR 0045): the receivable falls
		//	adjustment       a human decision: a credit, a correction, a goodwill refund
		//	refund           money back out to the card, or taken back by a bank
		//	expiry           promotional credit that lapsed unspent
		//
		// `promo` is not a `topup` and `expiry` is not an `adjustment`, and neither
		// distinction is cosmetic. A topup is money that arrived — it appears in revenue and
		// it is refundable; a promo is a discount, and a month where the two are added
		// together is a month whose revenue figure is wrong. An adjustment is a human
		// decision somebody can be asked about; an expiry is the calendar, and a customer
		// reading "adjustment -$40" on the day their trial credit lapsed would reasonably
		// ask who did that to them.
		field.Enum("kind").
			Values("topup", "promo", "usage", "subscription", "commitment", "invoice_payment",
				"adjustment", "refund", "expiry").
			Immutable().Comment(dataclass.None),

		// Signed: positive adds to the balance, negative spends it. One column rather than
		// a debit and a credit, because two columns invite a row with both filled in.
		field.Int64("amount_micros").Immutable().Comment(dataclass.Financial),
		field.Int64("balance_after_micros").Immutable().Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Immutable().Comment(dataclass.Financial),

		// What a person reads on a statement. Written at the time, because "Pro plan,
		// March" has to keep saying that after the plan is renamed.
		field.String("description").MaxLen(255).Default("").Immutable().Comment(dataclass.None),

		// The Stripe object behind it — a payment intent, an invoice, a refund. Nillable:
		// an adjustment somebody made in our own admin console has no Stripe counterpart.
		field.String("stripe_reference").MaxLen(255).Optional().Nillable().Immutable().
			Comment(dataclass.None),

		// The month a usage charge belongs to, by arrival (ADR 0039) — so an invoice can say
		// "May" for usage that settled on the first of June. Null for anything that is not usage.
		field.Time("period_start").Optional().Nillable().Immutable().Comment(dataclass.None),

		// How much of this movement became, or cleared, an invoiced account's receivable (ADR
		// 0045): positive for usage credit did not cover, a platform fee or a commitment top-up;
		// negative for a payment, a credit note or a write-off. Zero for every prepaid movement.
		// An invoice bills the accruals — the positive side — between two ledger entries.
		field.Int64("receivable_micros").Default(0).Immutable().Comment(dataclass.Financial),

		// The invoice a settlement settles: a payment, a credit note, a write-off, a dispute.
		// Null on every accrual, which is how the month close tells what to bill from what
		// has already been billed and is now being paid.
		field.Int("invoice_id").Optional().Nillable().Immutable().Comment(dataclass.None),

		// What makes writing this twice harmless.
		//
		// Every path that writes a ledger entry is retried: a webhook is delivered at
		// least once and out of order, a month-close job can be run again after a crash,
		// a top-up can be attempted while the previous attempt's response is still in
		// flight. Without a key that is derived from *what happened* rather than from when
		// we noticed — "usage:<period>", "topup:<payment intent>" — a retry is a second
		// charge, and a second charge against a real card is the one billing bug that
		// cannot be fixed by reprocessing.
		field.String("idempotency_key").NotEmpty().MaxLen(255).Unique().Immutable().
			Comment(dataclass.None),
	}
}

func (LedgerEntry) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("ledger_entries").
			Field("account_id").Unique().Required().Immutable(),
		edge.From("invoice", Invoice.Type).Ref("settlements").
			Field("invoice_id").Unique().Immutable(),
	}
}

func (LedgerEntry) Indexes() []ent.Index {
	return []ent.Index{
		// A statement: one account, newest first.
		index.Fields("account_id", "created_at"),
		// The month close: one account's entries since the last invoice.
		index.Fields("account_id", "id"),
		// What a month's usage came to, to check an invoice against the dashboard.
		index.Fields("account_id", "period_start"),
	}
}

// CreditLot is one grant of credit, with its own remaining balance and its own expiry.
//
// The wallet is lots rather than a number, and the reason is that **promotional credit expires**.
// A single `balance_micros` cannot expire part of itself: told that $140 is $100 of trial credit
// lapsing on Friday and $40 the customer paid for, a scalar has nowhere to keep the distinction and
// no way to take the right half away. So the balance becomes the sum of what is left in each lot,
// and `BillingAccount.balance_micros` becomes what it always was underneath — the ledger's running
// total, reconciled by the expiry sweep.
//
// # What is spent first, and why that order
//
// **Promotional credit, then purchased credit oldest first.**
//
// Promos before purchases because a promo is the only credit that can be *lost*. Spending a
// customer's own money while a trial credit ticks down to nothing is taking payment for something
// they had already been given, and they find out by reading a statement.
//
// Within promos, **soonest expiry first**. The alternative wastes credit by construction: spend a
// year-long promo while a week-long one is open and the week-long one lapses having paid for
// nothing. Nobody would choose that if asked, which is why it is not left to insertion order.
//
// Within purchases, **oldest grant first**. Money that has been on the account longest is spent
// first, which is what a customer assumes and what makes a refund of the most recent top-up
// possible for longest.
//
// # Nothing here is edited
//
// `amount_micros`, `kind` and `expires_at` are immutable. Changing what a grant *was* is how a
// balance stops being reconcilable, and an expiry date in particular: a lot whose date moved is a
// lot whose lapse cannot be predicted from the row. Extending a promo is therefore revoking it and
// granting another, which the ledger records as two movements rather than as a column that used to
// say something else.
//
// `remaining_micros` is the one mutable field, because it is not a fact about the grant — it is
// what is left of it. Every change to it is accompanied by a ledger entry in the same transaction.
type CreditLot struct{ ent.Schema }

func (CreditLot) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (CreditLot) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (CreditLot) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),

		//	purchase  money the customer paid, by card or by invoice. Does not expire
		//	promo     credit granted: a trial, a goodwill gesture, a launch deal
		//
		// The kinds differ in two ways and both follow from that sentence: a promo expires
		// and is spent first, and a promo is not revenue.
		field.Enum("kind").Values("purchase", "promo").Immutable().Comment(dataclass.None),

		// What was granted, and what is left of it. Both non-negative: a lot cannot be
		// granted for a negative amount and cannot be overspent, so a debt is the account's
		// signed balance rather than a lot with a hole in it.
		field.Int64("amount_micros").Immutable().Positive().Comment(dataclass.Financial),
		field.Int64("remaining_micros").Min(0).Comment(dataclass.Financial),

		field.String("currency").MaxLen(3).Default("usd").Immutable().Comment(dataclass.Financial),

		// When this lot becomes unspendable. Null never expires, which is what purchased
		// credit is: a customer's own money does not evaporate.
		//
		// Immutable, so a lapse is predictable from the row. A promo's default is
		// wallet.PromoTerm from the grant, and an operator may name any date instead —
		// that is the whole of "90 days unless we agreed otherwise".
		field.Time("expires_at").Optional().Nillable().Immutable().Comment(dataclass.None),

		// When the credit became available, which is the order purchased credit is spent in.
		// Distinct from created_at on purpose: a lot backdated to when an invoice was paid
		// takes its rightful place in the queue rather than the place the data entry did.
		field.Time("granted_at").Immutable().Default(now).Comment(dataclass.None),

		// What a person reads on a statement, written at the time.
		field.String("description").MaxLen(255).Default("").Immutable().Comment(dataclass.None),

		// The Stripe object behind a purchase. Null for a promo, which has no counterpart
		// anywhere outside this table.
		field.String("stripe_reference").MaxLen(255).Optional().Nillable().Immutable().
			Comment(dataclass.None),

		// The staff member who granted it, by the address their verified sign-in carried.
		// Empty for one created by a payment rather than by a person. A string rather than an
		// edge, for StaffAccess's reason: staff are not users of any organization, and the
		// record of who granted credit has to outlive the staff member's row.
		field.String("granted_by_staff").MaxLen(254).Default("").Immutable().Comment(dataclass.None),

		// The same protection the ledger has, for the same reason: a grant retried after a
		// timeout must not become two lots. Derived from what happened — the payment intent,
		// or the operator's own request id — never from the clock.
		field.String("idempotency_key").NotEmpty().MaxLen(255).Unique().Immutable().
			Comment(dataclass.None),
	}
}

func (CreditLot) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("credit_lots").
			Field("account_id").Unique().Required().Immutable(),
	}
}

func (CreditLot) Indexes() []ent.Index {
	return []ent.Index{
		// The spend query: one account's lots that still have something in them. The order
		// within that set is decided in Go, because "promos by expiry, then purchases by
		// grant" is two orderings over one set and no single index expresses it.
		index.Fields("account_id", "remaining_micros"),

		// The expiry sweep: every account's lots that have lapsed and still hold credit.
		// Across accounts, because the sweep is one pass over the table rather than one pass
		// per customer.
		index.Fields("expires_at", "remaining_micros"),
	}
}

// UsageDaily is the internal meter, one row per project per day.
//
// §40: "PlayerSense, not Stripe, is the source of truth for usage." This table is what that
// sentence means. A worker derives it from the analytics tables each day; a month close sums
// it; Stripe is told the total. Nothing sends a meter event per analytics event — §40 says so
// and the arithmetic agrees, since a billion events a day would be a billion meter calls to
// produce one number.
//
// # Per project, summed per organisation
//
// Billing is per organisation, but a studio with five games needs to know which one costs
// what, and an invoice that cannot be explained by game is an invoice that generates a support
// ticket every month. Summing three hundred rows at close is free; splitting a single stored
// total afterwards is impossible.
//
// # Why tracked players is not summed across days
//
// It is a *distinct count*, so the month's figure is not the sum of its days — a player active
// on twenty days would be counted twenty times. This column is the day's count, kept for the
// dashboard's daily chart; the billable monthly number is computed once at close by an exact
// distinct over the month's own actor set (§38) and kept in MonthlyUsage. The two
// numbers answer different questions and the day one is deliberately not billable.
type UsageDaily struct{ ent.Schema }

func (UsageDaily) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }

// Annotations names the table: ent's pluralizer turns UsageDaily into `usage_dailies`, which
// is not a word.
func (UsageDaily) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments(), entsql.Annotation{Table: "usage_daily"}}
}

func (UsageDaily) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Int("organization_id").Immutable().Comment(dataclass.None),

		// The activity day in UTC, not the day the row was written. A job that runs late
		// must produce the same row it would have produced on time.
		field.Time("day").Immutable().Comment(dataclass.None),

		// Counts, not people. class=none rather than class=derived: §96's derived class is
		// for values still attributable to a person, and "41,207 players were active" is
		// attributable to nobody. Classifying it otherwise would pull an organisation's
		// invoice into the scope of a player's erasure request, which is both wrong and
		// impossible to satisfy.
		field.Uint64("tracked_players").Default(0).Comment(dataclass.None),
		field.Uint64("events_accepted").Default(0).Comment(dataclass.None),

		// §39: canonical compressed archive bytes still inside the retention window, not
		// ClickHouse parts. A shared part holds many organisations and replication is our
		// implementation detail; the archive manifest's object sizes are objective and are
		// what the customer can be told they are paying for.
		field.Uint64("retained_bytes").Default(0).Comment(dataclass.None),

		// Whether the day is settled. A day is written as it goes and rewritten when late
		// events land; after this it is not touched again, which is what makes a closed
		// month reproducible.
		field.Bool("final").Default(false).Comment(dataclass.None),
	}
}

func (UsageDaily) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("usage_daily").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (UsageDaily) Indexes() []ent.Index {
	return []ent.Index{
		// Written once per project per day and rewritten in place, so this is the
		// constraint that makes a re-run an upsert rather than a duplicate.
		index.Fields("day").Edges("project").Unique(),
		// The month close: everything for one organisation in a date range.
		index.Fields("organization_id", "day"),
	}
}

// BillingContract is the terms an organization is billed on, from a moment on (ADR 0045).
//
// Append-only and versioned: new terms are a new row with a later `effective_from`, never an edit.
// A month's invoice uses the contract in force when the month ended, so "what did we agree in March"
// is answerable in September from the rows, not from somebody's memory of what the row used to say.
//
// The account's `collection` is what the charger and the wallet read, on every movement; this is what
// the month close reads, once a month. Both are written in one transaction by the operator route that
// sets a contract.
type BillingContract struct{ ent.Schema }

func (BillingContract) Mixin() []ent.Mixin               { return []ent.Mixin{appendOnlyMixin{}} }
func (BillingContract) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (BillingContract) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),

		// prepaid or invoiced. A prepaid contract is how an invoiced account is moved back, and it
		// is kept rather than deleted because the history of terms is the point of the table.
		field.Enum("collection").Values("prepaid", "invoiced").Immutable().Comment(dataclass.None),

		// When these terms start. A month is billed on the contract in force at its end.
		field.Time("effective_from").Immutable().Comment(dataclass.None),

		// Net terms: an invoice is due this many days after it is issued. And the grace after a
		// missed due date before ingestion is suspended.
		field.Int("terms_days").Default(30).Min(0).Max(120).Immutable().Comment(dataclass.None),
		field.Int("overdue_grace_days").Default(14).Min(0).Max(90).Immutable().Comment(dataclass.None),

		// Billed every month, prorated by the day in the first and last month of the contract.
		// Straight to the receivable: credit is a discount on usage, not on the contract.
		field.Int64("platform_fee_micros").Default(0).Min(0).Immutable().Comment(dataclass.Financial),

		// The least a month's invoice bills for usage. When usage came to less, the difference is
		// a `commitment` line; when it came to more, nothing.
		field.Int64("minimum_commitment_micros").Default(0).Min(0).Immutable().Comment(dataclass.Financial),

		// The exposure cap agreed with the contract, recorded with it. What is enforced is the
		// account's credit_limit_micros override, which the contract route sets from this.
		field.Int64("credit_limit_micros").Optional().Nillable().Min(0).Immutable().
			Comment(dataclass.Financial),

		// Printed on every invoice: a purchase order is how a finance team matches an invoice
		// to the budget it is paid from, and one without it is an invoice that waits.
		field.String("po_number").MaxLen(140).Default("").Immutable().Comment(dataclass.None),
		field.String("memo").MaxLen(500).Default("").Immutable().Comment(dataclass.None),

		// Who set it, self-asserted, for the reason audit.OperatorActor gives.
		field.String("operator").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
	}
}

func (BillingContract) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("contracts").
			Field("account_id").Unique().Required().Immutable(),
		edge.To("invoices", Invoice.Type),
	}
}

func (BillingContract) Indexes() []ent.Index {
	return []ent.Index{
		// The contract in force at a moment: one account's, latest first.
		index.Fields("account_id", "effective_from"),
	}
}

// Invoice is one invoiced account's bill for a period (ADR 0045).
//
// # A stretch of the ledger, not a recomputation
//
// An invoice bills every receivable accrual on its account's ledger after `ledger_from_id` and up to
// `ledger_to_id`: usage credit did not cover, the platform fee, the commitment top-up. So it is never
// computed from anything but the ledger, and a charge that settled late is on the next invoice rather
// than on neither. What it billed is fixed when it is written, and immutable.
//
// # Stripe is told, and answers by webhook
//
// The row is also the claim that issues it at Stripe, the way `auto_topup_pending_key` is for a
// top-up: `issue_key` is committed before Stripe is asked, and every Stripe step is keyed off it, so a
// response lost in flight is resumed as the same invoice rather than sent twice. Its status afterwards
// is Stripe's latest word, applied only when newer than the word before.
type Invoice struct{ ent.Schema }

func (Invoice) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Invoice) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Invoice) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),
		field.Int("contract_id").Optional().Nillable().Immutable().Comment(dataclass.None),

		// Half-open, [start, end). A month, or less: the first month of a contract starts when it
		// did, and a final invoice ends when the account left invoicing.
		field.Time("period_start").Immutable().Comment(dataclass.None),
		field.Time("period_end").Immutable().Comment(dataclass.None),

		// The ledger it bills: entries after `from` and up to `to`.
		field.Int("ledger_from_id").Immutable().Comment(dataclass.None),
		field.Int("ledger_to_id").Immutable().Comment(dataclass.None),

		field.String("currency").MaxLen(3).Default("usd").Immutable().Comment(dataclass.Financial),

		// What it bills, in whole cents as micros. Stripe bills cents and the ledger keeps micros, so
		// the part below a cent is carried to the next invoice rather than rounded: `carry_in` is
		// what the last one left, and `carry_out` what this one leaves.
		field.Int64("total_micros").Min(0).Immutable().Comment(dataclass.Financial),
		field.Int64("carry_in_micros").Default(0).Immutable().Comment(dataclass.Financial),
		field.Int64("carry_out_micros").Default(0).Immutable().Comment(dataclass.Financial),

		// How far the usage billed for the period's month is from what the dashboard said it cost
		// (monthly_usage.charged_micros). Zero when they agree, which they should; kept rather than
		// refused, because the ledger is what is owed and staff need to see the difference.
		field.Int64("discrepancy_micros").Default(0).Immutable().Comment(dataclass.Financial),

		// The terms it was issued on, from the contract at the time.
		field.Int("terms_days").Default(30).Immutable().Comment(dataclass.None),
		field.String("po_number").MaxLen(140).Default("").Immutable().Comment(dataclass.None),
		field.String("memo").MaxLen(500).Default("").Immutable().Comment(dataclass.None),

		// Issued when the account moved back to prepaid, for everything not yet billed.
		field.Bool("final").Default(false).Immutable().Comment(dataclass.None),

		//	draft          written by the close, not yet at Stripe
		//	open           issued; awaiting payment
		//	paid           Stripe reported it paid
		//	void           voided in Stripe; what it billed was written off
		//	uncollectible  marked uncollectible in Stripe; written off
		//	empty          nothing to bill; never sent
		field.Enum("status").Values("draft", "open", "paid", "void", "uncollectible", "empty").
			Default("draft").Comment(dataclass.None),

		// What is still owed on it, and what has been paid. Moved only by settlements, each a
		// ledger entry naming this invoice.
		field.Int64("amount_remaining_micros").Default(0).Min(0).Comment(dataclass.Financial),
		field.Int64("amount_paid_micros").Default(0).Min(0).Comment(dataclass.Financial),

		field.Time("issued_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("due_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("paid_at").Optional().Nillable().Comment(dataclass.None),

		// The claim: committed before Stripe is asked, and the base of every Idempotency-Key the
		// issue sends. `issue_attempted_at` is the latest attempt, so a claim left unanswered is
		// retried after a while rather than at once by every pass.
		field.String("issue_key").NotEmpty().MaxLen(255).Unique().Immutable().Comment(dataclass.None),
		field.Time("issue_attempted_at").Optional().Nillable().Comment(dataclass.None),
		field.String("last_error").MaxLen(500).Default("").Comment(dataclass.None),

		// Stripe's side: pointers, and what a customer clicks.
		field.String("stripe_invoice_id").MaxLen(255).Optional().Nillable().Unique().
			Comment(dataclass.None),
		field.String("number").MaxLen(64).Default("").Comment(dataclass.None),
		field.String("hosted_url").MaxLen(2048).Default("").Comment(dataclass.None),
		field.String("pdf_url").MaxLen(2048).Default("").Comment(dataclass.None),

		// The PaymentIntent that paid it, when one did — so a dispute of the payment finds the
		// invoice. Nil for a wire marked paid out of band.
		field.String("payment_intent").MaxLen(255).Optional().Nillable().Comment(dataclass.None),

		// Stripe's clock on the last event applied, so an event delivered late cannot undo a newer
		// one: a `finalized` arriving after `paid` must not reopen it.
		field.Time("stripe_synced_at").Optional().Nillable().Comment(dataclass.None),
	}
}

func (Invoice) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("invoices").
			Field("account_id").Unique().Required().Immutable(),
		edge.From("contract", BillingContract.Type).Ref("invoices").
			Field("contract_id").Unique().Immutable(),
		edge.To("lines", InvoiceLine.Type),
		edge.To("settlements", LedgerEntry.Type),
	}
}

func (Invoice) Indexes() []ent.Index {
	return []ent.Index{
		// One invoice per account per period start, which is also what stops a second close of the
		// same month from writing a second invoice.
		index.Fields("account_id", "period_start").Unique(),
		// The issue queue, and the overdue sweep.
		index.Fields("status", "due_at"),
		index.Fields("payment_intent"),
	}
}

// InvoiceLine is one line of an invoice, as it was billed.
type InvoiceLine struct{ ent.Schema }

func (InvoiceLine) Mixin() []ent.Mixin               { return []ent.Mixin{appendOnlyMixin{}} }
func (InvoiceLine) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (InvoiceLine) Fields() []ent.Field {
	return []ent.Field{
		field.Int("invoice_id").Immutable().Comment(dataclass.None),
		field.Int("position").Min(0).Immutable().Comment(dataclass.None),

		//	platform_fee     the contract's monthly fee
		//	usage            players in the period's month
		//	prior_usage      players in an earlier month, settled after it was invoiced
		//	credits_applied  the part of usage credit paid for; negative
		//	commitment       what the minimum exceeded usage by
		//	adjustment       anything else the ledger accrued: arrears carried in at the switch
		field.Enum("kind").
			Values("platform_fee", "usage", "prior_usage", "credits_applied", "commitment", "adjustment").
			Immutable().Comment(dataclass.None),
		field.String("description").MaxLen(255).Immutable().Comment(dataclass.None),

		// The ledger's exact amount, and what Stripe bills for it in cents. They differ by less than
		// a cent per line; the invoice's total is the sum of the cents.
		field.Int64("amount_micros").Immutable().Comment(dataclass.Financial),
		field.Int64("amount_cents").Immutable().Comment(dataclass.Financial),

		field.Time("period_start").Optional().Nillable().Immutable().Comment(dataclass.None),
	}
}

func (InvoiceLine) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("invoice", Invoice.Type).Ref("lines").
			Field("invoice_id").Unique().Required().Immutable(),
	}
}

func (InvoiceLine) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("invoice_id", "position").Unique(),
	}
}

// StripeEvent is one webhook, recorded before it is acted on.
//
// "We only process webhooks" is the whole integration, and it is why this table exists rather
// than being a nice-to-have log. A webhook is delivered **at least once**, out of order, and
// sometimes months late after Stripe's own retries. Three things follow, and each is a column
// here rather than a convention:
//
//   - **Idempotency.** `stripe_event_id` is unique. A redelivery loses the insert and the
//     handler stops, which is a two-line rule instead of every handler being separately
//     careful.
//   - **Ordering.** `stripe_created_at` is Stripe's clock, not ours. A subscription's
//     `updated` arriving after its `deleted` must not resurrect it, and only Stripe's
//     timestamp can say which came first.
//   - **Replay.** The payload is kept, so a handler fixed on Tuesday can be run again over
//     Monday's events. Without the body there is nothing to re-run and the only recovery is
//     asking the customer what happened.
type StripeEvent struct{ ent.Schema }

func (StripeEvent) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (StripeEvent) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (StripeEvent) Fields() []ent.Field {
	return []ent.Field{
		// "evt_...". The unique constraint is the idempotency mechanism.
		field.String("stripe_event_id").NotEmpty().MaxLen(255).Unique().Immutable().
			Comment(dataclass.None),

		// "invoice.paid", "customer.subscription.updated". A string, not an enum: Stripe
		// adds event types and an enum would make receiving an unknown one a migration
		// instead of a row marked ignored.
		field.String("type").NotEmpty().MaxLen(128).Immutable().Comment(dataclass.None),

		field.String("api_version").MaxLen(64).Default("").Immutable().Comment(dataclass.None),

		// Stripe's own timestamp, which is the only ordering that means anything.
		field.Time("stripe_created_at").Immutable().Comment(dataclass.None),

		// The whole body as delivered. Immutable, so what we acted on stays readable.
		field.JSON("payload", map[string]any{}).Immutable().Comment(dataclass.None),

		// Which account it turned out to concern, once resolved. Nillable because some
		// events name a customer we have never seen — a Stripe dashboard action against
		// an object created by hand — and those are recorded and ignored rather than
		// dropped.
		field.Int("account_id").Optional().Nillable().Comment(dataclass.None),

		//	received   stored, not yet handled
		//	processed  handled
		//	ignored    a type or an object we deliberately do nothing about
		//	failed     handling raised; `error` says what, and it can be replayed
		field.Enum("status").Values("received", "processed", "ignored", "failed").
			Default("received").Comment(dataclass.None),
		field.Time("processed_at").Optional().Nillable().Comment(dataclass.None),
		field.String("error").MaxLen(500).Default("").Comment(dataclass.None),
		field.Int("attempts").Default(0).Min(0).Comment(dataclass.None),
	}
}

func (StripeEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("stripe_events").
			Field("account_id").Unique(),
	}
}

func (StripeEvent) Indexes() []ent.Index {
	return []ent.Index{
		// The retry sweep: what is still received or failed, oldest first.
		index.Fields("status", "stripe_created_at"),
		// And "everything that ever happened to this account", which is the first thing
		// anybody opens when a customer says their card was charged twice.
		index.Fields("account_id", "stripe_created_at"),
	}
}
