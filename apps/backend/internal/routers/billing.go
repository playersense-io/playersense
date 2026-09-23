package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Billing returns what an organization can see and change about its own account.
//
// `billing.read` and `billing.manage` are the two permissions the BILLING role holds and nothing
// else does — which is the point of that role existing: the person who pays is often not the person
// who should read a player's data, and §08 wants those separable.
//
// # What a customer cannot do here
//
// **Put credit in the wallet.** A studio that could grant itself promotional credit would be a
// studio on an unlimited plan, so granting is an operator action and lives in [OperatorBilling]
// below. `billing.manage` is the widest thing a customer holds, and all it configures is automatic
// charging — a card *they* own being charged an amount *they* set.
func Billing() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("billing"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization does not exist"))

	r.Get("/organizations/{org}/usage", handlers.ReadUsage,
		muzak.Summary("What this organization used"),
		muzak.Description("Tracked players, events and retained bytes over a range, broken down "+
			"by project and by day, with the plan's allowances beside them. Defaults to the "+
			"current calendar month. Tracked players is the busiest day rather than a sum — "+
			"adding daily figures counts one player once per day they played."),
		core.Require(authz.BillingRead),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The range is not two dates, ends before it starts, or is longer than 400 days"))

	r.Get("/organizations/{org}/billing", handlers.ReadBilling,
		muzak.Summary("The plan, the state and the wallet"),
		muzak.Description("`available_micros` is what can be spent now: the sum of every grant "+
			"that has not expired, in the order it will be spent. `ledger_balance_micros` is "+
			"the ledger's running total, which is briefly higher between credit lapsing and the "+
			"sweep writing it off."),
		core.Require(authz.BillingRead),
		muzak.WithResponseDoc(http.StatusNotFound, "This organization has no billing account yet"))

	r.Get("/organizations/{org}/billing/ledger", handlers.ReadLedger,
		muzak.Summary("Why the balance is what it is"),
		muzak.Description("Every movement, newest first, append-only. A promo grant is a `promo` "+
			"rather than a `topup` and lapsed credit is an `expiry` rather than an "+
			"`adjustment` — an adjustment is a decision somebody can be asked about."),
		core.Require(authz.BillingRead))

	r.Put("/organizations/{org}/billing/auto-topup", handlers.SetAutoTopUp,
		muzak.Summary("Configure automatic charging"),
		muzak.Description("A threshold at or above the amount is refused: it would top up into a "+
			"balance still under the threshold, so the next check charges again, and the next. "+
			"Enabling it without a saved card is refused too — it would do nothing, and the "+
			"account would find out by being suspended."),
		core.Require(authz.BillingManage),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The settings cannot work together, or there is no saved card to charge; the message says why"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be asked whether a card is saved"))

	r.Post("/organizations/{org}/billing/top-ups", handlers.CreateTopUp,
		muzak.Summary("Pay for credit"),
		muzak.Description("Opens a Stripe Checkout page for this amount and returns its URL; send the "+
			"browser there. Nothing is credited by this call or by the browser coming back — the "+
			"wallet moves when Stripe reports the payment, usually within seconds. The card used is "+
			"saved at Stripe for automatic top-up; PlayerSense stores none of it."),
		core.Require(authz.BillingManage),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "Below $5, above $50,000, or not whole cents"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	r.Post("/organizations/{org}/billing/subscription", handlers.Subscribe,
		muzak.Summary("Subscribe to a plan"),
		muzak.Description("Opens Stripe Checkout for the plan's monthly or yearly price and returns its "+
			"URL. The organization moves to the plan when Stripe reports the subscription, by webhook. "+
			"An organization that already has a subscription changes or cancels it in the billing "+
			"portal instead."),
		core.Require(authz.BillingManage),
		muzak.WithResponseDoc(http.StatusConflict, "The organization already has a subscription"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The plan cannot be paid at that interval"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	r.Post("/organizations/{org}/billing/portal", handlers.OpenBillingPortal,
		muzak.Summary("Manage cards and receipts"),
		muzak.Description("Opens Stripe's customer portal and returns its URL: add, remove or choose "+
			"the default card, and read receipts. The default card is the one automatic top-up "+
			"charges."),
		core.Require(authz.BillingManage),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	return r
}

// StripeWebhooks is where Stripe reports payments (ADR 0040).
//
// No credential of ours: Stripe's signature over the exact bytes is the credential, which is why the
// body is captured before anything decodes it. Answered 2xx only when the event is recorded and
// handled — anything else and Stripe sends it again.
func StripeWebhooks() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("payments"))

	r.Post("/stripe/webhook", handlers.ReceiveStripeWebhook,
		muzak.Summary("Stripe's events"),
		muzak.Description("Verified by the Stripe-Signature header. A payment that succeeded puts "+
			"credit in its wallet, once, however many times and by however many events it is "+
			"reported; a failed automatic top-up is released and retried later. Everything else is "+
			"recorded and ignored."),
		muzak.CaptureBody(),
		muzak.MaxBodySize(1<<20),
		muzak.WithResponseDoc(http.StatusBadRequest, "The signature does not verify"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Payments are not configured"))

	return r
}

// OperatorBilling returns the credit surface PlayerSense staff use.
//
// # Why this is guarded by a shared secret, which everything else stopped being
//
// §56's admin console does not exist, and neither does the staff-access model behind it. The choice
// was between leaving the wallet unfillable — a wallet nothing can put credit in is a wallet — and
// putting it behind the operator token the configuration already carries. The second, with the cost
// stated rather than hidden:
//
// **A shared secret cannot say who acted.** `CreditLot.granted_by_user_id` is null on this path, so
// "who gave this account $100" has no answer inside the product. That is the first question anybody
// asks when an account has credit nobody expected, and it is recorded as an open item beside the
// audit log it needs rather than left for somebody to discover.
//
// The mitigations that do hold: every grant needs a caller-supplied `reference`, which is both the
// idempotency key and something a person has to be able to explain; nothing here is reachable with a
// customer's credential at any permission; and the ledger records the movement even though it cannot
// record the mover.
func OperatorBilling() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug"))

	r.Post("/organizations/{org}/credits", handlers.GrantCredit,
		muzak.Summary("Add credit to an organization's wallet"),
		muzak.Description("A `promo` expires — ninety days unless `expires_at` names another "+
			"date — and is spent before purchased credit. A `purchase` does not expire and "+
			"cannot be given a date. `granted_at` backdates the grant, which decides where "+
			"purchased credit sits in the spend queue. `reference` is the idempotency key: the "+
			"same one twice grants once."),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusOK,
			"That reference had already been used; the existing lot is returned"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The amount is not positive, or an expiry was named for purchased credit"))

	r.Post("/organizations/{org}/charges", handlers.ChargeCredit,
		muzak.Summary("Spend from an organization's wallet"),
		muzak.Description("Consumes promotional credit first, soonest to expire first, then "+
			"purchased credit oldest first. Refused whole rather than partly when there is not "+
			"enough. One ledger entry for the charge, however many grants paid for it — the "+
			"response says which."),
		muzak.WithResponseDoc(http.StatusConflict,
			"There is not enough credit, and the message names both numbers"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The amount is not positive"))

	return r
}
