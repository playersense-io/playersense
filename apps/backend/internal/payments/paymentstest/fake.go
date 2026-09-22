// Package paymentstest is a Stripe that lives in memory, for the rules around it.
//
// It behaves like Stripe where the rules depend on it — an Idempotency-Key returns the payment it
// first made, and a key reused with different parameters is refused — and lets a test decide what
// Stripe would have answered: a decline, a timeout, a card or no card.
package paymentstest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/internal/payments"
)

// ErrTimeout is Stripe not answering.
var ErrTimeout = errors.New("stripe did not answer")

// Gateway is the fake.
type Gateway struct {
	mu sync.Mutex

	// Card is what ChargeableCard answers for every customer. Empty means no saved card.
	Card string

	// Outcome decides the next charge: "succeeded" (the default), "declined", "processing".
	Outcome string

	// LoseResponses makes the next n charges happen at "Stripe" and report ErrTimeout — the
	// response lost after the card was charged, which is the case idempotency exists for.
	LoseResponses int

	// Down makes every call fail before anything happens.
	Down bool

	// Only, when set, is the one customer this Stripe knows. Every other customer has no card —
	// so a test's pass over a shared database charges its own account and nobody else's.
	Only string

	Customers map[string]string           // account uid → customer id
	Charges   map[string]payments.Payment // idempotency key → the payment it made
	requests  map[string]payments.OffSessionRequest
	Checkouts []payments.CheckoutRequest
	Portals   int

	// Calls counts charge requests that reached "Stripe", including replays of a key.
	Calls int
}

// New returns a fake with a saved card and payments that succeed.
func New() *Gateway {
	return &Gateway{
		Card: "pm_card_visa", Customers: map[string]string{},
		Charges: map[string]payments.Payment{}, requests: map[string]payments.OffSessionRequest{},
	}
}

func (g *Gateway) CreateCustomer(_ context.Context, in payments.CustomerRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return "", ErrTimeout
	}
	if id, found := g.Customers[in.AccountUID]; found {
		return id, nil // idempotent on the account, as the real key is
	}
	id := "cus_test_" + uuid.NewString()[:12]
	g.Customers[in.AccountUID] = id
	return id, nil
}

func (g *Gateway) CreateCheckout(_ context.Context, in payments.CheckoutRequest) (payments.Checkout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.Checkout{}, ErrTimeout
	}
	g.Checkouts = append(g.Checkouts, in)
	id := "cs_test_" + uuid.NewString()[:12]
	return payments.Checkout{ID: id, URL: "https://checkout.stripe.test/" + id,
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC()}, nil
}

func (g *Gateway) CreatePortal(_ context.Context, customer, _ string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return "", ErrTimeout
	}
	g.Portals++
	return "https://billing.stripe.test/session/" + customer, nil
}

func (g *Gateway) ChargeableCard(_ context.Context, customer string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return "", ErrTimeout
	}
	if g.Only != "" && customer != g.Only {
		return "", nil
	}
	return g.Card, nil
}

// ChargeOffSession behaves as Stripe does with an Idempotency-Key: the first request with a key
// decides the outcome, and every later request with it returns that outcome unchanged.
func (g *Gateway) ChargeOffSession(_ context.Context, in payments.OffSessionRequest) (payments.Payment, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.Payment{}, ErrTimeout
	}
	g.Calls++
	if first, seen := g.requests[in.Key]; seen {
		if first.AmountCents != in.AmountCents || first.PaymentMethod != in.PaymentMethod {
			return payments.Payment{}, payments.ErrKeyReused
		}
		return g.Charges[in.Key], nil
	}
	g.requests[in.Key] = in
	// Unique across fakes, as Stripe's ids are across its whole system: a PaymentIntent id is a
	// lot's idempotency key, and two tests sharing one would share a lot.
	p := payments.Payment{
		ID: "pi_test_" + uuid.NewString()[:12], Customer: in.Customer, Currency: in.Currency,
		Metadata: map[string]string{
			payments.MetaAccount: in.AccountUID, payments.MetaKind: payments.KindAutoTopUp,
			payments.MetaKey: in.Key,
		},
	}
	switch g.Outcome {
	case "declined":
		p.Status, p.FailureMessage = "requires_payment_method", "Your card was declined."
	case "processing":
		p.Status = "processing"
	default:
		p.Status, p.AmountReceived = "succeeded", in.AmountCents
	}
	g.Charges[in.Key] = p
	if g.LoseResponses > 0 {
		g.LoseResponses--
		return payments.Payment{}, ErrTimeout
	}
	return p, nil
}

func (g *Gateway) FindAutoTopUp(_ context.Context, _ string, key string, _ time.Time) (*payments.Payment, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return nil, ErrTimeout
	}
	if p, found := g.Charges[key]; found {
		return &p, nil
	}
	return nil, nil
}

// Charged is how many distinct payments were made — the number a customer's card statement shows.
func (g *Gateway) Charged() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, p := range g.Charges {
		if p.Succeeded() {
			n++
		}
	}
	return n
}
