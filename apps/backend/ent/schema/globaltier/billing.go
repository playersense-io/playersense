package globaltier

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
	return []ent.Hook{entitlementsHook(func(m ent.Mutation) (map[string]int64, bool) {
		mutation, ok := m.(interface {
			Entitlements() (map[string]int64, bool)
		})
		if !ok {
			return nil, false
		}
		return mutation.Entitlements()
	})}
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

		// Stripe's price object. Nillable, because a plan can exist here before anybody
		// has created its counterpart — and because the free plan has no price at all.
		field.String("stripe_price_id").MaxLen(255).Optional().Nillable().Comment(dataclass.None),

		// What it includes, keyed by billing.Entitlement. Validated in Go against the
		// closed vocabulary, so a typo is refused when somebody saves the plan rather than
		// read back months later as a silent zero allowance.
		field.JSON("entitlements", map[string]int64{}).Default(map[string]int64{}).Comment(dataclass.None),

		// Withdrawn rather than deleted: accounts still point at a plan that is no longer
		// sold, and deleting it would leave them pointing at nothing on the one screen
		// where "what am I paying for" has to be answerable.
		field.Bool("active").Default(true).Comment(dataclass.None),
	}
}

func (Plan) Edges() []ent.Edge {
	return []ent.Edge{edge.To("accounts", BillingAccount.Type)}
}

// BillingAccount is one organisation's commercial relationship.
//
// One row per organisation, because §75 makes the organisation the billing boundary: projects
// are how a studio separates its games, not how it separates its invoices.
//
// # Stripe owns money, PlayerSense owns usage
//
// §40 sets the direction and this row is the seam. Meters are computed here from the analytics
// tables and reported to Stripe; cards, invoices, tax and dunning are Stripe's, and everything
// it decides arrives back as a webhook (see StripeEvent). The Stripe identifiers below are
// therefore pointers, not mirrors — there is deliberately no local copy of an invoice, because
// a mirror of somebody else's state is a thing that goes stale silently.
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

		// Stripe's payment method id. Automatic top-up requires one — enforced in Go
		// rather than by a constraint, because "enabled implies present" is a rule about
		// two columns that a person reading this file should find here and not in a
		// trigger.
		field.String("default_payment_method").MaxLen(255).Optional().Nillable().
			Comment(dataclass.None),

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

func (BillingAccount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("billing_account").
			Field("organization_id").Unique().Required().Immutable(),
		edge.From("plan", Plan.Type).Ref("accounts").
			Field("plan_id").Unique().Required(),
		edge.To("ledger_entries", LedgerEntry.Type),
		edge.To("credit_lots", CreditLot.Type),
		edge.To("periods", BillingPeriod.Type),
		edge.To("stripe_events", StripeEvent.Type),
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

		//	topup         money in, from a card
		//	promo         credit granted rather than bought, and therefore expiring
		//	usage         a closed period's charge
		//	subscription  the monthly plan fee
		//	adjustment    a human decision: a credit, a correction, a goodwill refund
		//	refund        money back out to the card
		//	expiry        promotional credit that lapsed unspent
		//
		// `promo` is not a `topup` and `expiry` is not an `adjustment`, and neither
		// distinction is cosmetic. A topup is money that arrived — it appears in revenue and
		// it is refundable; a promo is a discount, and a month where the two are added
		// together is a month whose revenue figure is wrong. An adjustment is a human
		// decision somebody can be asked about; an expiry is the calendar, and a customer
		// reading "adjustment -$40" on the day their trial credit lapsed would reasonably
		// ask who did that to them.
		field.Enum("kind").
			Values("topup", "promo", "usage", "subscription", "adjustment", "refund", "expiry").
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

		// Which billing period a usage charge belongs to, so a statement can group. Null
		// for everything that is not a period charge.
		field.Int("period_id").Optional().Nillable().Immutable().Comment(dataclass.None),

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
		edge.From("period", BillingPeriod.Type).Ref("charges").
			Field("period_id").Unique().Immutable(),
	}
}

func (LedgerEntry) Indexes() []ent.Index {
	return []ent.Index{
		// A statement: one account, newest first.
		index.Fields("account_id", "created_at"),
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

		// Who granted it. Null for one created by a payment rather than by a person.
		field.Int("granted_by_user_id").Optional().Nillable().Immutable().Comment(dataclass.None),

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
		edge.From("granted_by", User.Type).Ref("granted_credits").
			Field("granted_by_user_id").Unique().Immutable(),
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
// distinct over the cell's own monthly actor set (§38) and stored on BillingPeriod. The two
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

// BillingPeriod is one month, frozen.
//
// §40's "immutable billing-period snapshot". The quantities are Immutable, so what was
// invoiced stays readable exactly as it was computed — a customer asking about March gets
// March's numbers, not today's recomputation of them. The reporting fields are not immutable,
// because telling Stripe is a thing that happens after the numbers exist and can fail.
//
// The distinction is the point: **a snapshot's facts are final, its delivery is not.**
type BillingPeriod struct{ ent.Schema }

func (BillingPeriod) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (BillingPeriod) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (BillingPeriod) Fields() []ent.Field {
	return []ent.Field{
		field.Int("account_id").Immutable().Comment(dataclass.None),

		// Half-open, [start, end), so two adjacent months never both claim an instant.
		field.Time("period_start").Immutable().Comment(dataclass.None),
		field.Time("period_end").Immutable().Comment(dataclass.None),

		// The plan as it was, by key and price, not by edge. A plan renamed or repriced in
		// June must not change what May's invoice says it was for.
		field.String("plan_key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),
		field.Int64("plan_price_micros").Default(0).Immutable().Comment(dataclass.Financial),

		// §38's exact distinct, computed once at close from the cell's monthly actor set
		// and never from events_raw. The cell sends the count; the actor set itself stays
		// in the region that produced it, which is what §99 requires.
		field.Uint64("tracked_players").Default(0).Immutable().Comment(dataclass.None),
		field.Uint64("events_accepted").Default(0).Immutable().Comment(dataclass.None),

		// §39's GB-month, as byte-days summed over the period. Stored as the integral
		// rather than the division, so the divisor — which is a calendar fact, 28 to 31 —
		// is applied at render time and a leap February cannot silently change a stored
		// number's meaning.
		field.Uint64("retained_byte_days").Default(0).Immutable().Comment(dataclass.None),

		// What it came to, and what was included. Both stored, because "you used 120k and
		// 100k was included" is the sentence a customer needs and recomputing the
		// allowance later would read today's plan.
		field.JSON("included", map[string]int64{}).Default(map[string]int64{}).Immutable().
			Comment(dataclass.None),
		field.Int64("amount_micros").Default(0).Immutable().Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Default("usd").Immutable().Comment(dataclass.Financial),

		//	open      the month is running; quantities are provisional
		//	closed    the numbers are final
		//	reported  Stripe has the meter events
		//	invoiced  Stripe has issued the invoice
		//	void      closed in error, superseded; kept rather than deleted
		field.Enum("status").Values("open", "closed", "reported", "invoiced", "void").
			Default("open").Comment(dataclass.None),
		field.Time("closed_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("reported_at").Optional().Nillable().Comment(dataclass.None),
		field.String("stripe_invoice_id").MaxLen(255).Optional().Nillable().Comment(dataclass.None),
	}
}

func (BillingPeriod) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", BillingAccount.Type).Ref("periods").
			Field("account_id").Unique().Required().Immutable(),
		edge.To("charges", LedgerEntry.Type),
	}
}

func (BillingPeriod) Indexes() []ent.Index {
	return []ent.Index{
		// One period per account per month, which is also what stops a second close from
		// writing a second snapshot.
		index.Fields("period_start").Edges("account").Unique(),
		index.Fields("status", "period_end"),
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
