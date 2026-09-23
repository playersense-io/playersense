package payments_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/testsupport"
)

// The gateway against Stripe itself, in test mode. Opt-in — STRIPE_LIVE=1 — because it needs the
// network and a test-mode key, and creates test customers in that account.
//
// The fakes prove the rules; this proves the wiring: that the SDK pinned to 2026-04-22.dahlia sends
// what Stripe accepts, that Stripe's idempotency behaves as the rules assume, and that a decline
// comes back as an answer rather than an error.
func liveGateway(t *testing.T) (*payments.Stripe, *stripe.Client) {
	t.Helper()
	if os.Getenv("STRIPE_LIVE") != "1" {
		t.Skip("set STRIPE_LIVE=1 to run against Stripe's test mode")
	}
	key := testsupport.Settings(t).StripeSecretKey
	if !strings.HasPrefix(key, "sk_test_") {
		t.Fatal("STRIPE_LIVE needs a test-mode key (sk_test_…); refusing to run against a live one")
	}
	return payments.NewStripe(key), stripe.NewClient(key)
}

// savedCard attaches one of Stripe's test cards and makes it the default, as the portal would.
func savedCard(t *testing.T, sc *stripe.Client, customer, testCard string) string {
	t.Helper()
	ctx := context.Background()
	pm, err := sc.V1PaymentMethods.Attach(ctx, testCard, &stripe.PaymentMethodAttachParams{
		Customer: stripe.String(customer),
	})
	if err != nil {
		t.Fatalf("attach %s: %v", testCard, err)
	}
	if _, err := sc.V1Customers.Update(ctx, customer, &stripe.CustomerUpdateParams{
		InvoiceSettings: &stripe.CustomerUpdateInvoiceSettingsParams{DefaultPaymentMethod: stripe.String(pm.ID)},
	}); err != nil {
		t.Fatalf("make it the default: %v", err)
	}
	return pm.ID
}

func TestLiveStripe(t *testing.T) {
	gw, sc := liveGateway(t)
	ctx := context.Background()
	account := uuid.NewString()

	// One customer per account, however often it is asked for.
	first, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: account, Name: "Live test studio", OrgSlug: "live-test"})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	again, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: account, Name: "Live test studio", OrgSlug: "live-test"})
	if err != nil || again != first {
		t.Fatalf("a second request opened %q (%v), want %q", again, err, first)
	}

	checkout, err := gw.CreateCheckout(ctx, payments.CheckoutRequest{
		AccountUID: account, Customer: first, AmountCents: 2_500, Currency: "usd",
		SuccessURL: "http://localhost:3000/billing/wallet?checkout=success",
		CancelURL:  "http://localhost:3000/billing/wallet?checkout=cancel",
	})
	if err != nil || !strings.HasPrefix(checkout.URL, "https://checkout.stripe.com/") {
		t.Fatalf("checkout = %+v, %v", checkout, err)
	}
	t.Logf("checkout page: %s", checkout.URL)

	if url, err := gw.CreatePortal(ctx, first, "http://localhost:3000/billing/wallet"); err != nil {
		t.Errorf("portal: %v — the test-mode portal needs its default configuration saved once at "+
			"https://dashboard.stripe.com/test/settings/billing/portal", err)
	} else {
		t.Logf("portal: %s", url)
	}

	// No card yet, then a card.
	if card, err := gw.ChargeableCard(ctx, first); err != nil || card != "" {
		t.Fatalf("a new customer has card %q (%v)", card, err)
	}
	card := savedCard(t, sc, first, "pm_card_visa")
	if got, err := gw.ChargeableCard(ctx, first); err != nil || got != card {
		t.Fatalf("chargeable card = %q (%v), want %q", got, err, card)
	}

	// The same key twice is one payment: this is the property the whole auto top-up rests on.
	key := "playersense-live-test-" + uuid.NewString()
	request := payments.OffSessionRequest{AccountUID: account, Customer: first, PaymentMethod: card,
		AmountCents: 5_000, Currency: "usd", Key: key}
	one, err := gw.ChargeOffSession(ctx, request)
	if err != nil || !one.Succeeded() || one.AmountReceived != 5_000 {
		t.Fatalf("charge = %+v, %v", one, err)
	}
	two, err := gw.ChargeOffSession(ctx, request)
	if err != nil || two.ID != one.ID {
		t.Fatalf("the same key made %q (%v), want %q again", two.ID, err, one.ID)
	}
	if one.Metadata[payments.MetaKey] != key || one.Metadata[payments.MetaAccount] != account {
		t.Errorf("metadata = %v", one.Metadata)
	}

	// One invoice for the charge and its replay, and it is paid by that charge. Stripe credits an
	// attached payment to its invoice a few seconds after the payment succeeds, so this waits.
	invoices := invoicesOf(t, sc, first)
	for deadline := time.Now().Add(30 * time.Second); len(invoices) == 1 &&
		invoices[0].Status != stripe.InvoiceStatusPaid && time.Now().Before(deadline); {
		time.Sleep(2 * time.Second)
		invoices = invoicesOf(t, sc, first)
	}
	if len(invoices) != 1 || invoices[0].Status != stripe.InvoiceStatusPaid || invoices[0].AmountPaid != 5_000 {
		for _, inv := range invoices {
			t.Logf("invoice %s: %s, paid %d", inv.ID, inv.Status, inv.AmountPaid)
		}
		t.Fatalf("%d invoices for one automatic top-up; want one, paid, for $50", len(invoices))
	}
	t.Logf("invoice %s paid: %s", invoices[0].ID, invoices[0].HostedInvoiceURL)

	// The same key with a different amount is refused, not charged.
	changed := request
	changed.AmountCents = 6_000
	if _, err := gw.ChargeOffSession(ctx, changed); !errors.Is(err, payments.ErrKeyReused) {
		t.Errorf("a reused key with another amount: %v, want ErrKeyReused", err)
	}

	// A charge can be found by its key after the fact.
	time.Sleep(2 * time.Second) // listing is eventually consistent
	found, err := gw.FindAutoTopUp(ctx, first, key, time.Now().Add(-time.Hour))
	if err != nil || found == nil || found.ID != one.ID {
		t.Errorf("find = %+v, %v; want %s", found, err, one.ID)
	}

	// A decline is an answer, with Stripe's reason, not an error.
	declining, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: uuid.NewString(), Name: "Declining studio"})
	if err != nil {
		t.Fatal(err)
	}
	bad := savedCard(t, sc, declining, "pm_card_chargeCustomerFail")
	declined, err := gw.ChargeOffSession(ctx, payments.OffSessionRequest{AccountUID: account, Customer: declining,
		PaymentMethod: bad, AmountCents: 5_000, Currency: "usd", Key: "playersense-live-test-" + uuid.NewString()})
	if err != nil || declined.Succeeded() || declined.FailureMessage == "" {
		t.Errorf("declined = %+v, %v; want a failure with a reason", declined, err)
	}
	t.Logf("declined with: %q (%s)", declined.FailureMessage, declined.Status)

	// The declined top-up's invoice is voided, not left open for the customer to wonder about.
	voided := invoicesOf(t, sc, declining)
	if len(voided) != 1 || voided[0].Status != stripe.InvoiceStatusVoid {
		for _, inv := range voided {
			t.Logf("invoice %s: %s", inv.ID, inv.Status)
		}
		t.Errorf("a declined top-up left %d invoices; want one, void", len(voided))
	}
}

func invoicesOf(t *testing.T, sc *stripe.Client, customer string) []*stripe.Invoice {
	t.Helper()
	var out []*stripe.Invoice
	for inv, err := range sc.V1Invoices.List(context.Background(), &stripe.InvoiceListParams{
		Customer: stripe.String(customer),
	}).All(context.Background()) {
		if err != nil {
			t.Fatalf("list invoices: %v", err)
		}
		out = append(out, inv)
	}
	return out
}
