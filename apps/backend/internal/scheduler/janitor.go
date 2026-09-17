package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"playersense.dev/backend/internal/erasure"
	"playersense.dev/backend/internal/export"
	"playersense.dev/backend/internal/retention"
)

// Janitor runs the data-lifecycle half of §79's list, on an interval.
//
// §79 puts all of this under one heading — "remove expired archives", "create exports", "run
// data deletion workflows" — alongside the nightly derivation, and it is the same package
// for that reason. It is a second loop rather than a second package because the difference
// between them is a cadence, not a kind.
//
// # Why not nightly
//
// The derivation is nightly because it rebuilds whole months and there is no sense rebuilding
// them twice. These three are the opposite shape:
//
//   - An **erasure** has a legal deadline and a drain window. §61 tombstones first and purges
//     asynchronously, and the purge has to run repeatedly while late deliveries are still
//     arriving (§21) — once a night would make the window a day wide for no reason.
//   - An **export** is something a person is waiting for. A studio that submits a request
//     and is told to come back tomorrow has been given a worse answer than the system can
//     give.
//   - **Expiry** is over-retention while it waits. Every pass that has not run is an object
//     held past the term a customer agreed to, and §60 is a promise about that term.
//
// # Why the three are independent
//
// One pass runs all three and none of them can stop the others: a bucket outage that blocks
// expiry must not also block an erasure, which has a deadline attached to it, and an export
// that fails must not leave the archive over-retained. The errors are joined and returned
// together, so `worker once` still exits non-zero and nothing is hidden.
type Janitor struct {
	log  *slog.Logger
	opts JanitorOptions

	now func() time.Time

	stats  janitorStats
	cancel context.CancelFunc
	done   chan struct{}
}

// JanitorOptions configures the loop and the jobs it drives.
//
// Each job is a pointer and a nil one is skipped, which is how a deployment that has not
// configured an object store still runs the erasure: the alternative is a worker that
// refuses to start because one of three unrelated things is missing.
type JanitorOptions struct {
	// Every is how often a pass runs.
	Every time.Duration

	Sweeper  *retention.Sweeper
	Purger   *erasure.Purger
	Exporter *export.Exporter
}

type janitorStats struct {
	runs     atomic.Int64
	failures atomic.Int64
	lastRun  atomic.Int64

	expired   atomic.Int64
	erased    atomic.Int64
	exported  atomic.Int64
	collected atomic.Int64 // export bundles removed at the end of their term
}

// DefaultJanitorEvery is the pass interval when none is configured.
//
// Chosen against the erasure rather than against the other two: §61's drain window is the
// span over which a late delivery can still arrive, and the purge has to run several times
// inside it for "completed" to mean anything. A quarter of an hour gives a day's window
// ninety-six passes, each of which does almost nothing when there is nothing to do.
const DefaultJanitorEvery = 15 * time.Minute

// NewJanitor returns a loop, or an error when nothing is configured for it to do.
func NewJanitor(log *slog.Logger, opts JanitorOptions) (*Janitor, error) {
	if opts.Every <= 0 {
		opts.Every = DefaultJanitorEvery
	}
	if opts.Sweeper == nil && opts.Purger == nil && opts.Exporter == nil {
		return nil, errors.New("the janitor has no jobs configured")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Janitor{
		log:  log,
		opts: opts,
		now:  func() time.Time { return time.Now().UTC() },
		done: make(chan struct{}),
	}, nil
}

// Name identifies the component in start-up and shutdown logs.
func (j *Janitor) Name() string { return "janitor" }

// Start begins the interval loop, in the background.
//
// No pass on start-up, for the reason the derivation gives: a process that crash-loops would
// otherwise run one on every restart. The first pass is one interval away, and `worker once`
// is what forces an immediate one. Nothing time-critical waits on it — §61's tombstone is
// raised by the surface that accepts the request, not here.
func (j *Janitor) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel

	go func() {
		defer close(j.done)
		j.loop(ctx)
	}()

	j.log.Info("janitor started",
		"every", j.opts.Every.String(),
		"expiry", j.opts.Sweeper != nil,
		"erasure", j.opts.Purger != nil,
		"export", j.opts.Exporter != nil)
	return nil
}

// Stop ends the loop and waits for a pass in flight.
//
// Waiting matters more here than it does for the derivation. A purge holds a request in
// `purging` while it works, and an archive redaction can be between writing a replacement
// object and retiring the one it replaced — both are safe to interrupt by construction, but
// finishing is still cheaper than resuming, and the shutdown timeout bounds the wait.
func (j *Janitor) Stop(ctx context.Context) error {
	if j.cancel == nil {
		return nil
	}
	j.cancel()
	select {
	case <-j.done:
	case <-ctx.Done():
		return fmt.Errorf("a lifecycle pass was still running at the shutdown deadline")
	}
	j.log.Info("janitor stopped",
		"runs", j.stats.runs.Load(), "failures", j.stats.failures.Load())
	return nil
}

// Stats reports what the loop has done, for the health endpoint and for tests.
func (j *Janitor) Stats() map[string]int64 {
	return map[string]int64{
		"runs":     j.stats.runs.Load(),
		"failures": j.stats.failures.Load(),
		"last_run": j.stats.lastRun.Load(),

		"archive_objects_expired": j.stats.expired.Load(),
		"erasures_completed":      j.stats.erased.Load(),
		"exports_completed":       j.stats.exported.Load(),
		"export_bundles_expired":  j.stats.collected.Load(),
	}
}

func (j *Janitor) loop(ctx context.Context) {
	ticker := time.NewTicker(j.opts.Every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if err := j.Run(ctx); err != nil {
			// Logged and swallowed, like the derivation's: a pass that failed now must
			// not stop the one that would succeed in fifteen minutes, and every job here
			// is idempotent so the retry is simply the next pass.
			j.stats.failures.Add(1)
			j.log.Error("a lifecycle pass failed", "error", err)
		}
	}
}

// Run executes one complete pass.
//
// Exported so `worker once` and the tests drive exactly what the timer drives. A scheduled
// job whose manual form takes a different path is a job whose manual form is untested.
func (j *Janitor) Run(ctx context.Context) error {
	var problems []error

	// Erasure first. It is the one with a deadline in law rather than in a contract, and
	// the one whose delay is measured against a promise made to a person.
	if j.opts.Purger != nil {
		done, err := j.opts.Purger.Run(ctx, j.now())
		j.stats.erased.Add(int64(done.Completed))
		if err != nil {
			problems = append(problems, fmt.Errorf("erasure: %w", err))
		}
		if done.Considered > 0 {
			j.log.Info("erasure pass",
				"considered", done.Considered,
				"completed", done.Completed,
				"failed", done.Failed,
				"devices", done.Devices,
				"objects_redacted", done.Objects,
				"purchases", done.Purchases,
				"export_bundles_removed", done.Exports)
		}
	}

	// Then exports, which somebody is waiting for.
	if j.opts.Exporter != nil {
		done, err := j.opts.Exporter.Run(ctx, j.now())
		j.stats.exported.Add(int64(done.Completed))
		j.stats.collected.Add(int64(done.Expired))
		if err != nil {
			problems = append(problems, fmt.Errorf("export: %w", err))
		}
		if done.Considered > 0 || done.Expired > 0 {
			j.log.Info("export pass",
				"considered", done.Considered,
				"completed", done.Completed,
				"failed", done.Failed,
				"bundles_expired", done.Expired,
				"rows", done.Rows,
				"bytes", done.Bytes)
		}
	}

	// Then expiry, which is over-retention while it waits but is not waiting on anyone.
	if j.opts.Sweeper != nil {
		done, err := j.opts.Sweeper.Run(ctx, j.now())
		j.stats.expired.Add(int64(done.Deleted))
		if err != nil {
			problems = append(problems, fmt.Errorf("archive expiry: %w", err))
		}
		if done.Due > 0 {
			j.log.Info("archive expiry pass",
				"due", done.Due,
				"deleted", done.Deleted,
				"failed", done.Failed,
				"bytes_reclaimed", done.Bytes)
		}
	}

	j.stats.runs.Add(1)
	j.stats.lastRun.Store(j.now().Unix())

	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}
