package geoip

import (
	"net/http"
	"net/netip"
	"testing"
)

// The sample databases ipinfo publishes, committed so these run everywhere including CI.
// Production uses the full ones; the shape of a record is identical.
const (
	locationDB = "testdata/location_sample.mmdb"
	privacyDB  = "testdata/privacy_sample.mmdb"
)

func open(t *testing.T) *Resolver {
	t.Helper()
	r, err := Open(locationDB, privacyDB)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(r.Close)
	return r
}

func TestAnAddressResolvesToWhereItExits(t *testing.T) {
	r := open(t)

	got := r.Resolve(netip.MustParseAddr("1.0.0.1"))
	if got.Country != "AU" {
		t.Errorf("country = %q, want AU", got.Country)
	}
	if got.City != "Sydney" {
		t.Errorf("city = %q, want Sydney", got.City)
	}
	if got.Region != "NSW" {
		t.Errorf("region = %q, want the code NSW rather than the name", got.Region)
	}
	if got.Continent != "OC" {
		t.Errorf("continent = %q, want OC", got.Continent)
	}

	// A different country, so the test is about the lookup rather than a constant.
	if cn := r.Resolve(netip.MustParseAddr("1.0.1.1")); cn.Country != "CN" {
		t.Errorf("country = %q, want CN", cn.Country)
	}
}

func TestTheConnectionSaysHowItArrives(t *testing.T) {
	r := open(t)

	// 1.0.0.2/32 is the one VPN in the sample; its neighbours are not.
	if vpn := r.Resolve(netip.MustParseAddr("1.0.0.2")); !vpn.VPN {
		t.Error("1.0.0.2 is a VPN in the sample database and did not resolve as one")
	}
	if plain := r.Resolve(netip.MustParseAddr("1.0.0.3")); plain.VPN {
		t.Error("1.0.0.3 is not a VPN and resolved as one")
	}
	if hosting := r.Resolve(netip.MustParseAddr("1.0.0.1")); !hosting.Hosting {
		t.Error("1.0.0.1 is a datacentre in the sample database and did not resolve as one")
	}
}

// A private address is what a developer's machine and a health probe look like. "Unknown" is
// more honest than whatever a database says about 10.0.0.0/8.
func TestAPrivateAddressResolvesToNothing(t *testing.T) {
	r := open(t)
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.5", "::1"} {
		if got := r.Resolve(netip.MustParseAddr(address)); !got.Empty() {
			t.Errorf("%s resolved to %+v, want nothing", address, got)
		}
	}
}

// Enrichment failing is never a reason to refuse an event, so a resolver with no databases
// has to be a working resolver that answers nothing.
func TestAResolverWithNoDatabasesIsHarmless(t *testing.T) {
	r, err := Open("", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	if location, privacy := r.Configured(); location || privacy {
		t.Errorf("Configured() = %v, %v with no paths", location, privacy)
	}
	if got := r.Resolve(netip.MustParseAddr("1.0.0.1")); !got.Empty() {
		t.Errorf("resolved %+v with no databases", got)
	}
	if got := (*Resolver)(nil).Resolve(netip.MustParseAddr("1.0.0.1")); !got.Empty() {
		t.Errorf("a nil resolver resolved %+v", got)
	}
}

// An absent key and a key set to "" are different things to a rule engine: the first is "we
// do not know", the second is "we know it is empty". A rule written against the wrong one
// silently never fires.
func TestUnknownValuesAreAbsentRatherThanEmpty(t *testing.T) {
	ctx := Connection{Country: "TR"}.Context()

	if ctx["geo.country"] != "TR" {
		t.Errorf("geo.country = %v", ctx["geo.country"])
	}
	if _, present := ctx["geo.city"]; present {
		t.Error("geo.city is present with no value; a rule would match on emptiness")
	}
	if _, present := ctx["network.vpn"]; present {
		t.Error("network.vpn is present when the privacy database said nothing")
	}

	// But a resolved false is a fact, and has to be there: "not a VPN" is what a rule
	// needs, and it is not the same as "we did not look".
	known := Connection{Country: "TR", Hosting: true}.Context()
	if known["network.vpn"] != false {
		t.Errorf("network.vpn = %v, want an explicit false", known["network.vpn"])
	}
	if known["network.hosting"] != true {
		t.Errorf("network.hosting = %v", known["network.hosting"])
	}
}

func request(remote string, forwarded ...string) *http.Request {
	r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	for _, value := range forwarded {
		r.Header.Add("X-Forwarded-For", value)
	}
	return r
}

// The test this package exists for. If a caller can choose the address PlayerSense resolves,
// every geo.* rule is worthless and ADR 0027's whole argument collapses.
func TestAForgedForwardedHeaderCannotChooseTheAddress(t *testing.T) {
	// One balancer in front. The caller sends a forged entry; the balancer appends the
	// address it actually saw and connects from itself.
	got := ClientIP(request("10.1.0.1:54321", "1.2.3.4, 203.0.113.9"), 1)
	if got.String() != "203.0.113.9" {
		t.Fatalf("client = %s, want 203.0.113.9 — the forged entry was believed", got)
	}

	// However many the caller adds, they land to the left of the real one and are never
	// reached, because the count is from the right.
	got = ClientIP(request("10.1.0.1:54321", "9.9.9.9, 8.8.8.8, 1.2.3.4, 203.0.113.9"), 1)
	if got.String() != "203.0.113.9" {
		t.Fatalf("client = %s with a longer forged chain", got)
	}
}

func TestTheSocketIsTrustedWhenNothingElseIs(t *testing.T) {
	// hops = 0: the header is ignored entirely, whatever it says.
	got := ClientIP(request("203.0.113.9:443", "1.2.3.4"), 0)
	if got.String() != "203.0.113.9" {
		t.Fatalf("client = %s, want the socket address", got)
	}

	// Two proxies, both ours.
	got = ClientIP(request("10.1.0.2:443", "203.0.113.9, 10.1.0.1"), 2)
	if got.String() != "203.0.113.9" {
		t.Fatalf("client = %s behind two hops", got)
	}

	// Configured for a balancer, reached directly — a health probe, or a mistake. The
	// leftmost available entry is the least-trusted thing there is.
	got = ClientIP(request("203.0.113.9:443"), 1)
	if got.String() != "203.0.113.9" {
		t.Fatalf("client = %s with a shorter chain than configured", got)
	}
}

func TestAddressesParseWithOrWithoutAPort(t *testing.T) {
	cases := map[string]string{
		"203.0.113.9:443":  "203.0.113.9",
		"203.0.113.9":      "203.0.113.9",
		"[2001:db8::1]:80": "2001:db8::1",
		"2001:db8::1":      "2001:db8::1",
		"::ffff:1.2.3.4":   "1.2.3.4", // v4-mapped, unmapped so a lookup finds it
	}
	for input, want := range cases {
		if got := ClientIP(request(input), 0); got.String() != want {
			t.Errorf("%q -> %s, want %s", input, got, want)
		}
	}

	// A malformed entry is dropped, not fatal: refusing would let any caller turn their own
	// bad header into an outage.
	got := ClientIP(request("10.1.0.1:443", "not-an-address, 203.0.113.9"), 1)
	if got.String() != "203.0.113.9" {
		t.Errorf("client = %s with a malformed entry in the chain", got)
	}
}
