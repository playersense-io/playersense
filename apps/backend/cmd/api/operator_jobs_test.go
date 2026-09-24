package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/jobs"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
)

// The retention jobs page (§56, §79): what each of the worker's passes did, and every failure.

// TestAFailedPassIsOnTheJobsPage: a pass the worker recorded as failed is counted, its error is the
// job's last, and it is among the recent passes when narrowed to failures.
func TestAFailedPassIsOnTheJobsPage(t *testing.T) {
	s := newStack(t)
	pool := chdb.NewSQL(testSettings(t).ClickHouseDSN)
	if err := pool.Start(t.Context()); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = pool.Stop(context.Background()) })

	cause := "the bucket said no " + uuid.NewString()
	now := time.Now().UTC()
	jobs.NewClickHouse(pool, nil).Record(t.Context(), jobs.Run{
		Job: jobs.ArchiveExpiry, Started: now.Add(-2 * time.Second), Finished: now,
		Err: errors.New(cause), Counts: map[string]int64{"due": 3, "failed": 3},
	})

	ops, _ := s.staffClient(t, staff.Ops)
	page := testclient.Decoded[schemas.JobsOut](ops.Get("/v1/operator/jobs?failed=true&job=archive_expiry").
		AssertStatus(http.StatusOK))
	if len(page.Jobs) < len(jobs.Names()) {
		t.Fatalf("%d jobs summarised, want every one of %v", len(page.Jobs), jobs.Names())
	}
	var summary *schemas.JobSummaryOut
	for i := range page.Jobs {
		if page.Jobs[i].Job == jobs.ArchiveExpiry {
			summary = &page.Jobs[i]
		}
	}
	if summary == nil || summary.Failures == 0 || summary.LastFailureAt == nil {
		t.Fatalf("archive expiry is %+v", summary)
	}
	found := false
	for _, run := range page.Recent {
		if run.Job != jobs.ArchiveExpiry || run.OK {
			t.Fatalf("narrowed to failed archive expiry and got %+v", run)
		}
		if run.Error == cause && run.Counts["failed"] == 3 && run.FinishedAt.Sub(run.StartedAt) >= 2*time.Second {
			found = true
		}
	}
	if !found {
		t.Fatalf("the recorded failure is not among %d recent passes", len(page.Recent))
	}
	if page.Erasures.ByStatus == nil || page.Archive.Failures == nil {
		t.Fatalf("the queues are %+v %+v", page.Erasures, page.Archive)
	}

	billing, _ := s.staffClient(t, staff.Billing)
	billing.Get("/v1/operator/jobs").AssertStatus(http.StatusForbidden)
}

// TestAFailedArchiveObjectGoesBackToTheSweep: once, on a ticket, and only for the project it
// belongs to.
func TestAFailedArchiveObjectGoesBackToTheSweep(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	projectSlug := s.createProject(t, owner, slug)
	org := s.organization(t, slug)
	proj, err := s.db.Client.Project.Query().Where(project.OrganizationID(org.ID), project.Slug(projectSlug)).
		Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	m, err := s.db.Client.ArchiveManifest.Create().
		SetBucket("test").SetObjectPath("t/" + uuid.NewString() + ".ndjson.zst").
		SetOrgUID(org.UID).SetProjectUID(proj.UID).SetEnvironmentUID(uuid.New()).
		SetMinEventTime(now.AddDate(0, -2, 0)).SetMaxEventTime(now.AddDate(0, -2, 1)).
		SetEventCount(10).SetObjectSizeBytes(100).SetNormalizedInputBytes(200).SetSchemaVersion(1).
		SetDeleteAfter(now.AddDate(0, 0, -1)).SetStatus(archivemanifest.StatusFailed).
		SetFailureReason("permission denied").Save(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	ops, _ := s.staffClient(t, staff.Ops)
	path := "/v1/operator/projects/" + proj.UID.String() + "/archive-manifests/" + m.UID.String() + "/retry"
	testclient.New(t, s.app).Post(path).AssertStatus(http.StatusUnauthorized)
	back := testclient.Decoded[schemas.ArchiveFailureOut](ops.Post(path).AssertStatus(http.StatusOK))
	if back.FailureReason != "" {
		t.Fatalf("retried %+v", back)
	}
	after, err := s.db.Client.ArchiveManifest.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != archivemanifest.StatusDurable {
		t.Fatalf("the object is %s after a retry", after.Status)
	}
	ops.Post(path).AssertStatus(http.StatusConflict)

	other := s.createProject(t, owner, slug)
	otherProj, err := s.db.Client.Project.Query().Where(project.OrganizationID(org.ID), project.Slug(other)).
		Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ops.Post("/v1/operator/projects/" + otherProj.UID.String() + "/archive-manifests/" + m.UID.String() + "/retry").
		AssertStatus(http.StatusNotFound)
}

// TestTheQueuePageNamesEveryWriter: each consumer group, read from the bus this deployment runs.
func TestTheQueuePageNamesEveryWriter(t *testing.T) {
	s := newStack(t)
	ops, _ := s.staffClient(t, staff.Ops)
	queue := testclient.Decoded[schemas.QueueOut](ops.Get("/v1/operator/queue").AssertStatus(http.StatusOK))
	if queue.Bus == "" || len(queue.Groups) != 3 {
		t.Fatalf("queue %+v", queue)
	}
	for _, g := range queue.Groups {
		if g.Error != "" {
			t.Fatalf("%s could not be read: %s", g.Group, g.Error)
		}
	}
	ops.Get("/v1/operator/queue?hours=500").AssertStatus(http.StatusUnprocessableEntity)
}

// TestARefusalIsCountedUnderItsProject: a looping device is refused, and the console sees its
// project refused on that quota, with its name, and never the device.
func TestARefusalIsCountedUnderItsProject(t *testing.T) {
	f := newFixtureWithLimits(t)
	f.publish(t, emptyInput(1))
	refused := 0
	for range core.DeviceConfigBurst * 3 {
		if f.ask(t, install(7), nil).Status == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("never refused")
	}

	s := newStack(t)
	ops, _ := s.staffClient(t, staff.Ops)
	page := testclient.Decoded[schemas.RateLimitsOut](ops.Get(
		"/v1/operator/rate-limits?hours=1&project=" + f.project.ProjectUID).AssertStatus(http.StatusOK))
	if !page.CountedOK || len(page.Budgets) != len(core.Budgets()) || page.Totals["device-config-burst"] < int64(refused) {
		t.Fatalf("page %+v, want at least %d device-config-burst refusals", page, refused)
	}
	var ours *schemas.RefusalScopeOut
	for i := range page.Top {
		if page.Top[i].Scope == f.project.ProjectUID && page.Top[i].Quota == "device-config-burst" {
			ours = &page.Top[i]
		}
	}
	if ours == nil || ours.Count < int64(refused) || ours.Project != f.project.ProjectSlug || ours.Organization != f.project.OrgSlug {
		t.Fatalf("the project's refusals are %+v among %+v", ours, page.Top)
	}
	if page.Project == nil || len(page.Project.Counters) != 2 || page.Project.Counters[1].Count < 1 ||
		page.Project.Counters[1].Limit != core.ProjectConfigSustained {
		t.Fatalf("live counters %+v", page.Project)
	}
}
