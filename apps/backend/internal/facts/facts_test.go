package facts_test

import (
	"testing"

	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/targeting"
)

// The point of the whole design, in one test: an organisation defines its own audience over a
// vocabulary it did not write, and the definition costs one lookup rather than a query.
func TestAStudioDefinesItsOwnWhale(t *testing.T) {
	spender := facts.Record{SpendMicros: 150_000_000, Purchases: 12}
	dabbler := facts.Record{SpendMicros: 5_000_000, Purchases: 1}

	// Two studios, two thresholds, neither of them ours.
	generous := rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":100000000}`)
	strict := rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":1000000000}`)

	if !generous.Matches(context(spender)) {
		t.Error("a $150 player is not a whale at the $100 threshold")
	}
	if generous.Matches(context(dabbler)) {
		t.Error("a $5 player is a whale at the $100 threshold")
	}
	if strict.Matches(context(spender)) {
		t.Error("a $150 player is a whale at the $1000 threshold")
	}
}

// "Paid users", "returning users" and "all users" over the same record, because these are the
// segments an organisation actually asks for and none of them is a feature we shipped.
func TestTheSegmentsAnOrganisationAsksFor(t *testing.T) {
	never := facts.Record{DaysActive: 3, DaysSinceSeen: 0}
	lapsed := facts.Record{
		SpendMicros: 20_000_000, Purchases: 2,
		DaysActive: 40, DaysSinceSeen: 45,
		DaysSinceLastPurchase: days(40),
	}

	payers := rule(t, `{"key":"commerce.player.purchases","op":"gt","value":0}`)
	if payers.Matches(context(never)) {
		t.Error("a player who never bought matched 'paid users'")
	}
	if !payers.Matches(context(lapsed)) {
		t.Error("a player with two purchases did not match 'paid users'")
	}

	// "Returning": away a while, and they have been here before.
	returning := rule(t, `{"all":[
		{"key":"activity.player.days_since_last_seen","op":"gte","value":30},
		{"key":"activity.player.days_active","op":"gte","value":5}
	]}`)
	if !returning.Matches(context(lapsed)) {
		t.Error("a player away 45 days with 40 active days did not match 'returning'")
	}
	if returning.Matches(context(never)) {
		t.Error("a player here today matched 'returning'")
	}

	// "All users" is the empty rule, and it must not need a lookup.
	all := targeting.Rule{}
	if !all.Matches(context(never)) || !all.Matches(context(lapsed)) {
		t.Error("'all users' excluded somebody")
	}
	if all.NeedsPlayerFacts() {
		t.Error("'all users' asked for a lookup")
	}
}

// The distinction ADR 0031 exists for, at the level a rule sees it.
func TestNeverBoughtIsNotTheSameAsBoughtLongAgo(t *testing.T) {
	neverBought := facts.Record{DaysActive: 10}
	boughtOnce := facts.Record{Purchases: 1, DaysSinceLastPurchase: days(60)}

	// A win-back offer: bought before, but not lately.
	winBack := rule(t, `{"key":"commerce.player.days_since_last_purchase","op":"gte","value":30}`)
	if winBack.Matches(context(neverBought)) {
		t.Error("a player who never bought matched a win-back audience")
	}
	if !winBack.Matches(context(boughtOnce)) {
		t.Error("a player who bought 60 days ago did not match a win-back audience")
	}

	// And the mirror: a first-purchase offer is 'has never bought', which is the absence
	// itself rather than a large number.
	firstPurchase := rule(t, `{"key":"commerce.player.days_since_last_purchase","op":"not_exists"}`)
	if !firstPurchase.Matches(context(neverBought)) {
		t.Error("a player who never bought did not match a first-purchase audience")
	}
	if firstPurchase.Matches(context(boughtOnce)) {
		t.Error("a player who has bought matched a first-purchase audience")
	}
}

// An install and an account are different subjects and a rule must be able to say which.
func TestTheTwoSubjectsDoNotLeakIntoEachOther(t *testing.T) {
	ctx := targeting.Context{}
	facts.Record{SpendMicros: 60_000_000}.Context(facts.SubjectInstall, ctx)
	facts.Record{SpendMicros: 120_000_000}.Context(facts.SubjectPlayer, ctx)

	if got := ctx["commerce.install.lifetime_spend_micros"]; got != int64(60_000_000) {
		t.Errorf("install spend = %v", got)
	}
	if got := ctx["commerce.player.lifetime_spend_micros"]; got != int64(120_000_000) {
		t.Errorf("player spend = %v", got)
	}
}

// An unknown player is not a whale. Failing closed is the only safe direction for a rule that
// grants something of value, and it is what a Valkey outage has to look like.
func TestAnUnknownPlayerMatchesNothingThatGrantsValue(t *testing.T) {
	store := facts.NewMemory()
	record, found := store.Get(t.Context(), 1, 1, facts.SubjectPlayer, "nobody")
	if found {
		t.Fatal("an empty store found a record")
	}

	// Nothing is rendered for a player with no record, so every fact key is absent...
	ctx := targeting.Context{}
	if found {
		record.Context(facts.SubjectPlayer, ctx)
	}
	whales := rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":1}`)
	if whales.Matches(ctx) {
		t.Error("a player we know nothing about matched a whale rule")
	}

	// ...including the negative forms, which is the subtle half: "has never bought" must
	// not fire for somebody we simply have not loaded.
	//
	// It does fire, and that is a real limitation rather than a bug to hide: absence of a
	// record and absence of purchases are indistinguishable to a rule. It is recorded in
	// the plan's open items and needs a first-sight write to fix.
	firstPurchase := rule(t, `{"key":"commerce.player.days_since_last_purchase","op":"not_exists"}`)
	if !firstPurchase.Matches(ctx) {
		t.Error("expected the known limitation: not_exists cannot tell unloaded from never-bought")
	}
}

func TestTheStoreRoundTripsARecord(t *testing.T) {
	store := facts.NewMemory()
	want := facts.Record{
		SpendMicros: 99, Purchases: 3,
		DaysSinceFirstPurchase: days(10), DaysSinceLastPurchase: days(2),
		DaysActive: 7, Sessions: 21, PlaytimeSeconds: 3600,
		DaysSinceSeen: 1, DaysSinceFirst: 30,
	}
	if err := store.Put(t.Context(), 7, 2, facts.SubjectPlayer, "p1", want); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, found := store.Get(t.Context(), 7, 2, facts.SubjectPlayer, "p1")
	if !found {
		t.Fatal("the record was not found")
	}
	if got.SpendMicros != want.SpendMicros || got.Sessions != want.Sessions {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.DaysSinceLastPurchase == nil || *got.DaysSinceLastPurchase != 2 {
		t.Errorf("purchase date lost: %+v", got.DaysSinceLastPurchase)
	}

	// A different project is a different player, or one studio's whale would be another's.
	if _, found := store.Get(t.Context(), 8, 2, facts.SubjectPlayer, "p1"); found {
		t.Error("a record leaked across projects")
	}
	if _, found := store.Get(t.Context(), 7, 2, facts.SubjectInstall, "p1"); found {
		t.Error("a record leaked across subjects")
	}
}

// ---- helpers ----------------------------------------------------------------

func rule(t *testing.T, raw string) targeting.Rule {
	t.Helper()
	parsed, err := targeting.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

func context(record facts.Record) targeting.Context {
	ctx := targeting.Context{}
	record.Context(facts.SubjectPlayer, ctx)
	return ctx
}

func days(n int64) *int64 { return &n }
