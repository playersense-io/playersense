package geoip

import (
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
