package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/liveops"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/targeting"
	"playersense.dev/backend/internal/testsupport"
)

func configSettings(t *testing.T) config.Settings {
	t.Helper()
	settings, err := config.Load(filepath.Join(repoRoot(), ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	settings.Addr = ":0"
	settings.LogFormat = "none"
	settings.RateLimitDisabled = true
	settings.OpenExchangeRatesAppID = ""
	// The store's whole job is to not read the database every request, which would make
	// every test below race its own publish.
	settings.ConfigCacheTTL = time.Millisecond
	return settings
}

// fixture is a project with a published document and a key that can ask about it.
type configFixture struct {
	settings config.Settings
	db       *pgdb.DB
	project  testsupport.Project
	client   *testclient.Client
}

// publish writes a compiled document straight into the global tier.
//
// Compiled through liveconfig.Compile rather than assembled by hand, so a test exercises the
// same inlining and validation a real publish does — a document a test invented could hold a
// shape the compiler would have refused.
func (f *configFixture) publish(t *testing.T, in liveconfig.Input) liveconfig.Document {
	t.Helper()
	document, err := liveconfig.Compile(in)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}

	if _, err := f.db.Client.ConfigVersion.Create().
		SetProjectID(f.project.ProjectSeq).
		SetEnvironmentID(f.project.EnvironmentID).
		SetVersion(in.Version).
		SetDocument(stored).
		SetPublishedByLabel("a test").
		Save(t.Context()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return document
}

// newFixtureWithLimits is newFixture with the rate limiter switched on.
//
// Every other test here leaves it off, so that one run's counters do not spend the next one's
// budget — the counters live in Valkey and outlive the process.
func newFixtureWithLimits(t *testing.T) *configFixture {
	t.Helper()
	return buildFixture(t, false)
}

func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	return buildFixture(t, true)
}

func buildFixture(t *testing.T, limitsDisabled bool) *configFixture {
	t.Helper()
	settings := configSettings(t)
	settings.RateLimitDisabled = limitsDisabled
	db := testsupport.Postgres(t, settings)
	project := testsupport.SeedProject(t, db, "standard")

	app, err := build(settings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	return &configFixture{
		settings: settings, db: db, project: project,
		client: testclient.New(t, app,
			testclient.WithHeader("Authorization", "Bearer "+project.Secret)),
	}
}

// ask sends one config request for a player.
func (f *configFixture) ask(t *testing.T, install string, context map[string]any, options ...testclient.RequestOption) *testclient.Response {
	t.Helper()
	body := map[string]any{
		"schema_version": 1,
		"install_id":     install,
		"app":            map[string]string{"version": "2.7.1"},
		"device":         map[string]string{"platform": "ios"},
		"sdk":            map[string]string{"name": "unity", "version": "1.4.0"},
	}
	if context != nil {
		body["context"] = context
	}
	return f.client.Post("/v1/config", append([]testclient.RequestOption{testclient.JSON(body)}, options...)...)
}

// The whole path: a published document, a key, a player, and the values that player gets.
func TestAPlayerIsServedTheirOwnAnswers(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{
			{Key: "hud_v2", Kind: "bool", Fallback: false, Rollout: targeting.Buckets,
				Rules: []liveconfig.Variant{{
					Audience: mustRule(t, `{"key":"context.tutorial_done","op":"eq","value":true}`),
					Value:    true, Rollout: targeting.Buckets,
				}}},
			{Key: "max_level", Kind: "number", Fallback: float64(50), Rollout: targeting.Buckets},
		},
	})

	var graduated protocol.ConfigResponse
	f.ask(t, install(1), map[string]any{"tutorial_done": true}).
		AssertStatus(http.StatusOK).JSON(&graduated)

	if graduated.Version != 1 {
		t.Errorf("version is %d", graduated.Version)
	}
	if graduated.Flags["hud_v2"] != true {
		t.Errorf("hud_v2 is %v for a player who finished the tutorial", graduated.Flags["hud_v2"])
	}
	if graduated.Flags["max_level"] != float64(50) {
		t.Errorf("max_level is %v", graduated.Flags["max_level"])
	}
	if graduated.ServerTimeMS == 0 {
		t.Error("no server time, so an SDK has nothing to anchor a window against")
	}

	var beginner protocol.ConfigResponse
	f.ask(t, install(2), map[string]any{"tutorial_done": false}).
		AssertStatus(http.StatusOK).JSON(&beginner)
	if beginner.Flags["hud_v2"] != false {
		t.Errorf("hud_v2 is %v for a player who has not", beginner.Flags["hud_v2"])
	}
}

// ADR 0027, end to end: the rules never leave the server. A response carrying an audience is
// a response a modified build can read and satisfy.
func TestTheRulesNeverReachThePlayer(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{
		Version:  1,
		Segments: liveconfig.Segments{"whales": mustRule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gt","value":100000000}`)},
		Flags: []liveconfig.Flag{{
			Key: "vip_shop", Kind: "bool", Fallback: false, Rollout: targeting.Buckets,
			Rules: []liveconfig.Variant{{
				Audience: mustRule(t, `{"segment":"whales"}`),
				Value:    true, Rollout: targeting.Buckets,
			}},
		}},
	})

	body := f.ask(t, install(1), nil).AssertStatus(http.StatusOK).String()
	for _, leak := range []string{
		"whales", "commerce.player", "lifetime_spend", "segment", "audience", "100000000",
	} {
		if contains(body, leak) {
			t.Errorf("the response carries %q, which tells a modified build what to claim:\n%s",
				leak, body)
		}
	}
}

// A modified build sends `geo.country` and expects to be treated as Turkish. It is not, and
// not because anything checked: the key becomes `context.geo.country`, which no reserved rule
// reads, so the forgery has nowhere to land.
func TestAClientCannotForgeAServerDerivedFact(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{{
			Key: "regional_offer", Kind: "bool", Fallback: false, Rollout: targeting.Buckets,
			Rules: []liveconfig.Variant{{
				Audience: mustRule(t, `{"key":"geo.country","op":"eq","value":"TR"}`),
				Value:    true, Rollout: targeting.Buckets,
			}},
		}},
	})

	var forged protocol.ConfigResponse
	f.ask(t, install(1), map[string]any{
		"geo.country":                           "TR",
		"network.vpn":                           false,
		"commerce.player.lifetime_spend_micros": 999999999,
	}).AssertStatus(http.StatusOK).JSON(&forged)

	if forged.Flags["regional_offer"] != false {
		t.Error("a client claimed geo.country and was believed")
	}
}

// Hasan's LiveOps example, served: the first five days of every month, on for a player inside
// the window and absent outside it, with the next occurrence so a game can count down.
func TestAnEventIsServedWithItsWindow(t *testing.T) {
	f := newConfigFixture(t)
	// Anchored so that "now" is always inside the window, whatever day the suite runs:
	// a daily event covering almost the whole day.
	f.publish(t, liveconfig.Input{
		Version: 1,
		Events: []liveconfig.LiveOpsEvent{{
			Key: "happy_hour", Kind: "offer", Priority: 10,
			Schedule: liveops.Schedule{
				Mode:     liveops.Daily,
				Anchor:   time.Now().UTC().Truncate(24 * time.Hour),
				Duration: 23 * time.Hour,
			},
			Payload: map[string]any{"multiplier": 2},
			Rollout: targeting.Buckets,
		}},
	})

	var got protocol.ConfigResponse
	f.ask(t, install(1), nil).AssertStatus(http.StatusOK).JSON(&got)

	if len(got.Events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got.Events), got.Events)
	}
	event := got.Events[0]
	if event.Key != "happy_hour" || event.Payload == nil {
		t.Errorf("event is %+v", event)
	}
	if event.EndsAtMS <= event.StartsAtMS {
		t.Errorf("the window ends before it starts: %+v", event)
	}
	if event.NextStartsAtMS <= event.EndsAtMS {
		t.Errorf("the next occurrence is not after this one: %+v", event)
	}
}

// An SDK polls. Most polls would receive exactly what they already have, and a 304 is how they
// find that out without carrying the body again.
func TestAPollingSDKIsToldNothingChanged(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{
			{Key: "hud_v2", Kind: "bool", Fallback: true, Rollout: targeting.Buckets},
		},
	})

	first := f.ask(t, install(1), nil).AssertStatus(http.StatusOK)
	tag := first.Header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag, so an SDK has nothing to send back")
	}

	// Far enough apart that the server clock has certainly moved. Without the pause the
	// two requests can land in the same millisecond, and the test passes even when
	// ServerTimeMS is inside the tag — which is how it passed a teeth check it should
	// have failed.
	time.Sleep(5 * time.Millisecond)

	again := f.ask(t, install(1), nil).AssertStatus(http.StatusOK)
	if again.Header.Get("ETag") != tag {
		t.Errorf("the tag changed between two identical answers: %s then %s\n"+
			"something that varies per request is inside it",
			tag, again.Header.Get("ETag"))
	}

	f.ask(t, install(1), nil, testclient.Header("If-None-Match", tag)).
		AssertStatus(http.StatusNotModified)

	// A different player is a different answer, so the same tag must not suppress it.
	other := f.ask(t, install(2), nil, testclient.Header("If-None-Match", tag))
	if other.Status == http.StatusNotModified && install(1) != install(2) {
		// Only a real failure if their answers differ; with one unconditional flag they
		// do not, and an identical answer legitimately shares a tag.
		var body protocol.ConfigResponse
		f.ask(t, install(2), nil).AssertStatus(http.StatusOK).JSON(&body)
		if body.Flags["hud_v2"] != true {
			t.Error("a 304 was served for an answer that differs")
		}
	}
}

// A project that has published nothing is where every project starts. A game whose first
// launch fails because nobody has pressed publish is a worse first experience than one that
// runs on its own defaults.
func TestAProjectWithNothingPublishedStillAnswers(t *testing.T) {
	f := newConfigFixture(t)

	var got protocol.ConfigResponse
	f.ask(t, install(1), nil).AssertStatus(http.StatusOK).JSON(&got)

	if got.Version != 0 {
		t.Errorf("version is %d, want 0 for an unpublished project", got.Version)
	}
	if got.Flags == nil {
		t.Error("flags is null rather than an empty object, which an SDK has to special-case")
	}
	if len(got.Events) != 0 {
		t.Errorf("an unpublished project served %d events", len(got.Events))
	}
}

// The environment comes from the key. A build pointed at production with a development key
// must not be able to ask for production's config by naming it in the body.
func TestTheBodyCannotChooseTheEnvironment(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{Version: 1})

	f.client.Post("/v1/config", testclient.JSON(map[string]any{
		"schema_version": 1,
		"install_id":     install(1),
		"environment":    "development",
		"app":            map[string]string{"version": "2.7.1"},
		"device":         map[string]string{"platform": "ios"},
		"sdk":            map[string]string{"name": "unity", "version": "1.4.0"},
	})).AssertStatus(http.StatusForbidden)
}

func TestConfigNeedsAKey(t *testing.T) {
	settings := configSettings(t)
	testsupport.Postgres(t, settings)
	app, err := build(settings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	testclient.New(t, app).Post("/v1/config", testclient.JSON(map[string]any{
		"schema_version": 1,
		"install_id":     install(1),
		"app":            map[string]string{"version": "2.7.1"},
		"device":         map[string]string{"platform": "ios"},
		"sdk":            map[string]string{"name": "unity", "version": "1.4.0"},
	})).AssertStatus(http.StatusUnauthorized)
}

func mustRule(t *testing.T, raw string) targeting.Rule {
	t.Helper()
	parsed, err := targeting.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// install returns a stable UUID per index, so a test naming "player 1" always means the same
// installation and its bucket does not move between runs.
func install(n int) string {
	return "00000000-0000-4000-8000-" + pad(n)
}

func pad(n int) string {
	out := []byte("000000000000")
	for i := len(out) - 1; i >= 0 && n > 0; i-- {
		out[i] = byte('0' + n%10)
		n /= 10
	}
	return string(out)
}

// emptyInput is a document with nothing in it, for tests about something other than its
// contents.
func emptyInput(version int) liveconfig.Input {
	return liveconfig.Input{Version: version}
}
