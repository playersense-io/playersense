package notify_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"playersense.dev/backend/ent/channeldelivery"
	"playersense.dev/backend/ent/channeldeliveryhour"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/testsupport"
)

// A send is one row of the log and one count in its hour, together: the Activity page reads the
// rollup, the log page reads the log, and the two must agree.
func TestADeliveryIsLoggedAndRolledUpTogether(t *testing.T) {
	ctx := context.Background()
	db := testsupport.Postgres(t, testsupport.Settings(t))
	p := testsupport.SeedProject(t, db, "standard")
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()
	ch, err := db.Client.NotificationChannel.Create().SetProjectID(p.ProjectSeq).SetType(notificationchannel.TypeWebhook).
		SetName("Ops").SetSecret(map[string]string{"url": server.URL, "signing_secret": "whsec_test"}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sender := notify.NewSender(&mail.Recorder{}, true)
	rule := 42
	sender.Deliver(ctx, db.Client, ch, notify.Message{Title: "one"}, "Rule: Ops", &rule)
	failing = true
	sender.Deliver(ctx, db.Client, ch, notify.Message{Title: "two"}, "Rule: Ops renamed", &rule)
	sender.Deliver(ctx, db.Client, ch, notify.Message{Title: "three", Test: true}, "Test by ada", nil)

	logged, err := db.Client.ChannelDelivery.Query().Where(channeldelivery.ChannelID(ch.ID)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hours, err := db.Client.ChannelDeliveryHour.Query().Where(channeldeliveryhour.ChannelID(ch.ID)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var delivered, failed, tests, answered int
	for _, h := range hours {
		delivered, failed, tests = delivered+h.Delivered, failed+h.Failed, tests+h.Tests
		answered += h.Lat100 + h.Lat250 + h.Lat500 + h.Lat1000 + h.Lat2500 + h.Lat5000 + h.Lat10000 + h.LatOver
	}
	if logged != 3 || delivered != 1 || failed != 1 || tests != 1 || answered != 3 {
		t.Fatalf("logged %d; rolled up %d delivered, %d failed, %d tests, %d answer times; want 3, 1, 1, 1, 3",
			logged, delivered, failed, tests, answered)
	}
	// The rule's two sends are one row of its hour, whatever it was called at each.
	ruleRows := 0
	for _, h := range hours {
		if h.RuleID == rule {
			ruleRows++
			if h.Source != "" {
				t.Fatalf("a rule's send kept its source %q", h.Source)
			}
		}
	}
	if ruleRows != 1 {
		t.Fatalf("the rule's sends are %d rows of the rollup; want 1", ruleRows)
	}
}
