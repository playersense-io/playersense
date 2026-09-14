package core

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/apikey"
)

// Tenant is everything ingestion needs to accept a batch, resolved once from the key.
//
// It carries the integer surrogates as well as the UUIDs because the writer stamps both
// onto every row: the surrogates are what ClickHouse sorts on (§23.1) and the UUIDs are
// what the outside world sees.
type Tenant struct {
	OrgSeq         uint32
	ProjectSeq     uint32
	EnvironmentSeq uint16

	OrgID         string
	ProjectID     string
	EnvironmentID string

	Environment   string
	PolicyTier    string
	RetentionTier uint16

	KeyKind   string
	KeyPrefix string
}

// ErrUnknownKey reports a credential that resolves to no environment.
var ErrUnknownKey = errors.New("unknown api key")

type cacheEntry struct {
	tenant  *Tenant
	err     error
	expires time.Time
}

// KeyResolver turns an API key into a Tenant, with a cache in front.
//
// The cache is not an optimisation. §64 requires ingestion to keep accepting batches
// through a PostgreSQL outage, and §3.2 forbids the ingestion path from querying the
// control plane per request at all — a gateway that needs a database to answer is a
// gateway that stops when the database does.
//
// Failures are cached too, for a shorter window. A publishable key is assumed
// discoverable (§71), so a flood of requests bearing a wrong one is an expected shape of
// traffic rather than a surprise, and each of them must not become a query.
type KeyResolver struct {
	control  *Control
	ttl      time.Duration
	negTTL   time.Duration
	maxEntry int

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewKeyResolver returns a resolver over the control plane.
func NewKeyResolver(control *Control, ttl, negativeTTL time.Duration, maxEntries int) *KeyResolver {
	return &KeyResolver{
		control:  control,
		ttl:      ttl,
		negTTL:   negativeTTL,
		maxEntry: maxEntries,
		cache:    make(map[string]cacheEntry),
	}
}

// Resolve looks a key up, consulting the cache first.
func (r *KeyResolver) Resolve(ctx context.Context, secret string) (*Tenant, error) {
	hash, err := HashAPIKey(secret)
	if err != nil {
		return nil, ErrUnknownKey
	}
	cacheKey := hex.EncodeToString(hash)

	r.mu.RLock()
	entry, ok := r.cache[cacheKey]
	r.mu.RUnlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.tenant, entry.err
	}

	tenant, lookupErr := r.lookup(ctx, hash)
	r.store(cacheKey, tenant, lookupErr)
	return tenant, lookupErr
}

func (r *KeyResolver) lookup(ctx context.Context, hash []byte) (*Tenant, error) {
	key, err := r.control.Global.APIKey.Query().
		Where(apikey.Hash(hash)).
		WithEnvironment(func(q *global.EnvironmentQuery) {
			q.WithProject(func(pq *global.ProjectQuery) { pq.WithOrganization() })
		}).
		Only(ctx)
	if err != nil {
		if global.IsNotFound(err) {
			return nil, ErrUnknownKey
		}
		return nil, err
	}
	if key.RevokedAt != nil {
		return nil, ErrUnknownKey
	}

	env := key.Edges.Environment
	if env == nil || env.Edges.Project == nil || env.Edges.Project.Edges.Organization == nil {
		return nil, ErrUnknownKey
	}
	proj := env.Edges.Project
	org := proj.Edges.Organization

	return &Tenant{
		OrgSeq:         uint32(org.ID),
		ProjectSeq:     uint32(proj.ID),
		EnvironmentSeq: uint16(env.ID),

		OrgID:         org.UID.String(),
		ProjectID:     proj.UID.String(),
		EnvironmentID: env.UID.String(),

		Environment:   string(env.Kind),
		PolicyTier:    string(proj.PolicyTier),
		RetentionTier: proj.RetentionDays,

		KeyKind:   string(key.Kind),
		KeyPrefix: key.Prefix,
	}, nil
}

func (r *KeyResolver) store(cacheKey string, tenant *Tenant, err error) {
	ttl := r.ttl
	if err != nil {
		ttl = r.negTTL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The table is keyed by something a client influences, so it is bounded. A full
	// table is swept rather than grown: an unbounded cache keyed on attacker input is a
	// memory leak with a name.
	if len(r.cache) >= r.maxEntry {
		now := time.Now()
		for k, v := range r.cache {
			if now.After(v.expires) {
				delete(r.cache, k)
			}
		}
		if len(r.cache) >= r.maxEntry {
			for k := range r.cache {
				delete(r.cache, k)
				break
			}
		}
	}
	r.cache[cacheKey] = cacheEntry{tenant: tenant, err: err, expires: time.Now().Add(ttl)}
}

// Len reports how many entries are held, which is what a metric or a test asking whether
// anything is accumulating wants.
func (r *KeyResolver) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cache)
}

// ResolveTenant is the provider ingestion routes declare with muzak.Needs.
//
// A provider rather than a guard, because it produces a value the handler reads: the
// tenant is resolved once, before binding, and reaching the handler is proof the key was
// good.
func ResolveTenant(ctx *muzak.Context) (*Tenant, error) {
	resolver := muzak.From[*KeyResolver](ctx)

	secret, present := muzak.BearerToken(ctx)
	if !present {
		return nil, muzak.Unauthorized("an ingestion key is required")
	}

	tenant, err := resolver.Resolve(ctx.Context(), secret)
	if err != nil {
		if errors.Is(err, ErrUnknownKey) {
			// The same answer whether the key is unknown, malformed or revoked. Telling
			// them apart tells an attacker which one to work on next.
			return nil, muzak.Unauthorized("that key is not valid for ingestion")
		}
		// The control plane is unreachable and nothing was cached. Ingestion is degraded
		// rather than wrong, so say so honestly with a 503 the SDK will retry.
		return nil, muzak.ServiceUnavailable("cannot verify the key right now").Wrap(err)
	}
	return tenant, nil
}
