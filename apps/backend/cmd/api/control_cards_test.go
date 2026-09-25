package main

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/schemas"
)

// The cards, invoice details and receipts a billing page shows: read from Stripe once, then kept
// in step by its webhooks, and never a card number.
func TestTheBillingPageFollowsStripe(t *testing.T) {
	stripe := paymentstest.New()
	w := newWalletWith(t, stripe)
	client := w.client(t)
	client.Post(w.base()+"/billing/top-ups", testclient.JSON(map[string]any{"amount_micros": dollars(40)})).
		AssertStatus(http.StatusOK)
	uid, customer := w.account(t)
	one, two := unique("pm_one"), unique("pm_two")
	stripe.Details = map[string]payments.CustomerDetails{customer: {
		Name: "Pixel Forge Ltd", Email: "finance@pixelforge.example", Address: []string{"1 Game St", "34000 Istanbul"},
		Country: "TR", DefaultCard: one,
		Cards: []payments.Card{{ID: one, Brand: "visa", Last4: "4242", ExpMonth: 12, ExpYear: 2030}},
	}}
	deliver := func(eventType string, object map[string]any) {
		t.Helper()
		body, header := signed(t, eventType, object)
		testclient.New(t, w.app).Post("/v1/stripe/webhook", testclient.Body("application/json", bytes.NewReader(body)),
			testclient.Header("Stripe-Signature", header)).AssertStatus(http.StatusOK)
	}

	// The first read asks Stripe.
	cards := testclient.Decoded[schemas.CardsOut](client.Get(w.base() + "/billing/cards").AssertStatus(http.StatusOK))
	if len(cards.Cards) != 1 || cards.Cards[0].Last4 != "4242" || !cards.Cards[0].IsDefault || cards.SyncedAt == nil {
		t.Fatalf("first read %+v", cards)
	}

	// A new card saved, made the default, and the old one removed, all by webhook.
	deliver("payment_method.attached", map[string]any{"id": two, "object": "payment_method", "type": "card",
		"customer": customer, "card": map[string]any{"brand": "mastercard", "last4": "4444", "exp_month": 1, "exp_year": 2031}})
	deliver("customer.updated", map[string]any{"id": customer, "object": "customer", "name": "Pixel Forge Ltd",
		"email": "billing@pixelforge.example", "address": map[string]any{"line1": "2 Game St", "city": "Istanbul", "country": "TR"},
		"invoice_settings": map[string]any{"default_payment_method": two}})
	deliver("payment_method.detached", map[string]any{"id": one, "object": "payment_method", "type": "card",
		"card": map[string]any{"brand": "visa", "last4": "4242", "exp_month": 12, "exp_year": 2030}})
	deliver("customer.tax_id.created", map[string]any{"id": unique("txi"), "object": "tax_id", "type": "eu_vat",
		"value": "DE123456789", "customer": customer})

	cards = testclient.Decoded[schemas.CardsOut](client.Get(w.base() + "/billing/cards").AssertStatus(http.StatusOK))
	if len(cards.Cards) != 1 || cards.Cards[0].ID != two || !cards.Cards[0].IsDefault || cards.Cards[0].Brand != "mastercard" {
		t.Fatalf("after the webhooks %+v", cards.Cards)
	}
	// From the page: a third card made the default, then removed, and the one left takes over.
	three := unique("pm_three")
	deliver("payment_method.attached", map[string]any{"id": three, "object": "payment_method", "type": "card",
		"customer": customer, "card": map[string]any{"brand": "amex", "last4": "0005", "exp_month": 3, "exp_year": 2032}})
	cards = testclient.Decoded[schemas.CardsOut](client.Put(w.base()+"/billing/cards/"+three+"/default",
		testclient.JSON(map[string]any{})).AssertStatus(http.StatusOK))
	if len(cards.Cards) != 2 || cards.Cards[0].ID != three || !cards.Cards[0].IsDefault || cards.Cards[1].IsDefault {
		t.Fatalf("after making the third the default %+v", cards.Cards)
	}
	if stripe.Details[customer].DefaultCard != three {
		t.Fatal("Stripe was not told the new default")
	}
	cards = testclient.Decoded[schemas.CardsOut](client.Delete(w.base() + "/billing/cards/" + three).AssertStatus(http.StatusOK))
	if len(cards.Cards) != 1 || cards.Cards[0].ID != two || !cards.Cards[0].IsDefault {
		t.Fatalf("after removing the default %+v", cards.Cards)
	}
	if stripe.Details[customer].DefaultCard != two || len(stripe.Detached) != 1 {
		t.Fatalf("Stripe after the removal: default %q, detached %v", stripe.Details[customer].DefaultCard, stripe.Detached)
	}
	client.Delete(w.base() + "/billing/cards/" + three).AssertStatus(http.StatusNotFound)
	client.Put(w.base()+"/billing/cards/pm_nobodys/default", testclient.JSON(map[string]any{})).AssertStatus(http.StatusNotFound)

	details := testclient.Decoded[schemas.InvoiceDetailsOut](client.Get(w.base() + "/billing/details").AssertStatus(http.StatusOK))
	if details.Email != "billing@pixelforge.example" || len(details.Address) != 2 || details.Country != "TR" ||
		len(details.TaxIDs) != 1 || details.TaxIDs[0] != "eu_vat DE123456789" || details.Customer != customer {
		t.Fatalf("details %+v", details)
	}

	// A top-up's invoice is a receipt; a usage invoice is not.
	now := time.Now().Unix()
	deliver("invoice.paid", map[string]any{"id": unique("in_topup"), "object": "invoice", "customer": customer, "status": "paid",
		"number": "PS-0001", "currency": "usd", "total": 4000, "created": now,
		"hosted_invoice_url": "https://invoice.stripe.test/in_topup", "invoice_pdf": "https://invoice.stripe.test/in_topup.pdf",
		"status_transitions": map[string]any{"finalized_at": now, "paid_at": now},
		"metadata":           map[string]string{payments.MetaAccount: uid, payments.MetaKind: payments.KindCheckout}})
	receipts := testclient.Decoded[schemas.ReceiptsOut](client.Get(w.base() + "/billing/receipts").AssertStatus(http.StatusOK))
	if len(receipts.Receipts) != 1 {
		t.Fatalf("%d receipts", len(receipts.Receipts))
	}
	if r := receipts.Receipts[0]; r.Kind != "top_up" || r.TotalMicros != dollars(40) || r.PaidAt == nil ||
		r.PDFURL == "" || r.Number != "PS-0001" {
		t.Fatalf("receipt %+v", r)
	}

	// Adding a card is Stripe's page, and nothing is charged.
	client.Post(w.base() + "/billing/cards").AssertStatus(http.StatusOK)
	if len(stripe.Setups) != 1 || stripe.Setups[0].Customer != customer {
		t.Fatalf("setups %+v", stripe.Setups)
	}
}
