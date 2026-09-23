package suspension_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/valkey"
	"playersense.dev/backend/internal/wallet"
)

// The bounded tolerance (ADR 0043), against the real database and cache.

// policy is the deployment default: a $50 floor, and 20% of the last 30 days' paid usage.
var policy = suspension.Policy{Floor: 50 * billing.Micros, Percent: 20}

type fixture struct {
	t       *testing.T
	db      *pgdb.DB
	cache   *valkey.Client
	account *ent.BillingAccount
	now     time.Time
	units   int64 // players queued so far, so each queued row covers the next ones
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	cache := testsupport.Cache(t, settings)
	ctx := context.Background()
	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "susp-" + id + "@studio.example", EmailVerified: true,
		Name: "Susp", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgs, _ := identity.Organizations(ctx, db.Client, person.ID)
	f := &fixture{t: t, db: db, cache: cache, account: testsupport.SeedBillingAccount(t, db, orgs[0].ID), now: time.Now().UTC()}
	t.Cleanup(func() { _ = cache.Del(context.Background(), suspension.Key(cache, uint32(f.account.OrganizationID))) })
	return f
}

func (f *fixture) job() *suspension.Job {
	return &suspension.Job{DB: f.db, Cache: f.cache, Grace: 7 * 24 * time.Hour,
		Policy: policy, Now: func() time.Time { return f.now }}
}

// owe charges usage the wallet cannot cover, as the drain does: arrears, and the grace period begins.
func (f *fixture) owe(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := wallet.ChargeUsage(context.Background(), tx, wallet.UsageRequest{
		AccountID: f.account.ID, Amount: amount, At: time.Now(), Key: "usage:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) pay(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, _, err := wallet.Grant(context.Background(), tx, wallet.GrantRequest{
		AccountID: f.account.ID, Kind: wallet.Purchase, Amount: amount, GrantedAt: time.Now(),
		Key: "topup:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) state() billingaccount.State {
	f.t.Helper()
	row, err := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return row.State
}

func (f *fixture) gate() *suspension.Gate {
	return &suspension.Gate{Cache: f.cache, Now: func() time.Time { return f.now }}
}

func (f *fixture) run() {
	f.t.Helper()
	if _, err := f.job().Run(context.Background()); err != nil {
		f.t.Logf("pass: %v", err)
	}
}

// Owing a little, for a little while, changes nothing: the tolerance.
func TestAnAccountInItsGracePeriodKeepsIngesting(t *testing.T) {
	f := newFixture(t)
	f.owe(10 * billing.Micros)
	f.now = f.now.Add(6 * 24 * time.Hour)
	f.run()
	if f.state() != billingaccount.StateGracePeriod || f.gate().Suspended(context.Background(), uint32(f.account.OrganizationID)) {
		t.Errorf("state %s after six days owing $10; want grace_period and ingesting", f.state())
	}
}

// The time bound: owing for longer than the grace period suspends, however little.
func TestOwingPastTheGracePeriodSuspends(t *testing.T) {
	f := newFixture(t)
	f.owe(10 * billing.Micros)
	f.now = f.now.Add(7*24*time.Hour + time.Minute)
	f.run()
	if f.state() != billingaccount.StateSuspended || !f.gate().Suspended(context.Background(), uint32(f.account.OrganizationID)) {
		t.Errorf("state %s after the grace period; want suspended and refused", f.state())
	}
}

// The money bound: owing the credit limit suspends at once, whatever the time — because owing does
// not grow with events, and events are what cost compute and storage.
func TestOwingTheCreditLimitSuspendsAtOnce(t *testing.T) {
	f := newFixture(t)
	f.owe(50 * billing.Micros)
	f.run()
	if f.state() != billingaccount.StateSuspended {
		t.Errorf("state %s owing the $50 limit on day one; want suspended", f.state())
	}
}

// A plan's own credit limit is used instead of the default — Enterprise can be trusted with more.
func TestAPlanCanCarryALargerCreditLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, err := f.db.Client.Plan.Create().SetKey("trusted-" + uuid.NewString()[:8]).SetName("Trusted").
		SetEntitlements(map[string]int64{string(billing.CreditLimit): 500 * billing.Micros}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).SetPlanID(p.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	f.owe(100 * billing.Micros)
	f.run()
	if f.state() != billingaccount.StateGracePeriod {
		t.Errorf("state %s owing $100 under a $500 limit; want still in grace", f.state())
	}
}

// Paying the debt makes the account active and lets its batches in again — without waiting for the
// mark to lapse, and whether it paid in grace or once suspended.
func TestPayingLetsASuspendedAccountBackIn(t *testing.T) {
	f := newFixture(t)
	org := uint32(f.account.OrganizationID)
	f.owe(60 * billing.Micros)
	j := f.job()
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.gate().Suspended(context.Background(), org) {
		t.Fatal("not suspended over the credit limit")
	}

	f.pay(100 * billing.Micros)
	if f.state() != billingaccount.StateActive {
		t.Fatalf("state %s after paying the debt; want active", f.state())
	}
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(suspension.GateTTL + time.Second) // past the ingestion path's memory
	if f.gate().Suspended(context.Background(), org) {
		t.Error("an account that paid is still refused")
	}
}

// Valkey unreachable: the gate lets batches in rather than refusing every customer.
func TestTheGateFailsOpenWithoutValkey(t *testing.T) {
	g := &suspension.Gate{Cache: valkey.New("redis://127.0.0.1:1/0")} // never started
	if g.Suspended(context.Background(), 1) {
		t.Error("the gate refused with no way to know")
	}
}

// The billing page can say when ingestion will stop.
func TestSuspendsAtIsTheGraceDeadline(t *testing.T) {
	f := newFixture(t)
	f.owe(10 * billing.Micros)
	row, _ := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	at := suspension.SuspendsAt(row, 7*24*time.Hour)
	if at == nil || at.Sub(row.StateChangedAt) != 7*24*time.Hour {
		t.Errorf("suspends at %v, owing since %v", at, row.StateChangedAt)
	}
}

// spend puts usage through a funded wallet: money the account paid for players, the thing its
// credit limit scales with.
func (f *fixture) spend(amount int64) {
	f.t.Helper()
	f.pay(amount)
	f.owe(amount)
}

func (f *fixture) limit() (int64, suspension.LimitSource) {
	f.t.Helper()
	row, err := f.db.Client.BillingAccount.Query().Where(billingaccount.ID(f.account.ID)).WithPlan().Only(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	limit, source, err := suspension.Limit(context.Background(), f.db.Client, row, policy, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return limit, source
}

// The limit follows what an account pays for: a studio spending $5,000 a month may owe $1,000; one
// spending $100 gets the $50 floor.
func TestTheCreditLimitScalesWithSpend(t *testing.T) {
	big := newFixture(t)
	big.spend(5_000 * billing.Micros)
	if limit, source := big.limit(); limit != 1_000*billing.Micros || source != suspension.FromUsage {
		t.Errorf("a $5,000 spender's limit = %s (%s), want $1,000 from usage", billing.USD(limit), source)
	}

	small := newFixture(t)
	small.spend(100 * billing.Micros)
	if limit, source := small.limit(); limit != 50*billing.Micros || source != suspension.FromFloor {
		t.Errorf("a $100 spender's limit = %s (%s), want the $50 floor", billing.USD(limit), source)
	}
}

// The same debt suspends a small spender and not a large one.
func TestTheSameDebtIsToleratedForALargeSpenderOnly(t *testing.T) {
	big := newFixture(t)
	big.spend(5_000 * billing.Micros)
	big.owe(200 * billing.Micros)
	big.run()

	small := newFixture(t)
	small.spend(100 * billing.Micros)
	small.owe(200 * billing.Micros)
	small.run()

	if big.state() != billingaccount.StateGracePeriod || small.state() != billingaccount.StateSuspended {
		t.Errorf("owing $200: large spender %s, small spender %s; want grace and suspended", big.state(), small.state())
	}
}

// An override set by PlayerSense wins over the rule, both ways.
func TestAnOverrideWinsOverTheRule(t *testing.T) {
	f := newFixture(t)
	f.spend(5_000 * billing.Micros)
	if _, err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetEntitlementOverrides(map[string]int64{string(billing.CreditLimit): 20 * billing.Micros}).
		Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	if limit, source := f.limit(); limit != 20*billing.Micros || source != suspension.FromOverride {
		t.Errorf("limit = %s (%s), want the $20 override", billing.USD(limit), source)
	}
}

// The scenario, end to end: top up $5,000, spend it, the card declines, and the studio keeps sending.
// It may owe exactly 20% of what it paid for — $1,000, not more because the debt grew its usage — and a
// $5,000 top-up then pays the debt first and leaves $4,000, active again.
func TestTheToleranceIsTwentyPercentOfWhatWasPaidFor(t *testing.T) {
	f := newFixture(t)
	org := uint32(f.account.OrganizationID)
	f.spend(5_000 * billing.Micros)

	f.owe(999 * billing.Micros)
	f.run()
	if f.state() != billingaccount.StateGracePeriod {
		t.Fatalf("owing $999 of a $1,000 tolerance: %s, want still ingesting", f.state())
	}
	if limit, _ := f.limit(); limit != 1_000*billing.Micros {
		t.Fatalf("the limit moved with the debt: %s, want $1,000", billing.USD(limit))
	}

	f.owe(1 * billing.Micros) // $1,000 owed: the limit
	f.run()
	if f.state() != billingaccount.StateSuspended || !f.gate().Suspended(context.Background(), org) {
		t.Fatalf("owing $1,000: %s, want suspended", f.state())
	}
	row, _ := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if row.BalanceMicros != -1_000*billing.Micros {
		t.Errorf("balance %s, want -$1,000", billing.USD(row.BalanceMicros))
	}

	f.pay(5_000 * billing.Micros)
	row, _ = f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if row.BalanceMicros != 4_000*billing.Micros || row.ArrearsMicros != 0 || row.State != billingaccount.StateActive {
		t.Errorf("after topping up $5,000: balance %s, owes %s, %s; want $4,000, nothing, active",
			billing.USD(row.BalanceMicros), billing.USD(row.ArrearsMicros), row.State)
	}
	j := f.job()
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(suspension.GateTTL + time.Second)
	if f.gate().Suspended(context.Background(), org) {
		t.Error("still refused after paying")
	}
}

// queue puts usage in the account's queue, not yet settled onto its ledger — as between two
// settlements (ADR 0044). The card sits on a throwaway plan so it prices nobody else's players.
func (f *fixture) queue(amount int64) {
	f.t.Helper()
	ctx := context.Background()
	plan, err := f.db.Client.Plan.Create().SetKey("pending-" + uuid.NewString()[:8]).SetName("Pending").
		SetEntitlements(map[string]int64{}).Save(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	card, err := f.db.Client.RateCard.Create().SetPlanID(plan.ID).SetMeter(ratecard.MeterTrackedPlayers).
		SetTiers(billing.Tiers{{UnitPriceMicros: 1_000}}).SetEffectiveFrom(time.Now()).
		SetCreatedBy("test@playersense.dev").Save(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.db.Client.UsageCharge.Create().SetAccountID(f.account.ID).
		SetPeriodStart(charging.PeriodOf(time.Now())).SetRateCardID(card.ID).
		SetFirstUnit(f.units + 1).SetLastUnit(f.units + amount/1_000).SetAmountMicros(amount).Exec(ctx); err != nil {
		f.t.Fatal(err)
	}
	f.units += amount / 1_000
}

// Usage waiting to be settled counts as debt: settlement runs every few minutes, and an account must
// not slip past its credit limit in between.
func TestUsageNotYetSettledCountsTowardTheCreditLimit(t *testing.T) {
	f := newFixture(t)
	f.owe(10 * billing.Micros) // in grace, well under the $50 floor
	f.run()
	if f.state() != billingaccount.StateGracePeriod {
		t.Fatalf("state %s owing $10; want grace", f.state())
	}
	f.queue(45 * billing.Micros) // $55 owed, $45 of it not yet on the ledger
	f.run()
	if f.state() != billingaccount.StateSuspended {
		t.Errorf("state %s owing $10 settled and $45 queued; want suspended at the $50 limit", f.state())
	}
}

// Queued usage is debt only past what the wallet holds: a funded account is not charged twice over.
func TestQueuedUsageIsDebtOnlyPastTheBalance(t *testing.T) {
	f := newFixture(t)
	f.pay(30 * billing.Micros)
	f.queue(20 * billing.Micros)
	account, err := f.db.Client.BillingAccount.Get(context.Background(), f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owed, err := suspension.Owed(context.Background(), f.db.Client, account); err != nil || owed != 0 {
		t.Errorf("owed %d (%v) with $30 held and $20 queued; want 0", owed, err)
	}
	f.queue(25 * billing.Micros)
	if owed, err := suspension.Owed(context.Background(), f.db.Client, account); err != nil || owed != 15*billing.Micros {
		t.Errorf("owed %s (%v) with $30 held and $45 queued; want $15", billing.USD(owed), err)
	}
}
