package main

import (
	"net/http"
	"strings"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// An experiment from draft to shipped: written, refused where it cannot work, started into the
// next publish as a split, ramped, concluded and shipped into its flag.
func TestAnExperimentRunsThroughItsFlag(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	client.Post(a.base()+"/flags", testclient.RawJSON(`{"key":"shop_layout","name":"Shop layout","kind":"string","fallback":"grid",
		"rules":[{"value":"carousel","rollout":1000}]}`)).AssertStatus(http.StatusCreated)

	experiments := a.base() + "/experiments"
	body := func(arms string) testclient.RequestOption {
		return testclient.RawJSON(`{"flag":"shop_layout","name":"Carousel shop","hypothesis":"More purchases",
			"primary_metric":"payer_conversion","guardrails":["d1_retention"],"traffic":20,"arms":` + arms + `}`)
	}

	// Every problem at once: a repeated value, a wrong kind, weights short of 100.
	refused := client.Post(experiments, body(`[{"id":"a","value":"grid","weight":50},{"id":"b","value":"grid","weight":30},{"id":"c","value":3,"weight":10}]`)).
		AssertStatus(http.StatusUnprocessableEntity)
	for _, want := range []string{"same value", "arm 3 value", "add up to 90"} {
		if !strings.Contains(refused.String(), want) {
			t.Errorf("the refusal does not say %q: %s", want, refused)
		}
	}

	created := testclient.Decoded[schemas.ExperimentRecordOut](client.Post(experiments,
		body(`[{"id":"control","value":"grid","weight":50,"control":true},{"id":"b","name":"Carousel","value":"carousel","weight":50}]`)).
		AssertStatus(http.StatusCreated))
	if created.Status != "draft" || len(created.Arms) != 2 || created.Traffic != 20 || len(created.Timeline) != 1 {
		t.Fatalf("created %+v", created)
	}
	one := experiments + "/" + created.ID.String()

	client.Post(one + "/start").AssertStatus(http.StatusOK)
	client.Put(one, testclient.RawJSON(`{"name":"Carousel shop","primary_metric":"payer_conversion",
		"arms":[{"id":"a","value":"grid","weight":50},{"id":"b","value":"carousel","weight":50}]}`)).
		AssertStatus(http.StatusConflict)

	// A second test on the same flag waits for the first.
	second := testclient.Decoded[schemas.ExperimentRecordOut](client.Post(experiments,
		body(`[{"id":"a","value":"grid","weight":50},{"id":"b","value":"list","weight":50}]`)).AssertStatus(http.StatusCreated))
	client.Post(experiments + "/" + second.ID.String() + "/start").AssertStatus(http.StatusConflict)

	// The next publish carries the split.
	client.Post(a.base()+"/config/production/publish", testclient.JSON(map[string]string{"environment": "production"})).
		AssertStatus(http.StatusCreated)
	doc := testclient.Decoded[schemas.ConfigVersionDocumentOut](client.Get(a.base() + "/config/production/versions/1").
		AssertStatus(http.StatusOK)).Document
	var split map[string]any
	for _, f := range doc["flags"].([]any) {
		if flag := f.(map[string]any); flag["key"] == "shop_layout" {
			split, _ = flag["split"].(map[string]any)
		}
	}
	if split == nil || split["experiment"] != created.ID.String() || split["traffic"] != float64(2000) ||
		len(split["arms"].([]any)) != 2 {
		t.Fatalf("the published flag's split is %v", split)
	}

	ramped := testclient.Decoded[schemas.ExperimentRecordOut](client.Put(one+"/traffic",
		testclient.JSON(map[string]int{"traffic": 60})).AssertStatus(http.StatusOK))
	if ramped.Traffic != 60 || !strings.Contains(ramped.Timeline[len(ramped.Timeline)-1].Text, "20% → 60%") {
		t.Fatalf("ramped %+v", ramped)
	}

	results := testclient.Decoded[schemas.ExperimentResultsOut](client.Get(
		a.analytics("production") + "/experiment-results/" + created.ID.String()).AssertStatus(http.StatusOK))
	if results.Result == nil || results.Experiment.ID != created.ID {
		t.Fatalf("results %+v", results)
	}

	client.Post(one+"/conclude", testclient.JSON(map[string]string{"winner": "z"})).AssertStatus(http.StatusUnprocessableEntity)
	done := testclient.Decoded[schemas.ExperimentRecordOut](client.Post(one+"/conclude",
		testclient.JSON(map[string]string{"winner": "b", "conclusion": "Carousel converts"})).AssertStatus(http.StatusOK))
	if done.Status != "concluded" || done.Winner == nil || *done.Winner != "b" || done.EndedAt == nil {
		t.Fatalf("concluded %+v", done)
	}
	shipped := testclient.Decoded[schemas.ExperimentRecordOut](client.Post(one + "/ship").AssertStatus(http.StatusOK))
	if !shipped.Shipped {
		t.Fatalf("shipped %+v", shipped)
	}
	client.Post(one + "/ship").AssertStatus(http.StatusConflict)

	var flag struct {
		Fallback string `json:"fallback"`
		Rules    []any  `json:"rules"`
	}
	client.Get(a.base() + "/flags/shop_layout").AssertStatus(http.StatusOK).JSON(&flag)
	if flag.Fallback != "carousel" || len(flag.Rules) != 0 {
		t.Fatalf("after shipping the flag is %+v", flag)
	}

	// Now the second can run, and a draft that never ran can be deleted.
	client.Post(experiments + "/" + second.ID.String() + "/start").AssertStatus(http.StatusOK)
	third := testclient.Decoded[schemas.ExperimentRecordOut](client.Post(experiments,
		body(`[{"id":"a","value":"grid","weight":50},{"id":"b","value":"list","weight":50}]`)).AssertStatus(http.StatusCreated))
	client.Delete(experiments + "/" + third.ID.String()).AssertStatus(http.StatusNoContent)
	client.Delete(one).AssertStatus(http.StatusConflict)

	list := testclient.Decoded[schemas.ExperimentRecordListOut](client.Get(experiments + "?status=running").AssertStatus(http.StatusOK))
	if len(list.Experiments) != 1 || list.Experiments[0].ID != second.ID {
		t.Fatalf("running %+v", list.Experiments)
	}
}

func (a *authoring) analytics(environment string) string {
	return "/v1/organizations/" + a.orgSlug + "/projects/" + a.projSlug + "/analytics/" + environment
}

// TestAPlayerInATestIsToldTheirArm: config-api names the experiment and arm beside the value, so
// the SDK can report the arm on the exposure; a player the test did not take in is told nothing.
func TestAPlayerInATestIsToldTheirArm(t *testing.T) {
	f := newConfigFixture(t)
	f.publish(t, liveconfig.Input{Version: 1, Flags: []liveconfig.Flag{{
		Key: "shop_layout", Kind: "string", Fallback: "grid", Rollout: targeting.Buckets,
		Split: &liveconfig.Split{Experiment: "exp-1", Traffic: 5000, Arms: []liveconfig.Arm{
			{ID: "control", Value: "grid", Weight: 5000}, {ID: "b", Value: "carousel", Weight: 5000},
		}},
	}}})

	in, out := 0, 0
	for n := range 40 {
		var got protocol.ConfigResponse
		f.ask(t, install(1000+n), nil).AssertStatus(http.StatusOK).JSON(&got)
		place, found := got.Experiments["shop_layout"]
		if !found {
			out++
			if got.Flags["shop_layout"] != "grid" {
				t.Fatalf("outside the test and served %v", got.Flags["shop_layout"])
			}
			continue
		}
		in++
		want := map[string]string{"control": "grid", "b": "carousel"}[place.Arm]
		if place.Experiment != "exp-1" || got.Flags["shop_layout"] != want {
			t.Fatalf("in arm %+v and served %v", place, got.Flags["shop_layout"])
		}
	}
	if in == 0 || out == 0 {
		t.Fatalf("%d in the test and %d out of 40 at half traffic", in, out)
	}
}
