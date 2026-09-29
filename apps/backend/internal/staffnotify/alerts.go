// Package staffnotify tells PlayerSense's own staff how the business is doing and when something
// broke, through two Slack incoming webhooks in the staff workspace.
//
// It is not the notification system of internal/alerts and internal/notify, which belongs to a
// studio: their channels, their rules, their rate caps, stored per project. These two destinations
// belong to PlayerSense, are set in the environment, and hold no rows.
//
//   - **Growth** is a digest at a set time each day: money in, new organizations, users.
//   - **Alerts** is what a person should look at now: every ERROR a process logs, which includes
//     each panic the framework recovers, held to a few a minute so a failing dependency is one
//     message and a count rather than a flood.
//
// Either is off when its URL is empty, and a process with neither behaves exactly as it did.
package staffnotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"playersense.dev/backend/internal/notify"
)

// Sender is what posts a message. *notify.Sender is one.
type Sender interface {
	Send(ctx context.Context, ch notify.Channel, m notify.Message) notify.Result
}

// Alerts posts to the alerts channel.
//
// Raise never blocks and never fails: it is called from a logger, and a logger that waits on
// Slack, or that errors when Slack is down, turns an outage of Slack into an outage of whatever
// was logging. A full queue drops the message and counts it.
type Alerts struct {
	Sender  Sender
	Channel notify.Channel
	// Dedupe is how long a message like one already sent is held back. It is the whole answer to a
	// crash loop: the first is sent, the rest are counted, and the count rides on the next.
	Dedupe time.Duration
	// PerHour bounds what is sent in any hour, across every kind of message.
	PerHour int
	// Logger records what this cannot say to Slack, at a level below error so that a failed post
	// is never itself an alert.
	Logger *slog.Logger
	Now    func() time.Time

	queue  chan notify.Message
	cancel context.CancelFunc
	done   chan struct{}

	mu         sync.Mutex
	seen       map[string]time.Time
	hour       time.Time
	sentInHour int
	suppressed map[string]int
	dropped    int
}

// NewAlerts returns nil when there is nowhere to send, which every method accepts, so a caller
// wires it unconditionally.
func NewAlerts(sender Sender, url string) *Alerts {
	if strings.TrimSpace(url) == "" || sender == nil {
		return nil
	}
	return &Alerts{
		Sender: sender, Channel: notify.Channel{Type: notify.Slack, Name: "PlayerSense staff alerts", URL: url},
		Dedupe: 15 * time.Minute, PerHour: 20,
		queue: make(chan notify.Message, 64),
	}
}

// Name identifies the component in start-up and shutdown logs.
func (a *Alerts) Name() string { return "staff-alerts" }

// Start begins posting in the background. What was raised before this is waiting.
func (a *Alerts) Start(context.Context) error {
	if a == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel, a.done = cancel, make(chan struct{})
	go func() {
		defer close(a.done)
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-a.queue:
				// Not cancelled by Stop: a message being posted when the process is told to end
				// is the one somebody most wants to read.
				a.post(context.WithoutCancel(ctx), m)
			}
		}
	}()
	return nil
}

// Stop ends posting. What is queued and unsent is not, because the process is going: a panic that
// takes it down has been raised already and gets its chance in the drain.
func (a *Alerts) Stop(ctx context.Context) error {
	if a == nil || a.cancel == nil {
		return nil
	}
	deadline := time.After(3 * time.Second)
	for drained := false; !drained; {
		select {
		case m := <-a.queue:
			a.post(ctx, m)
		case <-deadline:
			drained = true
		case <-ctx.Done():
			drained = true
		default:
			drained = true
		}
	}
	a.cancel()
	select {
	case <-a.done:
	case <-ctx.Done():
	}
	return nil
}

// Raise queues one alert. `key` says what it is a repeat of: the same key within Dedupe is not sent
// again, only counted.
func (a *Alerts) Raise(key, source, title, summary string, lines ...string) {
	if a == nil {
		return
	}
	extra, ok := a.admit(key, a.now())
	if !ok {
		return
	}
	if extra != "" {
		lines = append(lines, extra)
	}
	m := notify.Message{Title: title, Summary: summary, Lines: lines, Source: source, Severity: "critical"}
	select {
	case a.queue <- m:
	default:
		a.mu.Lock()
		a.dropped++
		a.mu.Unlock()
	}
}

func (a *Alerts) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// admit decides whether a message is sent now, and, when it is, what to say about the ones held
// back since the last that was. The order of the two checks matters: a repeat is counted against
// its key without spending the hour's allowance.
func (a *Alerts) admit(key string, now time.Time) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen, a.suppressed = map[string]time.Time{}, map[string]int{}
	}
	if last, ok := a.seen[key]; ok && now.Sub(last) < a.Dedupe {
		a.suppressed[key]++
		return "", false
	}
	if h := now.Truncate(time.Hour); !h.Equal(a.hour) {
		a.hour, a.sentInHour = h, 0
	}
	if a.sentInHour >= a.PerHour {
		a.suppressed[key]++
		return "", false
	}
	a.sentInHour++
	a.seen[key] = now
	if len(a.seen) > 1000 {
		for k, t := range a.seen {
			if now.Sub(t) >= a.Dedupe {
				delete(a.seen, k)
				delete(a.suppressed, k)
			}
		}
	}
	extra := ""
	if n := a.suppressed[key]; n > 0 {
		extra = fmt.Sprintf("%d more like this since the last one", n)
		delete(a.suppressed, key)
	}
	return extra, true
}

func (a *Alerts) post(ctx context.Context, m notify.Message) {
	res := a.Sender.Send(ctx, a.Channel, m)
	if !res.OK && a.Logger != nil {
		a.Logger.Warn("could not post a staff alert", "detail", res.Detail, "title", m.Title)
	}
}

// Held says what was held back and never sent, for a test and for a process's last words.
func (a *Alerts) Held() (suppressed, dropped int) {
	if a == nil {
		return 0, 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range a.suppressed {
		suppressed += n
	}
	return suppressed, a.dropped
}
