package main

import (
	"net/http"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// What a flag and an event say about themselves: kept across writes that do not send it, changed
// by those that do, and who wrote each.
func TestLabelsAreKeptUntilChanged(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)

	flag := testclient.Decoded[schemas.FlagOut](client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"gem_bonus","name":"Gem bonus","kind":"number","fallback":0,
		"purpose":"kill_switch","tags":["economy","q3"],"grants_value":true}`)).AssertStatus(http.StatusCreated))
	if flag.Purpose != "kill_switch" || len(flag.Tags) != 2 || !flag.GrantsValue || flag.CreatedBy == "" || flag.UpdatedBy == "" {
		t.Fatalf("created %+v", flag)
	}
	kept := testclient.Decoded[schemas.FlagOut](client.Put(a.base()+"/flags/gem_bonus", testclient.RawJSON(`{
		"name":"Gem bonus","fallback":5}`)).AssertStatus(http.StatusOK))
	if kept.Purpose != "kill_switch" || len(kept.Tags) != 2 || !kept.GrantsValue {
		t.Fatalf("a write without the labels changed them: %+v", kept)
	}
	changed := testclient.Decoded[schemas.FlagOut](client.Put(a.base()+"/flags/gem_bonus", testclient.RawJSON(`{
		"name":"Gem bonus","fallback":5,"purpose":"config","tags":[]}`)).AssertStatus(http.StatusOK))
	if changed.Purpose != "config" || len(changed.Tags) != 0 {
		t.Fatalf("changed %+v", changed)
	}
	plain := testclient.Decoded[schemas.FlagOut](client.Post(a.base()+"/flags", testclient.RawJSON(`{
		"key":"hud","name":"HUD","kind":"bool","fallback":false}`)).AssertStatus(http.StatusCreated))
	if plain.Purpose != "config" || plain.Tags == nil || plain.GrantsValue {
		t.Fatalf("defaults %+v", plain)
	}

	event := func(extra string) string {
		return `{"kind":"seasonal_event","name":"Weekend","payload":{},
			"schedule":{"mode":"weekly","anchor":"2026-01-02T18:00:00Z","duration":"48h"}` + extra + `}`
	}
	client.Post(a.base()+"/liveops-events", testclient.RawJSON(event(`,"key":"weekend","goal":"ad_revenue"`))).
		AssertStatus(http.StatusUnprocessableEntity)
	made := testclient.Decoded[schemas.LiveOpsEventOut](client.Post(a.base()+"/liveops-events",
		testclient.RawJSON(event(`,"key":"weekend","description":"Double gems","goal":"revenue","watch":["d1_retention"]`))).
		AssertStatus(http.StatusCreated))
	if made.Goal == nil || *made.Goal != "revenue" || len(made.Watch) != 1 || made.Description != "Double gems" || made.CreatedBy == "" {
		t.Fatalf("event %+v", made)
	}
	cleared := testclient.Decoded[schemas.LiveOpsEventOut](client.Put(a.base()+"/liveops-events/weekend",
		testclient.RawJSON(event(`,"goal":""`))).AssertStatus(http.StatusOK))
	if cleared.Goal != nil || len(cleared.Watch) != 1 || cleared.Description != "Double gems" {
		t.Fatalf("cleared %+v", cleared)
	}
}

// TestAnEventsWindowIsComparedWithTheWeeksBefore: the latest window that has begun is chosen, its
// days run from two weeks before to today, and every metric has a series of that length.
func TestAnEventsWindowIsComparedWithTheWeeksBefore(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	anchor := time.Now().UTC().Add(-50 * time.Hour).Format(time.RFC3339)
	client.Post(a.base()+"/liveops-events", testclient.RawJSON(`{"key":"sale","kind":"offer","name":"Sale","payload":{},
		"schedule":{"mode":"once","anchor":"`+anchor+`","duration":"24h"}}`)).AssertStatus(http.StatusCreated)
	client.Post(a.base()+"/liveops-events", testclient.RawJSON(`{"key":"later","kind":"offer","name":"Later","payload":{},
		"schedule":{"mode":"once","anchor":"2030-01-01T00:00:00Z","duration":"24h"}}`)).AssertStatus(http.StatusCreated)

	impact := testclient.Decoded[schemas.EventImpactOut](client.Get(
		a.analytics("production") + "/liveops-events/sale/impact").AssertStatus(http.StatusOK))
	if len(impact.Occurrences) != 1 || impact.Live || impact.Method != "baseline" || len(impact.Days) < 15 {
		t.Fatalf("impact %+v", impact)
	}
	for _, metric := range []string{"players", "revenue", "arpdau", "payer_conversion", "playtime_minutes"} {
		if len(impact.Daily[metric].Actual) != len(impact.Days) {
			t.Fatalf("%s has %d days, want %d", metric, len(impact.Daily[metric].Actual), len(impact.Days))
		}
	}
	client.Get(a.analytics("production") + "/liveops-events/later/impact").AssertStatus(http.StatusNotFound)
}
