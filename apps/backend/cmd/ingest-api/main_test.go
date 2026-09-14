package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/global/environment"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/routers"
)

// fixture is one project, one key and a bus the test can read back.
type fixture struct {
	client  *testclient.Client
	bus     *eventbus.Local
	key     string
	control *pgdb.Control
}

func newFixture(t *testing.T, tier string) *fixture {
	t.Helper()

	settings, err := config.Load(filepath.Join(repoRoot(), ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	settings.Addr, settings.LogFormat = ":0", "none"

	control := pgdb.New(settings.PostgresDSN)
	if err := control.Start(t.Context()); err != nil {
		t.Skipf("no control plane: %v", err)
	}
	t.Cleanup(func() { _ = control.Close() })

	secret := seedKey(t, control, tier)

	// Its own spool per test, so tests do not read each other's messages.
	bus := eventbus.NewLocal(t.TempDir())

	app := muzak.New(muzak.AppOptions{
		Title:         "ingest-test",
		Addr:          ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(control),
		muzak.WithSingleton(core.NewKeyResolver(control, time.Minute, time.Second, 1000)),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(bus)),
	)
	app.Include(routers.Meta())
	app.Include(routers.Ingest())

	return &fixture{
		client:  testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+secret)),
		bus:     bus,
		key:     secret,
		control: control,
	}
}

// seedKey creates an organization, a project on the given tier and a publishable key.
func seedKey(t *testing.T, control *pgdb.Control, tier string) string {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	org, err := control.Global.Organization.Create().
		SetName("Test Org").SetSlug("t-" + suffix).Save(ctx)
	if err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	proj, err := control.Global.Project.Create().
		SetName("Test Game").SetSlug("p-" + suffix).
		SetPolicyTier(project.PolicyTier(tier)).
		SetRetentionDays(30).
		SetOrganizationID(org.ID).Save(ctx)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	env, err := control.Global.Environment.Create().
		SetKind(environment.KindProduction).SetProjectID(proj.ID).Save(ctx)
	if err != nil {
		t.Fatalf("seed environment: %v", err)
	}

	secret, prefix, hash, err := core.NewAPIKey("publishable", "production")
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	if _, err := control.Global.APIKey.Create().
		SetKind("publishable").SetPrefix(prefix).SetHash(hash).
		SetEnvironmentID(env.ID).Save(ctx); err != nil {
		t.Fatalf("store key: %v", err)
	}
	return secret
}

// published returns every message the bus holds for today.
func (f *fixture) published(t *testing.T) []*eventbus.Message {
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

func TestAppBuilds(t *testing.T) {
	settings, err := config.Load(filepath.Join(repoRoot(), ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	settings.LogFormat = "none"
	app, err := build(settings)
	if err != nil {
		t.Fatalf("build = %v", err)
	}
	if err := app.Build(); err != nil {
		t.Fatalf("Build = %v", err)
	}
}

// TestIngestionIsGuarded — an unauthenticated caller gets 401 rather than a map of the
// schema, because the tenant resolves before binding.
func TestIngestionIsGuarded(t *testing.T) {
	f := newFixture(t, "standard")
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
func buildTestApp(t *testing.T, f *fixture) *muzak.App {
	t.Helper()
	app := muzak.New(muzak.AppOptions{
		Title: "ingest-test", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(f.control),
		muzak.WithSingleton(core.NewKeyResolver(f.control, time.Minute, time.Second, 1000)),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(f.bus)),
	)
	app.Include(routers.Ingest())
	return app
}

func TestAcceptedBatchReachesTheBus(t *testing.T) {
	f := newFixture(t, "standard")

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
	f := newFixture(t, "standard")

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
	f := newFixture(t, "standard")

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
	f := newFixture(t, "standard")

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
	f := newFixture(t, "standard")

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
	f := newFixture(t, "kids_strict")

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
	f := newFixture(t, "standard")

	// Warm the cache.
	f.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)

	// Take the control plane away for real: close the pools underneath the running
	// application, which is what an outage looks like from in here. A test that merely
	// sends a second request would pass with no cache at all.
	if err := f.control.Close(); err != nil {
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
	f := newFixture(t, "standard")

	// Nothing is warmed for this key, and the control plane is gone.
	other := testclient.New(t, buildTestApp(t, f),
		testclient.WithHeader("Authorization", "Bearer "+f.key))
	if err := f.control.Close(); err != nil {
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
	f := newFixture(t, "standard")

	f.client.Post("/v1/batches", testclient.RawJSON(fmt.Sprintf(
		`{"schema_version":1,"batch_id":"%s","environment":"production","install_id":"%s",
		  "sdk":{"name":"unity","version":"1.4.0"},"app":{"version":"2.7.1"},
		  "device":{"platform":"ios"},
		  "consent":{"analytics":true,"policy_version":"2026-01-15"},"events":[]}`,
		uuid.NewString(), uuid.NewString()))).
		AssertStatus(http.StatusUnprocessableEntity)
}
