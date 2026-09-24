package liveconfig

import (
	"strconv"
	"testing"
	"time"

	"playersense.dev/backend/internal/targeting"
)

var testNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func splitDocument(traffic int) Document {
	return Document{Flags: []Flag{{
		Key: "shop_layout", Kind: "string", Fallback: "grid", Rollout: targeting.Buckets,
		Split: &Split{Experiment: "exp-1", Traffic: traffic, Arms: []Arm{
			{ID: "control", Value: "grid", Weight: 5000},
			{ID: "b", Value: "carousel", Weight: 5000},
		}},
	}}}
}

// TestRaisingTrafficOnlyAddsPlayers: everybody in the test at 10% is still in at 50%, in the same
// arm, and the arms split the test about evenly.
func TestRaisingTrafficOnlyAddsPlayers(t *testing.T) {
	small, large := splitDocument(1000), splitDocument(5000)
	inSmall, inLarge := 0, 0
	arms := map[string]int{}
	for i := range 20000 {
		subject := Subject{InstallID: "install-" + strconv.Itoa(i)}
		before := small.Evaluate(testNow, "proj", subject)
		after := large.Evaluate(testNow, "proj", subject)
		if place, in := before.Experiments["shop_layout"]; in {
			inSmall++
			if after.Experiments["shop_layout"] != place {
				t.Fatalf("%s moved from %+v to %+v when traffic rose", subject.InstallID, place, after.Experiments["shop_layout"])
			}
		}
		if place, in := after.Experiments["shop_layout"]; in {
			inLarge++
			arms[place.Arm]++
			want := map[string]any{"control": "grid", "b": "carousel"}[place.Arm]
			if after.Flags["shop_layout"] != want {
				t.Fatalf("arm %s was served %v", place.Arm, after.Flags["shop_layout"])
			}
		} else if after.Flags["shop_layout"] != "grid" {
			t.Fatalf("a player outside the test was served %v", after.Flags["shop_layout"])
		}
	}
	if inSmall < 1600 || inSmall > 2400 || inLarge < 9400 || inLarge > 10600 {
		t.Fatalf("%d in at 10%% and %d at 50%% of 20000", inSmall, inLarge)
	}
	if arms["b"] < inLarge*45/100 || arms["b"] > inLarge*55/100 {
		t.Fatalf("arms %v", arms)
	}
}

// TestAnExperimentMustDivideItsTestWhole: arms that do not add up to the whole test are refused
// when published, not discovered when a player falls between them.
func TestAnExperimentMustDivideItsTestWhole(t *testing.T) {
	in := Input{Flags: []Flag{{Key: "f", Kind: "string", Fallback: "a", Rollout: targeting.Buckets,
		Split: &Split{Experiment: "e", Traffic: 10000, Arms: []Arm{{ID: "a", Value: "a", Weight: 5000}, {ID: "b", Value: "b", Weight: 4000}}}}}}
	if _, err := Compile(in); err == nil {
		t.Fatal("arms adding up to 90% compiled")
	}
	in.Flags[0].Split.Arms[1].Weight = 5000
	if _, err := Compile(in); err != nil {
		t.Fatal(err)
	}
}
