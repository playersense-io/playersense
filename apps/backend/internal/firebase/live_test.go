package firebase_test

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"playersense.dev/backend/internal/firebase"
)

// Everything else in this package is tested against a key this suite generated, which proves
// the verification and proves nothing about the document Google actually serves.
//
// This is the other half: the real endpoint, parsed by the real parser. It is the test that
// fails if Google changes the shape of that document — a change that would otherwise be found
// in production, by every sign-in at once.
//
// # Unreachable is a skip, unparseable is a failure
//
// The distinction is the whole design of this test. A machine with no egress must not fail it,
// or it becomes the test everyone learns to ignore; a machine that *reached* Google and could
// not read the answer has found something real.
func TestGooglesLiveCertificateDocumentParses(t *testing.T) {
	keys := firebase.NewKeySource()
	keys.HTTP = &http.Client{Timeout: 10 * time.Second}

	if err := keys.Start(t.Context()); err != nil {
		if offline(err) {
			t.Skipf("no route to Google: %v", err)
		}
		t.Fatalf("Google answered and the document did not parse: %v", err)
	}

	// Google publishes two or three, occasionally four during a rotation. Zero would mean
	// the parser silently accepted an empty document, which is the failure that would make
	// every token unverifiable while looking healthy.
	if keys.Len() == 0 {
		t.Fatal("the live document parsed to no keys at all")
	}
	if keys.Len() > firebase.MaxKeys {
		t.Errorf("the live document holds %d keys, more than MaxKeys", keys.Len())
	}

	// A `kid` nobody has published is a rejection, not an outage — the classification the
	// request path uses to choose between telling somebody to sign in again and telling
	// them to wait.
	_, err := keys.Key(t.Context(), "a-kid-google-has-never-issued")
	if err == nil {
		t.Fatal("an invented key id resolved to a key")
	}
	if !firebase.Rejected(err) {
		t.Errorf("an invented key id was classified as an outage: %v", err)
	}
}

// offline reports whether an error is "the network could not be used" rather than "the answer
// was wrong". Only the first is a reason to skip.
func offline(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
