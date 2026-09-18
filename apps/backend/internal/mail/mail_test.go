package mail_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"playersense.dev/backend/internal/mail"
)

// The placeholder is what every deployment runs today, so its two properties are the ones worth a
// test: it does not claim to have delivered anything, and it does not write the token.

// TestThePlaceholderDoesNotClaimDelivery is about a log line somebody will read while debugging a
// missing invitation.
//
// A line reading "sent invitation to ada@studio.example" would be taken as evidence that a message
// arrived, and the person reading it would go looking at mail servers instead of at MAIL_SENDER.
func TestThePlaceholderDoesNotClaimDelivery(t *testing.T) {
	var out bytes.Buffer
	sender := mail.Logger{Log: slog.New(slog.NewJSONHandler(&out, nil))}

	err := sender.Send(t.Context(), mail.Message{
		Kind: mail.Invitation, To: "ada@studio.example",
		Subject: "Join Pixel Forge on PlayerSense",
		Body:    "https://dash.example/invitations/accept?token=SECRET-TOKEN\n",
	})
	if err != nil {
		t.Fatalf("Send = %v", err)
	}

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &line); err != nil {
		t.Fatalf("the log line is not JSON: %v\n%s", err, out.String())
	}
	if delivered, _ := line["delivered"].(bool); delivered {
		t.Error(`the line claims delivered: true`)
	}
	if to, _ := line["to"].(string); to != "ada@studio.example" {
		t.Errorf("to = %v, want the recipient so a missing message can be traced", line["to"])
	}
	if !strings.Contains(out.String(), "did not send") {
		t.Errorf("the line does not say nothing was sent: %s", out.String())
	}

	// And Delivers agrees, which is what the response's `delivered` member is built from.
	if mail.Delivers(sender) {
		t.Error("Delivers reported true for the placeholder")
	}
}

// TestTheTokenIsNotLoggedByDefault is the reason IncludeBody exists as a setting rather than as the
// default.
//
// The body is the invitation link and the link is the token. In a log it outlives the invitation's
// own expiry, reaches whatever aggregates logs, and is readable by people who were never invited to
// anything — so a developer opts in, in development.
func TestTheTokenIsNotLoggedByDefault(t *testing.T) {
	const token = "SECRET-TOKEN"
	message := mail.Message{
		Kind: mail.Invitation, To: "ada@studio.example", Subject: "Join",
		Body: "https://dash.example/invitations/accept?token=" + token + "\n",
	}

	var quiet bytes.Buffer
	if err := (mail.Logger{Log: slog.New(slog.NewJSONHandler(&quiet, nil))}).
		Send(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet.String(), token) {
		t.Errorf("the token reached the log without being asked for:\n%s", quiet.String())
	}

	// Asked for, it is there — because a developer with no inbox has no other way to follow
	// the flow.
	var loud bytes.Buffer
	if err := (mail.Logger{Log: slog.New(slog.NewJSONHandler(&loud, nil)), IncludeBody: true}).
		Send(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loud.String(), token) {
		t.Errorf("MAIL_LOG_BODY did not write the body:\n%s", loud.String())
	}
}

// TestARecorderIsNotAProvider is the mistake the members suite would otherwise have made.
//
// A recorder delivers as little as the logger does, so a test substituting one must not see
// `delivered: true` and prove the opposite of what it meant to.
func TestARecorderIsNotAProvider(t *testing.T) {
	recorder := &mail.Recorder{}
	if mail.Delivers(recorder) {
		t.Error("Delivers reported true for a recorder")
	}

	for _, to := range []string{"Ada@studio.example", "grace@studio.example"} {
		if err := recorder.Send(t.Context(), mail.Message{
			Kind: mail.Invitation, To: to, Subject: "Join",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Matched the way an address should be: a studio inviting `Ada@` and a person signing in as
	// `ada@` is not two people.
	if got := len(recorder.To("ada@STUDIO.example")); got != 1 {
		t.Errorf("To matched %d messages, want 1 — addresses compare case-insensitively", got)
	}
	if got := len(recorder.Sent()); got != 2 {
		t.Errorf("Sent = %d, want 2", got)
	}
}

// TestAnUnknownSenderIsFatalRatherThanQuiet is the failure direction that matters at start-up.
//
// A deployment that meant to configure a provider and mistyped the name must not come up cleanly
// and deliver nothing: the symptom would be invitations that never arrive, discovered by a customer
// rather than by the deploy.
func TestAnUnknownSenderIsFatalRatherThanQuiet(t *testing.T) {
	if _, err := mail.New("ses", nil, false); err == nil {
		t.Error(`New("ses") succeeded; no provider is built, so naming one must fail`)
	} else if !strings.Contains(err.Error(), "log") {
		t.Errorf("the error does not say what is available: %v", err)
	}

	// The two spellings of the placeholder, because an unset setting is the common case.
	for _, name := range []string{"log", ""} {
		sender, err := mail.New(name, nil, false)
		if err != nil {
			t.Fatalf("New(%q) = %v", name, err)
		}
		if mail.Delivers(sender) {
			t.Errorf("New(%q) returned something claiming to deliver", name)
		}
	}
}
