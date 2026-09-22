package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/auditevent"
	"playersense.dev/backend/ent/plan"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/schemas"
)

// Plans and prices through HTTP (ADR 0039): staff publish, the public page reads, and a customer's
// billing page shows where the month stands.

func upTo(n int64) *int64 { return &n }

// launchTiers is the price list as the business stated it: $0.0005 per player to 100,000, $0.0004 to
// 500,000, $0.00035 to 1,000,000 and $0.0003 above.
func launchTiers() []map[string]any {
	return []map[string]any{
		{"up_to": 100_000, "unit_price_micros": 500},
		{"up_to": 500_000, "unit_price_micros": 400},
		{"up_to": 1_000_000, "unit_price_micros": 350},
		{"unit_price_micros": 300},
	}
}

type catalogue struct {
	*stack
	operator *testclient.Client
	anon     *testclient.Client
}

func newCatalogue(t *testing.T) *catalogue {
	t.Helper()
	s := newStack(t)
	return &catalogue{
		stack: s,
		operator: testclient.New(t, s.app,
			testclient.WithHeader("Authorization", "Bearer "+s.settings.AdminToken)),
		anon: testclient.New(t, s.app),
	}
}

func (c *catalogue) createPlan(t *testing.T, body map[string]any) schemas.PlanDetailOut {
	t.Helper()
	if _, named := body["key"]; !named {
		body["key"] = unique("plan")
	}
	if _, named := body["name"]; !named {
		body["name"] = "Plan " + body["key"].(string)
	}
	body["operator"] = "ada@playersense.dev"
	return testclient.Decoded[schemas.PlanDetailOut](
		c.operator.Post("/v1/operator/plans", testclient.JSON(body)).AssertStatus(http.StatusCreated))
}

func (c *catalogue) publish(t *testing.T, planKey string, body map[string]any) *testclient.Response {
	t.Helper()
	body["operator"] = "ada@playersense.dev"
	return c.operator.Post("/v1/operator/plans/"+planKey+"/rate-cards", testclient.JSON(body))
}

// inForce gives a plan a card that started an hour ago — what publishing one and waiting would
// produce, without the suite waiting for the horizon.
func (c *catalogue) inForce(t *testing.T, planKey string, tiers billing.Tiers) {
	t.Helper()
	p, err := c.db.Client.Plan.Query().Where(plan.Key(planKey)).Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Client.RateCard.Create().SetPlanID(p.ID).SetMeter(ratecard.MeterTrackedPlayers).
		SetTiers(tiers).SetEffectiveFrom(time.Now().Add(-time.Hour)).SetCreatedBy("test@playersense.dev").
		Save(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func launchCard() billing.Tiers {
	return billing.Tiers{
		{UpTo: upTo(100_000), UnitPriceMicros: 500}, {UpTo: upTo(500_000), UnitPriceMicros: 400},
		{UpTo: upTo(1_000_000), UnitPriceMicros: 350}, {UnitPriceMicros: 300},
	}
}

// unsetDefault puts the shared database back: a default plan changes what every other suite's
// organizations are provisioned onto.
func (c *catalogue) unsetDefault(t *testing.T, key string) {
	t.Cleanup(func() {
		_, _ = c.db.Client.Plan.Update().Where(plan.Key(key)).SetIsDefault(false).Save(context.Background())
	})
}

// The whole loop the business asked for: staff type the prices in, and the pricing page shows them —
// rendered below a cent, with positions a person can read.
func TestPublishedPricesAppearOnThePublicPricingPage(t *testing.T) {
	c := newCatalogue(t)
	created := c.createPlan(t, map[string]any{
		"name": "Pay as you go", "entitlements": map[string]int64{"included_mau": 10_000},
	})
	card := testclient.Decoded[schemas.RateCardOut](
		c.publish(t, created.Key, map[string]any{"tiers": launchTiers(), "note": "launch"}).
			AssertStatus(http.StatusCreated))
	// "Now" is the horizon: a minute ahead, so every charger has the card before it applies.
	if card.Status != "scheduled" || card.Mode != "graduated" ||
		card.EffectiveFrom.Before(time.Now().Add(50*time.Second)) {
		t.Errorf("published card is %s/%s from %s, want scheduled/graduated about a minute ahead",
			card.Status, card.Mode, card.EffectiveFrom)
	}

	page := testclient.Decoded[schemas.PlansOut](c.anon.Get("/v1/pricing").AssertStatus(http.StatusOK))
	var shown *schemas.PlanDetailOut
	for i := range page.Plans {
		if page.Plans[i].Key == created.Key {
			shown = &page.Plans[i]
		}
	}
	if shown == nil || shown.Scheduled == nil {
		t.Fatalf("the plan or its card is not on the public page")
	}
	want := []struct {
		from  int64
		upTo  *int64
		price string
	}{
		{1, upTo(100_000), "$0.0005"},
		{100_001, upTo(500_000), "$0.0004"},
		{500_001, upTo(1_000_000), "$0.00035"},
		{1_000_001, nil, "$0.0003"},
	}
	if len(shown.Scheduled.Tiers) != len(want) {
		t.Fatalf("tiers = %+v", shown.Scheduled.Tiers)
	}
	for i, w := range want {
		got := shown.Scheduled.Tiers[i]
		if got.From != w.from || got.UnitPrice != w.price || (got.UpTo == nil) != (w.upTo == nil) ||
			(got.UpTo != nil && *got.UpTo != *w.upTo) {
			t.Errorf("tier %d = %+v, want from %d to %v at %s", i, got, w.from, w.upTo, w.price)
		}
	}
	if shown.Scheduled.CreatedBy != "" {
		t.Error("the public page shows which operator published the card")
	}
	if shown.Entitlements["included_mau"] != 10_000 {
		t.Errorf("included players = %d", shown.Entitlements["included_mau"])
	}
}

// Every way a card could price something wrong is refused before it exists.
func TestACardThatCouldMispriceIsRefused(t *testing.T) {
	c := newCatalogue(t)
	key := c.createPlan(t, map[string]any{}).Key

	cases := map[string]map[string]any{
		"a bounded last band": {"tiers": []map[string]any{{"up_to": 100, "unit_price_micros": 1}}},
		"bands out of order": {"tiers": []map[string]any{
			{"up_to": 500, "unit_price_micros": 2}, {"up_to": 100, "unit_price_micros": 1}, {"unit_price_micros": 1},
		}},
		"a negative price": {"tiers": []map[string]any{{"unit_price_micros": -1}}},
		"a start in the past": {
			"tiers": launchTiers(), "effective_from": time.Now().Add(-time.Hour).UTC(),
		},
		"a start sooner than the horizon": {
			"tiers": launchTiers(), "effective_from": time.Now().Add(10 * time.Second).UTC(),
		},
		"a mode nobody defined": {"tiers": launchTiers(), "mode": "discounted"},
	}
	for name, body := range cases {
		res := c.publish(t, key, body)
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422 (%s)", name, res.Status, res.String())
		}
	}

	// And the catalogue's own typos.
	c.operator.Post("/v1/operator/plans", testclient.JSON(map[string]any{
		"key": unique("plan"), "name": "Typo", "operator": "ada@playersense.dev",
		"entitlements": map[string]int64{"included_maus": 1},
	})).AssertStatus(http.StatusUnprocessableEntity)
	c.operator.Post("/v1/operator/plans", testclient.JSON(map[string]any{
		"key": key, "name": "Again", "operator": "ada@playersense.dev",
	})).AssertStatus(http.StatusConflict)
}

// A price change is scheduled, not applied: the page shows both, and the history marks each.
func TestAScheduledPriceIsShownAsScheduled(t *testing.T) {
	c := newCatalogue(t)
	key := c.createPlan(t, map[string]any{}).Key
	c.inForce(t, key, launchCard())
	next := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	c.publish(t, key, map[string]any{
		"tiers":          []map[string]any{{"unit_price_micros": 250}},
		"effective_from": next,
	}).AssertStatus(http.StatusCreated)

	detail := testclient.Decoded[schemas.PlansOut](c.operator.Get("/v1/operator/plans").AssertStatus(http.StatusOK))
	for _, p := range detail.Plans {
		if p.Key != key {
			continue
		}
		if p.Current == nil || p.Current.Tiers[0].UnitPrice != "$0.0005" {
			t.Errorf("current = %+v", p.Current)
		}
		if p.Scheduled == nil || !p.Scheduled.EffectiveFrom.Equal(next) || p.Scheduled.Tiers[0].UnitPrice != "$0.00025" {
			t.Errorf("scheduled = %+v", p.Scheduled)
		}
	}

	history := testclient.Decoded[schemas.RateCardsOut](
		c.operator.Get("/v1/operator/plans/" + key + "/rate-cards").AssertStatus(http.StatusOK))
	if len(history.Cards) != 2 || history.Cards[0].Status != "scheduled" || history.Cards[1].Status != "current" {
		t.Errorf("history = %+v", history.Cards)
	}
}

// New organizations land on the default plan, and there is only ever one default.
func TestNewOrganizationsLandOnTheDefaultPlan(t *testing.T) {
	c := newCatalogue(t)
	first := c.createPlan(t, map[string]any{"is_default": true}).Key
	c.unsetDefault(t, first)
	second := c.createPlan(t, map[string]any{"is_default": true}).Key
	c.unsetDefault(t, second)

	defaults, err := c.db.Client.Plan.Query().Where(plan.IsDefault(true)).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 1 || defaults[0].Key != second {
		t.Fatalf("defaults = %d, want only %s", len(defaults), second)
	}

	owner := person(t)
	slug := c.createOrg(t, owner)
	got := testclient.Decoded[billingOut](
		c.as(t, owner).Get("/v1/organizations/" + slug + "/billing").AssertStatus(http.StatusOK))
	if got.Plan.Key != second {
		t.Errorf("a new organization is on %q, want the default %q", got.Plan.Key, second)
	}
}

// Moving an organization between plans is a decision about its account, and is in its log.
func TestAssigningAPlanIsRecordedAndShowsTheMonth(t *testing.T) {
	c := newCatalogue(t)
	key := c.createPlan(t, map[string]any{"entitlements": map[string]int64{"included_mau": 5}}).Key
	c.inForce(t, key, launchCard())

	owner := person(t)
	slug := c.createOrg(t, owner)
	c.operator.Put("/v1/operator/organizations/"+slug+"/plan", testclient.JSON(map[string]any{
		"plan": key, "operator": "ada@playersense.dev",
	})).AssertStatus(http.StatusOK)

	org := c.organization(t, slug)
	entries, err := c.db.Client.AuditEvent.Query().
		Where(auditevent.OrganizationID(org.ID), auditevent.Action(string(audit.PlanChanged))).
		All(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("plan changes logged = %d (%v), want 1", len(entries), err)
	}

	var got struct {
		Plan struct {
			Key string `json:"key"`
		} `json:"plan"`
		ArrearsMicros int64                  `json:"arrears_micros"`
		Month         *schemas.MonthUsageOut `json:"month"`
	}
	c.as(t, owner).Get("/v1/organizations/" + slug + "/billing").AssertStatus(http.StatusOK).JSON(&got)
	if got.Plan.Key != key || got.Month == nil || got.Month.Card == nil {
		t.Fatalf("billing = %+v", got)
	}
	if got.Month.IncludedPlayers != 5 || got.Month.Tier != 0 || got.Month.Card.Tiers[0].UnitPrice != "$0.0005" {
		t.Errorf("month = %+v", got.Month)
	}
}

// Nobody but staff can touch the catalogue.
func TestTheCatalogueIsStaffOnly(t *testing.T) {
	c := newCatalogue(t)
	owner := person(t)
	c.createOrg(t, owner)

	c.anon.Get("/v1/operator/plans").AssertStatus(http.StatusUnauthorized)
	c.as(t, owner).Post("/v1/operator/plans", testclient.JSON(map[string]any{
		"key": unique("plan"), "name": "Mine", "operator": "me@studio.example",
	})).AssertStatus(http.StatusUnauthorized)
}
