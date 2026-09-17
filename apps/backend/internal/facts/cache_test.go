package facts_test

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/testsupport"
)

// freshProject returns a tenant surrogate no previous run has used.
//
// Valkey outlives a test run, so a fixed project and identifier would let one run's cached
// record answer the next run's first read — and a test that passes because of what the
// previous run left behind is a test that will pass when the code is broken.
func freshProject() uint32 { return uint32(rand.Int31n(1_000_000) + 5_000_000) }

// cacheFixture wires the read-through cache to a fake source and real Valkey.
//
// Real Valkey rather than a fake, because what is being tested is the caching — the TTLs, the
// negative entry, the renewal — and a fake cache would only prove the fake works.
func cacheFixture(t *testing.T, absent time.Duration) (*facts.Cache, *facts.Memory) {
	t.Helper()
	settings := testsupport.Settings(t)
	client := testsupport.Cache(t, settings)
	source := facts.NewMemory()
	return facts.NewCache(client, source, time.Minute, absent), source
}

// The property the whole design rests on: a hit costs one round trip and never reaches
// ClickHouse.
func TestASecondRequestDoesNotReachTheSource(t *testing.T) {
	cache, source := cacheFixture(t, time.Minute)
	project := freshProject()
	player := "whale"

	if err := source.Put(t.Context(), project, 1, facts.SubjectPlayer, player,
		facts.Record{SpendMicros: 150_000_000, Purchases: 9}); err != nil {
		t.Fatal(err)
	}

	first, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player)
	if !found || first.SpendMicros != 150_000_000 {
		t.Fatalf("first read = %+v, found %v", first, found)
	}
	if source.Lookups != 1 {
		t.Fatalf("%d source lookups on a cold cache, want 1", source.Lookups)
	}

	second, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player)
	if !found || second.SpendMicros != 150_000_000 {
		t.Fatalf("second read = %+v, found %v", second, found)
	}
	if source.Lookups != 1 {
		t.Errorf("%d source lookups after a hit, want the first one only", source.Lookups)
	}
	if cache.Hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", cache.Hits.Load())
	}
}

// A brand-new installation has no lifetime row until the materialized views have seen its
// first events — and new installations are the population that requests config most eagerly.
// Without a remembered absence, every one of those requests is a ClickHouse lookup that finds
// nothing.
func TestAnAbsenceIsRememberedSoNewPlayersDoNotStampede(t *testing.T) {
	cache, source := cacheFixture(t, time.Minute)
	project := freshProject()
	player := "newcomer"

	for i := range 5 {
		if _, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player); found {
			t.Fatalf("request %d found a record for a player with no history", i)
		}
	}
	if source.Lookups != 1 {
		t.Errorf("%d source lookups for five requests, want 1 — the absence was not remembered",
			source.Lookups)
	}
}

// And the absence must expire, or a player who has just started playing stays untargetable.
func TestARememberedAbsenceExpires(t *testing.T) {
	cache, source := cacheFixture(t, 50*time.Millisecond)
	project := freshProject()
	player := "arriving"

	if _, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player); found {
		t.Fatal("found a record before there was one")
	}

	// Their first events land and the aggregates catch up.
	if err := source.Put(t.Context(), project, 1, facts.SubjectPlayer, player,
		facts.Record{Purchases: 1}); err != nil {
		t.Fatal(err)
	}

	// Still remembered as absent, so nothing changes yet.
	if _, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player); found {
		t.Error("the absence was not remembered at all")
	}

	time.Sleep(120 * time.Millisecond)
	if _, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, player); !found {
		t.Error("the absence outlived its TTL; a new player would stay untargetable")
	}
}

// A source outage must not become a wrong grant. An unknown player is not a whale.
func TestASourceOutageFailsClosed(t *testing.T) {
	cache, source := cacheFixture(t, time.Minute)
	project := freshProject()
	source.Err = errors.New("clickhouse is down")

	record, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, "anybody")
	if found {
		t.Error("a record was found while the source was failing")
	}
	if record.SpendMicros != 0 {
		t.Errorf("a failing source produced %+v", record)
	}
	if cache.Errors.Load() == 0 {
		t.Error("the failure was not counted")
	}

	// And nothing was cached, so the next request tries again rather than remembering a
	// failure as an absence.
	source.Err = nil
	if err := source.Put(t.Context(), project, 1, facts.SubjectPlayer, "anybody",
		facts.Record{Purchases: 3}); err != nil {
		t.Fatal(err)
	}
	if _, found := cache.Get(t.Context(), project, 1, facts.SubjectPlayer, "anybody"); !found {
		t.Error("a transient source failure was cached as an absence")
	}
}

// An empty identifier is an anonymous player asking about their account. There is no account,
// and asking the source would be a lookup guaranteed to find nothing.
func TestAnAnonymousPlayerIsNotLookedUp(t *testing.T) {
	cache, source := cacheFixture(t, time.Minute)
	if _, found := cache.Get(t.Context(), freshProject(), 1, facts.SubjectPlayer, ""); found {
		t.Error("found an account for a player with no identifier")
	}
	if source.Lookups != 0 {
		t.Errorf("%d source lookups for an empty identifier, want 0", source.Lookups)
	}
}
