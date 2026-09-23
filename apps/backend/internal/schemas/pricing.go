package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/billing"
)

// Plans and their prices, as staff edit them and as the pricing page reads them (ADR 0039).
//
// # A price is never edited
//
// A plan's name, description, allowances and whether it is sold are ordinary fields. Its prices are
// rate cards, and a rate card is published, never changed: a new price is a new card with a later
// `effective_from`, and every player is charged at the card in force when they arrived. So the admin
// form for "change the price" is "publish a card", and the history of what a price was is the list
// of cards rather than an audit trail somebody has to reconstruct.

// ---- rate cards -------------------------------------------------------------

// TierIn is one price band as staff type it.
type TierIn struct {
	// UpTo is the last player position the band covers, inclusive. Absent on the last band,
	// which covers everything above.
	UpTo *int64 `json:"up_to,omitzero" doc:"Inclusive. Absent on the last band only"`

	UnitPriceMicros int64 `json:"unit_price_micros" doc:"Per player, in micros: $0.0005 is 500"`
}

// PublishRateCardIn is a new price list for one plan.
type PublishRateCardIn struct {
	PlanKey string `path:"plan"`

	Meter string   `json:"meter,omitzero" default:"tracked_players" doc:"Only tracked_players today"`
	Mode  string   `json:"mode,omitzero" default:"graduated" doc:"graduated (each band at its own price) or volume (the whole month at the band reached)"`
	Tiers []TierIn `json:"tiers" doc:"Lowest band first"`

	// EffectiveFrom is when the card starts pricing new players. Absent means now. Never in
	// the past: a card that applied to players already charged would say those players cost
	// something they were not charged.
	EffectiveFrom *time.Time `json:"effective_from,omitzero" doc:"Defaults to now; refused if in the past"`

	Note string `json:"note,omitzero" doc:"Why the price changed, for whoever asks next"`

	// Operator is who is publishing it, self-asserted for the reason [GrantCreditIn.Operator]
	// gives.
	Operator string `json:"operator" doc:"Your own address. Stored on the card as self-asserted"`
}

func (in *PublishRateCardIn) Validate(v *muzak.Validation) {
	v.String(&in.PlanKey).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Meter).Trim().Lower().OneOf(string(billing.TrackedPlayers))
	v.String(&in.Mode).Trim().Lower().OneOf(string(billing.Graduated), string(billing.Volume))
	v.Slice(&in.Tiers).MinItems(1).MaxItems(billing.MaxTiers)
	v.String(&in.Note).Trim().MaxLen(500)
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// Card is the input as the arithmetic reads it.
func (in *PublishRateCardIn) Card() billing.Card {
	tiers := make(billing.Tiers, len(in.Tiers))
	for i, tier := range in.Tiers {
		tiers[i] = billing.Tier{UpTo: tier.UpTo, UnitPriceMicros: tier.UnitPriceMicros}
	}
	mode := billing.Mode(in.Mode)
	if mode == "" {
		mode = billing.Graduated
	}
	return billing.Card{Mode: mode, Tiers: tiers}
}

// TierOut is one band, with its price rendered the way a pricing page prints it.
type TierOut struct {
	From            int64  `json:"from" doc:"The first player position in the band"`
	UpTo            *int64 `json:"up_to,omitzero" doc:"The last, inclusive; absent on the last band"`
	UnitPriceMicros int64  `json:"unit_price_micros"`
	UnitPrice       string `json:"unit_price" doc:"$0.0005, not $0.00"`
}

// RateCardOut is one published price list.
type RateCardOut struct {
	ID            uuid.UUID `json:"id"`
	Meter         string    `json:"meter"`
	Mode          string    `json:"mode"`
	Currency      string    `json:"currency"`
	Tiers         []TierOut `json:"tiers"`
	EffectiveFrom time.Time `json:"effective_from"`
	Note          string    `json:"note,omitzero"`
	CreatedBy     string    `json:"created_by,omitzero" doc:"Self-asserted; operator views only"`
	CreatedAt     time.Time `json:"created_at"`

	// Status says where the card sits in time: `current` prices new players now, `scheduled`
	// will, `superseded` did.
	Status string `json:"status" doc:"current, scheduled or superseded"`
}

// RateCardsOut is a plan's price history, newest first.
type RateCardsOut struct {
	Cards []RateCardOut `json:"cards"`
}

// PlanParams names one plan.
type PlanParams struct {
	PlanKey string `path:"plan"`
}

func (in *PlanParams) Validate(v *muzak.Validation) {
	v.String(&in.PlanKey).Trim().Lower().Required().MaxLen(64)
}

// ---- plans ------------------------------------------------------------------

// CreatePlanIn is a new plan. Its prices are published separately, as rate cards.
type CreatePlanIn struct {
	Key         string `json:"key" doc:"Lowercase, permanent: accounts and snapshots refer to it"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`

	MonthlyPriceMicros int64 `json:"monthly_price_micros,omitzero" doc:"Stored and shown; not yet charged"`

	Entitlements map[string]int64 `json:"entitlements,omitzero" doc:"included_mau, included_events, … — the closed vocabulary"`

	IsDefault bool `json:"is_default,omitzero" doc:"The plan new organizations are put on; at most one"`

	Features []string `json:"features,omitzero" doc:"premium_support, slack_reports, mcp_access — the closed vocabulary"`

	Operator string `json:"operator"`
}

func (in *CreatePlanIn) Validate(v *muzak.Validation) {
	v.String(&in.Key).Trim().Lower().Required().MinLen(2).MaxLen(64).Slug()
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
	v.Number(&in.MonthlyPriceMicros).Min(0).Max(float64(MaxGrant))
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// UpdatePlanIn changes what a plan is called, what it includes, and whether it is sold.
//
// Pointers, so absent means unchanged. Not its prices: see [PublishRateCardIn].
type UpdatePlanIn struct {
	PlanKey string `path:"plan"`

	Name               *string          `json:"name,omitzero"`
	Description        *string          `json:"description,omitzero"`
	MonthlyPriceMicros *int64           `json:"monthly_price_micros,omitzero"`
	Entitlements       map[string]int64 `json:"entitlements,omitzero" doc:"Replaces the whole map when present"`
	Active             *bool            `json:"active,omitzero" doc:"Withdrawn plans are not sold; accounts on them stay"`
	IsDefault          *bool            `json:"is_default,omitzero" doc:"true makes this the default and unsets the previous one"`
	Features           []string         `json:"features,omitzero" doc:"Replaces the whole list when present"`

	Operator string `json:"operator"`
}

func (in *UpdatePlanIn) Validate(v *muzak.Validation) {
	v.String(&in.PlanKey).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Name).Trim().MinLen(1).MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
	v.Number(&in.MonthlyPriceMicros).Min(0).Max(float64(MaxGrant))
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// PlanDetailOut is a plan as staff and the pricing page see it.
type PlanDetailOut struct {
	Key                string           `json:"key"`
	Name               string           `json:"name"`
	Description        string           `json:"description,omitzero"`
	MonthlyPriceMicros int64            `json:"monthly_price_micros"`
	Currency           string           `json:"currency"`
	Entitlements       map[string]int64 `json:"entitlements,omitzero"`
	Active             bool             `json:"active"`
	IsDefault          bool             `json:"is_default"`

	// The subscription (ADR 0042): what it costs per month and per year, what it unlocks, and
	// whether it can be bought — a plan with no Stripe price is not subscribable.
	YearlyPriceMicros int64    `json:"yearly_price_micros"`
	Features          []string `json:"features"`
	Subscribable      bool     `json:"subscribable"`
	Intervals         []string `json:"intervals,omitzero" doc:"month, year — the ones this plan can be paid by"`

	// Current is the card pricing new players now, absent when the plan charges nothing for
	// them. Scheduled is the next card waiting to take over, so a pricing page can say "from
	// 1 October".
	Current   *RateCardOut `json:"current,omitzero"`
	Scheduled *RateCardOut `json:"scheduled,omitzero"`
}

// PlansOut is the catalogue.
type PlansOut struct {
	Plans []PlanDetailOut `json:"plans"`
}

// AssignPlanIn moves an organization onto a plan.
type AssignPlanIn struct {
	OrgSlug  string `path:"org"`
	Plan     string `json:"plan" doc:"The plan's key"`
	Operator string `json:"operator"`
}

func (in *AssignPlanIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Plan).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
}

// ---- the month so far -------------------------------------------------------

// MonthUsageOut is the running month, as real-time charging sees it.
//
// Exact rather than estimated: TrackedPlayers is the count the charger has priced, not a peak of
// daily figures, and ChargedMicros is what those players cost. PendingMicros is the part still in
// the queue — at most a few seconds' worth — so a balance that looks a little high is explained.
type MonthUsageOut struct {
	PeriodStart     time.Time `json:"period_start"`
	TrackedPlayers  int64     `json:"tracked_players"`
	IncludedPlayers int64     `json:"included_players" doc:"Free this month under the plan"`

	ChargedMicros int64  `json:"charged_micros" doc:"Every charge this month, drained or not"`
	Charged       string `json:"charged"`
	PendingMicros int64  `json:"pending_micros" doc:"Queued and not yet taken from the wallet; seconds old at most"`

	// Card is the price list in force, and Tier the band the next player lands in, counting
	// from zero — so a dashboard can say "you are in the $0.0004 band; $0.00035 from 500,001".
	Card *RateCardOut `json:"card,omitzero"`
	Tier int          `json:"tier"`
}

// ---- subscriptions (ADR 0042) -----------------------------------------------

// SetSubscriptionPricesIn sets what a plan's subscription costs, and creates the prices at Stripe.
type SetSubscriptionPricesIn struct {
	PlanKey string `path:"plan"`

	MonthlyPriceMicros int64 `json:"monthly_price_micros" doc:"$99 is 99000000; zero for no monthly price"`
	YearlyPriceMicros  int64 `json:"yearly_price_micros" doc:"$999 is 999000000; zero for no yearly price"`

	Operator string `json:"operator"`
}

func (in *SetSubscriptionPricesIn) Validate(v *muzak.Validation) {
	v.String(&in.PlanKey).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.MonthlyPriceMicros).Min(0).Max(float64(MaxGrant))
	v.Number(&in.YearlyPriceMicros).Min(0).Max(float64(MaxGrant))
	v.String(&in.Operator).Trim().Lower().Required().Email().MaxLen(254)
	if in.MonthlyPriceMicros%billing.CentMicros != 0 || in.YearlyPriceMicros%billing.CentMicros != 0 {
		v.Reject(&in.MonthlyPriceMicros, "a subscription is charged in whole cents")
	}
	if in.MonthlyPriceMicros == 0 && in.YearlyPriceMicros == 0 {
		v.Reject(&in.MonthlyPriceMicros, "name a monthly price, a yearly price, or both")
	}
}

// SubscribeIn is a person choosing a plan and how often to pay for it.
type SubscribeIn struct {
	OrgSlug  string `path:"org"`
	Plan     string `json:"plan" doc:"The plan's key, e.g. enterprise"`
	Interval string `json:"interval" doc:"month or year"`
}

func (in *SubscribeIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Plan).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Interval).Trim().Lower().Required().OneOf("month", "year")
}

// SubscriptionOut is the account's subscription, as Stripe last reported it.
type SubscriptionOut struct {
	Status            string     `json:"status" doc:"Stripe's: active, trialing, past_due, canceled, unpaid, …"`
	Interval          string     `json:"interval,omitzero" doc:"month or year"`
	PeriodEnd         *time.Time `json:"period_end,omitzero" doc:"When the paid period ends — and, if canceling, when the plan does"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
}
