package query

import (
	"testing"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/valkey"
)

func newCache() *Cache {
	// Never started. Key() touches no connection, and these tests are about what a key is
	// made of rather than about Valkey.
	return NewCache(valkey.New("redis://127.0.0.1:1/0"), time.Minute, time.Hour)
}

func baseQuery() protocol.CohortQuery {
	return protocol.CohortQuery{
		Metric: schemas.MetricRetention, RetentionBasis: schemas.RetentionClassic,
		Cohort:  window(),
		Offsets: []int{0, 1, 7},
	}
}

// TestTheCacheKeyCoversEveryPartOfTheQuestion is §58's list.
//
// Every entry is here because leaving it out serves somebody the wrong answer, and the two
// that matter most are the least obvious: without the tenant, two projects asking the same
// question share a result; without the schema version, a deployment that changes what a
// metric means keeps serving the old meaning until every entry expires.
func TestTheCacheKeyCoversEveryPartOfTheQuestion(t *testing.T) {
	c := newCache()
	base := c.Key(scope(), baseQuery())

	otherProject := scope()
	otherProject.ProjectSeq = 8
	otherEnvironment := scope()
	otherEnvironment.EnvironmentSeq = 4

	differs := map[string]string{
		"another project":     c.Key(otherProject, baseQuery()),
		"another environment": c.Key(otherEnvironment, baseQuery()),
	}
	for name, q := range map[string]func(protocol.CohortQuery) protocol.CohortQuery{
		"another metric": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.Metric = schemas.MetricRevenue
			q.RetentionBasis = ""
			return q
		},
		"another basis": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.RetentionBasis = schemas.RetentionRolling
			return q
		},
		"another grain": func(q protocol.CohortQuery) protocol.CohortQuery { q.Cohort.Grain = schemas.GrainWeek; return q },
		"another range": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.Cohort.To = protocol.NewDay(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC))
			return q
		},
		"another offset set": func(q protocol.CohortQuery) protocol.CohortQuery { q.Offsets = []int{0, 1, 30}; return q },
		"a breakdown": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.Breakdown = &protocol.Breakdown{Dimension: "install_country"}
			return q
		},
		"a filter": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.Filters = []protocol.Filter{{Field: "install_platform", Op: "eq", Value: "ios"}}
			return q
		},
		"a different filter value": func(q protocol.CohortQuery) protocol.CohortQuery {
			q.Filters = []protocol.Filter{{Field: "install_platform", Op: "eq", Value: "android"}}
			return q
		},
	} {
		differs[name] = c.Key(scope(), q(baseQuery()))
	}

	seen := map[string]string{base: "the base question"}
	for name, key := range differs {
		if key == base {
			t.Errorf("%s produces the same cache key as the base question", name)
		}
		if other, clash := seen[key]; clash {
			t.Errorf("%s collides with %s", name, other)
		}
		seen[key] = name
	}

	// The same question twice is the same key, or the cache never hits.
	if c.Key(scope(), baseQuery()) != base {
		t.Fatal("the same question produced two different keys")
	}
}

// TestASettledWindowIsHeldLongerThanAFreshOne is §58's two lifetimes.
//
// The derivation rebuilds whole activity months — the current one, and the previous one
// early in a new month (§29.14) — so an answer whose *activity* reaches into either can
// change tonight. Anything older cannot change without somebody running a backfill.
func TestASettledWindowIsHeldLongerThanAFreshOne(t *testing.T) {
	c := newCache()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	recent := baseQuery()
	recent.Cohort.To = protocol.NewDay(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	if got := c.TTL(recent, []int{0, 1}, now); got != c.Fresh {
		t.Fatalf("a window the derivation rebuilds tonight is held for %s", got)
	}

	old := baseQuery()
	old.Cohort.To = protocol.NewDay(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if got := c.TTL(old, []int{0, 1}, now); got != c.Settled {
		t.Fatalf("a window from February is held for only %s", got)
	}

	// A February cohort read at D180 describes activity in August, which the derivation
	// may still touch. The cohort's own date is not what decides this.
	reachesForward := old
	if got := c.TTL(reachesForward, []int{0, 180, 365}, now); got != c.Fresh {
		t.Fatalf("an old cohort read at D365 was held for %s despite describing recent activity", got)
	}
}
