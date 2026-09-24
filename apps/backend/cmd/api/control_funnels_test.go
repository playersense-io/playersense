package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// TestAFunnelIsSavedByName: saved, checked as a query is, one name per project, changed and
// forgotten, and what was saved is what a query takes.
func TestAFunnelIsSavedByName(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	funnels := a.base() + "/funnels"

	client.Post(funnels, testclient.RawJSON(`{"name":"Onboarding","kind":"steps","steps":[{"event":"tutorial_start"}]}`)).
		AssertStatus(http.StatusUnprocessableEntity)

	saved := testclient.Decoded[schemas.SavedFunnelOut](client.Post(funnels, testclient.RawJSON(`{
		"name":"Onboarding","kind":"steps","window":"1d",
		"steps":[{"event":"tutorial_start"},{"event":"tutorial_done","where":[{"property":"skipped","value":"false"}]}]}`)).
		AssertStatus(http.StatusCreated))
	if saved.Kind != "steps" || len(saved.Steps) != 2 || saved.Steps[1].Where[0].Property != "skipped" || saved.CreatedBy == "" {
		t.Fatalf("saved %+v", saved)
	}
	client.Post(funnels, testclient.RawJSON(`{"name":"Onboarding","kind":"progression",
		"progression":{"event":"level_up","property":"level","from":1,"to":10}}`)).AssertStatus(http.StatusConflict)

	changed := testclient.Decoded[schemas.SavedFunnelOut](client.Put(funnels+"/"+saved.ID.String(), testclient.RawJSON(`{
		"name":"Levels","kind":"progression","progression":{"event":"level_up","property":"level","from":1,"to":10}}`)).
		AssertStatus(http.StatusOK))
	if changed.Name != "Levels" || changed.Progression == nil || changed.Progression.To != 10 || len(changed.Steps) != 0 {
		t.Fatalf("changed %+v", changed)
	}

	// What was saved is the body a query takes, once given a range.
	client.Post(a.analytics("production")+"/funnels/query", testclient.JSON(map[string]any{
		"kind": changed.Kind, "progression": changed.Progression, "from": "2026-09-01", "to": "2026-09-07",
	})).AssertStatus(http.StatusOK)

	list := testclient.Decoded[schemas.SavedFunnelListOut](client.Get(funnels).AssertStatus(http.StatusOK))
	if len(list.Funnels) != 1 {
		t.Fatalf("%d funnels", len(list.Funnels))
	}
	client.Delete(funnels + "/" + saved.ID.String()).AssertStatus(http.StatusNoContent)
	client.Get(funnels + "/" + saved.ID.String()).AssertStatus(http.StatusNotFound)
}
