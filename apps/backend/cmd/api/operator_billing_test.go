package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/schemas"
)

// The billing team's queue: accounts that need a person, and Stripe's events.

// TestAFailedStripeEventIsReplayedOnce: the body kept on the row is enough to run a fixed handler
// over it, the money arrives once, and a second replay is refused rather than repeated.
func TestAFailedStripeEventIsReplayedOnce(t *testing.T) {
	w := newWalletWith(t, paymentstest.New())
	w.client(t).Post(w.base()+"/billing/top-ups",
		testclient.JSON(map[string]any{"amount_micros": dollars(40)})).AssertStatus(http.StatusOK)
	uid, customer := w.account(t)
	org := w.organization(t, w.slug)
	accountID, err := w.db.Client.BillingAccount.Query().Where(billingaccount.OrganizationID(org.ID)).OnlyID(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// As Webhooks.fail would leave it: recorded, handling raised.
	eventID := "evt_" + uuid.NewString()
	var payload map[string]any
	raw, _ := json.Marshal(map[string]any{
		"id": eventID, "object": "event", "type": "payment_intent.succeeded",
		"api_version": "2026-04-22.dahlia", "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{
			"id": "pi_" + uuid.NewString()[:12], "object": "payment_intent", "status": "succeeded",
			"currency": "usd", "amount": 4_000, "amount_received": 4_000, "customer": customer,
			"metadata": map[string]string{payments.MetaAccount: uid, payments.MetaKind: payments.KindCheckout},
		}},
	})
	_ = json.Unmarshal(raw, &payload)
	row, err := w.db.Client.StripeEvent.Create().SetStripeEventID(eventID).SetType("payment_intent.succeeded").
		SetStripeCreatedAt(time.Now()).SetPayload(payload).SetStatus(stripeevent.StatusFailed).
		SetError("the database went away").SetAttempts(3).SetAccountID(accountID).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	listed := testclient.Decoded[schemas.StripeEventListOut](w.operator.Get(
		"/v1/operator/stripe-events?status=failed&org=" + w.slug).AssertStatus(http.StatusOK))
	if len(listed.Events) != 1 || listed.Events[0].StripeEventID != eventID || listed.Events[0].Payload != nil ||
		listed.Events[0].Organization != w.slug {
		t.Fatalf("listed %+v", listed.Events)
	}
	one := testclient.Decoded[schemas.StripeEventOut](w.operator.Get(
		"/v1/operator/stripe-events/" + row.UID.String()).AssertStatus(http.StatusOK))
	if one.Payload["id"] != eventID || one.Error != "the database went away" {
		t.Fatalf("one %+v", one)
	}

	replayed := testclient.Decoded[schemas.StripeReplayOut](w.operator.Post(
		"/v1/operator/stripe-events/" + row.UID.String() + "/replay").AssertStatus(http.StatusOK))
	if replayed.Status != "processed" || !replayed.Credited || replayed.Event.Attempts != 4 {
		t.Fatalf("replayed %+v", replayed)
	}
	if got := w.read(t); got.AvailableMicros != dollars(40) {
		t.Fatalf("available %d after the replay, want $40", got.AvailableMicros)
	}
	w.operator.Post("/v1/operator/stripe-events/" + row.UID.String() + "/replay").AssertStatus(http.StatusConflict)
	if got := w.read(t); got.AvailableMicros != dollars(40) {
		t.Fatalf("available %d after a refused replay", got.AvailableMicros)
	}
	w.operator.Post("/v1/operator/stripe-events/" + uuid.NewString() + "/replay").AssertStatus(http.StatusNotFound)
}

// TestTriageNamesEveryReason: an account suspended and with a declined top-up is listed once with
// both, and narrowing to one reason still finds it.
func TestTriageNamesEveryReason(t *testing.T) {
	w := newWallet(t)
	org := w.organization(t, w.slug)
	if err := w.db.Client.BillingAccount.Update().Where(billingaccount.OrganizationID(org.ID)).
		SetState(billingaccount.StateSuspended).SetAutoTopupLastError("Your card was declined.").
		Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = w.db.Client.BillingAccount.Update().Where(billingaccount.OrganizationID(org.ID)).
			SetState(billingaccount.StateActive).SetAutoTopupLastError("").Exec(context.Background())
	})

	find := func(reason string) schemas.AccountTriageRowOut {
		t.Helper()
		var found *schemas.AccountTriageRowOut
		next := ""
		for {
			page := testclient.Decoded[schemas.AccountTriageOut](w.operator.Get(
				"/v1/operator/accounts?limit=200&reason=" + reason + "&before=" + next).AssertStatus(http.StatusOK))
			for i := range page.Accounts {
				if page.Accounts[i].Organization.Slug == w.slug {
					found = &page.Accounts[i]
				}
			}
			if found != nil || page.Next == "" {
				break
			}
			next = page.Next
		}
		if found == nil {
			t.Fatalf("reason=%q does not list the account", reason)
		}
		return *found
	}
	all := find("")
	if !slices.Equal(all.Reasons, []string{"state", "auto_topup"}) || all.State != "suspended" {
		t.Fatalf("the row is %+v", all)
	}
	find("auto_topup")
	find("state")
}
