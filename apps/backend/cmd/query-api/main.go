// Command query-api answers analytics questions (project.md §10.5).
//
// It is the first read path in the product: everything before it writes. A dashboard sends
// a logical query — §57's typed DSL — and this compiles it to ClickHouse SQL under a
// tenant scope taken from the credential. The browser never receives ClickHouse credentials
// and never submits SQL.
//
// Cell-local, like the writer. It reads its own cell's ClickHouse and holds a global-tier
// connection for one purpose only: filling the key cache. A reporting key belonging to
// another cell is refused with 421 and the address that can serve it, because this cell
// holds none of that project's data and "no rows" would read as "no players".
package main

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/valkey"
)

func main() {
	settings := config.MustLoad(filepath.Join(repoRoot(), ".env"))

	app, err := build(settings)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.RunSignals(); err != nil {
		log.Fatal(err)
	}
}

func build(settings config.Settings) (*muzak.App, error) {
	// The global tier fills the key cache and is touched by nothing else. Reporting is not
	// on the ingestion hot path, so it could afford to query the control plane per
	// request — it does not, because the three-tier resolver already exists and because a
	// control-plane outage should degrade one thing rather than two (§64).
	ctrl := pgdb.NewGlobal(settings.GlobalPostgresDSN)

	cache := valkey.New(settings.Cell, settings.ValkeyDSN)
	resolver := core.NewKeyResolver(ctrl, cache, core.KeyResolverOptions{
		TTL:         settings.KeyCacheTTL,
		NegativeTTL: settings.KeyCacheNegativeTTL,
		LocalTTL:    settings.KeyCacheLocalTTL,
		MaxLocal:    settings.KeyCacheMaxEntries,
	})

	ch := chdb.New(settings.ClickHouseDSN)
	engine := query.NewEngine(ch, query.Limits{
		MaxRows:          settings.QueryMaxRows,
		MaxExecutionTime: settings.QueryMaxExecutionTime,
		MaxRowsToRead:    settings.QueryMaxRowsToRead,
		MaxBytesToRead:   settings.QueryMaxBytesToRead,
	}, settings.QuerySuppressionK).
		WithCache(query.NewCache(cache, settings.QueryCacheTTL, settings.QueryCacheSettledTTL))

	app := muzak.New(muzak.AppOptions{
		Title:       "PlayerSense Reporting API",
		Version:     "0.1.0",
		Description: "Answers cohort questions from pre-aggregated facts. Never joins on a player key.",
		Servers:     []muzak.Server{{URL: settings.QueryAPIPublicURL, Description: "this deployment"}},
		Tags:        []muzak.Tag{{Name: "reporting", Description: "Reading analytics."}},

		Addr:            settings.QueryAPIAddr,
		ShutdownTimeout: settings.ShutdownTimeout,

		// A cohort query is a small document (§57.1). Anything larger is not one.
		MaxBodySize: 64 << 10,

		ClientIP:      muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},
		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(ctrl),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),
		muzak.WithSingleton(engine),

		// Registered as a lifecycle as well as a singleton: the engine holds it but does
		// not own its connection, and the application opens it before traffic and closes
		// it after draining.
		muzak.WithSingleton(ch),

		// Which cell this process serves. The handler compares it with the cell the key
		// belongs to and refuses the mismatch (§6, §99).
		muzak.WithSingleton(core.CellFrom(settings)),

		// And a start-up check that the slug names a real cell, so a typo fails to boot
		// rather than serving empty answers from a ClickHouse nobody writes to.
		muzak.WithLifecycle(core.NewCellCheck(settings)),
	)

	if !settings.RateLimitDisabled {
		// A cohort query is far more expensive than a batch insert, so the budget is much
		// smaller than ingestion's and shared across instances through Valkey. §32 asks for
		// per-organisation concurrency quotas too; this is the per-caller half.
		app.Options(muzak.WithRateLimit(muzak.RateLimitOptions{
			Storage: valkey.NewRateLimitStorage(cache),
			Tracker: muzak.IPPrefixTracker(32, 64),
			Quotas: []muzak.Quota{
				{Name: "query-burst", Window: time.Second, Limit: 10},
				{Name: "query-sustained", Window: time.Minute, Limit: 300},
			},
		}))
	}

	app.Include(routers.Meta())
	app.Include(routers.Cohorts())

	return app, nil
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
