// Package mail sends the few messages PlayerSense sends itself.
//
// "Itself" is the whole scope. Every *authentication* email — confirm your address, reset your
// password, the passwordless sign-in link — is sent by Firebase's browser SDK, which owns the
// credential and enforces its own sending protections (docs/rate-limits.md §6). Duplicating that
// here once produced two systems believing they owned the same message, and the fix was to
// delete this side of it.
//
// What is left is mail about PlayerSense's own state rather than about a credential, and today
// that is exactly one message: an invitation to join an organization. It cannot go through
// Firebase because Firebase does not know what an organization is.
//
// # Why an interface for one message
//
// Not for future providers — for the test. An invitation is the only write in the control plane
// whose effect leaves the system, so "was an invitation sent, to whom, once" has to be assertable
// without a network and without a real inbox. [Recorder] is that, and it is the reason the
// handler takes a Sender rather than calling a client.
//
// The second reason is that the placeholder is honest. [Logger] does not pretend to deliver: it
// says a message was composed and not sent, so a log cannot be read as evidence that somebody
// received something.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Kind labels a message for logs and tests without disclosing what is in it.
type Kind string

// The messages in the product: an invitation, and a notification to a report's email channel.
const (
	Invitation   Kind = "invitation"
	Notification Kind = "notification"
)

// Message is one email.
//
// Plain text only. An invitation is three sentences and a link; HTML would add a template
// language, a second body to keep in step with the first, and a rendering surface — for a message
// whose entire content is a URL that has to be copyable.
type Message struct {
	Kind    Kind
	To      string
	Subject string
	Body    string
}

// Sender delivers a message, or reports that it could not.
//
// The error matters: sending an invitation is what makes the invitation useful, so a caller that
// ignored this would write a row nobody can act on and answer 201.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Logger is the placeholder sender: it composes the message and delivers nothing.
//
// It exists so the flow is complete and testable before a provider is configured, and it says so
// in every line it writes. A log that read "sent invitation to ada@studio.example" would be read
// as delivery by the next person to debug a missing email.
type Logger struct {
	// Log is where lines go. Nil falls back to the default logger rather than panicking: a
	// misconfigured sender must not be the thing that takes the control plane down.
	Log *slog.Logger

	// IncludeBody writes the body too, which is how a developer gets the invitation link
	// out of a local stack without an inbox.
	//
	// **Off by default, because the body contains the token.** An invitation token is a
	// bearer credential: whoever holds it joins that organization as whatever the
	// invitation grants. In a log it outlives the invitation's own expiry, gets shipped to
	// whatever aggregates logs, and is readable by people who were never invited to
	// anything. On by hand, in development, and never in a deployment.
	IncludeBody bool
}

// Send writes a line and returns nil.
func (l Logger) Send(_ context.Context, message Message) error {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}

	attrs := []any{
		"kind", string(message.Kind),
		"to", message.To,
		"subject", message.Subject,
		// Stated in the line rather than only in this file, because the person reading it
		// is looking for a message that never arrived.
		"delivered", false,
	}
	if l.IncludeBody {
		attrs = append(attrs, "body", message.Body)
	}
	log.Info("composed an email and did not send it: no mail provider is configured", attrs...)
	return nil
}

// Placeholder is implemented by a sender that composes a message and delivers nothing.
//
// An optional interface rather than a method on [Sender], so that adding a real provider means
// implementing Send and nothing else — the absence of this is what "it delivers" means, which is
// the right default for a type somebody writes in order to deliver mail.
type Placeholder interface {
	// Placeholder reports that nothing was actually sent.
	Placeholder() bool
}

// Delivers reports whether a sender hands messages to something that delivers them.
//
// Read by the handler so a response can say so. A dashboard that told somebody to check their
// inbox for a message that was only written to a log would be lying on the product's behalf, and
// the person who pays for that is the colleague waiting for an invitation that never arrives.
func Delivers(sender Sender) bool {
	placeholder, ok := sender.(Placeholder)
	return !ok || !placeholder.Placeholder()
}

// Placeholder reports that this sender delivers nothing.
func (Logger) Placeholder() bool { return true }

// Placeholder reports that this sender delivers nothing.
//
// A recorder is no more a provider than the logger is. Without this, a test substituting one would
// see `delivered: true` and prove the opposite of what it meant to.
func (*Recorder) Placeholder() bool { return true }

// Recorder keeps messages in memory, for tests.
//
// Concurrent by construction. The control plane's suite runs in parallel and one refused
// invitation must not be explained by a data race in the thing observing it.
type Recorder struct {
	// Fail, when set, is returned by Send instead of recording anything — for the one test
	// that needs a provider to refuse, which is the case a real deployment will eventually
	// hit and the reason the caller has to handle this error at all.
	Fail error

	mu   sync.Mutex
	sent []Message
}

// Send records a message, or returns Fail without recording it.
func (r *Recorder) Send(_ context.Context, message Message) error {
	if r.Fail != nil {
		return r.Fail
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, message)
	return nil
}

// Sent returns a copy of what was recorded.
func (r *Recorder) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.sent...)
}

// To returns the messages recorded for one address, compared the way an address should be.
func (r *Recorder) To(address string) []Message {
	var out []Message
	for _, message := range r.Sent() {
		if strings.EqualFold(message.To, address) {
			out = append(out, message)
		}
	}
	return out
}

// New picks a sender by name, so a deployment chooses one in configuration.
//
// `log` is the only one built. A name this package does not know is an error rather than a
// silent fallback to the placeholder: a deployment that meant to configure a real provider and
// typed it wrongly would otherwise start cleanly and quietly deliver nothing.
func New(name string, log *slog.Logger, includeBody bool) (Sender, error) {
	switch name {
	case "log", "":
		return Logger{Log: log, IncludeBody: includeBody}, nil
	default:
		return nil, fmt.Errorf("unknown MAIL_SENDER %q; want log", name)
	}
}
