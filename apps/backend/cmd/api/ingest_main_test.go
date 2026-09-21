package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/transport"
	"playersense.dev/backend/internal/valkey"
)

// ingestFixture is one project, one key and a bus the test can read back.
type ingestFixture struct {
	client *testclient.Client
	bus    *eventbus.Local
	key    string
	db     *pgdb.DB
	envID  int
}

func newIngestFixture(t *testing.T, tier string) *ingestFixture {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.Addr = ":0"

	db := testsupport.Postgres(t, settings)
	seeded := testsupport.SeedProject(t, db, tier)

	// Its own spool per test, so tests do not read each other's messages.
	bus := eventbus.NewLocal(t.TempDir())

	app := muzak.New(muzak.AppOptions{
		Title:         "ingest-test",
		Addr:          ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(core.NewKeyResolver(db, testCache(t), core.KeyResolverOptions{TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Second, MaxLocal: 1000})),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(bus)),
	)
	// The same middleware `build` installs, so a test exercises the wire the way the
	// service serves it. It no-ops on a request with no Content-Encoding, which is most of
	// this file.
	app.Use(transport.Decompress(transport.Options{
		MaxCompressedBytes: settings.IngestMaxCompressedBytes,
	}))

	app.Include(routers.Meta())
	app.Include(routers.Ingest())

	return &ingestFixture{
		client: testclient.New(t, app,
			testclient.WithHeader("Authorization", "Bearer "+seeded.Secret)),
		bus:   bus,
		key:   seeded.Secret,
		db:    db,
		envID: seeded.EnvironmentID,
	}
}

// published returns every message the bus holds for today.
func (f *ingestFixture) published(t *testing.T) []*eventbus.Message {
	t.Helper()
	msgs, err := f.bus.Read(time.Now().UTC())
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	return msgs
}

func batch(events ...map[string]any) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"batch_id":       uuid.NewString(),
		"environment":    "production",
		"install_id":     uuid.NewString(),
		"sdk":            map[string]any{"name": "unity", "version": "1.4.0"},
		"app":            map[string]any{"version": "2.7.1", "build": "342"},
		"device":         map[string]any{"platform": "ios", "os_version": "18.2"},
		"consent": map[string]any{
			"analytics": true, "policy_version": "2026-01-15", "att_status": "not_determined",
		},
		"events": events,
	}
}

func event(name string, over map[string]any) map[string]any {
	e := map[string]any{
		"event_id":   uuid.NewString(),
		"event_name": name,
		"event_time": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range over {
		e[k] = v
	}
	return e
}

func decodeAccepted(t *testing.T, res *testclient.Response) protocol.Accepted {
	t.Helper()
	var out protocol.Accepted
	if err := json.Unmarshal([]byte(res.String()), &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, res.String())
	}
	return out
}

// TestIngestionIsGuarded — an unauthenticated caller gets 401 rather than a map of the
// schema, because the tenant resolves before binding.
func TestIngestionIsGuarded(t *testing.T) {
	f := newIngestFixture(t, "standard")
	anon := testclient.New(t, buildTestApp(t, f))

	anon.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusUnauthorized)

	anon.Post("/v1/batches",
		testclient.Header("Authorization", "Bearer ps_client_live_bogus"),
		testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusUnauthorized)
}

// buildTestApp returns a second application over the fixture's *same* control plane and
// bus, but with a cold key cache.
//
// Sharing the control plane is the point: a test that closes the fixture's pools has to
// affect this application too, or an outage test proves nothing.
func buildTestApp(t *testing.T, f *ingestFixture) *muzak.App {
	t.Helper()
	app := muzak.New(muzak.AppOptions{
		Title: "ingest-test", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(f.db),
		muzak.WithSingleton(core.NewKeyResolver(f.db, testCache(t), core.KeyResolverOptions{TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Millisecond, MaxLocal: 1000})),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(f.bus)),
	)
	app.Include(routers.Ingest())
	return app
}

func TestAcceptedBatchReachesTheBus(t *testing.T) {
	f := newIngestFixture(t, "standard")

	res := f.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", map[string]any{"session_id": "s1",
			"properties": map[string]any{"level": 3, "difficulty": "hard", "premium": true}}),
		event("purchase", map[string]any{"revenue_micros": 990000, "currency": "USD"}),
	)))
	res.AssertStatus(http.StatusAccepted)

	got := decodeAccepted(t, res)
	if got.Accepted != 2 || got.Dropped != 0 {
		t.Fatalf("accepted=%d dropped=%d, want 2 and 0", got.Accepted, got.Dropped)
	}

	// A 202 means the batch reached durable queueing (Appendix C.1), so it is on the bus
	// by the time the response is written rather than at some point afterwards.
	msgs := f.published(t)
	if len(msgs) != 1 {
		t.Fatalf("want one message on the bus, got %d", len(msgs))
	}
	m := msgs[0]
	if m.ProjectSeq == 0 || m.EnvironmentSeq == 0 {
		t.Error("the tenant must travel with the message, so the writer never needs PostgreSQL")
	}
	if m.RetentionTier != 30 {
		t.Errorf("retention_tier = %d, want 30", m.RetentionTier)
	}

	var payload ingest.NormalizedBatch
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// Properties are split by type (§22), so a filter on a number compares numbers.
	ev := payload.Events[0]
	if ev.PropsInt["level"] != 3 || ev.PropsString["difficulty"] != "hard" || ev.PropsBool["premium"] != 1 {
		t.Errorf("properties were not split by type: %+v", ev)
	}
	if payload.Events[1].EventType != "purchase" {
		t.Errorf("event_type = %q, want purchase", payload.Events[1].EventType)
	}
}

// TestConsentWithheldStoresNothing — §97. The batch is accepted and discarded rather than
// refused: an SDK that retries a rejected batch forever is worse for everyone.
func TestConsentWithheldStoresNothing(t *testing.T) {
	f := newIngestFixture(t, "standard")

	b := batch(event("session_start", nil))
	b["consent"] = map[string]any{"analytics": false, "policy_version": "2026-01-15"}

	res := f.client.Post("/v1/batches", testclient.JSON(b))
	res.AssertStatus(http.StatusAccepted)

	got := decodeAccepted(t, res)
	if got.Accepted != 0 || got.Dropped != 1 {
		t.Errorf("accepted=%d dropped=%d, want 0 and 1", got.Accepted, got.Dropped)
	}
	if len(f.published(t)) != 0 {
		t.Error("nothing may reach the bus without a consent basis")
	}
}

// TestOneBadEventDoesNotFailTheBatch — an SDK that sends one malformed event among five
// hundred good ones would otherwise retry the whole batch forever.
func TestOneBadEventDoesNotFailTheBatch(t *testing.T) {
	f := newIngestFixture(t, "standard")

	res := f.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", nil),
		event("bad name with spaces", nil),
		event("leaky", map[string]any{"properties": map[string]any{"email": "player@example.com"}}),
	)))
	res.AssertStatus(http.StatusAccepted)

	got := decodeAccepted(t, res)
	if got.Accepted != 1 || got.Dropped != 2 {
		t.Fatalf("accepted=%d dropped=%d, want 1 and 2", got.Accepted, got.Dropped)
	}
	if len(got.Reasons) != 2 {
		t.Errorf("want a reason per drop, got %v", got.Reasons)
	}
}

// TestDirectPersonalDataIsRefused — §19.3. A studio that sends an email address as a
// property makes PlayerSense a processor of data neither party intended it to hold.
func TestDirectPersonalDataIsRefused(t *testing.T) {
	f := newIngestFixture(t, "standard")

	for _, probe := range []map[string]any{
		{"email": "player@example.com"},
		{"phone": "+905551234567"},
		{"pan": "4111111111111111"},
	} {
		res := f.client.Post("/v1/batches",
			testclient.JSON(batch(event("leaky", map[string]any{"properties": probe}))))
		got := decodeAccepted(t, res)
		if got.Accepted != 0 {
			t.Errorf("%v was stored; it must be refused", probe)
		}
	}
}

// TestClientClockIsClamped — §19.2. A wrong device clock otherwise produces a 1970 cohort
// with a day-offset of twenty thousand.
func TestClientClockIsClamped(t *testing.T) {
	f := newIngestFixture(t, "standard")

	ancient := time.Now().UTC().AddDate(-2, 0, 0)
	f.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", map[string]any{"event_time": ancient.Format(time.RFC3339Nano)}),
	))).AssertStatus(http.StatusAccepted)

	msgs := f.published(t)
	var payload ingest.NormalizedBatch
	if err := json.Unmarshal(msgs[0].Payload, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ev := payload.Events[0]
	if ev.EventTime.Before(time.Now().AddDate(0, 0, -8)) {
		t.Errorf("event_time = %s was not clamped", ev.EventTime)
	}
	// The original is preserved, so the correction is auditable rather than silent.
	if ev.ClientEventTime == nil {
		t.Error("the clamped-away timestamp must be recorded")
	}
}

// TestKidsStrictHasNoPersistentIdentifier is the check that enforces §98 rather than
// describing it.
//
// The client sends the same install_id in both batches. A kids_strict project must not be
// able to link them, so the gateway replaces it — and that has to be true even when the
// SDK is wrong, misconfigured or hostile.
func TestKidsStrictHasNoPersistentIdentifier(t *testing.T) {
	f := newIngestFixture(t, "kids_strict")

	stable := uuid.NewString()
	seen := map[string]bool{}
	for range 2 {
		b := batch(event("session_start", nil))
		b["install_id"] = stable
		f.client.Post("/v1/batches", testclient.JSON(b)).AssertStatus(http.StatusAccepted)
	}
	for _, m := range f.published(t) {
		var payload ingest.NormalizedBatch
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload.InstallID == stable {
			t.Fatal("a kids_strict project stored the client's persistent identifier")
		}
		seen[payload.InstallID] = true
	}
	if len(seen) != 2 {
		t.Errorf("want two unlinkable identifiers, got %d", len(seen))
	}
}

// TestIngestionSurvivesAControlPlaneOutage is §64, and it is the reason the key cache
// exists at all.
//
// Ingestion must keep accepting batches while PostgreSQL is unreachable. The key was
// resolved once and cached, so the second batch needs no query — and a gateway that
// needs a database to answer is a gateway that stops when the database does.
func TestIngestionSurvivesAControlPlaneOutage(t *testing.T) {
	f := newIngestFixture(t, "standard")

	// Warm the cache.
	f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)

	// Take the control plane away for real: close the pools underneath the running
	// application, which is what an outage looks like from in here. A test that merely
	// sends a second request would pass with no cache at all.
	if err := f.db.Close(); err != nil {
		t.Fatalf("close control plane: %v", err)
	}

	f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)

	if got := len(f.published(t)); got != 2 {
		t.Errorf("want both batches accepted through the outage, got %d on the bus", got)
	}
}

// TestAnUncachedKeyFailsClosedDuringAnOutage is the other half of §64.
//
// Surviving an outage means serving what was already known, not inventing answers. A key
// the cache has never seen cannot be verified, and the honest response is a 503 the SDK
// will retry — never a 202 for a batch nobody authorised.
func TestAnUncachedKeyFailsClosedDuringAnOutage(t *testing.T) {
	f := newIngestFixture(t, "standard")

	// Nothing is warmed for this key, and the control plane is gone.
	other := testclient.New(t, buildTestApp(t, f),
		testclient.WithHeader("Authorization", "Bearer "+f.key))
	if err := f.db.Close(); err != nil {
		t.Fatalf("close control plane: %v", err)
	}

	res := other.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil))))
	if res.Status == http.StatusAccepted {
		t.Fatal("an unverifiable key was accepted; ingestion must fail closed")
	}
	if res.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 so the SDK retries", res.Status)
	}
}

func TestBatchMustCarryEvents(t *testing.T) {
	f := newIngestFixture(t, "standard")

	f.client.Post("/v1/batches", testclient.RawJSON(fmt.Sprintf(
		`{"schema_version":1,"batch_id":"%s","environment":"production","install_id":"%s",
		  "sdk":{"name":"unity","version":"1.4.0"},"app":{"version":"2.7.1"},
		  "device":{"platform":"ios"},
		  "consent":{"analytics":true,"policy_version":"2026-01-15"},"events":[]}`,
		uuid.NewString(), uuid.NewString()))).
		AssertStatus(http.StatusUnprocessableEntity)
}

// testCache returns a connected Valkey client, or skips.
//
// The tests run against the real cache rather than a fake: the three-tier resolver is the
// thing under test, and a fake for the middle tier would remove most of what is worth
// testing about it.
func testCache(t *testing.T) *valkey.Client {
	t.Helper()
	return testsupport.Cache(t, testsupport.Settings(t))
}

// seedKindKey mints a key of an arbitrary kind against the fixture's own project.
func seedKindKey(t *testing.T, f *ingestFixture, kind string) (secret, publicID string) {
	t.Helper()
	generated, err := core.NewAPIKey(kind, "production")
	if err != nil {
		t.Fatalf("mint %s key: %v", kind, err)
	}
	if _, err := f.db.Client.APIKey.Create().
		SetKind(apikey.Kind(kind)).
		SetPublicID(generated.PublicID).
		SetSecretHash(generated.Hash).
		SetEnvironmentID(f.envID).Save(context.Background()); err != nil {
		t.Fatalf("store %s key: %v", kind, err)
	}
	return generated.Secret, generated.PublicID
}

// TestOnlyAProjectKeyMayIngest — §52. A reporting or server credential reaching this
// endpoint is a misconfiguration, and accepting it would widen what a leaked analytics
// key can do.
func TestOnlyAProjectKeyMayIngest(t *testing.T) {
	f := newIngestFixture(t, "standard")

	for _, kind := range []string{core.KindReporting, core.KindServer} {
		secret, _ := seedKindKey(t, f, kind)
		client := testclient.New(t, buildTestApp(t, f),
			testclient.WithHeader("Authorization", "Bearer "+secret))

		res := client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil))))
		if res.Status != http.StatusForbidden {
			t.Errorf("a %s key got status %d; ingestion needs a project key", kind, res.Status)
		}
	}
}

// TestRevocationTakesEffectImmediately — §53.
//
// A revoked key must stop working now, not when its cache entry happens to expire. That
// window is exactly the minutes during which somebody needs it to stop.
func TestRevocationTakesEffectImmediately(t *testing.T) {
	f := newIngestFixture(t, "standard")
	secret, publicID := seedKindKey(t, f, core.KindProject)

	cache := testCache(t)
	resolver := core.NewKeyResolver(f.db, cache, core.KeyResolverOptions{
		TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Millisecond, MaxLocal: 100,
	})

	// Warm it: the credential is now in Valkey with a minute to live.
	if _, err := resolver.Resolve(t.Context(), secret); err != nil {
		t.Fatalf("a fresh key did not resolve: %v", err)
	}

	// Revoke it the way control-api does.
	if _, err := f.db.Client.APIKey.Update().
		Where(apikey.PublicID(publicID)).
		SetRevokedAt(time.Now().UTC()).Save(context.Background()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := resolver.EvictPublicID(t.Context(), publicID, core.RevocationTombstoneTTL); err != nil {
		t.Fatalf("evict: %v", err)
	}

	// Let the in-process tier lapse; the Valkey entry is still well inside its TTL.
	time.Sleep(20 * time.Millisecond)

	if _, err := resolver.Resolve(t.Context(), secret); err == nil {
		t.Fatal("a revoked key still resolved from a warm cache entry")
	}
}

// TestValkeyAnswersWhenPostgresIsDown is the middle tier earning its place.
//
// The control plane is the authority, but it is not on the request path. Once a key has
// been verified, every instance sharing the cache can serve it without a query — which is
// both §3.2's requirement and what makes an outage survivable (§64).
func TestValkeyAnswersWhenPostgresIsDown(t *testing.T) {
	f := newIngestFixture(t, "standard")
	secret, _ := seedKindKey(t, f, core.KindProject)

	cache := testCache(t)
	warm := core.NewKeyResolver(f.db, cache, core.KeyResolverOptions{
		TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Millisecond, MaxLocal: 100,
	})
	if _, err := warm.Resolve(t.Context(), secret); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	// A different instance, with no in-process cache of its own and no usable database.
	cold := core.NewKeyResolver(
		pgdb.New("postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable"),
		cache,
		core.KeyResolverOptions{TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Millisecond, MaxLocal: 100},
	)
	tenant, err := cold.Resolve(t.Context(), secret)
	if err != nil {
		t.Fatalf("the shared cache did not serve the key: %v", err)
	}
	if tenant.ProjectSeq == 0 || tenant.KeyKind != core.KindProject {
		t.Errorf("the cached tenant is incomplete: %+v", tenant)
	}

	// And a key that was never cached still fails closed rather than being invented.
	unknown, _ := core.NewAPIKey(core.KindProject, "production")
	if _, err := cold.Resolve(t.Context(), unknown.Secret); err == nil {
		t.Fatal("an unverifiable key was accepted; resolution must fail closed")
	}
}

// An exposure is its own kind of event (§44).
//
// Not "custom", because of how it is read: "which players saw variant B, and what did they do
// next" scans a type rather than filtering one name out of every custom event a game sends.
// Matched exactly rather than by prefix — an event named `feature_exposure_v2` is the studio's
// own, and stealing their namespace is how a product decides what a customer's data means.
func TestAnExposureIsItsOwnEventType(t *testing.T) {
	f := newIngestFixture(t, "standard")

	b := batch(
		event("feature_exposure", map[string]any{
			"properties": map[string]any{
				"flag": "hud_v2", "value": true, "config_version": 7, "published": true,
			},
		}),
		event("feature_exposure_v2", nil),
		event("level_completed", nil),
	)
	f.client.Post("/v1/batches", testclient.JSON(b)).AssertStatus(http.StatusAccepted)

	msgs := f.published(t)
	if len(msgs) != 1 {
		t.Fatalf("want one message on the bus, got %d", len(msgs))
	}
	var payload ingest.NormalizedBatch
	if err := json.Unmarshal(msgs[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	for i, want := range []string{"exposure", "custom", "custom"} {
		if got := payload.Events[i].EventType; got != want {
			t.Errorf("events[%d] (%s) is %q, want %q",
				i, payload.Events[i].EventName, got, want)
		}
	}

	// The properties a study reads, split by type as §22 requires.
	exposure := payload.Events[0]
	if exposure.PropsString["flag"] != "hud_v2" {
		t.Errorf("flag = %q", exposure.PropsString["flag"])
	}
	if exposure.PropsInt["config_version"] != 7 {
		t.Errorf("config_version = %d", exposure.PropsInt["config_version"])
	}
	if exposure.PropsBool["published"] != 1 || exposure.PropsBool["value"] != 1 {
		t.Errorf("booleans were not split by type: %+v", exposure)
	}
}
