// Command billing-writer charges tracked players as they arrive (ADR 0039).
//
// A third consumer of the event queue, beside analytics-writer and archive-writer, with its own
// position. It counts which players are new this month, queues what they cost, and every few
// seconds drains each account's queue into its wallet.
//
// It is its own deployable for the reason the other two are: it fails differently. It needs
// PostgreSQL, which analytics-writer deliberately does not (§64), and a billing outage must never be
// the reason a batch is not stored — nor a ClickHouse outage the reason a charge is late. And
// ingestion never waits for it: the SDK's batch was answered when it reached the queue.
//
// It has no API. The HTTP server exists for the health probe.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/staffnotify"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/valkey"
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
	// PlayerSense's own Slack: what this process logs at ERROR. First, because it replaces the
	// default logger and everything below is handed the default.
	staffAlerts, staffLogger, err := staffnotify.ForProcess(settings, "billing-writer", muzak.LoggerOptions{Format: logFormat(settings.LogFormat)})
	if err != nil {
		return nil, err
	}

	// Refused at start-up rather than defaulted. A digest under a guessable key is one anybody
	// holding the table can join back to the events, and a key that changes between restarts
	// makes every player of the month new again — so there is no safe default to fall back on.
	if len(settings.BillingDigestKey) < 32 {
		return nil, fmt.Errorf("BILLING_DIGEST_KEY must be at least 32 bytes; generate one with `openssl rand -hex 32`")
	}

	bus, err := newBus(settings)
	if err != nil {
		return nil, err
	}
	db := pgdb.New(settings.PostgresDSN)
	cache := valkey.New(settings.ValkeyDSN)
	logger := slog.Default()

	service := &charging.Service{
		DB: db, Cache: cache,
		Bus: bus,
		Accruer: &charging.Accruer{
			DB: db, Cache: cache, Bus: bus, Log: logger,
			Key:       []byte(settings.BillingDigestKey),
			MaxInsert: settings.BillingMaxInsert,
		},
		Drainer: &charging.Drainer{
			DB: db, Log: logger,
			MaxAccounts: settings.BillingDrainMaxAccounts,
			SettleAfter: settings.BillingSettleAfter,
			SettleCap:   settings.BillingSettleCap,
		},
		Pruner:          &charging.Pruner{DB: db},
		TopUp:           topUp(settings, db, logger),
		TopUpEvery:      settings.BillingAutoTopUpEvery,
		TopUpSweepEvery: settings.BillingAutoTopUpSweepEvery,
		Suspend: &suspension.Job{DB: db, Cache: cache, Log: logger,
			Grace:        settings.BillingGracePeriod,
			OverdueGrace: settings.BillingInvoiceOverdueGrace,
			Policy:       suspension.Policy{Floor: settings.BillingDefaultCreditLimit, Percent: settings.BillingCreditLimitPercent}},
		SuspendEvery: settings.BillingSuspensionEvery,
		Invoice:      invoices(settings, db, logger),
		InvoiceEvery: settings.BillingInvoiceEvery,
		Log:          logger,
		DrainEvery:   settings.BillingDrainEvery,
		PruneEvery:   settings.BillingPruneEvery,
	}

	app := muzak.New(muzak.AppOptions{
		Title:       "PlayerSense Billing Writer",
		Version:     "0.1.0",
		Description: "Charges tracked players as they arrive and drains the charges into wallets.",
		Servers:     []muzak.Server{{URL: "http://localhost" + settings.BillingWriterAddr}},

		Addr:            settings.BillingWriterAddr,
		ShutdownTimeout: settings.ShutdownTimeout,

		Logger:        staffLogger, // nil unless STAFF_SLACK_ALERTS_URL is set
		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	},
		muzak.WithSingleton(settings),
		muzak.WithSingleton(&staffnotify.Running{Alerts: staffAlerts}),
		muzak.WithSingleton(bus),

		// The service opens PostgreSQL and Valkey itself, in order, before its loops start —
		// the framework starts components in parallel, and a drain that ran before the pool
		// existed dereferenced a nil client (see charging.Service).
		muzak.WithSingleton(service),
	)

	app.Include(routers.Meta())
	return app, nil
}

// topUp is automatic top-up, or nothing when there is no Stripe key to charge with.
//
// Nil rather than a loop that fails every pass: a development stack with no Stripe account is a
// normal thing to run, and thirty seconds of "not configured" warnings forever is noise.
func topUp(settings config.Settings, db *pgdb.DB, log *slog.Logger) interface {
	Run(context.Context) (int, error)
	Sweep(context.Context) (int, error)
} {
	if settings.StripeSecretKey == "" {
		log.Warn("STRIPE_SECRET_KEY is not set; automatic top-up is off")
		return nil
	}
	return &payments.AutoTopUp{
		DB: db, Gateway: payments.NewStripe(settings.StripeSecretKey), Log: log,
		RetryAfter: settings.BillingAutoTopUpRetryAfter,
	}
}

// invoices is the month close and the invoices it issues (ADR 0045), or nothing without a Stripe key
// to issue them with — for the reason topUp gives. With no key an invoiced account's months simply
// wait, and are closed once one is configured.
func invoices(settings config.Settings, db *pgdb.DB, log *slog.Logger) interface {
	Run(context.Context) (invoicing.Result, error)
} {
	if settings.StripeSecretKey == "" {
		log.Warn("STRIPE_SECRET_KEY is not set; invoiced accounts' months are not closed")
		return nil
	}
	return &invoicing.Job{
		DB: db, Gateway: payments.NewStripe(settings.StripeSecretKey), Log: log,
		CloseAfter: settings.BillingInvoiceCloseAfter,
	}
}

// newBus is the same choice every consumer makes; they must agree or this one subscribes to
// something nothing publishes to.
func newBus(settings config.Settings) (eventbus.Bus, error) {
	switch settings.EventBus {
	case "pubsub":
		if settings.PubSubProjectID == "" {
			return nil, fmt.Errorf("EVENT_BUS=pubsub needs PUBSUB_PROJECT_ID")
		}
		bus := eventbus.NewPubSub(settings.PubSubProjectID, settings.PubSubTopic)
		// Grouped deliveries are what make the charger cheap: one INSERT per account per
		// delivery rather than per batch.
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
