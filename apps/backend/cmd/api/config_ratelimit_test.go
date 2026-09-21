package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/core"
)

// The rule the whole design exists for: a device stuck in a loop is stopped, and the players
// beside it never notice.
//
// Not "the limit works" — that would pass with a single global counter, which is what this
// service had. The assertion is that the *other* device is untouched while the first is being
// refused, which is the only version of this that is worth anything.
func TestOneDeviceCannotSpendAnothersBudget(t *testing.T) {
	f := newFixtureWithLimits(t)
	f.publish(t, emptyInput(1))

	// One device, well past its own burst.
	var refused int
	for range core.DeviceConfigBurst * 3 {
		if f.ask(t, install(1), nil).Status == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("a device asking thirty times a second was never refused")
	}

	// Its neighbour, at the same moment, on the same address, in the same project.
	res := f.ask(t, install(2), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("a well-behaved device got %d because another one was looping: %s",
			res.Status, res)
	}
}

// A refusal has to say when to come back, or an SDK guesses — and every SDK guessing the same
// wrong number is a thundering herd.
func TestARefusalSaysWhenToComeBack(t *testing.T) {
	f := newFixtureWithLimits(t)
	f.publish(t, emptyInput(1))

	var refusal *testclient.Response
	for range core.DeviceConfigBurst * 3 {
		res := f.ask(t, install(3), nil)
		if res.Status == http.StatusTooManyRequests {
			refusal = res
			break
		}
	}
	if refusal == nil {
		t.Fatal("never refused")
	}

	if got := refusal.Header.Get("Retry-After"); got == "" || got == "0" {
		t.Errorf("Retry-After is %q; an SDK told to wait no time retries immediately", got)
	}
	// Named, so a developer reading a log can tell "this device is looping" from "this
	// project is over its ceiling" — different problems with different fixes.
	if !contains(refusal.String(), "device-config-burst") {
		t.Errorf("the refusal does not name which budget: %s", refusal)
	}
}
