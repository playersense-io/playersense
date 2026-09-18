package liveconfig_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/liveops"
	"playersense.dev/backend/internal/targeting"
)

// everyDay is a schedule that is simply valid, for tests about something other than the
// calendar. Written once so that a test about segments reads as a test about segments.
func everyDay() liveops.Schedule {
	return liveops.Schedule{
		Mode:     liveops.Daily,
		Anchor:   time.Date(2026, time.January, 1, 18, 0, 0, 0, time.UTC),
		Duration: 2 * time.Hour,
	}
}

func rule(t *testing.T, raw string) targeting.Rule {
	t.Helper()
	parsed, err := targeting.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return parsed
}

// A studio names an audience once and uses it in three places. That is the point of segments,
// and inlining is how it costs nothing at request time.
func TestASegmentIsReusedAndInlined(t *testing.T) {
	segments := liveconfig.Segments{
		"whales": rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":100000000}`),
	}

	doc, err := liveconfig.Compile(liveconfig.Input{
		Version:  1,
		Segments: segments,
		Flags: []liveconfig.Flag{{
			Key: "vip_shop", Kind: "bool", Fallback: false,
			Rules: []liveconfig.Variant{{
				Audience: rule(t, `{"all":[
					{"segment":"whales"},
					{"key":"geo.country","op":"eq","value":"TR"}
				]}`),
				Value: true, Rollout: targeting.Buckets,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// The reference is gone: what is stored is the rule itself.
	audience := doc.Flags[0].Rules[0].Audience
	encoded, err := json.Marshal(audience)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"segment"`) {
		t.Fatalf("a segment reference survived compilation: %s", encoded)
	}

	// And it still means what it meant.
	whale := targeting.Context{"geo.country": "TR"}
	facts.Record{SpendMicros: 150_000_000}.Context(facts.SubjectPlayer, whale)
	if !audience.Matches(whale) {
		t.Error("a Turkish whale did not match")
	}

	elsewhere := targeting.Context{"geo.country": "US"}
	facts.Record{SpendMicros: 150_000_000}.Context(facts.SubjectPlayer, elsewhere)
	if audience.Matches(elsewhere) {
		t.Error("an American whale matched a Turkey-only audience")
	}
}

// Segments compose, because a studio will build "lapsed whales" out of "whales".
func TestSegmentsCanReferToSegments(t *testing.T) {
	segments := liveconfig.Segments{
		"whales": rule(t, `{"key":"commerce.player.lifetime_spend_micros","op":"gte","value":100000000}`),
		"lapsed_whales": rule(t, `{"all":[
			{"segment":"whales"},
			{"key":"activity.player.days_since_last_seen","op":"gte","value":14}
		]}`),
	}

	inlined, err := segments.Inline(rule(t, `{"segment":"lapsed_whales"}`))
	if err != nil {
		t.Fatalf("inline: %v", err)
	}

	away := targeting.Context{}
	facts.Record{SpendMicros: 150_000_000, DaysSinceSeen: 30}.Context(facts.SubjectPlayer, away)
	if !inlined.Matches(away) {
		t.Error("a whale away 30 days did not match 'lapsed whales'")
	}

	here := targeting.Context{}
	facts.Record{SpendMicros: 150_000_000, DaysSinceSeen: 1}.Context(facts.SubjectPlayer, here)
	if inlined.Matches(here) {
		t.Error("a whale who played today matched 'lapsed whales'")
	}
}

// A cycle is caught at publish, once, rather than on every evaluation forever — and the error
// names the path, because "cycle detected" in a project with forty segments is not actionable.
func TestACycleIsRefusedWithThePathThatCausedIt(t *testing.T) {
	segments := liveconfig.Segments{
		"a": rule(t, `{"segment":"b"}`),
		"b": rule(t, `{"segment":"c"}`),
		"c": rule(t, `{"segment":"a"}`),
	}

	_, err := segments.Inline(rule(t, `{"segment":"a"}`))
	if err == nil {
		t.Fatal("a cycle compiled")
	}
	if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("the error does not name the path: %v", err)
	}

	// The direct case too, which is what somebody actually types by accident.
	direct := liveconfig.Segments{"self": rule(t, `{"segment":"self"}`)}
	if _, err := direct.Inline(rule(t, `{"segment":"self"}`)); err == nil {
		t.Error("a segment referring to itself compiled")
	}
}

// A missing segment is refused rather than treated as "matches nobody". An event whose
// audience silently became empty is the failure this design keeps guarding against, and a
// publish is exactly the moment somebody can fix it.
func TestAMissingSegmentIsRefused(t *testing.T) {
	_, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Events: []liveconfig.LiveOpsEvent{{
			Key: "halloween", Kind: "seasonal_event",
			Schedule: everyDay(),
			Audience: rule(t, `{"segment":"ghosts"}`),
			Payload:  map[string]any{"xp": 2},
		}},
	})
	if err == nil {
		t.Fatal("an event naming a segment that does not exist compiled")
	}
	if !strings.Contains(err.Error(), "halloween") || !strings.Contains(err.Error(), "ghosts") {
		t.Errorf("the error names neither the event nor the segment: %v", err)
	}
}

// Publishing is the last moment a person is looking at the event, so a schedule that could
// never run has to be refused here rather than becoming an event that is silently never on.
func TestAnImpossibleScheduleIsRefused(t *testing.T) {
	_, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Events: []liveconfig.LiveOpsEvent{{
			Key: "double_xp", Kind: "seasonal_event",
			// Every day, lasting three: on twice at once, which no player can observe.
			Schedule: liveops.Schedule{
				Mode:     liveops.Daily,
				Anchor:   time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
				Duration: 72 * time.Hour,
			},
			Payload: map[string]any{"xp": 2},
		}},
	})
	if err == nil {
		t.Fatal("an event that overlaps itself compiled")
	}
	if !strings.Contains(err.Error(), "double_xp") {
		t.Errorf("the error does not name the event: %v", err)
	}
}

// The case Hasan described: an offer on the first five days of every month, targeted at a
// segment, created once rather than twelve times a year. The document must carry the
// recurrence rather than a single window, because a single window is exactly what a studio
// would otherwise be retyping every month.
func TestAMonthlyEventSurvivesPublishing(t *testing.T) {
	doc, err := liveconfig.Compile(liveconfig.Input{
		Version:  7,
		Segments: liveconfig.Segments{"engaged_tr": rule(t, `{"all":[{"key":"geo.country","op":"eq","value":"TR"},{"key":"activity.install.playtime_seconds","op":"gt","value":600}]}`)},
		Events: []liveconfig.LiveOpsEvent{{
			Key: "month_start_offer", Kind: "offer",
			Schedule: liveops.Schedule{
				Mode:     liveops.Monthly,
				Anchor:   time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				Duration: 5 * 24 * time.Hour,
			},
			Audience: rule(t, `{"segment":"engaged_tr"}`),
			Payload:  map[string]any{"discount": 20},
			Rollout:  targeting.Buckets,
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(doc.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(doc.Events))
	}

	published := doc.Events[0]
	if published.Audience.Segment != "" || len(published.Audience.All) != 2 {
		t.Errorf("the audience was published as a reference rather than inlined: %+v", published.Audience)
	}

	// March, June, and the second of March — one instant inside a window, one outside.
	for _, at := range []time.Time{
		time.Date(2026, time.March, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
	} {
		if _, on := published.Schedule.ActiveAt(at); !on {
			t.Errorf("the event was off at %s, which is inside the first five days", at)
		}
	}
	if _, on := published.Schedule.ActiveAt(time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC)); on {
		t.Error("the event was on nine days into the month")
	}
}

// The optimisation the package exists for: a project whose rules never name a fact must cost
// zero lookups, and the decision is made once here rather than per request.
func TestADocumentKnowsWhetherItNeedsALookup(t *testing.T) {
	free, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Flags: []liveconfig.Flag{
			{Key: "always_on", Kind: "bool", Fallback: true, Rollout: targeting.Buckets},
			{Key: "turkey_only", Kind: "bool", Fallback: false, Rules: []liveconfig.Variant{{
				Audience: rule(t, `{"key":"geo.country","op":"eq","value":"TR"}`),
				Value:    true, Rollout: targeting.Buckets,
			}}},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if free.NeedsPlayerFacts {
		t.Error("a document of geography rules asked for a player lookup")
	}

	costly, err := liveconfig.Compile(liveconfig.Input{
		Version:  1,
		Segments: liveconfig.Segments{"whales": rule(t, `{"key":"commerce.player.purchases","op":"gt","value":5}`)},
		Events: []liveconfig.LiveOpsEvent{{
			Key: "vip", Kind: "offer",
			Schedule: everyDay(),
			Audience: rule(t, `{"segment":"whales"}`),
			Payload:  map[string]any{},
		}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !costly.NeedsPlayerFacts {
		t.Error("a document whose segment names commerce.* did not ask for a lookup")
	}
}

// A reference that somehow reached evaluation must not match, and must not panic. Analytics
// never crashes a game; an event reaching nobody is findable, a crash in a config handler is
// an outage.
func TestAnUninlinedReferenceMatchesNothing(t *testing.T) {
	raw := rule(t, `{"segment":"whales"}`)
	if raw.Matches(targeting.Context{"commerce.player.purchases": int64(99)}) {
		t.Error("an uncompiled segment reference matched")
	}
}
