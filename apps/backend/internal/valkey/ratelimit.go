package valkey

import (
	"context"
	"fmt"
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
}

// NewRateLimitStorage returns storage backed by the given client.
func NewRateLimitStorage(client *Client) *RateLimitStorage {
	return &RateLimitStorage{client: client}
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
		[]string{s.client.Key("ratelimit", quota, key)},
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

// incrementManyScript counts one request against several budgets in one round trip.
//
// The ingestion path checks three — the project, the address inside it, and the device —
// and three round trips per request would make the limiter more expensive than the work it
// protects. One script, N keys, N windows, and the same two rules as above hold for each:
// the count includes this request, and an existing counter's life is never extended.
//
// Every budget is counted even when an earlier one is already over. Stopping at the first
// would make the later counters depend on the order keys happened to be passed in, so a
// device over its own limit would stop contributing to its project's — and the project's
// number would quietly become a number about something else.
var incrementManyScript = vk.NewLuaScript(`
local out = {}
for i = 1, #KEYS do
  local count = redis.call('INCR', KEYS[i])
  if count == 1 then
    redis.call('PEXPIRE', KEYS[i], ARGV[i])
  end
  out[#out + 1] = count
  out[#out + 1] = redis.call('PTTL', KEYS[i])
end
return out
`)

// Counted is what one budget's counter said.
type Counted struct {
	Count int
	Reset time.Duration
}

// IncrementMany counts one request against several budgets at once.
//
// `quota` and `keys` are paired with `windows` by position; all three must be the same
// length. The result is in the same order, so a caller reads budget i from element i rather
// than matching on a key it must never log.
func (s *RateLimitStorage) IncrementMany(ctx context.Context, quotas, keys []string, windows []time.Duration) ([]Counted, error) {
	if len(quotas) != len(keys) || len(keys) != len(windows) {
		return nil, fmt.Errorf("valkey: %d quotas, %d keys and %d windows", len(quotas), len(keys), len(windows))
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if !s.client.Ready() {
		return nil, ErrUnavailable
	}

	full := make([]string, len(keys))
	args := make([]string, len(windows))
	for i := range keys {
		full[i] = s.client.Key("ratelimit", quotas[i], keys[i])
		args[i] = formatMillis(windows[i])
	}

	res := incrementManyScript.Exec(ctx, s.client.client, full, args)
	if err := res.Error(); err != nil {
		return nil, err
	}
	values, err := res.ToArray()
	if err != nil {
		return nil, err
	}
	if len(values) != 2*len(keys) {
		return nil, fmt.Errorf("valkey: the limiter answered %d values for %d budgets",
			len(values), len(keys))
	}

	out := make([]Counted, len(keys))
	for i := range keys {
		count, err := values[2*i].AsInt64()
		if err != nil {
			return nil, err
		}
		ttl, err := values[2*i+1].AsInt64()
		if err != nil {
			return nil, err
		}
		if ttl < 0 {
			ttl = windows[i].Milliseconds()
		}
		out[i] = Counted{Count: int(count), Reset: time.Duration(ttl) * time.Millisecond}
	}
	return out, nil
}
