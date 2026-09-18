// Package firebase verifies a Firebase ID token without the Admin SDK.
//
// Firebase Auth owns authentication — passwords, social providers, magic links — and hands the
// browser an ID token. This package is the other end: it establishes that the token in an
// Authorization header really was issued by Firebase for this project, and turns it into the
// [identity.Claims] the rest of the backend already takes as "somebody has already proved
// this".
//
// # Why not a JWT library
//
// Google's instructions say to find a third-party JWT library, which is sound advice for a
// language whose standard library cannot verify RS256. Go's can — `crypto/rsa`,
// `crypto/x509`, `encoding/base64` — so the dependency would buy parsing and a validation
// framework we do not want:
//
// **Almost every JWT vulnerability is a library dispatching on the header's `alg`.** `alg:
// "none"` is accepted as unsigned; `alg: "HS256"` is verified with an RSA *public* key as the
// HMAC secret, which the attacker also has. Both exist because the token tells the library how
// to check the token. Here RS256 is the only algorithm implemented: `alg` is *compared* and
// never *obeyed*, so neither attack has anywhere to land. Both are tested anyway.
//
// The cost is that this file has to be right, which is why the signature is verified before
// the payload is parsed and every claim in Google's table is checked below.
//
// # What is deliberately not here
//
// **Session cookies, custom tokens, revocation checks and user management.** Verifying a
// revoked token's session requires a call to Google per request; Firebase's own SDKs make it
// opt-in for that reason. A one-hour ID token lifetime is the bound instead, and it is the
// same bound the SDKs' default gives.
package firebase

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CertificateURL is where Google publishes the public halves of the keys it signs ID tokens
// with, as PEM x509 certificates keyed by `kid`.
const CertificateURL = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"

// Defaults for the key source.
const (
	// FallbackTTL is used when the response carries no usable Cache-Control.
	//
	// Short enough that a rotation is picked up the same hour, long enough that the
	// endpoint is not hit per request. It is a fallback: Google does send max-age, and
	// honouring it is what Google's instructions ask for.
	FallbackTTL = time.Hour

	// MinTTL floors whatever max-age says.
	//
	// A max-age of zero or a handful of seconds — which a proxy, a captive portal or a
	// bad day at Google can produce — would turn every sign-in into an outbound HTTPS
	// request and make Google's availability our availability.
	MinTTL = 5 * time.Minute

	// MaxKeys bounds what one response may define, so a wrong or hostile body cannot grow
	// the map without limit. Google publishes two or three.
	MaxKeys = 20

	// MaxBody bounds the response read. Three certificates are a few kilobytes.
	MaxBody = 1 << 20
)

// KeySource holds Google's signing certificates and refreshes them when they go stale.
//
// # Stale keys are served rather than failing
//
// If the refresh fails, the keys already held keep being used. They are public certificates
// for an issuer that rotates rather than revokes, so an old one verifies the tokens it signed
// and nothing else — while refusing to serve them would mean Google's endpoint having a bad
// minute logs out every dashboard in the product.
//
// The refresh is therefore started in the background and the request is answered from what is
// already there. Only the very first fetch blocks, and [KeySource.Start] does that at boot so
// that a misconfigured deployment fails before it accepts traffic rather than on somebody's
// first sign-in.
type KeySource struct {
	// URL is where the certificates come from. Overridden only by tests.
	URL string

	// HTTP is the client used. Nil means a client with a short timeout, because this call
	// sits in front of a sign-in and a hung connection is worse than a stale key.
	HTTP *http.Client

	// Now is the clock, overridden by tests.
	Now func() time.Time

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	expires time.Time

	// refreshing is the whole of the single-flight. A refresh is idempotent and cheap, so
	// the only thing worth preventing is a thundering herd of them.
	refreshing atomic.Bool

	// Refreshes and Failures are counters a metric or a test can read.
	Refreshes atomic.Int64
	Failures  atomic.Int64
}

// NewKeySource returns a source pointed at Google.
func NewKeySource() *KeySource {
	return &KeySource{URL: CertificateURL}
}

func (k *KeySource) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

func (k *KeySource) client() *http.Client {
	if k.HTTP != nil {
		return k.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (k *KeySource) url() string {
	if k.URL != "" {
		return k.URL
	}
	return CertificateURL
}

// Start fetches the certificates once, so that a deployment that cannot reach Google fails
// while it is starting rather than when somebody tries to sign in.
func (k *KeySource) Start(ctx context.Context) error {
	if err := k.refresh(ctx); err != nil {
		return fmt.Errorf("fetch Google's signing certificates: %w", err)
	}
	return nil
}

// Key returns the public key for a `kid`.
//
// An unknown kid triggers one synchronous refresh before it is refused, because that is
// exactly what a rotation looks like from here: a token signed with a key published after our
// last fetch. Doing it only for an unknown kid keeps a stream of garbage kids from turning
// into a stream of outbound requests — the refresh is a no-op while the cache is fresh.
func (k *KeySource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	key, fresh := k.lookup(kid)
	if key != nil && fresh {
		return key, nil
	}

	if !fresh {
		if key == nil {
			// Nothing cached at all: the first request, or every refresh so far has
			// failed. This is the one case worth blocking for.
			if err := k.refresh(ctx); err != nil {
				return nil, err
			}
		} else {
			// Stale but usable. Answer from it and refresh behind the request.
			k.refreshInBackground()
			return key, nil
		}
	}

	if key, _ := k.lookup(kid); key != nil {
		return key, nil
	}

	// Fresh keys that do not include this kid. One more fetch, in case a rotation landed
	// between our last one and this token being minted.
	if err := k.refresh(ctx); err != nil {
		return nil, err
	}
	if key, _ := k.lookup(kid); key != nil {
		return key, nil
	}
	return nil, &UnknownKeyError{KeyID: kid}
}

func (k *KeySource) lookup(kid string) (key *rsa.PublicKey, fresh bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.keys[kid], k.now().Before(k.expires)
}

func (k *KeySource) refreshInBackground() {
	if !k.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer k.refreshing.Store(false)
		// Its own context and its own deadline: the request that noticed the staleness
		// has already been answered and may be long gone.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = k.refresh(ctx)
	}()
}

func (k *KeySource) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url(), nil)
	if err != nil {
		k.Failures.Add(1)
		return err
	}

	response, err := k.client().Do(request)
	if err != nil {
		k.Failures.Add(1)
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		k.Failures.Add(1)
		return fmt.Errorf("%s answered %s", k.url(), response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, MaxBody))
	if err != nil {
		k.Failures.Add(1)
		return err
	}

	var certificates map[string]string
	if err := json.Unmarshal(body, &certificates); err != nil {
		k.Failures.Add(1)
		return fmt.Errorf("the certificate document is not the expected JSON object: %w", err)
	}
	if len(certificates) == 0 {
		k.Failures.Add(1)
		return fmt.Errorf("the certificate document defines no keys")
	}
	if len(certificates) > MaxKeys {
		k.Failures.Add(1)
		return fmt.Errorf("the certificate document defines %d keys, more than the %d allowed",
			len(certificates), MaxKeys)
	}

	keys := make(map[string]*rsa.PublicKey, len(certificates))
	for kid, encoded := range certificates {
		key, err := publicKey(encoded)
		if err != nil {
			k.Failures.Add(1)
			return fmt.Errorf("certificate %q: %w", kid, err)
		}
		keys[kid] = key
	}

	k.mu.Lock()
	k.keys = keys
	k.expires = k.now().Add(ttl(response.Header.Get("Cache-Control")))
	k.mu.Unlock()

	k.Refreshes.Add(1)
	return nil
}

// publicKey pulls the RSA public key out of one PEM certificate.
func publicKey(encoded string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, fmt.Errorf("not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not an x509 certificate: %w", err)
	}
	key, isRSA := certificate.PublicKey.(*rsa.PublicKey)
	if !isRSA {
		// Refused rather than skipped. Google signs ID tokens with RSA; a certificate
		// holding anything else means this is not the document we think it is, and
		// quietly ignoring it would leave a key set that is silently incomplete.
		return nil, fmt.Errorf("holds a %T, not an RSA key", certificate.PublicKey)
	}
	return key, nil
}

// ttl reads max-age out of a Cache-Control header, as Google's instructions ask.
//
// Hand-parsed rather than pulled from a library because the header we care about has one
// directive in it, and the failure mode of getting it wrong is bounded on both sides by
// MinTTL and FallbackTTL.
func ttl(cacheControl string) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "max-age") {
			continue
		}
		seconds, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
		if err != nil || seconds <= 0 {
			break
		}
		if age := time.Duration(seconds) * time.Second; age > MinTTL {
			return age
		}
		return MinTTL
	}
	return FallbackTTL
}

// Len reports how many keys are held, for a metric or a test.
func (k *KeySource) Len() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.keys)
}
