// Package config holds the settings every PlayerSense binary reads at start-up.
package config

import (
	"time"

	"muzak.dev/framework"
)

// Settings is the service configuration, read once at start-up from the environment and
// from a .env file when one is present.
//
// Every connection string carries its own port rather than composing one from a host and
// a port setting, so that the ports declared in .env are the only place a port appears.
type Settings struct {
	// ClickHouseDSN addresses the analytics database (§13).
	ClickHouseDSN string `env:"CLICKHOUSE_DSN" required:"true" secret:"true"`
	// PostgresDSN addresses the control plane (§11).
	PostgresDSN string `env:"POSTGRES_DSN" required:"true" secret:"true"`
	// ValkeyDSN addresses the cache and the device-state store (§12).
	ValkeyDSN string `env:"VALKEY_DSN"`

	// MigrationsDir is the repository-root directory holding both migration sets.
	MigrationsDir string `env:"MIGRATIONS_DIR" default:"db"`

	// AppName titles the API in the generated documentation.
	AppName string `env:"APP_NAME" default:"PlayerSense Control API"`
	// Addr is the address control-api listens on.
	Addr string `env:"CONTROL_API_ADDR" default:":21080"`
	// PublicURL is the address clients actually reach, for the OpenAPI servers list.
	PublicURL string `env:"CONTROL_API_PUBLIC_URL" default:"http://localhost:21080"`

	// AdminToken guards the control API. Marked secret so a malformed value never
	// appears in a start-up error.
	AdminToken string `env:"CONTROL_API_ADMIN_TOKEN" secret:"true" required:"true"`

	// TrustedProxies lists the proxies whose forwarding header is believed, as a
	// comma-separated list of addresses or CIDR prefixes. Empty by default: a
	// forwarding header is written by whatever sent the request, and believing one from
	// an unknown sender hands every client the ability to choose which rate-limit
	// budget it spends (§70).
	TrustedProxies []string `env:"TRUSTED_PROXIES"`

	// --- ingest-api ---------------------------------------------------------

	// IngestAddr is the address ingest-api listens on. Separate from the control API
	// because §10.2 makes it a separate deployable: it scales on a different axis and
	// must keep serving when the control plane cannot.
	IngestAddr      string `env:"INGEST_API_ADDR" default:":21081"`
	IngestPublicURL string `env:"INGEST_API_PUBLIC_URL" default:"http://localhost:21081"`

	// EventBus selects the queue implementation: local or pubsub (§3.6).
	EventBus    string `env:"EVENT_BUS" default:"local"`
	EventBusDir string `env:"EVENT_BUS_DIR" default:".data/eventbus"`

	PubSubProjectID string `env:"PUBSUB_PROJECT_ID"`
	PubSubTopic     string `env:"PUBSUB_TOPIC" default:"analytics-events"`

	// The key cache is what lets ingestion keep accepting batches while the control
	// plane is unreachable (§64). A short negative TTL matters as much as the positive
	// one: a publishable key is assumed discoverable (§71), so wrong keys arrive in
	// volume and none of them should become a query.
	KeyCacheTTL         time.Duration `env:"KEY_CACHE_TTL" default:"5m"`
	KeyCacheNegativeTTL time.Duration `env:"KEY_CACHE_NEGATIVE_TTL" default:"30s"`
	KeyCacheMaxEntries  int           `env:"KEY_CACHE_MAX_ENTRIES" default:"50000"`

	// LogFormat is auto, console, json or none.
	//
	// Auto picks JSON when the output is not a terminal, which is right for a production
	// container and a surprise in front of `docker compose logs` — naming console there
	// is the reason this is configurable rather than inferred. Tests use none.
	LogFormat string `env:"LOG_FORMAT" default:"auto"`

	// ShutdownTimeout bounds the wait for in-flight requests. Kept under the grace
	// period Cloud Run allows after SIGTERM, which is about ten seconds — a drain the
	// platform kills part way through is not a drain.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" default:"8s"`
}

// Load reads the configuration, reporting every problem at once.
func Load(envFiles ...string) (Settings, error) {
	sources := make([]muzak.ConfigOption, 0, len(envFiles))
	for _, f := range envFiles {
		sources = append(sources, muzak.EnvFile(f))
	}
	return muzak.LoadConfig[Settings](sources...)
}

// MustLoad is Load for a program that cannot run without its configuration.
func MustLoad(envFiles ...string) Settings {
	settings, err := Load(envFiles...)
	if err != nil {
		panic(err)
	}
	return settings
}
