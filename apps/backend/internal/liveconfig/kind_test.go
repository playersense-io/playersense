package liveconfig_test

import (
	"encoding/json"
	"strings"
	"testing"

	"playersense.dev/backend/internal/liveconfig"
)

// TestAKindAcceptsOnlyItsOwnShape is the check that keeps a flag readable by a shipped build.
//
// A game calls a typed reader on a key. A number flag holding `"50"` is a flag every SDK reads
// as zero, and the game is then running a tuning value nobody chose — silently, because a string
// is a perfectly good JSON value and nothing below this would object.
func TestAKindAcceptsOnlyItsOwnShape(t *testing.T) {
	for _, tc := range []struct {
		kind    liveconfig.Kind
		value   string
		allowed bool
	}{
		{liveconfig.KindBool, `true`, true},
		{liveconfig.KindBool, `false`, true},
		{liveconfig.KindBool, `"true"`, false},
		{liveconfig.KindBool, `1`, false},

		{liveconfig.KindString, `"vip"`, true},
		{liveconfig.KindString, `""`, true},
		{liveconfig.KindString, `5`, false},

		{liveconfig.KindNumber, `50`, true},
		{liveconfig.KindNumber, `1.5`, true},
		{liveconfig.KindNumber, `0`, true},
		{liveconfig.KindNumber, `-1`, true},
		// The confusion this exists to stop: a number typed into a text box.
		{liveconfig.KindNumber, `"50"`, false},
		{liveconfig.KindNumber, `true`, false},

		// json is any JSON value, scalars included: the kind a studio picks when the shape
		// is theirs, and the one PlayerSense has no opinion about.
		{liveconfig.KindJSON, `{"rows":3}`, true},
		{liveconfig.KindJSON, `[1,2,3]`, true},
		{liveconfig.KindJSON, `2.0`, true},
		{liveconfig.KindJSON, `"anything"`, true},

		// Absence, refused for every kind including json. A flag exists to answer without
		// the network, so null is a flag with no answer — the game reaches the state it
		// would have reached had PlayerSense been unreachable, and nothing says why.
		{liveconfig.KindBool, `null`, false},
		{liveconfig.KindString, `null`, false},
		{liveconfig.KindNumber, `null`, false},
		{liveconfig.KindJSON, `null`, false},
	} {
		var value any
		if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
			t.Fatalf("%s is not JSON: %v", tc.value, err)
		}
		err := tc.kind.Accepts(value)
		if tc.allowed && err != nil {
			t.Errorf("%s flag refused %s: %v", tc.kind, tc.value, err)
		}
		if !tc.allowed && err == nil {
			t.Errorf("%s flag accepted %s", tc.kind, tc.value)
		}
	}
}

// TestARefusalNamesWhatArrived keeps the message readable by the person who typed the value.
//
// "%T" would say `map[string]interface {}`, which is a Go error in a dashboard. The person
// reading it wrote an object.
func TestARefusalNamesWhatArrived(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{map[string]any{"a": 1}, "an object"},
		{[]any{1}, "an array"},
		{"text", "a string"},
		{true, "true or false"},
		{float64(1), "a number"},
	} {
		err := liveconfig.KindNumber.Accepts(tc.value)
		if tc.want == "a number" {
			if err != nil {
				t.Errorf("a number was refused: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%v was accepted as a number", tc.value)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("refusing %v says %q, want it to say %q", tc.value, err, tc.want)
		}
	}
}

// TestAnUnknownKindIsRefusedRatherThanAssumed is the failure direction that matters.
//
// A kind this package does not know has to be a flag that will not save. Treating it as "anything
// goes" would make a typo in a dashboard the way a studio opts out of type checking entirely.
func TestAnUnknownKindIsRefusedRatherThanAssumed(t *testing.T) {
	if _, err := liveconfig.ParseKind("boolean"); err == nil {
		t.Error(`ParseKind("boolean") was accepted`)
	} else if !strings.Contains(err.Error(), "bool") {
		t.Errorf("the refusal does not say what the kinds are: %v", err)
	}
	if err := liveconfig.Kind("boolean").Accepts(true); err == nil {
		t.Error("an unknown kind accepted a value")
	}
	for _, kind := range liveconfig.Kinds() {
		if _, err := liveconfig.ParseKind(string(kind)); err != nil {
			t.Errorf("ParseKind(%q) = %v", kind, err)
		}
	}
}

// TestCheckRefusesWhatWouldNotCompile is the write-time half of the publish.
//
// Same walk, same errors, run at the moment one person, one rule and one mistake coincide. A
// cycle is the case that argues for it: A -> B -> A cannot exist until the write that closes the
// loop, and afterwards nothing points at which of the two rules to change.
func TestCheckRefusesWhatWouldNotCompile(t *testing.T) {
	segments := liveconfig.Segments{
		"payers": rule(t, `{"key":"commerce.revenue","op":"gt","value":0}`),
		"whales": rule(t, `{"segment":"payers"}`),
	}

	// A reference that resolves is fine, however deep.
	if err := segments.Check(rule(t, `{"all":[{"segment":"whales"}]}`)); err != nil {
		t.Errorf("a resolvable rule was refused: %v", err)
	}
	// An empty rule means everybody, which is a legitimate audience rather than a mistake.
	if err := segments.Check(rule(t, `{}`)); err != nil {
		t.Errorf("an empty rule was refused: %v", err)
	}

	// A name that does not exist, refused rather than silently matching nobody.
	err := segments.Check(rule(t, `{"segment":"whals"}`))
	if err == nil {
		t.Fatal("a rule naming a segment that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "whals") {
		t.Errorf("the refusal does not name the missing segment: %v", err)
	}

	// And the cycle, with the candidate in the set under its own key — which is how a handler
	// checks a rule whose row does not exist yet, or still holds the old version.
	closing := liveconfig.Segments{
		"payers": rule(t, `{"segment":"whales"}`),
		"whales": rule(t, `{"segment":"payers"}`),
	}
	err = closing.Check(rule(t, `{"segment":"payers"}`))
	if err == nil {
		t.Fatal("a cycle was accepted")
	}
	if !strings.Contains(err.Error(), "refers to itself") {
		t.Errorf("the refusal does not say it is a cycle: %v", err)
	}
	// The path is named, because "cycle detected" in a project with forty segments is not
	// something anybody can act on.
	if !strings.Contains(err.Error(), "->") {
		t.Errorf("the refusal does not name the path: %v", err)
	}
}
