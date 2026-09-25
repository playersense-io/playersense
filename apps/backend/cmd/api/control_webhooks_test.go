package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/signing"
	"playersense.dev/backend/internal/staff"
	"playersense.dev/backend/internal/webhooks"
)

// receiver is a studio's endpoint: it keeps what it was sent, and answers what it is told to.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	got    []http.Request
	bodies [][]byte
	status atomic.Int32
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{}
	r.status.Store(http.StatusOK)
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, *req)
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()
		w.WriteHeader(int(r.status.Load()))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *receiver) last() (http.Request, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[len(r.got)-1], r.bodies[len(r.bodies)-1]
}

func TestWebhooksAreDeliveredRetriedAndTurnedOff(t *testing.T) {
	conn := chdb.New(testSettings(t).ClickHouseDSN)
	if err := conn.Start(t.Context()); err != nil {
		t.Skipf("no ClickHouse: %v", err)
	}
	defer conn.Stop(t.Context()) //nolint:errcheck
	sender := &webhooks.Sender{CH: conn, Deliver: webhooks.NewDeliverer(true), AllowPrivate: true}
	s := newStackWith(t, muzak.WithSingleton(sender), muzak.WithSingleton(conn))
	sender.DB = s.db

	person := person(t)
	org := s.createOrg(t, person)
	proj := s.createProject(t, person, org)
	owner := s.as(t, person)
	base := "/v1/organizations/" + org + "/projects/" + proj
	hook := newReceiver(t)

	mailer := &mail.Recorder{}
	log := &webhooks.AttemptLog{CH: conn}
	dispatcher := &webhooks.Dispatcher{DB: s.db, Deliver: webhooks.NewDeliverer(true), Log: log, Mail: mailer,
		Dashboard: "https://app.test", EndpointTTL: time.Millisecond}
	// dispatch runs the worker until what is due is done, and writes the log.
	dispatch := func() {
		t.Helper()
		for i := 0; i < 20; i++ {
			n, err := dispatcher.Pass(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		if err := log.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	mintKey := func(name string) {
		owner.Post(base+"/keys", testclient.JSON(map[string]string{"kind": "server", "environment": "production",
			"name": name})).AssertStatus(http.StatusCreated)
	}

	// ---- an endpoint ----
	owner.Post(base+"/webhooks", testclient.JSON(map[string]any{"name": "Bad", "url": hook.URL,
		"events": []string{"nope.never"}, "environments": []string{"production"}})).AssertStatus(http.StatusUnprocessableEntity)
	owner.Post(base+"/webhooks", testclient.JSON(map[string]any{"name": "Bad", "url": hook.URL,
		"events": []string{"*"}, "environments": []string{"production"},
		"headers": []map[string]any{{"name": "X-PlayerSense-Signature", "value": "x"}}})).AssertStatus(http.StatusUnprocessableEntity)
	made := testclient.Decoded[schemas.EndpointOut](owner.Post(base+"/webhooks", testclient.JSON(map[string]any{
		"name": "Backend", "url": hook.URL, "events": []string{"api_key.created", "purchase.verified"},
		"environments": []string{"production"},
		"headers":      []map[string]any{{"name": "X-Gateway-Token", "value": "hush", "secret": true}, {"name": "X-Team", "value": "econ"}},
	})).AssertStatus(http.StatusCreated))
	if !strings.HasPrefix(made.SigningSecret, "whsec_") || made.Health != "healthy" || made.Headers[0].Value != "" {
		t.Fatalf("the endpoint as made: %+v", made)
	}
	if body := owner.Get(base + "/webhooks").String(); strings.Contains(body, made.SigningSecret) || strings.Contains(body, "hush") {
		t.Fatal("the list disclosed a secret")
	}

	// ---- an event, delivered at its first try ----
	mintKey("first")
	dispatch()
	if hook.count() != 1 {
		t.Fatalf("%d deliveries, want 1", hook.count())
	}
	req, body := hook.last()
	if err := signing.Verify(made.SigningSecret, req.Header.Get(signing.HeaderTimestamp), req.Header.Get(signing.HeaderSignature),
		body, time.Now()); err != nil {
		t.Fatalf("the delivery is not signed with the endpoint's secret: %v", err)
	}
	if req.Header.Get("X-Gateway-Token") != "hush" || req.Header.Get("X-Team") != "econ" ||
		!strings.Contains(string(body), `"type":"api_key.created"`) || !strings.Contains(string(body), `"name":"first"`) ||
		!strings.Contains(string(body), req.Header.Get(signing.HeaderID)) {
		t.Fatalf("the delivery: %v %s", req.Header, body)
	}
	one := base + "/webhooks/" + made.ID.String()
	list := testclient.Decoded[schemas.WebhookDeliveriesOut](owner.Get(one + "/deliveries").AssertStatus(http.StatusOK))
	if len(list.Deliveries) != 1 || list.Deliveries[0].Status != "succeeded" || list.Deliveries[0].Attempts != 1 {
		t.Fatalf("the log: %+v", list)
	}

	// ---- an endpoint down: retried on the schedule ----
	hook.status.Store(http.StatusServiceUnavailable)
	mintKey("second")
	dispatch()
	e, _ := s.db.Client.WebhookEndpoint.Query().Where(webhookendpoint.UID(made.ID)).Only(t.Context())
	retry, err := s.db.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(e.ID)).Only(t.Context())
	if err != nil || retry.Attempts != 1 || retry.NextAt.Before(time.Now().Add(50*time.Second)) {
		t.Fatalf("the retry owed: %+v %v", retry, err)
	}
	if e.FailingSince == nil {
		t.Fatal("a failing endpoint has no failing streak")
	}
	failing := testclient.Decoded[schemas.WebhookDeliveriesOut](owner.Get(one + "/deliveries?status=retrying").AssertStatus(http.StatusOK))
	if len(failing.Deliveries) != 1 || failing.Deliveries[0].LastCode == nil || *failing.Deliveries[0].LastCode != 503 {
		t.Fatalf("the retrying delivery: %+v", failing)
	}

	// ---- sent again by hand, once it is back ----
	hook.status.Store(http.StatusOK)
	resent := testclient.Decoded[schemas.WebhookSentOut](owner.Post(one+"/deliveries/"+failing.Deliveries[0].ID.String()+"/resend",
		testclient.JSON(map[string]any{})).AssertStatus(http.StatusOK))
	if resent.Try.Outcome != "succeeded" || resent.Try.Number != 2 || !resent.Try.Manual {
		t.Fatalf("the resend: %+v", resent)
	}
	if n, _ := s.db.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(e.ID)).Count(t.Context()); n != 0 {
		t.Fatalf("%d retries still owed after the resend landed", n)
	}
	detail := testclient.Decoded[schemas.WebhookDeliveryDetailOut](owner.Get(one + "/deliveries/" + failing.Deliveries[0].ID.String()).
		AssertStatus(http.StatusOK))
	if len(detail.Tries) != 2 || detail.Status != "succeeded" || len(detail.Payload) == 0 {
		t.Fatalf("the delivery's tries: %+v", detail)
	}

	// ---- a test send ----
	tested := testclient.Decoded[schemas.WebhookSentOut](owner.Post(one+"/test", testclient.JSON(map[string]any{
		"type": "purchase.verified"})).AssertStatus(http.StatusOK))
	if tested.Try.Outcome != "succeeded" {
		t.Fatalf("the test: %+v", tested)
	}
	if _, body := hook.last(); !strings.Contains(string(body), `"purchase"`) {
		t.Fatalf("the test's body: %s", body)
	}

	// ---- a rolled secret signs beside the old one for a while ----
	rolled := testclient.Decoded[schemas.EndpointOut](owner.Post(one+"/secret/roll", testclient.JSON(map[string]any{})).
		AssertStatus(http.StatusOK))
	if rolled.SigningSecret == made.SigningSecret || rolled.Secret.PreviousExpiresAt == nil {
		t.Fatalf("the roll: %+v", rolled)
	}
	mintKey("third")
	dispatch()
	req, body = hook.last()
	for _, secret := range []string{made.SigningSecret, rolled.SigningSecret} {
		if err := signing.Verify(secret, req.Header.Get(signing.HeaderTimestamp), req.Header.Get(signing.HeaderSignature),
			body, time.Now()); err != nil {
			t.Fatalf("during the roll a secret does not verify: %v", err)
		}
	}
	shown := testclient.Decoded[schemas.EndpointOut](owner.Get(one + "/secret").AssertStatus(http.StatusOK))
	if shown.SigningSecret != rolled.SigningSecret {
		t.Fatal("the secret shown is not the current one")
	}

	// ---- health ----
	health := testclient.Decoded[schemas.WebhookHealthOut](owner.Get(one + "/health").AssertStatus(http.StatusOK))
	if health.Current.Deliveries < 3 || health.Current.TrySuccess >= 1 || len(health.Hours) != 24 {
		t.Fatalf("health: %+v", health.Current)
	}
	read := testclient.Decoded[schemas.EndpointOut](owner.Get(one).AssertStatus(http.StatusOK))
	if len(read.History) < 2 || read.LastDay.Deliveries < 3 {
		t.Fatalf("one endpoint: history %d, day %+v", len(read.History), read.LastDay)
	}

	// ---- three days of nothing but failures: turned off, owners told ----
	hook.status.Store(http.StatusInternalServerError)
	long := time.Now().Add(-73 * time.Hour)
	if err := s.db.Client.WebhookEndpoint.UpdateOneID(e.ID).SetFailingSince(long).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	dispatcher = &webhooks.Dispatcher{DB: s.db, Deliver: webhooks.NewDeliverer(true), Log: log, Mail: mailer,
		Dashboard: "https://app.test", EndpointTTL: time.Millisecond}
	mintKey("fourth")
	dispatch()
	off, _ := s.db.Client.WebhookEndpoint.Query().Where(webhookendpoint.UID(made.ID)).Only(t.Context())
	if off.Enabled || off.DisabledReason != "failing" {
		t.Fatalf("after three days of failures: enabled %v, reason %q", off.Enabled, off.DisabledReason)
	}
	if len(mailer.To(person.Email)) != 1 {
		t.Fatalf("the owner was told %d times", len(mailer.To(person.Email)))
	}
	owner.Post(one+"/resend-failed", testclient.JSON(map[string]any{})).AssertStatus(http.StatusConflict)

	// Back on: three days again, and what it missed sent once more.
	hook.status.Store(http.StatusOK)
	back := testclient.Decoded[schemas.EndpointOut](owner.Put(one, testclient.JSON(map[string]any{
		"name": "Backend", "url": hook.URL, "events": []string{"api_key.created", "purchase.verified"},
		"environments": []string{"production"}, "enabled": true,
		"headers": []map[string]any{{"name": "X-Gateway-Token", "secret": true}, {"name": "X-Team", "value": "econ"}},
	})).AssertStatus(http.StatusOK))
	if back.Health != "healthy" || back.FailingSince != nil {
		t.Fatalf("turned back on: %+v", back)
	}
	queued := testclient.Decoded[schemas.ResendFailedOut](owner.Post(one+"/resend-failed", testclient.JSON(map[string]any{})).
		AssertStatus(http.StatusOK))
	if queued.Queued != 1 {
		t.Fatalf("%d queued, want the one that gave up", queued.Queued)
	}
	before := hook.count()
	dispatch()
	if hook.count() != before+1 {
		t.Fatalf("the missed delivery was sent %d times", hook.count()-before)
	}
	if req, _ := hook.last(); req.Header.Get("X-Gateway-Token") != "hush" {
		t.Fatal("a secret header sent without a value lost the one saved")
	}

	// ---- staff ----
	desk := staffOnly(s.staffClient(t, staff.Ops))
	overview := testclient.Decoded[schemas.OperatorWebhooksOut](desk.Get("/v1/operator/webhooks").AssertStatus(http.StatusOK))
	if overview.LastDay.Deliveries == 0 {
		t.Fatalf("the platform's day: %+v", overview.LastDay)
	}
	p, _ := s.db.Client.Project.Query().Where(project.Slug(proj)).Only(t.Context())
	desk.Get("/v1/operator/projects/" + p.UID.String() + "/webhooks/" + made.ID.String() + "/deliveries").AssertStatus(http.StatusOK)

	owner.Delete(one).AssertStatus(http.StatusNoContent)
	if n, _ := s.db.Client.WebhookEndpoint.Query().Where(webhookendpoint.UID(made.ID)).Count(t.Context()); n != 0 {
		t.Fatal("the endpoint outlived its delete")
	}
}
