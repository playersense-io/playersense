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
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/testsupport"
)

// Owing does not block ingestion, within the tolerance (ADR 0039, 0043).
//
// An account in its grace period still has its batches accepted — and its new players are still
// counted and charged downstream, so nothing it sends is free either. The gateway reads no balance,
// no price and no account state; only whether the organization has been suspended, from Valkey.
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
		SetState(billingaccount.StateGracePeriod).
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

// Past its grace period or credit limit, an organization sends no more data until it pays (ADR 0043):
// 429 with Retry-After — the answer the SDK keeps its batch for and retries later — and nothing
// reaches the queue. Paying lets it straight back in.
func TestASuspendedOrganizationIsRefusedUntilItPays(t *testing.T) {
	f := newIngestFixture(t, "standard")
	ctx := context.Background()
	cache := testCache(t)
	account := testsupport.SeedBillingAccount(t, f.db, f.orgID)
	if _, err := f.db.Client.BillingAccount.UpdateOneID(account.ID).
		SetArrearsMicros(80 * billing.Micros).SetBalanceMicros(-80 * billing.Micros).
		SetState(billingaccount.StateGracePeriod).Save(ctx); err != nil {
		t.Fatal(err)
	}
	job := &suspension.Job{DB: f.db, Cache: cache, Grace: 7 * 24 * time.Hour,
		Policy: suspension.Policy{Floor: 50 * billing.Micros, Percent: 25}}
	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Del(context.Background(), suspension.Key(cache, uint32(f.orgID))) })

	res := f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil))))
	res.AssertStatus(http.StatusTooManyRequests).AssertErrorCode("payment_required")
	if got := res.Header.Get("Retry-After"); got != "3600" {
		t.Errorf("Retry-After = %q, want 3600", got)
	}
	if n := len(f.published(t)); n != 0 {
		t.Fatalf("%d batches reached the queue from a suspended organization", n)
	}

	// Paying the debt — arrears to zero is what makes the account active — and the job unmarking it.
	if _, err := f.db.Client.BillingAccount.UpdateOneID(account.ID).
		SetArrearsMicros(0).SetBalanceMicros(0).SetState(billingaccount.StateActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// Past the gate's memory of the refusal — seconds, in a running API.
	f.gate.Now = func() time.Time { return time.Now().Add(suspension.GateTTL + time.Second) }
	f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).AssertStatus(http.StatusAccepted)
}
