package liveconfig_test

import (
	"encoding/json"
	"strings"
	"testing"

	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/targeting"
)

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

// A missing segment is refused rather than treated as "matches nobody". A campaign whose
// audience silently became empty is the failure this design keeps guarding against, and a
// publish is exactly the moment somebody can fix it.
func TestAMissingSegmentIsRefused(t *testing.T) {
	_, err := liveconfig.Compile(liveconfig.Input{
		Version: 1,
		Campaigns: []liveconfig.Campaign{{
			Key: "halloween", Kind: "seasonal_event",
			Audience: rule(t, `{"segment":"ghosts"}`),
			Payload:  map[string]any{"xp": 2},
		}},
	})
	if err == nil {
		t.Fatal("a campaign naming a segment that does not exist compiled")
	}
	if !strings.Contains(err.Error(), "halloween") || !strings.Contains(err.Error(), "ghosts") {
		t.Errorf("the error names neither the campaign nor the segment: %v", err)
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
		Campaigns: []liveconfig.Campaign{{
			Key: "vip", Kind: "offer",
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
// never crashes a game; a campaign reaching nobody is findable, a crash in a config handler is
// an outage.
func TestAnUninlinedReferenceMatchesNothing(t *testing.T) {
	raw := rule(t, `{"segment":"whales"}`)
	if raw.Matches(targeting.Context{"commerce.player.purchases": int64(99)}) {
		t.Error("an uncompiled segment reference matched")
	}
}
