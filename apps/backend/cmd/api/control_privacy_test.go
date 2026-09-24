package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// TestAMemberAsksForAPlayersDataAndSeesItListed: an erasure and an export from the dashboard, with
// who asked and why, listed for the project; an export for somebody being erased is refused, and a
// bundle is not downloadable before it exists.
func TestAMemberAsksForAPlayersDataAndSeesItListed(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	base := a.base() + "/privacy"

	erased := testclient.Decoded[schemas.ErasureOut](client.Post(base+"/erasures",
		testclient.JSON(map[string]string{"player_id": "p_8f2a61c0", "reference": "Support #4821"})).
		AssertStatus(http.StatusAccepted))
	again := testclient.Decoded[schemas.ErasureOut](client.Post(base+"/erasures",
		testclient.JSON(map[string]string{"player_id": "p_8f2a61c0"})).AssertStatus(http.StatusAccepted))
	if again.RequestID != erased.RequestID {
		t.Fatal("a second erasure for the same player made a second request")
	}
	client.Post(base+"/exports", testclient.JSON(map[string]string{"player_id": "p_8f2a61c0"})).
		AssertStatus(http.StatusConflict)
	exported := testclient.Decoded[schemas.PrivacyRequestOut](client.Post(base+"/exports",
		testclient.JSON(map[string]string{"player_id": "p_19bd07e4", "reference": "Support #4830"})).
		AssertStatus(http.StatusAccepted))

	list := testclient.Decoded[schemas.PrivacyListOut](client.Get(base).AssertStatus(http.StatusOK))
	if len(list.Requests) != 2 || list.Requests[0].ID != exported.ID || list.Requests[1].Kind != "erase" ||
		list.Requests[1].Reference != "Support #4821" || list.Requests[1].RequestedBy == "" {
		t.Fatalf("listed %+v", list.Requests)
	}
	only := testclient.Decoded[schemas.PrivacyListOut](client.Get(base + "?kind=export").AssertStatus(http.StatusOK))
	if len(only.Requests) != 1 || only.Requests[0].Kind != "export" {
		t.Fatalf("exports only %+v", only.Requests)
	}
	client.Get(base + "/exports/" + exported.ID.String() + "/download").AssertStatus(http.StatusConflict)

	log := testclient.Decoded[schemas.AuditListOut](client.Get(
		"/v1/organizations/" + a.orgSlug + "/audit?target_type=privacy_request").AssertStatus(http.StatusOK))
	if len(log.Events) != 3 {
		t.Fatalf("%d privacy entries in the log, want one per call that made or found a request", len(log.Events))
	}
	for _, e := range log.Events {
		if e.Metadata["player_id"] != nil {
			t.Fatalf("the log names the player: %+v", e)
		}
	}
}
