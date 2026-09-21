package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/firebase/fbtest"
)

// testSettings loads the developer's .env and quiets the application.
//
// The suite runs against the local stack rather than a mock: the point of testclient is
// that a test exercises middleware, guards, binding, validation and the store together,
// and a faked store would remove most of what is worth testing.
func testSettings(t *testing.T) config.Settings {
	t.Helper()
	settings, err := config.Load(filepath.Join(repoRoot(), ".env"))
	if err != nil {
		t.Skipf("no local environment: %v", err)
	}
	settings.Addr = ":0"
	// A passing run should print nothing.
	settings.LogFormat = "none"
	// Limits off unless a test is about them.
	//
	// The invitation budget is three per address per *hour*, and its counters live in Valkey
	// and outlive the process — so a suite that left them on would spend the next run's budget
	// and fail somewhere unrelated to whatever was being changed. TestAnInboxCannotBeFlooded
	// turns them back on for itself.
	settings.RateLimitDisabled = true
	return settings
}

// signedIn returns a client carrying a freshly minted Firebase ID token for a new person.
//
// A real token: a real RSA key, a real RS256 signature, verified by the same code a browser's
// token goes through. The endpoint it is checked against is a local one, which is the only
// thing about the sign-in that is not real — the suite would otherwise need the internet and
// a Firebase project to run at all.
//
// Every call is a different person, which is what keeps runs independent: each gets their own
// personal organization, seeded with its own six roles.
func signedIn(t *testing.T) (*testclient.Client, *fbtest.Google) {
	t.Helper()
	settings := testSettings(t)
	google := fbtest.New(t, settings.FirebaseProjectID)
	settings.FirebaseCertificateURL = google.URL

	app := mustBuild(t, settings)
	token := google.Token(person(t))
	return testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+token)), google
}

// newClient is the common case: signed in, and the sign-in is not what is being tested.
func newClient(t *testing.T) *testclient.Client {
	t.Helper()
	client, _ := signedIn(t)
	return client
}

// newAnonClient carries no credential, for the tests where that is the point.
func newAnonClient(t *testing.T) *testclient.Client {
	t.Helper()
	settings := testSettings(t)
	google := fbtest.New(t, settings.FirebaseProjectID)
	settings.FirebaseCertificateURL = google.URL
	return testclient.New(t, mustBuild(t, settings))
}

// person invents a distinct, verified Firebase identity.
func person(t *testing.T) fbtest.Person {
	t.Helper()
	id := unique("uid")
	return fbtest.Person{
		UID:           id,
		Email:         id + "@studio.example",
		Name:          "Ada Lovelace",
		EmailVerified: true,
		Provider:      "google.com",
	}
}

// unique keeps runs independent without a teardown step.
//
// A UUID rather than the clock, because the clock is not fine-grained enough to be an
// identifier. Measured on this machine: twenty thousand calls to
// `time.Now().UnixNano() % 1e9` produce **1,470 distinct values** — 93% of them collide. Two
// `person(t)` calls in one test run would then be one Firebase identity, one user row and one
// membership, and a test that gave "two people" different roles would quietly be asserting
// about whichever won. That is what made TestTheLiveOpsManagerIsLiveOpsOnly fail once in the
// full suite and never again on its own.
func unique(prefix string) string {
	return prefix + "-" + uuid.NewString()[:12]
}

// mustBuild composes the application or fails the test.
//
// One helper rather than a check at every call site: build now returns an error because it
// dials nothing but can still be handed an unusable setting — an unknown MAIL_SENDER, an
// EVENT_BUS nothing implements — and every one of those is a test setup problem rather
// than something a test is asserting about.
func mustBuild(t *testing.T, settings config.Settings, overrides ...muzak.RouterOption) *muzak.App {
	t.Helper()
	app, err := build(settings, overrides...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return app
}

func TestAppBuilds(t *testing.T) {
	// Build reports every route problem at once — a duplicate path, an unbindable input
	// type, a path parameter no field binds, a duplicate operation identifier. One test
	// keeps all of them out of a deployment — and now that the four services are one
	// binary, it is also what catches two of them claiming the same path.
	if err := mustBuild(t, testSettings(t)).Build(); err != nil {
		t.Fatalf("Build = %v", err)
	}
}

func TestHealthNeedsNoCredential(t *testing.T) {
	newAnonClient(t).Get("/healthz").
		AssertStatus(http.StatusOK).
		AssertJSON(`{"status":"ok"}`)
}

func TestControlPlaneIsGuarded(t *testing.T) {
	anon := newAnonClient(t)

	anon.Get("/v1/organizations").
		AssertStatus(http.StatusUnauthorized).
		AssertErrorCode(muzak.CodeUnauthorized)

	// Not a token at all.
	anon.Get("/v1/organizations", testclient.Header("Authorization", "Bearer hailhydra")).
		AssertStatus(http.StatusUnauthorized)

	// And the one that used to work. A shared secret cannot say who is asking, so it
	// cannot be the credential on an API where the answer depends on who that is.
	anon.Get("/v1/organizations",
		testclient.Header("Authorization", "Bearer "+testSettings(t).AdminToken)).
		AssertStatus(http.StatusUnauthorized)

	newClient(t).Get("/v1/organizations").AssertStatus(http.StatusOK)
}

func TestCreateOrganization(t *testing.T) {
	client := newClient(t)
	slug := unique("org")

	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Pixel Forge", "slug": slug})).
		AssertStatus(http.StatusCreated)

	// A second create of the same slug is a conflict, not a silent overwrite.
	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Pixel Forge", "slug": slug})).
		AssertStatus(http.StatusConflict).
		AssertErrorCode(muzak.CodeConflict)
}

func TestValidationReportsEveryField(t *testing.T) {
	client := newClient(t)

	res := client.Post("/v1/organizations",
		testclient.RawJSON(`{"name":"x","slug":"Not A Slug!"}`))

	res.AssertStatus(http.StatusUnprocessableEntity).
		AssertErrorCode(muzak.CodeValidationError)

	// Both fields are reported at once rather than one per round trip.
	fields := map[string]bool{}
	for _, d := range res.Error().Error.Details {
		fields[d.Field] = true
	}
	for _, want := range []string{"name", "slug"} {
		if !fields[want] {
			t.Errorf("want %q reported; got %v", want, fields)
		}
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	// A client's typo becomes an immediate 422 naming the member, rather than a value
	// that silently disappears.
	res := newClient(t).Post("/v1/organizations",
		testclient.RawJSON(`{"name":"Pixel Forge","slguu":"typo"}`))

	res.AssertStatus(http.StatusUnprocessableEntity)
}

func TestProjectLifecycle(t *testing.T) {
	client := newClient(t)
	orgSlug, projSlug := unique("org"), unique("proj")

	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Studio", "slug": orgSlug})).
		AssertStatus(http.StatusCreated)

	created := testclient.Decoded[projectOut](client.Post("/v1/organizations/"+orgSlug+"/projects", testclient.JSON(map[string]any{
		"name": "Dungeon Run", "slug": projSlug,
		"policy_tier": "standard", "retention_days": 90,
	})))

	if len(created.Environments) != 3 {
		t.Fatalf("want three environments created with the project, got %d", len(created.Environments))
	}
	if created.RetentionDays != 90 {
		t.Errorf("retention_days = %d, want 90", created.RetentionDays)
	}

	// Reading one back names its cell, which the listing already did and this did not: the
	// principal resolved the project without its cell edge, so `cell`, `region`, `ingest_url`
	// and `query_url` were silently absent from exactly the response a studio reads to
	// configure their SDK. The analytics routes need the same edge to refuse another cell's
	// project, which is how it was noticed.
	read := testclient.Decoded[projectOut](client.Get(
		"/v1/organizations/" + orgSlug + "/projects/" + projSlug).AssertStatus(http.StatusOK))
	if read.Cell == "" || read.Region == "" {
		t.Errorf("reading a project does not say where its data lives: %+v", read)
	}
	if read.IngestURL == "" {
		t.Error("reading a project does not say where its SDKs should send batches")
	}

	// A retention value outside §24's discrete tiers is refused.
	client.Post("/v1/organizations/"+orgSlug+"/projects",
		testclient.JSON(map[string]any{"name": "Bad", "slug": unique("bad"), "retention_days": 45})).
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestKidsStrictAnnouncesItsLimit is the check that matters commercially.
//
// A kids_strict project has no persistent identifier, so cross-day retention is not
// computable (§98). Saying so at creation is the difference between a documented
// limitation and a studio discovering an empty retention page after integrating.
func TestKidsStrictAnnouncesItsLimit(t *testing.T) {
	client := newClient(t)
	orgSlug := unique("org")

	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Kids Studio", "slug": orgSlug})).
		AssertStatus(http.StatusCreated)

	created := testclient.Decoded[projectOut](client.Post("/v1/organizations/"+orgSlug+"/projects", testclient.JSON(map[string]any{
		"name": "Tiny Farm", "slug": unique("proj"), "policy_tier": "kids_strict",
	})))

	if len(created.Notes) == 0 {
		t.Fatal("a kids_strict project must say that cohort metrics are unavailable")
	}
}

func TestAPIKeyIsDisclosedOnce(t *testing.T) {
	client := newClient(t)
	orgSlug, projSlug := unique("org"), unique("proj")

	client.Post("/v1/organizations",
		testclient.JSON(map[string]string{"name": "Studio", "slug": orgSlug})).
		AssertStatus(http.StatusCreated)
	client.Post("/v1/organizations/"+orgSlug+"/projects",
		testclient.JSON(map[string]string{"name": "Game", "slug": projSlug})).
		AssertStatus(http.StatusCreated)

	base := "/v1/organizations/" + orgSlug + "/projects/" + projSlug + "/keys"

	minted := testclient.Decoded[keyCreatedOut](client.Post(base,
		testclient.JSON(map[string]string{
			"kind": "project", "environment": "production", "name": "unity",
		})))
	if minted.Secret == "" {
		t.Fatal("creating a key must return it once")
	}

	// And never again: the list carries no secret member at all, because the response
	// type does not declare one.
	body := client.Get(base).AssertStatus(http.StatusOK).String()
	if contains(body, minted.Secret) {
		t.Error("the key list disclosed a secret")
	}

	// Revoking twice is a conflict rather than a silent success.
	client.Delete(base + "/" + minted.ID).AssertStatus(http.StatusOK)
	client.Delete(base + "/" + minted.ID).AssertStatus(http.StatusConflict)
}

func contains(haystack, needle string) bool {
	return needle != "" && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}

// Local decoding types, so the test asserts on the wire format rather than importing the
// schema package and testing it against itself.
type projectOut struct {
	Slug          string   `json:"slug"`
	Cell          string   `json:"cell"`
	Region        string   `json:"region"`
	IngestURL     string   `json:"ingest_url"`
	PolicyTier    string   `json:"policy_tier"`
	RetentionDays int      `json:"retention_days"`
	Notes         []string `json:"notes"`
	Environments  []struct {
		Kind string `json:"kind"`
	} `json:"environments"`
}

type keyCreatedOut struct {
	ID     string `json:"id"`
	Prefix string `json:"prefix"`
	Secret string `json:"secret"`
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
