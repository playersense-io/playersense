package firebase_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"playersense.dev/backend/internal/firebase"
)

// identityToolkit stands in for Firebase's authentication API.
type identityToolkit struct {
	calls atomic.Int64

	// answer is the error message to return, empty for success.
	answer atomic.Pointer[string]

	// seen is the last request body, so a test can assert what was actually sent.
	seen atomic.Pointer[map[string]string]

	// key is the API key the last request carried.
	key atomic.Pointer[string]

	server *httptest.Server
}

func newIdentityToolkit(t *testing.T) *identityToolkit {
	t.Helper()
	it := &identityToolkit{}
	it.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		it.calls.Add(1)

		key := r.URL.Query().Get("key")
		it.key.Store(&key)

		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		it.seen.Store(&body)

		if answer := it.answer.Load(); answer != nil && *answer != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": 400, "message": *answer},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"kind":  "identitytoolkit#GetOobConfirmationCodeResponse",
			"email": "ada@studio.example",
		})
	}))
	t.Cleanup(it.server.Close)
	return it
}

func (it *identityToolkit) sender() *firebase.Sender {
	return &firebase.Sender{
		APIKey: "AIzaTestKey", URL: it.server.URL, HTTP: it.server.Client(),
	}
}

// The request has to be the one the browser SDK makes, because the whole reason for using the
// REST API is that Firebase then sends the mail itself.
func TestAVerificationEmailIsRequestedByToken(t *testing.T) {
	it := newIdentityToolkit(t)

	if err := it.sender().SendVerification(t.Context(), "an-id-token"); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := *it.seen.Load()
	if body["requestType"] != "VERIFY_EMAIL" {
		t.Errorf("requestType is %q", body["requestType"])
	}
	// The token is the authorisation, and there is deliberately no email parameter: one
	// would be a way to make Firebase send mail to an address the caller does not hold a
	// token for.
	if body["idToken"] != "an-id-token" {
		t.Errorf("idToken is %q", body["idToken"])
	}
	if _, present := body["email"]; present {
		t.Error("the request names an email address, which Firebase should take from the token")
	}
	if got := *it.key.Load(); got != "AIzaTestKey" {
		t.Errorf("the API key was not sent: %q", got)
	}
}

// Firebase refuses the second request within its own window. That is the send having worked,
// not having failed, and a caller that showed a red banner for it would be reporting success
// as an error.
func TestBeingToldOneWentOutRecentlyIsNotAFailure(t *testing.T) {
	it := newIdentityToolkit(t)
	answer := "TOO_MANY_ATTEMPTS_TRY_LATER"
	it.answer.Store(&answer)

	err := it.sender().SendVerification(t.Context(), "an-id-token")
	if !errors.Is(err, firebase.ErrSentRecently) {
		t.Fatalf("want ErrSentRecently, got %v", err)
	}
}

// Everything else Firebase can say is a real failure and must keep its reason: "the API key is
// wrong" and "that token is not valid" send an operator to completely different places.
func TestOtherRefusalsKeepTheirReason(t *testing.T) {
	it := newIdentityToolkit(t)

	for _, message := range []string{"INVALID_ID_TOKEN", "API key not valid. Please pass a valid API key."} {
		answer := message
		it.answer.Store(&answer)

		err := it.sender().SendVerification(t.Context(), "an-id-token")
		if err == nil {
			t.Fatalf("%s was treated as success", message)
		}
		if errors.Is(err, firebase.ErrSentRecently) {
			t.Errorf("%s was treated as a recent send", message)
		}
		if !strings.Contains(err.Error(), message) {
			t.Errorf("the error loses the reason: %v", err)
		}
	}
}

// A deployment with no key configured must refuse clearly rather than make a request that
// cannot work.
func TestNoAPIKeyIsNotARequest(t *testing.T) {
	it := newIdentityToolkit(t)
	sender := it.sender()
	sender.APIKey = ""

	if err := sender.SendVerification(t.Context(), "an-id-token"); !errors.Is(err, firebase.ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	if it.calls.Load() != 0 {
		t.Error("a request was made with no API key")
	}
}

// An endpoint that is unreachable, or answers something that is not the expected JSON, is a
// failure with a reason — never a silent success that would leave somebody waiting for an
// email nobody sent.
func TestAnUnreadableAnswerIsAFailure(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>nginx</html>"))
	}))
	t.Cleanup(broken.Close)

	sender := &firebase.Sender{APIKey: "k", URL: broken.URL, HTTP: broken.Client()}
	err := sender.SendVerification(t.Context(), "an-id-token")
	if err == nil {
		t.Fatal("a 502 of HTML was treated as a sent email")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("the error does not say what happened: %v", err)
	}

	unreachable := &firebase.Sender{
		APIKey: "k",
		URL:    "http://" + unusedAddress(t) + "/v1",
		HTTP:   broken.Client(),
	}
	if err := unreachable.SendVerification(t.Context(), "an-id-token"); err == nil {
		t.Fatal("an unreachable endpoint was treated as a sent email")
	}
}

// unusedAddress returns a host:port nothing is listening on.
func unusedAddress(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	return parsed.Host
}
