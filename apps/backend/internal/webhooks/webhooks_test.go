package webhooks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/schema"
	"playersense.dev/backend/internal/signing"
)

func TestTheRetryScheduleGivesUpAfterThreeDays(t *testing.T) {
	now := time.Now()
	var total time.Duration
	for made := 1; made < Tries; made++ {
		next, ok := NextTry(made, now)
		if !ok {
			t.Fatalf("gave up after %d tries", made)
		}
		wait := next.Sub(now)
		base := RetryAfter[made-1]
		if wait < base || wait > base+base/10+time.Nanosecond {
			t.Fatalf("try %d waits %v, want %v plus up to a tenth", made+1, wait, base)
		}
		total += base
	}
	if _, ok := NextTry(Tries, now); ok {
		t.Fatal("a delivery that has had every try is tried again")
	}
	if total < 66*time.Hour || total > 76*time.Hour {
		t.Fatalf("the schedule spans %v, want about three days", total)
	}
}

func TestAnEndpointHearsWhatItListensTo(t *testing.T) {
	if !Hears([]string{"*"}, "purchase.verified") || !Hears([]string{"purchase.verified"}, "purchase.verified") ||
		Hears([]string{"purchase.refunded"}, "purchase.verified") {
		t.Fatal("event lists")
	}
	if !HearsFrom([]string{"production"}, "") || !HearsFrom([]string{"production"}, "production") ||
		HearsFrom([]string{"production"}, "staging") {
		t.Fatal("environments")
	}
	for _, e := range Catalog {
		if !Known(e.Type) {
			t.Fatalf("%s is in the catalog and unknown", e.Type)
		}
	}
}

func TestAnEndpointURLMustBePublicHTTPS(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hooks.example.com/playersense": true,
		"http://hooks.example.com":              false,
		"https://localhost/x":                   false,
		"https://10.0.0.8/x":                    false,
		"https://169.254.169.254/latest":        false,
		"https://user:pass@example.com":         false,
		"https://api.internal/x":                false,
		"not a url":                             false,
	} {
		if err := CheckURL(raw, false); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if err := CheckURL("http://127.0.0.1:8080/hook", true); err != nil {
		t.Errorf("a local stack's own address: %v", err)
	}
}

// A failing endpoint is left alone once it has failed five times running, and let back one
// delivery at a time; an endpoint with every slot busy has further deliveries wait a moment for one,
// and turns them away after that.
func TestAFailingEndpointIsLeftAloneAndABusyOneHoldsDeliveries(t *testing.T) {
	st := &endpointState{slots: make(chan struct{}, 2)}
	now := time.Now()
	for i := 0; i < breakAfter; i++ {
		ok, _, _ := st.gate(now)
		if !ok || !st.acquire(0, false) {
			t.Fatalf("turned away after %d failures", i)
		}
		st.release(false, now)
	}
	ok, _, back := st.gate(now)
	if ok || !back.After(now) {
		t.Fatalf("after five failures: let through %v, back %v", ok, back)
	}
	later := now.Add(breakMax + time.Second)
	ok, probe, _ := st.gate(later)
	if !ok || !probe {
		t.Fatal("no probe once the pause is over")
	}
	if ok, _, _ := st.gate(later); ok {
		t.Fatal("a second delivery went while the probe was out")
	}
	st.acquire(0, true)
	st.release(true, later)
	if ok, probe, _ := st.gate(later); !ok || probe {
		t.Fatal("a recovered endpoint is still held back")
	}

	// Busy: both slots taken, a third waits and is turned away; one freed in time is taken.
	st.acquire(0, false)
	st.acquire(0, false)
	if st.acquire(20*time.Millisecond, false) {
		t.Fatal("a third try took a slot of two")
	}
	go func() { time.Sleep(10 * time.Millisecond); <-st.slots }()
	if !st.acquire(time.Second, false) {
		t.Fatal("a slot freed while waiting was not taken")
	}
}

func TestADeliveryIsSignedAndCarriesItsHeaders(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(strings.Repeat("x", ResponseLimit+100)))
	}))
	defer srv.Close()

	d := NewDeliverer(true)
	id := uuid.New()
	res := d.Send(context.Background(), Target{URL: srv.URL, Secrets: []string{"whsec_a", "whsec_b"},
		Headers: []schema.WebhookHeader{{Name: "X-Token", Value: "t"}}}, id, []byte(`{"id":"`+id.String()+`"}`))
	if !res.OK() || res.Code != http.StatusAccepted || len(res.Response) != ResponseLimit {
		t.Fatalf("the result: %d, %d bytes kept", res.Code, len(res.Response))
	}
	if got.Header.Get(signing.HeaderID) != id.String() || got.Header.Get("X-Token") != "t" || got.Header.Get("User-Agent") != UserAgent {
		t.Fatalf("headers %v", got.Header)
	}
	sig := got.Header.Get(signing.HeaderSignature)
	if strings.Count(sig, "v1=") != 2 {
		t.Fatalf("two secrets, signatures %q", sig)
	}
	for _, secret := range []string{"whsec_a", "whsec_b"} {
		if err := signing.Verify(secret, got.Header.Get(signing.HeaderTimestamp), sig, body, time.Now()); err != nil {
			t.Fatalf("%s: %v", secret, err)
		}
	}

	srv.Close()
	down := d.Send(context.Background(), Target{URL: srv.URL}, id, []byte(`{}`))
	if down.OK() || down.Code != 0 || down.Error == "" {
		t.Fatalf("a closed server: %+v", down)
	}
	refused := NewDeliverer(false).Send(context.Background(), Target{URL: "http://127.0.0.1:9/"}, id, []byte(`{}`))
	if refused.Error != "Refused: the address is private" {
		t.Fatalf("a private address: %q", refused.Error)
	}
}
