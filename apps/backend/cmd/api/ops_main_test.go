package main

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
)

// TestTheOperationsConsoleSeesTheWholePlatform: ingestion across tenants, the node's health and
// storage, behind a signed-in staff member with the ops role and nothing else.
func TestTheOperationsConsoleSeesTheWholePlatform(t *testing.T) {
	r := newReporting(t, "standard")
	r.levelEvents(t)

	settings := testSettings(t)
	settings.FirebaseCertificateURL = r.google.URL
	app := mustBuild(t, settings)
	engineer := person(t)
	seedStaff(t, r.db, engineer, staff.Ops)
	operator := testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+r.google.Token(engineer)))

	// Asked about its own project: a shared database holds many projects with a day like this one,
	// and whether a small one makes the platform's top hundred is not what this test is about.
	query := r.yesterdayOnly()
	query.Set("project", strconv.Itoa(r.project.ProjectSeq))
	ingestion := get[schemas.IngestionOut](t, operator, "/v1/operator/ingestion", query, http.StatusOK)
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

	// A customer, however senior in their own organization, is not staff; and a staff member
	// without the role is refused as well.
	customer := testclient.New(t, app, testclient.WithHeader("Authorization", "Bearer "+r.google.Token(person(t))))
	get[schemas.IngestionOut](t, customer, "/v1/operator/ingestion", nil, http.StatusForbidden)
	billing := person(t)
	seedStaff(t, r.db, billing, staff.Billing)
	get[schemas.IngestionOut](t, testclient.New(t, app,
		testclient.WithHeader("Authorization", "Bearer "+r.google.Token(billing))),
		"/v1/operator/ingestion", nil, http.StatusForbidden)
	anon := testclient.New(t, app)
	get[schemas.IngestionOut](t, anon, "/v1/operator/ingestion", url.Values{}, http.StatusUnauthorized)
}
