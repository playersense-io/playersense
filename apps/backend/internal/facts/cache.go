package facts

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"time"

	"playersense.dev/backend/internal/valkey"
)

// Cache is the read-through store the config path uses.
//
// A hit is one Valkey round trip. A miss is one ClickHouse point lookup — 2–3 ms — and the
// answer is written back, so a player pays it once per session rather than once per request.
type Cache struct {
	cache  *valkey.Client
	source Source
	ttl    time.Duration
	absent time.Duration

	// Counted rather than logged: one line per lookup at §4.2's rate is a log nobody can
	// read. The three are kept apart because they mean different things — a miss is the
	// product working, a *failure* is Valkey unreachable, and a source error is ClickHouse
	// unreachable. Adding them together would hide which.
	Hits     atomic.Int64
	Misses   atomic.Int64
	Failures atomic.Int64
	Errors   atomic.Int64
}

// DefaultTTL is a **staleness bound**, not an idle timeout, and that is why it is an hour
// rather than the thirty days the writer's device-state entry uses.
//
// The entry is never renewed on read. That is deliberate: a record cached at the start of a
// long session would otherwise keep being extended and never re-read, so a player who bought
// something would stay a non-payer for as long as they kept playing — which is precisely the
// audience a "buy the starter pack" offer must stop targeting the moment they buy.
//
// An hour costs one 2 ms lookup per player per hour of play. At 5M daily actives that is
// roughly 230 lookups a second against a query measured at 2–3 ms: about half of one
// concurrent query. The cache is not there to spare ClickHouse the load; it is there so that a
// session making several config requests makes one lookup.
const DefaultTTL = time.Hour

// DefaultAbsentTTL is how long "this player has no history" is remembered.
//
// Short, and it is the difference between a cache and a stampede. A brand-new installation has
// no lifetime row until the materialized views have seen its first events, so without a
// negative entry every one of its requests would be a ClickHouse lookup that finds nothing —
// and new installations are exactly the population that requests config most eagerly.
//
// Short because it is also the delay before a genuinely new player becomes targetable: five
// minutes is invisible to a person and bounded for the database.
const DefaultAbsentTTL = 5 * time.Minute

// absentMarker is what a negative entry holds. A single byte, so a negative entry costs the
// key and nothing else.
var absentMarker = []byte("-")

func NewCache(cache *valkey.Client, source Source, ttl, absent time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if absent <= 0 {
		absent = DefaultAbsentTTL
	}
	return &Cache{cache: cache, source: source, ttl: ttl, absent: absent}
}

// key is `<cell>:facts:<subject>:<project>:<environment>:<identifier>`.
//
// The cell prefix comes from the client, so one Valkey serving two cells cannot confuse them.
// The subject is in the key rather than in the value because the two are read and expire
// independently: an account has a record only once somebody signs in.
func (c *Cache) key(projectSeq uint32, environmentSeq uint16, subject, identifier string) string {
	return c.cache.Key("facts", subject,
		strconv.FormatUint(uint64(projectSeq), 10),
		strconv.FormatUint(uint64(environmentSeq), 10),
		identifier)
}

// Get answers from the cache, falling back to the source.
//
// Every failure resolves to "no facts", and the caller treats that the same as a player with
// no history: a rule naming `commerce.*` does not match. That fails **closed** — an unknown
// player is not a whale — which is the only safe direction for an event that grants
// something of value. A cache outage costs reach, never a wrong grant.
func (c *Cache) Get(ctx context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier string) (Record, bool) {
	if identifier == "" {
		return Record{}, false
	}
	key := c.key(projectSeq, environmentSeq, subject, identifier)

	// Get, not GetEx. Renewing on read would extend a remembered absence every time it was
	// read, so a new player would never become targetable — and it would extend a positive
	// record too, turning the staleness bound into an idle timeout. Found by the test that
	// asserts an absence expires.
	raw, err := c.cache.Get(ctx, key)
	switch {
	case err != nil:
		c.Failures.Add(1)
		// Straight to the source. A Valkey outage should cost latency, not reach — the
		// alternative is every LiveOps event in the product quietly stopping.
		return c.fromSource(ctx, projectSeq, environmentSeq, subject, identifier, key, false)

	case len(raw) == 1 && raw[0] == absentMarker[0]:
		// A remembered absence. Still a hit: it saved a lookup.
		c.Hits.Add(1)
		return Record{}, false

	case raw != nil:
		var record Record
		if err := json.Unmarshal(raw, &record); err == nil {
			c.Hits.Add(1)
			return record, true
		}
		// A corrupt entry is a miss and is overwritten below. Refusing the request
		// instead would turn one bad key into an outage.
	}

	c.Misses.Add(1)
	return c.fromSource(ctx, projectSeq, environmentSeq, subject, identifier, key, true)
}

// fromSource reads the authority and writes what it found back.
//
// `store` is false when Valkey is the thing that failed, because writing to a cache that just
// refused a read is a second round trip that will fail the same way.
func (c *Cache) fromSource(ctx context.Context, projectSeq uint32, environmentSeq uint16, subject, identifier, key string, store bool) (Record, bool) {
	record, found, err := c.source.Lookup(ctx, projectSeq, environmentSeq, subject, identifier)
	if err != nil {
		c.Errors.Add(1)
		return Record{}, false
	}
	if !store {
		return record, found
	}

	if !found {
		_ = c.cache.Set(ctx, key, absentMarker, c.absent)
		return Record{}, false
	}
	if encoded, err := json.Marshal(record); err == nil {
		_ = c.cache.Set(ctx, key, encoded, c.ttl)
	}
	return record, true
}
