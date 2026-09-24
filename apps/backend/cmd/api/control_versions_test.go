package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// An environment's history, and going back in it.

// TestARollbackRepublishesTheOldDocument: two publishes, a rollback to the first, and the
// environment serves the first document again as a third version that says so, with the history
// still whole and the audit log naming it.
func TestARollbackRepublishesTheOldDocument(t *testing.T) {
	a := newAuthoring(t)
	client := a.client(t)
	publish := func(note string) {
		t.Helper()
		client.Post(a.base()+"/config/production/publish",
			testclient.JSON(map[string]string{"environment": "production", "note": note})).
			AssertStatus(http.StatusCreated)
	}

	client.Post(a.base()+"/flags", testclient.RawJSON(`{"key":"hud_v2","name":"HUD v2","kind":"bool","fallback":false}`)).
		AssertStatus(http.StatusCreated)
	publish("one flag")
	client.Post(a.base()+"/flags", testclient.RawJSON(`{"key":"shop_v3","name":"Shop v3","kind":"bool","fallback":true}`)).
		AssertStatus(http.StatusCreated)
	publish("two flags")

	versions := a.base() + "/config/production/versions"
	client.Post(versions+"/2/rollback", testclient.JSON(map[string]string{"reason": "x"})).
		AssertStatus(http.StatusConflict)
	client.Post(versions+"/1/rollback", testclient.JSON(map[string]string{})).
		AssertStatus(http.StatusUnprocessableEntity)
	back := testclient.Decoded[schemas.ConfigVersionOut](client.Post(versions+"/1/rollback",
		testclient.JSON(map[string]string{"reason": "shop v3 crashes on iOS"})).AssertStatus(http.StatusCreated))
	if back.Version != 3 || back.RollbackOf != 1 || back.Flags != 1 || !back.Current ||
		back.Note != "Rolled back to v1: shop v3 crashes on iOS" {
		t.Fatalf("rolled back %+v", back)
	}

	list := testclient.Decoded[schemas.ConfigVersionListOut](client.Get(versions + "?limit=2").AssertStatus(http.StatusOK))
	if list.Current != 3 || len(list.Versions) != 2 || list.Versions[0].Version != 3 || !list.Versions[0].Current ||
		list.Versions[1].Current || list.Next != 2 {
		t.Fatalf("history %+v", list)
	}
	rest := testclient.Decoded[schemas.ConfigVersionListOut](client.Get(versions + "?before=2").AssertStatus(http.StatusOK))
	if len(rest.Versions) != 1 || rest.Versions[0].Version != 1 || rest.Next != 0 {
		t.Fatalf("second page %+v", rest)
	}

	served := testclient.Decoded[schemas.ConfigVersionDocumentOut](client.Get(versions + "/3").AssertStatus(http.StatusOK))
	first := testclient.Decoded[schemas.ConfigVersionDocumentOut](client.Get(versions + "/1").AssertStatus(http.StatusOK))
	if served.Document["version"] != float64(3) || len(served.Document["flags"].([]any)) != 1 ||
		first.Document["version"] != float64(1) {
		t.Fatalf("v3 serves %v; v1 was %v", served.Document, first.Document)
	}
	client.Get(versions + "/9").AssertStatus(http.StatusNotFound)

	log := testclient.Decoded[schemas.AuditListOut](client.Get(
		"/v1/organizations/" + a.orgSlug + "/audit?action=liveops.rollback").AssertStatus(http.StatusOK))
	if len(log.Events) != 1 || log.Events[0].Metadata["rollback_of"] != float64(1) {
		t.Fatalf("audit %+v", log.Events)
	}
}
