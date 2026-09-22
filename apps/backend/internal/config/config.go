// Package config holds the settings every PlayerSense binary reads at start-up.
package config

import (
	"time"

	muzak "muzak.dev/framework"
)

// Settings is the service configuration, read once at start-up from the environment and
// from a .env file when one is present.
//
// Every connection string carries its own port rather than composing one from a host and
// a port setting, so that the ports declared in .env are the only place a port appears.
type Settings struct {
	// PostgresDSN addresses the database (§99).
	//
	// One database: accounts, organisations, projects, keys and aggregate counts
	// alongside purchases, deletion requests, exports and archive manifests. It was two,
	// split by whether a table could name a player; §96's per-column classification
	// survives that split and `migrate verify` still enforces it.
	PostgresDSN string `env:"POSTGRES_DSN" required:"true" secret:"true"`

	// ClickHouseDSN addresses the analytics database (§13).
	ClickHouseDSN string `env:"CLICKHOUSE_DSN" required:"true" secret:"true"`
	// ValkeyDSN addresses the cache and device-state store (§12).
	ValkeyDSN string `env:"VALKEY_DSN"`

	// MigrationsDir is the repository-root directory holding the ClickHouse migrations.
	MigrationsDir string `env:"MIGRATIONS_DIR" default:"db"`

	// AppName titles the API in the generated documentation.
	AppName string `env:"APP_NAME" default:"PlayerSense API"`
	// Addr is the address the API listens on.
	//
	// One address, because there is one API: the dashboard's surface, the SDK's ingestion
	// and configuration endpoints and the reporting surface are routers in one binary
	// rather than four deployables. They were split so ingestion could scale on its own
	// axis and keep serving while the control plane was down — a real property, and one
	// nothing at this size is paying for.
	Addr string `env:"API_ADDR" default:":21080"`
	// PublicURL is the address clients actually reach, for the OpenAPI servers list and
	// for the endpoint the SDK is told to send batches to.
	PublicURL string `env:"API_PUBLIC_URL" default:"http://localhost:21080"`

	// AdminToken guards the API's operator surface. Marked secret so a malformed
	// value never appears in a start-up error.
	//
	// It is no longer what guards the customer-facing routes: those resolve a signed-in
	// person through Firebase and check that person's permissions. A shared secret cannot
	// answer "who is asking", so it cannot be the credential on an API where the answer
	// decides what comes back.
	AdminToken string `env:"CONTROL_API_ADMIN_TOKEN" secret:"true" required:"true"`

	// FirebaseProjectID is the Firebase project whose ID tokens this deployment accepts.
	//
	// Both the audience an ID token must name and, with the issuer prefix, where it must
	// have come from. Getting it wrong does not fail open — every token is refused —
	// which is the right direction for a value nothing else can check.
	FirebaseProjectID string `env:"FIREBASE_PROJECT_ID" required:"true"`

	// FirebaseCertificateURL is where Google's signing certificates are fetched from.
	//
	// Defaulted, and overridden in two places only: a test, which stands up its own
	// endpoint and its own key so that the whole sign-in path runs without the internet,
	// and a deployment that mirrors the document internally. Both are the same need — the
	// URL is data, and the alternative is a verifier with a seam cut into it purely so
	// that tests can reach inside.
	FirebaseCertificateURL string `env:"FIREBASE_CERTIFICATE_URL" default:"https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"`

	// TrustedProxies lists the proxies whose forwarding header is believed, as a
	// comma-separated list of addresses or CIDR prefixes. Empty by default: a
	// forwarding header is written by whatever sent the request, and believing one from
	// an unknown sender hands every client the ability to choose which rate-limit
	// budget it spends (§70).
	TrustedProxies []string `env:"TRUSTED_PROXIES"`

	// CORSAllowedOrigins lists the browser origins allowed to read a response, as a
	// comma-separated list. An origin is matched exactly, scheme and port included, so
	// `https://app.playersense.io` and `http://app.playersense.io` are two origins and
	// neither implies the other.
	//
	// Defaulted to the three local development servers — landing on 3000, client on 3001,
	// admin on 3002 — for the same reason every DSN here defaults to the local stack: a
	// checkout should run. **A deployment must set this**, and the failure if it forgets
	// is the safe one: the framework's zero value denies every cross-origin request, so a
	// production dashboard that was never named here cannot read a single response.
	//
	// It deliberately does not include an SDK origin. Ingestion is called from a game
	// rather than a browser, and a `*` here would hand every page on the internet the
	// ability to read authenticated responses out of a signed-in customer's browser.
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" default:"http://localhost:3000,http://localhost:3001,http://localhost:3002"`

	// --- ingestion ----------------------------------------------------------

	// EventBus selects the queue implementation: local or pubsub (§3.6).
	EventBus    string `env:"EVENT_BUS" default:"local"`
	EventBusDir string `env:"EVENT_BUS_DIR" default:".data/eventbus"`

	PubSubProjectID string `env:"PUBSUB_PROJECT_ID"`
	PubSubTopic     string `env:"PUBSUB_TOPIC" default:"analytics-events"`

	// These three decide when a consumer is handed a group: whichever is reached first.
	// They are the queue half of the insert-size decision (ADR 0023) and, between them,
	// what actually sets the insert rate — WRITER_BATCH_SIZE is only a ceiling on how a
	// delivery is split, and never makes one wait.
	//
	// Rows is the primary trigger, because rows are the unit ClickHouse cares about. An SDK
	// batch is anywhere from one event to a thousand (§19.1), so a message count is not a
	// size: two hundred messages is two hundred rows from one integration and two hundred
	// thousand from another.
	//
	// The arithmetic worth doing before changing these. At §4's ceiling — a billion events
	// a day, about 11.5k a second — a 100k row target is reached in roughly nine seconds,
	// so the *wait* is what binds and it sets the insert rate: two seconds gives about
	// 23k rows an insert and 43k inserts a day. Shorter makes smaller parts and more merge
	// work; longer makes bigger blocks and costs event visibility, which §80 budgets at ten
	// seconds p95. A quiet cell is bound by the wait in the other direction and simply
	// writes small parts, which is affordable precisely because it is quiet.
	BusDeliveryMaxRows int `env:"BUS_DELIVERY_MAX_ROWS" default:"100000"`

	// A memory and outstanding-message bound rather than a target, and the backstop for a
	// producer that reported no event count. Keep it below the subscription's
	// MaxOutstandingMessages: a group is held unacknowledged while it fills, and Receive
	// stops pulling once too many are outstanding.
	BusDeliveryMaxMessages int `env:"BUS_DELIVERY_MAX_MESSAGES" default:"1000"`

	// How long a partly-filled group waits for more. This is the latency the grouping
	// costs, and it is paid on a quiet cell rather than a busy one.
	//
	// Thirty seconds is a deliberate choice about a *quiet* cell, not a busy one: at any
	// real volume the row target above is reached first and this timer never fires. Where
	// it does bind — development, a new customer, a trickle of traffic — an event takes up
	// to half a minute to appear, which is outside §80's ten-second visibility target. §80
	// qualifies that target with "at normal load", and at normal load the row trigger
	// governs, so the two do not actually conflict; a deployment that wants snappier
	// feedback while integrating should lower it, which is why .env.example does.
	BusDeliveryMaxWait time.Duration `env:"BUS_DELIVERY_MAX_WAIT" default:"30s"`

	// IngestMaxCompressedBytes bounds a compressed batch on the wire (§17, §19.1).
	//
	// The first of §19.1's two limits. The second — maximum *decompressed* bytes — is the
	// route's own body limit, applied by the framework while binding, so a compressed batch
	// and an uncompressed one are held to the same size and a decompression bomb stops
	// being read at the limit rather than being read and then measured.
	//
	// Derived rather than picked: a batch is at most 1000 events and about 290 bytes an
	// event of JSON, so the largest legitimate one is a little under 300 KB uncompressed and
	// roughly 32 KB compressed (`cmd/wiresize`). A megabyte is thirty times that headroom,
	// and four times smaller than the uncompressed limit — which is the point, since
	// compression is for sending the same batch in fewer bytes rather than a bigger one.
	IngestMaxCompressedBytes int64 `env:"INGEST_MAX_COMPRESSED_BYTES" default:"1048576"`

	// GeoIPLocationDB and GeoIPPrivacyDB are the MaxMind-format databases behind `geo.*`
	// and `network.*`. Both optional: without them those namespaces are simply absent and
	// a rule naming one never matches, which is a degraded product rather than a stopped
	// one and is the right behaviour on a developer's machine.
	GeoIPLocationDB string `env:"GEOIP_LOCATION_DB" default:""`
	GeoIPPrivacyDB  string `env:"GEOIP_PRIVACY_DB" default:""`

	// ConfigCacheTTL is how long a published document is served before it is looked up
	// again. A publish is a deliberate act somebody is watching the result of, so this is
	// also the longest anybody waits to see one.
	ConfigCacheTTL time.Duration `env:"CONFIG_CACHE_TTL" default:"1m"`

	// FactsTTL bounds how stale a player's history may be (§41).
	//
	// A staleness bound rather than an idle timeout: the entry is never renewed on read,
	// so a player who buys something stops being a non-payer within the hour rather than
	// whenever they next stop playing.
	FactsTTL time.Duration `env:"FACTS_TTL" default:"1h"`

	// FactsAbsentTTL is how long "this player has no history" is remembered.
	//
	// The difference between a cache and a stampede. A brand-new installation has no
	// lifetime row until the materialized views have seen its first events, and new
	// installations are the population that requests config most eagerly.
	FactsAbsentTTL time.Duration `env:"FACTS_ABSENT_TTL" default:"5m"`

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

	// QuerySuppressionK is the minimum cell size (§32.2, ADR 0017).
	//
	// Configurable, and the record says why that matters: "the threshold can be raised
	// later without recomputing history", because suppression happens at query time and
	// the stored cells stay complete. Five is a default, not a finding.
	QuerySuppressionK int `env:"QUERY_SUPPRESSION_K" default:"5"`

	// QueryCacheTTL is how long an answer is reused while the nightly derivation could
	// still change it (§58).
	QueryCacheTTL time.Duration `env:"QUERY_CACHE_TTL" default:"60s"`

	// QueryCacheSettledTTL applies to a window nothing will rebuild. Longer because it
	// cannot change, and because the historical range somebody left open in a browser tab
	// is the most expensive repeated query a dashboard makes.
	QueryCacheSettledTTL time.Duration `env:"QUERY_CACHE_SETTLED_TTL" default:"1h"`

	// QueryMaxRows caps a result set. Exceeding it is an error rather than a truncation:
	// a silently truncated cohort curve is a curve that appears to end.
	QueryMaxRows int `env:"QUERY_MAX_ROWS" default:"50000"`

	// QueryMaxExecutionTime bounds wall-clock time in ClickHouse. §80 wants a
	// pre-aggregated chart under a second, so anything approaching this is already a
	// query that has escaped the cohort facts.
	QueryMaxExecutionTime time.Duration `env:"QUERY_MAX_EXECUTION_TIME" default:"10s"`

	// QueryMaxRowsToRead and QueryMaxBytesToRead bound the scan rather than the result,
	// which is what actually protects the node: a query returning ten rows can still read
	// the whole table.
	QueryMaxRowsToRead  uint64 `env:"QUERY_MAX_ROWS_TO_READ" default:"200000000"`
	QueryMaxBytesToRead uint64 `env:"QUERY_MAX_BYTES_TO_READ" default:"4294967296"`

	// ArchiveWriterAddr is where the archive writer serves its health probe. It has no API.
	ArchiveWriterAddr string `env:"ARCHIVE_WRITER_ADDR" default:":21084"`

	// ArchiveStore picks the object store: local writes to a directory, gcs to a bucket.
	// The same shape as EVENT_BUS, and for §3.6's reason — the archive must not couple the
	// writer to one deployment product.
	ArchiveStore string `env:"ARCHIVE_STORE" default:"local"`

	// ArchiveBucket is the cell's bucket. One per cell, in that cell's region (§6, §14):
	// an archive in the wrong region is the same residency failure as a database in the
	// wrong region, and a quieter one.
	ArchiveBucket string `env:"ARCHIVE_BUCKET" default:"playersense-events-local"`

	// ArchiveDir is where ARCHIVE_STORE=local keeps its buckets.
	ArchiveDir string `env:"ARCHIVE_DIR" default:".data/archive"`

	// ArchiveMaxRowsPerObject bounds one object, so a very large delivery is split rather
	// than buffered whole in memory. A ceiling, not a target.
	ArchiveMaxRowsPerObject int `env:"ARCHIVE_MAX_ROWS_PER_OBJECT" default:"200000"`

	// AnalyticsWriterAddr is where the writer serves its health probe. It has no API.
	AnalyticsWriterAddr string `env:"ANALYTICS_WRITER_ADDR" default:":21082"`

	// WriterBatchSize is the maximum number of rows in one ClickHouse insert block.
	//
	// A ceiling, not a target: the writer fills a block from as many messages as the
	// delivery holds and splits only when it would exceed this. Raised from five thousand
	// once the writer began coalescing across messages — at §4's ceiling the number that
	// matters is inserts per day, and a hundred thousand rows a block turns a billion
	// events into ten thousand inserts rather than two million (ADR 0023).
	//
	// It bounds memory as much as merge pressure: a block is assembled in the writer before
	// it is sent, so this times the average row size is what one insert costs in RAM.
	WriterBatchSize int `env:"WRITER_BATCH_SIZE" default:"100000"`

	// WriterMaxAttempts is how many times a delivery is retried whole before the writer
	// starts storing its messages one at a time to find one it cannot store (ADR 0023).
	//
	// Not a dead-letter threshold on its own: a message is parked only after ClickHouse has
	// been probed and found to be answering, so an outage cannot be mistaken for poison.
	WriterMaxAttempts int `env:"WRITER_MAX_ATTEMPTS" default:"5"`

	// WriterFlushInterval is how often the writer looks for new messages when the queue
	// is empty. It bounds delivery latency on an idle system and costs nothing on a busy
	// one, where the consumer drains without waiting.
	WriterFlushInterval time.Duration `env:"WRITER_FLUSH_INTERVAL" default:"2s"`

	// WriterDeviceStateTTL is how long a device's sticky cohort dimensions are
	// remembered in Valkey (§29.5). Every read renews it, so a device that keeps playing
	// keeps its entry; a device absent longer than this is stamped as a new cohort at
	// write time and corrected by the nightly derivation, which reads player_lifetime.
	WriterDeviceStateTTL time.Duration `env:"WRITER_DEVICE_STATE_TTL" default:"720h"`

	// BillingWriterAddr is where the billing writer serves its health probe (ADR 0039).
	BillingWriterAddr string `env:"BILLING_WRITER_ADDR" default:":21088"`

	// BillingDigestKey is the HMAC key behind every billable player's monthly digest.
	//
	// Secret, because the digest is what keeps the billing table from being joined to
	// events_raw by whoever can read it. Not required by every binary — only the billing
	// writer uses it, and it refuses to start without one of at least 32 bytes.
	//
	// **Do not rotate it mid-month.** Every player of the current month would become new
	// again under a new key and be charged a second time. Rotate on the first of a month,
	// before the first batch of it arrives.
	BillingDigestKey string `env:"BILLING_DIGEST_KEY" secret:"true"`

	// BillingDrainEvery is how often queued usage charges are moved into wallets. It is how
	// stale a balance can be; the charges themselves are durable from the moment they are
	// counted.
	BillingDrainEvery time.Duration `env:"BILLING_DRAIN_EVERY" default:"5s"`

	// BillingDrainMaxAccounts bounds one drain pass. The rest are the next pass's.
	BillingDrainMaxAccounts int `env:"BILLING_DRAIN_MAX_ACCOUNTS" default:"500"`

	// BillingMaxInsert bounds one INSERT of new players.
	BillingMaxInsert int `env:"BILLING_MAX_INSERT" default:"1000"`

	// BillingPruneEvery is how often player sets older than last month are deleted.
	BillingPruneEvery time.Duration `env:"BILLING_PRUNE_EVERY" default:"1h"`

	// WorkerAddr is where the worker serves its health probe. It has no API.
	WorkerAddr string `env:"WORKER_ADDR" default:":21086"`

	// WorkerDeriveAt is the time of day, in UTC, the nightly derivation starts (§79.1).
	//
	// UTC rather than a local zone: a job anchored to a zone with daylight saving either
	// runs twice or not at all on two days a year, and the day it does not run is the day
	// every cohort chart is stale.
	WorkerDeriveAt string `env:"WORKER_DERIVE_AT" default:"02:00"`

	// WorkerActivityMonths is how many trailing activity months are rebuilt, counting the
	// current one. Two keeps last month open all month, so a late event arriving on the
	// 20th is absorbed rather than only one arriving on the 2nd.
	WorkerActivityMonths int `env:"WORKER_ACTIVITY_MONTHS" default:"2"`

	// WorkerCohortMonths is how many trailing cohort months have their denominators
	// rebuilt. A different axis from the activity window: one activity day contributes
	// cells to every cohort month in the preceding year (§29.8), so the denominators
	// reach much further back than the numerators do.
	WorkerCohortMonths int `env:"WORKER_COHORT_MONTHS" default:"13"`

	// WorkerLifetimeRetention is how long a device may go unseen before player_lifetime
	// forgets it (§33.1). Empty or zero disables the prune.
	//
	// 550 days — about eighteen months — is derived rather than chosen. The floor is the
	// cohort rebuild window: WORKER_COHORT_MONTHS of denominators are republished from
	// player_lifetime_v every night, so a device that arrived inside that window must still
	// be there, and thirteen months is about 396 days. Above that floor the number only
	// trades storage against how long a dormant player stays recognisable, and eighteen
	// months also comfortably covers §29.12's longest offset, which is 365 days.
	//
	// It keys on last activity, not arrival: an active player is never forgotten however
	// long ago they installed, so their lifetime playtime and spend survive for §29.11's
	// buckets. The worker refuses to start if this is below the floor.
	WorkerLifetimeRetention time.Duration `env:"WORKER_LIFETIME_RETENTION" default:"13200h"`

	// WorkerJanitorEvery is how often the data-lifecycle pass runs (§60, §61, §101).
	//
	// A different cadence from the derivation above and for a different reason. The
	// derivation rebuilds whole months and gains nothing from running twice; these three
	// jobs are each waiting on something — an erasure on a legal deadline, an export on a
	// person, an expired object on a retention term a customer agreed to.
	//
	// Sized against the erasure. §61's drain window is how long a late delivery can still
	// arrive for an erased actor, and the purge runs repeatedly inside it so that
	// "completed" means "and nothing has arrived since". Fifteen minutes gives a day's
	// window ninety-six passes, each of which costs almost nothing when there is nothing
	// to do.
	WorkerJanitorEvery time.Duration `env:"WORKER_JANITOR_EVERY" default:"15m"`

	// RetentionMaxObjects bounds one archive-expiry pass (§60).
	//
	// A bound, not a target. The first pass over a bucket that has never been swept has
	// every expired object in it at once; several bounded passes reach the same place
	// without holding one enormous unit of work open against both the cell tier and the
	// bucket.
	RetentionMaxObjects int `env:"RETENTION_MAX_OBJECTS" default:"5000"`

	// ErasureDrainWindow is how long an erasure keeps re-purging before it completes (§61).
	//
	// Sized on the queue rather than on the law. §21 allows a message to sit for hours and
	// §06 makes the purge — not the tombstone — the guarantee that a late arrival is
	// removed, so the window has to outlast any plausible backlog. A day does, and it is
	// still far inside the month §05 works to. It also sets the tombstone's lease: the
	// tombstone exists to drop exactly the deliveries this window is waiting for.
	ErasureDrainWindow time.Duration `env:"ERASURE_DRAIN_WINDOW" default:"24h"`

	// ErasureMaxObjects bounds the archive work inside one erasure's turn (§61).
	//
	// The archive is the expensive half: every candidate object is downloaded, decoded and,
	// if it holds the person, written back. A request covering a long span is several
	// bounded passes, and it stays in `purging` between them — a state it was going to be
	// in anyway.
	ErasureMaxObjects int `env:"ERASURE_MAX_OBJECTS" default:"500"`

	// ExportTTL is how long a completed export bundle stays collectable (§101).
	//
	// A bundle is a second copy of everything held about one person. Long enough for a
	// studio to fetch it inside a working week; short enough that an uncollected copy is
	// not a permanent fixture of the bucket.
	ExportTTL time.Duration `env:"EXPORT_TTL" default:"168h"`

	// ExportMaxRows bounds one export bundle (§101).
	//
	// The bundle is written as one object and therefore assembled in memory, so the bound
	// is real rather than defensive. A subject with more history than this is refused with
	// a number in the failure reason: §05 would rather say "ask again, narrower" than hand
	// over a truncated file that claims to be complete.
	ExportMaxRows uint64 `env:"EXPORT_MAX_ROWS" default:"1000000"`

	// DashboardURL is where an invitation link points.
	//
	// The backend composes the link because the backend holds the token, so it has to know
	// the address of a front end that does not exist yet (§88's Nuxt client). Wrong here
	// means an invitation nobody can accept, which is why it is configuration rather than a
	// constant.
	DashboardURL string `env:"DASHBOARD_URL" default:"http://localhost:3000"`

	// InvitationTTL is how long an invitation link works.
	//
	// Three days. An invitation is a bearer credential sitting in an inbox, so the window in
	// which one is useful should be about as long as it takes somebody to read their mail
	// over a weekend — and no longer, because the thing that makes a stale invitation
	// dangerous is that nobody remembers it exists.
	InvitationTTL time.Duration `env:"INVITATION_TTL" default:"72h"`

	// MailSender picks how PlayerSense's own mail is delivered: `log` is the placeholder.
	//
	// Only invitations go through it — every authentication email is Firebase's
	// (docs/rate-limits.md §6). An unknown name fails at start-up rather than falling back,
	// because a deployment that meant to name a provider and mistyped it would otherwise
	// come up clean and deliver nothing.
	MailSender string `env:"MAIL_SENDER" default:"log"`

	// MailLogBody writes the message body to the log, for a developer with no inbox.
	//
	// **Development only.** An invitation body contains the token, which is a bearer
	// credential: in a log it outlives the invitation's expiry, reaches whatever aggregates
	// logs, and is readable by people who were never invited.
	MailLogBody bool `env:"MAIL_LOG_BODY" default:"false"`

	// CreditExpiryMaxLots bounds one pass of the wallet's expiry sweep.
	//
	// A pass writes two rows per lapsed lot inside one transaction, so the bound is about how
	// long that transaction holds rather than about how much work there is. Whatever is left is
	// swept minutes later, and the spend path was already refusing the credit.
	CreditExpiryMaxLots int `env:"CREDIT_EXPIRY_MAX_LOTS" default:"500"`

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
