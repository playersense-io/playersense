package valkey

import (
	"context"
	"time"

	vk "github.com/valkey-io/valkey-go"
)

// incrementScript counts one request and reports the window's remaining life.
//
// The increment and the expiry happen in one round trip, so two requests arriving
// together cannot both create the window and leave it without one — which would be a
// counter that never resets.
//
// PEXPIRE is set only when the counter is new. Extending the life of an existing counter
// on every request is the classic mistake here: a limit whose window restarts on each
// request is a limit that never resets, and a client that keeps knocking is never let
// back in.
var incrementScript = vk.NewLuaScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return {count, redis.call('PTTL', KEYS[1])}
`)

// RateLimitStorage counts rate-limit windows in Valkey, so every instance of a service
// shares one budget.
//
// The in-process default the framework ships is correct for a single process and wrong
// for several: counters held in one process are not shared with the next, so a limit of a
// hundred a minute silently becomes a hundred a minute per instance.
type RateLimitStorage struct {
	client *Client
	prefix string
}

// NewRateLimitStorage returns storage backed by the given client.
func NewRateLimitStorage(client *Client) *RateLimitStorage {
	return &RateLimitStorage{client: client, prefix: "ratelimit:"}
}

// Increment counts one request against a quota for one client.
//
// Two rules the framework requires of any implementation, both satisfied above: the count
// includes the request being counted, so the first request in a window returns one; and
// the window is how long a newly created counter should live, never an extension of an
// existing one.
//
// The key is derived from client-supplied data and routinely carries an API key or a user
// identifier, so it is never logged.
func (s *RateLimitStorage) Increment(ctx context.Context, quota, key string, window time.Duration) (int, time.Duration, error) {
	if !s.client.Ready() {
		// The limiter could not count. Returning the error rather than pretending the
		// request was counted is what lets the framework fail closed by default — an
		// attacker who can reach the storage should not also get to choose the moment
		// limits stop applying.
		return 0, 0, ErrUnavailable
	}

	res := incrementScript.Exec(ctx, s.client.client,
		[]string{s.prefix + quota + ":" + key},
		[]string{formatMillis(window)})
	if err := res.Error(); err != nil {
		return 0, 0, err
	}

	values, err := res.ToArray()
	if err != nil || len(values) != 2 {
		return 0, 0, err
	}
	count, err := values[0].AsInt64()
	if err != nil {
		return 0, 0, err
	}
	ttl, err := values[1].AsInt64()
	if err != nil {
		return 0, 0, err
	}
	if ttl < 0 {
		// The key exists with no expiry, which should not happen. Report the full window
		// rather than a negative reset a client cannot act on.
		ttl = window.Milliseconds()
	}
	return int(count), time.Duration(ttl) * time.Millisecond, nil
}

func formatMillis(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return itoa(ms)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
