package reports

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// A browser's EventSource cannot send an Authorization header, so a run's stream is opened with
// a token in its URL instead: the run's id and an expiry, signed by the API. It reaches that one
// run's events and nothing else, and for an hour.

// StreamTokenTTL is how long a stream token opens its run's stream.
const StreamTokenTTL = time.Hour

// ErrBadToken is a token that was not issued for this run, or has lapsed.
var ErrBadToken = errors.New("the stream token is not valid for this run")

// StreamToken signs a token for one run.
func StreamToken(key []byte, run string, now time.Time) string {
	exp := strconv.FormatInt(now.Add(StreamTokenTTL).Unix(), 10)
	return exp + "." + sign(key, run, exp)
}

// CheckStreamToken verifies a token for one run.
func CheckStreamToken(key []byte, run, token string, now time.Time) error {
	exp, sig, found := strings.Cut(token, ".")
	if !found {
		return ErrBadToken
	}
	at, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > at {
		return ErrBadToken
	}
	if !hmac.Equal([]byte(sig), []byte(sign(key, run, exp))) {
		return ErrBadToken
	}
	return nil
}

func sign(key []byte, run, exp string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("report-run-stream\x00" + run + "\x00" + exp))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
