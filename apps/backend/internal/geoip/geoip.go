// Package geoip answers what a connection's address says about it.
//
// This exists because of one rule in §100 and ADR 0027: geography, and whether a connection
// is a VPN or a datacentre, are **derived from the request** and never sent by the client.
// A value the client never sends is a value a modified build cannot forge, which is the
// difference between a targeting rule worth writing and one worth nothing.
//
// The naming is deliberate and the distinction is the point. `geo.country` means *this
// connection exits from an address geolocated to Turkey*. It does not mean *this person is
// in Turkey*. When both matter they are separate facts — `account.country` is the studio's
// own record — and collapsing them would throw away the only interesting part.
//
// # Where the address comes from
//
// From the framework: `ctx.ClientAddr()`, configured once by `TRUSTED_PROXIES`. This package
// deliberately does **not** work it out for itself.
//
// That matters more than it looks. Behind a load balancer the socket address is the balancer
// and `X-Forwarded-For` carries the chain — but a caller writes that header too, so believing
// it as written lets a modified build choose its own country and every `geo.*` rule becomes
// worthless. The framework resolves it against a CIDR list of proxies that are actually ours,
// trusts nothing by default, and bounds the walk. An earlier version of this package
// reimplemented that with a hop count, which was a second and weaker answer to a question the
// application had already settled.
package geoip

import (
	"fmt"
	"net/netip"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Connection is what an address resolves to. Every field is optional: a resolver with no
// databases, or an address in neither of them, yields the zero value and the caller carries
// on. Enrichment failing is never a reason to refuse an event.
type Connection struct {
	// ISO 3166-1 alpha-2, upper case: "TR". A rule is written against the code, not the
	// name, because "Türkiye" and "Turkey" are the same country and one of them will be
	// typed wrong.
	Country   string
	Region    string
	City      string
	Continent string
	Timezone  string

	// How the connection reaches us, not where from.
	VPN     bool
	Proxy   bool
	Tor     bool
	Relay   bool
	Hosting bool

	// PrivacyKnown records that the privacy database answered, which is what makes the
	// five booleans above mean something.
	//
	// Without it, "not a VPN" and "we did not look" are both `false`, and a rule written as
	// `network.vpn is false` silently reaches nobody — the failure this codebase keeps
	// finding, where a rule is accepted and then never matches and gives no reason. A
	// clean connection has to be a *fact*, not an absence.
	PrivacyKnown bool
}

// Empty reports whether nothing at all was resolved.
func (c Connection) Empty() bool { return c == Connection{} }

// location is the subset of ipinfo's location database that is kept.
//
// Latitude, longitude and postal code are deliberately **not** read. City is the floor for
// analytics and the ceiling for this: a coordinate pair is personal location data, and §100
// forbids collecting what composes into a fingerprint. Not reading them is stronger than
// reading and discarding them, because there is then no field for one to leak into.
type location struct {
	CountryCode string `maxminddb:"country_code"`
	RegionCode  string `maxminddb:"region_code"`
	Region      string `maxminddb:"region"`
	City        string `maxminddb:"city"`
	Continent   string `maxminddb:"continent_code"`
	Timezone    string `maxminddb:"timezone"`
}

type privacy struct {
	VPN     bool `maxminddb:"vpn"`
	Proxy   bool `maxminddb:"proxy"`
	Tor     bool `maxminddb:"tor"`
	Relay   bool `maxminddb:"relay"`
	Hosting bool `maxminddb:"hosting"`
}

// Resolver holds the open databases. Safe for concurrent use; the readers are mmap'd and
// read-only.
type Resolver struct {
	location *maxminddb.Reader
	privacy  *maxminddb.Reader
}

// Open loads whichever databases are configured. Either path may be empty.
//
// A missing database is not an error. The service runs without enrichment — every rule that
// names `geo.*` simply never matches — which is a degraded product rather than a stopped
// one, and is the right behaviour on a developer's machine.
func Open(locationPath, privacyPath string) (*Resolver, error) {
	r := &Resolver{}

	if locationPath != "" {
		db, err := maxminddb.Open(locationPath)
		if err != nil {
			return nil, fmt.Errorf("geoip location database %q: %w", locationPath, err)
		}
		r.location = db
	}
	if privacyPath != "" {
		db, err := maxminddb.Open(privacyPath)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("geoip privacy database %q: %w", privacyPath, err)
		}
		r.privacy = db
	}
	return r, nil
}

// Close releases both databases.
func (r *Resolver) Close() {
	if r == nil {
		return
	}
	if r.location != nil {
		_ = r.location.Close()
		r.location = nil
	}
	if r.privacy != nil {
		_ = r.privacy.Close()
		r.privacy = nil
	}
}

// Configured reports whether anything at all was loaded, for a startup log line that says
// which half of the product is live.
func (r *Resolver) Configured() (location, privacy bool) {
	if r == nil {
		return false, false
	}
	return r.location != nil, r.privacy != nil
}

// Resolve looks an address up in both databases.
//
// A lookup that fails is a lookup that returns nothing: the databases are a best effort over
// an address space that changes daily, and an unroutable or unknown address is ordinary
// rather than exceptional.
func (r *Resolver) Resolve(addr netip.Addr) Connection {
	var out Connection
	if r == nil || !addr.IsValid() {
		return out
	}

	// A private or loopback address resolves to nothing, deliberately. It is what a
	// developer's machine and a health probe look like, and putting "unknown" in the data
	// is more honest than whatever a database says about 10.0.0.0/8.
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return out
	}

	if r.location != nil {
		var found location
		if err := r.location.Lookup(addr).Decode(&found); err == nil {
			out.Country = found.CountryCode
			out.City = found.City
			out.Continent = found.Continent
			out.Timezone = found.Timezone

			// The code where there is one — "NSW" travels better than "New South Wales"
			// — and the name where there is not, so a rule always has something to match.
			out.Region = found.RegionCode
			if out.Region == "" {
				out.Region = found.Region
			}
		}
	}

	if r.privacy != nil {
		var found privacy
		if err := r.privacy.Lookup(addr).Decode(&found); err == nil {
			out.PrivacyKnown = true
			out.VPN = found.VPN
			out.Proxy = found.Proxy
			out.Tor = found.Tor
			out.Relay = found.Relay
			out.Hosting = found.Hosting
		}
	}
	return out
}

// Context renders a resolved connection into the reserved namespaces a targeting rule names.
//
// Only what was resolved appears. An absent key and a key set to "" are different things to
// a rule engine: the first is "we do not know", the second is "we know it is empty", and a
// rule written against the wrong one is a rule that silently never fires.
func (c Connection) Context() map[string]any {
	out := make(map[string]any, 10)
	set := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}
	set("geo.country", c.Country)
	set("geo.region", c.Region)
	set("geo.city", c.City)
	set("geo.continent", c.Continent)
	set("geo.timezone", c.Timezone)

	// All five, or none. Present whenever the privacy database answered, because "not a
	// VPN" is a fact a rule needs and is not the same as "we did not look" — and absent
	// entirely when it did not, so a rule that asks falls through to NotExists rather than
	// being told a confident false nobody established.
	if c.PrivacyKnown {
		out["network.vpn"] = c.VPN
		out["network.proxy"] = c.Proxy
		out["network.tor"] = c.Tor
		out["network.relay"] = c.Relay
		out["network.hosting"] = c.Hosting
	}
	return out
}
