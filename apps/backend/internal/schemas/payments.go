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

// ---- cards, invoice details and receipts --------------------------------------

// CardsParams reads the saved cards.
type CardsParams struct {
	OrgSlug string `path:"org"`
	Refresh bool   `query:"refresh,omitzero" doc:"Ask Stripe again rather than reading the copy the webhooks keep"`
}

func (in *CardsParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
}

// CardsOut is the organization's saved cards, as Stripe describes them.
type CardsOut struct {
	Cards    []CardOut  `json:"cards"`
	SyncedAt *time.Time `json:"synced_at,omitzero" doc:"When Stripe was last asked directly; the webhooks keep it current after that"`
}

// CardOut is a saved card: never its number.
type CardOut struct {
	ID        string    `json:"id" doc:"Stripe's payment method id"`
	Brand     string    `json:"brand" doc:"visa, mastercard, amex..."`
	Last4     string    `json:"last4"`
	ExpMonth  int       `json:"exp_month"`
	ExpYear   int       `json:"exp_year"`
	Funding   string    `json:"funding,omitzero" doc:"credit, debit or prepaid"`
	IsDefault bool      `json:"is_default" doc:"What automatic top-up charges"`
	Expired   bool      `json:"expired,omitzero"`
	AddedAt   time.Time `json:"added_at"`
}

// InvoiceDetailsOut is what Stripe prints on the organization's invoices. Edited in the customer
// portal; this is Stripe's own description, copied.
type InvoiceDetailsOut struct {
	Name     string     `json:"name,omitzero"`
	Email    string     `json:"email,omitzero"`
	Address  []string   `json:"address"`
	Country  string     `json:"country,omitzero"`
	TaxIDs   []string   `json:"tax_ids" doc:"Type and value: eu_vat DE123456789"`
	Customer string     `json:"stripe_customer,omitzero" doc:"The Stripe customer these belong to"`
	SyncedAt *time.Time `json:"synced_at,omitzero"`
}

// ReceiptsParams pages the receipts.
type ReceiptsParams struct {
	OrgSlug string `path:"org"`
	Before  string `query:"before,omitzero" doc:"The next cursor of the previous page"`
	Limit   int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *ReceiptsParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Before).Trim().MaxLen(20)
	v.Number(&in.Limit).Min(1).Max(200)
}

// ReceiptsOut is one page of receipts.
type ReceiptsOut struct {
	Receipts []ReceiptOut `json:"receipts"`
	Next     string       `json:"next,omitzero"`
}

// ReceiptOut is a Stripe invoice for a top-up or the plan's subscription.
type ReceiptOut struct {
	ID          string     `json:"id" doc:"Stripe's invoice id"`
	Number      string     `json:"number,omitzero"`
	Kind        string     `json:"kind" doc:"top_up or subscription"`
	Status      string     `json:"status" doc:"open, paid, void or uncollectible"`
	Description string     `json:"description,omitzero"`
	TotalMicros int64      `json:"total_micros"`
	Currency    string     `json:"currency"`
	IssuedAt    time.Time  `json:"issued_at"`
	PaidAt      *time.Time `json:"paid_at,omitzero"`
	HostedURL   string     `json:"hosted_url,omitzero" doc:"Stripe's page for it"`
	PDFURL      string     `json:"pdf_url,omitzero"`
}
