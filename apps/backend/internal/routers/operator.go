package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// OperatorCustomers is how the console finds a customer and reads their account (§56): the
// directory, one organization and one project, what they may use, their wallet and their own audit
// log. Mounted under /v1/operator behind a signed-in staff member.
//
// Every route here names a customer, the directory included, since a list of who our customers
// are is theirs as much as ours. So every one asks for a ticket and writes staff_access before it
// runs (core.StaffReads, §104).
func OperatorCustomers() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"))
	lookup := core.RequireStaff(staff.Support, staff.Billing, staff.Ops)
	money := core.RequireStaff(staff.Support, staff.Billing)

	r.Get("/organizations", handlers.SearchOrganizations,
		lookup, core.StaffReads("organization.search"),
		muzak.Summary("Find an organization"),
		muzak.Description("By a slug prefix, part of its name, its id or sequence number, or the "+
			"address of anyone in it; narrowed by plan, account state or how it pays. Newest first, "+
			"each with its plan, live balance and how many projects and members it has."))

	r.Get("/organizations/{org}", handlers.ReadOperatorOrganization,
		lookup, core.StaffReads("organization.read"),
		muzak.Summary("One organization"),
		muzak.Description("Its projects, its members with their roles and when each was last seen, "+
			"invitations outstanding, and its billing account's Stripe identity and terms."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug"))

	r.Get("/projects", handlers.SearchProjects,
		lookup, core.StaffReads("project.search"),
		muzak.Summary("Find a project"),
		muzak.Description("By a slug prefix, part of its name, its id, or the sequence number the "+
			"ingestion and storage pages show. Each with the organization it belongs to."))

	r.Get("/projects/{project}", handlers.ReadOperatorProject,
		lookup, core.StaffReads("project.read"),
		muzak.Summary("One project"),
		muzak.Description("Its policy tier and retention, its environments and every key each has "+
			"had (revoked ones included, never a secret), how much targeting it defines, and the "+
			"last thirty days of its meter."),
		muzak.WithResponseDoc(http.StatusNotFound, "No project has that id"))

	r.Get("/organizations/{org}/entitlements", handlers.ReadEntitlements,
		money, core.StaffReads("entitlements.read"),
		muzak.Summary("What an organization may use"),
		muzak.Description("The plan's allowances, the overrides set for this organization alone, "+
			"and the two layered, which is what is enforced; with its member limit and automatic "+
			"top-ups a day."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug, or it has no billing account"))

	r.Put("/organizations/{org}/entitlements", handlers.SetEntitlements,
		core.RequireStaff(staff.Billing), core.StaffReads("entitlements.set"),
		muzak.Summary("Change what an organization may use"),
		muzak.Description("Merged into what it has: a number sets that override, null clears it back "+
			"to the plan's, and a name not sent is left alone. Enforced from the next player, and "+
			"recorded in the organization's audit log with the previous values and the reason."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug, or it has no billing account"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "An unknown entitlement, a negative amount, or nothing to change"))

	r.Get("/organizations/{org}/billing", handlers.ReadOperatorBilling,
		money, core.StaffReads("billing.read"),
		muzak.Summary("An organization's wallet, as staff see it"),
		muzak.Description("The customer's billing page, plus the account's Stripe customer and "+
			"subscription, its overrides, and the automatic top-up in flight: the key sent to "+
			"Stripe, when, and for how much."),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug, or it has no billing account"))

	r.Get("/organizations/{org}/ledger", handlers.ReadOperatorLedger,
		money, core.StaffReads("billing.ledger"),
		muzak.Summary("An organization's statement"),
		muzak.Description("Every movement of its wallet, newest first, paged with before."))

	r.Get("/organizations/{org}/credit-lots", handlers.ReadOperatorCreditLots,
		money, core.StaffReads("billing.credit_lots"),
		muzak.Summary("An organization's grants"),
		muzak.Description("Those with credit left, in the order they are spent; with all=true the "+
			"spent and lapsed ones too. Each with the staff member who granted it or the payment "+
			"that bought it, and its reference."))

	r.Get("/organizations/{org}/usage", handlers.ReadOperatorUsage,
		money, core.StaffReads("billing.usage"),
		muzak.Summary("An organization's usage"),
		muzak.Description("The customer's usage page: tracked players, events and retained bytes by "+
			"project and by day, with the plan's allowances. Defaults to the current month."),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The range is not two dates, ends before it starts, or is longer than 400 days"))

	r.Get("/organizations/{org}/audit", handlers.ReadOperatorAudit,
		money, core.StaffReads("audit.read"),
		muzak.Summary("An organization's own audit log"),
		muzak.Description("What its members, its keys and PlayerSense staff did to it, as the "+
			"customer reads it: filter by action, target, actor or dates, newest first. Not "+
			"staff_access, which is what staff read."))

	return r
}
