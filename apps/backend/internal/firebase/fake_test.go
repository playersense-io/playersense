package firebase_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// google stands in for Google's certificate endpoint and its signing keys.
//
// A real key and a real signature, not a stub: the whole point of these tests is the
// cryptography, so anything that faked the verification would be testing nothing.
type google struct {
	t *testing.T

	keyID string
	key   *rsa.PrivateKey

	server *httptest.Server

	// maxAge is what the endpoint puts in Cache-Control, and requests counts how many
	// times it has been asked — which is what the cache tests read.
	maxAge   atomic.Int64
	requests atomic.Int64

	// down makes the endpoint fail, for the stale-keys test.
	down atomic.Bool

	// certificates is what the endpoint currently serves, so a test can rotate keys.
	certificates atomic.Pointer[map[string]string]
}

func newGoogle(t *testing.T) *google {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}

	g := &google{t: t, keyID: "kid-one", key: key}
	g.maxAge.Store(21600) // six hours, which is roughly what Google sends
	g.publish(map[string]string{g.keyID: certificateFor(t, key)})

	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		g.requests.Add(1)
		if g.down.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control",
			fmt.Sprintf("public, max-age=%d, must-revalidate, no-transform", g.maxAge.Load()))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(*g.certificates.Load())
	}))
	t.Cleanup(g.server.Close)
	return g
}

func (g *google) publish(certificates map[string]string) {
	g.certificates.Store(&certificates)
}

// certificateFor wraps a public key in the self-signed PEM certificate Google's endpoint
// serves.
func certificateFor(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "securetoken.google.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create a certificate: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// claims is a token body a test can bend one field of at a time.
type claims map[string]any

// validClaims is a token Firebase would have issued a moment ago.
func validClaims(projectID string, now time.Time) claims {
	return claims{
		"iss":            "https://securetoken.google.com/" + projectID,
		"aud":            projectID,
		"sub":            "firebase-uid-123",
		"iat":            now.Add(-time.Minute).Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"auth_time":      now.Add(-time.Minute).Unix(),
		"email":          "ada@studio.example",
		"email_verified": true,
		"name":           "Ada Lovelace",
		"picture":        "https://example.test/ada.png",
		"firebase":       map[string]any{"sign_in_provider": "google.com"},
	}
}

// sign mints a real RS256 token with the given header and body.
func (g *google) sign(head map[string]any, body claims) string {
	g.t.Helper()
	signing := segment(g.t, head) + "." + segment(g.t, body)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, digest[:])
	if err != nil {
		g.t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// token mints a valid token with the ordinary header.
func (g *google) token(body claims) string {
	return g.sign(map[string]any{"alg": "RS256", "kid": g.keyID, "typ": "JWT"}, body)
}

func segment(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode a segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}
