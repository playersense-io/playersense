package charging

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billableplayer"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/valkey"
	"playersense.dev/backend/internal/wallet"
)

// The charger's promises, each tested where it could break: against the real database and the real
// cache, because every one of them is a property of a transaction boundary or a race, and a fake
// would remove exactly the thing under test.

func upTo(n int64) *int64 { return &n }

// launch is the card from the request: $0.0005 to 100k, $0.0004 to 500k, $0.00035 to 1M, $0.0003
// above — scaled down by 10,000 in positions so a test can cross every band with a few hundred
// players. The prices are the real ones.
func launch() billing.Tiers {
	return billing.Tiers{
		{UpTo: upTo(10), UnitPriceMicros: 500},
		{UpTo: upTo(50), UnitPriceMicros: 400},
		{UpTo: upTo(100), UnitPriceMicros: 350},
		{UnitPriceMicros: 300},
	}
}

// deadLetters is a bus that only parks messages. The charger never publishes, and subscription is
// the Service's business rather than the handler's.
type deadLetters struct {
	mu     sync.Mutex
	parked []*eventbus.Message
}

func (b *deadLetters) Publish(context.Context, *eventbus.Message) error { return nil }
func (b *deadLetters) Subscribe(context.Context, string, eventbus.Handler) error {
	return nil
}
func (b *deadLetters) Name() string { return "test" }
func (b *deadLetters) DeadLetter(_ context.Context, msg *eventbus.Message, _ eventbus.DeadLetterReason) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.parked = append(b.parked, msg)
	return nil
}

// fixture is one organization with an account on its own plan and card.
type fixture struct {
	t       *testing.T
	db      *pgdb.DB
	cache   *valkey.Client
	bus     *deadLetters
	org     int
	account int
	plan    int
	project string
	env     string
	key     []byte
}

func newFixture(t *testing.T, mode billing.Mode, included int64) *fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	cache := testsupport.Cache(t, settings)
	ctx := context.Background()

	id := uuid.NewString()[:12]
	person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
		UID: "firebase-" + id, Email: "charging-" + id + "@studio.example",
		EmailVerified: true, Name: "Ada", Provider: "google.com",
	}, time.Now())
	if err != nil {
		t.Fatalf("seed a person: %v", err)
	}
	orgs, err := identity.Organizations(ctx, db.Client, person.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("the personal organization is missing: %v", err)
	}

	plan, err := db.Client.Plan.Create().
		SetKey("test-" + id).SetName("Test " + id).
		SetEntitlements(map[string]int64{string(billing.IncludedMAU): included}).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed a plan: %v", err)
	}
	f := &fixture{
		t: t, db: db, cache: cache, bus: &deadLetters{},
		org: orgs[0].ID, plan: plan.ID,
		project: uuid.NewString(), env: uuid.NewString(),
		key: []byte("test-digest-key-" + id),
	}
	f.publishCard(mode, launch(), time.Now().Add(-time.Hour))

	account, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(f.org)).Only(ctx)
	if ent.IsNotFound(err) {
		account, err = db.Client.BillingAccount.Create().
			SetOrganizationID(f.org).SetPlanID(plan.ID).Save(ctx)
	} else if err == nil {
		account, err = account.Update().SetPlanID(plan.ID).Save(ctx)
	}
	if err != nil {
		t.Fatalf("seed the account: %v", err)
	}
	f.account = account.ID
	return f
}

func (f *fixture) publishCard(mode billing.Mode, tiers billing.Tiers, from time.Time) {
	f.t.Helper()
	if _, err := f.db.Client.RateCard.Create().
		SetPlanID(f.plan).SetMeter("tracked_players").SetMode(ratecardMode(mode)).
		SetTiers(tiers).SetEffectiveFrom(from).SetCreatedBy("test@playersense.dev").
		Save(context.Background()); err != nil {
		f.t.Fatalf("publish a card: %v", err)
	}
}

func (f *fixture) accruer() *Accruer {
	return &Accruer{DB: f.db, Cache: f.cache, Bus: f.bus, Key: f.key, MaxInsert: 7}
}

func (f *fixture) drainer() *Drainer { return &Drainer{DB: f.db} }

// players returns n new install ids.
func players(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

// messages is one batch per player, as the SDK would send them.
func (f *fixture) messages(installs []string, at time.Time) []*eventbus.Message {
	out := make([]*eventbus.Message, len(installs))
	for i, install := range installs {
		payload, _ := json.Marshal(map[string]any{"install_id": install, "batch_id": uuid.NewString()})
		out[i] = &eventbus.Message{
			PublishedAt: at, DedupKey: uuid.NewString(),
			OrgSeq: uint32(f.org), OrgID: uuid.NewString(),
			ProjectID: f.project, EnvironmentID: f.env,
			PolicyTier: "standard", EventCount: 3, Payload: payload,
		}
	}
	return out
}

func (f *fixture) handle(a *Accruer, msgs []*eventbus.Message) {
	f.t.Helper()
	if err := a.Handle(context.Background(), msgs); err != nil {
		f.t.Fatalf("Handle: %v", err)
	}
}

func (f *fixture) drain() DrainResult {
	f.t.Helper()
	result, err := f.drainer().Run(context.Background())
	if err != nil {
		f.t.Fatalf("drain: %v", err)
	}
	return result
}

func (f *fixture) topUp(amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, _, err := wallet.Grant(context.Background(), tx, wallet.GrantRequest{
		AccountID: f.account, Kind: wallet.Purchase, Amount: amount,
		GrantedAt: time.Now(), Key: "topup:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatalf("top up: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) usage() *ent.MonthlyUsage {
	f.t.Helper()
	row, err := f.db.Client.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(f.account), monthlyusage.PeriodStart(PeriodOf(time.Now()))).
		Only(context.Background())
	if err != nil {
		f.t.Fatalf("read the month: %v", err)
	}
	return row
}

func (f *fixture) charges() []*ent.UsageCharge {
	f.t.Helper()
	rows, err := f.db.Client.UsageCharge.Query().
		Where(usagecharge.AccountID(f.account)).
		Order(ent.Asc(usagecharge.FieldFirstUnit)).All(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func (f *fixture) unsettled() int {
	f.t.Helper()
	n, err := f.db.Client.UsageCharge.Query().
		Where(usagecharge.AccountID(f.account), usagecharge.SettledAtIsNil()).Count(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) accountRow() *ent.BillingAccount {
	f.t.Helper()
	row, err := f.db.Client.BillingAccount.Get(context.Background(), f.account)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fixture) usageEntries() []*ent.LedgerEntry {
	f.t.Helper()
	rows, err := f.db.Client.LedgerEntry.Query().
		Where(ledgerentry.AccountID(f.account), ledgerentry.KindEQ(ledgerentry.KindUsage)).
		All(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

// assertBooks is every conservation law at once: the month's count is exactly the players counted,
// the queue sums to what the card says that count costs, the queued positions are contiguous with no
// gap and no overlap, and — once drained — the wallet moved by exactly the queue's total and the
// wallet's invariant holds.
func (f *fixture) assertBooks(card billing.Card, included, wantPlayers int64) {
	f.t.Helper()
	usage := f.usage()
	if usage.TrackedPlayers != wantPlayers {
		f.t.Errorf("tracked %d players, want %d", usage.TrackedPlayers, wantPlayers)
	}
	var queued int64
	next := int64(0)
	for _, c := range f.charges() {
		queued += c.AmountMicros
		if next != 0 && c.FirstUnit != next {
			f.t.Errorf("charges are not contiguous: one starts at %d, the previous ended at %d", c.FirstUnit, next-1)
		}
		next = c.LastUnit + 1
	}
	if want := card.Total(wantPlayers, included); queued != want || usage.ChargedMicros != want {
		f.t.Errorf("queued %s, month says %s; the card says %d players cost %s",
			billing.UnitPrice(queued), billing.UnitPrice(usage.ChargedMicros), wantPlayers, billing.UnitPrice(want))
	}

	if f.unsettled() == 0 {
		var drained int64
		for _, e := range f.usageEntries() {
			drained -= e.AmountMicros
		}
		if drained != queued {
			f.t.Errorf("the ledger drained %s, the queue held %s", billing.UnitPrice(drained), billing.UnitPrice(queued))
		}
	}

	account := f.accountRow()
	lots, err := f.db.Client.CreditLot.Query().Where(creditlot.AccountID(f.account)).All(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	var held int64
	for _, lot := range lots {
		held += lot.RemainingMicros
	}
	if account.BalanceMicros != held-account.ArrearsMicros {
		f.t.Errorf("balance %s ≠ lots %s − arrears %s", billing.UnitPrice(account.BalanceMicros),
			billing.UnitPrice(held), billing.UnitPrice(account.ArrearsMicros))
	}
}

// ---- pricing ----------------------------------------------------------------

// Every band is crossed inside single deliveries, and every player is priced at their position.
func TestPlayersArePricedAtTheirPositionAcrossEveryBand(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	now := time.Now()

	// 7, then 36 (crossing 10 and 50 would need more), then 70 more crossing 50 and 100.
	for _, n := range []int{7, 36, 70} {
		f.handle(a, f.messages(players(n), now))
	}
	f.topUp(billing.Micros)
	f.drain()

	card := billing.Card{Mode: billing.Graduated, Tiers: launch()}
	f.assertBooks(card, 0, 113)
	// 10×500 + 40×400 + 50×350 + 13×300
	if want := int64(10*500 + 40*400 + 50*350 + 13*300); card.Total(113, 0) != want {
		t.Fatalf("the card's own arithmetic moved: %d", card.Total(113, 0))
	}
}

// The plan's free players cost nothing and queue nothing; the first paid one is priced at its
// position.
func TestIncludedPlayersQueueNothing(t *testing.T) {
	f := newFixture(t, billing.Graduated, 20)
	a := f.accruer()

	f.handle(a, f.messages(players(20), time.Now()))
	if n := len(f.charges()); n != 0 {
		t.Fatalf("%d charges queued for included players", n)
	}
	f.handle(a, f.messages(players(5), time.Now()))
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 20, 25)
}

// A price change mid-month applies from its moment: players before it keep the old price.
func TestAPriceChangeAppliesOnlyToPlayersAfterIt(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	clock := time.Now()
	a := f.accruer()
	a.Pricing = &PricingCache{DB: f.db, Cache: f.cache, Now: func() time.Time { return clock }}

	f.handle(a, f.messages(players(5), clock)) // warms every cache with the launch card

	// Published the way the API publishes: no sooner than the horizon, and the plan's shared
	// entry deleted. This process's memory still holds the old plan — that is the point.
	starts := clock.Add(CardHorizon)
	f.publishCard(billing.Graduated, billing.Tiers{{UnitPriceMicros: 1_000}}, starts)
	if err := InvalidatePlan(context.Background(), f.cache, f.plan); err != nil {
		t.Fatal(err)
	}

	// Before the card starts: still the old price, from memory.
	f.handle(a, f.messages(players(5), starts.Add(-time.Second)))
	// After it starts. By then the memory entry is older than LocalTTL, which is shorter than the
	// horizon, so the card is known before it applies.
	clock = starts.Add(time.Second)
	f.handle(a, f.messages(players(5), clock))

	charges := f.charges()
	if len(charges) != 3 || charges[0].AmountMicros != 5*500 || charges[1].AmountMicros != 5*500 ||
		charges[2].AmountMicros != 5*1_000 {
		for _, c := range charges {
			t.Logf("charge %d–%d: %d", c.FirstUnit, c.LastUnit, c.AmountMicros)
		}
		t.Fatal("the old price did not hold until the card started, or the new one after it")
	}
	if charges[0].RateCardID == charges[2].RateCardID {
		t.Error("both prices name the same card")
	}
}

// A price scheduled in advance needs no invalidation at all: the cached plan holds every card, and the
// one in force is chosen by the batch's arrival time, at the exact second it changes.
func TestAScheduledPriceSwitchesOnTheSecondFromACachedPlan(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	switchAt := time.Now().Add(time.Hour).Truncate(time.Second)
	f.publishCard(billing.Graduated, billing.Tiers{{UnitPriceMicros: 1_000}}, switchAt)
	a := f.accruer()

	f.handle(a, f.messages(players(2), switchAt.Add(-time.Second)))
	f.handle(a, f.messages(players(2), switchAt))

	charges := f.charges()
	if len(charges) != 2 || charges[0].AmountMicros != 2*500 || charges[1].AmountMicros != 2*1_000 {
		t.Fatalf("charges = %+v", charges)
	}
	if got := a.Pricing.Stats.Database.Load(); got != 1 {
		t.Errorf("the organization was read from the database %d times, want once", got)
	}
}

// A busy organization is priced from memory: one database read, then none, for as long as the entry
// lives — and a second billing writer is warmed by Valkey rather than by the database.
func TestABusyOrganizationIsPricedFromMemory(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	first := f.accruer()
	for range 20 {
		f.handle(first, f.messages(players(3), time.Now()))
	}
	if db, local := first.Pricing.Stats.Database.Load(), first.Pricing.Stats.Local.Load(); db != 1 || local != 19 {
		t.Errorf("database %d, memory %d; want 1 and 19", db, local)
	}

	second := f.accruer()
	f.handle(second, f.messages(players(3), time.Now()))
	if db, shared := second.Pricing.Stats.Database.Load(), second.Pricing.Stats.Shared.Load(); db != 0 || shared != 1 {
		t.Errorf("a second writer: database %d, valkey %d; want 0 and 1", db, shared)
	}
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 63)
}

// With Valkey gone, pricing still works — from memory and the database.
func TestPricingWorksWithoutValkey(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	a.Cache = nil
	f.handle(a, f.messages(players(4), time.Now()))
	f.handle(a, f.messages(players(4), time.Now()))
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 8)
}

// A volume card crossing into a cheaper band credits the month, and the credit reaches the wallet.
func TestAVolumeCardCreditsTheWalletWhenTheMonthGetsCheaper(t *testing.T) {
	f := newFixture(t, billing.Volume, 0)
	a := f.accruer()
	f.topUp(billing.Micros)

	f.handle(a, f.messages(players(10), time.Now()))
	f.drain()
	before := f.accountRow().BalanceMicros
	f.handle(a, f.messages(players(1), time.Now())) // the 11th reprices all 11 at $0.0004
	f.drain()
	after := f.accountRow().BalanceMicros

	// 10×500 = 5000 paid; 11×400 = 4400 is the month; so the 11th credits 600.
	if after-before != 600 {
		t.Errorf("the 11th player moved the balance by %d micros, want +600", after-before)
	}
	f.assertBooks(billing.Card{Mode: billing.Volume, Tiers: launch()}, 0, 11)
}

// ---- never twice ------------------------------------------------------------

// A redelivered delivery counts nothing: Valkey answers for it, and PostgreSQL agrees.
func TestARedeliveryChargesNothing(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	msgs := f.messages(players(30), time.Now())

	f.handle(a, msgs)
	f.handle(a, msgs)
	f.handle(a, msgs)

	if got := a.Stats.KnownByCache.Load(); got != 60 {
		t.Errorf("Valkey answered for %d players on redelivery, want 60 — PostgreSQL did the work", got)
	}
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 30)
}

// With the cache gone, PostgreSQL alone still refuses to count anybody twice — the cache is speed,
// not correctness.
func TestWithoutTheCacheARedeliveryStillChargesNothing(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	a.Cache = nil
	msgs := f.messages(players(25), time.Now())

	f.handle(a, msgs)
	f.handle(a, msgs)
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 25)
}

// A cache that claims to know nothing — evicted, restarted, or a different node — costs lookups and
// nothing else.
func TestAnEvictedCacheChargesNothingTwice(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	msgs := f.messages(players(12), time.Now())
	f.handle(a, msgs)

	g := &group{orgSeq: f.org, period: PeriodOf(time.Now())}
	var keys []string
	for _, m := range msgs {
		var batch struct {
			InstallID string `json:"install_id"`
		}
		_ = json.Unmarshal(m.Payload, &batch)
		keys = append(keys, a.seenKey(g, Digest(f.key, g.period, f.project, f.env, batch.InstallID)))
	}
	if err := f.cache.Del(context.Background(), keys...); err != nil {
		t.Fatal(err)
	}

	f.handle(a, msgs)
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 12)
}

// Two consumers holding overlapping deliveries at the same time — a Pub/Sub redelivery racing the
// original. Every player is counted once, positions stay contiguous, and the month costs exactly
// what the card says.
func TestConcurrentConsumersCountEachPlayerOnce(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	shared := f.messages(players(40), time.Now())

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := f.accruer()
			if i%2 == 0 {
				a.Cache = nil // half of them cannot even ask the cache
			}
			own := f.messages(players(5), time.Now())
			errs <- a.Handle(context.Background(), append(append([]*eventbus.Message{}, shared...), own...))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a consumer failed: %v", err)
		}
	}
	f.topUp(billing.Micros)
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 40+8*5)
}

// Two drains at once move each charge once. The loser skips the locked account rather than waiting.
func TestConcurrentDrainsSettleEachChargeOnce(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	for range 5 {
		f.handle(a, f.messages(players(9), time.Now()))
	}
	f.topUp(billing.Micros)

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.drainer().Run(context.Background()); err != nil {
				t.Errorf("a drain failed: %v", err)
			}
		}()
	}
	wg.Wait()
	f.drain() // anything a skipped drain left

	if n := f.unsettled(); n != 0 {
		t.Fatalf("%d charges left unsettled", n)
	}
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 45)
}

// ---- never lost -------------------------------------------------------------

// A drain that dies after spending and before committing leaves nothing behind: the wallet did not
// move, the charges are still queued, and the next drain pays them once.
func TestADrainThatDiesMidwayLosesAndDoublesNothing(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	f.handle(a, f.messages(players(20), time.Now()))
	f.topUp(billing.Micros)
	balance := f.accountRow().BalanceMicros

	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settled, err := f.drainer().settle(context.Background(), tx, f.account)
	if err != nil || settled == 0 {
		t.Fatalf("the doomed drain did nothing: %d, %v", settled, err)
	}
	_ = tx.Rollback() // the process died here

	if f.accountRow().BalanceMicros != balance || f.unsettled() == 0 {
		t.Fatal("a rolled-back drain moved money or settled charges")
	}
	f.drain()
	if f.unsettled() != 0 {
		t.Fatal("the next drain did not pay the charges")
	}
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 20)
}

// An account another transaction holds — a top-up in flight — is skipped, not waited on, and its
// charges are paid by the next pass.
func TestALockedAccountIsSkippedAndPaidNextPass(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	f.handle(a, f.messages(players(8), time.Now()))

	holder, err := f.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.BillingAccount.Query().Where(billingaccount.ID(f.account)).ForUpdate().Only(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	f.drain()
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("the drain waited %s on a locked account", waited)
	}
	if f.unsettled() == 0 {
		t.Fatal("the charges were settled while the account was locked")
	}
	_ = holder.Rollback()

	f.drain()
	if f.unsettled() != 0 {
		t.Fatal("the next pass did not pay them")
	}
}

// An empty wallet does not refuse the charge or drop it: the whole month is on the ledger, the
// shortfall is arrears, and the account is in grace until it pays.
func TestAnEmptyWalletOwesTheChargeRatherThanLosingIt(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	f.handle(a, f.messages(players(30), time.Now()))
	f.drain()

	account := f.accountRow()
	want := billing.Card{Mode: billing.Graduated, Tiers: launch()}.Total(30, 0)
	if account.ArrearsMicros != want || account.State != billingaccount.StateGracePeriod {
		t.Errorf("arrears %d, state %s; want %d and grace_period", account.ArrearsMicros, account.State, want)
	}
	f.topUp(billing.Micros)
	if account := f.accountRow(); account.ArrearsMicros != 0 || account.State != billingaccount.StateActive {
		t.Errorf("after a top-up: arrears %d, state %s", account.ArrearsMicros, account.State)
	}
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 30)
}

// A batch nothing could ever price is parked, not dropped and not retried forever — and it does not
// hold back the rest of its delivery.
func TestAnUnpriceableBatchIsParkedAndTheRestAreCharged(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	msgs := f.messages(players(4), time.Now())
	broken := f.messages(players(1), time.Now())[0]
	broken.Payload = []byte("not json")

	f.handle(a, append(msgs, broken))
	if len(f.bus.parked) != 1 || f.bus.parked[0] != broken {
		t.Fatalf("parked %d messages, want the broken one", len(f.bus.parked))
	}
	f.drain()
	f.assertBooks(billing.Card{Mode: billing.Graduated, Tiers: launch()}, 0, 4)
}

// A children's game has no persistent player to count (§98), so its batches are not tracked
// players.
func TestAKidsStrictBatchIsNotATrackedPlayer(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	msgs := f.messages(players(3), time.Now())
	for _, m := range msgs {
		m.PolicyTier = "kids_strict"
	}
	f.handle(a, msgs)
	if _, err := f.db.Client.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(f.account)).Only(context.Background()); !ent.IsNotFound(err) {
		t.Errorf("a kids_strict batch opened a month: %v", err)
	}
}

// An organization that cannot be billed — no account and no default plan, or deleted while its last
// batches were queued — counts nobody, and does not stall the queue. Counting nobody matters: when it
// is given an account, its players are still new rather than silently free.
func TestAnOrganizationThatCannotBeBilledCountsNobodyAndDoesNotStall(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	msgs := f.messages(players(2), time.Now())
	for _, m := range msgs {
		m.OrgSeq = 2_000_000_000 // an organization id nothing will ever have
	}

	// Whether or not some plan is the default: the organization does not exist, so there is
	// nothing to provision — the same path an organization deleted mid-flight takes — and the
	// delivery must not stall on it.
	f.handle(a, msgs)
	if a.Stats.Counted.Load() != 0 || a.Stats.Unbilled.Load() != 2 {
		t.Errorf("counted %d, unbilled %d; want 0 and 2", a.Stats.Counted.Load(), a.Stats.Unbilled.Load())
	}
}

// ---- the prune --------------------------------------------------------------

// The month before the current one is kept; everything older goes.
func TestThePruneKeepsLastMonthAndRemovesOlder(t *testing.T) {
	f := newFixture(t, billing.Graduated, 0)
	a := f.accruer()
	a.Cache = nil
	now := time.Now().UTC()
	f.handle(a, f.messages(players(3), now.AddDate(0, -2, 0)))
	f.handle(a, f.messages(players(3), now.AddDate(0, -1, 0)))
	f.handle(a, f.messages(players(3), now))

	if _, err := (&Pruner{DB: f.db, Chunk: 2}).Run(context.Background()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for months, want := range map[int]int{-2: 0, -1: 3, 0: 3} {
		n, err := f.db.Client.BillablePlayer.Query().Where(
			billableplayer.AccountID(f.account), billableplayer.PeriodStart(PeriodOf(now.AddDate(0, months, 0))),
		).Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%d months back: %d players kept, want %d", -months, n, want)
		}
	}
}

func TestDigestsDoNotLinkAcrossMonthsOrKeys(t *testing.T) {
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	feb := jan.AddDate(0, 1, 0)
	a := Digest([]byte("k1"), jan, "p", "e", "i")
	if b := Digest([]byte("k1"), feb, "p", "e", "i"); string(a) == string(b) {
		t.Error("one player has the same digest in two months")
	}
	if b := Digest([]byte("k2"), jan, "p", "e", "i"); string(a) == string(b) {
		t.Error("two keys produce the same digest")
	}
	if b := Digest([]byte("k1"), jan, "p", "e", "i"); string(a) != string(b) {
		t.Error("a digest is not deterministic")
	}
	if len(a) != DigestSize {
		t.Errorf("digest is %d bytes", len(a))
	}
}

func ratecardMode(mode billing.Mode) ratecard.Mode { return ratecard.Mode(mode) }
