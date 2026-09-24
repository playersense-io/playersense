package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// TestASegmentsReachIsCountedFromThePlayers: six installs last week, four on iOS. A segment on
// platform holds four of them, one on the game's own context cannot be counted and says why, and
// a segment that names another is counted through it.
func TestASegmentsReachIsCountedFromThePlayers(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)
	for key, rule := range map[string]map[string]any{
		"ios":        {"key": "device.platform", "op": "eq", "value": "ios"},
		"deep":       {"key": "context.progress.level", "op": "gte", "value": 10},
		"ios_active": {"all": []any{map[string]any{"segment": "ios"}, map[string]any{"key": "activity.install.sessions", "op": "gte", "value": 0}}},
	} {
		if err := r.db.Client.Segment.Create().SetProjectID(r.project.ProjectSeq).SetKey(key).SetName(key).
			SetRule(rule).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	out := get[schemas.SegmentReachOut](t, r.member, r.analytics()+"/segments/reach", nil, http.StatusOK)
	if !out.Exact || out.Sampled != 6 || out.WeeklyPlayers != 6 {
		t.Fatalf("sampled %d of %d, exact %v", out.Sampled, out.WeeklyPlayers, out.Exact)
	}
	byKey := map[string]schemas.SegmentReachRow{}
	for _, s := range out.Segments {
		byKey[s.Key] = s
	}
	for _, key := range []string{"ios", "ios_active"} {
		s := byKey[key]
		if !s.Estimable || s.Share == nil || *s.Share < 0.66 || *s.Share > 0.67 {
			t.Fatalf("%s reach %+v", key, s)
		}
		// Four players is under the suppression threshold of five, so the count is withheld.
		if s.Players != nil {
			t.Fatalf("%s shows %d players, under the threshold", key, *s.Players)
		}
	}
	if deep := byKey["deep"]; deep.Estimable || len(deep.Unestimable) != 1 || deep.Unestimable[0] != "context.progress.level" {
		t.Fatalf("deep %+v", deep)
	}
}

// TestTheCatalogueCarriesEachEventsDefinition: a described event shows its category and
// sentence; an undescribed one is custom.
func TestTheCatalogueCarriesEachEventsDefinition(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)
	if err := r.db.Client.EventDefinition.Create().SetProjectID(r.project.ProjectSeq).SetName("level_complete").
		SetCategory("progression").SetDescription("A level finished").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalogue := get[schemas.EventCatalogueOut](t, r.member, r.analytics()+"/events", r.yesterdayOnly(), http.StatusOK)
	for _, e := range catalogue.Events {
		switch e.Name {
		case "level_complete":
			if e.Category != "progression" || e.Description != "A level finished" {
				t.Fatalf("level_complete %+v", e)
			}
		case "rare_event":
			if e.Category != "custom" || e.Description != "" {
				t.Fatalf("rare_event %+v", e)
			}
		}
	}
}

// TestAnEventIsDescribedOnce: written, rewritten, and refused outside the categories.
func TestAnEventIsDescribedOnce(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	base := a.base() + "/event-definitions"
	client.Put(base+"/level_complete", testclient.JSON(map[string]string{
		"category": "progression", "description": "A level finished"})).AssertStatus(http.StatusOK)
	again := testclient.Decoded[schemas.EventDefinitionOut](client.Put(base+"/level_complete", testclient.JSON(map[string]string{
		"category": "progression", "description": "A level finished, won or lost"})).AssertStatus(http.StatusOK))
	if again.Description != "A level finished, won or lost" || again.UpdatedBy == "" {
		t.Fatalf("rewritten %+v", again)
	}
	client.Put(base+"/x", testclient.JSON(map[string]string{"category": "frogs"})).AssertStatus(http.StatusUnprocessableEntity)
	defs := testclient.Decoded[schemas.EventDefinitionListOut](client.Get(base).AssertStatus(http.StatusOK))
	if len(defs.Definitions) != 1 {
		t.Fatalf("%d definitions", len(defs.Definitions))
	}
}
