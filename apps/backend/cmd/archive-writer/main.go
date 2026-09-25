// Command archive-writer keeps the cold, canonical copy of accepted events (§10.4, §14).
//
// It consumes the same stream as analytics-writer through its own subscription (§20), so
// the two are independent: a slow archive cannot hold storage back, and a broken archive
// cannot lose events storage already took. That independence is the point of the fan-out
// rather than a chain.
//
// It has no API. The HTTP server exists for the health probe.
//
// Unlike analytics-writer it does touch a database — the cell's own, to record manifests
// (§11, §99). That is not a weakening of §64: the manifest *is* the durable side effect, so
// a cell tier outage must stop the archive rather than let it write objects nothing knows
// about. Ingestion and storage keep running either way, which is what §64 actually protects.
package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/archive"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/routers"
)

func main() {
	settings := config.MustLoad(config.EnvFile(repoRoot()))

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
	store, err := newStore(settings)
	if err != nil {
		return nil, err
	}

	db := pgdb.New(settings.PostgresDSN)

	writer := archive.New(bus, store, db, slog.Default(), archive.Options{
		MaxRowsPerObject: settings.ArchiveMaxRowsPerObject,
		MaxAttempts:      settings.WriterMaxAttempts,
	})

	app := muzak.New(muzak.AppOptions{
		Title:       "PlayerSense Archive Writer",
		Version:     "0.1.0",
		Description: "Writes the canonical Parquet archive of accepted events.",
		Servers:     []muzak.Server{{URL: "http://localhost" + settings.ArchiveWriterAddr}},

		Addr: settings.ArchiveWriterAddr,
		// The drain matters: Stop waits for the delivery in flight, so a platform that
		// kills the process part way through gets a redelivery rather than an object with
		// no manifest.
		ShutdownTimeout: settings.ShutdownTimeout,

		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(settings),
		muzak.WithSingleton(bus),
		muzak.WithSingleton(db),
		muzak.WithSingleton(store),

		// Registered after its dependencies so it is stopped before them.
		muzak.WithSingleton(writer),
	)

	app.Include(routers.Meta())
	return app, nil
}

// newBus picks the queue implementation, and must agree with the API's choice.
func newBus(settings config.Settings) (eventbus.Bus, error) {
	switch settings.EventBus {
	case "pubsub":
		if settings.PubSubProjectID == "" {
			return nil, fmt.Errorf("EVENT_BUS=pubsub needs PUBSUB_PROJECT_ID")
		}
		bus := eventbus.NewPubSub(settings.PubSubProjectID, settings.PubSubTopic)
		// Group messages before they reach the handler, sized in rows. One message per
		// delivery is one ClickHouse part per SDK batch, which is what stops a node at
		// §4's ceiling (ADR 0023).
		bus.SetDelivery(settings.BusDeliveryMaxRows,
			settings.BusDeliveryMaxMessages, settings.BusDeliveryMaxWait)
		return bus, nil
	case "local", "":
		bus := eventbus.NewLocal(settings.EventBusDir)
		bus.SetPollInterval(settings.WriterFlushInterval)
		bus.SetLogger(slog.Default())
		return bus, nil
	default:
		return nil, fmt.Errorf("unknown EVENT_BUS %q; want local or pubsub", settings.EventBus)
	}
}

// newStore picks the object store, the same way and for the same reason (§3.6).
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
