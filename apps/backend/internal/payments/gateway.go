// Package payments puts money into wallets through Stripe (ADR 0040).
//
// # What Stripe does, and what it does not
//
// Stripe **funds the wallet**. A studio tops up through a Stripe Checkout page, manages its cards in
// Stripe's customer portal, and — when it turns automatic top-up on — is charged off-session when its
// balance runs low. Every successful payment becomes a `purchase` credit lot, which pays any arrears
// first (ADR 0039).
//
// Stripe does **not** meter or invoice usage: tracked players are priced and charged by the
// billing writer against the wallet (ADR 0039). And PlayerSense stores **no billing information**:
// no card, no payment method id, no address. It keeps one pointer — the Stripe customer id — and asks
// Stripe for everything else at the moment it is needed.
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
	MetaKind    = "playersense_kind"    // KindCheckout or KindAutoTopUp
	MetaKey     = "playersense_topup_key"
)

// The two ways money arrives.
const (
	KindCheckout  = "checkout"
	KindAutoTopUp = "auto_topup"
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

// Gateway is the whole of what PlayerSense asks Stripe.
//
// An interface so the rules around it — idempotency, claims, the webhook — are tested without a
// network, and one implementation, Stripe's own SDK.
type Gateway interface {
	CreateCustomer(ctx context.Context, in CustomerRequest) (string, error)
	CreateCheckout(ctx context.Context, in CheckoutRequest) (Checkout, error)
	CreatePortal(ctx context.Context, customer, returnURL string) (string, error)

	// ChargeableCard is the payment method an off-session charge would use: the customer's
	// default, or failing that the most recently saved card. Empty when there is none.
	ChargeableCard(ctx context.Context, customer string) (string, error)

	// ChargeOffSession charges without the customer present. A decline is a Payment with a
	// failure status, not an error: an error means the outcome is unknown.
	ChargeOffSession(ctx context.Context, in OffSessionRequest) (Payment, error)

	// FindAutoTopUp looks for the charge a key already made, for a claim whose response was
	// lost long enough ago that Stripe may have forgotten the key.
	FindAutoTopUp(ctx context.Context, customer, key string, since time.Time) (*Payment, error)
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
