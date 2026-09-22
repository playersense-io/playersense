package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v85"
)

// Stripe is the Gateway, through Stripe's own SDK.
//
// The SDK pins the API version (stripe.APIVersion — 2026-04-22.dahlia for v85.1.0), so the shapes
// below are the ones that version returns, and upgrading the module is upgrading the API.
type Stripe struct {
	client *stripe.Client
}

// NewStripe returns the gateway for a secret key.
func NewStripe(secretKey string) *Stripe {
	return &Stripe{client: stripe.NewClient(secretKey)}
}

// CreateCustomer opens a customer, keyed on the account so a retry or a race opens one.
func (s *Stripe) CreateCustomer(ctx context.Context, in CustomerRequest) (string, error) {
	params := &stripe.CustomerCreateParams{
		Name:     stripe.String(in.Name),
		Metadata: map[string]string{MetaAccount: in.AccountUID, "playersense_organization": in.OrgSlug},
	}
	if in.Email != "" {
		params.Email = stripe.String(in.Email)
	}
	params.SetIdempotencyKey("playersense-customer-" + in.AccountUID)
	customer, err := s.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("create the stripe customer: %w", err)
	}
	return customer.ID, nil
}

// CreateCheckout opens a hosted payment page for one top-up.
//
// setup_future_usage=off_session saves the card to the customer as it is used, which is what lets
// automatic top-up charge it later without anybody present. The same metadata goes on the session
// and on its PaymentIntent, so whichever of the two events arrives first can credit the wallet.
func (s *Stripe) CreateCheckout(ctx context.Context, in CheckoutRequest) (Checkout, error) {
	meta := map[string]string{MetaAccount: in.AccountUID, MetaKind: KindCheckout}
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		Customer:          stripe.String(in.Customer),
		ClientReferenceID: stripe.String(in.AccountUID),
		SuccessURL:        stripe.String(in.SuccessURL),
		CancelURL:         stripe.String(in.CancelURL),
		Metadata:          meta,
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(in.Currency),
				UnitAmount: stripe.Int64(in.AmountCents),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String("PlayerSense credit"),
				},
			},
		}},
		PaymentIntentData: &stripe.CheckoutSessionCreatePaymentIntentDataParams{
			SetupFutureUsage: stripe.String(string(stripe.PaymentIntentSetupFutureUsageOffSession)),
			Description:      stripe.String("PlayerSense credit"),
			Metadata:         meta,
		},
	}
	session, err := s.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return Checkout{}, fmt.Errorf("create the checkout session: %w", err)
	}
	return Checkout{ID: session.ID, URL: session.URL, ExpiresAt: time.Unix(session.ExpiresAt, 0).UTC()}, nil
}

// CreatePortal opens Stripe's customer portal: cards, receipts, billing details — all Stripe's.
func (s *Stripe) CreatePortal(ctx context.Context, customer, returnURL string) (string, error) {
	session, err := s.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customer),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", fmt.Errorf("create the portal session: %w", err)
	}
	return session.URL, nil
}

// ChargeableCard asks Stripe which card an off-session charge would use.
func (s *Stripe) ChargeableCard(ctx context.Context, customer string) (string, error) {
	c, err := s.client.V1Customers.Retrieve(ctx, customer, nil)
	if err != nil {
		return "", fmt.Errorf("read the stripe customer: %w", err)
	}
	if c.Deleted {
		return "", nil
	}
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil &&
		c.InvoiceSettings.DefaultPaymentMethod.ID != "" {
		return c.InvoiceSettings.DefaultPaymentMethod.ID, nil
	}
	// No default: a card saved by Checkout is attached but not made the default. The most
	// recently saved one is what the customer last chose to pay with.
	list := s.client.V1Customers.ListPaymentMethods(ctx, &stripe.CustomerListPaymentMethodsParams{
		Customer:   stripe.String(customer),
		Type:       stripe.String(string(stripe.PaymentMethodTypeCard)),
		ListParams: stripe.ListParams{Limit: stripe.Int64(1), Single: true},
	})
	for pm, err := range list.All(ctx) {
		if err != nil {
			return "", fmt.Errorf("list the customer's cards: %w", err)
		}
		return pm.ID, nil
	}
	return "", nil
}

// ChargeOffSession makes an automatic top-up.
//
// error_on_requires_action: a card that wants 3-D Secure cannot be authenticated by nobody, so the
// charge fails at once rather than sitting in requires_action waiting for a person who is not there.
//
// payment_method_types=[card] is what makes that allowed. Stripe refuses error_on_requires_action on
// a PaymentIntent whose methods come from the Dashboard's dynamic settings — found by the live suite,
// not by the docs — and an automatic top-up only ever charges a saved card anyway.
func (s *Stripe) ChargeOffSession(ctx context.Context, in OffSessionRequest) (Payment, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount:                stripe.Int64(in.AmountCents),
		Currency:              stripe.String(in.Currency),
		Customer:              stripe.String(in.Customer),
		PaymentMethod:         stripe.String(in.PaymentMethod),
		OffSession:            stripe.Bool(true),
		Confirm:               stripe.Bool(true),
		ErrorOnRequiresAction: stripe.Bool(true),
		PaymentMethodTypes:    []*string{stripe.String(string(stripe.PaymentMethodTypeCard))},
		Description:           stripe.String("PlayerSense automatic top-up"),
		Metadata: map[string]string{
			MetaAccount: in.AccountUID, MetaKind: KindAutoTopUp, MetaKey: in.Key,
		},
	}
	params.SetIdempotencyKey(in.Key)

	pi, err := s.client.V1PaymentIntents.Create(ctx, params)
	if err == nil {
		return paymentOf(pi), nil
	}
	// A decline is an answer, not a failure to get one. Stripe returns it as an error carrying
	// the PaymentIntent it declined.
	var stripeErr *stripe.Error
	if errors.As(err, &stripeErr) && stripeErr.Type == stripe.ErrorTypeCard {
		out := Payment{Status: "requires_payment_method", FailureMessage: stripeErr.Msg,
			Customer: in.Customer, Currency: in.Currency}
		if stripeErr.PaymentIntent != nil {
			out = paymentOf(stripeErr.PaymentIntent)
			out.FailureMessage = stripeErr.Msg
		}
		return out, nil
	}
	if errors.As(err, &stripeErr) && stripeErr.Type == stripe.ErrorTypeIdempotency {
		return Payment{}, fmt.Errorf("%w: %v", ErrKeyReused, err)
	}
	return Payment{}, fmt.Errorf("charge off-session: %w", err)
}

// FindAutoTopUp scans the customer's recent PaymentIntents for the one a key made.
func (s *Stripe) FindAutoTopUp(ctx context.Context, customer, key string, since time.Time) (*Payment, error) {
	params := &stripe.PaymentIntentListParams{
		Customer:     stripe.String(customer),
		CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: since.Unix()},
	}
	params.Limit = stripe.Int64(100)
	for pi, err := range s.client.V1PaymentIntents.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("list the customer's payments: %w", err)
		}
		if pi.Metadata[MetaKey] == key {
			found := paymentOf(pi)
			return &found, nil
		}
	}
	return nil, nil
}

func paymentOf(pi *stripe.PaymentIntent) Payment {
	out := Payment{
		ID:             pi.ID,
		Status:         string(pi.Status),
		Currency:       strings.ToLower(string(pi.Currency)),
		AmountReceived: pi.AmountReceived,
		Metadata:       pi.Metadata,
	}
	if pi.Customer != nil {
		out.Customer = pi.Customer.ID
	}
	if pi.LastPaymentError != nil {
		out.FailureMessage = pi.LastPaymentError.Msg
	}
	return out
}
