package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/testsupport"
)

// A key is rolled with a grace and without one, and its requests are counted by the hour.
func TestAKeyIsRolledAndItsUseCounted(t *testing.T) {
	// The service's own tracker, with a flush the test drives rather than a ticker it waits on.
	settings := testSettings(t)
	usage := core.NewKeyUsage(testsupport.Cache(t, settings), globalTier(t, settings), nil, time.Hour)
	s := newStackWith(t, muzak.WithSingleton(usage))
	owner := person(t)
	org := s.createOrg(t, owner)
	a := &authoring{stack: s, owner: owner, orgSlug: org, projSlug: s.createProject(t, owner, org)}
	client := a.client(t)
	keys := a.base() + "/keys"
	created := testclient.Decoded[schemas.APIKeyCreatedOut](client.Post(keys, testclient.JSON(map[string]string{
		"kind": "server", "environment": "production", "name": "Backend"})).AssertStatus(http.StatusCreated))
	if created.CreatedBy == "" {
		t.Fatalf("created %+v", created.APIKeyOut)
	}

	// Two requests with it, counted and flushed as the running service does.
	for range 2 {
		testclient.New(t, a.app, testclient.WithHeader("Authorization", "Bearer "+created.Secret)).
			Get("/v1/privacy/erasures/00000000-0000-4000-8000-000000000000").AssertStatus(http.StatusNotFound)
	}
	usage.Flush(context.Background())
	one := testclient.Decoded[schemas.KeyUsageOut](client.Get(keys + "/" + created.ID.String() + "/usage").
		AssertStatus(http.StatusOK))
	if one.Total != 2 || one.LastUsedAt == nil || len(one.Hours) != 24 {
		t.Fatalf("usage %+v", one)
	}
	listed := testclient.Decoded[schemas.APIKeyListOut](client.Get(keys).AssertStatus(http.StatusOK))
	for _, k := range listed.Keys {
		if k.ID == created.ID && (len(k.Hourly) != 24 || k.Hourly[23] != 2) {
			t.Fatalf("listed %+v", k)
		}
	}

	// With a grace, both work until it ends; without, the old one stops at once.
	rolled := testclient.Decoded[schemas.RolledAPIKeyOut](client.Post(keys+"/"+created.ID.String()+"/roll",
		testclient.JSON(map[string]int{"grace_hours": 2})).AssertStatus(http.StatusCreated))
	if rolled.Key.Secret == "" || rolled.Replaced.ReplacedBy == nil || *rolled.Replaced.ReplacedBy != rolled.Key.ID ||
		rolled.Replaced.ExpiresAt == nil || rolled.Replaced.ExpiresAt.Sub(time.Now()) < time.Hour || rolled.Replaced.RevokedAt != nil {
		t.Fatalf("rolled %+v", rolled)
	}
	client.Post(keys+"/"+created.ID.String()+"/roll", testclient.JSON(map[string]int{})).AssertStatus(http.StatusConflict)

	again := testclient.Decoded[schemas.RolledAPIKeyOut](client.Post(keys+"/"+rolled.Key.ID.String()+"/roll",
		testclient.JSON(map[string]int{})).AssertStatus(http.StatusCreated))
	if again.Replaced.RevokedAt == nil || again.Replaced.RevokedBy == "" {
		t.Fatalf("rolled with no grace %+v", again.Replaced)
	}
	testclient.New(t, a.app, testclient.WithHeader("Authorization", "Bearer "+rolled.Key.Secret)).
		Get("/v1/privacy/erasures/00000000-0000-4000-8000-000000000000").AssertStatus(http.StatusUnauthorized)
	testclient.New(t, a.app, testclient.WithHeader("Authorization", "Bearer "+again.Key.Secret)).
		Get("/v1/privacy/erasures/00000000-0000-4000-8000-000000000000").AssertStatus(http.StatusNotFound)
}
