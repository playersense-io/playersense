package firebase_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"playersense.dev/backend/internal/firebase"
)

// Google's instructions: "Use the value of max-age in the Cache-Control header to know when to
// refresh the public keys." Without this the endpoint is called once per sign-in, which makes
// Google's availability our availability and their rate limit our ceiling.
func TestTheKeysAreCachedForMaxAge(t *testing.T) {
	g := newGoogle(t)
	now := time.Now()
	clock := func() time.Time { return now }

	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client(), Now: clock}
	v := &firebase.Verifier{ProjectID: projectID, Keys: keys, Now: clock}

	for range 50 {
		if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if fetched := g.requests.Load(); fetched != 1 {
		t.Errorf("fifty sign-ins cost %d fetches, want 1", fetched)
	}

	// Past max-age, the next verification refreshes. It is a background refresh, so the
	// request is answered from the keys already held and the fetch lands shortly after.
	now = now.Add(7 * time.Hour)
	if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
		t.Fatalf("verify after expiry: %v", err)
	}
	waitFor(t, func() bool { return g.requests.Load() == 2 })
}

// A max-age of zero — which a proxy, a captive portal or a bad minute at Google can produce —
// must not turn every sign-in into an outbound request.
func TestAnAbsurdMaxAgeIsFloored(t *testing.T) {
	g := newGoogle(t)
	g.maxAge.Store(1)

	now := time.Now()
	clock := func() time.Time { return now }
	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client(), Now: clock}
	v := &firebase.Verifier{ProjectID: projectID, Keys: keys, Now: clock}

	if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// A second past the header's max-age, but well inside the floor.
	now = now.Add(2 * time.Second)
	for range 20 {
		if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if fetched := g.requests.Load(); fetched != 1 {
		t.Errorf("a max-age of one second produced %d fetches, want 1", fetched)
	}
}

// Rotation is the ordinary case, not an incident: Google signs with a new key and publishes it
// under a new id. A token carrying an id we have not seen has to trigger one fetch rather than
// being refused, or every rotation would log the whole product out until the cache expired.
func TestARotationIsPickedUpImmediately(t *testing.T) {
	g := newGoogle(t)
	now := time.Now()
	clock := func() time.Time { return now }

	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client(), Now: clock}
	v := &firebase.Verifier{ProjectID: projectID, Keys: keys, Now: clock}

	if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
		t.Fatalf("before rotation: %v", err)
	}

	// Google rotates: a second key appears, still inside max-age, and signs the next token.
	next, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g.publish(map[string]string{
		g.keyID:   certificateFor(t, g.key),
		"kid-two": certificateFor(t, next),
	})
	previous := g.key
	g.key, g.keyID = next, "kid-two"
	defer func() { g.key = previous }()

	if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
		t.Fatalf("a token signed with a freshly rotated key was refused: %v", err)
	}
	if keys.Len() != 2 {
		t.Errorf("holding %d keys after a rotation, want 2", keys.Len())
	}
}

// The keys are public certificates for an issuer that rotates rather than revokes, so an old
// one still verifies what it signed. Refusing to serve them while Google has a bad minute
// would log out every dashboard in the product for a reason none of them can act on.
func TestStaleKeysAreServedWhenGoogleIsDown(t *testing.T) {
	g := newGoogle(t)
	now := time.Now()
	clock := func() time.Time { return now }

	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client(), Now: clock}
	v := &firebase.Verifier{ProjectID: projectID, Keys: keys, Now: clock}

	if err := keys.Start(t.Context()); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	g.down.Store(true)
	now = now.Add(7 * time.Hour) // past max-age, so every call now wants a refresh

	for range 5 {
		if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
			t.Fatalf("a sign-in failed while Google was unreachable: %v", err)
		}
	}
	waitFor(t, func() bool { return keys.Failures.Load() > 0 })

	// And it recovers on its own, without anything restarting.
	g.down.Store(false)
	if _, err := v.Verify(t.Context(), g.token(validClaims(projectID, now))); err != nil {
		t.Fatalf("verify: %v", err)
	}
	waitFor(t, func() bool { return keys.Refreshes.Load() >= 2 })
}

// Start exists so that a deployment which cannot reach Google fails while it is starting,
// rather than on the first person who tries to sign in an hour later.
func TestStartFailsLoudlyWhenTheCertificatesCannotBeFetched(t *testing.T) {
	g := newGoogle(t)
	g.down.Store(true)

	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client()}
	if err := keys.Start(t.Context()); err == nil {
		t.Fatal("a source that cannot reach its endpoint started successfully")
	}
}

// With nothing cached, an unreachable endpoint is an outage rather than a bad token — the
// distinction that decides whether a person is told to sign in again or told to wait.
func TestAnOutageIsNotABadToken(t *testing.T) {
	g := newGoogle(t)
	now := time.Now()
	g.down.Store(true)

	v := verifier(t, g, now)
	_, err := v.Verify(t.Context(), g.token(validClaims(projectID, now)))
	if err == nil {
		t.Fatal("verification succeeded with no keys")
	}
	if firebase.Rejected(err) {
		t.Errorf("an unreachable endpoint was reported as an invalid token: %v", err)
	}
}

// A certificate document that is not what we think it is must fail rather than leave a key set
// that is silently incomplete.
func TestARubbishCertificateDocumentIsRefused(t *testing.T) {
	g := newGoogle(t)
	g.publish(map[string]string{"kid-one": "-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----"})

	keys := &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client()}
	if err := keys.Start(t.Context()); err == nil {
		t.Fatal("a document of rubbish certificates was accepted")
	}
	if keys.Len() != 0 {
		t.Errorf("holding %d keys after a failed parse", keys.Len())
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the background refresh")
}
