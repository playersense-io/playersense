// Package payments puts money into wallets through Stripe (ADR 0040).
//
// # What Stripe does, and what it does not
//
// Stripe **funds the wallet**. A studio tops up through a Stripe Checkout page, manages its cards in
// Stripe's customer portal, and — when it turns automatic top-up on — is charged off-session when its
// balance runs low. Every successful payment becomes a `purchase` credit lot, which pays any arrears
// first (ADR 0039).
//
// Stripe does **not** meter or price usage: tracked players are priced and charged by the billing
// writer against the wallet (ADR 0039). For a contracted account it **issues and collects the monthly
// invoice** PlayerSense wrote (ADR 0045) — what is on it is decided here. And PlayerSense stores **no
// billing information**: no card, no payment method id, no address. It keeps one pointer — the Stripe
// customer id — and asks Stripe for everything else at the moment it is needed.
//
// # Never twice
//
// Every credit is keyed on the PaymentIntent id, so a payment reported three times — by the
// Checkout Session, by the PaymentIntent, and by the auto top-up's own response — is one lot. Every
// automatic charge is sent with an Idempotency-Key that was committed to the database *before* the
// request, so a charge whose response was lost is retried as the same charge rather than a new one.
package payments

import (
	"context"
	"errors"
	"time"
)

// Metadata PlayerSense puts on every Stripe object it creates, so a webhook can say which account a
// payment belongs to without trusting anything but Stripe's signature.
const (
	MetaAccount = "playersense_account" // the billing account's uid
	MetaKind    = "playersense_kind"    // KindCheckout, KindAutoTopUp or KindUsageInvoice
	MetaKey     = "playersense_topup_key"
	MetaPlan    = "playersense_plan"
	MetaInvoice = "playersense_invoice" // a usage invoice's uid
	MetaLine    = "playersense_line"    // a usage invoice line's position
)

// The ways money arrives.
const (
	KindCheckout  = "checkout"
	KindAutoTopUp = "auto_topup"

	// KindUsageInvoice is a contracted account's monthly invoice (ADR 0045) — the one kind of
	// invoice whose events move money.
	KindUsageInvoice = "usage_invoice"
)

// Payment is a PaymentIntent as the wallet needs it: whose, how much, and whether it happened.
type Payment struct {
	ID       string
	Status   string // Stripe's: succeeded, processing, requires_action, requires_payment_method, canceled
	Customer string
	Currency string

	// AmountReceived is in cents, Stripe's unit. Zero until the payment succeeds.
	AmountReceived int64

	Metadata map[string]string

	// FailureMessage is Stripe's explanation of a decline, when there was one.
	FailureMessage string
}

// Succeeded is whether money arrived.
func (p Payment) Succeeded() bool { return p.Status == "succeeded" }

// CustomerRequest opens a Stripe customer for one billing account.
type CustomerRequest struct {
	AccountUID string // also the idempotency key, so two racing requests open one customer
	Name       string
	Email      string
	OrgSlug    string
}

// CheckoutRequest is one top-up through Stripe's hosted page.
type CheckoutRequest struct {
	AccountUID  string
	Customer    string
	AmountCents int64
	Currency    string
	SuccessURL  string
	CancelURL   string
}

// Checkout is where to send the person.
type Checkout struct {
	ID        string
	URL       string
	ExpiresAt time.Time
}

// OffSessionRequest is one automatic top-up.
type OffSessionRequest struct {
	AccountUID    string
	Customer      string
	PaymentMethod string
	AmountCents   int64
	Currency      string

	// Key is the Idempotency-Key, already committed to the account row.
	Key string
}

// SubscriptionPriceRequest sets a plan's subscription prices at Stripe (ADR 0042).
type SubscriptionPriceRequest struct {
	PlanUID  string
	PlanKey  string
	Name     string
	Product  string // the plan's Stripe Product, when it already has one
	Currency string

	MonthlyCents int64 // zero: no monthly price
	YearlyCents  int64 // zero: no yearly price
}

// SubscriptionPrices is what Stripe calls them.
type SubscriptionPrices struct {
	Product string
	Monthly string
	Yearly  string
}

// SubscriptionCheckoutRequest is one person subscribing through Stripe's hosted page.
type SubscriptionCheckoutRequest struct {
	AccountUID string
	PlanKey    string
	Customer   string
	Price      string
	SuccessURL string
	CancelURL  string
}

// InvoiceRequest is one usage invoice to issue (ADR 0045).
type InvoiceRequest struct {
	AccountUID string
	InvoiceUID string
	Customer   string
	Currency   string

	// DaysUntilDue is the contract's net terms.
	DaysUntilDue int64

	Description string // "PlayerSense, May 2026"
	PONumber    string // printed as a custom field; empty prints none
	Memo        string // the footer

	Lines []InvoiceLine

	// Since is when the claim was first made. An earlier attempt's invoice is looked for among
	// the customer's invoices created after it, so a retry continues that one.
	Since time.Time

	// Key is the base of every step's Idempotency-Key, committed before the first request.
	Key string
}

// InvoiceLine is one line, in cents. Position identifies it, so a resumed issue adds only the lines
// an earlier attempt did not.
type InvoiceLine struct {
	Position    int
	Description string
	AmountCents int64 // negative for a credit
}

// IssuedInvoice is a usage invoice as Stripe holds it.
type IssuedInvoice struct {
	ID       string
	Number   string
	Status   string // Stripe's: draft, open, paid, void, uncollectible
	Customer string
	Currency string

	HostedURL string
	PDFURL    string
	DueAt     time.Time // zero until finalized

	// In cents, Stripe's unit.
	Total      int64
	AmountDue  int64
	AmountPaid int64

	Metadata map[string]string
}

// CreditNoteRequest reduces what an open usage invoice asks for.
type CreditNoteRequest struct {
	AccountUID  string
	InvoiceUID  string
	Invoice     string // Stripe's id
	AmountCents int64
	Memo        string
	Key         string
}

// CreditNote is what Stripe made of one.
type CreditNote struct {
	ID          string
	AmountCents int64
}

// CustomerDetails is what Stripe holds about a customer that its invoices print, and the cards it
// has saved, as Stripe describes them. Never a card number: brand, last four and expiry are all
// Stripe gives out and all PlayerSense keeps.
type CustomerDetails struct {
	Name        string
	Email       string
	Address     []string // the lines, as an invoice prints them
	Country     string
	TaxIDs      []string // "eu_vat DE123456789"
	DefaultCard string   // the payment method id, or empty
	Cards       []Card
}

// Card is one saved card, as Stripe describes it.
type Card struct {
	ID       string
	Brand    string
	Last4    string
	ExpMonth int
	ExpYear  int
	Funding  string
	Country  string
}

// SetupCheckoutRequest opens Stripe's hosted page to save a card without paying.
type SetupCheckoutRequest struct {
	AccountUID string
	Customer   string
	Currency   string
	SuccessURL string
	CancelURL  string
}

// Gateway is the whole of what PlayerSense asks Stripe.
//
// An interface so the rules around it — idempotency, claims, the webhook — are tested without a
// network, and one implementation, Stripe's own SDK.
type Gateway interface {
	CreateCustomer(ctx context.Context, in CustomerRequest) (string, error)
	CreateCheckout(ctx context.Context, in CheckoutRequest) (Checkout, error)
	CreatePortal(ctx context.Context, customer, returnURL string) (string, error)

	// DescribeCustomer is the customer's invoice details and saved cards, for the first time the
	// billing page asks and for a refresh; the webhooks keep them in step after that.
	DescribeCustomer(ctx context.Context, customer string) (CustomerDetails, error)

	// CreateSetupCheckout opens Stripe's hosted page to save a card, for automatic top-up,
	// without charging it.
	CreateSetupCheckout(ctx context.Context, in SetupCheckoutRequest) (Checkout, error)

	// SetDefaultCard makes a saved card the one Stripe charges: invoices, and automatic top-up.
	SetDefaultCard(ctx context.Context, customer, card string) error

	// DetachCard removes a saved card from the customer. Stripe keeps nothing of it to charge.
	DetachCard(ctx context.Context, card string) error

	// ChargeableCard is the payment method an off-session charge would use: the customer's
	// default, or failing that the most recently saved card. Empty when there is none.
	ChargeableCard(ctx context.Context, customer string) (string, error)

	// ChargeOffSession charges without the customer present. A decline is a Payment with a
	// failure status, not an error: an error means the outcome is unknown.
	ChargeOffSession(ctx context.Context, in OffSessionRequest) (Payment, error)

	// FindAutoTopUp looks for the charge a key already made, for a claim whose response was
	// lost long enough ago that Stripe may have forgotten the key.
	FindAutoTopUp(ctx context.Context, customer, key string, since time.Time) (*Payment, error)

	// SetUpSubscription gives a plan a Stripe Product (once) and a Price per interval.
	SetUpSubscription(ctx context.Context, in SubscriptionPriceRequest) (SubscriptionPrices, error)

	// CreateSubscriptionCheckout opens Stripe's hosted page to subscribe to a plan.
	CreateSubscriptionCheckout(ctx context.Context, in SubscriptionCheckoutRequest) (Checkout, error)

	// SetBillingEmail is where Stripe sends a customer's invoices. Stripe refuses to create an
	// invoice it is to send to a customer with no address, so a contract sets one.
	SetBillingEmail(ctx context.Context, customer, email string) error

	// IssueInvoice creates, finalizes and sends a usage invoice with net terms (ADR 0045) — or
	// continues the one an earlier attempt under the same key began. An error means the outcome is
	// unknown and the claim is kept.
	IssueInvoice(ctx context.Context, in InvoiceRequest) (IssuedInvoice, error)

	// PayInvoiceOutOfBand marks an open invoice paid by a transfer Stripe did not see. What the
	// wallet does about it arrives as Stripe's invoice.paid, like any other payment.
	PayInvoiceOutOfBand(ctx context.Context, invoice, key string) (IssuedInvoice, error)

	// CreditInvoice issues a credit note on an open invoice. Applied by its webhook.
	CreditInvoice(ctx context.Context, in CreditNoteRequest) (CreditNote, error)

	// InvoiceOfPayment is the invoice a PaymentIntent paid, or empty when it paid none. For a
	// dispute that arrives before the event saying which invoice its payment paid — Stripe sends
	// invoice_payment.paid up to a minute after the payment, and a dispute can come first.
	InvoiceOfPayment(ctx context.Context, paymentIntent string) (string, error)
}

// ErrNotConfigured is a deployment with no Stripe key.
var ErrNotConfigured = errors.New("payments are not configured on this deployment")

// ErrKeyReused is Stripe refusing an Idempotency-Key it has seen with different parameters.
var ErrKeyReused = errors.New("the idempotency key was already used with different parameters")

// New returns the gateway for a deployment: Stripe when a key is configured, and one that refuses
// every call with ErrNotConfigured when it is not — so a development stack with no Stripe account
// still starts, and says plainly why a top-up cannot be made.
func New(secretKey string) Gateway {
	if secretKey == "" {
		return Disabled{}
	}
	return NewStripe(secretKey)
}

// Disabled is a deployment with no Stripe key.
type Disabled struct{}

func (Disabled) CreateCustomer(context.Context, CustomerRequest) (string, error) {
	return "", ErrNotConfigured
}

func (Disabled) CreateCheckout(context.Context, CheckoutRequest) (Checkout, error) {
	return Checkout{}, ErrNotConfigured
}

func (Disabled) DescribeCustomer(context.Context, string) (CustomerDetails, error) {
	return CustomerDetails{}, ErrNotConfigured
}

func (Disabled) CreateSetupCheckout(context.Context, SetupCheckoutRequest) (Checkout, error) {
	return Checkout{}, ErrNotConfigured
}

func (Disabled) SetDefaultCard(context.Context, string, string) error { return ErrNotConfigured }

func (Disabled) DetachCard(context.Context, string) error { return ErrNotConfigured }

func (Disabled) CreatePortal(context.Context, string, string) (string, error) {
	return "", ErrNotConfigured
}

func (Disabled) ChargeableCard(context.Context, string) (string, error) {
	return "", ErrNotConfigured
}

func (Disabled) ChargeOffSession(context.Context, OffSessionRequest) (Payment, error) {
	return Payment{}, ErrNotConfigured
}

func (Disabled) FindAutoTopUp(context.Context, string, string, time.Time) (*Payment, error) {
	return nil, ErrNotConfigured
}

func (Disabled) SetUpSubscription(context.Context, SubscriptionPriceRequest) (SubscriptionPrices, error) {
	return SubscriptionPrices{}, ErrNotConfigured
}

func (Disabled) CreateSubscriptionCheckout(context.Context, SubscriptionCheckoutRequest) (Checkout, error) {
	return Checkout{}, ErrNotConfigured
}

func (Disabled) SetBillingEmail(context.Context, string, string) error {
	return ErrNotConfigured
}

func (Disabled) IssueInvoice(context.Context, InvoiceRequest) (IssuedInvoice, error) {
	return IssuedInvoice{}, ErrNotConfigured
}

func (Disabled) PayInvoiceOutOfBand(context.Context, string, string) (IssuedInvoice, error) {
	return IssuedInvoice{}, ErrNotConfigured
}

func (Disabled) CreditInvoice(context.Context, CreditNoteRequest) (CreditNote, error) {
	return CreditNote{}, ErrNotConfigured
}

func (Disabled) InvoiceOfPayment(context.Context, string) (string, error) {
	return "", ErrNotConfigured
}
