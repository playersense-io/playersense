package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/plan"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/valkey"
)

// Plans, prices and the pricing page (ADR 0039).
//
// Staff edit the catalogue through the operator surface — behind the shared operator token until
// §56's console and its staff identities exist, with the cost that implies stated on
// routers.OperatorBilling. The pricing page reads the same rows through a public route, so a price
// published here is on the landing page the moment it takes effect, with nothing to redeploy.

// ---- public -----------------------------------------------------------------

// ReadPricing is the pricing page: every plan on sale, with the prices in force and any change
// already scheduled.
func ReadPricing(ctx *muzak.Context, _ struct{}) (schemas.PlansOut, error) {
	db := muzak.From[*core.DB](ctx)
	plans, err := db.Client.Plan.Query().
		Where(plan.Active(true)).
		Order(ent.Asc(plan.FieldID)).
		All(ctx.Context())
	if err != nil {
		return schemas.PlansOut{}, core.AsHTTPError(err)
	}
	return plansOut(ctx.Context(), db, plans, nowUTC(), false)
}

// ---- operator: plans --------------------------------------------------------

// ListPlans is the whole catalogue, withdrawn plans included.
func ListPlans(ctx *muzak.Context, _ struct{}) (schemas.PlansOut, error) {
	db := muzak.From[*core.DB](ctx)
	plans, err := db.Client.Plan.Query().Order(ent.Asc(plan.FieldID)).All(ctx.Context())
	if err != nil {
		return schemas.PlansOut{}, core.AsHTTPError(err)
	}
	return plansOut(ctx.Context(), db, plans, nowUTC(), true)
}

// CreatePlan adds a plan. It charges nothing for players until a rate card is published for it.
func CreatePlan(ctx *muzak.Context, in schemas.CreatePlanIn) (schemas.PlanDetailOut, error) {
	db := muzak.From[*core.DB](ctx)

	created, err := inTx(ctx, func(tx *ent.Tx) (*ent.Plan, error) {
		if in.IsDefault {
			if err := clearDefault(ctx.Context(), tx); err != nil {
				return nil, err
			}
		}
		return tx.Plan.Create().
			SetKey(in.Key).
			SetName(in.Name).
			SetDescription(in.Description).
			SetMonthlyPriceMicros(in.MonthlyPriceMicros).
			SetEntitlements(orEmptyLimits(in.Entitlements)).
			SetIsDefault(in.IsDefault).
			Save(ctx.Context())
	})
	if err != nil {
		return schemas.PlanDetailOut{}, catalogueError(err)
	}
	ctx.SetStatus(http.StatusCreated)
	return planOut(ctx.Context(), db, created, nowUTC(), true)
}

// UpdatePlan changes a plan's name, allowances, or whether it is sold or the default.
//
// Making a plan the default unsets the previous default in the same transaction, so there is never
// a moment with two or — worse, for an organization being created at that instant — none.
func UpdatePlan(ctx *muzak.Context, in schemas.UpdatePlanIn) (schemas.PlanDetailOut, error) {
	db := muzak.From[*core.DB](ctx)

	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Plan, error) {
		current, err := tx.Plan.Query().Where(plan.Key(in.PlanKey)).Only(ctx.Context())
		if err != nil {
			return nil, err
		}
		if in.IsDefault != nil && *in.IsDefault && !current.IsDefault {
			if err := clearDefault(ctx.Context(), tx); err != nil {
				return nil, err
			}
		}
		update := tx.Plan.UpdateOne(current)
		if in.Name != nil {
			update = update.SetName(*in.Name)
		}
		if in.Description != nil {
			update = update.SetDescription(*in.Description)
		}
		if in.MonthlyPriceMicros != nil {
			update = update.SetMonthlyPriceMicros(*in.MonthlyPriceMicros)
		}
		if in.Entitlements != nil {
			update = update.SetEntitlements(in.Entitlements)
		}
		if in.Active != nil {
			update = update.SetActive(*in.Active)
		}
		if in.IsDefault != nil {
			update = update.SetIsDefault(*in.IsDefault)
		}
		return update.Save(ctx.Context())
	})
	if err != nil {
		return schemas.PlanDetailOut{}, catalogueError(err)
	}
	// Its allowance may have changed, and chargers price included players from the cache.
	invalidatePlan(ctx, updated.ID)
	return planOut(ctx.Context(), db, updated, nowUTC(), true)
}

// ---- operator: rate cards ---------------------------------------------------

// ListRateCards is a plan's price history, newest first.
func ListRateCards(ctx *muzak.Context, in schemas.PlanParams) (schemas.RateCardsOut, error) {
	db := muzak.From[*core.DB](ctx)
	found, err := db.Client.Plan.Query().Where(plan.Key(in.PlanKey)).Only(ctx.Context())
	if err != nil {
		return schemas.RateCardsOut{}, catalogueError(err)
	}
	cards, err := found.QueryRateCards().
		Order(ent.Desc(ratecard.FieldEffectiveFrom)).
		All(ctx.Context())
	if err != nil {
		return schemas.RateCardsOut{}, core.AsHTTPError(err)
	}
	now := nowUTC()
	out := schemas.RateCardsOut{Cards: make([]schemas.RateCardOut, 0, len(cards))}
	for i, card := range cards {
		// Newest first, so a card is current when it has started and every newer card has
		// not; superseded when a newer one has started.
		status := "scheduled"
		if !card.EffectiveFrom.After(now) {
			status = "current"
			for _, newer := range cards[:i] {
				if !newer.EffectiveFrom.After(now) {
					status = "superseded"
					break
				}
			}
		}
		out.Cards = append(out.Cards, rateCardOut(card, status, true))
	}
	return out, nil
}

// PublishRateCard sets a plan's prices from a moment on.
//
// The card cannot be edited afterwards and cannot start in the past. A price change is therefore
// always "from now" or "from a date", never "as of last Tuesday" — which would say that players
// already charged cost something they were not charged.
func PublishRateCard(ctx *muzak.Context, in schemas.PublishRateCardIn) (schemas.RateCardOut, error) {
	db := muzak.From[*core.DB](ctx)

	card := in.Card()
	if err := card.Validate(); err != nil {
		return schemas.RateCardOut{}, muzak.UnprocessableEntity(err.Error()).WithCode("invalid_tiers")
	}
	// No sooner than the horizon (ADR 0041). Chargers keep a plan's cards in memory for less than
	// that, so every one of them has the card before it applies and no player is priced at the
	// old card after the new one starts. "Now" therefore means "in a minute", and says so in the
	// effective_from it returns.
	now := nowUTC()
	earliest := now.Add(charging.CardHorizon)
	from := earliest
	if in.EffectiveFrom != nil {
		from = in.EffectiveFrom.UTC()
		// Five seconds of slack for an operator's clock. Slack comes out of the margin over
		// charging.LocalTTL, so it stays small.
		if from.Before(earliest.Add(-5 * time.Second)) {
			return schemas.RateCardOut{}, muzak.UnprocessableEntity(
				"a card starts no sooner than a minute from now, so every charger has it before " +
					"it applies; omit effective_from to start as soon as possible").
				WithCode("effective_from_too_soon")
		}
	}

	found, err := db.Client.Plan.Query().Where(plan.Key(in.PlanKey)).Only(ctx.Context())
	if err != nil {
		return schemas.RateCardOut{}, catalogueError(err)
	}
	created, err := db.Client.RateCard.Create().
		SetPlanID(found.ID).
		SetMeter(ratecard.Meter(billing.TrackedPlayers)).
		SetMode(ratecard.Mode(card.Mode)).
		SetCurrency(found.Currency).
		SetTiers(card.Tiers).
		SetEffectiveFrom(from).
		SetNote(in.Note).
		SetCreatedBy(in.Operator).
		Save(ctx.Context())
	if err != nil {
		if ent.IsConstraintError(err) {
			return schemas.RateCardOut{}, muzak.Conflict(
				"this plan already has a card starting at exactly that moment").WithCode("card_exists")
		}
		return schemas.RateCardOut{}, catalogueError(err)
	}
	invalidatePlan(ctx, found.ID)
	ctx.SetStatus(http.StatusCreated)
	return rateCardOut(created, "scheduled", true), nil
}

// ---- operator: an organization's plan ---------------------------------------

// AssignPlan moves an organization onto a plan.
//
// From the next player on: the month's count carries over, so a studio moved from one plan to
// another mid-month keeps its position in the bands and its players already charged stay charged.
func AssignPlan(ctx *muzak.Context, in schemas.AssignPlanIn) (schemas.BillingOut, error) {
	db := muzak.From[*core.DB](ctx)

	org, err := organizationBySlug(ctx.Context(), db, in.OrgSlug)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	target, err := db.Client.Plan.Query().Where(plan.Key(in.Plan)).Only(ctx.Context())
	if err != nil {
		return schemas.BillingOut{}, catalogueError(err)
	}

	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		var none struct{}
		account, err := tx.BillingAccount.Query().
			Where(billingaccount.OrganizationID(org.ID)).
			WithPlan().ForUpdate().Only(ctx.Context())
		previous := ""
		switch {
		case ent.IsNotFound(err):
			if _, err := tx.BillingAccount.Create().
				SetOrganizationID(org.ID).SetPlanID(target.ID).
				SetCurrency(target.Currency).Save(ctx.Context()); err != nil {
				return none, err
			}
		case err != nil:
			return none, err
		default:
			if account.Edges.Plan != nil {
				previous = account.Edges.Plan.Key
			}
			if _, err := tx.BillingAccount.UpdateOne(account).
				SetPlanID(target.ID).Save(ctx.Context()); err != nil {
				return none, err
			}
		}
		return none, auditFor(ctx).forOrganization(org.ID).byOperator(in.Operator).
			record(ctx.Context(), tx, audit.PlanChanged, "billing_account", org.Slug,
				map[string]any{"plan": target.Key, "was_plan": previous})
	})
	if err != nil {
		return schemas.BillingOut{}, catalogueError(err)
	}

	// Chargers price its next player on the new plan once their memory of the old one expires —
	// at most charging.LocalTTL.
	if err := charging.InvalidateOrganization(ctx.Context(), muzak.From[*valkey.Client](ctx), org.ID); err != nil {
		ctx.Logger().Warn("could not invalidate the organization's cached pricing; it expires on its own",
			"org", org.Slug, "error", err)
	}

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	return billingOut(ctx.Context(), db, account, nowUTC())
}

// invalidatePlan deletes a plan's cached pricing. Best effort: the entry expires on its own within
// charging.SharedTTL, and a card never needs it because it cannot start before the horizon.
func invalidatePlan(ctx *muzak.Context, planID int) {
	if err := charging.InvalidatePlan(ctx.Context(), muzak.From[*valkey.Client](ctx), planID); err != nil {
		ctx.Logger().Warn("could not invalidate a plan's cached pricing; it expires on its own",
			"plan_id", planID, "error", err)
	}
}

// ---- the month so far -------------------------------------------------------

// monthOut is the running month for the customer's billing page.
func monthOut(ctx context.Context, db *core.DB, account *ent.BillingAccount, at time.Time) (*schemas.MonthUsageOut, error) {
	period := charging.PeriodOf(at)
	out := &schemas.MonthUsageOut{PeriodStart: period}

	usage, err := db.Client.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(account.ID), monthlyusage.PeriodStart(period)).
		Only(ctx)
	switch {
	case err == nil:
		out.TrackedPlayers = usage.TrackedPlayers
		out.ChargedMicros = usage.ChargedMicros
	case !ent.IsNotFound(err):
		return nil, core.AsHTTPError(err)
	}
	out.Charged = billing.USD(out.ChargedMicros)

	var pending []struct {
		Sum int64 `json:"sum"`
	}
	if err := db.Client.UsageCharge.Query().
		Where(usagecharge.AccountID(account.ID), usagecharge.SettledAtIsNil()).
		Aggregate(ent.Sum(usagecharge.FieldAmountMicros)).
		Scan(ctx, &pending); err != nil {
		return nil, core.AsHTTPError(err)
	}
	if len(pending) == 1 {
		out.PendingMicros = pending[0].Sum
	}

	if resolved := resolvedEntitlements(account); resolved != nil {
		out.IncludedPlayers = resolved[string(billing.IncludedMAU)]
	}

	card, err := db.Client.RateCard.Query().
		Where(
			ratecard.PlanID(account.PlanID),
			ratecard.MeterEQ(ratecard.Meter(billing.TrackedPlayers)),
			ratecard.EffectiveFromLTE(at),
		).
		Order(ent.Desc(ratecard.FieldEffectiveFrom)).
		First(ctx)
	switch {
	case err == nil:
		rendered := rateCardOut(card, "current", false)
		out.Card = &rendered
		out.Tier = billing.Card{Mode: billing.Mode(card.Mode), Tiers: card.Tiers}.TierAt(out.TrackedPlayers + 1)
	case !ent.IsNotFound(err):
		return nil, core.AsHTTPError(err)
	}
	return out, nil
}

// ---- mapping ----------------------------------------------------------------

func plansOut(ctx context.Context, db *core.DB, plans []*ent.Plan, at time.Time, operator bool) (schemas.PlansOut, error) {
	out := schemas.PlansOut{Plans: make([]schemas.PlanDetailOut, 0, len(plans))}
	for _, p := range plans {
		one, err := planOut(ctx, db, p, at, operator)
		if err != nil {
			return schemas.PlansOut{}, err
		}
		out.Plans = append(out.Plans, one)
	}
	return out, nil
}

// planOut renders a plan with the card in force at `at` and the next one scheduled after it.
func planOut(ctx context.Context, db *core.DB, p *ent.Plan, at time.Time, operator bool) (schemas.PlanDetailOut, error) {
	out := schemas.PlanDetailOut{
		Key: p.Key, Name: p.Name, Description: p.Description,
		MonthlyPriceMicros: p.MonthlyPriceMicros, Currency: p.Currency,
		Entitlements: p.Entitlements, Active: p.Active, IsDefault: p.IsDefault,
	}
	base := db.Client.RateCard.Query().Where(
		ratecard.PlanID(p.ID), ratecard.MeterEQ(ratecard.Meter(billing.TrackedPlayers)))

	current, err := base.Clone().Where(ratecard.EffectiveFromLTE(at)).
		Order(ent.Desc(ratecard.FieldEffectiveFrom)).First(ctx)
	switch {
	case err == nil:
		rendered := rateCardOut(current, "current", operator)
		out.Current = &rendered
	case !ent.IsNotFound(err):
		return out, core.AsHTTPError(err)
	}

	next, err := base.Clone().Where(ratecard.EffectiveFromGT(at)).
		Order(ent.Asc(ratecard.FieldEffectiveFrom)).First(ctx)
	switch {
	case err == nil:
		rendered := rateCardOut(next, "scheduled", operator)
		out.Scheduled = &rendered
	case !ent.IsNotFound(err):
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// rateCardOut renders a card. `operator` decides whether who published it is shown: a
// self-asserted staff address is an operator's business, not the pricing page's.
func rateCardOut(card *ent.RateCard, status string, operator bool) schemas.RateCardOut {
	out := schemas.RateCardOut{
		ID: card.UID, Meter: string(card.Meter), Mode: string(card.Mode),
		Currency: card.Currency, EffectiveFrom: card.EffectiveFrom,
		Note: card.Note, CreatedAt: card.CreatedAt, Status: status,
		Tiers: make([]schemas.TierOut, 0, len(card.Tiers)),
	}
	if operator {
		out.CreatedBy = card.CreatedBy
	}
	from := int64(1)
	for _, tier := range card.Tiers {
		out.Tiers = append(out.Tiers, schemas.TierOut{
			From: from, UpTo: tier.UpTo,
			UnitPriceMicros: tier.UnitPriceMicros,
			UnitPrice:       billing.UnitPrice(tier.UnitPriceMicros),
		})
		if tier.UpTo != nil {
			from = *tier.UpTo + 1
		}
	}
	return out
}

// clearDefault unsets whichever plan is the default, so another can become it.
func clearDefault(ctx context.Context, tx *ent.Tx) error {
	return tx.Plan.Update().Where(plan.IsDefault(true)).SetIsDefault(false).Exec(ctx)
}

func orEmptyLimits(in map[string]int64) map[string]int64 {
	if in == nil {
		return map[string]int64{}
	}
	return in
}

// organizationBySlug is the operator path's organization lookup.
func organizationBySlug(ctx context.Context, db *core.DB, slug string) (*ent.Organization, error) {
	org, err := db.Client.Organization.Query().Where(organization.Slug(slug)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, muzak.NotFound("no organization has the slug " + slug)
	}
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return org, nil
}

// catalogueError turns a refusal about the catalogue into the status it deserves.
func catalogueError(err error) error {
	var unknown *billing.UnknownEntitlementError
	var negative *billing.NegativeEntitlementError
	var tiers *billing.TierError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &unknown), errors.As(err, &negative):
		return muzak.UnprocessableEntity(err.Error()).WithCode("invalid_entitlement")
	case errors.As(err, &tiers):
		return muzak.UnprocessableEntity(err.Error()).WithCode("invalid_tiers")
	case ent.IsNotFound(err):
		return muzak.NotFound("no plan or organization by that name").WithCode("not_found")
	case ent.IsConstraintError(err):
		return muzak.Conflict("a plan with that key already exists").WithCode("plan_exists")
	default:
		return core.AsHTTPError(err)
	}
}
