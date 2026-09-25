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
	Setups    []payments.SetupCheckoutRequest
	Detached  []string

	// Details is what DescribeCustomer answers, by customer. Absent: a customer with the
	// fake's Card, if any, and no details.
	Details       map[string]payments.CustomerDetails
	Subscriptions []payments.SubscriptionCheckoutRequest
	Portals       int

	// Calls counts charge requests that reached "Stripe", including replays of a key.
	Calls int

	// Invoices are the usage invoices issued, by uid; IssueCalls counts requests that reached
	// "Stripe", including resumptions. LoseInvoiceResponses makes the next n issues happen and
	// report ErrTimeout.
	Invoices             map[string]payments.IssuedInvoice
	IssueRequests        []payments.InvoiceRequest
	IssueCalls           int
	LoseInvoiceResponses int
	CreditNotes          []payments.CreditNoteRequest
	PaidOutOfBand        []string
	BillingEmails        map[string]string // customer → address

	// PaidInvoices is which invoice a PaymentIntent paid, as Stripe's invoice payments list says.
	PaidInvoices map[string]string
}

// New returns a fake with a saved card and payments that succeed.
func New() *Gateway {
	return &Gateway{
		Card: "pm_card_visa", Customers: map[string]string{},
		Charges: map[string]payments.Payment{}, requests: map[string]payments.OffSessionRequest{},
		Invoices: map[string]payments.IssuedInvoice{}, BillingEmails: map[string]string{},
		PaidInvoices: map[string]string{}, Details: map[string]payments.CustomerDetails{},
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

func (g *Gateway) DescribeCustomer(_ context.Context, customer string) (payments.CustomerDetails, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.CustomerDetails{}, ErrTimeout
	}
	if d, found := g.Details[customer]; found {
		return d, nil
	}
	if g.Card == "" || (g.Only != "" && customer != g.Only) {
		return payments.CustomerDetails{}, nil
	}
	return payments.CustomerDetails{DefaultCard: g.Card, Cards: []payments.Card{{
		ID: g.Card, Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030, Funding: "credit", Country: "US",
	}}}, nil
}

func (g *Gateway) CreateSetupCheckout(_ context.Context, in payments.SetupCheckoutRequest) (payments.Checkout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.Checkout{}, ErrTimeout
	}
	g.Setups = append(g.Setups, in)
	id := "cs_test_" + uuid.NewString()[:12]
	return payments.Checkout{ID: id, URL: "https://checkout.stripe.test/" + id,
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC()}, nil
}

// SetDefaultCard makes a card the customer's default in the details this fake describes.
func (g *Gateway) SetDefaultCard(_ context.Context, customer, card string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return ErrTimeout
	}
	if g.Details == nil {
		g.Details = map[string]payments.CustomerDetails{}
	}
	d := g.Details[customer]
	d.DefaultCard = card
	g.Details[customer] = d
	return nil
}

// DetachCard removes a card from whichever customer this fake describes it for.
func (g *Gateway) DetachCard(_ context.Context, card string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return ErrTimeout
	}
	for customer, d := range g.Details {
		kept := d.Cards[:0]
		for _, c := range d.Cards {
			if c.ID != card {
				kept = append(kept, c)
			}
		}
		d.Cards = kept
		if d.DefaultCard == card {
			d.DefaultCard = ""
		}
		g.Details[customer] = d
	}
	g.Detached = append(g.Detached, card)
	return nil
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

func (g *Gateway) SetUpSubscription(_ context.Context, in payments.SubscriptionPriceRequest) (payments.SubscriptionPrices, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.SubscriptionPrices{}, ErrTimeout
	}
	out := payments.SubscriptionPrices{Product: in.Product}
	if out.Product == "" {
		out.Product = "prod_test_" + in.PlanUID[:8]
	}
	if in.MonthlyCents > 0 {
		out.Monthly = "price_test_" + uuid.NewString()[:10]
	}
	if in.YearlyCents > 0 {
		out.Yearly = "price_test_" + uuid.NewString()[:10]
	}
	return out, nil
}

func (g *Gateway) CreateSubscriptionCheckout(_ context.Context, in payments.SubscriptionCheckoutRequest) (payments.Checkout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.Checkout{}, ErrTimeout
	}
	g.Subscriptions = append(g.Subscriptions, in)
	id := "cs_test_" + uuid.NewString()[:12]
	return payments.Checkout{ID: id, URL: "https://checkout.stripe.test/" + id, ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}

// IssueInvoice behaves as the real one's search does: an invoice already issued for a uid is returned,
// however many times it is asked for, so a resumed claim never makes a second one.
func (g *Gateway) IssueInvoice(_ context.Context, in payments.InvoiceRequest) (payments.IssuedInvoice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.IssuedInvoice{}, ErrTimeout
	}
	g.IssueCalls++
	g.IssueRequests = append(g.IssueRequests, in)
	issued, found := g.Invoices[in.InvoiceUID]
	if !found {
		var total int64
		for _, line := range in.Lines {
			total += line.AmountCents
		}
		id := "in_test_" + uuid.NewString()[:12]
		issued = payments.IssuedInvoice{
			ID: id, Number: "PS-" + id[8:16], Status: "open", Customer: in.Customer, Currency: in.Currency,
			HostedURL: "https://invoice.stripe.test/" + id, PDFURL: "https://invoice.stripe.test/" + id + ".pdf",
			DueAt: time.Now().UTC().Add(time.Duration(in.DaysUntilDue) * 24 * time.Hour),
			Total: total, AmountDue: total,
			Metadata: map[string]string{
				payments.MetaAccount: in.AccountUID, payments.MetaKind: payments.KindUsageInvoice,
				payments.MetaInvoice: in.InvoiceUID,
			},
		}
		g.Invoices[in.InvoiceUID] = issued
	}
	if g.LoseInvoiceResponses > 0 {
		g.LoseInvoiceResponses--
		return payments.IssuedInvoice{}, ErrTimeout
	}
	return issued, nil
}

// Issued is how many distinct invoices were issued — the number the customer's inbox shows.
func (g *Gateway) Issued() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Invoices)
}

func (g *Gateway) PayInvoiceOutOfBand(_ context.Context, invoice, _ string) (payments.IssuedInvoice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.IssuedInvoice{}, ErrTimeout
	}
	for uid, issued := range g.Invoices {
		if issued.ID == invoice {
			issued.Status, issued.AmountPaid = "paid", issued.AmountDue
			g.Invoices[uid] = issued
			g.PaidOutOfBand = append(g.PaidOutOfBand, invoice)
			return issued, nil
		}
	}
	return payments.IssuedInvoice{}, errors.New("no such invoice")
}

func (g *Gateway) CreditInvoice(_ context.Context, in payments.CreditNoteRequest) (payments.CreditNote, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return payments.CreditNote{}, ErrTimeout
	}
	g.CreditNotes = append(g.CreditNotes, in)
	if issued, found := g.Invoices[in.InvoiceUID]; found {
		issued.AmountDue -= in.AmountCents
		g.Invoices[in.InvoiceUID] = issued
	}
	return payments.CreditNote{ID: "cn_test_" + uuid.NewString()[:12], AmountCents: in.AmountCents}, nil
}

func (g *Gateway) SetBillingEmail(_ context.Context, customer, email string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return ErrTimeout
	}
	g.BillingEmails[customer] = email
	return nil
}

func (g *Gateway) InvoiceOfPayment(_ context.Context, paymentIntent string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Down {
		return "", ErrTimeout
	}
	return g.PaidInvoices[paymentIntent], nil
}
