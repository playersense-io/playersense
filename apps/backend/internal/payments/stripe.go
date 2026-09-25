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
		// A paid invoice for every top-up (ADR 0040): Stripe issues it after the payment, emails
		// it, and lists it in the customer portal. It describes the payment; it is never a second
		// report of it — credit comes from the PaymentIntent, once.
		InvoiceCreation: &stripe.CheckoutSessionCreateInvoiceCreationParams{
			Enabled: stripe.Bool(true),
			InvoiceData: &stripe.CheckoutSessionCreateInvoiceCreationInvoiceDataParams{
				Description: stripe.String("PlayerSense credit"),
				Metadata:    meta,
			},
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

// DescribeCustomer reads a customer's invoice details, tax ids and saved cards: three requests,
// made the first time the billing page asks and on a refresh, never per request.
func (s *Stripe) DescribeCustomer(ctx context.Context, customer string) (CustomerDetails, error) {
	c, err := s.client.V1Customers.Retrieve(ctx, customer, nil)
	if err != nil {
		return CustomerDetails{}, fmt.Errorf("read the stripe customer: %w", err)
	}
	out := DetailsOf(c)
	for id, err := range s.client.V1TaxIDs.List(ctx, &stripe.TaxIDListParams{
		Customer: stripe.String(customer),
	}).All(ctx) {
		if err != nil {
			return out, fmt.Errorf("list the customer's tax ids: %w", err)
		}
		out.TaxIDs = append(out.TaxIDs, string(id.Type)+" "+id.Value)
	}
	for pm, err := range s.client.V1Customers.ListPaymentMethods(ctx, &stripe.CustomerListPaymentMethodsParams{
		Customer: stripe.String(customer),
		Type:     stripe.String(string(stripe.PaymentMethodTypeCard)),
	}).All(ctx) {
		if err != nil {
			return out, fmt.Errorf("list the customer's cards: %w", err)
		}
		if card, ok := CardOf(pm); ok {
			out.Cards = append(out.Cards, card)
		}
	}
	return out, nil
}

// DetailsOf is a Stripe customer's invoice details, as an invoice prints them.
func DetailsOf(c *stripe.Customer) CustomerDetails {
	out := CustomerDetails{Name: c.Name, Email: c.Email}
	if c.Address != nil {
		for _, line := range []string{c.Address.Line1, c.Address.Line2,
			strings.TrimSpace(c.Address.PostalCode + " " + c.Address.City), c.Address.State} {
			if strings.TrimSpace(line) != "" {
				out.Address = append(out.Address, strings.TrimSpace(line))
			}
		}
		out.Country = c.Address.Country
	}
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil {
		out.DefaultCard = c.InvoiceSettings.DefaultPaymentMethod.ID
	}
	return out
}

// CardOf is a payment method's description, when it is a card.
func CardOf(pm *stripe.PaymentMethod) (Card, bool) {
	if pm == nil || pm.Card == nil {
		return Card{}, false
	}
	return Card{ID: pm.ID, Brand: string(pm.Card.Brand), Last4: pm.Card.Last4,
		ExpMonth: int(pm.Card.ExpMonth), ExpYear: int(pm.Card.ExpYear),
		Funding: string(pm.Card.Funding), Country: pm.Card.Country}, true
}

// CreateSetupCheckout opens Checkout in setup mode: the card is saved for off-session use and
// nothing is charged. It arrives as payment_method.attached.
func (s *Stripe) CreateSetupCheckout(ctx context.Context, in SetupCheckoutRequest) (Checkout, error) {
	session, err := s.client.V1CheckoutSessions.Create(ctx, &stripe.CheckoutSessionCreateParams{
		Mode:               stripe.String(string(stripe.CheckoutSessionModeSetup)),
		Customer:           stripe.String(in.Customer),
		Currency:           stripe.String(in.Currency),
		ClientReferenceID:  stripe.String(in.AccountUID),
		SuccessURL:         stripe.String(in.SuccessURL),
		CancelURL:          stripe.String(in.CancelURL),
		PaymentMethodTypes: []*string{stripe.String("card")},
		Metadata:           map[string]string{MetaAccount: in.AccountUID, MetaKind: "card_setup"},
		SetupIntentData: &stripe.CheckoutSessionCreateSetupIntentDataParams{
			Metadata: map[string]string{MetaAccount: in.AccountUID, MetaKind: "card_setup"},
		},
	})
	if err != nil {
		return Checkout{}, fmt.Errorf("create the setup session: %w", err)
	}
	return Checkout{ID: session.ID, URL: session.URL, ExpiresAt: time.Unix(session.ExpiresAt, 0).UTC()}, nil
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

// ChargeOffSession makes an automatic top-up, as a paid invoice.
//
// Three steps, so the invoice exists before any money moves and the payment is its payment:
//
//  1. the PaymentIntent, created **unconfirmed** — nothing is charged yet;
//  2. the invoice: created, given its one line, finalized, and the PaymentIntent attached to it;
//  3. the PaymentIntent confirmed off-session — the charge. When it succeeds the invoice is paid.
//
// Every step carries its own Idempotency-Key derived from the claim's key, so a retried claim replays
// the same PaymentIntent, the same invoice and the same confirmation rather than making new ones: the
// "one charge per claim" property the claim exists for holds for the invoice too. A PaymentIntent
// already succeeded or processing — the answer to an earlier attempt that was lost — is returned as
// it is. A decline voids the invoice, so the customer is not left with an unpaid one; the next attempt
// is a new claim with a new invoice.
//
// The card is named only at confirmation, so the steps before it do not change when a customer
// changes cards between attempts. payment_method_types=[card] with error_on_requires_action: a card
// that wants 3-D Secure fails at once rather than waiting for a person who is not there — and Stripe
// refuses error_on_requires_action with the Dashboard's dynamic payment methods, which the live suite
// found and the docs do not say.
func (s *Stripe) ChargeOffSession(ctx context.Context, in OffSessionRequest) (Payment, error) {
	meta := map[string]string{MetaAccount: in.AccountUID, MetaKind: KindAutoTopUp, MetaKey: in.Key}

	create := &stripe.PaymentIntentCreateParams{
		Amount:             stripe.Int64(in.AmountCents),
		Currency:           stripe.String(in.Currency),
		Customer:           stripe.String(in.Customer),
		PaymentMethodTypes: []*string{stripe.String(string(stripe.PaymentMethodTypeCard))},
		Description:        stripe.String("PlayerSense automatic top-up"),
		Metadata:           meta,
	}
	create.SetIdempotencyKey(in.Key + ":payment")
	pi, err := s.client.V1PaymentIntents.Create(ctx, create)
	if err != nil {
		return Payment{}, stripeFailure("create the payment", err)
	}
	switch pi.Status {
	case stripe.PaymentIntentStatusSucceeded, stripe.PaymentIntentStatusProcessing:
		return paymentOf(pi), nil // charged by an earlier attempt whose answer was lost
	}

	invoice, err := s.invoiceFor(ctx, in, meta, pi.ID)
	if err != nil {
		return Payment{}, err
	}

	confirm := &stripe.PaymentIntentConfirmParams{
		PaymentMethod:         stripe.String(in.PaymentMethod),
		OffSession:            stripe.Bool(true),
		ErrorOnRequiresAction: stripe.Bool(true),
	}
	confirm.SetIdempotencyKey(in.Key + ":confirm")
	confirmed, err := s.client.V1PaymentIntents.Confirm(ctx, pi.ID, confirm)
	if err == nil {
		return paymentOf(confirmed), nil
	}
	// A decline is an answer, not a failure to get one. Stripe returns it as an error carrying
	// the PaymentIntent it declined.
	var stripeErr *stripe.Error
	if errors.As(err, &stripeErr) && stripeErr.Type == stripe.ErrorTypeCard {
		s.void(ctx, invoice, pi.ID, in.Key)
		out := Payment{ID: pi.ID, Status: "requires_payment_method", FailureMessage: stripeErr.Msg,
			Customer: in.Customer, Currency: in.Currency, Metadata: meta}
		if stripeErr.PaymentIntent != nil {
			out = paymentOf(stripeErr.PaymentIntent)
			out.FailureMessage = stripeErr.Msg
		}
		return out, nil
	}
	return Payment{}, stripeFailure("confirm the payment", err)
}

// invoiceFor is the finalized invoice the automatic top-up's payment pays.
func (s *Stripe) invoiceFor(ctx context.Context, in OffSessionRequest, meta map[string]string, pi string) (string, error) {
	create := &stripe.InvoiceCreateParams{
		Customer:                    stripe.String(in.Customer),
		Currency:                    stripe.String(in.Currency),
		CollectionMethod:            stripe.String(string(stripe.InvoiceCollectionMethodChargeAutomatically)),
		AutoAdvance:                 stripe.Bool(false), // the charge is ours to make, not Stripe's schedule's
		PendingInvoiceItemsBehavior: stripe.String("exclude"),
		Description:                 stripe.String("PlayerSense automatic top-up"),
		Metadata:                    meta,
	}
	create.SetIdempotencyKey(in.Key + ":invoice")
	invoice, err := s.client.V1Invoices.Create(ctx, create)
	if err != nil {
		return "", stripeFailure("create the invoice", err)
	}

	item := &stripe.InvoiceItemCreateParams{
		Customer:    stripe.String(in.Customer),
		Invoice:     stripe.String(invoice.ID),
		Amount:      stripe.Int64(in.AmountCents),
		Currency:    stripe.String(in.Currency),
		Description: stripe.String("PlayerSense credit (automatic top-up)"),
		Metadata:    meta,
	}
	item.SetIdempotencyKey(in.Key + ":item")
	if _, err := s.client.V1InvoiceItems.Create(ctx, item); err != nil {
		return "", stripeFailure("add the invoice line", err)
	}

	finalize := &stripe.InvoiceFinalizeInvoiceParams{AutoAdvance: stripe.Bool(false)}
	finalize.SetIdempotencyKey(in.Key + ":finalize")
	if _, err := s.client.V1Invoices.FinalizeInvoice(ctx, invoice.ID, finalize); err != nil {
		return "", stripeFailure("finalize the invoice", err)
	}

	attach := &stripe.InvoiceAttachPaymentParams{PaymentIntent: stripe.String(pi)}
	attach.SetIdempotencyKey(in.Key + ":attach")
	if _, err := s.client.V1Invoices.AttachPayment(ctx, invoice.ID, attach); err != nil {
		return "", stripeFailure("attach the payment to the invoice", err)
	}
	return invoice.ID, nil
}

// void cancels the invoice of a declined top-up.
//
// Stripe will not void an invoice while a payment on it is still open, and a declined PaymentIntent is
// open — waiting for another card. So the declined payment is canceled first, then the invoice voided;
// voiding also closes the payment Stripe itself put on the invoice when it was finalized, which cannot
// be canceled directly. Both steps found by the live suite.
//
// Best effort: an invoice left open is untidy, not wrong — it is never charged, because auto_advance
// is off and its payment is ours to confirm.
func (s *Stripe) void(ctx context.Context, invoice, pi, key string) {
	cancel := &stripe.PaymentIntentCancelParams{}
	cancel.SetIdempotencyKey(key + ":cancel")
	_, _ = s.client.V1PaymentIntents.Cancel(ctx, pi, cancel)

	params := &stripe.InvoiceVoidInvoiceParams{}
	params.SetIdempotencyKey(key + ":void")
	_, _ = s.client.V1Invoices.VoidInvoice(ctx, invoice, params)
}

// stripeFailure classifies an error that is not a decline: a reused key is a question for
// FindAutoTopUp, anything else is an unknown outcome and the claim is kept.
func stripeFailure(step string, err error) error {
	var stripeErr *stripe.Error
	if errors.As(err, &stripeErr) && stripeErr.Type == stripe.ErrorTypeIdempotency {
		return fmt.Errorf("%w: %s: %v", ErrKeyReused, step, err)
	}
	return fmt.Errorf("%s: %w", step, err)
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

// SetUpSubscription gives a plan its Stripe Product and Prices (ADR 0042).
//
// The Product once, keyed on the plan. A Price per interval, keyed on the plan, the interval and the
// amount — so pressing "save" twice makes one Price, and changing the amount makes a new one, because a
// Stripe Price cannot be edited. Existing subscribers keep the Price they subscribed at until they
// change plan; new subscribers get the new one.
func (s *Stripe) SetUpSubscription(ctx context.Context, in SubscriptionPriceRequest) (SubscriptionPrices, error) {
	out := SubscriptionPrices{Product: in.Product}
	meta := map[string]string{MetaPlan: in.PlanKey, "playersense_plan_uid": in.PlanUID}
	if out.Product == "" {
		params := &stripe.ProductCreateParams{
			Name:        stripe.String("PlayerSense " + in.Name),
			Description: stripe.String("PlayerSense " + in.Name + " subscription"),
			Metadata:    meta,
		}
		params.SetIdempotencyKey("playersense-product-" + in.PlanUID)
		product, err := s.client.V1Products.Create(ctx, params)
		if err != nil {
			return out, fmt.Errorf("create the stripe product: %w", err)
		}
		out.Product = product.ID
	}
	price := func(interval string, cents int64) (string, error) {
		if cents <= 0 {
			return "", nil
		}
		params := &stripe.PriceCreateParams{
			Product:    stripe.String(out.Product),
			Currency:   stripe.String(in.Currency),
			UnitAmount: stripe.Int64(cents),
			Recurring:  &stripe.PriceCreateRecurringParams{Interval: stripe.String(interval)},
			Nickname:   stripe.String(in.Name + " (" + interval + "ly)"),
			Metadata:   meta,
		}
		params.SetIdempotencyKey(fmt.Sprintf("playersense-price-%s-%s-%d", in.PlanUID, interval, cents))
		p, err := s.client.V1Prices.Create(ctx, params)
		if err != nil {
			return "", fmt.Errorf("create the %sly price: %w", interval, err)
		}
		return p.ID, nil
	}
	var err error
	if out.Monthly, err = price("month", in.MonthlyCents); err != nil {
		return out, err
	}
	if out.Yearly, err = price("year", in.YearlyCents); err != nil {
		return out, err
	}
	return out, nil
}

// CreateSubscriptionCheckout opens Stripe's page to subscribe. The subscription carries the account
// and the plan in its metadata, so its webhooks say whose it is without trusting anything else; its
// invoices are Stripe's own, and its card is saved to the customer by Stripe.
func (s *Stripe) CreateSubscriptionCheckout(ctx context.Context, in SubscriptionCheckoutRequest) (Checkout, error) {
	meta := map[string]string{MetaAccount: in.AccountUID, MetaPlan: in.PlanKey}
	session, err := s.client.V1CheckoutSessions.Create(ctx, &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:          stripe.String(in.Customer),
		ClientReferenceID: stripe.String(in.AccountUID),
		SuccessURL:        stripe.String(in.SuccessURL),
		CancelURL:         stripe.String(in.CancelURL),
		Metadata:          meta,
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Price: stripe.String(in.Price), Quantity: stripe.Int64(1),
		}},
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: meta},
	})
	if err != nil {
		return Checkout{}, fmt.Errorf("create the subscription checkout: %w", err)
	}
	return Checkout{ID: session.ID, URL: session.URL, ExpiresAt: time.Unix(session.ExpiresAt, 0).UTC()}, nil
}
