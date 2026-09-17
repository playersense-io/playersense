package geoip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the address a request came from.
//
// This is the load-bearing half of §100's "geography is derived, not sent". Behind a load
// balancer the socket address is the balancer and `X-Forwarded-For` carries the chain — but a
// client can set that header too, so trusting it as written means a modified build picks its
// own country and every `geo.*` rule becomes worthless. The header is data from the caller
// until proven otherwise, exactly like the body.
//
// `hops` is how many proxies PlayerSense sits behind, and it is a deployment fact rather than
// a guess: 0 for a service reached directly, 1 behind one load balancer, 2 behind a CDN and a
// load balancer. The chain is `X-Forwarded-For` entries followed by the socket address, and
// the client is the `hops`-th entry **from the right** — the left of the chain is whatever the
// caller wanted to put there, and counting from the right is what makes the extra entries
// harmless.
//
// A worked example, with hops = 1 and a caller trying to forge Turkey:
//
//	caller sends   X-Forwarded-For: 1.2.3.4
//	balancer appends the real address and connects from itself
//	chain          [1.2.3.4, 203.0.113.9, 10.1.0.1]
//	               ^ forged   ^ the client  ^ the balancer
//	hops = 1       -> index len-1-1 = 1 -> 203.0.113.9
//
// The forged entry stays where the caller put it and is never reached.
func ClientIP(r *http.Request, hops int) netip.Addr {
	socket := parseAddr(r.RemoteAddr)

	// Trust nothing. The only address the caller cannot choose is the one the kernel saw.
	if hops <= 0 {
		return socket
	}

	chain := forwardedChain(r)
	if socket.IsValid() {
		chain = append(chain, socket)
	}
	if len(chain) == 0 {
		return netip.Addr{}
	}

	index := len(chain) - 1 - hops
	if index < 0 {
		// Fewer hops than configured: a direct request to a service that expects a
		// balancer — a health probe, or a misconfiguration. The leftmost entry is the
		// least-trusted thing available, and with no header at all it is the socket.
		index = 0
	}
	return chain[index]
}

// forwardedChain parses every X-Forwarded-For entry in order, dropping what does not parse.
//
// Dropped rather than rejected: a malformed entry is the caller's doing, and refusing the
// request would let anyone turn their own bad header into an outage. Positions still count
// from the right, so a dropped entry cannot shift the client leftward past a trusted hop —
// it can only make the chain shorter, which falls back to something less trusted, never more.
func forwardedChain(r *http.Request) []netip.Addr {
	var chain []netip.Addr
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, entry := range strings.Split(header, ",") {
			if addr := parseAddr(strings.TrimSpace(entry)); addr.IsValid() {
				chain = append(chain, addr)
			}
		}
	}
	return chain
}

// parseAddr accepts an address with or without a port, v4 or v6.
func parseAddr(value string) netip.Addr {
	if value == "" {
		return netip.Addr{}
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Unmap()
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap()
		}
	}
	return netip.Addr{}
}
