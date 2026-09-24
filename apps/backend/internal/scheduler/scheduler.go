// Package scheduler runs the derivations that turn stored events into served facts
// (project.md §79, §79.1).
//
// Everything the reporting API answers from is built here. Until this runs, `events_raw`
// fills, `player_lifetime` and `player_activity_daily` fill behind their materialized
// views, and every cohort fact and daily metric stays empty — so a dashboard reads zero
// while ingestion is perfectly healthy. That is the gap this package closes.
//
// Three properties, all of them §79.1's:
//
//   - **Whole months, never a trailing window.** Each row of a cohort fact belongs to
//     exactly one activity-month partition (§29.8), and REPLACE PARTITION swaps a
//     partition wholesale. A partial rebuild swapped into a whole partition deletes every
//     row it does not contain — which is ADR 0003's bug, found by running it.
//   - **Idempotent.** Running a pass twice produces the same tables, so a retry after a
//     failure is safe and a manual run is safe.
//   - **Late events are absorbed** inside the rebuilt window without double counting,
//     which matters because §19 accepts a skew window and §21 guarantees only
//     at-least-once delivery.
//
// It is cell-local. Every table it touches is its cell's ClickHouse, and it holds no
// handle on the control plane at all — the same shape as analytics-writer (§10.3), and for
// the same reason (§64).
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"playersense.dev/backend/internal/cohort"
	"playersense.dev/backend/internal/rollup"
)

// Options configures the derivation pass and when it runs.
type Options struct {
	// At is the time of day, in UTC, the daily pass starts. §79.1 calls this a nightly
	// job; the hour is a deployment choice rather than a design one.
	At TimeOfDay

	// ActivityMonths is how many trailing activity months are rebuilt, counting the
	// current one.
	//
	// §79.1 asks for "the whole current activity month — and the previous month as well,
	// for the first few days of a new one". Two, unconditionally, is the same guarantee
	// without a date-dependent branch: the previous month stays open all month, which
	// also absorbs a late event arriving on the 20th rather than only on the 2nd. The
	// cost is one extra month-sized scan per night.
	ActivityMonths int

	// MaxBackfillDevices bounds the cohort-key repair (§65). Zero takes the package default.
	MaxBackfillDevices int

	// LifetimeRetention is how long a device may go unseen before player_lifetime forgets
	// it (§33.1). Zero disables the prune.
	//
	// It must exceed the cohort rebuild window, and New refuses a value that does not:
	// DeriveSizes rebuilds CohortMonths of denominators from player_lifetime_v, so pruning
	// a device that still belongs to a republished cohort shrinks its denominator and
	// retention jumps up. That is the mirror of the bug the backfill fixes and just as
	// silent, which is why the coupling is checked rather than written down.
	LifetimeRetention time.Duration

	// CohortMonths is how many trailing cohort months have their denominators rebuilt.
	//
	// This is a different axis from ActivityMonths and the difference is the subtle part
	// of the whole job. One *activity* day contributes cells to every *cohort* month in
	// the preceding year (§29.8), so rebuilding one activity month's numerators touches
	// denominators belonging to thirteen cohort months. Rebuilding fewer than that leaves
	// a numerator whose denominator was computed before the players in it arrived — which
	// is how retention goes above 100%, and it is exactly what §29.9 forbids by requiring
	// the two to be recomputed together.
	//
	// Thirteen covers the 365-day offset the dense set runs to, plus the current month.
	CohortMonths int
}

// TimeOfDay is an hour and minute, in UTC.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseTimeOfDay reads "HH:MM".
//
// A named error rather than a silent default: a service that quietly ran at midnight
// because its schedule did not parse is a service nobody can reason about.
//
// Strict about trailing input, which is not pedantry. Sscanf stops at the last verb it
// matched and reports success with whatever follows still unread, so "02:00:00" — the
// obvious thing to write for two in the morning — parses as 02:00 and says nothing. A
// schedule is exactly the setting where a value that half-parses is worse than one that
// does not parse at all.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	hh, mm, found := strings.Cut(s, ":")
	if !found {
		return TimeOfDay{}, fmt.Errorf("parse %q as HH:MM: no colon", s)
	}
	hour, err := strconv.Atoi(hh)
	if err != nil {
		return TimeOfDay{}, fmt.Errorf("parse %q as HH:MM: hour %q: %w", s, hh, err)
	}
	minute, err := strconv.Atoi(mm)
	if err != nil {
		return TimeOfDay{}, fmt.Errorf("parse %q as HH:MM: minute %q: %w", s, mm, err)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return TimeOfDay{}, fmt.Errorf("%q is not a time of day", s)
	}
	return TimeOfDay{Hour: hour, Minute: minute}, nil
}

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

// next returns the first occurrence of this time of day strictly after now.
func (t TimeOfDay) next(now time.Time) time.Time {
	now = now.UTC()
	at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour, t.Minute, 0, 0, time.UTC)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

type stats struct {
	runs       atomic.Int64
	failures   atomic.Int64
	backfilled atomic.Int64
	pruned     atomic.Int64
	lastRun    atomic.Int64 // unix seconds; 0 until the first pass completes
}

// Pool supplies the ClickHouse handle.
//
// An interface, and resolved per run rather than held, because the application starts its
// lifecycle components after constructing them: the pool does not exist at the moment the
// scheduler is built. Asking for it at run time is also what makes a pass fail with
// "clickhouse is not started" rather than dereferencing a nil somebody forgot to check.
type Pool interface {
	DB() (*sql.DB, error)
}

// Scheduler owns the derivation and the clock that drives it.
type Scheduler struct {
	pool Pool
	log  *slog.Logger
	opts Options

	// now is swappable so a test can drive the clock rather than wait for one.
	now func() time.Time

	stats  stats
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a scheduler, or an error when two settings contradict each other.
func New(pool Pool, log *slog.Logger, opts Options) (*Scheduler, error) {
	if opts.ActivityMonths <= 0 {
		opts.ActivityMonths = 2
	}
	if opts.CohortMonths <= 0 {
		opts.CohortMonths = 13
	}
	if opts.LifetimeRetention > 0 {
		if floor := cohort.MinimumAfter(opts.CohortMonths); opts.LifetimeRetention < floor {
			return nil, fmt.Errorf(
				"WORKER_LIFETIME_RETENTION of %s is shorter than the %d-month cohort rebuild "+
					"window needs (%s): pruning a device whose cohort is still being "+
					"republished shrinks its denominator and retention silently rises",
				opts.LifetimeRetention, opts.CohortMonths, floor)
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		pool: pool,
		log:  log,
		opts: opts,
		now:  func() time.Time { return time.Now().UTC() },
		done: make(chan struct{}),
	}, nil
}

// Name identifies the component in start-up and shutdown logs.
func (s *Scheduler) Name() string { return "scheduler" }

// Start begins the daily loop, in the background.
//
// It returns once the loop is running rather than blocking on it, for the reason
// analytics-writer's Start gives: the application starts its lifecycle components before
// opening the health socket, and a Start that never returns is a service that never
// becomes ready.
//
// Deliberately no run on start-up. The pass is idempotent, so an extra one is harmless in
// principle — but a process that crash-loops would then rebuild every partition on every
// restart, which turns a small problem into a ClickHouse one. Use `worker once` for a
// deliberate immediate run.
func (s *Scheduler) Start(context.Context) error {
	// Not derived from the start-up context: that one is cancelled once start-up
	// finishes, and a loop tied to it would stop the moment the service was ready.
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go func() {
		defer close(s.done)
		s.loop(ctx)
	}()

	s.log.Info("scheduler started",
		"at", s.opts.At.String(),
		"activity_months", s.opts.ActivityMonths,
		"cohort_months", s.opts.CohortMonths,
		"next_run", s.opts.At.next(s.now()).Format(time.RFC3339))
	return nil
}

// Stop ends the loop and waits for a pass in flight.
//
// Waiting is deliberate, though for a weaker reason than the writer's: a pass cut off
// between two REPLACE PARTITION swaps leaves a month whose numerators have been rebuilt
// and whose buckets have not. That is self-correcting on the next run — the pass is
// idempotent and rebuilds both — but finishing is still better than being interrupted, and
// the shutdown timeout bounds the wait either way.
func (s *Scheduler) Stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
		return fmt.Errorf("a derivation was still running at the shutdown deadline")
	}
	s.log.Info("scheduler stopped",
		"runs", s.stats.runs.Load(), "failures", s.stats.failures.Load())
	return nil
}

// Stats reports what the scheduler has done, for the health endpoint and for tests.
func (s *Scheduler) Stats() map[string]int64 {
	return map[string]int64{
		"runs":     s.stats.runs.Load(),
		"failures": s.stats.failures.Load(),
		"last_run": s.stats.lastRun.Load(),
		// Normally zero. A non-zero value means Valkey was unavailable while events were
		// arriving, and says how much the repair had to touch (§65, §67).
		"backfilled_devices": s.stats.backfilled.Load(),
		"pruned_devices":     s.stats.pruned.Load(),
	}
}

func (s *Scheduler) loop(ctx context.Context) {
	for {
		wait := time.Until(s.opts.At.next(s.now()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := s.Run(ctx); err != nil {
			// Logged and swallowed rather than returned: a derivation that failed
			// tonight must not stop the one that would succeed tomorrow, and the pass is
			// idempotent so the retry is the next run. §67 asks for the failure to be
			// visible, which is what the counter and this line are for.
			s.stats.failures.Add(1)
			s.log.Error("the derivation failed", "error", err)
		}
	}
}

// Run executes one complete pass.
//
// Exported so `worker once` and the tests drive exactly what the timer drives. A scheduled
// job whose manual form takes a different path is a job whose manual form is untested.
func (s *Scheduler) Run(ctx context.Context) error {
	started := s.now()
	asOf := started

	db, err := s.pool.DB()
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	cohorts := &cohort.Deriver{DB: db}
	rollups := &rollup.Deriver{DB: db}

	activity := trailingMonths(started, s.opts.ActivityMonths)
	cohortWindow := trailingMonths(started, s.opts.CohortMonths)

	s.log.Info("derivation started",
		"activity_months", months(activity), "cohort_months", months(cohortWindow))

	// Denominators first, and the order is a correctness decision rather than a
	// preference.
	//
	// A cohort's size only ever grows: a device's first_seen is min(ingest_time) and can
	// only move earlier, so rebuilding sizes before numerators means every numerator is
	// divided by a denominator at least as fresh as itself. The other order admits the
	// failure cohortcheck's check 7 exists to catch — returned_players above cohort_size,
	// which renders as retention over 100% — because a returner counted tonight can
	// belong to a cohort whose size was last computed before they arrived.
	//
	// §29.9 asks for the two to be published "from the same snapshot in the same atomic
	// swap". Across two partition keys that is not literally available, so this is the
	// strongest form that is: the same pass, denominators leading.
	for _, m := range cohortWindow {
		if err := cohorts.DeriveSizes(ctx, m, asOf); err != nil {
			return fmt.Errorf("cohort sizes for %s: %w", m, err)
		}
	}

	// Then the facts, one activity month at a time, each month's three tables together.
	for _, m := range activity {
		// Before anything reads the month. §65 promises that losing Valkey costs speed
		// rather than data, and this is the half of that promise that does the work: a
		// device-day the writer could not stamp is resolved from player_lifetime, which
		// never needed Valkey. Without it the derivation drops those rows while
		// cohort_sizes still counts the players, and retention reads low with no error
		// anywhere.
		backfill := &cohort.Backfill{Deriver: cohorts, MaxDevices: s.opts.MaxBackfillDevices}
		repaired, err := backfill.Run(ctx, m)
		if err != nil {
			return fmt.Errorf("backfill cohort keys for %s: %w", m, err)
		}
		if repaired.DeviceDays > 0 {
			s.stats.backfilled.Add(int64(repaired.Devices))
			s.log.Warn("repaired device-days the writer could not stamp",
				"month", m.String(),
				"device_days", repaired.DeviceDays,
				"devices", repaired.Devices,
				"cohorts", repaired.Cohorts,
				// Not necessarily a bug: a device whose first sighting was itself
				// unstamped has no arrival fact to resolve against.
				"unresolvable", repaired.Remaining)
		}

		if err := cohorts.DeriveMonth(ctx, m); err != nil {
			return fmt.Errorf("cohort metrics for %s: %w", m, err)
		}
		if err := cohorts.DeriveBuckets(ctx, m); err != nil {
			return fmt.Errorf("cohort buckets for %s: %w", m, err)
		}
		if err := rollups.DeriveMonth(ctx, m); err != nil {
			return fmt.Errorf("daily metrics for %s: %w", m, err)
		}
		if err := rollups.DeriveSessions(ctx, m); err != nil {
			return fmt.Errorf("daily sessions for %s: %w", m, err)
		}
	}

	// Last, and after the derivations rather than before them. A device pruned now was
	// still present for everything above, so a pass is internally consistent even on the
	// night a cohort ages out of the rebuild window.
	if s.opts.LifetimeRetention > 0 {
		prune := &cohort.Prune{Deriver: cohorts, After: s.opts.LifetimeRetention}
		pruned, err := prune.Run(ctx, started)
		if err != nil {
			return fmt.Errorf("prune player_lifetime: %w", err)
		}
		if pruned.Deleted > 0 {
			s.stats.pruned.Add(int64(pruned.Deleted))
			s.log.Info("forgot devices that have not been seen in a long time",
				"devices", pruned.Deleted, "unseen_for", s.opts.LifetimeRetention.String())
		}
	}

	s.stats.runs.Add(1)
	s.stats.lastRun.Store(s.now().Unix())
	s.log.Info("derivation finished", "duration", s.now().Sub(started).String())
	return nil
}

// trailingMonths returns n months ending at the one containing t, oldest first.
//
// Oldest first so a pass that fails part way through has rebuilt the older months rather
// than the newer ones. The newest month is the one most likely to be rebuilt again
// tomorrow anyway; an older month left stale could stay stale for as long as the window.
//
// The anchor is the first of the month, and that is not tidiness. AddDate normalises an
// overflowing day rather than clamping it, so subtracting a month from the 31st of March
// gives the 3rd of March — the same month it started in. A job running on the 29th, 30th
// or 31st would silently rebuild the current month twice and never rebuild the previous
// one, which is the failure ADR 0003 describes arriving through the calendar instead of
// through the partition key.
func trailingMonths(t time.Time, n int) []cohort.Month {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]cohort.Month, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, cohort.MonthOf(first.AddDate(0, -i, 0)))
	}
	return out
}

// months renders a window for a log line.
func months(ms []cohort.Month) string {
	if len(ms) == 0 {
		return ""
	}
	if len(ms) == 1 {
		return ms[0].String()
	}
	return ms[0].String() + ".." + ms[len(ms)-1].String()
}
