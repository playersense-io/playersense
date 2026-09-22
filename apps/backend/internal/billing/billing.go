// Package billing holds the vocabulary a bill is written in.
//
// The split is the same one targeting and authorization use, for the same reason: PlayerSense
// owns a closed set of names, and what those names are *worth* is data. §37 is explicit —
// "do not hardcode commercial values in application logic" — so a plan's included MAU lives in
// a row and changing it is a write, not a deploy.
//
// # Money is int64 micros of one currency unit
//
// $99.00 is 99_000_000. The same representation `events_raw.revenue_micros` uses, so a number
// never changes shape as it moves between the data plane and a bill. Never a float: a cent
// that rounds differently on two machines is a cent somebody has to reconcile by hand, and
// binary floating point cannot represent 0.1 at all.
//
// # What PlayerSense is the source of truth for
//
// **Usage.** §40 is explicit about the direction: meters are computed here from the analytics
// tables and *reported* to Stripe. Stripe owns money — prices, cards, invoices, tax, dunning —
// and everything it decides arrives back as a webhook. Neither side is authoritative for both
// halves, and the seam is the meter report.
package billing

// Money, in micros of the account's currency.
const (
	// MinTopUp and MaxTopUp bound one automatic charge.
	//
	// The floor is a payment-processing floor rather than a policy: a $1 top-up is
	// mostly Stripe's fixed fee, so a threshold that triggers it repeatedly would spend a
	// meaningful fraction of the customer's money on the act of charging them. The
	// ceiling is a blast radius — an automatic charge is one nobody is watching, and a
	// misconfigured threshold on a busy month should cost at most this before a person
	// notices.
	MinTopUp int64 = 5_000_000     // $5
	MaxTopUp int64 = 5_000_000_000 // $5,000

	// Micros is one unit of currency, for reading a number in a test without counting
	// zeroes.
	Micros int64 = 1_000_000

	// CentMicros is one cent. A card is charged in cents, so an amount sent to Stripe is a
	// whole number of these.
	CentMicros int64 = 10_000
)

// Entitlement is one thing a plan includes. The set is closed here; the amounts are rows.
type Entitlement string

const (
	// IncludedMAU is §36's primary meter: players tracked in a billing month. Zero is a
	// real answer and means "no free allowance", so absence and zero must not be confused —
	// which is why Get returns a second value.
	IncludedMAU Entitlement = "included_mau"

	// IncludedEvents is §36's safety meter. A generous allowance that exists so a title
	// sending two thousand events per player per day cannot arrive under a tiny MAU bill.
	IncludedEvents Entitlement = "included_events"

	// IncludedStorageGBMonth is §39's second meter, over canonical compressed archive
	// bytes rather than ClickHouse parts — a shared part holds many organisations and
	// replication overhead is our implementation detail, not the customer's.
	IncludedStorageGBMonth Entitlement = "included_storage_gb_month"

	// MaxRawRetentionDays caps what a project may set, not what it stores today.
	MaxRawRetentionDays Entitlement = "max_raw_retention_days"

	MaxProjects         Entitlement = "max_projects"
	MaxWebhookEndpoints Entitlement = "max_webhook_endpoints"
)

// entitlements is the closed set, in the order a pricing page reads.
var entitlements = []Entitlement{
	IncludedMAU, IncludedEvents, IncludedStorageGBMonth,
	MaxRawRetentionDays, MaxProjects, MaxWebhookEndpoints,
}

// Entitlements returns the vocabulary.
func Entitlements() []Entitlement {
	out := make([]Entitlement, len(entitlements))
	copy(out, entitlements)
	return out
}

// Known reports whether a name is one PlayerSense understands.
func Known(e Entitlement) bool {
	for _, known := range entitlements {
		if known == e {
			return true
		}
	}
	return false
}

// Limits is what a plan includes, as it is stored.
//
// A map rather than columns, and the reason is §37's: adding `max_segments` next quarter is
// then a constant above and a value in six rows, not a migration against a table whose other
// columns are somebody's live plan. The names are validated against the closed set exactly as
// a role's permissions are, so an unknown key is refused when it is written rather than read
// back later as a silent zero.
type Limits map[Entitlement]int64

// Get returns an entitlement and whether the plan states it.
//
// Two values because zero is a real amount — "no free events at all" is a plan somebody may
// sell — and a caller that could not tell it from "unstated" would hand out the free tier's
// allowance to a plan that deliberately withheld it.
func (l Limits) Get(e Entitlement) (int64, bool) {
	value, found := l[e]
	return value, found
}

// Validate refuses a name outside the vocabulary and an amount below zero.
func (l Limits) Validate() error {
	for name, amount := range l {
		if !Known(name) {
			return &UnknownEntitlementError{Name: name}
		}
		if amount < 0 {
			return &NegativeEntitlementError{Name: name, Amount: amount}
		}
	}
	return nil
}

// Resolve layers an account's overrides onto its plan.
//
// One enterprise customer with ten times the included MAU is a row, not a plan nobody else is
// on — and not a special case in code, which is where a discount that outlives the deal comes
// from. An override of zero is honoured, because withholding an allowance the plan includes is
// a thing a negotiation can produce.
func Resolve(plan, overrides Limits) Limits {
	out := make(Limits, len(plan)+len(overrides))
	for name, amount := range plan {
		out[name] = amount
	}
	for name, amount := range overrides {
		out[name] = amount
	}
	return out
}
