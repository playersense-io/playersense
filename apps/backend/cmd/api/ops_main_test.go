package main

import (
	"net/http"
	"net/url"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
)

// TestTheOperationsConsoleSeesTheWholePlatform: ingestion across tenants, the node's health and
// storage, behind the operator token and nothing else.
func TestTheOperationsConsoleSeesTheWholePlatform(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)

	settings := testSettings(t)
	app := mustBuild(t, settings)
	operator := testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+settings.AdminToken))

	ingestion := get[schemas.IngestionOut](t, operator, "/v1/operator/ingestion", r.yesterdayOnly(), http.StatusOK)
	var ours *schemas.IngestionProjectOut
	for i, p := range ingestion.Projects {
		if int(p.ProjectSeq) == r.project.ProjectSeq {
			ours = &ingestion.Projects[i]
		}
	}
	if ours == nil || ours.Events != 8 || ours.Project != r.project.ProjectSlug || ours.Organization != r.project.OrgSlug {
		t.Fatalf("the seeded project is %+v among %d, want 8 events under its own names", ours, len(ingestion.Projects))
	}
	var events uint64
	for _, b := range ingestion.Series {
		events += b.Events
	}
	if events < 8 {
		t.Fatalf("the fleet's series holds %d events; the seeded project alone sent 8", events)
	}

	health := get[schemas.ClickHouseHealthOut](t, operator, "/v1/operator/clickhouse", nil, http.StatusOK)
	tables := map[string]bool{}
	for _, tbl := range health.Tables {
		tables[tbl.Table] = true
	}
	if health.Version == "" || !tables["events_raw"] || !tables["activity_1h"] || len(health.Replicas) == 0 {
		t.Fatalf("health is version %q with tables %v and %d replicas", health.Version, tables, len(health.Replicas))
	}

	storage := get[schemas.StorageOut](t, operator, "/v1/operator/storage", r.yesterdayOnly(), http.StatusOK)
	found := false
	for _, p := range storage.Projects {
		if int(p.ProjectSeq) == r.project.ProjectSeq && p.Bytes > 0 && p.Share > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("storage %+v does not attribute bytes to the seeded project", storage.Projects)
	}

	// A person, however senior, is not the operator.
	get[schemas.IngestionOut](t, r.member, "/v1/operator/ingestion", nil, http.StatusNotFound)
	anon := testclient.New(t, app)
	get[schemas.IngestionOut](t, anon, "/v1/operator/ingestion", url.Values{}, http.StatusUnauthorized)
}
