package webhooks

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
)

// RetryAfter is the wait after each failed try: a minute, five, half an hour, then hours, then a
// day at a time, three days in all. Each wait is stretched by up to a tenth at random, so an
// endpoint coming back is not met by every retry at once.
var RetryAfter = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour,
	6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 24 * time.Hour}

// Tries is every try a delivery gets: the first, and one after each wait.
var Tries = len(RetryAfter) + 1

// DisableAfter is how long an endpoint may answer nothing but failures before it is turned off.
const DisableAfter = 72 * time.Hour

// NextTry is when a delivery that has had `made` tries is tried again, and false when it has had
// them all.
func NextTry(made int, now time.Time) (time.Time, bool) {
	if made < 1 || made > len(RetryAfter) {
		return time.Time{}, false
	}
	wait := RetryAfter[made-1]
	return now.Add(wait + time.Duration(rand.Int64N(int64(wait)/10+1))), true
}

// Outcomes of a delivery after a try.
const (
	Succeeded = "succeeded"
	Retrying  = "retrying"
	Failed    = "failed"
)

// Attempt is one try, as the log keeps it.
type Attempt struct {
	ProjectID       int
	EndpointUID     uuid.UUID
	DeliveryID      uuid.UUID
	EventID         uuid.UUID
	Type            string
	Environment     string
	DeliveryCreated time.Time
	Number          int
	Result          Result
	Outcome         string
	NextAt          *time.Time
	Manual, Test    bool
}

// LoggedEvent is an event's body, kept so it can be read and sent again.
type LoggedEvent struct {
	ProjectID   int
	EventID     uuid.UUID
	Type        string
	Environment string
	CreatedAt   time.Time
	Body        []byte
}

// AttemptLog writes tries and events to ClickHouse. The worker's is buffered: rows collect and go
// in one insert a second, or sooner at a few thousand, which is what ClickHouse wants and what
// millions of tries a day need. Write sends at once, for a test or a resend from the dashboard.
type AttemptLog struct {
	CH    interface{ DB() (driver.Conn, error) }
	Every time.Duration
	Max   int
	Log   *slog.Logger

	mu       sync.Mutex
	attempts []Attempt
	events   []LoggedEvent
	kick     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
}

func (l *AttemptLog) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// Name identifies the component in start-up and shutdown logs.
func (l *AttemptLog) Name() string { return "webhook-log" }

// Record queues a try, and the event it carried when it is the event's first.
func (l *AttemptLog) Record(a Attempt, event *LoggedEvent) {
	l.mu.Lock()
	l.attempts = append(l.attempts, a)
	if event != nil {
		l.events = append(l.events, *event)
	}
	full := len(l.attempts) >= l.max()
	l.mu.Unlock()
	if full && l.kick != nil {
		select {
		case l.kick <- struct{}{}:
		default:
		}
	}
}

// RecordEvent queues an event's body alone, for a delivery held back before its first try.
func (l *AttemptLog) RecordEvent(e LoggedEvent) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *AttemptLog) max() int {
	if l.Max <= 0 {
		return 5000
	}
	return l.Max
}

// Start begins flushing in the background.
func (l *AttemptLog) Start(context.Context) error {
	if l.Every <= 0 {
		l.Every = time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.done, l.kick = cancel, make(chan struct{}), make(chan struct{}, 1)
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(l.Every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-l.kick:
			}
			if err := l.Flush(context.Background()); err != nil {
				l.log().Error("the webhook log could not be written; kept for the next flush", "error", err)
			}
		}
	}()
	return nil
}

// Stop flushes what is queued and ends the loop.
func (l *AttemptLog) Stop(ctx context.Context) error {
	if l.cancel != nil {
		l.cancel()
		select {
		case <-l.done:
		case <-ctx.Done():
		}
	}
	return l.Flush(ctx)
}

// Flush writes what is queued. A failed write keeps the rows, bounded, for the next.
func (l *AttemptLog) Flush(ctx context.Context) error {
	l.mu.Lock()
	attempts, events := l.attempts, l.events
	l.attempts, l.events = nil, nil
	l.mu.Unlock()
	if len(attempts) == 0 && len(events) == 0 {
		return nil
	}
	if err := l.Write(ctx, attempts, events); err != nil {
		l.mu.Lock()
		// Kept for the next flush, but never more than ten flushes' worth: a log that cannot be
		// written must not take the worker's memory with it.
		if len(l.attempts)+len(attempts) <= 10*l.max() {
			l.attempts = append(attempts, l.attempts...)
			l.events = append(events, l.events...)
		}
		l.mu.Unlock()
		return err
	}
	return nil
}

// Write inserts rows now.
func (l *AttemptLog) Write(ctx context.Context, attempts []Attempt, events []LoggedEvent) error {
	conn, err := l.CH.DB()
	if err != nil {
		return err
	}
	if len(events) > 0 {
		block, err := conn.PrepareBatch(ctx, `INSERT INTO webhook_events (project_seq, event_id, type, environment, created_at, payload)`)
		if err != nil {
			return fmt.Errorf("prepare webhook_events: %w", err)
		}
		for _, e := range events {
			if err := block.Append(uint32(e.ProjectID), e.EventID, e.Type, e.Environment, e.CreatedAt.UTC(), string(e.Body)); err != nil {
				return err
			}
		}
		if err := block.Send(); err != nil {
			return fmt.Errorf("write webhook_events: %w", err)
		}
	}
	if len(attempts) > 0 {
		block, err := conn.PrepareBatch(ctx, `INSERT INTO webhook_attempts (project_seq, endpoint_id, delivery_id, event_id,
			type, environment, delivery_created, attempt, at, status_code, error, duration_ms, response, outcome, next_at, manual, test)`)
		if err != nil {
			return fmt.Errorf("prepare webhook_attempts: %w", err)
		}
		for _, a := range attempts {
			var next *time.Time
			if a.NextAt != nil {
				n := a.NextAt.UTC()
				next = &n
			}
			if err := block.Append(uint32(a.ProjectID), a.EndpointUID, a.DeliveryID, a.EventID, a.Type, a.Environment,
				a.DeliveryCreated.UTC(), uint16(a.Number), a.Result.At.UTC(), uint16(a.Result.Code), a.Result.Error,
				uint32(max(a.Result.Ms, 0)), a.Result.Response, a.Outcome, next, boolByte(a.Manual), boolByte(a.Test)); err != nil {
				return err
			}
		}
		if err := block.Send(); err != nil {
			return fmt.Errorf("write webhook_attempts: %w", err)
		}
	}
	return nil
}

func boolByte(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// Canceled closes the deliveries an endpoint was owed when it went off or away: each gets a last
// entry, failed, so the log does not show it retrying forever.
func Canceled(retries []*ent.WebhookRetry, endpoint uuid.UUID, why string, now time.Time) []Attempt {
	out := make([]Attempt, 0, len(retries))
	for _, r := range retries {
		out = append(out, Attempt{ProjectID: r.ProjectID, EndpointUID: endpoint, DeliveryID: r.UID, EventID: r.EventUID,
			Type: r.Type, Environment: r.Environment, DeliveryCreated: r.FirstAt, Number: max(r.Attempts, 1),
			Result: Result{At: now.UTC(), Error: why}, Outcome: Failed, Manual: r.Manual})
	}
	return out
}
