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

	"playersense.dev/backend/internal/alerts"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/schemas"
)

// Notification rules through the API: a project's and the organization's, what each may hear and
// reach, a test that arrives, and who may change them.
func TestNotificationRulesRouteEventsToChannels(t *testing.T) {
	var received atomic.Int32
	var lastBody atomic.Value
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
	}))
	defer receiver.Close()

	s := newStackWith(t, muzak.WithSingleton(&notify.Sender{HTTP: notify.NewClient(2*time.Second, true),
		Mail: &mail.Recorder{}, AllowPrivate: true}))
	owner := person(t)
	org := s.createOrg(t, owner)
	a := &authoring{stack: s, owner: owner, orgSlug: org, projSlug: s.createProject(t, owner, org)}
	client := a.client(t)

	channel := testclient.Decoded[schemas.ChannelOut](client.Post(a.base()+"/channels", testclient.JSON(map[string]any{
		"type": "webhook", "name": "Ops", "config": map[string]any{"url": receiver.URL + "/hooks/abcdefghijk"}})).
		AssertStatus(http.StatusCreated))

	kinds := testclient.Decoded[schemas.NotificationKindsOut](client.Get("/v1/organizations/" + org + "/notification-events").
		AssertStatus(http.StatusOK))
	owned := 0
	for _, k := range kinds.Kinds {
		if k.Owners {
			owned++
		}
	}
	if len(kinds.Kinds) < 20 || owned != 4 {
		t.Fatalf("%d kinds, %d that always email the owners", len(kinds.Kinds), owned)
	}

	// A project's rule hears the project's events, from the environments it names.
	rules := a.base() + "/notification-rules"
	rule := testclient.Decoded[schemas.NotificationRuleOut](client.Post(rules, testclient.JSON(map[string]any{
		"name": "Publishing", "events": []string{"config.published", "config.rolled_back"},
		"environments": []string{"production"}, "channels": []string{channel.ID.String()}, "cooldown_minutes": 10})).
		AssertStatus(http.StatusCreated))
	if rule.Scope != "project" || !rule.Enabled || rule.CooldownMinutes != 10 {
		t.Fatalf("made %+v", rule)
	}
	client.Post(rules, testclient.JSON(map[string]any{"name": "Money", "events": []string{alerts.TopUpFailed},
		"channels": []string{channel.ID.String()}})).AssertStatus(http.StatusUnprocessableEntity)
	client.Post(rules, testclient.JSON(map[string]any{"name": "Nowhere", "events": []string{"config.published"},
		"environments": []string{"moon"}, "channels": []string{channel.ID.String()}})).
		AssertStatus(http.StatusUnprocessableEntity)

	test := testclient.Decoded[schemas.NotificationRuleTestOut](client.Post(rules + "/" + rule.ID.String() + "/test").
		AssertStatus(http.StatusOK))
	if len(test.Channels) != 1 || !test.Channels[0].OK || received.Load() != 1 ||
		!strings.Contains(lastBody.Load().(string), "Config published") {
		t.Fatalf("test %+v, received %d", test, received.Load())
	}

	muted := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	changed := testclient.Decoded[schemas.NotificationRuleOut](client.Put(rules+"/"+rule.ID.String(), testclient.JSON(map[string]any{
		"name": "Publishing", "enabled": false, "events": []string{"*"},
		"channels": []string{channel.ID.String()}, "muted_until": muted})).AssertStatus(http.StatusOK))
	if changed.Enabled || changed.MutedUntil == nil || len(changed.Environments) != 0 {
		t.Fatalf("changed %+v", changed)
	}

	// The organization's rules hear its account, may point at any of its projects' channels, and
	// are the billing people's to change.
	orgRules := "/v1/organizations/" + org + "/notification-rules"
	orgChannels := testclient.Decoded[schemas.OrgChannelListOut](client.Get("/v1/organizations/" + org + "/notification-channels").
		AssertStatus(http.StatusOK))
	if len(orgChannels.Channels) != 1 || orgChannels.Channels[0].Project == "" {
		t.Fatalf("org channels %+v", orgChannels)
	}
	money := testclient.Decoded[schemas.NotificationRuleOut](client.Post(orgRules, testclient.JSON(map[string]any{
		"name": "Money", "events": []string{alerts.TopUpFailed, alerts.AccountSuspended},
		"channels": []string{channel.ID.String()}})).AssertStatus(http.StatusCreated))
	if money.Scope != "organization" {
		t.Fatalf("made %+v", money)
	}
	client.Post(orgRules, testclient.JSON(map[string]any{"name": "Purchases", "events": []string{"purchase.verified"},
		"channels": []string{channel.ID.String()}})).AssertStatus(http.StatusUnprocessableEntity)

	developer := person(t)
	s.join(t, org, developer, "developer", true)
	dev := s.as(t, developer)
	dev.Get(orgRules).AssertStatus(http.StatusForbidden)
	dev.Delete(orgRules + "/" + money.ID.String()).AssertStatus(http.StatusForbidden)

	billing := person(t)
	s.join(t, org, billing, "billing", true)
	s.as(t, billing).Delete(orgRules + "/" + money.ID.String()).AssertStatus(http.StatusNoContent)
	if left := testclient.Decoded[schemas.NotificationRuleListOut](client.Get(orgRules).AssertStatus(http.StatusOK)); len(left.Rules) != 0 {
		t.Fatalf("left %+v", left)
	}
	client.Delete(rules + "/" + rule.ID.String()).AssertStatus(http.StatusNoContent)
}
