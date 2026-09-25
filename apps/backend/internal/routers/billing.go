package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
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

	r.Get("/organizations/{org}/revenue-settings", handlers.ReadRevenueSettings,
		muzak.Summary("How net revenue is estimated"),
		muzak.Description("Each store's share of gross, the usual rate or the organization's own, why net is "+
			"an estimate, and whether revenue can be shown in dollars on this deployment."),
		core.Require(authz.OrgRead))
	r.Put("/organizations/{org}/revenue-settings", handlers.UpdateRevenueSettings,
		muzak.Summary("Set the stores' shares"),
		muzak.Description("Percent of gross each store keeps, as your own statements show it: 15 for Apple's "+
			"Small Business Program or Google's first million, for instance. A store sent with null goes back "+
			"to the usual rate. Only the estimate of net changes; gross is the stores' own figure."),
		core.Require(authz.OrgUpdate))

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

	r.Get("/organizations/{org}/billing/invoices", handlers.ListInvoices,
		muzak.Summary("Monthly invoices"),
		muzak.Description("For an organization billed by invoice (ADR 0045): every invoice it was sent, "+
			"newest first, with its lines — the platform fee, each month's players, the credit that "+
			"paid for some of them, and any top-up to the contract's minimum — its due date, and "+
			"Stripe's page to read and pay it. Empty for a prepaid organization."),
		core.Require(authz.BillingRead))

	r.Get("/organizations/{org}/billing/invoices/{invoice_id}", handlers.ReadInvoice,
		muzak.Summary("One invoice"),
		core.Require(authz.BillingRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No such invoice"))

	r.Get("/organizations/{org}/billing/cards", handlers.ListCards,
		muzak.Summary("The saved cards"),
		muzak.Description("Brand, last four digits, expiry and which is the default, as Stripe describes "+
			"them; never a card number. Read from Stripe the first time and with refresh=true, and "+
			"kept current by its webhooks after that. Cards are managed in the billing portal."),
		core.Require(authz.BillingRead),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "A refresh was asked for and Stripe could not be reached"))

	r.Post("/organizations/{org}/billing/cards", handlers.AddCard,
		muzak.Summary("Save a card"),
		muzak.Description("Opens Stripe Checkout to save a card for automatic top-up without paying, and "+
			"returns its URL. The card is listed when Stripe reports it, usually within seconds."),
		core.Require(authz.BillingManage),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	r.Get("/organizations/{org}/billing/details", handlers.ReadInvoiceDetails,
		muzak.Summary("What the invoices print"),
		muzak.Description("The name, email, address and tax ids Stripe prints on every invoice. Stripe "+
			"holds them and the billing portal edits them; this is Stripe's own description, copied."),
		core.Require(authz.BillingRead))

	r.Get("/organizations/{org}/billing/receipts", handlers.ListReceipts,
		muzak.Summary("Receipts for top-ups and the plan"),
		muzak.Description("Every Stripe invoice for a top-up, automatic or by card, and for the plan's "+
			"subscription, newest first, with Stripe's page and PDF for each. Usage invoices for an "+
			"invoiced organization are under invoices."),
		core.Require(authz.BillingRead))

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
			"reported; a failed automatic top-up is released and retried later; a usage invoice paid, "+
			"credited or voided settles what its organization owes (ADR 0045). Everything else is "+
			"recorded and ignored."),
		muzak.CaptureBody(),
		muzak.MaxBodySize(1<<20),
		muzak.WithResponseDoc(http.StatusBadRequest, "The signature does not verify"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Payments are not configured"))

	return r
}

// OperatorBilling returns the credit surface PlayerSense staff use.
//
// Behind a signed-in staff member with the billing role (core.RequireStaff), and every route names a
// ticket and writes staff_access before it runs (core.StaffReads). "Who gave this account $100" is
// answered three times over: the lot carries the staff member's verified address, the customer's
// audit log names them, and staff_access has the ticket. Every grant also needs a caller-supplied
// `reference`, which is the idempotency key and something a person has to be able to explain, and
// nothing here is reachable with a customer's credential at any permission.
func OperatorBilling() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug"))

	r.Post("/organizations/{org}/credits", handlers.GrantCredit,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.credit"),
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
		core.RequireStaff(staff.Billing), core.StaffReads("billing.charge"),
		muzak.Summary("Spend from an organization's wallet"),
		muzak.Description("Consumes promotional credit first, soonest to expire first, then "+
			"purchased credit oldest first. Refused whole rather than partly when there is not "+
			"enough. One ledger entry for the charge, however many grants paid for it — the "+
			"response says which."),
		muzak.WithResponseDoc(http.StatusConflict,
			"There is not enough credit, and the message names both numbers"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The amount is not positive"))

	r.Put("/organizations/{org}/contract", handlers.SetContract,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.contract"),
		muzak.Summary("Put an organization on a contract"),
		muzak.Description("From now (ADR 0045). `invoiced` bills it monthly: usage its credit does not "+
			"cover, the platform fee (prorated in a part month) and any top-up to the minimum "+
			"commitment go on one Stripe invoice, due `terms_days` after it is sent; ingestion is "+
			"suspended `overdue_grace_days` after a missed due date, or at once when what it owes, "+
			"billed or not, reaches `credit_limit_micros`. It needs a credit limit and a billing email, "+
			"and no live subscription. `prepaid` moves it back, closing what is unbilled on a final "+
			"invoice. Terms are never edited: each call is a new contract. Recorded in the "+
			"organization's audit log."),
		muzak.WithResponseDoc(http.StatusConflict, "The organization has a live subscription"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "No credit limit, or no billing email, for an invoiced contract"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached, or payments are not configured"))

	r.Get("/organizations/{org}/invoices", handlers.ListOperatorInvoices,
		core.RequireStaff(staff.Billing, staff.Support), core.StaffReads("billing.invoices"),
		muzak.Summary("An organization's invoices, as staff see them"),
		muzak.Description("Drafts and failed issues included, with why the last attempt did not issue, "+
			"any difference between the month's usage and the dashboard's, and the ledger range each "+
			"one bills."))

	r.Post("/organizations/{org}/invoices/{invoice_id}/mark-paid", handlers.MarkInvoicePaid,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.invoice_paid"),
		muzak.Summary("Record a wire Stripe did not see"),
		muzak.Description("Marks an open invoice paid out of band at Stripe, and settles it here as "+
			"Stripe's webhook would. `reference` is the transfer's; the same one twice marks it once."),
		muzak.WithResponseDoc(http.StatusConflict, "The invoice is not open"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached"))

	r.Post("/organizations/{org}/invoices/{invoice_id}/credit-notes", handlers.CreditInvoice,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.credit_note"),
		muzak.Summary("Credit part of an open invoice"),
		muzak.Description("Issues a Stripe credit note lowering what the invoice asks for, and what the "+
			"organization owes with it. Whole cents, at most what is still owed."),
		muzak.WithResponseDoc(http.StatusConflict, "The invoice is not open"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "Not whole cents, or more than is owed"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "Stripe could not be reached"))

	r.Get("/accounts", handlers.TriageAccounts,
		core.RequireStaff(staff.Billing, staff.Support), core.StaffReads("billing.triage"),
		muzak.Summary("Accounts that need somebody"),
		muzak.Description("Billing accounts in their grace period, restricted or suspended; with an "+
			"invoice past due; whose last automatic top-up failed; or with a top-up sent to Stripe "+
			"over an hour ago and no answer. Each with every reason it is listed. Narrow with reason."))

	r.Get("/stripe-events", handlers.ListStripeEvents,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.stripe_events"),
		muzak.Summary("What Stripe told us"),
		muzak.Description("Every webhook, newest first, with what became of it: processed, ignored "+
			"with why, or failed with the error and how many times it was tried. Narrow by status, "+
			"type or organization. The bodies are on each event, not in the list."))

	r.Get("/stripe-events/{event_id}", handlers.ReadStripeEvent,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.stripe_event"),
		muzak.Summary("One Stripe event, with its body"),
		muzak.WithResponseDoc(http.StatusNotFound, "No event has that id"))

	r.Post("/stripe-events/{event_id}/replay", handlers.ReplayStripeEvent,
		core.RequireStaff(staff.Billing), core.StaffReads("billing.stripe_replay"),
		muzak.Summary("Handle a Stripe event again"),
		muzak.Description("From the body it arrived with, for after a handler is fixed: an event that "+
			"failed, or was ignored and should not have been. Not signed again; it was verified when it "+
			"arrived. Answered with what handling it did this time, a failure included."),
		muzak.WithResponseDoc(http.StatusConflict, "The event was processed; replaying it could only repeat it"),
		muzak.WithResponseDoc(http.StatusNotFound, "No event has that id"))

	return r
}
