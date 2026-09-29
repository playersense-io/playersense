package staffnotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/pgdb"
)

// Marker is the once-a-day lock. *valkey.Client is one.
type Marker interface {
	Ready() bool
	Key(parts ...string) string
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Del(ctx context.Context, keys ...string) error
}

// Growth posts the day's digest at a set time, once, however many workers there are and however
// often they restart.
//
// The time is UTC, for the reason WORKER_DERIVE_AT is: a job anchored to a zone with daylight
// saving runs twice or not at all on two days a year.
//
// "Once" is a Valkey SETNX on the day, set before the digest is read and released if the post fails,
// so a second worker skips and a failed post is tried again on the next tick. Valkey keeps nothing
// across a restart of its own (ADR 0001), so a Valkey restart between the post and the end of the
// window can send the digest a second time. That is the choice: a duplicate digest over a missing
// one, and only inside the window.
type Growth struct {
	DB      *pgdb.DB
	Cache   Marker
	Sender  Sender
	Channel notify.Channel
	// At is how long after midnight UTC the digest is due.
	At time.Duration
	// Window is how long after At a digest is still worth sending, so a worker that comes up at
	// dinner time does not post the morning's numbers. Six hours if unset.
	Window time.Duration
	// Every is how often to look at the clock. A minute if unset.
	Every     time.Duration
	Dashboard string
	Logger    *slog.Logger
	Now       func() time.Time

	cancel context.CancelFunc
	done   chan struct{}
}

// NewGrowth returns nil when there is nowhere to send.
func NewGrowth(db *pgdb.DB, cache Marker, sender Sender, url string, at time.Duration, dashboard string) *Growth {
	if strings.TrimSpace(url) == "" || sender == nil {
		return nil
	}
	return &Growth{
		DB: db, Cache: cache, Sender: sender, At: at, Dashboard: dashboard,
		Channel: notify.Channel{Type: notify.Slack, Name: "PlayerSense staff growth", URL: url},
	}
}

// Name identifies the component in start-up and shutdown logs.
func (g *Growth) Name() string { return "staff-growth-digest" }

// Start begins watching the clock in the background.
func (g *Growth) Start(context.Context) error {
	if g == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel, g.done = cancel, make(chan struct{})
	every := g.Every
	if every <= 0 {
		every = time.Minute
	}
	go func() {
		defer close(g.done)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			if _, err := g.Pass(ctx); err != nil && ctx.Err() == nil && g.Logger != nil {
				g.Logger.Warn("the growth digest was not posted; tried again shortly", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return nil
}

// Stop ends the watch, waiting for the pass in flight.
func (g *Growth) Stop(ctx context.Context) error {
	if g == nil || g.cancel == nil {
		return nil
	}
	g.cancel()
	select {
	case <-g.done:
	case <-ctx.Done():
	}
	return nil
}

func (g *Growth) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// Pass posts today's digest if it is due and nobody has, and says whether it did. The digest covers
// yesterday: a day that has ended, so the numbers do not move after they are read.
func (g *Growth) Pass(ctx context.Context) (bool, error) {
	now := g.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	due := today.Add(g.At)
	window := g.Window
	if window <= 0 {
		window = 6 * time.Hour
	}
	if now.Before(due) || !now.Before(due.Add(window)) {
		return false, nil
	}

	// The application starts its components together, so the first tick can come before the pools
	// are open. Nothing to fail about: the next tick is a minute away.
	if !g.DB.Ready() || !g.Cache.Ready() {
		return false, nil
	}

	key := g.Cache.Key("staff", "growth", today.Format("2006-01-02"))
	first, err := g.Cache.SetNX(ctx, key, []byte("1"), 48*time.Hour)
	if err != nil {
		// Failing closed: without the lock, two workers would each post.
		return false, fmt.Errorf("take the day's lock: %w", err)
	}
	if !first {
		return false, nil
	}
	release := func() { _ = g.Cache.Del(context.WithoutCancel(ctx), key) }

	digest, err := Collect(ctx, g.DB.Client, today.AddDate(0, 0, -1))
	if err != nil {
		release()
		return false, err
	}
	if res := g.Sender.Send(ctx, g.Channel, digest.Message(g.Dashboard)); !res.OK {
		release()
		return false, fmt.Errorf("post to Slack: %s", res.Detail)
	}
	return true, nil
}
