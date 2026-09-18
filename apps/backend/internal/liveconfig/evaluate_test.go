package liveconfig_test

import (
	"testing"
	"time"

	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/liveops"
	"playersense.dev/backend/internal/targeting"
)

const project = "proj-1"

func subject(install string, context targeting.Context) liveconfig.Subject {
	if context == nil {
		context = targeting.Context{}
	}
	return liveconfig.Subject{InstallID: install, Context: context}
}

// Every flag comes back, whether or not a rule matched. A game asking for a flag it was not
// sent has no answer at all, and "absent" and "false" are different things to a caller that
// has to pick one.
func TestEveryFlagIsAnswered(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 3,
		Flags: []liveconfig.Flag{
			{Key: "hud_v2", Kind: "bool", Fallback: false, Rollout: targeting.Buckets},
			{Key: "max_level", Kind: "number", Fallback: float64(50), Rollout: targeting.Buckets,
				Rules: []liveconfig.Variant{{
					Audience: rule(t, `{"key":"geo.country","op":"eq","value":"TR"}`),
					Value:    float64(80), Rollout: targeting.Buckets,
				}}},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	turkish := doc.Evaluate(time.Now(), project,
		subject("install-a", targeting.Context{"geo.country": "TR"}))
	if turkish.Version != 3 {
		t.Errorf("version is %d", turkish.Version)
	}
	if len(turkish.Flags) != 2 {
		t.Errorf("got %d flags, want 2: %v", len(turkish.Flags), turkish.Flags)
	}
	if turkish.Flags["max_level"] != float64(80) {
		t.Errorf("max_level is %v, want 80", turkish.Flags["max_level"])
	}
	if turkish.Flags["hud_v2"] != false {
		t.Errorf("a flag whose fallback is false came back as %v", turkish.Flags["hud_v2"])
	}

	// Somebody the rule does not reach still gets an answer: the fallback.
	german := doc.Evaluate(time.Now(), project,
		subject("install-b", targeting.Context{"geo.country": "DE"}))
	if german.Flags["max_level"] != float64(50) {
		t.Errorf("max_level outside the audience is %v, want the fallback 50",
			german.Flags["max_level"])
	}
}

// A rule that matches but whose rollout excludes this player gets the fallback, not the next
// rule. Falling through would quietly enrol the control group into whatever came after.
func TestAnExcludedPlayerGetsTheFallbackNotTheNextRule(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{{
			Key: "offer", Kind: "string", Fallback: "none", Rollout: targeting.Buckets,
			Rules: []liveconfig.Variant{
				{
					Audience: rule(t, `{"key":"geo.country","op":"eq","value":"TR"}`),
					Value:    "experiment", Rollout: 0, // reaches nobody
				},
				{
					// Would match the same player, and must not be reached.
					Audience: rule(t, `{}`),
					Value:    "everyone_else", Rollout: targeting.Buckets,
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	got := doc.Evaluate(time.Now(), project,
		subject("install-a", targeting.Context{"geo.country": "TR"})).Flags["offer"]
	if got == "everyone_else" {
		t.Fatal("a player excluded from a rule's sample fell through into the next rule")
	}
	if got != "none" {
		t.Errorf("offer is %v, want the fallback", got)
	}
}

// §43: the same player stays in the same variant, launch after launch, and two flags do not
// share one audience for no visible reason.
func TestRolloutIsStableAndUncorrelated(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{
			{Key: "flag_a", Kind: "bool", Fallback: false, Rollout: 5000,
				Rules: []liveconfig.Variant{{Audience: rule(t, `{}`), Value: true, Rollout: targeting.Buckets}}},
			{Key: "flag_b", Kind: "bool", Fallback: false, Rollout: 5000,
				Rules: []liveconfig.Variant{{Audience: rule(t, `{}`), Value: true, Rollout: targeting.Buckets}}},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// The same installation, a hundred times: the same answer.
	first := doc.Evaluate(time.Now(), project, subject("install-steady", nil))
	for range 100 {
		again := doc.Evaluate(time.Now(), project, subject("install-steady", nil))
		if again.Flags["flag_a"] != first.Flags["flag_a"] {
			t.Fatal("the same installation changed variant between two evaluations")
		}
	}

	// Across many installations, a 50% rollout reaches roughly half — and the two flags
	// disagree often, which is what "uncorrelated" means in practice. A salt-less hash of
	// only the actor would put every player in the same arm of every flag.
	var onA, onB, disagree int
	const population = 2000
	for i := range population {
		got := doc.Evaluate(time.Now(), project, subject("install-"+itoa(i), nil))
		a, _ := got.Flags["flag_a"].(bool)
		b, _ := got.Flags["flag_b"].(bool)
		if a {
			onA++
		}
		if b {
			onB++
		}
		if a != b {
			disagree++
		}
	}
	for name, count := range map[string]int{"flag_a": onA, "flag_b": onB} {
		if count < population*4/10 || count > population*6/10 {
			t.Errorf("%s reached %d of %d, want roughly half", name, count, population)
		}
	}
	if disagree < population/4 {
		t.Errorf("the two flags agreed on %d of %d players; they are correlated",
			population-disagree, population)
	}
}

// Hasan's example: an offer on the first five days of every month, for a segment. The player
// is inside it in March and outside it a week later, and the response says when it comes back
// so a game can count down without holding a calendar.
func TestAnEventIsServedOnlyWhileItIsOn(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version:  1,
		Segments: liveconfig.Segments{"tr": rule(t, `{"key":"geo.country","op":"eq","value":"TR"}`)},
		Events: []liveconfig.LiveOpsEvent{{
			Key: "month_start_offer", Kind: "offer", Priority: 10,
			Schedule: liveops.Schedule{
				Mode:     liveops.Monthly,
				Anchor:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				Duration: 5 * 24 * time.Hour,
			},
			Audience: rule(t, `{"segment":"tr"}`),
			Payload:  map[string]any{"discount": 20},
			Rollout:  targeting.Buckets,
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	turkish := subject("install-a", targeting.Context{"geo.country": "TR"})

	inside := doc.Evaluate(time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC), project, turkish)
	if len(inside.Events) != 1 {
		t.Fatalf("got %d events on 2 March, want 1", len(inside.Events))
	}
	event := inside.Events[0]
	if event.Key != "month_start_offer" || event.Payload == nil {
		t.Errorf("the event is %+v", event)
	}
	if got := time.UnixMilli(event.StartsAtMS).UTC(); !got.Equal(time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("it started at %s", got)
	}
	// The next occurrence, not this one — a recurring event must not report itself.
	if got := time.UnixMilli(event.NextStartsAtMS).UTC(); !got.Equal(time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("it comes back at %s, want 1 April", got)
	}

	outside := doc.Evaluate(time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC), project, turkish)
	if len(outside.Events) != 0 {
		t.Errorf("an event nine days into the month is still being served: %+v", outside.Events)
	}

	// On, but for somebody else's audience.
	german := doc.Evaluate(time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC), project,
		subject("install-b", targeting.Context{"geo.country": "DE"}))
	if len(german.Events) != 0 {
		t.Errorf("an event reached a player outside its segment: %+v", german.Events)
	}
}

// Two events on at once come back highest priority first, and in a stable order when they tie,
// so a game can render the list without it wobbling between requests.
func TestEventsComeBackInAStableOrder(t *testing.T) {
	always := liveops.Schedule{
		Mode:     liveops.Daily,
		Anchor:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		Duration: 23 * time.Hour,
	}
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Events: []liveconfig.LiveOpsEvent{
			{Key: "zebra", Kind: "offer", Priority: 5, Schedule: always, Payload: 1, Rollout: targeting.Buckets},
			{Key: "alpha", Kind: "offer", Priority: 5, Schedule: always, Payload: 2, Rollout: targeting.Buckets},
			{Key: "urgent", Kind: "offer", Priority: 99, Schedule: always, Payload: 3, Rollout: targeting.Buckets},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	at := time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC)
	for range 10 {
		got := doc.Evaluate(at, project, subject("install-a", nil))
		keys := []string{}
		for _, e := range got.Events {
			keys = append(keys, e.Key)
		}
		if len(keys) != 3 || keys[0] != "urgent" || keys[1] != "alpha" || keys[2] != "zebra" {
			t.Fatalf("order is %v, want [urgent alpha zebra]", keys)
		}
	}
}

// What is coming, for a game that shows "starts Friday". Separate from Evaluate so that an
// SDK asking only what is on now does not pay for a forward scan of every schedule.
func TestUpcomingLooksForward(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Events: []liveconfig.LiveOpsEvent{{
			Key: "weekend", Kind: "seasonal_event",
			Schedule: liveops.Schedule{
				Mode:     liveops.Weekly,
				Anchor:   time.Date(2026, time.January, 2, 18, 0, 0, 0, time.UTC), // a Friday
				Duration: 54 * time.Hour,
				Weekdays: []time.Weekday{time.Friday},
			},
			Payload: map[string]any{"xp": 2},
			Rollout: targeting.Buckets,
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// A Wednesday: nothing on, one thing coming.
	wednesday := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)
	if got := doc.Evaluate(wednesday, project, subject("install-a", nil)); len(got.Events) != 0 {
		t.Errorf("the weekend event is on during the week: %+v", got.Events)
	}
	soon := doc.Upcoming(wednesday, 7*24*time.Hour, project, subject("install-a", nil))
	if len(soon) != 1 {
		t.Fatalf("got %d upcoming, want 1", len(soon))
	}
	if got := time.UnixMilli(soon[0].StartsAtMS).UTC().Weekday(); got != time.Friday {
		t.Errorf("the next weekend starts on a %s", got)
	}

	// And a horizon shorter than the gap finds nothing, rather than everything.
	if got := doc.Upcoming(wednesday, time.Hour, project, subject("install-a", nil)); len(got) != 0 {
		t.Errorf("a one-hour horizon returned %d events", len(got))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for i > 0 {
		out = append([]byte{byte('0' + i%10)}, out...)
		i /= 10
	}
	return string(out)
}

// A flag at 50% carrying one rule at 50% must reach a quarter of players, not half.
//
// The two gates are drawn against the same installation, so unless they are salted apart they
// return the same bucket and the second is a no-op: everybody who passed the flag gate passes
// the variant gate too. The symptom is not an error — it is an experiment whose arm is twice
// the size somebody asked for, which is discovered, if ever, in the results.
func TestNestedRolloutsAreIndependent(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{{
			Key: "nested", Kind: "bool", Fallback: false, Rollout: 5000,
			Rules: []liveconfig.Variant{{
				Audience: rule(t, `{}`), Value: true, Rollout: 5000,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	const population = 4000
	on := 0
	for i := range population {
		if got, _ := doc.Evaluate(time.Now(), project,
			subject("install-"+itoa(i), nil)).Flags["nested"].(bool); got {
			on++
		}
	}

	// A quarter, with room for sampling noise. Half would mean the inner gate never bit.
	if on < population*15/100 || on > population*35/100 {
		t.Errorf("a 50%% flag with a 50%% rule reached %d of %d (%.0f%%), want about a quarter",
			on, population, 100*float64(on)/float64(population))
	}
}
