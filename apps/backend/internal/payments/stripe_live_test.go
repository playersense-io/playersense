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

// A plan's subscription prices, and a subscription checkout, at Stripe itself.
func TestLiveSubscriptionSetUp(t *testing.T) {
	gw, _ := liveGateway(t)
	ctx := context.Background()
	plan := uuid.NewString()
	in := payments.SubscriptionPriceRequest{PlanUID: plan, PlanKey: "enterprise-live-test", Name: "Enterprise (live test)",
		Currency: "usd", MonthlyCents: 9_900, YearlyCents: 99_900}

	first, err := gw.SetUpSubscription(ctx, in)
	if err != nil || first.Product == "" || first.Monthly == "" || first.Yearly == "" {
		t.Fatalf("set up = %+v, %v", first, err)
	}
	in.Product = first.Product
	again, err := gw.SetUpSubscription(ctx, in)
	if err != nil || again.Monthly != first.Monthly || again.Yearly != first.Yearly {
		t.Errorf("saving the same prices again made new ones: %+v vs %+v (%v)", again, first, err)
	}
	in.MonthlyCents = 10_900
	changed, err := gw.SetUpSubscription(ctx, in)
	if err != nil || changed.Monthly == first.Monthly || changed.Product != first.Product {
		t.Errorf("a changed price did not make a new Price on the same Product: %+v (%v)", changed, err)
	}

	customer, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: uuid.NewString(), Name: "Subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := gw.CreateSubscriptionCheckout(ctx, payments.SubscriptionCheckoutRequest{
		AccountUID: uuid.NewString(), PlanKey: "enterprise-live-test", Customer: customer, Price: first.Yearly,
		SuccessURL: "http://localhost:3000/billing?subscribed=1", CancelURL: "http://localhost:3000/billing",
	})
	if err != nil || !strings.HasPrefix(checkout.URL, "https://checkout.stripe.com/") {
		t.Fatalf("subscription checkout = %+v, %v", checkout, err)
	}
}

// A usage invoice (ADR 0045): issued with net terms and sent; issued again under a key Stripe has
// forgotten it is still the same invoice, found by its uid; then credited and marked paid out of band.
// And a draft an earlier attempt left half-built is finished rather than duplicated.
func TestLiveUsageInvoice(t *testing.T) {
	gw, sc := liveGateway(t)
	ctx := context.Background()
	account, uid := uuid.NewString(), uuid.NewString()
	customer, err := gw.CreateCustomer(ctx, payments.CustomerRequest{
		AccountUID: account, Name: "Live invoiced studio", Email: "finance@studio.example", OrgSlug: "live-invoiced",
	})
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	request := payments.InvoiceRequest{
		AccountUID: account, InvoiceUID: uid, Customer: customer, Currency: "usd",
		DaysUntilDue: 30, Description: "PlayerSense, May 2026", PONumber: "PO-4471", Memo: "Net 30",
		Lines: []payments.InvoiceLine{
			{Position: 0, Description: "Platform fee, May 2026", AmountCents: 50_000},
			{Position: 1, Description: "Tracked players, May 2026", AmountCents: 12_345},
			{Position: 2, Description: "Credits applied", AmountCents: -2_345},
		},
		Since: time.Now(), Key: "playersense-invoice-live-" + uid,
	}

	issued, err := gw.IssueInvoice(ctx, request)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if issued.Status != "open" || issued.AmountDue != 60_000 || issued.HostedURL == "" || issued.DueAt.IsZero() {
		t.Fatalf("issued = %+v; want open, $600.00 due, with a hosted page and a due date", issued)
	}
	if days := issued.DueAt.Sub(time.Now()).Hours() / 24; days < 29 || days > 31 {
		t.Errorf("due in %.1f days, want 30", days)
	}
	t.Logf("hosted invoice: %s", issued.HostedURL)

	// A new key — Stripe has forgotten the old one — still finds the invoice by its uid.
	request.Key += "-resumed"
	again, err := gw.IssueInvoice(ctx, request)
	if err != nil || again.ID != issued.ID {
		t.Fatalf("resumed as %q (%v), want %q", again.ID, err, issued.ID)
	}
	if n := len(invoicesOf(t, sc, customer)); n != 1 {
		t.Errorf("%d invoices for the customer, want 1", n)
	}

	note, err := gw.CreditInvoice(ctx, payments.CreditNoteRequest{
		AccountUID: account, InvoiceUID: uid, Invoice: issued.ID, AmountCents: 10_000,
		Memo: "Outage credit", Key: "playersense-credit-live-" + uid,
	})
	if err != nil || note.AmountCents != 10_000 {
		t.Fatalf("credit note = %+v, %v", note, err)
	}

	// What the wallet settles from: the amount paid, after the credit note.
	paid, err := gw.PayInvoiceOutOfBand(ctx, issued.ID, "playersense-paid-live-"+uid)
	if err != nil || paid.Status != "paid" || paid.AmountPaid != 50_000 {
		t.Fatalf("paid = %+v, %v; want paid, $500.00", paid, err)
	}
}

// An attempt that died after creating the invoice and one line: the next finds the draft, adds only
// the line it lacks, and finalizes it.
func TestLiveUsageInvoiceResumesAHalfBuiltDraft(t *testing.T) {
	gw, sc := liveGateway(t)
	ctx := context.Background()
	account, uid := uuid.NewString(), uuid.NewString()
	customer, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: account, Name: "Live resumed studio", OrgSlug: "live-resumed"})
	if err == nil {
		// Created without one, as a customer opened by a top-up may have been: the contract route
		// sets it, because Stripe will not send an invoice to a customer with no address.
		err = gw.SetBillingEmail(ctx, customer, "finance@studio.example")
	}
	if err != nil {
		t.Fatalf("customer: %v", err)
	}
	meta := map[string]string{payments.MetaAccount: account, payments.MetaKind: payments.KindUsageInvoice, payments.MetaInvoice: uid}
	draft, err := sc.V1Invoices.Create(ctx, &stripe.InvoiceCreateParams{
		Customer: stripe.String(customer), Currency: stripe.String("usd"),
		CollectionMethod: stripe.String("send_invoice"), DaysUntilDue: stripe.Int64(30),
		AutoAdvance: stripe.Bool(false), PendingInvoiceItemsBehavior: stripe.String("exclude"), Metadata: meta,
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	first := map[string]string{payments.MetaLine: "0"}
	for k, v := range meta {
		first[k] = v
	}
	if _, err := sc.V1InvoiceItems.Create(ctx, &stripe.InvoiceItemCreateParams{
		Customer: stripe.String(customer), Invoice: stripe.String(draft.ID), Amount: stripe.Int64(50_000),
		Currency: stripe.String("usd"), Description: stripe.String("Platform fee"), Metadata: first,
	}); err != nil {
		t.Fatalf("first line: %v", err)
	}

	issued, err := gw.IssueInvoice(ctx, payments.InvoiceRequest{
		AccountUID: account, InvoiceUID: uid, Customer: customer, Currency: "usd", DaysUntilDue: 30,
		Description: "PlayerSense, June 2026",
		Lines: []payments.InvoiceLine{
			{Position: 0, Description: "Platform fee", AmountCents: 50_000},
			{Position: 1, Description: "Tracked players", AmountCents: 7_500},
		},
		Since: time.Now().Add(-time.Minute), Key: "playersense-invoice-live-" + uid,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if issued.ID != draft.ID || issued.Status != "open" || issued.AmountDue != 57_500 {
		t.Errorf("issued = %+v; want the draft %s, open, with both lines ($575.00)", issued, draft.ID)
	}
}
