package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// A URL a customer typed is a URL an attacker may have typed. PlayerSense POSTs to it from inside
// its own network, so the client that does refuses anything that is not the public internet:
// loopback, private ranges, link-local (which is where a cloud's metadata server answers), and
// the rest of the special-purpose blocks. The check is made on the address actually dialled,
// after resolution, so a name that resolves to 10.0.0.1 is refused as surely as the literal, and
// redirects are not followed, so a public URL cannot bounce the request inward.

// ErrPrivateAddress is a destination inside a private or special-purpose network.
var ErrPrivateAddress = errors.New("the destination is not on the public internet")

// blocked are the special-purpose ranges no customer destination may be in.
var blocked = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "100::/64", "2001:db8::/32",
	"fc00::/7", "fe80::/10", "ff00::/8",
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// Public reports whether an address is on the public internet.
func Public(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range blocked {
		if p.Contains(addr) {
			return false
		}
	}
	return addr.IsValid()
}

// NewClient returns the client every customer destination is reached with: bounded in time, no
// redirects, and, unless allowPrivate, public addresses only. allowPrivate is for development and
// tests, where the destination is a server on this machine.
func NewClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if allowPrivate {
				return dialer.DialContext(ctx, network, address)
			}
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				if !Public(a) {
					return nil, fmt.Errorf("%w: %s is %s", ErrPrivateAddress, host, a)
				}
			}
			var last error
			for _, a := range addrs {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("%s resolves to nothing", host)
			}
			return nil, last
		},
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
