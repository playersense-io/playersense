package firebase_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/firebase"
)

const projectID = "playersense-dev"

func verifier(t *testing.T, g *google, now time.Time) *firebase.Verifier {
	t.Helper()
	return &firebase.Verifier{
		ProjectID: projectID,
		Keys:      &firebase.KeySource{URL: g.server.URL, HTTP: g.server.Client()},
		Now:       func() time.Time { return now },
	}
}

// The whole point, once: a token Firebase really issued produces the claims the rest of the
// backend acts on.
func TestAGenuineTokenVerifies(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)

	got, err := verifier(t, g, now).Verify(t.Context(), g.token(validClaims(projectID, now)))
	if err != nil {
		t.Fatalf("a valid token was refused: %v", err)
	}

	if got.UID != "firebase-uid-123" {
		t.Errorf("uid is %q", got.UID)
	}
	if got.Email != "ada@studio.example" || !got.EmailVerified {
		t.Errorf("email is %q, verified=%v", got.Email, got.EmailVerified)
	}
	if got.Name != "Ada Lovelace" || got.AvatarURL != "https://example.test/ada.png" {
		t.Errorf("name=%q avatar=%q", got.Name, got.AvatarURL)
	}
	// `firebase.sign_in_provider` is nested, so this is the claim most likely to be read
	// from the wrong place and silently come back empty.
	if got.Provider != "google.com" {
		t.Errorf("provider is %q, want google.com", got.Provider)
	}
	if err := got.Valid(); err != nil {
		t.Errorf("the claims are not usable: %v", err)
	}
}

// The two attacks that exist because libraries let the token choose how it is checked. Neither
// has anywhere to land here, and both are tested rather than argued.
func TestTheTokenDoesNotChooseTheAlgorithm(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)
	v := verifier(t, g, now)
	body := validClaims(projectID, now)

	t.Run("alg none, unsigned", func(t *testing.T) {
		unsigned := segment(t, map[string]any{"alg": "none", "kid": g.keyID, "typ": "JWT"}) +
			"." + segment(t, body) + "."
		if _, err := v.Verify(t.Context(), unsigned); err == nil {
			t.Fatal("an unsigned token verified")
		} else if !strings.Contains(err.Error(), "none") {
			t.Errorf("the refusal does not name the algorithm: %v", err)
		}
	})

	t.Run("alg HS256, signed with the public key as the secret", func(t *testing.T) {
		// The classic confusion: the RSA public key is public, so if the verifier can be
		// talked into treating it as an HMAC secret, anybody can mint tokens.
		head := segment(t, map[string]any{"alg": "HS256", "kid": g.keyID, "typ": "JWT"})
		signing := head + "." + segment(t, body)

		public := certificateFor(t, g.key)
		mac := hmac.New(sha256.New, []byte(public))
		mac.Write([]byte(signing))
		forged := signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

		if _, err := v.Verify(t.Context(), forged); err == nil {
			t.Fatal("an HMAC-signed token verified against an RSA verifier")
		}
	})
}

// A signature over a different body, and a body swapped under a real signature. Both are what
// "verify the signature before parsing the payload" exists for.
func TestATamperedTokenIsRefused(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)
	v := verifier(t, g, now)

	genuine := g.token(validClaims(projectID, now))
	parts := strings.Split(genuine, ".")

	elevated := validClaims(projectID, now)
	elevated["sub"] = "somebody-else"
	swapped := parts[0] + "." + segment(t, elevated) + "." + parts[2]

	if _, err := v.Verify(t.Context(), swapped); err == nil {
		t.Fatal("a token whose payload was replaced verified")
	}

	// And a single flipped character in the signature.
	corrupt := parts[0] + "." + parts[1] + "." + flipOne(parts[2])
	if _, err := v.Verify(t.Context(), corrupt); err == nil {
		t.Fatal("a token with a corrupted signature verified")
	}
}

// A token from another Firebase project is signed by the same Google keys and is entirely
// genuine — anybody can create a Firebase project. The audience and issuer checks are the only
// things between such a token and an account here.
//
// The three cases are separated on purpose. Google mints both claims together, so one test
// with both wrong passes even if only one check exists — which is exactly what happened here:
// deleting the audience comparison left every assertion green, because the issuer check was
// catching it. Each check now has a case that only it can refuse.
func TestATokenForAnotherProjectIsRefused(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)
	v := verifier(t, g, now)

	t.Run("both claims foreign, which is what Google would really mint", func(t *testing.T) {
		if _, err := v.Verify(t.Context(), g.token(validClaims("somebody-elses-project", now))); err == nil {
			t.Fatal("a token minted for another project verified")
		}
	})

	t.Run("only the audience is foreign", func(t *testing.T) {
		body := validClaims(projectID, now)
		body["aud"] = "somebody-elses-project"
		_, err := v.Verify(t.Context(), g.token(body))
		if err == nil {
			t.Fatal("a token addressed to another project verified")
		}
		if !strings.Contains(err.Error(), "issued for") {
			t.Errorf("refused for the wrong reason, so the audience check is untested: %v", err)
		}
	})

	t.Run("only the issuer is foreign", func(t *testing.T) {
		body := validClaims(projectID, now)
		body["iss"] = "https://securetoken.google.com/somebody-elses-project"
		_, err := v.Verify(t.Context(), g.token(body))
		if err == nil {
			t.Fatal("a token from a foreign issuer verified")
		}
		if !strings.Contains(err.Error(), "issued by") {
			t.Errorf("refused for the wrong reason, so the issuer check is untested: %v", err)
		}
	})

	t.Run("an issuer that merely starts with the right one", func(t *testing.T) {
		// The prefix attack a HasPrefix would let through.
		body := validClaims(projectID, now)
		body["iss"] = "https://securetoken.google.com/" + projectID + ".evil.example"
		if _, err := v.Verify(t.Context(), g.token(body)); err == nil {
			t.Fatal("an issuer with the project id as a prefix verified")
		}
	})
}

// Google's payload table, one row at a time.
func TestTheTimeAndSubjectClaims(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)
	v := verifier(t, g, now)

	for _, c := range []struct {
		name  string
		bend  func(claims)
		wants string
	}{
		{"expired an hour ago", func(c claims) {
			c["exp"] = now.Add(-time.Hour).Unix()
		}, "expired"},
		{"expiring exactly now", func(c claims) {
			// Not in the future, so not acceptable — the boundary, which is where an
			// off-by-one lives.
			c["exp"] = now.Add(-firebase.Leeway).Unix()
		}, "expired"},
		{"no exp at all", func(c claims) { delete(c, "exp") }, "expired"},
		{"issued tomorrow", func(c claims) {
			c["iat"] = now.Add(24 * time.Hour).Unix()
		}, "future"},
		{"no iat at all", func(c claims) { delete(c, "iat") }, "future"},
		{"signed in tomorrow", func(c claims) {
			c["auth_time"] = now.Add(24 * time.Hour).Unix()
		}, "has not happened yet"},
		{"no auth_time at all", func(c claims) { delete(c, "auth_time") }, "has not happened yet"},
		{"empty subject", func(c claims) { c["sub"] = "" }, "no subject"},
		{"whitespace subject", func(c claims) { c["sub"] = "   " }, "no subject"},
		{"no subject at all", func(c claims) { delete(c, "sub") }, "no subject"},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := validClaims(projectID, now)
			c.bend(body)
			_, err := v.Verify(t.Context(), g.token(body))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("the refusal does not explain itself: %v", err)
			}
		})
	}

	// Clock drift inside the tolerance is not a refusal. A server a few seconds ahead of
	// Google must not reject every token in the first moments of its life.
	drifted := validClaims(projectID, now)
	drifted["iat"] = now.Add(30 * time.Second).Unix()
	drifted["auth_time"] = now.Add(30 * time.Second).Unix()
	if _, err := v.Verify(t.Context(), g.token(drifted)); err != nil {
		t.Errorf("half a minute of clock drift was treated as a forgery: %v", err)
	}
}

// Malformed input must be refused cheaply and without panicking, because this function is the
// first thing an unauthenticated request touches.
func TestMalformedTokens(t *testing.T) {
	now := time.Now()
	g := newGoogle(t)
	v := verifier(t, g, now)
	genuine := g.token(validClaims(projectID, now))

	for _, c := range []struct{ name, token string }{
		{"empty", ""},
		{"no dots", "not-a-token"},
		{"one dot", "aaa.bbb"},
		{"four segments", genuine + ".extra"},
		{"header is not base64", "!!!." + strings.SplitN(genuine, ".", 2)[1]},
		{"header is not JSON", base64.RawURLEncoding.EncodeToString([]byte("hello")) +
			"." + strings.SplitN(genuine, ".", 2)[1]},
		{"no kid", func() string {
			g2 := newGoogle(t)
			return g2.sign(map[string]any{"alg": "RS256"}, validClaims(projectID, now))
		}()},
		{"typ is not JWT", func() string {
			return g.sign(map[string]any{"alg": "RS256", "kid": g.keyID, "typ": "JWE"},
				validClaims(projectID, now))
		}()},
		{"implausibly long", strings.Repeat("a", 9<<10)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := v.Verify(t.Context(), c.token); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// A token signed by a key Google does not publish. After a refresh it is a forgery rather than
// a rotation we missed, so it is the caller's problem and not an outage.
func TestATokenSignedByAStrangerIsRefused(t *testing.T) {
	now := time.Now()
	real := newGoogle(t)
	stranger := newGoogle(t)

	// Signed by the stranger's key, but naming the real key's id, which is the shape an
	// attacker would actually try.
	forged := stranger.sign(
		map[string]any{"alg": "RS256", "kid": real.keyID, "typ": "JWT"},
		validClaims(projectID, now))

	_, err := verifier(t, real, now).Verify(t.Context(), forged)
	if err == nil {
		t.Fatal("a token signed by a stranger verified")
	}
	if !firebase.Rejected(err) {
		t.Errorf("a forgery was classified as an outage: %v", err)
	}

	// And an id nobody has ever published.
	unknown := stranger.sign(
		map[string]any{"alg": "RS256", "kid": "kid-that-never-existed", "typ": "JWT"},
		validClaims(projectID, now))
	_, err = verifier(t, real, now).Verify(t.Context(), unknown)
	var unknownKey *firebase.UnknownKeyError
	if !errors.As(err, &unknownKey) {
		t.Errorf("want an unknown-key error, got %v", err)
	}
	if !firebase.Rejected(err) {
		t.Error("an unknown key id was classified as an outage")
	}
}

func flipOne(segment string) string {
	runes := []rune(segment)
	if runes[0] == 'A' {
		runes[0] = 'B'
	} else {
		runes[0] = 'A'
	}
	return string(runes)
}
