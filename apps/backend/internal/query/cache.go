package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/valkey"
)

// Cache is §58's short-lived result cache.
//
// Two TTLs, because a cohort answer has two very different lifetimes. A window the nightly
// derivation will rebuild tonight can change, so it is held briefly. A window whose
// activity month closed months ago cannot, so holding it longer costs nothing and saves the
// most expensive repeated query a dashboard makes — the one somebody left open on a
// historical range.
//
// Losing Valkey costs speed and not answers (§65). Every error here is swallowed: a read
// miss is a read, a write failure is a query that was still answered. The alternative would
// make the reporting API less available than the data it reads.
type Cache struct {
	client *valkey.Client

	// Fresh applies while the derivation could still change the answer.
	Fresh time.Duration
	// Settled applies to a window nothing will rebuild.
	Settled time.Duration
}

// NewCache returns a cache over one cell's Valkey.
func NewCache(client *valkey.Client, fresh, settled time.Duration) *Cache {
	if fresh <= 0 {
		fresh = time.Minute
	}
	if settled <= 0 {
		settled = time.Hour
	}
	return &Cache{client: client, Fresh: fresh, Settled: settled}
}

// Get returns a cached answer, or reports a miss.
func (c *Cache) Get(ctx context.Context, key string) (*protocol.CohortResult, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var out protocol.CohortResult
	if err := json.Unmarshal(raw, &out); err != nil {
		// A stored answer this build cannot read is a schema that moved without the
		// version moving with it. Treat it as a miss rather than an error: the query is
		// about to be answered from the facts anyway.
		return nil, false
	}
	return &out, true
}

// Put stores an answer under the lifetime its window deserves.
func (c *Cache) Put(ctx context.Context, key string, result *protocol.CohortResult, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = c.client.Set(ctx, key, raw, ttl)
}

// Key is §58's cache key, hashed.
//
// The list is the document's, in full: org, project, environment, metric, dimensions,
// filters, time range, resolution and the query schema version. Two of those are easy to
// leave out and both are how a cache serves somebody else's data:
//
//   - the **tenant**, without which two projects asking the same question share an answer;
//   - the **schema version**, without which a deployment that changes what a metric means
//     keeps serving the old meaning until every entry expires.
//
// Hashed rather than readable, because the parts include filter values a customer chose and
// a key is a string that ends up in logs and in `KEYS *` output. The cell prefix comes from
// the client, so one cell cannot read another's answers.
func (c *Cache) Key(scope Scope, q protocol.CohortQuery) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "v%d|p=%d|e=%d|m=%s|ev=%s|b=%s|rb=%s|bk=%s|g=%s|from=%s|to=%s",
		protocol.QuerySchemaVersion, scope.ProjectSeq, scope.EnvironmentSeq,
		q.Metric, q.Event, q.Basis, q.RetentionBasis, q.Bucket, q.Cohort.Grain,
		day(q.Cohort.From), day(q.Cohort.To))
	if q.Breakdown != nil {
		fmt.Fprintf(&sb, "|dim=%s", q.Breakdown.Dimension)
	}
	for _, o := range q.Offsets {
		fmt.Fprintf(&sb, "|o=%d", o)
	}
	for _, f := range q.Filters {
		fmt.Fprintf(&sb, "|f=%s%s%s%s", f.Field, f.Op, f.Value, strings.Join(f.In, ","))
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return c.client.Key("cohort", hex.EncodeToString(sum[:16]))
}

// TTL decides how long an answer for this window may be reused.
//
// The derivation rebuilds whole activity months — the current one, and the previous one for
// the first days of a new month (§29.14). So an answer whose activity window reaches into
// either of those can change tonight and is held briefly; anything older cannot change at
// all without somebody running a backfill, and is held for as long as §58 allows.
//
// The activity window is the cohort's last day plus its largest offset, not the cohort's
// last day. A cohort from March read at D180 is describing activity in September.
func (c *Cache) TTL(q protocol.CohortQuery, offsets []int, now time.Time) time.Duration {
	last := 0
	for _, o := range offsets {
		if o > last {
			last = o
		}
	}
	activityEnd := q.Cohort.To.AddDate(0, 0, last)
	rebuildFloor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if !activityEnd.Before(rebuildFloor) {
		return c.Fresh
	}
	return c.Settled
}

// ---- the time-series half ----------------------------------------------------

// SeriesKey is §58's cache key for a time-series question.
//
// A separate namespace from the cohort keys — the prefix differs — so the two shapes cannot
// collide even if their parts happened to hash the same. Everything else is the same list
// and the same reasoning: the tenant and the schema version are in it because leaving either
// out serves somebody another project's numbers or last release's meaning.
func (c *Cache) SeriesKey(scope Scope, q protocol.MetricQuery) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "v%d|p=%d|e=%d|m=%s|i=%s|from=%s|to=%s|ev=%s",
		protocol.QuerySchemaVersion, scope.ProjectSeq, scope.EnvironmentSeq,
		q.Metric, q.Interval, day(q.From), day(q.To), q.EventName)
	for _, g := range q.GroupBy {
		fmt.Fprintf(&sb, "|g=%s", g)
	}
	for _, f := range q.Filters {
		fmt.Fprintf(&sb, "|f=%s%s%s%s", f.Field, f.Op, f.Value, strings.Join(f.In, ","))
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return c.client.Key("series", hex.EncodeToString(sum[:16]))
}

// GetSeries returns a cached series, or reports a miss.
func (c *Cache) GetSeries(ctx context.Context, key string) (*protocol.MetricResult, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var out protocol.MetricResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return &out, true
}

// PutSeries stores a series.
func (c *Cache) PutSeries(ctx context.Context, key string, result *protocol.MetricResult, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = c.client.Set(ctx, key, raw, ttl)
}

// SeriesTTL decides how long a series may be reused.
//
// A realtime tier is never held long, whatever window it covers: the minute and hour views
// update continuously as events arrive, so a minute of cache is already most of what the
// grain resolves. §58 calls for "very short TTLs" on realtime charts by name.
//
// The derived tiers follow the same rule the cohort answers do — a window the nightly job
// will rebuild is held briefly, an older one for as long as §58 allows.
func (c *Cache) SeriesTTL(q protocol.MetricQuery, interval string, now time.Time) time.Duration {
	if interval == "minute" || interval == "hour" {
		return c.Fresh
	}
	rebuildFloor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if !q.To.Before(rebuildFloor) {
		return c.Fresh
	}
	return c.Settled
}

// ---- the explorer's pages ----------------------------------------------------

// pageKey is a cache key for an explorer answer: the kind, the tenant, the schema version and
// the question's canonical parts, hashed for the same reasons Key hashes.
func (c *Cache) pageKey(kind string, scope Scope, parts ...string) string {
	sb := fmt.Sprintf("v%d|p=%d|e=%d|%s", protocol.QuerySchemaVersion, scope.ProjectSeq,
		scope.EnvironmentSeq, strings.Join(parts, "|"))
	sum := sha256.Sum256([]byte(sb))
	return c.client.Key(kind, hex.EncodeToString(sum[:16]))
}

// cached reads an explorer answer, or reports a miss.
func cached[T any](ctx context.Context, c *Cache, key string) (*T, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return &out, true
}

// keep stores an explorer answer.
func keep[T any](ctx context.Context, c *Cache, key string, v *T, ttl time.Duration) {
	if c == nil || c.client == nil {
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = c.client.Set(ctx, key, raw, ttl)
}

// rawTTL is how long an answer read from events_raw may be reused: briefly while its window
// reaches the last two days, where events are still arriving, and for the settled lifetime after.
func (c *Cache) rawTTL(to, now time.Time) time.Duration {
	if !to.Before(now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)) {
		return c.Fresh
	}
	return c.Settled
}
