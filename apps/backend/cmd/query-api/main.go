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
//
// It also carries §101's data-subject surface — erasure and export — and that is not a
// second responsibility bolted on. Those requests name a player, so §99 forbids them
// reaching the US-hosted control plane, and this is the cell-local API a studio's backend
// already talks to. They take a server key rather than a reporting one (§52): reading a
// studio's aggregates and erasing one of its players are not the same privilege.
package main

import (
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

	// The cell's own tier and its bucket, for §101's requests: the deletion and export
	// records live in the region that holds the data, and the export bundle is written to
	// and served from the same bucket the archive uses (§99, §03).
	cell := pgdb.NewCell(settings.Cell, settings.CellPostgresDSN)
	store, err := newStore(settings)
	if err != nil {
		return nil, err
	}

	ch := chdb.New(settings.ClickHouseDSN)
	engine := query.NewEngine(ch, query.Limits{
		MaxRows:          settings.QueryMaxRows,
		MaxExecutionTime: settings.QueryMaxExecutionTime,
		MaxRowsToRead:    settings.QueryMaxRowsToRead,
		MaxBytesToRead:   settings.QueryMaxBytesToRead,
	}, settings.QuerySuppressionK).
		WithCache(query.NewCache(cache, settings.QueryCacheTTL, settings.QueryCacheSettledTTL))

	app := muzak.New(muzak.AppOptions{
		Title:   "PlayerSense Reporting API",
		Version: "0.1.0",
		Description: "Answers cohort questions from pre-aggregated facts and never joins on a " +
			"player key, and carries this cell's data-subject requests.",
		Servers: []muzak.Server{{URL: settings.QueryAPIPublicURL, Description: "this deployment"}},
		Tags: []muzak.Tag{
			{Name: "reporting", Description: "Reading analytics."},
			{Name: "privacy", Description: "Erasing and exporting one player's data."},
		},

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
		muzak.WithSingleton(cell),
		muzak.WithSingleton(store),

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

	// No rate limit, deliberately.
	//
	// §32's protections here are per *query* rather than per caller — MaxRows,
	// MaxExecutionTime, MaxRowsToRead, MaxBytesToRead, the refusal to join on an actor key —
	// and they are the ones that matter: a bounded query cannot be expensive, so a great many
	// of them cannot be catastrophic. A request limit on top of that would only ever
	// inconvenience a real dashboard, which is the one caller this service has.
	//
	// The data-subject surface below takes a server key and has no request rate limit
	// either. See docs/rate-limits.md.

	app.Include(routers.Meta())
	app.Include(routers.Cohorts())
	app.Include(routers.Privacy())

	return app, nil
}

// newStore picks the object store, and must agree with archive-writer's and the worker's
// choice: the export bundle this service serves is the one the worker wrote.
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
