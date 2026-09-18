// Package fbtest stands in for Firebase's signing keys, so that a test can sign in.
//
// A real RSA key and a real signature, served from a real HTTP endpoint. Nothing about the
// verification is stubbed: a test using this exercises the same code path a browser's token
// takes, which is the only version of this worth having — a fake that skipped the signature
// would leave the one part that matters untested.
//
// It lives in its own package because two suites need it: internal/firebase's own, and any
// service test that has to present a credential.
package fbtest

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
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// KeyID is the `kid` the fake endpoint publishes under.
const KeyID = "fbtest-key-1"

// Google is a stand-in for Google's certificate endpoint and its signing key.
type Google struct {
	t   *testing.T
	key *rsa.PrivateKey

	// URL is what a Verifier's KeySource should be pointed at.
	URL string

	// ProjectID is the Firebase project tokens are minted for.
	ProjectID string
}

// New starts one, shut down when the test ends.
func New(t *testing.T, projectID string) *Google {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}

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
	document := map[string]string{
		KeyID: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=21600, must-revalidate, no-transform")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)

	return &Google{t: t, key: key, URL: server.URL, ProjectID: projectID}
}

// Person is who a minted token says signed in.
type Person struct {
	UID           string
	Email         string
	Name          string
	EmailVerified bool

	// Provider is Firebase's own id: "password", "google.com", and so on.
	Provider string
}

// Token mints a valid ID token for one person, as Firebase would have a moment ago.
func (g *Google) Token(person Person) string {
	g.t.Helper()

	now := time.Now()
	if person.Provider == "" {
		person.Provider = "password"
	}
	return g.sign(map[string]any{
		"iss":            "https://securetoken.google.com/" + g.ProjectID,
		"aud":            g.ProjectID,
		"sub":            person.UID,
		"iat":            now.Add(-time.Minute).Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"auth_time":      now.Add(-time.Minute).Unix(),
		"email":          person.Email,
		"email_verified": person.EmailVerified,
		"name":           person.Name,
		"firebase":       map[string]any{"sign_in_provider": person.Provider},
	})
}

func (g *Google) sign(body map[string]any) string {
	g.t.Helper()
	head := segment(g.t, map[string]any{"alg": "RS256", "kid": KeyID, "typ": "JWT"})
	signing := head + "." + segment(g.t, body)

	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, digest[:])
	if err != nil {
		g.t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func segment(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode a segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}
