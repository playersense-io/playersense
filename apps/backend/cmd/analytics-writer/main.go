// Command analytics-writer stores accepted batches in its cell's ClickHouse (§10.3).
//
// It is the second half of the ingestion path and deliberately separate from the first:
// ingest-api answers HTTP and must stay fast and available, this consumes a queue and must
// stay correct. They scale on different axes and fail in different ways, which is §10.2's
// argument for two deployables rather than one.
//
// It has no API. The HTTP server exists for the health probe, because a platform that
// cannot see a process is a platform that will restart it at the wrong moment.
//
// It never touches the control plane. The tenant travels on the message, which is what
// lets storage keep working through a control-plane outage rather than only ingestion
// (§64).
package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/valkey"
	"playersense.dev/backend/internal/writer"
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

	// The writer has no PostgreSQL handle — not even read-only — which is what makes
	// §64's claim about storage true rather than hopeful.
	cache := valkey.New(settings.ValkeyDSN)
	ch := chdb.New(settings.ClickHouseDSN)

	logger := slog.Default()
	consumer := writer.New(bus, cache, ch, logger, writer.Options{
		MaxRowsPerInsert: settings.WriterBatchSize,
		DeviceStateTTL:   settings.WriterDeviceStateTTL,
		MaxAttempts:      settings.WriterMaxAttempts,
	})

	app := muzak.New(muzak.AppOptions{
		Title:       "PlayerSense Analytics Writer",
		Version:     "0.1.0",
		Description: "Consumes the event queue and stores batches in ClickHouse.",
		Servers:     []muzak.Server{{URL: "http://localhost" + settings.AnalyticsWriterAddr}},

		Addr: settings.AnalyticsWriterAddr,
		// The drain matters more here than in a request service: Stop waits for the
		// message in flight, and a platform that kills the process part way through gets
		// a redelivery rather than a clean hand-off.
		ShutdownTimeout: settings.ShutdownTimeout,

		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(cache),
		muzak.WithSingleton(ch),
		muzak.WithSingleton(settings),
		muzak.WithSingleton(bus),

		// Registered after its dependencies are published so it is stopped before them —
		// components start in parallel but the writer dials nothing in Start, so there is
		// no ordering to get wrong there.
		muzak.WithSingleton(consumer),
	)

	app.Include(routers.Meta())
	return app, nil
}

// newBus picks the queue implementation, and is the same choice the API makes. The two
// must agree or the writer subscribes to something nothing is publishing to.
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
