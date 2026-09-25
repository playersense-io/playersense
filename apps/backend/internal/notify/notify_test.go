package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/signing"
)

func TestPrivateAddressesAreRefused(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "192.168.0.1", "::1", "fd00::1", "::ffff:10.0.0.1"} {
		if Public(netip.MustParseAddr(a)) {
			t.Errorf("%s counted as public", a)
		}
	}
	if !Public(netip.MustParseAddr("8.8.8.8")) {
		t.Error("8.8.8.8 counted as private")
	}
	// A real request to this machine is refused by the dialer itself.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	s := &Sender{HTTP: NewClient(2*time.Second, false)}
	r := s.Send(context.Background(), Channel{Type: Webhook, URL: server.URL}, Message{Title: "x"})
	if r.OK || !strings.Contains(r.Detail, ErrPrivateAddress.Error()) {
		t.Fatalf("posting to loopback: %+v", r)
	}
}

func TestEachTypeChecksItsHost(t *testing.T) {
	s := &Sender{}
	cases := []struct {
		ch Channel
		ok bool
	}{
		{Channel{Type: Slack, URL: "https://hooks.slack.com/services/T/B/x"}, true},
		{Channel{Type: Slack, URL: "https://evil.example/services"}, false},
		{Channel{Type: Slack, URL: "http://hooks.slack.com/services/T/B/x"}, false},
		{Channel{Type: Discord, URL: "https://discord.com/api/webhooks/1/abc"}, true},
		{Channel{Type: Discord, URL: "https://discord.com/other"}, false},
		{Channel{Type: Teams, URL: "https://prod-1.westus.logic.azure.com/workflows/x"}, true},
		{Channel{Type: Webhook, URL: "https://api.studio.example/hooks", HeaderName: "X-Token"}, true},
		{Channel{Type: Webhook, URL: "https://api.studio.example/hooks", HeaderName: "Host"}, false},
		{Channel{Type: PagerDuty, RoutingKey: strings.Repeat("a", 32), Severity: "critical"}, true},
		{Channel{Type: PagerDuty, RoutingKey: "short"}, false},
		{Channel{Type: Email, Addresses: []string{"ops@studio.example"}}, true},
		{Channel{Type: Email, Addresses: []string{"not an address"}}, false},
	}
	for _, c := range cases {
		if err := s.Validate(c.ch); (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.ch.Type, c.ch.URL, err)
		}
		if err := s.Validate(c.ch); err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%v is not ErrInvalid", err)
		}
	}
}

func TestEachTypeSendsItsOwnShape(t *testing.T) {
	var got map[string]any
	var headers http.Header
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		headers = r.Header
		got = nil
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	recorder := &mail.Recorder{}
	s := &Sender{HTTP: NewClient(2*time.Second, true), Mail: recorder, AllowPrivate: true, PagerDutyURL: server.URL}
	m := Message{Title: "Revenue fell", Summary: "Down 32%", Lines: []string{"TR  $120"}, Source: "Report: Revenue"}

	if r := s.Send(context.Background(), Channel{Type: Slack, URL: server.URL}, m); !r.OK || !strings.Contains(got["text"].(string), "*Revenue fell*") {
		t.Fatalf("slack %+v %v", r, got)
	}
	if r := s.Send(context.Background(), Channel{Type: Discord, URL: server.URL}, m); !r.OK || !strings.Contains(got["content"].(string), "Down 32%") {
		t.Fatalf("discord %+v %v", r, got)
	}
	if r := s.Send(context.Background(), Channel{Type: Teams, URL: server.URL}, m); !r.OK || got["type"] != "message" {
		t.Fatalf("teams %+v %v", r, got)
	}
	if r := s.Send(context.Background(), Channel{Type: PagerDuty, RoutingKey: strings.Repeat("k", 32)}, m); !r.OK ||
		got["event_action"] != "trigger" || got["payload"].(map[string]any)["severity"] != "error" {
		t.Fatalf("pagerduty %+v %v", r, got)
	}
	secret, _ := signing.NewSecret()
	r := s.Send(context.Background(), Channel{Type: Webhook, URL: server.URL, SigningSecret: secret, HeaderName: "X-Gateway", HeaderValue: "k"}, m)
	if !r.OK || headers.Get("X-Gateway") != "k" {
		t.Fatalf("webhook %+v", r)
	}
	if err := signing.Verify(secret, headers.Get(signing.HeaderTimestamp),
		headers.Get(signing.HeaderSignature), raw, time.Now()); err != nil {
		t.Fatalf("the webhook's signature: %v", err)
	}
	if r := s.Send(context.Background(), Channel{Type: Email, Addresses: []string{"a@studio.example", "b@studio.example"}}, m); !r.OK ||
		len(recorder.Sent()) != 2 {
		t.Fatalf("email %+v", r)
	}
}
