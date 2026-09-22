package charging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/plan"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// Pricing is everything the accrual needs to price one organization's players, without asking the
// database (ADR 0041).
//
// # Two entries, because they change for different reasons
//
//	billing:org:<seq>   the account, its currency, its plan and its overrides — changes when the
//	                    organization is moved to another plan
//	billing:plan:<id>   the plan's included players and every rate card it has ever had — changes
//	                    when the plan is edited or a card is published
//
// A plan is shared by thousands of organizations, so a price change is one key to invalidate, not
// thousands.
//
// # Three levels, fastest first
//
//	this process   30 seconds    no network at all for a busy organization
//	Valkey         10 minutes    shared by every billing writer and survives a restart
//	PostgreSQL     the truth     read only on a miss
//
// Writes delete the Valkey entries. Another process's memory keeps a stale entry for at most
// LocalTTL, which is why a rate card cannot start sooner than CardHorizon from when it is published:
// by the time it applies, every charger has already seen it. And because the plan entry holds every
// card — past, current and scheduled — a scheduled price change needs no invalidation at all: the
// card in force is chosen in memory, by the batch's arrival time, at the exact second it changes.
//
// Losing Valkey costs speed, never correctness (§65): a failed read falls through to PostgreSQL.
type Pricing struct {
	AccountID int           `json:"account_id"`
	Currency  string        `json:"currency"`
	PlanID    int           `json:"plan_id"`
	Included  int64         `json:"included"`
	Cards     []PricingCard `json:"cards"`
}

// PricingCard is one rate card as the accrual uses it.
type PricingCard struct {
	ID            int           `json:"id"`
	EffectiveFrom time.Time     `json:"effective_from"`
	Mode          billing.Mode  `json:"mode"`
	Currency      string        `json:"currency"`
	Tiers         billing.Tiers `json:"tiers"`
}

// CardAt is the card in force at an instant: the latest to have started. Nil when none has.
func (p *Pricing) CardAt(at time.Time) *PricingCard {
	var found *PricingCard
	for i := range p.Cards {
		if p.Cards[i].EffectiveFrom.After(at) {
			break
		}
		found = &p.Cards[i]
	}
	return found
}

// The cache's timings.
const (
	LocalTTL  = 30 * time.Second
	SharedTTL = 10 * time.Minute

	// CardHorizon is how soon a newly published card may start. Longer than LocalTTL, so no
	// charger can still be holding a plan entry that lacks the card when the card applies.
	CardHorizon = time.Minute
)

// orgEntry and planEntry are what is stored.
type orgEntry struct {
	AccountID int              `json:"account_id"`
	Currency  string           `json:"currency"`
	PlanID    int              `json:"plan_id"`
	Overrides map[string]int64 `json:"overrides,omitempty"`
}

type planEntry struct {
	Entitlements map[string]int64 `json:"entitlements,omitempty"`
	Cards        []PricingCard    `json:"cards"`
}

// PricingCache serves Pricing from memory, then Valkey, then PostgreSQL.
type PricingCache struct {
	DB    *pgdb.DB
	Cache *valkey.Client // nil: memory and PostgreSQL only
	Now   func() time.Time

	mu    sync.Mutex
	orgs  map[int]local[orgEntry]
	plans map[int]local[planEntry]

	Stats PricingStats
}

// PricingStats says where answers came from, which is how a load test tells a warm cache from a
// cold one.
type PricingStats struct {
	Local    atomic.Int64
	Shared   atomic.Int64
	Database atomic.Int64
}

type local[T any] struct {
	value   T
	expires time.Time
}

// maxLocal bounds each in-process map. Past it the map is cleared rather than evicted entry by entry:
// the next reads refill it from Valkey, which is cheap, and the bound is about memory, not hit rate.
const maxLocal = 200_000

// ErrNoAccount is an organization with no billing account.
var ErrNoAccount = errors.New("the organization has no billing account")

// Get returns an organization's pricing.
func (c *PricingCache) Get(ctx context.Context, orgSeq int) (*Pricing, error) {
	org, err := c.org(ctx, orgSeq)
	if err != nil {
		return nil, err
	}
	pl, err := c.plan(ctx, org.PlanID)
	if err != nil {
		return nil, err
	}
	limits := billing.Limits{}
	for name, amount := range pl.Entitlements {
		limits[billing.Entitlement(name)] = amount
	}
	overrides := billing.Limits{}
	for name, amount := range org.Overrides {
		overrides[billing.Entitlement(name)] = amount
	}
	included, _ := billing.Resolve(limits, overrides).Get(billing.IncludedMAU)
	return &Pricing{
		AccountID: org.AccountID, Currency: org.Currency, PlanID: org.PlanID,
		Included: included, Cards: pl.Cards,
	}, nil
}

func (c *PricingCache) org(ctx context.Context, orgSeq int) (orgEntry, error) {
	now := c.now()
	c.mu.Lock()
	if hit, found := c.orgs[orgSeq]; found && now.Before(hit.expires) {
		c.mu.Unlock()
		c.Stats.Local.Add(1)
		return hit.value, nil
	}
	c.mu.Unlock()

	key := OrgKey(c.Cache, orgSeq)
	var entry orgEntry
	if c.fromShared(ctx, key, &entry) {
		c.Stats.Shared.Add(1)
		c.remember(func() { c.orgs = put(c.orgs, orgSeq, entry, now) })
		return entry, nil
	}

	account, err := c.DB.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(orgSeq)).Only(ctx)
	if ent.IsNotFound(err) {
		// Not remembered: an account can be provisioned at any moment, and a cached "none"
		// would leave its players unbilled until it expired.
		return orgEntry{}, ErrNoAccount
	}
	if err != nil {
		return orgEntry{}, fmt.Errorf("read the billing account: %w", err)
	}
	c.Stats.Database.Add(1)
	entry = orgEntry{AccountID: account.ID, Currency: account.Currency, PlanID: account.PlanID,
		Overrides: account.EntitlementOverrides}
	c.toShared(ctx, key, entry)
	c.remember(func() { c.orgs = put(c.orgs, orgSeq, entry, now) })
	return entry, nil
}

func (c *PricingCache) plan(ctx context.Context, planID int) (planEntry, error) {
	now := c.now()
	c.mu.Lock()
	if hit, found := c.plans[planID]; found && now.Before(hit.expires) {
		c.mu.Unlock()
		return hit.value, nil
	}
	c.mu.Unlock()

	key := PlanKey(c.Cache, planID)
	var entry planEntry
	if c.fromShared(ctx, key, &entry) {
		c.remember(func() { c.plans = put(c.plans, planID, entry, now) })
		return entry, nil
	}

	row, err := c.DB.Client.Plan.Query().
		Where(plan.ID(planID)).
		WithRateCards(func(q *ent.RateCardQuery) {
			q.Where(ratecard.MeterEQ(ratecard.Meter(billing.TrackedPlayers)))
		}).
		Only(ctx)
	if err != nil {
		return planEntry{}, fmt.Errorf("read the plan: %w", err)
	}
	entry = planEntry{Entitlements: row.Entitlements}
	for _, card := range row.Edges.RateCards {
		entry.Cards = append(entry.Cards, PricingCard{
			ID: card.ID, EffectiveFrom: card.EffectiveFrom.UTC(), Mode: billing.Mode(card.Mode),
			Currency: card.Currency, Tiers: card.Tiers,
		})
	}
	sort.Slice(entry.Cards, func(i, j int) bool {
		return entry.Cards[i].EffectiveFrom.Before(entry.Cards[j].EffectiveFrom)
	})
	c.toShared(ctx, key, entry)
	c.remember(func() { c.plans = put(c.plans, planID, entry, now) })
	return entry, nil
}

func (c *PricingCache) fromShared(ctx context.Context, key string, into any) bool {
	if c.Cache == nil || key == "" {
		return false
	}
	raw, err := c.Cache.Get(ctx, key)
	if err != nil || raw == nil {
		return false
	}
	return json.Unmarshal(raw, into) == nil
}

func (c *PricingCache) toShared(ctx context.Context, key string, value any) {
	if c.Cache == nil || key == "" {
		return
	}
	if raw, err := json.Marshal(value); err == nil {
		_ = c.Cache.Set(ctx, key, raw, SharedTTL) // best effort: a miss next time is only slower
	}
}

func (c *PricingCache) remember(write func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.orgs == nil {
		c.orgs = map[int]local[orgEntry]{}
	}
	if c.plans == nil {
		c.plans = map[int]local[planEntry]{}
	}
	write()
}

// Forget drops this process's copies, for a test or for the process that made the change.
func (c *PricingCache) Forget(orgSeq, planID int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.orgs, orgSeq)
	delete(c.plans, planID)
}

func put[T any](m map[int]local[T], key int, value T, now time.Time) map[int]local[T] {
	if len(m) >= maxLocal {
		m = map[int]local[T]{}
	}
	m[key] = local[T]{value: value, expires: now.Add(LocalTTL)}
	return m
}

func (c *PricingCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// OrgKey and PlanKey name the shared entries. Every writer that changes what they hold deletes them.
func OrgKey(cache *valkey.Client, orgSeq int) string {
	if cache == nil {
		return ""
	}
	return cache.Key("billing", "pricing", "org", strconv.Itoa(orgSeq))
}

func PlanKey(cache *valkey.Client, planID int) string {
	if cache == nil {
		return ""
	}
	return cache.Key("billing", "pricing", "plan", strconv.Itoa(planID))
}

// InvalidateOrganization is called after an organization moves plans or its overrides change.
func InvalidateOrganization(ctx context.Context, cache *valkey.Client, orgSeq int) error {
	if cache == nil {
		return nil
	}
	return cache.Del(ctx, OrgKey(cache, orgSeq))
}

// InvalidatePlan is called after a plan's allowances change or a card is published for it.
func InvalidatePlan(ctx context.Context, cache *valkey.Client, planID int) error {
	if cache == nil {
		return nil
	}
	return cache.Del(ctx, PlanKey(cache, planID))
}
