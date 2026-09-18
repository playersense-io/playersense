package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/firebase/fbtest"
)

// firebaseMail records what the application asks Firebase to send.
type firebaseMail struct {
	sent   atomic.Int64
	tokens sync.Map
	server *httptest.Server
}

func newFirebaseMail(t *testing.T) *firebaseMail {
	t.Helper()
	m := &firebaseMail{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		if body["requestType"] == "VERIFY_EMAIL" {
			m.sent.Add(1)
			m.tokens.Store(body["idToken"], true)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "ada@studio.example"})
	}))
	t.Cleanup(m.server.Close)
	return m
}

// unverifiedStack builds the application with a fake Google and a fake Firebase mailer.
func unverifiedStack(t *testing.T) (config.Settings, *fbtest.Google, *firebaseMail) {
	t.Helper()
	settings := testSettings(t)
	google := fbtest.New(t, settings.FirebaseProjectID)
	mail := newFirebaseMail(t)

	settings.FirebaseCertificateURL = google.URL
	settings.FirebaseIdentityURL = mail.server.URL
	settings.FirebaseAPIKey = "AIzaTestKey"
	return settings, google, mail
}

// Somebody signs in with an address Firebase has not confirmed. They are refused, and a
// verification link is on its way — which is the whole of what the dashboard has to do about
// it: show the message.
func TestAnUnverifiedSignInIsSentALink(t *testing.T) {
	settings, google, mail := unverifiedStack(t)
	app := build(settings)

	who := person(t)
	who.EmailVerified = false
	client := testclient.New(t, app,
		testclient.WithHeader("Authorization", "Bearer "+google.Token(who)))

	// The request succeeds — they are signed in and can look around — and the link is on
	// its way. The dashboard shows the banner; the backend does not refuse to make that
	// happen.
	client.Get("/v1/organizations").AssertStatus(http.StatusOK)
	if got := mail.sent.Load(); got != 1 {
		t.Fatalf("Firebase was asked to send %d emails, want 1", got)
	}
}

// A person reloading the dashboard, or two tabs opening at once, must not produce an email per
// request. Firebase's own limit refuses the second within seconds, so without a throttle the
// product would be reporting a failure for the one thing that worked.
func TestOnlyOneLinkPerIntervalHoweverManyRequests(t *testing.T) {
	settings, google, mail := unverifiedStack(t)
	app := build(settings)

	who := person(t)
	who.EmailVerified = false
	token := google.Token(who)

	client := testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+token))
	for range 5 {
		client.Get("/v1/organizations").AssertStatus(http.StatusOK)
	}

	// And concurrently, which is the case a read-then-write throttle would get wrong.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+token)).
				Get("/v1/organizations")
		}()
	}
	wg.Wait()

	if got := mail.sent.Load(); got != 1 {
		t.Errorf("thirteen requests produced %d emails, want 1", got)
	}
}

// Two different people must each get their own link: the throttle is per account, not global.
func TestTheThrottleIsPerAccount(t *testing.T) {
	settings, google, mail := unverifiedStack(t)
	app := build(settings)

	for range 2 {
		who := person(t)
		who.EmailVerified = false
		testclient.New(t, app,
			testclient.WithHeader("Authorization", "Bearer "+google.Token(who))).
			Get("/v1/organizations").AssertStatus(http.StatusOK)
	}

	if got := mail.sent.Load(); got != 2 {
		t.Errorf("two people got %d emails, want 2", got)
	}
}

// A verified address is never emailed, whatever else is true.
func TestAVerifiedSignInIsNotEmailed(t *testing.T) {
	settings, google, mail := unverifiedStack(t)
	app := build(settings)

	testclient.New(t, app,
		testclient.WithHeader("Authorization", "Bearer "+google.Token(person(t)))).
		Get("/v1/organizations").AssertStatus(http.StatusOK)

	if got := mail.sent.Load(); got != 0 {
		t.Errorf("a verified account was sent %d verification emails", got)
	}
}
