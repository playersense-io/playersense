package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/report"
	"playersense.dev/backend/ent/reportrun"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/reports"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/testsupport"
)

// A report saved, previewed, run and followed on its stream: its notify reaches the channel, its
// export is a file to download, and a dry run sends nothing.
func TestAReportRunsAndIsFollowed(t *testing.T) {
	var posts atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts.Add(1) }))
	defer receiver.Close()
	s := newStackWith(t, muzak.WithSingleton(&notify.Sender{HTTP: notify.NewClient(2*time.Second, true),
		Mail: &mail.Recorder{}, AllowPrivate: true}))
	owner := person(t)
	org := s.createOrg(t, owner)
	a := &authoring{stack: s, owner: owner, orgSlug: org, projSlug: s.createProject(t, owner, org)}
	client := a.client(t)

	ch := testclient.Decoded[schemas.ChannelOut](client.Post(a.base()+"/channels", testclient.JSON(map[string]any{
		"type": "webhook", "name": "Hook", "config": map[string]any{"url": receiver.URL}})).AssertStatus(http.StatusCreated))

	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "start", "position": map[string]int{"x": 0, "y": 0}, "data": map[string]any{"kind": "start", "title": "Start", "config": map[string]any{}}},
			{"id": "sched", "position": map[string]int{"x": 0, "y": -200}, "data": map[string]any{"kind": "schedule", "title": "Mondays",
				"config": map[string]any{"cron": "0 9 * * 1", "timezone": "UTC"}}},
			{"id": "src", "position": map[string]int{"x": 100, "y": 0}, "data": map[string]any{"kind": "source", "title": "Purchases",
				"config": map[string]any{"source": "purchases", "range": "7"}}},
			{"id": "grp", "position": map[string]int{"x": 200, "y": 0}, "data": map[string]any{"kind": "group", "title": "By country",
				"config": map[string]any{"by": []string{"country"}, "metrics": []map[string]any{{"fn": "sum", "column": "price", "as": "revenue"}}}}},
			{"id": "tell", "position": map[string]int{"x": 300, "y": 0}, "data": map[string]any{"kind": "notify", "title": "Tell",
				"config": map[string]any{"channels": []string{ch.ID.String()}, "when": "always", "subject": "{{report}}: {{rows}} countries"}}},
			{"id": "file", "position": map[string]int{"x": 300, "y": 100}, "data": map[string]any{"kind": "export", "title": "CSV",
				"config": map[string]any{"file": "csv", "destination": "download"}}},
			{"id": "finish", "position": map[string]int{"x": 400, "y": 0}, "data": map[string]any{"kind": "finish", "title": "Finish", "config": map[string]any{}}},
		},
		"edges": []map[string]any{
			{"id": "e0", "source": "sched", "target": "start", "targetHandle": "when"},
			{"id": "e1", "source": "src", "target": "grp"},
			{"id": "e2", "source": "grp", "target": "tell"},
			{"id": "e3", "source": "grp", "target": "file"},
		},
	}
	client.Post(a.base()+"/reports", testclient.JSON(map[string]any{"name": "Bad", "graph": map[string]any{
		"nodes": []map[string]any{{"id": "x", "data": map[string]any{"kind": "teleport"}}}}})).AssertStatus(http.StatusUnprocessableEntity)
	made := testclient.Decoded[schemas.ReportOut](client.Post(a.base()+"/reports", testclient.JSON(map[string]any{
		"name": "Weekly revenue", "graph": graph})).AssertStatus(http.StatusCreated))
	if made.NextRunAt == nil || made.NextRunAt.Weekday() != time.Monday || made.NextRunAt.Hour() != 9 {
		t.Fatalf("next run %v", made.NextRunAt)
	}

	preview := testclient.Decoded[schemas.ReportPreviewOut](client.Post(a.base()+"/reports/preview", testclient.JSON(map[string]any{
		"name": "Weekly revenue", "graph": graph})).AssertStatus(http.StatusOK))
	if p := preview.Nodes["tell"]; p.Notify == nil || !p.Notify.Send || p.Notify.Title != "Weekly revenue: 0 countries" {
		t.Fatalf("preview of tell %+v", p)
	}
	if posts.Load() != 0 {
		t.Fatal("a preview sent something")
	}

	started := testclient.Decoded[schemas.StartedRunOut](client.Post(a.base()+"/reports/"+made.ID.String()+"/runs",
		testclient.JSON(map[string]any{})).AssertStatus(http.StatusAccepted))
	events := follow(t, s.app, started.StreamURL)
	if events[0] != "run_started" || events[len(events)-1] != "run_finished" || !contains(strings.Join(events, ","), "delivery") {
		t.Fatalf("events %v", events)
	}
	testclient.New(t, s.app).Get(strings.Replace(started.StreamURL, "token=", "token=x", 1)).AssertStatus(http.StatusUnauthorized)

	run := testclient.Decoded[schemas.ReportRunOut](client.Get(a.base() + "/reports/" + made.ID.String() + "/runs/" + started.Run.ID.String()).
		AssertStatus(http.StatusOK))
	if run.Status != "succeeded" || len(run.Deliveries) != 2 || len(run.Exports) != 1 || posts.Load() != 1 {
		t.Fatalf("run %+v, %d posts", run, posts.Load())
	}
	file := client.Get(a.base() + "/reports/" + made.ID.String() + "/runs/" + run.ID.String() + "/exports/file").AssertStatus(http.StatusOK)
	if !strings.HasPrefix(file.String(), "Country,revenue") {
		t.Fatalf("the file begins %q", file.String())
	}

	dry := testclient.Decoded[schemas.StartedRunOut](client.Post(a.base()+"/reports/"+made.ID.String()+"/runs",
		testclient.JSON(map[string]any{"dry": true, "focus": "tell"})).AssertStatus(http.StatusAccepted))
	follow(t, s.app, dry.StreamURL)
	if posts.Load() != 1 {
		t.Fatal("a dry run sent something")
	}
	runs := testclient.Decoded[schemas.ReportRunListOut](client.Get(a.base() + "/reports/" + made.ID.String() + "/runs").AssertStatus(http.StatusOK))
	if len(runs.Runs) != 2 || !runs.Runs[0].Dry || len(runs.Runs[0].Deliveries) != 1 {
		t.Fatalf("runs %+v", runs.Runs)
	}
}

// follow reads a run's stream to its end and returns the events' names.
func follow(t *testing.T, app *muzak.App, url string) []string {
	t.Helper()
	server := httptest.NewServer(app)
	defer server.Close()
	res, err := http.Get(server.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("stream %d: %s", res.StatusCode, body)
	}
	var names []string
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			names = append(names, name)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok && len(names) > 0 && names[len(names)-1] == "run_finished" {
			var e map[string]any
			_ = json.Unmarshal([]byte(data), &e)
			return names
		}
	}
	return names
}

// TestAScheduledReportRunsOnce: a report due now is claimed by one pass, run on its schedule, and
// moved to its next time; a second pass finds nothing due.
func TestAScheduledReportRunsOnce(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	org := s.createOrg(t, owner)
	a := &authoring{stack: s, owner: owner, orgSlug: org, projSlug: s.createProject(t, owner, org)}
	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "start", "data": map[string]any{"kind": "start", "config": map[string]any{}}},
			{"id": "sched", "data": map[string]any{"kind": "schedule", "config": map[string]any{"cron": "*/5 * * * *"}}},
			{"id": "src", "data": map[string]any{"kind": "source", "config": map[string]any{"source": "sessions", "range": "7"}}},
			{"id": "table", "data": map[string]any{"kind": "table", "config": map[string]any{}}},
			{"id": "finish", "data": map[string]any{"kind": "finish", "config": map[string]any{}}},
		},
		"edges": []map[string]any{
			{"id": "e0", "source": "sched", "target": "start", "targetHandle": "when"},
			{"id": "e1", "source": "src", "target": "table"},
		},
	}
	made := testclient.Decoded[schemas.ReportOut](a.client(t).Post(a.base()+"/reports", testclient.JSON(map[string]any{
		"name": "Sessions", "graph": graph})).AssertStatus(http.StatusCreated))
	past := time.Now().UTC().Add(-time.Minute)
	if err := s.db.Client.Report.Update().Where(report.UID(made.ID)).SetNextRunAt(past).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn := testsupport.ClickHouse(t, s.settings)
	scheduler := &reports.Scheduler{
		Runner: &reports.Runner{DB: s.db, Notify: &notify.Sender{HTTP: notify.NewClient(time.Second, true)}},
		Engine: query.NewEngine(conn, query.DefaultLimits(), 5),
	}
	if _, err := scheduler.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	row, err := s.db.Client.Report.Query().Where(report.UID(made.ID)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if row.NextRunAt == nil || !row.NextRunAt.After(time.Now()) {
		t.Fatalf("next run %v", row.NextRunAt)
	}
	runs, err := s.db.Client.ReportRun.Query().Where(reportrun.ReportID(row.ID)).All(t.Context())
	if err != nil || len(runs) != 1 || runs[0].Trigger != reportrun.TriggerSchedule || runs[0].Status != reportrun.StatusQuiet {
		t.Fatalf("runs %+v, %v", runs, err)
	}
	if _, err := scheduler.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.db.Client.ReportRun.Query().Where(reportrun.ReportID(row.ID)).Count(t.Context()); n != 1 {
		t.Fatalf("%d runs after a second pass", n)
	}
}
