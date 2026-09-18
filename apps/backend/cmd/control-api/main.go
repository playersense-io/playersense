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
	"playersense.dev/backend/internal/firebase"
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
	// The control API speaks only to the global tier (§99): accounts, projects, keys and
	// cells. Anything naming a player lives in a cell's own database, and this service
	// has no connection to one.
	//
	// Construction is cheap and dials nothing. Global implements muzak.Lifecycle, so the
	// application opens it before the server accepts traffic and closes it after the
	// server has drained — a request still in flight keeps a usable connection to the
	// very end.
	ctrl := pgdb.NewGlobal(settings.GlobalPostgresDSN)

	// The cache is here so revoking a key can evict it immediately rather than leaving
	// it usable until its entry expires — which is exactly the minutes during which
	// somebody needs it to stop working (§53).
	//
	// It is a *cell's* cache, and that is a real limit worth naming rather than hiding:
	// the revocation tombstone is written into one cell's keyspace, so with several cells
	// this service needs a client per cell and must evict from all of them. Stage 1 runs
	// one cell (§6), so one client is the whole fleet.
	cache := valkey.New(settings.Cell, settings.ValkeyDSN)

	// Firebase Auth owns authentication; this is the other end of it. PlayerSense holds
	// no credential, so there is no password column to leak and no session store to
	// compromise — and the price is that signing in depends on Google being reachable,
	// which is why the key cache serves what it already has when it is not.
	verifier := firebase.NewVerifier(settings.FirebaseProjectID)
	verifier.Keys.URL = settings.FirebaseCertificateURL
	resolver := core.NewKeyResolver(ctrl, cache, core.KeyResolverOptions{
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
		muzak.WithSingleton(ctrl),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(core.CellFrom(settings)),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),

		// Verifies the ID token on every request and caches Google's signing
		// certificates. Started with the application so that a deployment which cannot
		// reach Google fails while it is starting rather than on somebody's first
		// sign-in.
		muzak.WithSingleton(verifier, muzak.LifecycleFunc("firebase keys", verifier.Start, nil)),
	)

	// Counted before the guards, so a request a guard rejects still costs the caller its
	// budget. Behind a flag only because the counters are now shared through Valkey: one
	// test suite would otherwise spend the next one's budget.
	if !settings.RateLimitDisabled {
		app.Options(muzak.WithRateLimit(core.RateLimitPolicy(valkey.NewRateLimitStorage(cache))))
	}

	// Operational routes answer without a credential; everything else sits behind a
	// signed-in person. Both decisions are made here rather than inside the routers, which
	// keeps the routers reusable and puts the security decision where a reviewer will look.
	app.Include(routers.Meta())

	api := muzak.NewRouter()
	api.Include(routers.Organizations())
	api.Include(routers.Projects())
	api.Include(routers.Keys())

	// A provider, not a guard, and the framework's own documentation says why twice over:
	// per-user credentials want a provider rather than a shared-secret guard, and every
	// guard runs before every provider — so a guard could not read the verifier or the
	// database, both of which are published above as singletons.
	//
	// Declared here, once, so that the permission each route names has a principal to
	// check against. A route inside this subtree that forgets its core.Require still
	// requires a valid sign-in; it simply checks nothing further, which is the right
	// failure direction for the two routes that deliberately need no permission.
	app.Include(api,
		muzak.WithPrefix("/v1"),
		muzak.Needs(core.ResolveUser),
		muzak.WithResponseDoc(http.StatusUnauthorized, "No usable sign-in was presented"),
		muzak.WithResponseDoc(http.StatusForbidden, "This account may not do that"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"A sign-in could not be verified right now; the credential is not necessarily bad"),
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
