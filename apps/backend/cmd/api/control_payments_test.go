package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85/webhook"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/schemas"
)

// Paying for credit through HTTP (ADR 0040): a top-up is a redirect to Stripe, the portal is a
// redirect to Stripe, and only a signed webhook puts money in a wallet.

// signed is a Stripe delivery, signed with the suite's webhook secret.
func signed(t *testing.T, eventType string, object map[string]any) (body []byte, header string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id": "evt_" + uuid.NewString(), "object": "event", "type": eventType,
		"api_version": "2026-04-22.dahlia", "created": time.Now().Unix(),
		"data": map[string]any{"object": object},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: testWebhookSecret})
	return p.Payload, p.Header
}

func (w *money) account(t *testing.T) (uid, customer string) {
	t.Helper()
	org := w.organization(t, w.slug)
	row, err := w.db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(org.ID)).Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if row.StripeCustomerID != nil {
		customer = *row.StripeCustomerID
	}
	return row.UID.String(), customer
}

// A top-up hands back Stripe's page and moves no money: the browser coming back proves nothing, and
// the wallet waits for Stripe.
func TestATopUpSendsThePersonToStripeAndCreditsNothing(t *testing.T) {
	stripe := paymentstest.New()
	w := newWalletWith(t, stripe)
	client := w.client(t)

	first := testclient.Decoded[schemas.CheckoutOut](client.Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(100)})).AssertStatus(http.StatusOK))
	client.Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(25)})).AssertStatus(http.StatusOK)

	if first.URL == "" || first.ID == "" {
		t.Fatalf("checkout = %+v", first)
	}
	if len(stripe.Checkouts) != 2 || stripe.Checkouts[0].AmountCents != 10_000 {
		t.Fatalf("checkouts = %+v", stripe.Checkouts)
	}
	// One Stripe customer for the account however many top-ups open, stored on the account.
	uid, customer := w.account(t)
	if len(stripe.Customers) != 1 || customer == "" || stripe.Customers[uid] != customer {
		t.Errorf("customers = %v, stored %q", stripe.Customers, customer)
	}
	if got := w.read(t); got.AvailableMicros != 0 {
		t.Errorf("opening Checkout credited %d", got.AvailableMicros)
	}
}

func TestATopUpIsBoundedAndInWholeCents(t *testing.T) {
	w := newWallet(t)
	client := w.client(t)
	for name, amount := range map[string]int64{
		"below $5":       dollars(5) - 10_000,
		"above $50,000":  dollars(50_000) + 10_000,
		"half a cent":    dollars(10) + 5_000,
		"nothing at all": 0,
	} {
		res := client.Post(w.base()+"/billing/top-ups", testclient.JSON(map[string]any{"amount_micros": amount}))
		if res.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", name, res.Status)
		}
	}
}

// Paying is billing.manage; a developer can see the wallet and cannot spend a card on it.
func TestOnlyBillingManagersCanPay(t *testing.T) {
	w := newWallet(t)
	dev := person(t)
	w.join(t, w.slug, dev, authz.RoleDeveloper, true)
	w.as(t, dev).Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(10)})).AssertStatus(http.StatusForbidden)
	w.as(t, dev).Post(w.base()+"/billing/portal").AssertStatus(http.StatusForbidden)
}

// The portal opens before any top-up, so a card can be added first.
func TestThePortalOpensForANewAccount(t *testing.T) {
	stripe := paymentstest.New()
	w := newWalletWith(t, stripe)
	out := testclient.Decoded[schemas.PortalOut](
		w.client(t).Post(w.base()+"/billing/portal").AssertStatus(http.StatusOK))
	if out.URL == "" || stripe.Portals != 1 {
		t.Errorf("portal = %+v, opened %d", out, stripe.Portals)
	}
	if _, customer := w.account(t); customer == "" {
		t.Error("the portal opened without a customer being stored")
	}
}

// The whole path: pay at Stripe, Stripe reports it, the wallet has the money — once.
func TestASignedPaymentReachesTheWalletOnce(t *testing.T) {
	w := newWalletWith(t, paymentstest.New())
	client := w.client(t)
	client.Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(40)})).AssertStatus(http.StatusOK)
	uid, customer := w.account(t)

	body, header := signed(t, "payment_intent.succeeded", map[string]any{
		"id": "pi_" + uuid.NewString()[:12], "object": "payment_intent", "status": "succeeded",
		"currency": "usd", "amount": 4_000, "amount_received": 4_000, "customer": customer,
		"metadata": map[string]string{payments.MetaAccount: uid, payments.MetaKind: payments.KindCheckout},
	})
	stripeClient := testclient.New(t, w.app)
	for range 2 { // Stripe delivers at least once
		stripeClient.Post("/v1/stripe/webhook", testclient.Body("application/json", bytes.NewReader(body)),
			testclient.Header("Stripe-Signature", header)).AssertStatus(http.StatusOK)
	}

	got := w.read(t)
	if got.AvailableMicros != dollars(40) || got.PurchasedMicros != dollars(40) {
		t.Errorf("available %d, purchased %d; want $40 once", got.AvailableMicros, got.PurchasedMicros)
	}
}

// Anything Stripe did not sign is refused before it is read.
func TestAnUnsignedWebhookIsRefused(t *testing.T) {
	w := newWallet(t)
	body, _ := signed(t, "payment_intent.succeeded", map[string]any{"id": "pi_x"})
	testclient.New(t, w.app).Post("/v1/stripe/webhook", testclient.Body("application/json", bytes.NewReader(body)),
		testclient.Header("Stripe-Signature", "t=1,v1=forged")).AssertStatus(http.StatusBadRequest)
}

// Without a Stripe key the payment routes say so, rather than failing in some other way.
func TestWithoutStripeThePaymentRoutesSaySo(t *testing.T) {
	w := newWalletWith(t, nil)
	w.client(t).Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(10)})).AssertStatus(http.StatusServiceUnavailable)
}
