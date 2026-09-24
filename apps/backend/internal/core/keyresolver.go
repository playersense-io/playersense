package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// Tenant is everything a request needs, resolved once from the key.
//
// It carries the integer surrogates as well as the UUIDs because the writer stamps both
// onto every row: the surrogates are what ClickHouse sorts on (§23.1) and the UUIDs are
// what the outside world sees.
type Tenant struct {
	OrgSeq         uint32 `json:"org_seq"`
	ProjectSeq     uint32 `json:"project_seq"`
	EnvironmentSeq uint16 `json:"environment_seq"`

	OrgID         string `json:"org_id"`
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`

	Environment   string `json:"environment"`
	PolicyTier    string `json:"policy_tier"`
	RetentionTier uint16 `json:"retention_tier"`

	KeyKind     string `json:"key_kind"`
	KeyPublicID string `json:"key_public_id"`

	// KeyExpiresAt is when a rolled key stops, carried so a cached entry stops with it rather
	// than when the cache happens to expire.
	KeyExpiresAt *time.Time `json:"key_expires_at,omitempty"`
}

// expired reports whether the key this tenant came from has reached its end.
func (t *Tenant) expired(now time.Time) bool {
	return t != nil && t.KeyExpiresAt != nil && !now.Before(*t.KeyExpiresAt)
}

// ErrUnknownKey reports a credential that resolves to nothing usable.
//
// One error for unknown, malformed, revoked and wrong-secret alike. Telling them apart
// tells an attacker which one to work on next.
var ErrUnknownKey = errors.New("unknown api key")

type localEntry struct {
	tenant  *Tenant
	err     error
	expires time.Time
}

// KeyResolver turns a presented credential into a Tenant.
//
// Three tiers, each covering the failure of the one below it:
//
//  1. an in-process cache, which survives both Valkey and PostgreSQL being unreachable;
//  2. Valkey, which is shared by every instance so a key verified once is verified for
//     the fleet;
//  3. PostgreSQL plus one Argon2id comparison, which is the authority.
//
// The tiers are not an optimisation. §3.2 forbids the ingestion path from querying the
// control plane per request, §64 requires ingestion to keep working through a PostgreSQL
// outage, and §65 requires losing Valkey to cost speed rather than data. Each tier exists
// because one of those sentences does.
type KeyResolver struct {
	global *DB
	cache  *valkey.Client

	ttl      time.Duration
	negTTL   time.Duration
	localTTL time.Duration
	maxLocal int

	mu    sync.RWMutex
	local map[string]localEntry
}

// KeyResolverOptions configures the tiers.
type KeyResolverOptions struct {
	// TTL is how long a verified key stays in Valkey.
	TTL time.Duration
	// NegativeTTL is how long a rejection is remembered. A project key is assumed
	// discoverable (§71), so wrong keys arrive in volume and none of them should reach
	// an Argon2 comparison twice.
	NegativeTTL time.Duration
	// LocalTTL is how long the in-process tier holds an entry. Deliberately short:
	// revoking a key evicts it from Valkey immediately, and this is the one window that
	// eviction cannot reach.
	LocalTTL time.Duration
	// MaxLocal bounds the in-process table, which is keyed by something a client
	// influences.
	MaxLocal int
}

// NewKeyResolver returns a resolver over the control plane and the cache.
func NewKeyResolver(global *DB, cache *valkey.Client, opts KeyResolverOptions) *KeyResolver {
	return &KeyResolver{
		global:   global,
		cache:    cache,
		ttl:      opts.TTL,
		negTTL:   opts.NegativeTTL,
		localTTL: opts.LocalTTL,
		maxLocal: opts.MaxLocal,
		local:    make(map[string]localEntry),
	}
}

// Resolve verifies a presented credential.
func (r *KeyResolver) Resolve(ctx context.Context, presented string) (*Tenant, error) {
	publicID, private, err := ParseKey(presented)
	if err != nil {
		return nil, ErrUnknownKey
	}
	cacheKey := r.cache.Key("apikey", CacheDigest(presented))

	// Tier 1: this process. Survives everything else being unreachable.
	if entry, ok := r.readLocal(cacheKey); ok {
		if entry.tenant.expired(time.Now()) {
			return nil, ErrUnknownKey
		}
		return entry.tenant, entry.err
	}

	// Tier 2: Valkey, shared across instances.
	if tenant, err, ok := r.readCache(ctx, cacheKey); ok {
		if tenant.expired(time.Now()) {
			return nil, ErrUnknownKey
		}
		r.writeLocal(cacheKey, tenant, err)
		return tenant, err
	}

	// Tier 3: the authority. One indexed lookup, then one Argon2 comparison.
	tenant, lookupErr := r.verify(ctx, publicID, private)
	r.writeCache(ctx, cacheKey, tenant, lookupErr)
	r.writeLocal(cacheKey, tenant, lookupErr)
	return tenant, lookupErr
}

// Evict removes a credential from the shared cache.
//
// Called when a key is revoked, so revocation takes effect now rather than when the TTL
// happens to expire. The in-process tier is not reachable from here, which is why its TTL
// is measured in seconds.
func (r *KeyResolver) Evict(ctx context.Context, presented string) error {
	return r.cache.Del(ctx, r.cache.Key("apikey", CacheDigest(presented)))
}

// EvictPublicID removes every cached form of a key by its public half.
//
// The cache is keyed by a digest of the whole credential, which the control plane does
// not hold — it only ever saw the secret once. So revocation writes a short-lived
// tombstone under the public identifier, and Resolve refuses anything matching it. That
// is what lets a key be revoked by somebody who cannot reproduce it.
func (r *KeyResolver) EvictPublicID(ctx context.Context, publicID string, ttl time.Duration) error {
	return r.cache.Set(ctx, r.tombstoneKey(publicID), []byte("revoked"), ttl)
}

func (r *KeyResolver) tombstoneKey(publicID string) string {
	return RevokedKey(r.cache, publicID)
}

// RevokedKey is where a revoked key's tombstone lives, for a process that revokes keys without a
// resolver of its own: the worker, when it carries out a project's deletion.
func RevokedKey(cache *valkey.Client, publicID string) string {
	return cache.Key("apikey", "revoked", publicID)
}

func (r *KeyResolver) readLocal(cacheKey string) (localEntry, bool) {
	r.mu.RLock()
	entry, ok := r.local[cacheKey]
	r.mu.RUnlock()
	if !ok || time.Now().After(entry.expires) {
		return localEntry{}, false
	}
	return entry, true
}

func (r *KeyResolver) writeLocal(cacheKey string, tenant *Tenant, err error) {
	ttl := r.localTTL
	if err != nil {
		ttl = min(r.localTTL, r.negTTL)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.local) >= r.maxLocal {
		now := time.Now()
		for k, v := range r.local {
			if now.After(v.expires) {
				delete(r.local, k)
			}
		}
		if len(r.local) >= r.maxLocal {
			for k := range r.local {
				delete(r.local, k)
				break
			}
		}
	}
	r.local[cacheKey] = localEntry{tenant: tenant, err: err, expires: time.Now().Add(ttl)}
}

// cachedRejection is what a remembered failure looks like in Valkey.
const cachedRejection = "!"

func (r *KeyResolver) readCache(ctx context.Context, cacheKey string) (*Tenant, error, bool) {
	raw, err := r.cache.Get(ctx, cacheKey)
	if err != nil || raw == nil {
		// Valkey is down or has nothing. Either way the next tier answers — losing the
		// cache costs a slower request, not a failed one (§65).
		return nil, nil, false
	}
	if string(raw) == cachedRejection {
		return nil, ErrUnknownKey, true
	}
	var tenant Tenant
	if err := json.Unmarshal(raw, &tenant); err != nil {
		return nil, nil, false
	}
	// A revoked key is refused even while its entry is still warm.
	if tomb, err := r.cache.Get(ctx, r.tombstoneKey(tenant.KeyPublicID)); err == nil && tomb != nil {
		return nil, ErrUnknownKey, true
	}
	return &tenant, nil, true
}

func (r *KeyResolver) writeCache(ctx context.Context, cacheKey string, tenant *Tenant, lookupErr error) {
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrUnknownKey) {
			_ = r.cache.Set(ctx, cacheKey, []byte(cachedRejection), r.negTTL)
		}
		// Anything else is the control plane being unreachable, which must not be
		// remembered as a rejection.
		return
	}
	if raw, err := json.Marshal(tenant); err == nil {
		_ = r.cache.Set(ctx, cacheKey, raw, r.ttl)
	}
}

// verify is the authority: one indexed lookup, then one Argon2id comparison.
func (r *KeyResolver) verify(ctx context.Context, publicID, private string) (*Tenant, error) {
	// The authority may be unreachable, or may never have connected at all. Either is an
	// outage rather than a rejection, and the difference matters: a rejection would be
	// cached, and an outage remembered as a rejection turns a blip into an hour of
	// refused traffic.
	if !r.global.Ready() {
		return nil, pgdb.ErrNotStarted
	}
	key, err := r.global.Client.APIKey.Query().
		Where(apikey.PublicID(publicID)).
		WithEnvironment(func(q *ent.EnvironmentQuery) {
			q.WithProject(func(pq *ent.ProjectQuery) {
				pq.WithOrganization()
			})
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrUnknownKey
		}
		// The control plane is unreachable. This is not a rejection, and saying so is
		// what stops an outage from looking like a fleet of bad credentials.
		return nil, err
	}
	if key.RevokedAt != nil || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
		return nil, ErrUnknownKey
	}

	// The one slow comparison, reached only after an indexed lookup matched. A key that
	// does not exist never gets this far, which is what keeps a guessing flood cheap.
	if !VerifySecret(private, key.SecretHash) {
		return nil, ErrUnknownKey
	}

	env := key.Edges.Environment
	if env == nil || env.Edges.Project == nil || env.Edges.Project.Edges.Organization == nil {
		return nil, ErrUnknownKey
	}
	proj := env.Edges.Project
	org := proj.Edges.Organization

	// A digest written under weaker parameters is upgraded on use, so raising the cost
	// later does not invalidate anyone's key.
	if NeedsRehash(key.SecretHash) {
		if rehashed, err := HashSecret(private); err == nil {
			_, _ = key.Update().SetSecretHash(rehashed).Save(ctx)
		}
	}

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

		KeyKind:      string(key.Kind),
		KeyPublicID:  key.PublicID,
		KeyExpiresAt: key.ExpiresAt,
	}, nil
}

// LocalLen reports how many entries the in-process tier holds, which is what a metric or
// a test asking whether anything is accumulating wants.
func (r *KeyResolver) LocalLen() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.local)
}

// requireKind resolves the caller and insists the key is of the given class.
//
// Having the returned Tenant is proof of the check, because the only thing that produces
// one is this function.
func requireKind(ctx *muzak.Context, kind string) (*Tenant, error) {
	resolver := muzak.From[*KeyResolver](ctx)

	presented, present := muzak.BearerToken(ctx)
	if !present {
		return nil, muzak.Unauthorized("an API key is required")
	}

	tenant, err := resolver.Resolve(ctx.Context(), presented)
	if err != nil {
		if errors.Is(err, ErrUnknownKey) {
			return nil, muzak.Unauthorized("that key is not valid")
		}
		// Neither tier could answer and nothing was cached. Ingestion is degraded rather
		// than wrong, so say so honestly with a 503 the client will retry — never a
		// success for a request nobody authorised.
		return nil, muzak.ServiceUnavailable("cannot verify the key right now").Wrap(err)
	}
	if tenant.KeyKind != kind {
		return nil, muzak.Forbidden("this endpoint needs a " + kind + " key")
	}
	if usage, found := muzak.TryFrom[*KeyUsage](ctx); found {
		usage.Touch(tenant.KeyPublicID)
	}
	return tenant, nil
}

// ResolveProjectKey is the provider ingestion routes declare with muzak.Needs.
func ResolveProjectKey(ctx *muzak.Context) (*Tenant, error) {
	return requireKind(ctx, KindProject)
}

// ResolveReportingKey is the provider the reporting API declares.
func ResolveReportingKey(ctx *muzak.Context) (*Tenant, error) {
	return requireKind(ctx, KindReporting)
}

// ResolveServerKey is the provider the server-to-server surface declares (§47).
func ResolveServerKey(ctx *muzak.Context) (*Tenant, error) {
	return requireKind(ctx, KindServer)
}

// RevocationTombstoneTTL is how long a revoked key's tombstone is kept.
//
// It only has to outlive the longest cache entry that could still hold the key, so it is
// the positive TTL with room to spare. Keeping it forever would turn the cache into a
// growing list of every key ever revoked.
const RevocationTombstoneTTL = 30 * time.Minute
