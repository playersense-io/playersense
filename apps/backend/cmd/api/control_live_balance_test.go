package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/wallet"
)

// The live balance (ADR 0044): usage reaches the ledger every few minutes, and the billing page does
// not wait for it. It shows the ledger minus what is queued, spent in the wallet's own order — promo
// first — so the numbers a studio reads are the numbers it will have once the queue settles.

type liveOut struct {
	Live struct {
		BalanceMicros   int64  `json:"balance_micros"`
		Balance         string `json:"balance"`
		AvailableMicros int64  `json:"available_micros"`
		PromoMicros     int64  `json:"promo_micros"`
		PurchasedMicros int64  `json:"purchased_micros"`
		OwedMicros      int64  `json:"owed_micros"`
		PendingMicros   int64  `json:"pending_micros"`
	} `json:"live"`
	AvailableMicros     int64 `json:"available_micros"`
	LedgerBalanceMicros int64 `json:"ledger_balance_micros"`
}

// queue puts usage in the organization's queue, not yet settled. The card sits on a throwaway plan,
// so it prices nobody else's players.
func (w *money) queue(t *testing.T, amount int64) {
	t.Helper()
	ctx := t.Context()
	org := w.organization(t, w.slug)
	account, err := w.db.Client.BillingAccount.Query().Where(billingaccount.OrganizationID(org.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := w.db.Client.Plan.Create().SetKey("live-" + uuid.NewString()[:8]).SetName("Live").
		SetEntitlements(map[string]int64{}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	card, err := w.db.Client.RateCard.Create().SetPlanID(plan.ID).SetMeter(ratecard.MeterTrackedPlayers).
		SetTiers(billing.Tiers{{UnitPriceMicros: 1_000}}).SetEffectiveFrom(time.Now()).
		SetCreatedBy("test@playersense.dev").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Now().UnixNano()%1_000_000_000 + 1 // a range of its own, whatever else is queued
	if err := w.db.Client.UsageCharge.Create().SetAccountID(account.ID).
		SetPeriodStart(charging.PeriodOf(time.Now())).SetRateCardID(card.ID).
		SetFirstUnit(first).SetLastUnit(first + amount/1_000 - 1).SetAmountMicros(amount).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func (w *money) live(t *testing.T) liveOut {
	t.Helper()
	return testclient.Decoded[liveOut](w.client(t).Get(w.base() + "/billing").AssertStatus(http.StatusOK))
}

func TestTheBalanceAStudioSeesIncludesUsageNotYetSettled(t *testing.T) {
	w := newWallet(t)
	w.grant(t, map[string]any{"kind": wallet.Promo, "amount_micros": dollars(10), "description": "Launch credit"})
	w.grant(t, map[string]any{"kind": wallet.Purchase, "amount_micros": dollars(30), "description": "Top-up"})

	w.queue(t, dollars(15))
	got := w.live(t)
	if got.AvailableMicros != dollars(40) || got.LedgerBalanceMicros != dollars(40) {
		t.Errorf("ledger figures = %s available, %s balance; want $40 each — the queue is not on the ledger yet",
			billing.USD(got.AvailableMicros), billing.USD(got.LedgerBalanceMicros))
	}
	l := got.Live
	if l.PendingMicros != dollars(15) || l.BalanceMicros != dollars(25) || l.AvailableMicros != dollars(25) ||
		l.PromoMicros != 0 || l.PurchasedMicros != dollars(25) || l.OwedMicros != 0 || l.Balance != "$25.00" {
		t.Errorf("live = %+v; want $15 pending, $25 left, promo spent first", l)
	}

	// Past what the wallet holds, the rest shows as owed — before the drain has written a line.
	w.queue(t, dollars(40))
	l = w.live(t).Live
	if l.PendingMicros != dollars(55) || l.BalanceMicros != -dollars(15) || l.AvailableMicros != 0 ||
		l.PromoMicros != 0 || l.PurchasedMicros != 0 || l.OwedMicros != dollars(15) {
		t.Errorf("live = %+v; want $55 pending, −$15, $15 owed", l)
	}
}
