package firebase

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"playersense.dev/backend/internal/identity"
)

// Algorithm is the only one implemented, and the word "only" is the security property.
//
// It is compared against the token's header and never used to choose a verifier. A token
// saying `alg: "none"` or `alg: "HS256"` is refused here, before anything else happens, rather
// than routed to a different code path — which is the bug behind almost every JWT CVE.
const Algorithm = "RS256"

// Issuer is where a Firebase ID token for a project comes from.
func Issuer(projectID string) string {
	return "https://securetoken.google.com/" + projectID
}

// Leeway is how much clock disagreement is tolerated on the time claims.
//
// Zero would be correct and unusable: a server a few seconds ahead of Google would reject
// every token in the first moments of its life, and the symptom is an intermittent sign-in
// failure nobody can reproduce. A minute is enough for ordinary NTP drift and far short of the
// token's one-hour life, so it does not meaningfully extend how long a stolen token works.
const Leeway = time.Minute

// MaxTokenBytes bounds what will even be examined.
//
// A Firebase ID token is around 800 bytes. This is the cheap refusal that keeps a megabyte of
// header from being base64-decoded and JSON-parsed before anything doubts it.
const MaxTokenBytes = 8 << 10

// Verifier turns a token into claims, for one Firebase project.
//
// Safe for concurrent use: everything mutable is inside the KeySource.
type Verifier struct {
	// ProjectID is the Firebase project. It is both the audience the token must name and,
	// through Issuer, the issuer it must come from — which is what stops a valid token
	// minted for somebody else's Firebase project from working here.
	ProjectID string

	Keys *KeySource

	// Now is the clock, overridden by tests.
	Now func() time.Time
}

// NewVerifier returns a verifier for one project, pointed at Google.
func NewVerifier(projectID string) *Verifier {
	return &Verifier{ProjectID: projectID, Keys: NewKeySource()}
}

// Start warms the key cache. See [KeySource.Start].
func (v *Verifier) Start(ctx context.Context) error { return v.Keys.Start(ctx) }

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// header is the part of the JOSE header this package reads.
type header struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// payload is Firebase's ID token body.
type payload struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	AuthTime  int64  `json:"auth_time"`

	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`

	Firebase struct {
		SignInProvider string `json:"sign_in_provider"`
	} `json:"firebase"`
}

// Verify establishes that a token was issued by Firebase for this project, and returns what it
// says.
//
// The order is deliberate and is the whole of the method's safety:
//
//  1. The header is parsed and its algorithm and key id checked.
//  2. The **signature is verified** over the exact bytes as they arrived.
//  3. Only then is the payload parsed and its claims checked.
//
// Parsing the payload first would be more convenient — the audience is in it, and refusing
// early on the wrong audience saves an RSA verification. It would also mean acting on
// attacker-controlled JSON before establishing that anybody signed it, which is how a parser
// bug becomes an authentication bypass.
func (v *Verifier) Verify(ctx context.Context, token string) (identity.Claims, error) {
	if len(token) == 0 {
		return identity.Claims{}, &TokenError{Reason: "the token is empty"}
	}
	if len(token) > MaxTokenBytes {
		return identity.Claims{}, &TokenError{Reason: "the token is implausibly long"}
	}

	// Exactly three segments, counted before anything is cut. Splitting and taking the
	// first three would silently accept a fourth, and the signature covers only the two
	// segments before it — so a token with something appended would verify while carrying
	// a piece nobody checked.
	if strings.Count(token, ".") != 2 {
		return identity.Claims{}, &TokenError{Reason: "the token is not a JWT"}
	}
	headerPart, rest, _ := strings.Cut(token, ".")
	payloadPart, signaturePart, _ := strings.Cut(rest, ".")
	signedPart := headerPart + "." + payloadPart

	// ---- 1. the header -------------------------------------------------------

	rawHeader, err := decodeSegment(headerPart)
	if err != nil {
		return identity.Claims{}, &TokenError{Reason: "the header is not base64url", Err: err}
	}
	var head header
	if err := json.Unmarshal(rawHeader, &head); err != nil {
		return identity.Claims{}, &TokenError{Reason: "the header is not JSON", Err: err}
	}

	if head.Algorithm != Algorithm {
		// Compared, never obeyed. `none` and `HS256` both land here.
		return identity.Claims{}, &TokenError{
			Reason: fmt.Sprintf("the token is signed with %q; only %s is accepted",
				head.Algorithm, Algorithm)}
	}
	if head.KeyID == "" {
		return identity.Claims{}, &TokenError{Reason: "the header names no key"}
	}
	if head.Type != "" && !strings.EqualFold(head.Type, "JWT") {
		return identity.Claims{}, &TokenError{
			Reason: fmt.Sprintf("the header says this is a %q, not a JWT", head.Type)}
	}

	// ---- 2. the signature ----------------------------------------------------

	key, err := v.Keys.Key(ctx, head.KeyID)
	if err != nil {
		// Two very different failures, and they must not become one status. An unknown
		// key id is the caller's problem; not being able to reach Google is ours, and
		// answering it with "your token is invalid" would tell a person to log in again
		// during an outage that logging in cannot fix.
		return identity.Claims{}, err
	}

	signature, err := decodeSegment(signaturePart)
	if err != nil {
		return identity.Claims{}, &TokenError{Reason: "the signature is not base64url", Err: err}
	}

	digest := sha256.Sum256([]byte(signedPart))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return identity.Claims{}, &TokenError{Reason: "the signature does not match", Err: err}
	}

	// ---- 3. the claims -------------------------------------------------------
	//
	// Everything below is Google's own table, in its order.

	rawPayload, err := decodeSegment(payloadPart)
	if err != nil {
		return identity.Claims{}, &TokenError{Reason: "the payload is not base64url", Err: err}
	}
	var body payload
	if err := json.Unmarshal(rawPayload, &body); err != nil {
		return identity.Claims{}, &TokenError{Reason: "the payload is not JSON", Err: err}
	}

	now := v.now()

	if body.ExpiresAt == 0 || !time.Unix(body.ExpiresAt, 0).After(now.Add(-Leeway)) {
		return identity.Claims{}, &TokenError{Reason: "the token has expired"}
	}
	if body.IssuedAt == 0 || time.Unix(body.IssuedAt, 0).After(now.Add(Leeway)) {
		return identity.Claims{}, &TokenError{Reason: "the token was issued in the future"}
	}
	if audience := v.ProjectID; body.Audience != audience {
		// A token minted for another Firebase project is signed by the same Google key
		// and is otherwise entirely valid. This comparison is the only thing standing
		// between that token and an account here.
		return identity.Claims{}, &TokenError{
			Reason: fmt.Sprintf("the token was issued for %q, not for this project", body.Audience)}
	}
	if issuer := Issuer(v.ProjectID); body.Issuer != issuer {
		return identity.Claims{}, &TokenError{
			Reason: fmt.Sprintf("the token was issued by %q, not by %q", body.Issuer, issuer)}
	}
	if strings.TrimSpace(body.Subject) == "" {
		return identity.Claims{}, &TokenError{Reason: "the token names no subject"}
	}
	if body.AuthTime == 0 || time.Unix(body.AuthTime, 0).After(now.Add(Leeway)) {
		return identity.Claims{}, &TokenError{Reason: "the token says the sign-in has not happened yet"}
	}

	return identity.Claims{
		UID:           body.Subject,
		Email:         body.Email,
		EmailVerified: body.EmailVerified,
		Name:          body.Name,
		AvatarURL:     body.Picture,
		Provider:      body.Firebase.SignInProvider,
	}, nil
}

// decodeSegment decodes one base64url segment.
//
// Raw encoding, with no padding: RFC 7515 §2 says JWT segments carry none, and accepting
// padded input would mean two spellings of the same token — which matters the moment anything
// upstream caches or rate-limits on the string.
func decodeSegment(segment string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(segment)
}
