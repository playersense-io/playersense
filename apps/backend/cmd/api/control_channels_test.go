package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/signing"
)

// A channel set up, tested, changed and removed: its secrets never read back, its webhook
// signed, and every send in its log.
func TestAChannelIsSetUpTestedAndLogged(t *testing.T) {
	var received atomic.Int32
	var lastSig, lastID, lastTS string
	var lastBody []byte
	failing := atomic.Bool{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		lastBody, _ = io.ReadAll(r.Body)
		lastSig, lastID, lastTS = r.Header.Get(signing.HeaderSignature), r.Header.Get(signing.HeaderID), r.Header.Get(signing.HeaderTimestamp)
		if failing.Load() {
			http.Error(w, "gone", http.StatusGone)
		}
	}))
	defer receiver.Close()

	s := newStackWith(t, muzak.WithSingleton(&notify.Sender{HTTP: notify.NewClient(2*time.Second, true),
		Mail: &mail.Recorder{}, AllowPrivate: true}))
	owner := person(t)
	org := s.createOrg(t, owner)
	a := &authoring{stack: s, owner: owner, orgSlug: org, projSlug: s.createProject(t, owner, org)}
	client := a.client(t)
	base := a.base() + "/channels"

	// A draft is tested before it is saved; nothing is kept.
	draft := testclient.Decoded[schemas.ChannelTestOut](client.Post(base+"/test", testclient.JSON(map[string]any{
		"type": "slack", "config": map[string]any{"url": receiver.URL + "/services/T/B/secret"}})).AssertStatus(http.StatusOK))
	if !draft.OK || received.Load() != 1 {
		t.Fatalf("draft test %+v", draft)
	}

	made := testclient.Decoded[schemas.ChannelOut](client.Post(base, testclient.JSON(map[string]any{
		"type": "webhook", "name": "Ops webhook",
		"config": map[string]any{"url": receiver.URL + "/hooks/abcdefghijklmnop", "header_name": "X-Gateway", "header_value": "k"}})).
		AssertStatus(http.StatusCreated))
	if made.SigningSecret == "" || strings.Contains(made.Config.URL, "abcdefghijklmnop") || made.Status != "untested" {
		t.Fatalf("created %+v", made)
	}
	one := base + "/" + made.ID.String()
	if again := testclient.Decoded[schemas.ChannelOut](client.Get(one).AssertStatus(http.StatusOK)); again.SigningSecret != "" {
		t.Fatal("the signing secret was shown twice")
	}

	tested := testclient.Decoded[schemas.ChannelTestOut](client.Post(one + "/test").AssertStatus(http.StatusOK))
	if !tested.OK {
		t.Fatalf("test %+v", tested)
	}
	if err := signing.Verify(made.SigningSecret, lastID, lastTS, lastSig, lastBody, time.Now()); err != nil {
		t.Fatalf("the test was not signed with the channel's secret: %v", err)
	}
	if got := testclient.Decoded[schemas.ChannelOut](client.Get(one).AssertStatus(http.StatusOK)); got.Status != "working" {
		t.Fatalf("after a good test %+v", got)
	}

	// Renamed without its URL: the URL is kept, and so is the test.
	renamed := testclient.Decoded[schemas.ChannelOut](client.Put(one, testclient.JSON(map[string]any{
		"name": "Ops", "config": map[string]any{"header_name": "X-Gateway"}})).AssertStatus(http.StatusOK))
	if renamed.Name != "Ops" || renamed.Status != "working" || renamed.Config.URL == "" {
		t.Fatalf("renamed %+v", renamed)
	}
	failing.Store(true)
	if bad := testclient.Decoded[schemas.ChannelTestOut](client.Post(one + "/test").AssertStatus(http.StatusOK)); bad.OK ||
		!strings.Contains(bad.Detail, "410") {
		t.Fatalf("a failing destination %+v", bad)
	}

	client.Post(base, testclient.JSON(map[string]any{"type": "pagerduty", "name": "x", "config": map[string]any{"routing_key": "short"}})).
		AssertStatus(http.StatusUnprocessableEntity)

	log := testclient.Decoded[schemas.ChannelDeliveriesOut](client.Get(base + "/deliveries?channel=" + made.ID.String()).
		AssertStatus(http.StatusOK))
	if len(log.Deliveries) != 2 || log.Deliveries[0].OK || !log.Deliveries[1].OK || !log.Deliveries[0].Test {
		t.Fatalf("log %+v", log.Deliveries)
	}
	client.Delete(one).AssertStatus(http.StatusNoContent)
	after := testclient.Decoded[schemas.ChannelDeliveriesOut](client.Get(base + "/deliveries").AssertStatus(http.StatusOK))
	if len(after.Deliveries) != 2 || after.Deliveries[0].ChannelID != nil {
		t.Fatalf("after deleting, the log %+v", after.Deliveries)
	}
}
