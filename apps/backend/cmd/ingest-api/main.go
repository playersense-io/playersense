// Command ingest-api accepts SDK batches (project.md §10.2).
//
// It is the only service on the high-volume path and deliberately the smallest. Its whole
// job is to accept a valid batch, durably enqueue it and return quickly: it runs no
// dashboards, calculates no retention, calls no webhooks and never waits for ClickHouse
// (§3.2).
package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/transport"
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
	bus, err := newBus(settings)
	if err != nil {
		return nil, err
	}

	// The global tier is here only to fill the key cache. Ingestion never queries it per
	// request, which is what lets a batch still be accepted while PostgreSQL is down
	// (§64). It is also the only control-plane connection this service has: the gateway
	// writes nothing to any database, it enqueues.
	ctrl := pgdb.NewGlobal(settings.GlobalPostgresDSN)

	// Cell-scoped, so a misrouted read is a miss rather than one cell silently serving
	// another cell's credential or spending its rate-limit budget (§6).
	cache := valkey.New(settings.Cell, settings.ValkeyDSN)
	resolver := core.NewKeyResolver(ctrl, cache, core.KeyResolverOptions{
		TTL:         settings.KeyCacheTTL,
		NegativeTTL: settings.KeyCacheNegativeTTL,
		LocalTTL:    settings.KeyCacheLocalTTL,
		MaxLocal:    settings.KeyCacheMaxEntries,
	})

	app := muzak.New(muzak.AppOptions{
		Title:       "PlayerSense Ingestion API",
		Version:     "0.1.0",
		Description: "The SDK ingestion endpoint. Accepts batches and durably enqueues them.",
		Servers:     []muzak.Server{{URL: settings.IngestPublicURL, Description: "this deployment"}},
		Tags:        []muzak.Tag{{Name: "ingestion", Description: "The high-volume SDK path."}},

		Addr:            settings.IngestAddr,
		ShutdownTimeout: settings.ShutdownTimeout,

		// Bounded here as well as per route, so an oversized body is refused while it is
		// being read rather than buffered first.
		MaxBodySize: 4 << 20,

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

		// The rate limiter, counted in the handler rather than by the framework.
		//
		// The framework's own limiter runs before any dependency resolves, so it can only
		// key on the client's address — and an address is not a player. Carrier-grade NAT
		// puts tens of thousands of subscribers behind one IPv4, so a budget keyed on that
		// throttles a country for one bad actor; and because its key carries no project, it
		// let one studio's hit game throttle another studio's players. Both were true of
		// this service until this commit.
		//
		// Fails **open**: §65's rule is that losing Valkey costs speed rather than data, and
		// refusing every batch in the product because the limiter is unreachable would turn
		// a degraded cache into an outage of the thing customers pay for. See
		// docs/rate-limits.md.
		//
		// Nil when limits are disabled, which is how one test run's counters stop spending
		// the next one's budget. The handler resolves it with TryFrom and counts nothing
		// when it is absent.
		muzak.WithSingleton(ratelimit.New(
			valkey.NewRateLimitStorage(cache), true, !settings.RateLimitDisabled)),

		muzak.WithSingleton(ingest.DefaultLimits()),

		// Which cell this gateway is. The handler compares it with the cell the key
		// belongs to and refuses the mismatch (§6, §99) — one wrong hostname in an SDK
		// config would otherwise put one region's player data on another's disks.
		muzak.WithSingleton(core.CellFrom(settings)),

		// And a start-up check that the slug names a real cell, so a typo fails to boot
		// rather than writing somewhere nobody is reading.
		muzak.WithLifecycle(core.NewCellCheck(settings)),

		// Published as the interface, so no handler can depend on which implementation is
		// running — §3.6's whole point. Both implementations satisfy muzak.Lifecycle, so
		// the application opens and closes the bus around the server's own life.
		muzak.WithSingleton(bus),

		// Counted per address prefix and before the guards, so an unauthenticated flood
		// costs the sender its budget rather than costing a key lookup each time (§71).
		// An IPv6 /64 is the block most providers hand out, so keying on the exact
		// address would give one client a fresh budget on every request.
	)

	// Before anything binds a body, because §19.1's "maximum decompressed bytes" is the
	// route's own body limit and a limit applied after decoding is not a limit (§17, §19).
	//
	// A request with no Content-Encoding passes straight through, so the uncompressed path
	// pays one header lookup.
	app.Use(transport.Decompress(transport.Options{
		MaxCompressedBytes: settings.IngestMaxCompressedBytes,
	}))

	app.Include(routers.Meta())
	app.Include(routers.Ingest())

	return app, nil
}

// newBus picks the queue implementation.
//
// The return type is the interface, so the application is composed against §3.6's
// abstraction rather than against a product. Swapping local for Pub/Sub is a setting.
func newBus(settings config.Settings) (eventbus.Bus, error) {
	switch settings.EventBus {
	case "pubsub":
		if settings.PubSubProjectID == "" {
			return nil, fmt.Errorf("EVENT_BUS=pubsub needs PUBSUB_PROJECT_ID")
		}
		return eventbus.NewPubSub(settings.PubSubProjectID, settings.PubSubTopic), nil
	case "local", "":
		// Durable on disk and fsynced before the gateway answers, so a 202 means locally
		// what it means in production. It is not a queue — there is no subscription or
		// redelivery — but the publish side of §19 behaves identically, which is what
		// makes this endpoint exercisable without a cloud dependency.
		return eventbus.NewLocal(settings.Cell, settings.EventBusDir), nil
	default:
		return nil, fmt.Errorf("unknown EVENT_BUS %q; want local or pubsub", settings.EventBus)
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
