package schemas

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/billing"
)

// Paying for credit, through Stripe's own pages (ADR 0040).
//
// Nothing here carries a card. A studio is sent to Stripe Checkout to pay and to Stripe's customer
// portal to manage cards, and comes back to the dashboard; the money arrives by webhook.

// MaxCheckout is the largest single top-up through Checkout. Well above the automatic ceiling,
// because a person typing an amount on purpose is not an unwatched charge; a typo guard, not a policy.
const MaxCheckout int64 = 50_000 * billing.Micros

// TopUpIn is one top-up a person is about to pay for.
type TopUpIn struct {
	OrgSlug      string `path:"org"`
	AmountMicros int64  `json:"amount_micros" doc:"Whole cents, $5 to $50,000. $100 is 100000000"`
}

func (in *TopUpIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.AmountMicros).Min(float64(billing.MinTopUp)).Max(float64(MaxCheckout))
	if in.AmountMicros%billing.CentMicros != 0 {
		v.Reject(&in.AmountMicros, "a card is charged in whole cents")
	}
}

// CheckoutOut is where to send the person to pay.
type CheckoutOut struct {
	URL       string    `json:"url" doc:"Stripe's hosted payment page; redirect the browser here"`
	ID        string    `json:"id" doc:"Stripe's Checkout Session id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PortalOut is where to send the person to manage cards and see receipts.
type PortalOut struct {
	URL string `json:"url" doc:"Stripe's customer portal; redirect the browser here"`
}

// WebhookOut acknowledges a delivery.
type WebhookOut struct {
	Received bool   `json:"received"`
	Status   string `json:"status,omitzero"`
}
