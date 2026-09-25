// Package signing is how PlayerSense signs what it POSTs to a customer's URL, so the receiver can
// tell it came from PlayerSense and was not replayed: a report's webhook channel, and the outbound
// webhooks (project.md §49-51).
//
// Three headers, the scheme Standard Webhooks and Stripe both use:
//
//	X-PlayerSense-Id         the event's id; the same on every retry of it, so a receiver can
//	                         ignore a repeat
//	X-PlayerSense-Timestamp  seconds since the epoch, when this attempt was signed
//	X-PlayerSense-Signature  v1=<hex HMAC-SHA256 over "timestamp.body">
//
// The receiver recomputes the MAC with its secret and compares in constant time, and refuses a
// timestamp more than five minutes from its clock, which is what stops a captured request being
// sent again later. The body carries the id, so the MAC covers it. Several signatures may be sent,
// comma-separated, while a secret is rolled: the dashboard's verification code, in four
// languages, is written against exactly this.
package signing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Header names.
const (
	HeaderID        = "X-PlayerSense-Id"
	HeaderTimestamp = "X-PlayerSense-Timestamp"
	HeaderSignature = "X-PlayerSense-Signature"
)

// Tolerance is how far a timestamp may be from the receiver's clock.
const Tolerance = 5 * time.Minute

// NewSecret returns a signing secret to show a customer once: "whsec_" and 32 random bytes.
func NewSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Sign is the signature header's value for one attempt, over each secret given: the current one,
// and the previous one while a roll is still being honoured.
func Sign(at time.Time, body []byte, secrets ...string) string {
	parts := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		parts = append(parts, "v1="+hex.EncodeToString(mac(secret, at.Unix(), body)))
	}
	return strings.Join(parts, ",")
}

func mac(secret string, ts int64, body []byte) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(strconv.FormatInt(ts, 10)))
	h.Write([]byte("."))
	h.Write(body)
	return h.Sum(nil)
}

// Errors a receiver can get from Verify.
var (
	ErrNoSignature = errors.New("no v1 signature matches")
	ErrStale       = errors.New("the timestamp is too far from now")
)

// Verify is what a receiver does, here so PlayerSense's own tests and documentation use the same
// code a customer is told to write.
func Verify(secret, timestamp, header string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrStale
	}
	if d := now.Sub(time.Unix(ts, 0)); d > Tolerance || d < -Tolerance {
		return ErrStale
	}
	want := mac(secret, ts, body)
	for _, part := range strings.Split(header, ",") {
		sig, found := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		if !found {
			continue
		}
		got, err := hex.DecodeString(sig)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrNoSignature
}
