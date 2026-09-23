package schemas

import "github.com/google/uuid"

// MeOut is the signed-in person and where they belong: what the dashboard's shell needs on every
// page, in one request.
type MeOut struct {
	Person        PersonOut           `json:"person"`
	Organizations []MeOrganizationOut `json:"organizations" doc:"Every organization you belong to, by name"`
}

// PersonOut is the signed-in person, as Firebase last reported them.
type PersonOut struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified" doc:"False means read-only until the address is confirmed"`
	Name          string    `json:"name,omitzero"`
	AvatarURL     string    `json:"avatar_url,omitzero"`
}

// MeOrganizationOut is one organization the person belongs to, their role in it, and — when that
// role may read billing — its balance.
type MeOrganizationOut struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
	Role MeRoleOut `json:"role"`

	// Balance is null when the role does not hold billing.read — exactly when GET …/billing would
	// refuse — or the organization has no billing account yet.
	Balance *BalanceSummaryOut `json:"balance" doc:"Null when your role may not read billing, or there is no billing account"`
}

// MeRoleOut names a person's role in an organization.
type MeRoleOut struct {
	Key  string `json:"key" doc:"Stable; owner, admin, liveops_manager, developer, growth_manager or billing for the seeded six"`
	Name string `json:"name"`
}

// BalanceSummaryOut is an organization's live balance, headline figures only (ADR 0044). The same
// numbers as `live` on GET …/billing, which carries the rest.
type BalanceSummaryOut struct {
	State           string `json:"state" doc:"active, grace_period, restricted, suspended or canceled"`
	Currency        string `json:"currency"`
	AvailableMicros int64  `json:"available_micros" doc:"The headline: credit left to spend, after usage not yet settled"`
	Available       string `json:"available"`
	OwedMicros      int64  `json:"owed_micros" doc:"What it owes, including usage not yet settled; the headline when not zero"`
	Owed            string `json:"owed"`
	PendingMicros   int64  `json:"pending_micros" doc:"Usage counted and not yet on the statement — minutes old at most"`

	// An invoiced organization (ADR 0045) owes by design; that is not a debt it is late on.
	Collection       string `json:"collection" doc:"prepaid or invoiced"`
	ReceivableMicros int64  `json:"receivable_micros" doc:"For an invoiced organization, what it owes on its invoices, billed or not; not late"`
}
