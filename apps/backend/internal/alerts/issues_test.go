package alerts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/webhookoutbox"
	"playersense.dev/backend/internal/alerts"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/webhooks"
)

func at(t time.Time) *time.Time { return &t }

// The detector's judgement, case by case: each announcement happens once, whatever the volume.
func TestAnIssueIsAnnouncedOnceForEachThingThatHappensToIt(t *testing.T) {
	now := time.Date(2026, 9, 26, 14, 30, 0, 0, time.UTC)
	r := alerts.DefaultIssueRules
	fresh := alerts.IssueFacts{FirstSeen: now.Add(-10 * time.Minute), LastSeen: now, ThisHour: 5000}

	// New: once. A thousand more events the next minute are the same issue, already announced.
	d := alerts.DecideIssue(fresh, alerts.IssueState{}, "unresolved", nil, now, r)
	if !d.New || d.Spiking {
		t.Fatalf("a new issue: %+v; want new, and not a spike (it has no usual rate yet)", d)
	}
	if again := alerts.DecideIssue(fresh, d.Next, "unresolved", nil, now.Add(time.Minute), r); again.New || again.Spiking {
		t.Fatalf("the same issue a minute later: %+v", again)
	}

	// Old issues are not news when the detector first meets them.
	old := alerts.IssueFacts{FirstSeen: now.Add(-72 * time.Hour), LastSeen: now, ThisHour: 3, Day: 72}
	if d := alerts.DecideIssue(old, alerts.IssueState{}, "unresolved", nil, now, r); d.New || !d.Next.Known {
		t.Fatalf("an issue three days old met for the first time: %+v", d)
	}

	// Regressed: once per resolution, and back among the unresolved.
	resolved := now.Add(-time.Hour)
	d = alerts.DecideIssue(old, alerts.IssueState{Known: true}, "resolved", at(resolved), now, r)
	if !d.Regressed || !d.Unresolve {
		t.Fatalf("seen after it was resolved: %+v", d)
	}
	if again := alerts.DecideIssue(old, d.Next, "resolved", at(resolved), now.Add(time.Minute), r); again.Regressed {
		t.Fatalf("the same regression twice: %+v", again)
	}

	// Ignored is silent, spike or not.
	loud := alerts.IssueFacts{FirstSeen: now.Add(-72 * time.Hour), LastSeen: now, ThisHour: 9000, Day: 240}
	if d := alerts.DecideIssue(loud, alerts.IssueState{Known: true}, "ignored", nil, now, r); d.Spiking || d.New {
		t.Fatalf("an ignored issue: %+v", d)
	}

	// Spiking: once per episode. Ten an hour usually; 150 this hour is a spike.
	usual := alerts.IssueFacts{FirstSeen: now.Add(-72 * time.Hour), LastSeen: now, Day: 240}
	spiking := usual
	spiking.ThisHour = 150
	d = alerts.DecideIssue(spiking, alerts.IssueState{Known: true}, "unresolved", nil, now, r)
	if !d.Spiking {
		t.Fatalf("150 an hour against a usual 10: %+v", d)
	}
	worse := usual
	worse.ThisHour = 900
	if again := alerts.DecideIssue(worse, d.Next, "unresolved", nil, now.Add(20*time.Minute), r); again.Spiking {
		t.Fatalf("the same spike, worse: announced again: %+v", again)
	}
	// It dips as the hour turns: still the same episode.
	dip := usual
	dip.ThisHour, dip.PrevHour = 2, 400
	if again := alerts.DecideIssue(dip, d.Next, "unresolved", nil, now.Add(40*time.Minute), r); !again.Next.Spiking {
		t.Fatalf("a spike that dips at the top of the hour ended: %+v", again)
	}
	// A whole hour back near normal ends it, quietly; the next one is a new episode.
	calm := usual
	calm.ThisHour, calm.PrevHour = 4, 9
	over := alerts.DecideIssue(calm, d.Next, "unresolved", nil, now.Add(2*time.Hour), r)
	if over.Spiking || over.Next.Spiking {
		t.Fatalf("an hour back near normal: %+v", over)
	}
	if next := alerts.DecideIssue(spiking, over.Next, "unresolved", nil, now.Add(5*time.Hour), r); !next.Spiking {
		t.Fatalf("a second spike, hours later: %+v", next)
	}

	// Ten times one an hour is not a spike anybody needs: there is a floor.
	quiet := alerts.IssueFacts{FirstSeen: now.Add(-72 * time.Hour), LastSeen: now, Day: 24, ThisHour: 12}
	if d := alerts.DecideIssue(quiet, alerts.IssueState{Known: true}, "unresolved", nil, now, r); d.Spiking {
		t.Fatalf("12 against a usual 1: %+v", d)
	}
}

// writeLogs puts records straight into ClickHouse's logs table, as the writer would, so the
// rollups the detector reads fill from them.
func writeLogs(t *testing.T, conn *chdb.Conn, orgSeq, projectSeq, envSeq int, fingerprint uint64, kind, title string, n int, when time.Time) {
	t.Helper()
	db, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	block, err := db.PrepareBatch(context.Background(), `INSERT INTO logs (
		org_seq, project_seq, environment_seq, retention_days,
		timestamp, received_at, record_id, severity, kind, logger,
		message, exception_type, exception_value, stacktrace, breadcrumbs, handled, culprit,
		fingerprint, session_id, install_id, player_id,
		app_version, app_build, platform, os_version, device_model, country_code, sdk_name, sdk_version,
		attributes, size)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		var player *string
		if err := block.Append(uint32(orgSeq), uint32(projectSeq), uint16(envSeq), uint16(14),
			when.Add(time.Duration(i)*time.Millisecond), when, uuid.New(), "fatal", kind, "unity",
			"", "NullReferenceException", title, "", "", uint8(0), "Game.Shop.Buy",
			fingerprint, "", uuid.New(), player,
			"2.7.1", "", "android", "14", "SM-S918B", "TR", "unity", "0.1.0",
			map[string]string{}, uint32(200)); err != nil {
			t.Fatal(err)
		}
	}
	if err := block.Send(); err != nil {
		t.Fatal(err)
	}
}

// The whole path, against the real rollups: a thousand crashes of one kind and a handful of
// another are two issues, two events, and one message in the channel.
func TestAThousandCrashesAreOneMessage(t *testing.T) {
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	conn := testsupport.ClickHouse(t, settings)
	ctx := context.Background()
	p := testsupport.SeedProject(t, db, "standard")

	var mu sync.Mutex
	var posts []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		posts = append(posts, body)
		mu.Unlock()
	}))
	defer server.Close()
	ch, err := db.Client.NotificationChannel.Create().SetProjectID(p.ProjectSeq).SetType("webhook").SetName("Crashes").
		SetSecret(map[string]string{"url": server.URL, "signing_secret": "whsec_test"}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Client.NotificationRule.Create().SetOrganizationID(p.OrgSeq).SetProjectID(p.ProjectSeq).
		SetName("Crashes to ops").SetEvents([]string{alerts.IssueNew}).SetChannels([]uuid.UUID{ch.UID}).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	base := uint64(time.Now().UnixNano())
	writeLogs(t, conn, p.OrgSeq, p.ProjectSeq, p.EnvironmentID, base+1, "crash", "Object reference not set", 1000, now.Add(-3*time.Minute))
	writeLogs(t, conn, p.OrgSeq, p.ProjectSeq, p.EnvironmentID, base+2, "crash", "Index was outside the bounds", 5, now.Add(-2*time.Minute))

	detector := &alerts.IssueDetector{DB: db, CH: conn}
	n, err := detector.Pass(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("announced %d; want 2, one for each issue, not one for each crash", n)
	}
	if again, _ := detector.Pass(ctx, now.Add(time.Minute)); again != 0 {
		t.Fatalf("announced %d more a minute later; want none", again)
	}

	// The events go through the outbox to the rules, as the dispatcher hands them over.
	events, err := db.Client.WebhookOutbox.Query().Where(webhookoutbox.ProjectID(p.ProjectSeq)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sink := &alerts.Sink{Client: db.Client}
	for _, e := range events {
		if err := sink.Enqueue(ctx, webhooks.ClaimedEvent{EventUID: e.UID, ProjectID: e.ProjectID,
			Environment: e.Environment, Type: e.Type, Body: e.Body, CreatedAt: e.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	// They wait so a burst can be one message; this test does not.
	if _, err := db.DB().ExecContext(ctx, `UPDATE notification_outboxes SET next_at = now() - interval '1 second'
		WHERE project_id = $1`, p.ProjectSeq); err != nil {
		t.Fatal(err)
	}
	recorder := &mail.Recorder{}
	worker := &alerts.Worker{DB: db, Notify: notify.NewSender(recorder, true), Mail: recorder, Dashboard: "https://app.example"}
	for {
		n, err := worker.Pass(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 {
		t.Fatalf("the channel heard %d messages; want 1", len(posts))
	}
	data, _ := posts[0]["data"].(map[string]any)
	if data["title"] != "2 new crashes · Test Game (production)" {
		t.Fatalf("the message: %v", data["title"])
	}
	lines, _ := data["lines"].([]any)
	if len(lines) != 2 || lines[0] != "Object reference not set (1000 events)" {
		t.Fatalf("the most frequent should lead: %v", lines)
	}
}

// A rule never sends more than its hourly cap: past it, one notice, then silence until the hour
// turns. Counted in messages, so a digest of many is one.
func TestARuleIsCappedAtItsMessagesAnHour(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	var titles []string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		data, _ := body["data"].(map[string]any)
		mu.Lock()
		titles = append(titles, data["title"].(string))
		mu.Unlock()
	}))
	defer server.Close()
	ch := f.channel(t, f.project, server.URL)
	project := f.project
	f.rule(t, &project, []string{"config.published"}, []uuid.UUID{ch.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetMaxPerHour(2)
	})
	sink := &alerts.Sink{Client: f.db.Client}
	for i := range 6 {
		if err := sink.Enqueue(ctx, webhooks.ClaimedEvent{EventUID: uuid.New(), ProjectID: f.project,
			Environment: "production", Type: "config.published", Body: []byte(`{"data":{"version":` + string(rune('1'+i)) + `}}`),
			CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	recorder := &mail.Recorder{}
	worker := &alerts.Worker{DB: f.db, Notify: notify.NewSender(recorder, true), Mail: recorder}
	for {
		n, err := worker.Pass(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 3 || titles[2] != "Notifications held back · rule" {
		t.Fatalf("the channel heard %q; want two notifications and one notice", titles)
	}
	if left := f.queued(t); len(left) != 0 {
		t.Fatalf("%d notifications left queued after the cap", len(left))
	}
}

// A rule's cooldown is per issue: one noisy issue cannot silence another.
func TestACooldownIsPerIssue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ch := f.channel(t, f.project, "https://hooks.example/a")
	project := f.project
	f.rule(t, &project, []string{alerts.IssueSpiking}, []uuid.UUID{ch.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetCooldownMinutes(60)
	})
	sink := &alerts.Sink{Client: f.db.Client}
	for _, fp := range []string{"111", "111", "222"} {
		if err := sink.Enqueue(ctx, webhooks.ClaimedEvent{EventUID: uuid.New(), ProjectID: f.project,
			Environment: "production", Type: alerts.IssueSpiking, Body: []byte(`{"data":{"fingerprint":"` + fp + `"}}`),
			CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if rows := f.queued(t); len(rows) != 2 {
		t.Fatalf("queued %d; want one for each issue", len(rows))
	}
}
