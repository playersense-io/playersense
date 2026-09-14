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
	// Cell is which data plane this process belongs to (§6).
	//
	// Every stateful thing below except the global tier belongs to this cell, and the
	// slug prefixes every Valkey key and every ClickHouse replica path. A service
	// asserts at start-up that the cell exists in the registry and is active, so a typo
	// fails to boot rather than writing somewhere unexpected.
	Cell string `env:"CELL" default:"eu-1"`

	// CellRegion is the jurisdiction the cell sits in — the legal boundary, as distinct
	// from the cell, which is a stack inside it (§6).
	CellRegion string `env:"CELL_REGION" default:"eu"`

	// CellIngestURL is the address SDKs for this cell's projects must send batches to.
	// Returned to a client that reaches the wrong cell, so a misconfigured integration
	// is told where to go rather than merely refused.
	CellIngestURL string `env:"CELL_INGEST_URL" default:"http://localhost:21081"`

	// CellQueryURL is the address reporting clients for this cell's projects must query.
	// A separate host from the ingest endpoint: the two are different services with very
	// different traffic, and a 421 can only direct a caller if it names the address for
	// the surface they were actually using.
	CellQueryURL string `env:"CELL_QUERY_URL" default:"http://localhost:21083"`

	// GlobalPostgresDSN addresses the one global control-plane tier (§99).
	//
	// US-hosted: it holds accounts, organisations, projects, keys and aggregate counts,
	// and by §99's rule no player data at all.
	GlobalPostgresDSN string `env:"GLOBAL_POSTGRES_DSN" required:"true" secret:"true"`

	// CellPostgresDSN addresses this cell's own database, holding everything that names
	// a player. It never leaves the cell's region.
	CellPostgresDSN string `env:"CELL_POSTGRES_DSN" secret:"true"`

	// ClickHouseDSN addresses this cell's analytics database (§13).
	ClickHouseDSN string `env:"CLICKHOUSE_DSN" required:"true" secret:"true"`
	// ValkeyDSN addresses this cell's cache and device-state store (§12).
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

	// KeyCacheLocalTTL bounds the in-process tier, which exists so ingestion survives
	// both Valkey and PostgreSQL being unreachable (§64, §65). It is deliberately short:
	// revoking a key evicts it from Valkey at once, and this is the one window eviction
	// cannot reach.
	KeyCacheLocalTTL time.Duration `env:"KEY_CACHE_LOCAL_TTL" default:"10s"`

	// RateLimitDisabled turns the limiter off entirely.
	//
	// Only a test sets this. Counters now live in Valkey and are shared by every
	// instance, which is the point — but it also means one suite's requests spend the
	// next suite's budget, and a test that fails because a previous test ran is a test
	// nobody trusts. The limiter itself is exercised by a test written for it.
	RateLimitDisabled bool `env:"RATE_LIMIT_DISABLED" default:"false"`

	// AnalyticsWriterAddr is where the writer serves its health probe. It has no API.
	AnalyticsWriterAddr string `env:"ANALYTICS_WRITER_ADDR" default:":21082"`

	// WriterBatchSize is the maximum number of rows in one ClickHouse insert block. A
	// message carrying more events than this is split deterministically, so a
	// redelivery produces the same blocks with the same deduplication tokens.
	//
	// Never one event per insert (§86). A message is already one SDK batch of up to a
	// thousand events, so this is a ceiling rather than a target.
	WriterBatchSize int `env:"WRITER_BATCH_SIZE" default:"5000"`

	// WriterFlushInterval is how often the writer looks for new messages when the queue
	// is empty. It bounds delivery latency on an idle system and costs nothing on a busy
	// one, where the consumer drains without waiting.
	WriterFlushInterval time.Duration `env:"WRITER_FLUSH_INTERVAL" default:"2s"`

	// WriterDeviceStateTTL is how long a device's sticky cohort dimensions are
	// remembered in Valkey (§29.5). Every read renews it, so a device that keeps playing
	// keeps its entry; a device absent longer than this is stamped as a new cohort at
	// write time and corrected by the nightly derivation, which reads player_lifetime.
	WriterDeviceStateTTL time.Duration `env:"WRITER_DEVICE_STATE_TTL" default:"720h"`

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
