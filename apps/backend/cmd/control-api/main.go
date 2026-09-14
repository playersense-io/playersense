// Command control-api serves the control plane (project.md §10.1).
//
// This file does nothing but compose. Every piece of behaviour lives in a package of its
// own — core, schemas, handlers, routers — and the dependencies point one way, so each is
// testable without a server.
package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/valkey"
)

func main() {
	settings := config.MustLoad(filepath.Join(repoRoot(), ".env"))

	if err := build(settings).RunSignals(); err != nil {
		log.Fatal(err)
	}
}

// build composes the application.
//
// It is a function rather than inline in main so that the test suite exercises exactly
// the application that runs, rather than a second one assembled to look like it.
func build(settings config.Settings) *muzak.App {
	// Construction is cheap and dials nothing. Control implements muzak.Lifecycle, so
	// the application opens it before the server accepts traffic and closes it after the
	// server has drained — a request still in flight keeps a usable connection to the
	// very end.
	control := pgdb.New(settings.PostgresDSN)

	// The cache is here so revoking a key can evict it immediately rather than leaving
	// it usable until its entry expires — which is exactly the minutes during which
	// somebody needs it to stop working (§53).
	cache := valkey.New(settings.ValkeyDSN)
	resolver := core.NewKeyResolver(control, cache, core.KeyResolverOptions{
		TTL:         settings.KeyCacheTTL,
		NegativeTTL: settings.KeyCacheNegativeTTL,
		LocalTTL:    settings.KeyCacheLocalTTL,
		MaxLocal:    settings.KeyCacheMaxEntries,
	})

	app := muzak.New(muzak.AppOptions{
		Title:       settings.AppName,
		Version:     "0.1.0",
		Description: "The PlayerSense control plane: organizations, projects, environments and API keys.",
		Servers: []muzak.Server{
			{URL: settings.PublicURL, Description: "this deployment"},
		},
		Tags: []muzak.Tag{
			{Name: "organizations", Description: "The billing and ownership boundary."},
			{Name: "projects", Description: "One game, with its environments."},
			{Name: "api-keys", Description: "Credentials scoped to one environment."},
		},

		Addr: settings.Addr,
		// Under the ten seconds Cloud Run allows after SIGTERM: a drain the platform
		// kills part way through is not a drain.
		ShutdownTimeout: settings.ShutdownTimeout,

		// Nothing is believed from a forwarding header until the proxy that wrote it is
		// named. Without this, every request behind a load balancer is attributed to the
		// balancer and the whole world shares one rate-limit budget.
		ClientIP: muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},

		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},

		// A health check polled every second should not drown out real traffic.
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		// Published by value, and started and stopped by the application because it
		// implements Lifecycle.
		muzak.WithSingleton(control),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),
	)

	// Counted before the guards, so a request a guard rejects still costs the caller its
	// budget. Behind a flag only because the counters are now shared through Valkey: one
	// test suite would otherwise spend the next one's budget.
	if !settings.RateLimitDisabled {
		app.Options(muzak.WithRateLimit(core.RateLimitPolicy(valkey.NewRateLimitStorage(cache))))
	}

	// Operational routes answer without a credential; everything else sits behind the
	// guard. Both decisions are made here rather than inside the routers, which keeps the
	// routers reusable and puts the security decision where a reviewer will look.
	app.Include(routers.Meta())

	api := muzak.NewRouter()
	api.Include(routers.Organizations())
	api.Include(routers.Projects())
	api.Include(routers.Keys())

	app.Include(api,
		muzak.WithPrefix("/v1"),
		muzak.WithDependencies(core.RequireAdminToken(settings)),
		muzak.WithResponseDoc(http.StatusUnauthorized, "No usable credential was presented"),
	)

	return app
}

// logFormat maps the configured name onto the framework's format.
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

// repoRoot walks upward for go.work, so the binary finds .env when run from anywhere in
// the tree during development.
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
