package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/erasure"
	"playersense.dev/backend/internal/export"
	"playersense.dev/backend/internal/jobs"
	"playersense.dev/backend/internal/metering"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/projects"
	"playersense.dev/backend/internal/retention"
	"playersense.dev/backend/internal/wallet"
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

	// Meter derives §40's usage days, and Credit writes off promotional credit whose date has
	// passed. Both are billing rather than data lifecycle, and both belong on this loop for the
	// same reason the other three do: each is waiting on the calendar rather than on the night.
	//
	// A day's usage that is hours late is a usage page that is hours behind; credit that lapsed
	// at midnight and is written off at ten past is a balance that was briefly overstated and
	// never overspent, because the spend path refuses a lapsed lot on its own.
	Meter  *metering.Meter
	Credit *creditExpiry

	// LogCharges prices Cloud Logging's two meters (ADR 0050).
	LogCharges *charging.LogAccruer

	// Deletion carries out scheduled project deletions and Retention enforces a lowered raw
	// retention on rows already written. Both before archive expiry, so a deleted project's
	// objects are expired in the pass that hands them over.
	Deletion  *projects.Deletion
	Retention *projects.Retention

	// Recorder keeps a row per job per pass for the operations console (job_runs). Nil keeps
	// none.
	Recorder jobs.Recorder
}

// creditExpiry writes off lapsed promotional credit.
//
// A small type rather than a function so the janitor's fields all read the same way, and so the
// transaction boundary lives beside the thing that needs one: every movement of credit is a lot
// change and a ledger entry together, and a job that took a client could not promise that.
type creditExpiry struct {
	DB    *pgdb.DB
	Log   *slog.Logger
	Limit int
}

// CreditExpiry returns the job, or nil when there is no control-plane connection to run it against.
func CreditExpiry(db *pgdb.DB, log *slog.Logger, limit int) *creditExpiry {
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = DefaultCreditExpiryLimit
	}
	return &creditExpiry{DB: db, Log: log, Limit: limit}
}

// DefaultCreditExpiryLimit bounds one pass.
//
// A pass writes two rows per lapsed lot inside one transaction, so the bound is about how long that
// transaction holds rather than about how much work there is: whatever is left is swept on the next
// pass, minutes later, and the spend path was already refusing the credit.
const DefaultCreditExpiryLimit = 500

// Run writes off everything that has lapsed, up to the pass limit.
func (c *creditExpiry) Run(ctx context.Context, at time.Time) (int, error) {
	tx, err := c.DB.Client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	written, err := wallet.ExpireLapsed(ctx, tx, at, c.Limit)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(written), nil
}

type janitorStats struct {
	runs     atomic.Int64
	failures atomic.Int64
	lastRun  atomic.Int64

	expired   atomic.Int64
	erased    atomic.Int64
	exported  atomic.Int64
	collected atomic.Int64 // export bundles removed at the end of their term
	metered   atomic.Int64 // usage days written
	lapsed    atomic.Int64 // promotional credit written off
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
	if opts.Sweeper == nil && opts.Purger == nil && opts.Exporter == nil &&
		opts.Meter == nil && opts.Credit == nil && opts.Deletion == nil && opts.Retention == nil &&
		opts.LogCharges == nil {
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
		"export", j.opts.Exporter != nil,
		"meter", j.opts.Meter != nil,
		"credit_expiry", j.opts.Credit != nil)
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
		"usage_days_metered":      j.stats.metered.Load(),
		"credit_lots_expired":     j.stats.lapsed.Load(),
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
	rec := j.opts.Recorder

	// Erasure first. It is the one with a deadline in law rather than in a contract, and
	// the one whose delay is measured against a promise made to a person.
	if j.opts.Purger != nil {
		err := jobs.Time(ctx, rec, jobs.Erasure, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Purger.Run(ctx, j.now())
			j.stats.erased.Add(int64(done.Completed))
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
			return map[string]int64{
				"considered": int64(done.Considered), "completed": int64(done.Completed),
				"failed": int64(done.Failed), "devices": int64(done.Devices),
				"objects_redacted": int64(done.Objects), "purchases": int64(done.Purchases),
				"export_bundles_removed": int64(done.Exports),
			}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("erasure: %w", err))
		}
	}

	// Then exports, which somebody is waiting for.
	if j.opts.Exporter != nil {
		err := jobs.Time(ctx, rec, jobs.Export, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Exporter.Run(ctx, j.now())
			j.stats.exported.Add(int64(done.Completed))
			j.stats.collected.Add(int64(done.Expired))
			if done.Considered > 0 || done.Expired > 0 {
				j.log.Info("export pass",
					"considered", done.Considered,
					"completed", done.Completed,
					"failed", done.Failed,
					"bundles_expired", done.Expired,
					"rows", done.Rows,
					"bytes", done.Bytes)
			}
			return map[string]int64{
				"considered": int64(done.Considered), "completed": int64(done.Completed),
				"failed": int64(done.Failed), "bundles_expired": int64(done.Expired),
				"rows": int64(done.Rows), "bytes": int64(done.Bytes),
			}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("export: %w", err))
		}
	}

	// Then deletions somebody scheduled thirty days ago, and retention somebody lowered.
	if j.opts.Deletion != nil {
		err := jobs.Time(ctx, rec, jobs.ProjectDeletion, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Deletion.Run(ctx, j.now())
			return map[string]int64{"closed": int64(done.Closed), "purged": int64(done.Purged)}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("project deletion: %w", err))
		}
	}
	if j.opts.Retention != nil {
		err := jobs.Time(ctx, rec, jobs.RetentionEnforce, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Retention.Run(ctx, j.now())
			if err != nil {
				return map[string]int64{"projects": int64(done.Projects)}, err
			}
			logged, err := j.opts.Retention.RunLogs(ctx, j.now())
			return map[string]int64{"projects": int64(done.Projects), "log_projects": int64(logged.Projects)}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("retention: %w", err))
		}
	}

	// Then expiry, which is over-retention while it waits but is not waiting on anyone.
	if j.opts.Sweeper != nil {
		err := jobs.Time(ctx, rec, jobs.ArchiveExpiry, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Sweeper.Run(ctx, j.now())
			j.stats.expired.Add(int64(done.Deleted))
			if done.Due > 0 {
				j.log.Info("archive expiry pass",
					"due", done.Due,
					"deleted", done.Deleted,
					"failed", done.Failed,
					"bytes_reclaimed", done.Bytes)
			}
			return map[string]int64{
				"due": int64(done.Due), "deleted": int64(done.Deleted), "failed": int64(done.Failed),
				"bytes_reclaimed": int64(done.Bytes),
			}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("archive expiry: %w", err))
		}
	}

	// Then the meter, which nobody is waiting on within the minute but which every usage page
	// and every month close reads.
	if j.opts.Meter != nil {
		err := jobs.Time(ctx, rec, jobs.UsageMeter, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Meter.Run(ctx)
			j.stats.metered.Add(int64(done.Rows))
			if done.Rows > 0 || done.Finalled > 0 {
				j.log.Info("usage meter pass",
					"days", done.Days,
					"rows", done.Rows,
					"days_finalized", done.Finalled)
			}
			return map[string]int64{
				"days": int64(done.Days), "rows": int64(done.Rows), "days_finalized": int64(done.Finalled),
			}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("usage meter: %w", err))
		}
	}

	// Cloud Logging's charges (ADR 0050): what each organization has taken in this month, every
	// pass, and what it kept, once a day. On this loop because a quarter of an hour is as late as a
	// log charge should be: long enough to batch, short enough that a runaway build is on the
	// balance, and under the suspension gate, within the hour.
	if j.opts.LogCharges != nil {
		err := jobs.Time(ctx, rec, jobs.LogCharges, j.now, func() (map[string]int64, error) {
			done, err := j.opts.LogCharges.Run(ctx)
			if done.IngestMicros > 0 || done.StorageMicros > 0 {
				j.log.Info("log charges pass", "organizations", done.Organizations,
					"ingest_micros", done.IngestMicros, "storage_micros", done.StorageMicros)
			}
			return map[string]int64{"organizations": int64(done.Organizations),
				"ingest_micros": done.IngestMicros, "storage_micros": done.StorageMicros}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("log charges: %w", err))
		}
	}

	// And credit expiry last, because it is the one job whose lateness costs nothing: a spend
	// already refuses a lapsed lot, so all this pass moves is the number on a statement.
	if j.opts.Credit != nil {
		err := jobs.Time(ctx, rec, jobs.CreditExpiry, j.now, func() (map[string]int64, error) {
			done, err := j.opts.Credit.Run(ctx, j.now())
			j.stats.lapsed.Add(int64(done))
			if done > 0 {
				j.log.Info("credit expiry pass", "lots_written_off", done)
			}
			return map[string]int64{"lots_written_off": int64(done)}, err
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("credit expiry: %w", err))
		}
	}

	j.stats.runs.Add(1)
	j.stats.lastRun.Store(j.now().Unix())

	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}
