package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/testsupport"
)

// Billing never blocks ingestion (ADR 0039).
//
// An account that owes, and even one a person has suspended, still has its batches accepted — and
// its new players are still counted and charged downstream, so nothing it sends is free either.
// The gateway does not read a balance, a price or an account state, and this is the test that says
// so rather than a comment.
func TestAnAccountThatOwesStillHasItsBatchesAcceptedAndCharged(t *testing.T) {
	f := newIngestFixture(t, "standard")
	ctx := context.Background()

	account := testsupport.SeedBillingAccount(t, f.db, f.orgID)
	if _, err := f.db.Client.RateCard.Create().
		SetPlanID(account.PlanID).
		SetMeter(ratecard.MeterTrackedPlayers).
		SetTiers(billing.Tiers{{UnitPriceMicros: 500}}).
		SetEffectiveFrom(time.Now().Add(-time.Minute)).
		SetCreatedBy("test@playersense.dev").
		Save(ctx); err != nil {
		// Another test published a card for the shared free plan at the same instant.
		t.Logf("card: %v", err)
	}
	if _, err := f.db.Client.BillingAccount.UpdateOneID(account.ID).
		SetArrearsMicros(1_000 * billing.Micros).
		SetBalanceMicros(-1_000 * billing.Micros).
		SetState(billingaccount.StateSuspended).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	res := f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil))))
	res.AssertStatus(http.StatusAccepted)
	if got := decodeAccepted(t, res); got.Accepted != 1 {
		t.Fatalf("accepted %d events from an account in arrears, want 1", got.Accepted)
	}

	// And the charger counts the player: owing does not make players free.
	msgs := f.published(t)
	if len(msgs) != 1 {
		t.Fatalf("%d messages on the bus, want 1", len(msgs))
	}
	accruer := &charging.Accruer{DB: f.db, Key: []byte("test-" + uuid.NewString())}
	if err := accruer.Handle(ctx, msgs); err != nil {
		t.Fatalf("the charger refused the batch: %v", err)
	}
	if got := accruer.Stats.Counted.Load(); got != 1 {
		t.Errorf("counted %d players for an account in arrears, want 1", got)
	}
}
