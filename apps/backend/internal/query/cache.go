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
	fmt.Fprintf(&sb, "v%d|p=%d|e=%d|m=%s|b=%s|rb=%s|bk=%s|g=%s|from=%s|to=%s",
		protocol.QuerySchemaVersion, scope.ProjectSeq, scope.EnvironmentSeq,
		q.Metric, q.Basis, q.RetentionBasis, q.Bucket, q.Cohort.Grain,
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
