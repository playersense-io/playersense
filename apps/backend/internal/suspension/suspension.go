// Package suspension stops ingestion for accounts that owe too much, or for too long (ADR 0043).
//
// # A bounded tolerance
//
// An account that owes goes into its grace period (ADR 0039) and keeps working: a card that expired on
// a Saturday is not a reason to lose a studio's live data. But every accepted event costs compute and
// storage, and owing does not grow with events — only new players are charged — so an account could
// send unlimited events from players it already has while its debt stood still. So the tolerance has
// two bounds, and whichever comes first suspends ingestion:
//
//	time   the grace period, counted from when the account first owed (BILLING_GRACE_PERIOD)
//	money  the credit limit, a plan allowance (credit_limit_micros) with a deployment default
//
// # What suspension does, and does not
//
// The SDK's batches are answered 429 with Retry-After. The SDK keeps them on the device and tries
// again later (protocol §9; sdk/core/src/transport.rs `a_429_honours_retry_after`), so nothing is lost
// while the studio pays — within the SDK's seven-day spool. Nothing already stored is deleted, the
// game's configuration endpoint keeps answering, and the dashboard keeps working. Paying the debt makes
// the account active again (wallet), and ingestion resumes within a minute or so.
//
// # Not a database read per request
//
// The database decides; Valkey carries the answer to the ingestion path. The job marks every
// suspended organization in Valkey with a short lifetime and re-marks them every pass, and the gate
// remembers each organization's answer in memory for a few seconds. So a request costs a map lookup,
// and one Valkey round trip per organization per GateTTL. If Valkey is unreachable the gate lets
// batches in: a Valkey outage must not become an outage for every customer, and the cost of a few
// minutes' unbilled ingestion from a handful of suspended accounts is the smaller one.
package suspension

import (
	"context"
	"strconv"
	"sync"
	"time"

	"playersense.dev/backend/internal/valkey"
)

// Timings.
const (
	// GateTTL is how long the ingestion path trusts its memory of an organization's answer.
	GateTTL = 15 * time.Second

	// MarkTTL is how long a mark lives in Valkey without being renewed. Longer than two job
	// passes, so a live job never lets one lapse; short enough that a dead job fails open soon,
	// and that an account which paid is unmarked within this even if nothing deletes its mark.
	MarkTTL = 90 * time.Second

	// RetryAfter is what a refused SDK is told: try again in an hour. The SDK keeps the batch.
	RetryAfter = time.Hour
)

// Key is the Valkey mark for a suspended organization.
func Key(cache *valkey.Client, orgSeq uint32) string {
	return cache.Key("billing", "suspended", strconv.FormatUint(uint64(orgSeq), 10))
}

// Gate answers "is this organization suspended" for the ingestion path.
type Gate struct {
	Cache *valkey.Client
	Now   func() time.Time

	mu      sync.Mutex
	answers map[uint32]answer
}

type answer struct {
	suspended bool
	until     time.Time
}

// Suspended reports whether an organization's batches are to be refused.
func (g *Gate) Suspended(ctx context.Context, orgSeq uint32) bool {
	if g == nil || g.Cache == nil {
		return false
	}
	now := g.now()
	g.mu.Lock()
	if a, found := g.answers[orgSeq]; found && now.Before(a.until) {
		g.mu.Unlock()
		return a.suspended
	}
	g.mu.Unlock()

	suspended, err := g.Cache.Exists(ctx, Key(g.Cache, orgSeq))
	if err != nil {
		return false // fail open; see the package comment
	}
	g.mu.Lock()
	if g.answers == nil || len(g.answers) > 500_000 {
		g.answers = map[uint32]answer{}
	}
	g.answers[orgSeq] = answer{suspended: suspended, until: now.Add(GateTTL)}
	g.mu.Unlock()
	return suspended
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}
