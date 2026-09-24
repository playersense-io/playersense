package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// Pricing is the public price list: what the landing page's pricing page reads (ADR 0039).
//
// No credential, on purpose. Prices are published to be read, and serving them from the same rows
// the charger prices from means the page cannot drift from what is charged — a price staff publish
// is on the page the moment it takes effect, with nothing to redeploy. Only plans on sale are
// listed, and who published a card is not: that is an operator's business.
func Pricing() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("pricing"))

	r.Get("/pricing", handlers.ReadPricing,
		muzak.Summary("What PlayerSense costs"),
		muzak.Description("Every plan on sale, its allowances, the price per tracked player in "+
			"force now, and any price change already scheduled. Tiers are player positions within "+
			"a calendar month, inclusive; a graduated card prices each player at the band their "+
			"position falls in."))

	return r
}

// OperatorPricing is the catalogue as PlayerSense staff edit it.
//
// Behind a signed-in staff member with the billing role. Every write is in staff_access with a
// ticket; rate cards carry the staff member's address on the row, and moving an organization between
// plans is in that organization's audit log as well.
//
// # Prices are published, never edited
//
// There is no route that changes a price. A new price is a new card from a moment on, and the
// charger prices each player at the card in force when they arrived — so a price change can never
// reprice a player already charged, and the history of a plan's prices is its list of cards.
func OperatorPricing() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"))

	r.Get("/plans", handlers.ListPlans,
		core.RequireStaff(staff.Billing, staff.Support),
		muzak.Summary("The whole catalogue"),
		muzak.Description("Every plan, withdrawn ones included, with the card in force now and "+
			"the next one scheduled."))

	r.Post("/plans", handlers.CreatePlan,
		core.RequireStaff(staff.Billing), core.StaffReads("plan.create"),
		muzak.Summary("Add a plan"),
		muzak.Description("A plan charges nothing for players until a rate card is published for "+
			"it. `is_default` makes it the plan new organizations are put on, and unsets the "+
			"previous default in the same transaction."),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "A plan with that key exists"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "An entitlement is misspelled or negative"))

	r.Put("/plans/{plan}", handlers.UpdatePlan,
		core.RequireStaff(staff.Billing), core.StaffReads("plan.update"),
		muzak.Summary("Rename a plan, change what it includes, or withdraw it"),
		muzak.Description("Absent fields are left alone. Not its prices — publish a rate card for "+
			"that. Withdrawing a plan stops it being sold; accounts on it stay."),
		muzak.WithResponseDoc(http.StatusNotFound, "No plan has that key"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "An entitlement is misspelled or negative"))

	r.Get("/plans/{plan}/rate-cards", handlers.ListRateCards,
		core.RequireStaff(staff.Billing, staff.Support),
		muzak.Summary("A plan's price history"),
		muzak.Description("Newest first, each marked current, scheduled or superseded."),
		muzak.WithResponseDoc(http.StatusNotFound, "No plan has that key"))

	r.Post("/plans/{plan}/rate-cards", handlers.PublishRateCard,
		core.RequireStaff(staff.Billing), core.StaffReads("plan.rate_card"),
		muzak.Summary("Set a plan's prices from a moment on"),
		muzak.Description("Tiers are player positions within a calendar month, lowest first, the "+
			"last one unbounded; `unit_price_micros` is per player ($0.0005 is 500). "+
			"`effective_from` defaults to now and cannot be in the past. Cards are never edited: "+
			"publish another to change a price."),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusNotFound, "No plan has that key"),
		muzak.WithResponseDoc(http.StatusConflict, "The plan already has a card starting at that moment"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The tiers leave a gap, overlap, or bound the last band; or the card starts in the past"))

	r.Put("/plans/{plan}/subscription", handlers.SetSubscriptionPrices,
		core.RequireStaff(staff.Billing), core.StaffReads("plan.subscription"),
		muzak.Summary("Set what a plan's subscription costs"),
		muzak.Description("Monthly and yearly prices in micros ($99 is 99000000). Creates the plan's "+
			"Stripe Product the first time, and a Stripe Price per interval — Prices cannot be edited, "+
			"so a changed amount is a new Price: new subscribers pay it, existing ones keep theirs "+
			"until they change plan."),
		muzak.WithResponseDoc(http.StatusNotFound, "No plan has that key"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "Neither price named, or not whole cents"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	r.Put("/organizations/{org}/credit-limit", handlers.SetCreditLimit,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.credit_limit"),
		muzak.Summary("Set an organization's credit limit"),
		muzak.Description("How much it may owe before its ingestion is suspended. Without an override "+
			"the limit is 20% of its paid usage in the last 30 days, or the plan's floor if that is higher; "+
			"an override sets it exactly. Omit credit_limit_micros to clear the override. Recorded in "+
			"the organization's audit log."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug"))

	r.Put("/organizations/{org}/plan", handlers.AssignPlan,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.plan"),
		muzak.Summary("Move an organization onto a plan"),
		muzak.Description("From the next player on. The month's count carries over, so the "+
			"organization keeps its place in the bands and players already charged stay charged. "+
			"Recorded in the organization's audit log."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization or plan by that name"))

	return r
}
