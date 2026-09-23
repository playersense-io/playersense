package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// What an organization is using, and what it has left to pay for it.
//
// # Money is micros on the wire, and a rendered string beside it
//
// `available_micros` is what a client computes with; `available` is what it prints. Both, because a
// number that has been through a float is a number somebody reconciles by hand, and a dashboard that
// formats money itself is a dashboard that rounds a cent differently from the invoice.

// ---- usage ------------------------------------------------------------------

// UsageOut is what an organization used over a range.
type UsageOut struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// Totals is the range summed, which is the number on the page's headline.
	Totals UsageTotalsOut `json:"totals"`

	// Included is what the plan allows, so a client can draw the bar rather than compute the
	// allowance from a plan document it would have to fetch separately. Absent entitlements are
	// absent rather than zero: "no free events at all" is a real plan and must not read as
	// "unlimited".
	Included map[string]int64 `json:"included,omitzero"`

	// Projects is the breakdown, and Days is the series. Both, because the two questions a usage
	// page answers — "which game is costing this" and "when did it change" — are different
	// shapes over one set of rows.
	Projects []UsageByProjectOut `json:"projects"`
	Days     []UsageDayOut       `json:"days"`

	// Final says whether every day in the range is settled. A month still open is a month whose
	// numbers can still move, and a page that did not say so would be a page somebody quotes.
	Final bool `json:"final"`
}

// UsageTotalsOut is a summed range.
//
// Tracked players is a **peak of daily values rather than a sum**, because §36's meter is monthly
// tracked players and adding up DAU counts one person once per day they played. The peak is the
// honest reading available from a daily table; the exact monthly figure comes from `metrics_monthly`
// at month close.
type UsageTotalsOut struct {
	PeakDailyPlayers uint64 `json:"peak_daily_players" doc:"The busiest day in the range. Not a sum: adding DAU counts one player once per day"`
	Events           uint64 `json:"events"`
	RetainedBytes    uint64 `json:"retained_bytes" doc:"Archive bytes still inside the retention window on the last day of the range"`
}

// UsageByProjectOut is one game's share.
type UsageByProjectOut struct {
	Project ProjectRefOut  `json:"project"`
	Totals  UsageTotalsOut `json:"totals"`
}

// UsageDayOut is one day across the whole organization.
type UsageDayOut struct {
	Day            time.Time `json:"day"`
	TrackedPlayers uint64    `json:"tracked_players"`
	Events         uint64    `json:"events"`
	RetainedBytes  uint64    `json:"retained_bytes"`
	Final          bool      `json:"final"`
}

// UsageParams is the range a usage page asks for.
type UsageParams struct {
	OrgSlug string `path:"org"`

	// From and To are dates. Absent means the current calendar month, which is what a billing
	// page opens on.
	From string `query:"from,omitzero" doc:"YYYY-MM-DD. Defaults to the first of the current month"`
	To   string `query:"to,omitzero" doc:"YYYY-MM-DD. Defaults to today"`
}

func (in *UsageParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
}

// ---- the wallet -------------------------------------------------------------

// BillingOut is the account, its plan and its wallet.
type BillingOut struct {
	Plan  PlanOut `json:"plan"`
	State string  `json:"state" doc:"active, grace_period (owes; everything works), suspended (ingestion refused until paid), restricted or canceled"`

	Currency string `json:"currency"`

	// Available is what can be spent right now: the sum of what is left in every lot that has
	// not expired. **Not** the account's stored balance, which is the ledger's running total and
	// is briefly higher between a lot lapsing and the sweep writing it off.
	AvailableMicros int64  `json:"available_micros"`
	Available       string `json:"available" doc:"The same number, rendered"`

	// PromoMicros and PurchasedMicros split it, because the two behave differently and a studio
	// planning against a balance needs to know how much of it can evaporate.
	PromoMicros     int64 `json:"promo_micros"`
	PurchasedMicros int64 `json:"purchased_micros"`

	// LedgerBalanceMicros is the account's stored running total. Equal to available once the
	// expiry sweep has caught up; the difference is credit that has lapsed and not yet been
	// written off. Exposed rather than hidden, so a number that looks wrong can be explained.
	LedgerBalanceMicros int64 `json:"ledger_balance_micros"`

	// ArrearsMicros is usage the wallet could not cover when it was charged (ADR 0039). The
	// next top-up pays it before any of it becomes spendable, and while it is above zero the
	// account is in its grace period. Ingestion is not affected.
	ArrearsMicros int64  `json:"arrears_micros"`
	Arrears       string `json:"arrears"`

	// While it owes, when ingestion stops unless it pays (ADR 0043): SuspendsAt by time, and the
	// credit limit by amount — whichever comes first. SuspendsAt is absent when it owes nothing.
	SuspendsAt        *time.Time `json:"suspends_at,omitzero" doc:"If still owing then, ingestion is suspended"`
	CreditLimitMicros int64      `json:"credit_limit_micros" doc:"Owing this much suspends ingestion at once"`
	CreditLimitSource string     `json:"credit_limit_source" doc:"usage (20% of the last 30 days' paid usage), floor (the minimum), or override (set by PlayerSense)"`

	// Live is the wallet as it stands at this request (ADR 0044): the ledger's numbers minus usage
	// queued and not yet written to it. The headline a dashboard shows; the fields above are the
	// statement's, which trail by at most a few minutes.
	Live LiveBalanceOut `json:"live"`

	// Month is the running month as real-time charging sees it: exact players, what they
	// cost, and the band the next one lands in.
	Month *MonthUsageOut `json:"month,omitzero"`

	// Subscription is the plan's subscription as Stripe last reported it, absent when the account
	// has none (ADR 0042). Features is what the current plan unlocks.
	Subscription *SubscriptionOut `json:"subscription,omitzero"`
	Features     []string         `json:"features"`

	Lots []CreditLotOut `json:"lots,omitzero" doc:"Every grant with credit left, in the order it will be spent"`

	// ExpiringSoon is what lapses within the warning window, soonest first. The one number a
	// balance is dangerous without: a studio plans against $100 and loses it on Friday.
	ExpiringSoon []CreditLotOut `json:"expiring_soon,omitzero"`

	AutoTopUp AutoTopUpOut `json:"auto_topup"`
}

// PlanOut is what an organization is on.
type PlanOut struct {
	Key                string           `json:"key"`
	Name               string           `json:"name"`
	MonthlyPriceMicros int64            `json:"monthly_price_micros"`
	Entitlements       map[string]int64 `json:"entitlements,omitzero" doc:"The plan's allowances, with any per-account override already applied"`
}

// CreditLotOut is one grant.
type CreditLotOut struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind" doc:"purchase or promo"`

	AmountMicros    int64  `json:"amount_micros" doc:"What was granted"`
	RemainingMicros int64  `json:"remaining_micros" doc:"What is left of it"`
	Remaining       string `json:"remaining"`

	// ExpiresAt is absent for purchased credit, which does not expire.
	ExpiresAt *time.Time `json:"expires_at,omitzero"`
	GrantedAt time.Time  `json:"granted_at"`

	Description string `json:"description,omitzero"`

	// Expired marks a lot that has lapsed but whose write-off the sweep has not reached. It is
	// in the list rather than filtered out because a customer looking for their $100 needs to see
	// where it went, and an absent row answers nothing.
	Expired bool `json:"expired,omitzero"`
}

// AutoTopUpOut is the automatic-charge configuration.
//
// No card is shown: cards live at Stripe and are managed in its portal (ADR 0040). What is shown is
// whether a top-up is in flight and why the last one did not happen.
type AutoTopUpOut struct {
	Enabled         bool  `json:"enabled"`
	ThresholdMicros int64 `json:"threshold_micros"`
	AmountMicros    int64 `json:"amount_micros"`

	Pending    bool       `json:"pending" doc:"A charge has been sent to Stripe and its outcome is not yet known"`
	Queued     bool       `json:"queued" doc:"The balance is below the threshold and a top-up is due within seconds"`
	DailyLimit int        `json:"daily_limit" doc:"Automatic charges allowed in any 24 hours"`
	UsedToday  int        `json:"used_today" doc:"Automatic charges sent in the current 24-hour window"`
	RetryAfter *time.Time `json:"retry_after,omitzero" doc:"After a decline, not tried again before this"`
	LastError  string     `json:"last_error,omitzero" doc:"Why the last automatic top-up did not happen"`
}

// LedgerOut is a statement.
type LedgerOut struct {
	Entries []LedgerEntryOut `json:"entries"`
}

// LedgerEntryOut is one movement of money.
type LedgerEntryOut struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind" doc:"topup, promo, usage, subscription, adjustment, refund or expiry"`

	AmountMicros       int64  `json:"amount_micros" doc:"Positive adds, negative spends"`
	Amount             string `json:"amount"`
	BalanceAfterMicros int64  `json:"balance_after_micros"`

	Description string    `json:"description,omitzero"`
	At          time.Time `json:"at"`
}

// LedgerParams pages a statement.
type LedgerParams struct {
	OrgSlug string `path:"org"`
	Limit   int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *LedgerParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.Limit).Min(1).Max(200)
}

// SetAutoTopUpIn configures automatic charging.
//
// The rule about how the fields relate lives in [billing.AutoTopUp.Validate]: a threshold at or above
// the amount tops up into a balance still under the threshold, so the next check charges again, and
// the next. There is no card field: whether one exists is asked of Stripe (ADR 0040).
type SetAutoTopUpIn struct {
	OrgSlug string `path:"org"`

	Enabled bool `json:"enabled"`

	// Pointers, so absent means "leave it as it is". The amounts are stored while automatic
	// top-up is off precisely so that turning it back on restores what was configured — and a
	// plain int64 could not tell `{"enabled": false}` from "and set both amounts to nothing".
	ThresholdMicros *int64 `json:"threshold_micros,omitzero" doc:"Absent leaves the stored value alone"`
	AmountMicros    *int64 `json:"amount_micros,omitzero" doc:"Absent leaves the stored value alone"`
}

func (in *SetAutoTopUpIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	// Bounds only. Whether the three make sense *together* is the domain's question, asked in
	// the handler, and asking it twice is how two answers eventually disagree.
	v.Number(&in.ThresholdMicros).Min(0).Max(float64(billing.MaxTopUp))
	v.Number(&in.AmountMicros).Min(0).Max(float64(billing.MaxTopUp))
}

// ---- the operator surface ---------------------------------------------------

// MaxGrant is a typo guard rather than a policy.
//
// A million dollars. An automatic top-up is capped low because nobody is watching it; this is a
// person typing a number on purpose, so the only thing worth refusing is the one with three extra
// zeroes in it.
const MaxGrant int64 = 1_000_000 * billing.Micros

// GrantCreditIn adds credit to an organization's wallet.
//
// The operator's own form: "add $100 to this organization, expiring on that date". Not reachable
// with a customer's credential at any permission — a studio that could grant itself promotional
// credit would be a studio with an unlimited plan.
type GrantCreditIn struct {
	OrgSlug string `path:"org"`

	// Kind decides both the expiry and the accounting. A promo expires and is spent first; a
	// purchase is money that arrived and appears in revenue.
	Kind string `json:"kind" doc:"purchase or promo"`

	AmountMicros int64 `json:"amount_micros" doc:"Micros of the account's currency. $100 is 100000000"`

	// ExpiresAt overrides the ninety-day default for a promo. Refused for a purchase: money a
	// customer paid for is not ours to put a clock on.
	ExpiresAt *time.Time `json:"expires_at,omitzero" doc:"Defaults to 90 days for a promo. Not accepted for a purchase"`

	// GrantedAt backdates the grant, which decides where purchased credit sits in the spend
	// queue. Absent means now.
	GrantedAt *time.Time `json:"granted_at,omitzero" doc:"Defaults to now. Backdate to place an invoice payment where it belongs in the queue"`

	Description string `json:"description,omitzero" doc:"What a customer reads on their statement"`

	// Reference is the idempotency key, and it is required rather than generated. A grant retried
	// after a timeout must find the lot it already made — and a key the server invented would be
	// different on the retry, which is how an account gets $200.
	Reference string `json:"reference" doc:"An idempotency key derived from what happened: a payment intent, a ticket id"`

	StripeReference string `json:"stripe_reference,omitzero"`

	// Operator is who is doing this, and it is **required and unverified**.
	//
	// The shared operator token cannot say who is holding it, so this is the only name the audit
	// entry will carry — and the entry says it is self-asserted, because a log reading
	// `ada@playersense.dev` would be read as proof that Ada did it when all it proves is that
	// somebody with the token typed her address.
	//
	// Required anyway. A field somebody has to fill in is a field they have to think about, and
	// the alternative was an entry reading "operator (unidentified)" on every credit grant in the
	// product.
	Operator string `json:"operator" doc:"Your own address. Recorded in the customer's audit log as self-asserted"`
}

func (in *GrantCreditIn) Validate(v *muzak.Validation) {
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Kind).Trim().Lower().Required().OneOf(wallet.Purchase, wallet.Promo)
	// Positive, and bounded well above MaxTopUp: an automatic charge is capped because nobody is
	// watching it, and this is a person typing a number on purpose. The ceiling is a typo guard.
	v.Number(&in.AmountMicros).Min(1).Max(float64(MaxGrant))
	v.String(&in.Description).Trim().MaxLen(255)
	v.String(&in.Reference).Trim().Required().MinLen(4).MaxLen(255)
	v.String(&in.StripeReference).Trim().MaxLen(255)
}

// ChargeCreditIn spends from the wallet.
//
// The operator's counterpart to a grant, and the thing that exercises the spend order in production
// before §40's month close exists to do it automatically. A charge names what it is for, because a
// negative line on a statement with no explanation is the one entry a customer always asks about.
type ChargeCreditIn struct {
	OrgSlug string `path:"org"`

	AmountMicros int64  `json:"amount_micros"`
	Kind         string `json:"kind,omitzero" default:"adjustment" doc:"usage, subscription or adjustment"`
	Description  string `json:"description"`
	Reference    string `json:"reference" doc:"An idempotency key derived from what happened"`

	// Operator, for the reason [GrantCreditIn.Operator] gives.
	Operator string `json:"operator" doc:"Your own address. Recorded in the customer's audit log as self-asserted"`
}

func (in *ChargeCreditIn) Validate(v *muzak.Validation) {
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.AmountMicros).Min(1).Max(float64(MaxGrant))
	v.String(&in.Kind).Trim().Lower().OneOf("usage", "subscription", "adjustment")
	v.String(&in.Description).Trim().Required().MinLen(3).MaxLen(255)
	v.String(&in.Reference).Trim().Required().MinLen(4).MaxLen(255)
}

// CreditGrantedOut is the lot, and what the wallet now holds.
type CreditGrantedOut struct {
	Lot CreditLotOut `json:"lot"`

	AvailableMicros int64  `json:"available_micros"`
	Available       string `json:"available"`

	// Granted is false when the reference had already been used, so an operator pressing a button
	// twice is told the second press did nothing rather than shown a second lot.
	Granted bool `json:"granted"`
}

// CreditChargedOut is what a charge consumed, lot by lot.
//
// The draws are here and not on a statement, which carries one line for the charge: a customer wants
// to know they were charged $130 once, and an operator wants to know which credit paid for it.
type CreditChargedOut struct {
	AmountMicros int64          `json:"amount_micros"`
	Draws        []DrawOut      `json:"draws"`
	Entry        LedgerEntryOut `json:"entry"`

	AvailableMicros int64  `json:"available_micros"`
	Available       string `json:"available"`

	// Charged is false when the reference had already been used.
	Charged bool `json:"charged"`
}

// DrawOut is one lot's contribution to a charge.
type DrawOut struct {
	Lot          uuid.UUID `json:"lot"`
	Kind         string    `json:"kind"`
	AmountMicros int64     `json:"amount_micros"`
}

// LiveBalanceOut is the wallet at this request: the ledger minus what is queued (ADR 0044).
//
// Exact rather than estimated: queued usage is committed the moment players are counted, and the
// split is projected with the same spending order the drain will apply — promotional credit soonest to
// expire first, then purchased oldest first — so it is what the statement will say once the queue is
// written.
type LiveBalanceOut struct {
	BalanceMicros   int64  `json:"balance_micros" doc:"The headline: credit minus what is owed, after pending usage"`
	Balance         string `json:"balance"`
	AvailableMicros int64  `json:"available_micros" doc:"Credit left to spend, after pending usage"`
	PromoMicros     int64  `json:"promo_micros"`
	PurchasedMicros int64  `json:"purchased_micros"`
	OwedMicros      int64  `json:"owed_micros" doc:"Arrears plus pending usage the credit does not cover"`
	PendingMicros   int64  `json:"pending_micros" doc:"Usage counted and not yet on the statement — minutes old at most"`
	Pending         string `json:"pending"`
}
