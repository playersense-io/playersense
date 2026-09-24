package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// A project's settings: its name, its retention within its caps, and a deletion that waits.
func TestAProjectsSettingsChangeWithinTheirLimits(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	client := s.as(t, owner)
	created := testclient.Decoded[schemas.ProjectOut](client.Post("/v1/organizations/"+slug+"/projects",
		testclient.JSON(map[string]any{"name": "Moonrise", "slug": unique("moon"), "policy_tier": "child_directed",
			"retention_days": 90})).AssertStatus(http.StatusCreated))
	base := "/v1/organizations/" + slug + "/projects/" + created.Slug

	renamed := testclient.Decoded[schemas.ProjectSettingsOut](client.Patch(base,
		testclient.JSON(map[string]string{"name": "Moonrise HD"})).AssertStatus(http.StatusOK))
	if renamed.Name != "Moonrise HD" || renamed.Slug != created.Slug || renamed.RetentionCap > 90 {
		t.Fatalf("renamed %+v", renamed)
	}

	// Child-directed data is kept for at most ninety days, whatever the plan.
	client.Put(base+"/retention", testclient.JSON(map[string]int{"retention_days": 180})).
		AssertStatus(http.StatusUnprocessableEntity)
	client.Put(base+"/retention", testclient.JSON(map[string]int{"retention_days": 45})).
		AssertStatus(http.StatusUnprocessableEntity)
	lowered := testclient.Decoded[schemas.ProjectSettingsOut](client.Put(base+"/retention",
		testclient.JSON(map[string]int{"retention_days": 30})).AssertStatus(http.StatusOK))
	if lowered.RetentionDays != 30 || lowered.RetentionChangedAt == nil || lowered.RetentionPurgeUntil == nil {
		t.Fatalf("lowered %+v", lowered)
	}

	scheduled := testclient.Decoded[schemas.ProjectSettingsOut](client.Post(base + "/deletion").AssertStatus(http.StatusOK))
	if scheduled.DeletionAt == nil || scheduled.DeletionRequestedBy == "" {
		t.Fatalf("scheduled %+v", scheduled)
	}
	client.Post(base + "/deletion").AssertStatus(http.StatusConflict)
	kept := testclient.Decoded[schemas.ProjectSettingsOut](client.Delete(base + "/deletion").AssertStatus(http.StatusOK))
	if kept.DeletionAt != nil {
		t.Fatalf("kept %+v", kept)
	}
	client.Delete(base + "/deletion").AssertStatus(http.StatusConflict)

	apps := testclient.Decoded[schemas.ProjectAppsOut](client.Get(base + "/apps").AssertStatus(http.StatusOK))
	if apps.Apps == nil {
		t.Fatal("apps is null, want a list")
	}
}
