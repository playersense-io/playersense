// Package core holds the configuration, guards, dependencies and managed resources the
// control API is composed from. It knows nothing about routers or handlers, which is what
// keeps it testable on its own.
package core

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Key classes (§52). A publishable key ships inside a game binary and is assumed
// discoverable; a secret key never leaves a server.
const (
	KindPublishable = "publishable"
	KindSecret      = "secret"
)

// ErrMalformedKey reports a credential that is not a PlayerSense key at all.
var ErrMalformedKey = errors.New("malformed api key")

// NewAPIKey mints a key and returns it alongside the prefix and hash to store.
//
// The plaintext exists only in the response to the request that created it. What is
// persisted is a SHA-256 digest and a displayable head — §53's rule, and the reason a
// leaked database is not a leaked fleet of keys.
func NewAPIKey(kind, environment string) (secret, prefix string, hash []byte, err error) {
	var label string
	switch kind {
	case KindPublishable:
		label = "client"
	case KindSecret:
		label = "secret"
	default:
		return "", "", nil, fmt.Errorf("unknown key kind %q", kind)
	}

	// Environment is part of the key so that a development key pasted into a production
	// build is visibly wrong before it is ever sent (§76).
	env := "live"
	if environment != "production" {
		env = environment
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, fmt.Errorf("generate api key: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	secret = fmt.Sprintf("ps_%s_%s_%s", label, env, body)

	sum := sha256.Sum256([]byte(secret))
	// Enough of the head to identify the key in a dashboard, not enough to use it.
	prefix = secret[:len(secret)-len(body)+6]
	return secret, prefix, sum[:], nil
}

// HashAPIKey digests a presented key for lookup.
func HashAPIKey(secret string) ([]byte, error) {
	if !strings.HasPrefix(secret, "ps_") {
		return nil, ErrMalformedKey
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

// SameKey compares two digests in constant time.
//
// Comparing with == returns as soon as two bytes differ, so an attacker who can time the
// response recovers the value one byte at a time. Digests are fixed-length, so no length
// leaks either.
func SameKey(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
