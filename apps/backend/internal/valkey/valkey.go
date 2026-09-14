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

// Name identifies the component in start-up and shutdown logs.
func (c *Client) Name() string { return "valkey" }

// Start dials Valkey.
func (c *Client) Start(ctx context.Context) error {
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
