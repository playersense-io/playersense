// Command worker runs the scheduled work of §79 (§10.7).
//
// It is the job that turns stored events into served facts. `analytics-writer` fills
// `events_raw`, the materialized views fill `player_lifetime` and `player_activity_daily`
// behind it, and nothing else happens until this runs: every cohort fact and every daily
// metric is rebuilt here. Without it the reporting API answers zero while ingestion is
// perfectly healthy, which is the worst shape a failure can take — a confident number that
// is wrong.
//
// It runs two loops, on two cadences, because §79's list is two kinds of work.
//
//   - The **derivation**, nightly at WORKER_DERIVE_AT. It rebuilds whole months and gains
//     nothing from running twice.
//   - The **janitor**, every WORKER_JANITOR_EVERY: §60's archive expiry, §61's erasures and
//     §101's exports. Each of those is waiting on something — a retention term, a legal
//     deadline, a person — so a nightly cadence would be a day of delay for no benefit.
//
// Two forms, and they run the same code:
//
//	worker        long-running; serves a health probe and runs both loops on their schedules
//	worker once   one pass of both, then exit
//
// `once` is what a Stage 2 deployment uses: §87 moves stateless services to Cloud Run, and
// a Cloud Run Job driven by Cloud Scheduler is a better nightly job than a process holding
// a timer. §79 warns against depending on "a single in-memory cron process for irreversible
// business actions" — every pass here is idempotent, so none of them is one of those, but
// the one-shot form is what removes the question.
//
// §10.7 names other worker roles that do not exist yet: Stripe billing sync, the webhook
// dispatcher, archive compaction, session finalization. None of them is stubbed here. What
// this runs is what is built.
//
// # What it connects to, and what it deliberately does not
//
// Cell-local, entirely. Its ClickHouse, its cell tier, its bucket, its Valkey — everything
// §61 touches names a player, and a player identifier never reaches the global tier (§99).
//
// It holds no global-tier handle at all, which is the same claim analytics-writer makes and
// §64 relies on: a control-plane outage does not stop tonight's facts being built, and it
// does not stop an erasure that a deadline is running against either. The cell tier is a
// different matter — it is where a deletion request *lives*, so the janitor cannot run
// without it, and that is the correct dependency rather than a weakening of §64.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/erasure"
	"playersense.dev/backend/internal/export"
	"playersense.dev/backend/internal/metering"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/retention"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/scheduler"
	"playersense.dev/backend/internal/valkey"
)

func main() {
	settings := config.MustLoad(filepath.Join(repoRoot(), ".env"))

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "once":
			if err := once(settings); err != nil {
				log.Fatal(err)
			}
			return
		default:
			log.Fatalf("unknown command %q; want `worker` or `worker once`", os.Args[1])
		}
	}

	app, err := build(settings)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.RunSignals(); err != nil {
		log.Fatal(err)
	}
}

// once runs a single pass of both loops and exits non-zero if either failed.
//
// No HTTP server and no lifecycle: an external scheduler already knows whether the process
// exited cleanly, which is the only signal it needs.
//
// Both, and their errors joined rather than the first one returned. A derivation that failed
// must not hide an erasure that also failed — the two are unrelated, and the second has a
// deadline.
func once(settings config.Settings) error {
	ctx := context.Background()

	db := chdb.NewSQL(settings.ClickHouseDSN)
	if err := db.Start(ctx); err != nil {
		return err
	}
	defer db.Stop(ctx) //nolint:errcheck // the process is exiting

	deps, err := newDependencies(settings)
	if err != nil {
		return err
	}
	for _, start := range deps.starters() {
		if err := start.Start(ctx); err != nil {
			return err
		}
		defer start.Stop(ctx) //nolint:errcheck // the process is exiting
	}

	sched, err := newScheduler(settings, db, slog.Default())
	if err != nil {
		return err
	}
	janitor, err := newJanitor(settings, db, deps, slog.Default())
	if err != nil {
		return err
	}
	return errors.Join(sched.Run(ctx), janitor.Run(ctx))
}

func build(settings config.Settings) (*muzak.App, error) {
	db := chdb.NewSQL(settings.ClickHouseDSN)

	deps, err := newDependencies(settings)
	if err != nil {
		return nil, err
	}

	sched, err := newScheduler(settings, db, slog.Default())
	if err != nil {
		return nil, err
	}
	janitor, err := newJanitor(settings, db, deps, slog.Default())
	if err != nil {
		return nil, err
	}

	app := muzak.New(muzak.AppOptions{
		Title:   "PlayerSense Worker",
		Version: "0.1.0",
		Description: "Rebuilds the cohort facts and the daily metrics nightly, and runs the " +
			"data-lifecycle jobs — archive expiry, erasures and exports — on an interval.",
		Servers: []muzak.Server{{URL: "http://localhost" + settings.WorkerAddr}},

		Addr: settings.WorkerAddr,
		// The drain lets a pass in flight finish its partition swaps rather than being
		// cut off between two of them.
		ShutdownTimeout: settings.ShutdownTimeout,

		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(settings),
		muzak.WithSingleton(core.CellFrom(settings)),

		// The cell's own tier, its bucket and its cache — everything §61 needs and nothing
		// the global tier can see.
		muzak.WithSingleton(deps.cell),
		muzak.WithSingleton(deps.store),
		muzak.WithSingleton(deps.cache),

		// Registered after the pools they read through, so they are stopped before them.
		muzak.WithSingleton(sched),
		muzak.WithSingleton(janitor),

		// A typo in CELL is otherwise silent: the derivation would rebuild a cell's facts
		// while claiming to be a different cell.
		muzak.WithLifecycle(core.NewCellCheck(settings)),
	)

	app.Include(routers.Meta())
	return app, nil
}

// newScheduler reads the schedule and hands the scheduler its pool.
//
// The pool is passed rather than the handle inside it, because the application opens it
// during start-up — after this runs. The scheduler resolves it per pass.
//
// It holds no control-plane handle, which keeps §64's claim true of the derivation as well
// as of storage: a control-plane outage does not stop tonight's facts being built.
func newScheduler(settings config.Settings, db *chdb.SQL, log *slog.Logger) (*scheduler.Scheduler, error) {
	at, err := scheduler.ParseTimeOfDay(settings.WorkerDeriveAt)
	if err != nil {
		return nil, fmt.Errorf("WORKER_DERIVE_AT: %w", err)
	}
	return scheduler.New(db, log, scheduler.Options{
		At:                at,
		ActivityMonths:    settings.WorkerActivityMonths,
		CohortMonths:      settings.WorkerCohortMonths,
		LifetimeRetention: settings.WorkerLifetimeRetention,
	})
}

// dependencies are the cell-local stores the janitor needs and the derivation does not.
type dependencies struct {
	cell  *pgdb.Cell
	store blobstore.Store
	cache *valkey.Client

	// global is the control plane, for the two billing jobs: §40's usage meter writes a day
	// there and the wallet's expiry sweep writes off lapsed credit. The other three jobs have no
	// business in it, which is why it arrived last and why it is named rather than folded into
	// one "database" field.
	global *pgdb.Global
}

// lifecycle is the part of muzak.Lifecycle `worker once` drives by hand.
type lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// starters lists the dependencies in the order they must be opened.
//
// Only used by `worker once`, which has no application to run the lifecycle for it. The
// long-running form registers them as singletons and the framework does this.
func (d dependencies) starters() []lifecycle {
	out := []lifecycle{d.cell, d.global, d.cache}
	if l, ok := d.store.(lifecycle); ok {
		out = append(out, l)
	}
	return out
}

func newDependencies(settings config.Settings) (dependencies, error) {
	store, err := newStore(settings)
	if err != nil {
		return dependencies{}, err
	}
	return dependencies{
		cell:   pgdb.NewCell(settings.Cell, settings.CellPostgresDSN),
		global: pgdb.NewGlobal(settings.GlobalPostgresDSN),
		store:  store,
		cache:  valkey.New(settings.Cell, settings.ValkeyDSN),
	}, nil
}

// newStore picks the object store, and must agree with archive-writer's choice.
//
// It has to be the same bucket: §60 expires the objects archive-writer wrote, and §61
// rewrites them. A worker pointed at a different store would sweep an empty bucket while the
// real one grew, and would report success doing it.
func newStore(settings config.Settings) (blobstore.Store, error) {
	switch settings.ArchiveStore {
	case "gcs":
		return blobstore.NewGCS(settings.ArchiveBucket), nil
	case "local", "":
		return blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket), nil
	default:
		return nil, fmt.Errorf("unknown ARCHIVE_STORE %q; want local or gcs", settings.ArchiveStore)
	}
}

// newJanitor assembles §79's data-lifecycle jobs.
//
// Each is handed the pools rather than the handles inside them, for the reason newScheduler
// gives: the application opens them after this runs.
func newJanitor(settings config.Settings, db *chdb.SQL, deps dependencies, log *slog.Logger) (*scheduler.Janitor, error) {
	return scheduler.NewJanitor(log, scheduler.JanitorOptions{
		Every: settings.WorkerJanitorEvery,

		Sweeper: &retention.Sweeper{
			Cell:       deps.cell,
			Store:      deps.store,
			Log:        log,
			MaxObjects: settings.RetentionMaxObjects,
		},
		Purger: &erasure.Purger{
			Cell:        deps.cell,
			CH:          db,
			Store:       deps.store,
			Cache:       deps.cache,
			Log:         log,
			DrainWindow: settings.ErasureDrainWindow,
			MaxObjects:  settings.ErasureMaxObjects,
		},
		Exporter: &export.Exporter{
			Cell:    deps.cell,
			CH:      db,
			Store:   deps.store,
			Log:     log,
			TTL:     settings.ExportTTL,
			MaxRows: settings.ExportMaxRows,
		},

		// §40's usage meter and the wallet's expiry sweep, both against the global tier.
		//
		// A cell worker writing to the US-hosted control plane, which §99 permits and the
		// classification check proves: a usage row is counts and nothing else — "41,207
		// players were active" is attributable to nobody — and a credit lot is money. Neither
		// carries a player identifier, which is the rule the two tiers are split under.
		Meter: &metering.Meter{
			Global: deps.global,
			Cell:   deps.cell,
			CH:     db,
			Log:    log,
		},
		Credit: scheduler.CreditExpiry(deps.global, log, settings.CreditExpiryMaxLots),
	})
}

func logFormat(name string) muzak.LogFormat {
	switch name {
	case "console":
		return muzak.LogFormatConsole
	case "json":
		return muzak.LogFormatJSON
	case "none":
		return muzak.LogFormatNone
	default:
		return muzak.LogFormatAuto
	}
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
