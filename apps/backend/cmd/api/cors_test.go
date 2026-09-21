package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"muzak.dev/framework/testclient"
)

// The browser half of the API, which is the half no Go test exercises by accident.
//
// Every other test here speaks to the application directly, so it never sends an `Origin`
// header and never learns whether a browser would have been allowed to read the answer. A
// policy that is wrong in either direction fails silently: too narrow and the dashboard
// sees an opaque network error with nothing in the server log, too wide and any page on
// the internet can read a signed-in customer's data out of their browser.

const (
	allowedOrigin = "http://localhost:3001" // the client dashboard in development
	hostileOrigin = "https://evil.example"
)

// TestADashboardOriginMayReadTheAnswer is the policy working.
func TestADashboardOriginMayReadTheAnswer(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	response := client.Get("/healthz",
		testclient.Header("Origin", allowedOrigin)).
		AssertStatus(http.StatusOK)

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowedOrigin)
	}

	// The response varies by origin, so a shared cache must not serve one origin's
	// answer to another. Without it a CDN in front of this API is a cross-origin leak.
	//
	// Values rather than Get: the preflight path adds two more Vary headers, and Get
	// would read only the first of them.
	if vary := response.Header.Values("Vary"); !slices.Contains(vary, "Origin") {
		t.Errorf("Vary = %v, want it to name Origin", vary)
	}
}

// TestAnUnknownOriginIsNotToldItMayRead is the policy refusing.
//
// The request still *runs* — CORS is enforced by the browser, not by the server, and a
// non-browser client was never subject to it. What must not happen is the header that
// tells a browser to hand the body to script.
func TestAnUnknownOriginIsNotToldItMayRead(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	response := client.Get("/healthz",
		testclient.Header("Origin", hostileOrigin)).
		AssertStatus(http.StatusOK)

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("an unlisted origin was told it may read the response: %q", got)
	}
}

// TestAPreflightFromAnUnknownOriginIsRefused.
//
// The preflight is the whole of the protection for a mutating route: a browser will not
// send the real POST until it is answered. Answering one for an origin that is not allowed
// would let a hostile page make the request even if it could not read the reply, which for
// anything that changes state is most of the damage.
func TestAPreflightFromAnUnknownOriginIsRefused(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	client.Options("/v1/organizations",
		testclient.Header("Origin", hostileOrigin),
		testclient.Header("Access-Control-Request-Method", http.MethodPost)).
		AssertStatus(http.StatusForbidden)
}

// TestAPreflightFromTheDashboardNamesWhatItMaySend.
//
// The dashboard sends `Authorization: Bearer <firebase id token>` on every request, which
// is not a CORS-safelisted request header — so the preflight has to name it or every
// authenticated call fails before it is made.
func TestAPreflightFromTheDashboardNamesWhatItMaySend(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	response := client.Options("/v1/organizations",
		testclient.Header("Origin", allowedOrigin),
		testclient.Header("Access-Control-Request-Method", http.MethodPost),
		testclient.Header("Access-Control-Request-Headers", "Authorization")).
		AssertStatus(http.StatusNoContent)

	headers := response.Header.Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(headers), "authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to name Authorization", headers)
	}

	methods := response.Header.Get("Access-Control-Allow-Methods")
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if !strings.Contains(methods, method) {
			t.Errorf("Access-Control-Allow-Methods = %q, want it to name %s", methods, method)
		}
	}
}

// TestCredentialsAreNotAllowed pins the decision, because the field name invites the
// opposite one.
//
// `AllowCredentials` governs cookies and TLS client certificates, not the `Authorization`
// header — that one is an ordinary request header, permitted by `AllowedHeaders`. Both
// browser apps hold their token in client-side state and attach it themselves, so turning
// this on would buy nothing and widen what a mistake in the origin list costs.
//
// If a future change genuinely needs cookies, this test is where the decision is recorded
// and the place to argue with.
func TestCredentialsAreNotAllowed(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	response := client.Get("/healthz",
		testclient.Header("Origin", allowedOrigin)).
		AssertStatus(http.StatusOK)

	if got := response.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want it absent", got)
	}
}

// TestTheHeadersAClientMustReadAreExposed.
//
// A browser hands script only six response headers unless the server says otherwise, and
// every one of these is read by the dashboard or the SDK. `Retry-After` is the one that
// matters most: unexposed, a 429 tells a client to back off and gives it no way to find
// out for how long, which is how a rate limit becomes a retry storm.
func TestTheHeadersAClientMustReadAreExposed(t *testing.T) {
	client := testclient.New(t, mustBuild(t, testSettings(t)))

	response := client.Get("/healthz",
		testclient.Header("Origin", allowedOrigin)).
		AssertStatus(http.StatusOK)

	exposed := strings.Split(response.Header.Get("Access-Control-Expose-Headers"), ", ")
	for _, want := range []string{"Retry-After", "X-Request-Id", "ETag", "Content-Disposition"} {
		if !slices.Contains(exposed, want) {
			t.Errorf("%s is not exposed; a browser cannot read it. Exposed: %v", want, exposed)
		}
	}
}

// TestEveryConfiguredOriginIsAllowed reads the setting rather than a copy of it.
//
// The list is a deployment's to choose, so a test naming origins would only prove that the
// test and the code agree. This one asserts the weaker, truer thing: whatever is
// configured is what the policy honours, so a deployment that adds its production
// dashboard gets it without editing a test.
func TestEveryConfiguredOriginIsAllowed(t *testing.T) {
	settings := testSettings(t)
	if len(settings.CORSAllowedOrigins) == 0 {
		t.Fatal("CORS_ALLOWED_ORIGINS is empty, so no browser app can call this API")
	}

	client := testclient.New(t, mustBuild(t, settings))
	for _, origin := range settings.CORSAllowedOrigins {
		response := client.Get("/healthz", testclient.Header("Origin", origin)).
			AssertStatus(http.StatusOK)
		if got := response.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("configured origin %q was refused: Access-Control-Allow-Origin = %q", origin, got)
		}
	}
}
