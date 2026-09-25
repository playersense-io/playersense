package alerts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/ent/notificationoutbox"
	"playersense.dev/backend/internal/alerts"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/webhooks"
)

func TestEveryWebhookEventCanBeHeardByAProjectRule(t *testing.T) {
	for _, e := range webhooks.Catalog {
		k, ok := alerts.Lookup(e.Type)
		if !ok || k.Scope != alerts.ScopeProject || k.Title == "" || k.Severity == "" {
			t.Errorf("%s: %+v, %v", e.Type, k, ok)
		}
	}
	if _, ok := alerts.Valid(alerts.ScopeProject, []string{alerts.TopUpFailed}); ok {
		t.Error("a project rule was allowed to hear the organization's money")
	}
	if _, ok := alerts.Valid(alerts.ScopeOrganization, []string{"purchase.verified"}); ok {
		t.Error("an organization rule was allowed to hear a project's purchases")
	}
	if _, ok := alerts.Valid(alerts.ScopeOrganization, []string{alerts.Every, alerts.AccountSuspended}); !ok {
		t.Error("an organization rule could not hear its own events")
	}
}

func TestAMessageLeadsWithWhatMattersAndLinksToWhereItIsDealtWith(t *testing.T) {
	kind, _ := alerts.Lookup(alerts.TopUpFailed)
	m := alerts.Render(kind, alerts.Event{Type: alerts.TopUpFailed, At: time.Now(),
		Data: map[string]any{"balance": "-$4.20", "reason": "Your card was declined.", "amount": "$50.00", "zeta": 3.0}},
		alerts.Where{Organization: "Moonrise"}, "https://app.example/")
	if m.Title != "Automatic top-up failed · Moonrise" {
		t.Errorf("title %q", m.Title)
	}
	want := []string{"Reason: Your card was declined.", "Amount: $50.00", "Balance: -$4.20", "Zeta: 3"}
	if strings.Join(m.Lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines %q", m.Lines)
	}
	if m.Link != "https://app.example/settings/billing" || m.Severity != "error" {
		t.Errorf("link %q severity %q", m.Link, m.Severity)
	}
}

type fixture struct {
	db      *pgdb.DB
	org     int
	project int
	other   int // a project of another organization
}

func setup(t *testing.T) fixture {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Postgres(t, settings)
	p := testsupport.SeedProject(t, db, "standard")
	o := testsupport.SeedProject(t, db, "standard")
	return fixture{db: db, org: p.OrgSeq, project: p.ProjectSeq, other: o.ProjectSeq}
}

func (f fixture) channel(t *testing.T, projectID int, url string) *ent.NotificationChannel {
	t.Helper()
	ch, err := f.db.Client.NotificationChannel.Create().SetProjectID(projectID).
		SetType(notificationchannel.TypeWebhook).SetName("hook-" + uuid.NewString()[:6]).
		SetSecret(map[string]string{"url": url, "signing_secret": "whsec_test"}).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func (f fixture) rule(t *testing.T, project *int, events []string, channels []uuid.UUID, tweak func(*ent.NotificationRuleCreate)) *ent.NotificationRule {
	t.Helper()
	c := f.db.Client.NotificationRule.Create().SetOrganizationID(f.org).SetNillableProjectID(project).
		SetName("rule").SetEvents(events).SetChannels(channels)
	if tweak != nil {
		tweak(c)
	}
	r, err := c.Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f fixture) queued(t *testing.T) []*ent.NotificationOutbox {
	t.Helper()
	rows, err := f.db.Client.NotificationOutbox.Query().
		Where(notificationoutbox.OrganizationID(f.org)).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAFailedTopUpReachesTheOwnersAndTheRulesThatHearIt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	mine := f.channel(t, f.project, "https://hooks.example/a")
	theirs := f.channel(t, f.other, "https://hooks.example/b")
	f.rule(t, nil, []string{alerts.TopUpFailed}, []uuid.UUID{mine.UID, theirs.UID}, nil)
	f.rule(t, nil, []string{alerts.AccountSuspended}, []uuid.UUID{mine.UID}, nil)
	f.rule(t, nil, []string{alerts.Every}, []uuid.UUID{mine.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetMutedUntil(time.Now().Add(time.Hour))
	})
	f.rule(t, nil, []string{alerts.Every}, []uuid.UUID{mine.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetEnabled(false)
	})

	if err := alerts.Publish(ctx, f.db.Client, alerts.Event{OrganizationID: f.org, Type: alerts.TopUpFailed,
		Data: map[string]any{"reason": "declined"}}); err != nil {
		t.Fatal(err)
	}
	rows := f.queued(t)
	var owners, toMine, toTheirs int
	for _, r := range rows {
		switch {
		case r.ChannelID == nil:
			owners++
		case *r.ChannelID == mine.ID:
			toMine++
		case *r.ChannelID == theirs.ID:
			toTheirs++
		}
	}
	if owners != 1 || toMine != 1 || toTheirs != 0 {
		t.Fatalf("owners %d, mine %d, another organization's %d; want 1, 1, 0", owners, toMine, toTheirs)
	}

	// The same failure again the same day: the rule hears it again, the owners are not mailed again.
	if err := alerts.Publish(ctx, f.db.Client, alerts.Event{OrganizationID: f.org, Type: alerts.TopUpFailed}); err != nil {
		t.Fatal(err)
	}
	owners = 0
	for _, r := range f.queued(t) {
		if r.ChannelID == nil {
			owners++
		}
	}
	if len(f.queued(t)) != 3 || owners != 1 {
		t.Fatalf("after a second failure: %d rows, %d for the owners; want 3 and 1", len(f.queued(t)), owners)
	}
}

func TestACooldownHoldsBackTheSameEventButNotAnother(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ch := f.channel(t, f.project, "https://hooks.example/a")
	f.rule(t, nil, []string{alerts.Every}, []uuid.UUID{ch.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetCooldownMinutes(60)
	})
	for _, kind := range []string{alerts.TopUpFailed, alerts.TopUpFailed, alerts.AccountSuspended} {
		if err := alerts.Publish(ctx, f.db.Client, alerts.Event{OrganizationID: f.org, Type: kind}); err != nil {
			t.Fatal(err)
		}
	}
	channelRows := 0
	for _, r := range f.queued(t) {
		if r.ChannelID != nil {
			channelRows++
		}
	}
	if channelRows != 2 {
		t.Fatalf("%d notifications to the channel; want 2 (one top-up failure, one suspension)", channelRows)
	}
}

func TestAProjectEventIsQueuedOnceHoweverOftenItIsHandedOver(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ch := f.channel(t, f.project, "https://hooks.example/a")
	project := f.project
	f.rule(t, &project, []string{"config.published"}, []uuid.UUID{ch.UID}, func(c *ent.NotificationRuleCreate) {
		c.SetEnvironments([]string{"production"})
	})
	sink := &alerts.Sink{Client: f.db.Client}
	event := webhooks.ClaimedEvent{EventUID: uuid.New(), ProjectID: f.project, Environment: "production",
		Type: "config.published", Body: []byte(`{"data":{"version":7}}`), CreatedAt: time.Now()}
	for range 2 {
		if err := sink.Enqueue(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	staging := event
	staging.EventUID, staging.Environment = uuid.New(), "staging"
	if err := sink.Enqueue(ctx, staging); err != nil {
		t.Fatal(err)
	}
	rows := f.queued(t)
	if len(rows) != 1 || rows[0].Data["version"] != float64(7) {
		t.Fatalf("queued %d rows (%v); want the production event once", len(rows), rows)
	}
}

func TestTheWorkerSendsRetriesAndMailsTheOwners(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	var mu sync.Mutex
	var bodies []map[string]any
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
	}))
	defer server.Close()

	ch := f.channel(t, f.project, server.URL)
	f.rule(t, nil, []string{alerts.AccountSuspended}, []uuid.UUID{ch.UID}, nil)
	testsupport.SeedMember(t, f.db, f.org, identity.Claims{UID: "owner-" + uuid.NewString(),
		Email: "owner-" + uuid.NewString()[:8] + "@studio.example", EmailVerified: true, Name: "Ada",
		Provider: "google.com"}, "owner")
	testsupport.SeedMember(t, f.db, f.org, identity.Claims{UID: "dev-" + uuid.NewString(),
		Email: "dev-" + uuid.NewString()[:8] + "@studio.example", EmailVerified: true, Name: "Bo",
		Provider: "google.com"}, "developer")

	if err := alerts.Publish(ctx, f.db.Client, alerts.Event{OrganizationID: f.org, Type: alerts.AccountSuspended,
		Data: map[string]any{"owed": "$120.00"}}); err != nil {
		t.Fatal(err)
	}
	recorder := &mail.Recorder{}
	worker := &alerts.Worker{DB: f.db, Notify: notify.NewSender(recorder, true), Mail: recorder,
		Dashboard: "https://app.example"}
	pass := func() {
		t.Helper()
		// Other tests' rows may be due too; this one's are what is looked at.
		for {
			n, err := worker.Pass(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
		}
	}
	pass()

	if sent := recorder.Sent(); len(recorder.Sent()) == 0 || !strings.Contains(sent[len(sent)-1].Subject, "Ingestion suspended") {
		t.Fatalf("the owners were not mailed: %v", sent)
	}
	for _, m := range recorder.Sent() {
		if strings.HasPrefix(m.To, "dev-") {
			t.Fatalf("a developer was mailed about the organization's money: %s", m.To)
		}
	}
	left := f.queued(t)
	if len(left) != 1 || left[0].Attempts != 1 || left[0].LastError == "" {
		t.Fatalf("after a failed send: %+v; want one row, tried once, with the error kept", left)
	}

	// The endpoint recovers, and the retry is made due.
	mu.Lock()
	fail = false
	mu.Unlock()
	if err := f.db.Client.NotificationOutbox.UpdateOneID(left[0].ID).SetNextAt(time.Now().Add(-time.Second)).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	pass()
	if rows := f.queued(t); len(rows) != 0 {
		t.Fatalf("%d rows left after a successful send", len(rows))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("the endpoint heard %d notifications; want 1", len(bodies))
	}
	data, _ := bodies[0]["data"].(map[string]any)
	if !strings.HasPrefix(data["title"].(string), "Ingestion suspended") || data["source"] != "Rule: rule" {
		t.Fatalf("sent %v", data)
	}
}

func TestASuspensionIsAnnouncedAndAPaidAccountIsWelcomedBack(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	account := testsupport.SeedBillingAccount(t, f.db, f.org)
	moves := []struct {
		from, to billingaccount.State
		want     string
	}{
		{billingaccount.StateActive, billingaccount.StateGracePeriod, alerts.PaymentOverdue},
		{billingaccount.StateGracePeriod, billingaccount.StateSuspended, alerts.AccountSuspended},
		{billingaccount.StateSuspended, billingaccount.StateActive, alerts.AccountRestored},
		{billingaccount.StateGracePeriod, billingaccount.StateActive, ""},
	}
	for _, m := range moves {
		before := len(f.queued(t))
		if err := alerts.AccountMoved(ctx, f.db.Client, account, m.from, m.to, -4_200_000, 4_200_000); err != nil {
			t.Fatal(err)
		}
		rows := f.queued(t)
		switch {
		case m.want == "" && len(rows) != before:
			t.Errorf("%s -> %s announced something", m.from, m.to)
		case m.want != "" && (len(rows) != before+1 || rows[len(rows)-1].Type != m.want):
			t.Errorf("%s -> %s: want %s for the owners", m.from, m.to, m.want)
		}
	}
}

func TestActivityCountsWhatWasSentAndSetsTestsApart(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	slack := f.channel(t, f.project, "https://hooks.example/a")
	mail := f.channel(t, f.project, "https://hooks.example/b")
	project := f.project
	rule := f.rule(t, &project, []string{"config.published"}, []uuid.UUID{slack.UID}, nil)

	send := func(ch *ent.NotificationChannel, at time.Time, ok, test bool, source string, ruleID *int) {
		t.Helper()
		ms := time.Duration(80+len(source)) * time.Millisecond
		if err := notify.RollUp(ctx, f.db.Client, ch, notify.Result{OK: ok, Duration: ms}, test, source, ruleID, at); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	send(slack, now.Add(-2*time.Hour), true, false, "Rule: Publishing", &rule.ID)
	send(slack, now.Add(-day-time.Hour), true, false, "Rule: Publishing to ops", &rule.ID) // renamed since
	send(slack, now.Add(-2*day), false, false, "Rule: Publishing to ops", &rule.ID)
	send(mail, now.Add(-3*day), true, false, "Report: Daily revenue", nil)
	send(mail, now.Add(-time.Hour), true, true, "Test by ada@studio.example", nil)
	send(mail, now.Add(-9*day), true, false, "Report: Daily revenue", nil) // the week before

	a, err := alerts.ProjectActivity(ctx, f.db.Client, f.project, alerts.Ranges["7d"], now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Current.Sent != 4 || a.Current.Delivered != 3 || a.Current.Failed != 1 || a.Current.Tests != 1 {
		t.Fatalf("current %+v; want 4 sent, 3 delivered, 1 failed, 1 test", a.Current)
	}
	if a.Previous.Sent != 1 {
		t.Fatalf("previous %+v; want the one send of the week before", a.Previous)
	}
	var delivered, failed, tests int
	for _, b := range a.Buckets {
		delivered, failed, tests = delivered+b.Delivered, failed+b.Failed, tests+b.Tests
	}
	if len(a.Buckets) != 7 || delivered != 3 || failed != 1 || tests != 1 {
		t.Fatalf("%d buckets holding %d, %d, %d", len(a.Buckets), delivered, failed, tests)
	}
	if len(a.Channels) != 2 || a.Channels[0].UID != slack.UID || a.Channels[0].Sent != 3 || a.Channels[0].Failed != 1 {
		t.Fatalf("channels %+v", a.Channels)
	}
	sum := 0
	for _, n := range a.Channels[0].Spark {
		sum += n
	}
	if sum != 3 || !a.Channels[0].LastOK {
		t.Fatalf("slack's spark sums to %d, last ok %v", sum, a.Channels[0].LastOK)
	}
	if len(a.Sources) != 2 || a.Sources[0].RuleID == nil || a.Sources[0].Sent != 3 || a.Sources[0].Source != "Rule: rule" {
		t.Fatalf("sources %+v; want the rule once, under its current name, then the report", a.Sources)
	}
	if a.Current.P50Ms < 100 || a.Current.P50Ms > 250 {
		t.Fatalf("median answer %d ms; every send took 80 to 120 ms", a.Current.P50Ms)
	}

	stats, err := alerts.RuleStats(ctx, f.db.Client, []int{rule.ID}, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if st := stats[rule.ID]; st.Sent != 3 || st.Failed != 1 || st.Spark[6] != 1 || st.LastAt == nil {
		t.Fatalf("rule stats %+v", st)
	}
}
