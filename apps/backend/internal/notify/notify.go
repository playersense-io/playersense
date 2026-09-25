// Package notify sends a message to a notification channel: an email list, a Slack, Discord or
// Microsoft Teams incoming webhook, a PagerDuty service, or a URL of the customer's, signed.
//
// Incoming webhooks rather than apps, on purpose: a URL the customer makes in their own workspace
// is a destination PlayerSense holds no OAuth grant for and cannot use for anything else. The
// URL is a secret all the same, so it is stored apart from the channel's visible settings and
// shown back masked.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/signing"
)

// Type is a kind of destination.
type Type string

// The types.
const (
	Email     Type = "email"
	Slack     Type = "slack"
	Discord   Type = "discord"
	Teams     Type = "teams"
	PagerDuty Type = "pagerduty"
	Webhook   Type = "webhook"
)

// Types is every type, in the order a picker lists them.
func Types() []string {
	return []string{string(Email), string(Slack), string(Discord), string(Teams), string(PagerDuty), string(Webhook)}
}

// Channel is one destination with its secrets.
type Channel struct {
	Type      Type
	Name      string
	Addresses []string // email
	URL       string   // slack, discord, teams, webhook
	// RoutingKey is a PagerDuty integration key; Severity what the incident is raised as.
	RoutingKey string
	Severity   string
	// A webhook's optional header, for a gateway in front of it, and the secret it is signed with.
	HeaderName    string
	HeaderValue   string
	SigningSecret string
}

// Message is what is sent.
type Message struct {
	Title   string
	Summary string
	// Lines are a few rows, already rendered, for the channels that show a body.
	Lines []string
	// Severity overrides the channel's for PagerDuty: critical, error, warning or info.
	Severity string
	// Source names what sent it: "Report: Daily revenue".
	Source string
	Link   string
	// Payload is the webhook body's data; the other channels ignore it.
	Payload any
	Test    bool
}

// Result is what one send did.
type Result struct {
	OK       bool
	Detail   string
	Duration time.Duration
}

// Sender sends messages. One per process.
type Sender struct {
	HTTP *http.Client
	Mail mail.Sender
	// AllowPrivate lets destinations resolve to this machine or a private network, for
	// development and tests; production leaves it off.
	AllowPrivate bool
	// PagerDutyURL is the Events API; empty means PagerDuty's own.
	PagerDutyURL string
}

// NewSender returns a sender over the safe client.
func NewSender(m mail.Sender, allowPrivate bool) *Sender {
	return &Sender{HTTP: NewClient(10*time.Second, allowPrivate), Mail: m, AllowPrivate: allowPrivate}
}

// ErrInvalid is a channel that cannot be sent to as configured.
var ErrInvalid = errors.New("invalid channel")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks a channel before it is saved: the fields its type needs, and a URL on the host
// its type posts to. With AllowPrivate the host is not checked, so tests can post to themselves.
func (s *Sender) Validate(ch Channel) error {
	switch ch.Type {
	case Email:
		if len(ch.Addresses) == 0 || len(ch.Addresses) > 20 {
			return invalid("an email channel has 1 to 20 addresses")
		}
		for _, a := range ch.Addresses {
			if !strings.Contains(a, "@") || strings.ContainsAny(a, " \r\n<>") || len(a) > 254 {
				return invalid("%q is not an email address", a)
			}
		}
		return nil
	case PagerDuty:
		if len(ch.RoutingKey) != 32 {
			return invalid("a PagerDuty integration key is 32 characters, from the service's Events API v2 integration")
		}
		switch ch.Severity {
		case "", "critical", "error", "warning", "info":
			return nil
		}
		return invalid("severity is critical, error, warning or info")
	case Slack, Discord, Teams, Webhook:
		u, err := url.Parse(ch.URL)
		if err != nil || u.Host == "" {
			return invalid("the URL is not a URL")
		}
		if s.AllowPrivate {
			return nil
		}
		if u.Scheme != "https" {
			return invalid("the URL must be https")
		}
		host := strings.ToLower(u.Hostname())
		switch ch.Type {
		case Slack:
			if host != "hooks.slack.com" {
				return invalid("a Slack incoming webhook is on hooks.slack.com")
			}
		case Discord:
			if (host != "discord.com" && host != "discordapp.com" && !strings.HasSuffix(host, ".discord.com")) ||
				!strings.HasPrefix(u.Path, "/api/webhooks/") {
				return invalid("a Discord webhook is https://discord.com/api/webhooks/…")
			}
		case Teams:
			if !strings.HasSuffix(host, ".webhook.office.com") && !strings.HasSuffix(host, ".logic.azure.com") &&
				!strings.HasSuffix(host, ".powerplatform.com") {
				return invalid("a Teams webhook is a Workflows URL on logic.azure.com or powerplatform.com")
			}
		}
		if ch.HeaderName != "" && !validHeader(ch.HeaderName) {
			return invalid("the header name is not a header name")
		}
		return nil
	}
	return invalid("%q is not a channel type", ch.Type)
}

func validHeader(name string) bool {
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	switch strings.ToLower(name) {
	case "host", "content-type", "content-length", "user-agent",
		strings.ToLower(signing.HeaderID), strings.ToLower(signing.HeaderTimestamp), strings.ToLower(signing.HeaderSignature):
		return false
	}
	return true
}

// Send delivers one message and says what happened, never an error: a failed send is an answer
// the delivery log keeps.
func (s *Sender) Send(ctx context.Context, ch Channel, m Message) Result {
	start := time.Now()
	detail, err := s.send(ctx, ch, m)
	r := Result{OK: err == nil, Detail: detail, Duration: time.Since(start)}
	if err != nil {
		r.Detail = err.Error()
	}
	if len(r.Detail) > 500 {
		r.Detail = r.Detail[:500]
	}
	return r
}

func (s *Sender) send(ctx context.Context, ch Channel, m Message) (string, error) {
	title := m.Title
	if m.Test {
		title = "[Test] " + title
	}
	text := title
	if m.Summary != "" {
		text += "\n" + m.Summary
	}
	if len(m.Lines) > 0 {
		text += "\n" + strings.Join(m.Lines, "\n")
	}
	if m.Link != "" {
		text += "\n" + m.Link
	}

	switch ch.Type {
	case Email:
		if s.Mail == nil {
			return "", errors.New("no mail sender is configured")
		}
		for _, to := range ch.Addresses {
			if err := s.Mail.Send(ctx, mail.Message{Kind: mail.Notification, To: to, Subject: title, Body: text}); err != nil {
				return "", fmt.Errorf("to %s: %w", to, err)
			}
		}
		if !mail.Delivers(s.Mail) {
			return fmt.Sprintf("composed for %d addresses; this deployment's mail sender delivers nothing", len(ch.Addresses)), nil
		}
		return fmt.Sprintf("sent to %d addresses", len(ch.Addresses)), nil

	case Slack:
		return s.post(ctx, ch.URL, map[string]any{"text": clip(slackText(title, m), 3000)}, nil)

	case Discord:
		body := map[string]any{"content": clip(text, 2000), "allowed_mentions": map[string]any{"parse": []string{}}}
		return s.post(ctx, ch.URL, body, nil)

	case Teams:
		blocks := []map[string]any{{"type": "TextBlock", "text": title, "weight": "Bolder", "size": "Medium", "wrap": true}}
		if m.Summary != "" {
			blocks = append(blocks, map[string]any{"type": "TextBlock", "text": m.Summary, "wrap": true})
		}
		for _, line := range m.Lines {
			blocks = append(blocks, map[string]any{"type": "TextBlock", "text": line, "wrap": true, "spacing": "None"})
		}
		card := map[string]any{"type": "message", "attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type": "AdaptiveCard", "version": "1.4", "body": blocks},
		}}}
		return s.post(ctx, ch.URL, card, nil)

	case PagerDuty:
		severity := m.Severity
		if severity == "" {
			severity = ch.Severity
		}
		if severity == "" {
			severity = "error"
		}
		endpoint := s.PagerDutyURL
		if endpoint == "" {
			endpoint = "https://events.pagerduty.com/v2/enqueue"
		}
		body := map[string]any{
			"routing_key": ch.RoutingKey, "event_action": "trigger",
			"payload": map[string]any{"summary": clip(title+": "+m.Summary, 1024), "source": "PlayerSense",
				"severity": severity, "custom_details": map[string]any{"source": m.Source, "lines": m.Lines}},
		}
		if m.Link != "" {
			body["links"] = []map[string]string{{"href": m.Link, "text": "Open in PlayerSense"}}
		}
		return s.post(ctx, endpoint, body, nil)

	case Webhook:
		id := "msg_" + uuid.NewString()
		body, err := json.Marshal(map[string]any{
			"id": id, "type": "notification", "test": m.Test, "created_at": time.Now().UTC(),
			"data": map[string]any{"title": m.Title, "summary": m.Summary, "source": m.Source, "link": m.Link,
				"lines": m.Lines, "payload": m.Payload},
		})
		if err != nil {
			return "", err
		}
		now := time.Now()
		headers := map[string]string{
			signing.HeaderID: id, signing.HeaderTimestamp: fmt.Sprint(now.Unix()),
			signing.HeaderSignature: signing.Sign(now, body, ch.SigningSecret),
		}
		if ch.HeaderName != "" {
			headers[ch.HeaderName] = ch.HeaderValue
		}
		return s.postRaw(ctx, ch.URL, body, headers)
	}
	return "", invalid("%q is not a channel type", ch.Type)
}

func slackText(title string, m Message) string {
	out := "*" + title + "*"
	if m.Summary != "" {
		out += "\n" + m.Summary
	}
	if len(m.Lines) > 0 {
		out += "\n```\n" + strings.Join(m.Lines, "\n") + "\n```"
	}
	if m.Link != "" {
		out += "\n<" + m.Link + "|Open in PlayerSense>"
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func (s *Sender) post(ctx context.Context, endpoint string, body any, headers map[string]string) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return s.postRaw(ctx, endpoint, raw, headers)
}

func (s *Sender) postRaw(ctx context.Context, endpoint string, body []byte, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PlayerSense-Notify/1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("answered %d: %s", res.StatusCode, strings.TrimSpace(string(answer)))
	}
	return fmt.Sprintf("answered %d", res.StatusCode), nil
}
