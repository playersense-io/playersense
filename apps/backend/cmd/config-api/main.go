// Command config-api tells a running game what this player gets (project.md §41, §42).
//
// It is the second read path and the only one a player's device talks to. A game sends its
// installation, its app version and whatever the studio segments on; it receives resolved
// values and concrete windows. It never receives a rule — ADR 0027: an audience that reaches
// a modified build is an audience that build can satisfy.
//
// Cell-local, and for the same reason query-api is. Answering needs the player's own history
// when any rule in the project asks for it, and that history lives in the cell that produced
// it (§99). A project key belonging to another cell is refused with 421 and the address that
// can serve it, rather than answered with the defaults — a game silently running on
// fallbacks is worse than one told where to ask.
//
// # What it does not do
//
// It does not compile anything. Publishing is control-api's, where a person with
// `liveops.publish` presses the button; this reads the result. The split is what keeps the
// path a player waits on free of the work a studio does once a week.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/geoip"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/ratelimit"
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
	// The global tier, for two things and nothing else: the key cache, and the published
	// document. Both are read-mostly and both are cached, so a control-plane outage
	// degrades new keys and new publishes rather than the answers a running game depends
	// on (§64).
	ctrl := pgdb.NewGlobal(settings.GlobalPostgresDSN)

	cache := valkey.New(settings.Cell, settings.ValkeyDSN)
	resolver := core.NewKeyResolver(ctrl, cache, core.KeyResolverOptions{
		TTL:         settings.KeyCacheTTL,
		NegativeTTL: settings.KeyCacheNegativeTTL,
		LocalTTL:    settings.KeyCacheLocalTTL,
		MaxLocal:    settings.KeyCacheMaxEntries,
	})

	store := liveconfig.NewStore(func() *global.Client { return ctrl.Client }, settings.ConfigCacheTTL)

	// The player's own history, read through Valkey with ClickHouse behind it — and only
	// for the minority of projects whose rules name `commerce.` or `activity.`. The
	// compiled document carries that decision, so a project without such a rule performs
	// no lookup at all.
	ch := chdb.NewSQL(settings.ClickHouseDSN)
	playerFacts := facts.NewCache(cache, facts.NewClickHouse(ch),
		settings.FactsTTL, settings.FactsAbsentTTL)

	// Geography and network, derived from the request's address. Optional: a deployment
	// without the databases resolves nothing and every `geo.*` rule falls through to
	// NotExists, which is a smaller failure than refusing to answer.
	geo, err := geoip.Open(settings.GeoIPLocationDB, settings.GeoIPPrivacyDB)
	if err != nil {
		return nil, fmt.Errorf("open the geoip databases: %w", err)
	}

	app := muzak.New(muzak.AppOptions{
		Title:   "PlayerSense Config API",
		Version: "0.1.0",
		Description: "Resolves feature flags and LiveOps events for one player. Returns " +
			"values and windows, never the rules behind them.",
		Servers: []muzak.Server{{URL: settings.ConfigAPIPublicURL, Description: "this deployment"}},
		Tags: []muzak.Tag{
			{Name: "config", Description: "What a player gets."},
		},

		Addr:            settings.ConfigAPIAddr,
		ShutdownTimeout: settings.ShutdownTimeout,

		// A config request is an installation, an app version and a handful of context
		// values. The route bounds itself too; this is the outer wall.
		MaxBodySize: 32 << 10,

		ClientIP:      muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},
		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(ctrl),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(ch),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),

		// Counted in the handler rather than by the framework, for the reason
		// docs/rate-limits.md gives: the framework's limiter runs before any dependency
		// resolves and can only key on the address, which behind carrier NAT is tens of
		// thousands of players sharing one budget.
		//
		// Fails open. A game that cannot read its configuration falls back to the values it
		// already has; a game refused because Valkey was unreachable falls back for no
		// reason at all.
		//
		// Nil when limits are disabled, for the reason ingest-api gives.
		muzak.WithSingleton(ratelimit.New(
			valkey.NewRateLimitStorage(cache), true, !settings.RateLimitDisabled)),
		muzak.WithSingleton(store),
		muzak.WithSingleton(playerFacts),
		muzak.WithSingleton(geo, muzak.LifecycleFunc("geoip", nil,
			func(context.Context) error { geo.Close(); return nil })),

		// Which cell this process serves, and a start-up check that the slug names a real
		// one — so a typo fails to boot rather than serving fallbacks to a live game.
		muzak.WithSingleton(core.CellFrom(settings)),
		muzak.WithLifecycle(core.NewCellCheck(settings)),
	)

	app.Include(routers.Meta())
	app.Include(routers.Config())

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

// repoRoot walks up until it finds the module, so the binary runs from anywhere.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Dir(filepath.Dir(dir))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
