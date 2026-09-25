package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/webhooks"
)

// How fast one worker delivers: events through the outbox, each POSTed to an endpoint and logged.
// Opt-in, since it is a measurement rather than a check: PLAYERSENSE_LOAD=20000 go test -run Load.
func TestLoadWebhookThroughput(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PLAYERSENSE_LOAD"))
	if n <= 0 {
		t.Skip("set PLAYERSENSE_LOAD to the number of events to send")
	}
	conn := chdb.New(testSettings(t).ClickHouseDSN)
	if err := conn.Start(t.Context()); err != nil {
		t.Skipf("no ClickHouse: %v", err)
	}
	defer conn.Stop(t.Context()) //nolint:errcheck
	s := newStack(t)
	person := person(t)
	org := s.createOrg(t, person)
	slug := s.createProject(t, person, org)
	p, _ := s.db.Client.Project.Query().Where(project.Slug(slug)).Only(t.Context())

	// Earlier runs' endpoints point at servers long closed: off, with what they were owed, so this
	// run measures itself.
	old, _ := s.db.Client.WebhookEndpoint.Query().Where(webhookendpoint.NameHasPrefix("load ")).IDs(t.Context())
	if len(old) > 0 {
		s.db.Client.WebhookRetry.Delete().Where(webhookretry.EndpointIDIn(old...)).ExecX(t.Context())
		s.db.Client.WebhookEndpoint.Update().Where(webhookendpoint.IDIn(old...)).SetEnabled(false).ExecX(t.Context())
	}

	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	for i := 0; i < 4; i++ {
		s.db.Client.WebhookEndpoint.Create().SetProjectID(p.ID).SetName("load " + strconv.Itoa(i)).SetURL(srv.URL).
			SetEvents([]string{"*"}).SetEnvironments([]string{"production"}).SetAPIVersion(webhooks.APIVersion).
			SetSecret("whsec_load").SetSecretCreatedAt(time.Now()).ExecX(t.Context())
	}

	for i := 0; i < n; i += 1000 {
		creates := make([]*ent.WebhookOutboxCreate, 0, 1000)
		for j := i; j < min(i+1000, n); j++ {
			id := uuid.New()
			body, _ := webhooks.Body(webhooks.Event{ProjectID: p.ID, ProjectUID: p.UID, Environment: "production",
				Type: "purchase.verified", Data: webhooks.Sample("purchase.verified", "production")}, id)
			creates = append(creates, s.db.Client.WebhookOutbox.Create().SetUID(id).SetProjectID(p.ID).
				SetEnvironment("production").SetType("purchase.verified").SetBody(body))
		}
		s.db.Client.WebhookOutbox.CreateBulk(creates...).ExecX(t.Context())
	}

	log := &webhooks.AttemptLog{CH: conn}
	d := &webhooks.Dispatcher{DB: s.db, Deliver: webhooks.NewDeliverer(true), Log: log, Batch: 1000, PerEndpoint: 64}
	start := time.Now()
	for {
		taken, err := d.Pass(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if taken == 0 {
			break
		}
	}
	if err := log.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("%d events to 4 endpoints: %d deliveries in %v, %.0f deliveries a second, %.1f million a day; %d tries, %d held",
		n, got.Load(), took.Round(time.Millisecond), float64(got.Load())/took.Seconds(),
		float64(got.Load())/took.Seconds()*86400/1e6, d.Tried.Load(), d.Held.Load())
}
