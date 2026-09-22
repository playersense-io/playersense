package payments_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/testsupport"
)

// The round trip, with Stripe in it: a real automatic top-up charged in test mode, and Stripe's own
// signed webhook for it arriving at a running API through `stripe listen`.
//
// Opt-in — STRIPE_E2E=1 — and it needs, running beside it:
//
//	stripe listen --forward-to localhost:21080/v1/stripe/webhook
//	STRIPE_WEBHOOK_SECRET=<the secret `stripe listen` printed> go run ./apps/backend/cmd/api
//
// What it proves that nothing else can: that Stripe's real payload, signed with a real secret, in
// whatever API version the account sends, verifies and decodes here; and that the webhook for a
// payment the loop already credited changes nothing.
func TestEndToEndAutoTopUpWithStripesOwnWebhook(t *testing.T) {
	if os.Getenv("STRIPE_E2E") != "1" {
		t.Skip("set STRIPE_E2E=1 with `stripe listen` and the API running")
	}
	gw, sc := liveGateway(t)
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	ctx := context.Background()

	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "e2e-" + id + "@studio.example",
		EmailVerified: true, Name: "E2E", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgs, _ := identity.Organizations(ctx, db.Client, person.ID)
	account := testsupport.SeedBillingAccount(t, db, orgs[0].ID)

	customer, err := gw.CreateCustomer(ctx, payments.CustomerRequest{AccountUID: account.UID.String(), Name: "E2E studio " + id})
	if err != nil {
		t.Fatal(err)
	}
	savedCard(t, sc, customer, "pm_card_visa")
	account, err = account.Update().
		SetStripeCustomerID(customer).
		SetAutoTopupEnabled(true).
		SetAutoTopupThresholdMicros(10 * billing.Micros).
		SetAutoTopupAmountMicros(25 * billing.Micros).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = account.Update().SetAutoTopupEnabled(false).Save(context.Background()) })

	// The loop charges the saved card for real and credits the wallet from its own answer.
	loop := &payments.AutoTopUp{DB: db, Gateway: onlyCustomer{gw, customer}}
	if _, err := loop.Run(ctx); err != nil {
		t.Logf("pass (other accounts in the shared database may fail): %v", err)
	}
	lots, err := db.Client.CreditLot.Query().Where(creditlot.AccountID(account.ID)).All(ctx)
	if err != nil || len(lots) != 1 || lots[0].AmountMicros != 25*billing.Micros {
		t.Fatalf("after the charge: %d lots (%v), want one $25 lot", len(lots), err)
	}
	pi := *lots[0].StripeReference
	t.Logf("charged %s", pi)

	// Stripe's webhook for the same payment arrives through `stripe listen`, verifies, and
	// finds the payment already credited.
	deadline := time.Now().Add(45 * time.Second)
	for {
		events, err := db.Client.StripeEvent.Query().
			Where(stripeevent.Type("payment_intent.succeeded"), stripeevent.StatusIn(stripeevent.StatusProcessed, stripeevent.StatusFailed)).
			Order(stripeevent.ByID()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if obj, ok := e.Payload["data"].(map[string]any)["object"].(map[string]any); ok && obj["id"] == pi {
				if e.Status != stripeevent.StatusProcessed {
					t.Fatalf("Stripe's webhook for %s was %s: %s", pi, e.Status, e.Error)
				}
				t.Logf("webhook %s (api %s) processed", e.StripeEventID, e.APIVersion)
				after, _ := db.Client.CreditLot.Query().Where(creditlot.AccountID(account.ID)).Count(ctx)
				if after != 1 {
					t.Fatalf("the webhook credited again: %d lots", after)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no webhook for %s arrived; is `stripe listen` forwarding to the running API?", pi)
		}
		time.Sleep(time.Second)
	}
}

// onlyCustomer keeps a pass over the shared database from charging any other account's real card.
type onlyCustomer struct {
	payments.Gateway
	customer string
}

func (o onlyCustomer) ChargeableCard(ctx context.Context, customer string) (string, error) {
	if customer != o.customer || !strings.HasPrefix(customer, "cus_") {
		return "", nil
	}
	return o.Gateway.ChargeableCard(ctx, customer)
}
