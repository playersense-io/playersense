package charging

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/testsupport"
)

// TestLoad is production's shape at a smaller clock: many organizations, a million batches, every
// player seen several times, several billing writers consuming at once while the drain and automatic
// top-up run beside them. Opt-in — BILLING_LOAD=1 — because it runs for a minute or two.
//
// It asserts the books, not the speed: every organization's count is exactly its distinct players,
// its charges are exactly what the card says that count costs, its wallet reconciles, and no card was
// charged more than the daily limit. The speed is reported.
//
//	BILLING_LOAD=1 LOAD_ORGS=200 LOAD_PLAYERS=250000 LOAD_REPEATS=4 go test ./internal/charging -run TestLoad -v
func TestLoad(t *testing.T) {
	if os.Getenv("BILLING_LOAD") != "1" {
		t.Skip("set BILLING_LOAD=1 to run the load test")
	}
	orgs := envInt("LOAD_ORGS", 200)
	playerCount := envInt("LOAD_PLAYERS", 250_000)
	repeats := envInt("LOAD_REPEATS", 4)
	writers := envInt("LOAD_WRITERS", 4)
	const delivery = 1000

	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	cache := testsupport.Cache(t, settings)
	ctx := context.Background()

	// A plan with the launch prices in production positions, 1,000 included players, and automatic
	// top-up on every account: $50 whenever the balance is below $10.
	id := uuid.NewString()[:8]
	pl, err := db.Client.Plan.Create().SetKey("load-" + id).SetName("Load " + id).
		SetEntitlements(map[string]int64{string(billing.IncludedMAU): 1_000}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	card := billing.Card{Mode: billing.Graduated, Tiers: billing.Tiers{
		{UpTo: upTo(100_000), UnitPriceMicros: 1_000}, {UpTo: upTo(500_000), UnitPriceMicros: 800},
		{UpTo: upTo(1_000_000), UnitPriceMicros: 700}, {UnitPriceMicros: 600},
	}}
	if _, err := db.Client.RateCard.Create().SetPlanID(pl.ID).SetMeter("tracked_players").
		SetTiers(card.Tiers).SetEffectiveFrom(time.Now().Add(-time.Hour)).SetCreatedBy("load@test").
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	type tenant struct {
		org, account int
		project, env string
		customer     string
		players      []string
	}
	tenants := make([]*tenant, orgs)
	for i := range tenants {
		person, err := identity.EnsureUser(ctx, db.Client, identity.Claims{
			UID: "load-" + uuid.NewString(), Email: "load-" + uuid.NewString()[:12] + "@studio.example",
			EmailVerified: true, Name: "Load", Provider: "google.com",
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		orgList, _ := identity.Organizations(ctx, db.Client, person.ID)
		account := testsupport.SeedBillingAccount(t, db, orgList[0].ID)
		customer := "cus_load_" + uuid.NewString()[:12]
		if _, err := account.Update().SetPlanID(pl.ID).SetStripeCustomerID(customer).
			SetAutoTopupEnabled(true).SetAutoTopupThresholdMicros(10 * billing.Micros).
			SetAutoTopupAmountMicros(50 * billing.Micros).Save(ctx); err != nil {
			t.Fatal(err)
		}
		tenants[i] = &tenant{org: orgList[0].ID, account: account.ID,
			project: uuid.NewString(), env: uuid.NewString(), customer: customer}
		t.Cleanup(func() {
			_, _ = db.Client.BillingAccount.UpdateOneID(account.ID).SetAutoTopupEnabled(false).Save(context.Background())
		})
	}

	// Players are spread unevenly, as real studios are: a few large, many small.
	for i := range playerCount {
		owner := tenants[int(float64(orgs)*rand.Float64()*rand.Float64())]
		owner.players = append(owner.players, "install-"+strconv.Itoa(i)+"-"+id)
	}

	// Every player seen `repeats` times, shuffled, as batches arrive from devices.
	type sighting struct {
		tenant *tenant
		player string
	}
	var sightings []sighting
	for _, tn := range tenants {
		for _, p := range tn.players {
			for range repeats {
				sightings = append(sightings, sighting{tn, p})
			}
		}
	}
	rand.Shuffle(len(sightings), func(i, j int) { sightings[i], sightings[j] = sightings[j], sightings[i] })
	now := time.Now().UTC()
	deliveries := make(chan []*eventbus.Message, 64)
	go func() {
		defer close(deliveries)
		for start := 0; start < len(sightings); start += delivery {
			var msgs []*eventbus.Message
			for _, s := range sightings[start:min(start+delivery, len(sightings))] {
				payload, _ := json.Marshal(map[string]string{"install_id": s.player})
				msgs = append(msgs, &eventbus.Message{
					PublishedAt: now, DedupKey: uuid.NewString(), OrgSeq: uint32(s.tenant.org),
					ProjectID: s.tenant.project, EnvironmentID: s.tenant.env,
					PolicyTier: "standard", EventCount: 5, Payload: payload,
				})
			}
			deliveries <- msgs
		}
	}()

	// The drain and automatic top-up, running beside the writers as they do in billing-writer.
	stripe := paymentstest.New()
	known := map[string]bool{}
	for _, tn := range tenants {
		known[tn.customer] = true
	}
	topUp := &payments.AutoTopUp{DB: db, Gateway: loadOnly{Gateway: stripe, known: known}}
	// As deployed (ADR 0044): an account settles when its oldest charge is five minutes old, or at
	// once when its queue would cross zero or its top-up threshold. The run is shorter than five
	// minutes, so every settlement during it is one of the boundaries.
	drainer := &Drainer{DB: db, MaxAccounts: 1000, SettleAfter: 5 * time.Minute}
	stop := make(chan struct{})
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			_, _ = drainer.Run(ctx)
			_, _ = topUp.Run(ctx)
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()

	key := []byte("load-test-key-" + id + "-0123456789abcdef0123456789")
	var (
		wg      sync.WaitGroup
		handled atomic.Int64
		accs    = make([]*Accruer, writers)
		failed  atomic.Value
	)
	started := time.Now()
	for w := range writers {
		// Each writer is its own process: its own memory, the same Valkey.
		accs[w] = &Accruer{DB: db, Cache: cache, Bus: &deadLetters{}, Key: key, MaxInsert: 1000,
			Pricing: &PricingCache{DB: db, Cache: cache}}
		wg.Add(1)
		go func(a *Accruer) {
			defer wg.Done()
			for msgs := range deliveries {
				for attempt := 0; ; attempt++ {
					err := a.Handle(ctx, msgs)
					if err == nil {
						break
					}
					if attempt > 5 {
						failed.Store(err)
						return
					}
					time.Sleep(50 * time.Millisecond) // a redelivery, as the bus would make
				}
				handled.Add(int64(len(msgs)))
			}
		}(accs[w])
	}
	wg.Wait()
	elapsed := time.Since(started)
	if err, _ := failed.Load().(error); err != nil {
		t.Fatalf("a delivery kept failing: %v", err)
	}

	// Settle what is left — as the clock would five minutes later — top up what is low, then stop
	// the background loops.
	close(stop)
	background.Wait()
	final := &Drainer{DB: db, MaxAccounts: 1000}
	for range 20 {
		if _, err := final.Run(ctx); err != nil {
			t.Logf("drain: %v", err)
		}
		_, _ = topUp.Run(ctx)
	}

	var counted, cached, fromDB, fromValkey, fromMemory int64
	for _, a := range accs {
		counted += a.Stats.Counted.Load()
		cached += a.Stats.KnownByCache.Load()
		fromDB += a.Pricing.Stats.Database.Load()
		fromValkey += a.Pricing.Stats.Shared.Load()
		fromMemory += a.Pricing.Stats.Local.Load()
	}
	t.Logf("%d batches from %d players across %d organizations, %d writers: %s, %.0f batches/s",
		handled.Load(), playerCount, orgs, writers, elapsed.Round(time.Millisecond),
		float64(handled.Load())/elapsed.Seconds())
	t.Logf("new players counted: %d; repeat sightings answered by Valkey without PostgreSQL: %d", counted, cached)
	t.Logf("pricing lookups: memory %d, valkey %d, database %d", fromMemory, fromValkey, fromDB)
	t.Logf("automatic top-ups: %d charged in %d requests", stripe.Charged(), stripe.Calls)

	if counted != int64(playerCount) {
		t.Errorf("counted %d players, there are %d", counted, playerCount)
	}

	// The books, organization by organization.
	var rows struct{ charges, entries, orgs, maxEntries int }
	defer func() {
		if rows.orgs > 0 {
			t.Logf("rows per organization: %.1f queue rows, %.1f ledger lines (at most %d), for %.0f deliveries each",
				float64(rows.charges)/float64(rows.orgs), float64(rows.entries)/float64(rows.orgs),
				rows.maxEntries, float64(handled.Load())/float64(rows.orgs))
		}
	}()
	for _, tn := range tenants {
		if len(tn.players) == 0 {
			continue
		}
		usage, err := db.Client.MonthlyUsage.Query().
			Where(monthlyusage.AccountID(tn.account), monthlyusage.PeriodStart(PeriodOf(now))).Only(ctx)
		if err != nil {
			t.Fatalf("org %d: %v", tn.org, err)
		}
		want := card.Total(int64(len(tn.players)), 1_000)
		if usage.TrackedPlayers != int64(len(tn.players)) || usage.ChargedMicros != want {
			t.Errorf("org %d: %d players charged %s; want %d and %s", tn.org, usage.TrackedPlayers,
				billing.USD(usage.ChargedMicros), len(tn.players), billing.USD(want))
		}
		charges, _ := db.Client.UsageCharge.Query().Where(usagecharge.AccountID(tn.account)).All(ctx)
		var queued int64
		for _, c := range charges {
			queued += c.AmountMicros
		}
		entries, _ := db.Client.LedgerEntry.Query().
			Where(ledgerentry.AccountID(tn.account), ledgerentry.KindEQ(ledgerentry.KindUsage)).All(ctx)
		var drained int64
		for _, e := range entries {
			drained -= e.AmountMicros
		}
		if queued != want || drained != want {
			t.Errorf("org %d: queued %s, ledger drained %s; want %s", tn.org,
				billing.USD(queued), billing.USD(drained), billing.USD(want))
		}
		rows.charges += len(charges)
		rows.entries += len(entries)
		rows.orgs++
		rows.maxEntries = max(rows.maxEntries, len(entries))
		unsettled, _ := db.Client.UsageCharge.Query().
			Where(usagecharge.AccountID(tn.account), usagecharge.SettledAtIsNil()).Count(ctx)
		if unsettled != 0 {
			t.Errorf("org %d: %d charges left in the queue", tn.org, unsettled)
		}
		account, _ := db.Client.BillingAccount.Get(ctx, tn.account)
		lots, _ := db.Client.CreditLot.Query().Where(creditlot.AccountID(tn.account)).All(ctx)
		var held int64
		for _, l := range lots {
			held += l.RemainingMicros
		}
		if account.BalanceMicros != held-account.ArrearsMicros {
			t.Errorf("org %d: balance %d ≠ lots %d − arrears %d", tn.org, account.BalanceMicros, held, account.ArrearsMicros)
		}
		if account.AutoTopupWindowCount > account.AutoTopupDailyLimit {
			t.Errorf("org %d: %d automatic charges, limit %d", tn.org, account.AutoTopupWindowCount, account.AutoTopupDailyLimit)
		}
		topups, _ := db.Client.CreditLot.Query().
			Where(creditlot.AccountID(tn.account), creditlot.StripeReferenceNotNil()).Count(ctx)
		if topups > account.AutoTopupDailyLimit {
			t.Errorf("org %d: %d top-up lots, limit %d", tn.org, topups, account.AutoTopupDailyLimit)
		}
	}
}

// loadOnly is the fake Stripe, answering only for the load test's own customers.
type loadOnly struct {
	*paymentstest.Gateway
	known map[string]bool
}

func (l loadOnly) ChargeableCard(ctx context.Context, customer string) (string, error) {
	if !l.known[customer] {
		return "", nil
	}
	return l.Gateway.ChargeableCard(ctx, customer)
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}
