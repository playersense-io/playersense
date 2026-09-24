// Package valkey wraps the cache and the rate-limit counters (project.md §12).
//
// Valkey is ephemeral acceleration and never a source of truth. Everything it holds can
// be rebuilt from PostgreSQL, and every caller here has a path that still works when it
// is unavailable — §65's rule that losing Valkey costs speed rather than data.
package valkey

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	vk "github.com/valkey-io/valkey-go"
)

// Client is a Valkey connection with a lifecycle.
type Client struct {
	dsn string

	client vk.Client
}

// New returns an unconnected client.
//
// Construction dials nothing, so the application opens it before serving and closes it
// after draining, like every other managed resource.
func New(dsn string) *Client { return &Client{dsn: dsn} }

// Key renders a namespaced key. Every caller goes through it, so no caller has to
// remember the separator and a key is greppable from one place.
func (c *Client) Key(parts ...string) string {
	return strings.Join(parts, ":")
}

// Name identifies the component in start-up and shutdown logs.
func (c *Client) Name() string { return "valkey" }

// Start dials Valkey.
func (c *Client) Start(ctx context.Context) error {
	// Once, as pgdb.DB.Start is and for its reason: a second start would replace an open
	// client without closing it.
	if c.client != nil {
		return nil
	}
	opt, err := parseDSN(c.dsn)
	if err != nil {
		return err
	}
	client, err := vk.NewClient(opt)
	if err != nil {
		return fmt.Errorf("dial valkey: %w", err)
	}
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return fmt.Errorf("ping valkey: %w", err)
	}
	c.client = client
	return nil
}

// Stop closes the connection. It runs after the server has drained, so nothing is still
// reading from it.
func (c *Client) Stop(context.Context) error {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	return nil
}

// Ready reports whether the client is usable.
//
// Callers check this rather than assuming: a nil client means Valkey never started, and
// every path through this package has to degrade rather than panic (§65).
func (c *Client) Ready() bool { return c.client != nil }

// Get reads a key. A miss is (nil, nil) rather than an error, because a miss is the
// ordinary case and an error is not.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	if !c.Ready() {
		return nil, ErrUnavailable
	}
	msg := c.client.Do(ctx, c.client.B().Get().Key(key).Build())
	if err := msg.Error(); err != nil {
		if vk.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, err
	}
	return msg.AsBytes()
}

// Set writes a key with a TTL. Everything this package stores expires: an entry that
// outlives its source is an entry nobody can correct.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if !c.Ready() {
		return ErrUnavailable
	}
	return c.client.Do(ctx,
		c.client.B().Set().Key(key).Value(vk.BinaryString(value)).
			PxMilliseconds(ttl.Milliseconds()).Build()).Error()
}

// SetNX writes a key only if it is not already there, and reports whether it won.
//
// The atomic half of a "do this at most once per interval" rule. Reading and then writing
// would let two concurrent requests both see nothing and both proceed — which for the caller
// that wants this means two of whatever was supposed to happen once, sent to a real person.
//
// A false with no error is the ordinary outcome, not a failure: somebody else holds the slot.
func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if !c.Ready() {
		return false, ErrUnavailable
	}
	err := c.client.Do(ctx,
		c.client.B().Set().Key(key).Value(vk.BinaryString(value)).
			Nx().PxMilliseconds(ttl.Milliseconds()).Build()).Error()
	switch {
	case err == nil:
		return true, nil
	case vk.IsValkeyNil(err):
		// The key was already there. SET NX answers a nil reply rather than an error.
		return false, nil
	default:
		return false, err
	}
}

// Del removes keys. Used to evict a revoked credential immediately rather than waiting
// out its TTL (§53).
func (c *Client) Del(ctx context.Context, keys ...string) error {
	if !c.Ready() || len(keys) == 0 {
		return nil
	}
	return c.client.Do(ctx, c.client.B().Del().Key(keys...).Build()).Error()
}

// ErrUnavailable reports that Valkey is not connected.
var ErrUnavailable = fmt.Errorf("valkey is unavailable")

// parseDSN accepts redis:// and valkey:// URLs.
func parseDSN(dsn string) (vk.ClientOption, error) {
	if dsn == "" {
		return vk.ClientOption{}, fmt.Errorf("empty VALKEY_DSN")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return vk.ClientOption{}, fmt.Errorf("parse VALKEY_DSN: %w", err)
	}
	opt := vk.ClientOption{InitAddress: []string{u.Host}}
	if u.User != nil {
		opt.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			opt.Password = pw
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		n, err := strconv.Atoi(db)
		if err != nil {
			return vk.ClientOption{}, fmt.Errorf("VALKEY_DSN database must be a number, got %q", db)
		}
		opt.SelectDB = n
	}
	return opt, nil
}

// GetEx reads a key and extends its life in one round trip.
//
// A device-state entry (§29.5) should live as long as the device keeps playing, so every
// read is also a renewal. Doing it as two calls would double the writer's Valkey traffic
// on its hottest path, and doing it as a read without a renewal would expire an entry for
// a device that is active every day.
func (c *Client) GetEx(ctx context.Context, key string, ttl time.Duration) ([]byte, error) {
	if !c.Ready() {
		return nil, ErrUnavailable
	}
	msg := c.client.Do(ctx,
		c.client.B().Getex().Key(key).PxMilliseconds(ttl.Milliseconds()).Build())
	if err := msg.Error(); err != nil {
		if vk.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, err
	}
	return msg.AsBytes()
}

// Exists reports whether a key is present.
//
// Used for tombstones, where the value carries nothing and only presence matters. An
// unavailable cache reports the error rather than "absent": a tombstone read that fails
// open would let a deleted project's batch be written, and the caller decides what to do
// about that rather than having it decided silently here.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if !c.Ready() {
		return false, ErrUnavailable
	}
	n, err := c.client.Do(ctx, c.client.B().Exists().Key(key).Build()).AsInt64()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Present reports, for each key, whether it exists — in one round trip.
//
// MGET rather than one EXISTS per key, because the caller is the billing writer's filter and it
// asks about every player in a delivery at once: a thousand round trips per delivery would make
// the filter slower than the database write it exists to avoid. An unavailable cache is an error
// rather than "all absent", so the caller decides — and the billing writer's decision is to ask
// PostgreSQL, which is always right and only slower.
func (c *Client) Present(ctx context.Context, keys []string) ([]bool, error) {
	if !c.Ready() {
		return nil, ErrUnavailable
	}
	out := make([]bool, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	values, err := c.client.Do(ctx, c.client.B().Mget().Key(keys...).Build()).ToArray()
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		if i < len(out) {
			out[i] = !value.IsNil()
		}
	}
	return out, nil
}

// Mark sets each key to a one-byte value with a lifetime, pipelined into one round trip.
//
// The counterpart to Present. Best effort by contract: a key that failed to set costs a later
// delivery one extra database lookup, never a wrong answer, so the first error is returned for
// logging and nothing is retried.
func (c *Client) Mark(ctx context.Context, keys []string, ttl time.Duration) error {
	if !c.Ready() {
		return ErrUnavailable
	}
	if len(keys) == 0 {
		return nil
	}
	cmds := make(vk.Commands, 0, len(keys))
	for _, key := range keys {
		cmds = append(cmds, c.client.B().Set().Key(key).Value("1").
			PxMilliseconds(ttl.Milliseconds()).Build())
	}
	for _, result := range c.client.DoMulti(ctx, cmds...) {
		if err := result.Error(); err != nil {
			return err
		}
	}
	return nil
}

// AddHourlyCounts adds counts into one hash and records last-use times into another, in one round
// trip: HINCRBY per field, an expiry on the counts, and HSET of the times.
func (c *Client) AddHourlyCounts(ctx context.Context, countsKey string, counts map[string]int64, ttl time.Duration,
	lastKey string, last map[string]string,
) error {
	if !c.Ready() {
		return ErrUnavailable
	}
	cmds := make(vk.Commands, 0, len(counts)+3)
	for field, n := range counts {
		cmds = append(cmds, c.client.B().Hincrby().Key(countsKey).Field(field).Increment(n).Build())
	}
	cmds = append(cmds, c.client.B().Pexpire().Key(countsKey).Milliseconds(ttl.Milliseconds()).Build())
	if len(last) > 0 {
		set := c.client.B().Hset().Key(lastKey).FieldValue()
		for field, v := range last {
			set = set.FieldValue(field, v)
		}
		cmds = append(cmds, set.Build())
		cmds = append(cmds, c.client.B().Pexpire().Key(lastKey).Milliseconds((30 * 24 * time.Hour).Milliseconds()).Build())
	}
	for _, result := range c.client.DoMulti(ctx, cmds...) {
		if err := result.Error(); err != nil {
			return err
		}
	}
	return nil
}

// HashFields reads the same fields from several hashes in one round trip: out[i][j] is field j of
// hash i, empty when absent.
func (c *Client) HashFields(ctx context.Context, keys, fields []string) ([][]string, error) {
	out := make([][]string, len(keys))
	if !c.Ready() {
		return out, ErrUnavailable
	}
	cmds := make(vk.Commands, len(keys))
	for i, key := range keys {
		cmds[i] = c.client.B().Hmget().Key(key).Field(fields...).Build()
	}
	for i, result := range c.client.DoMulti(ctx, cmds...) {
		values, err := result.ToArray()
		if err != nil && !vk.IsValkeyNil(err) {
			return out, err
		}
		out[i] = make([]string, len(fields))
		for j, v := range values {
			if s, err := v.ToString(); err == nil {
				out[i][j] = s
			}
		}
	}
	return out, nil
}
