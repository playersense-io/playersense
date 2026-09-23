// Command api serves PlayerSense (project.md §10).
//
// One binary, four surfaces that used to be four deployables:
//
//	/v1/…            the dashboard: organisations, projects, keys, members, billing, LiveOps
//	/v1/ingest       the SDK's high-volume path — accept a batch, enqueue it, answer
//	/v1/config       what a running game gets: resolved flags and windows, never rules
//	/v1/cohorts, /v1/metrics, /v1/privacy   reporting, and §101's data-subject requests
//
// They were split because they scale differently and fail differently: §10.2 wanted
// ingestion to keep accepting batches while the control plane was unreachable, and a cell
// wanted its own ingest, config and query endpoints in its own region. Both properties are
// real and both cost a deployment each, four sets of settings, and a routing rule — the
// 421 — on every credential check. None of that is earned at this size, and the part worth
// keeping survives the merge intact: ingestion still never queries PostgreSQL per request,
// because the tenant is resolved from the key cache and the batch goes to the queue.
//
// This file does nothing but compose. Every piece of behaviour lives in a package of its
// own — core, schemas, handlers, routers — and the dependencies point one way, so each is
// testable without a server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/facts"
	"playersense.dev/backend/internal/firebase"
	"playersense.dev/backend/internal/geoip"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/transport"
	"playersense.dev/backend/internal/valkey"
)

func main() {
	// `-openapi` prints the document and exits, so the TypeScript types in
	// packages/contracts are generated from the application itself rather than from a
	// running one. Nothing here dials anything — build() constructs its dependencies and
	// opens none of them — so this works on a laptop with no stack up and in CI with no
	// services started, which is what makes the drift check cheap enough to run on every
	// push (packages/contracts/README.md).
	emitDocument := flag.Bool("openapi", false,
		"print the OpenAPI document to stdout and exit, instead of serving")
	flag.Parse()

	settings := config.MustLoad(filepath.Join(repoRoot(), ".env"))

	app, err := build(settings)
	if err != nil {
		log.Fatal(err)
	}

	if *emitDocument {
		if err := writeDocument(app, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := app.RunSignals(); err != nil {
		log.Fatal(err)
	}
}

// writeDocument prints the OpenAPI description of the application.
//
// Marshalled through the framework's own deterministic encoder rather than encoding/json:
// key order is stable across runs, which is the difference between a committed document
// that diffs cleanly and one that reports drift every time somebody regenerates it.
func writeDocument(app *muzak.App, out io.Writer) error {
	doc, err := app.Document()
	if err != nil {
		return fmt.Errorf("build the OpenAPI document: %w", err)
	}
	if doc == nil {
		return errors.New("this application serves no OpenAPI document")
	}
	body, err := doc.Marshal()
	if err != nil {
		return fmt.Errorf("marshal the OpenAPI document: %w", err)
	}
	_, err = out.Write(append(body, '\n'))
	return err
}

// build composes the application.
//
// It is a function rather than inline in main so that the test suite exercises exactly
// the application that runs, rather than a second one assembled to look like it.
//
// `overrides` are appended after everything below, so a later declaration of the same type
// wins. It exists for one thing: an invitation's token leaves this process only inside the
// email, so a test of the accept flow has to be able to substitute a [mail.Recorder] for
// the sender. Anything a deployment needs is a setting, not an override.
func build(settings config.Settings, overrides ...muzak.RouterOption) (*muzak.App, error) {
	// Construction is cheap and dials nothing. Each of these implements muzak.Lifecycle,
	// so the application opens them before the server accepts traffic and closes them
	// after it has drained — a request still in flight keeps a usable connection to the
	// very end.
	db := pgdb.New(settings.PostgresDSN)

	// The cache is what keeps the key lookup off the hot path, and it is also how
	// revoking a key stops working immediately rather than whenever its entry expires —
	// which is exactly the minutes during which somebody needs it to stop working (§53).
	cache := valkey.New(settings.ValkeyDSN)
	resolver := core.NewKeyResolver(db, cache, core.KeyResolverOptions{
		TTL:         settings.KeyCacheTTL,
		NegativeTTL: settings.KeyCacheNegativeTTL,
		LocalTTL:    settings.KeyCacheLocalTTL,
		MaxLocal:    settings.KeyCacheMaxEntries,
	})

	// Firebase Auth owns authentication; this is the other end of it. PlayerSense holds
	// no credential, so there is no password column to leak and no session store to
	// compromise — and the price is that signing in depends on Google being reachable,
	// which is why the key cache serves what it already has when it is not.
	gateway := payments.New(settings.StripeSecretKey)
	verifier := firebase.NewVerifier(settings.FirebaseProjectID)
	verifier.Keys.URL = settings.FirebaseCertificateURL

	// The one thing this service sends itself. Every authentication email is Firebase's
	// (docs/rate-limits.md §6); this is for invitations, which Firebase cannot send
	// because it does not know what an organization is.
	//
	// An unknown MAIL_SENDER is fatal rather than a fallback to the placeholder: a
	// deployment that meant to name a provider and mistyped it would otherwise start
	// cleanly and deliver nothing at all.
	sender, err := mail.New(settings.MailSender, nil, settings.MailLogBody)
	if err != nil {
		return nil, err
	}

	bus, err := newBus(settings)
	if err != nil {
		return nil, err
	}

	store, err := newStore(settings)
	if err != nil {
		return nil, err
	}

	// Two ClickHouse handles, because the two packages want different things from it: the
	// query engine wants a typed connection and the facts cache wants database/sql. Both
	// address the same server.
	ch := chdb.New(settings.ClickHouseDSN)
	chSQL := chdb.NewSQL(settings.ClickHouseDSN)

	engine := query.NewEngine(ch, query.Limits{
		MaxRows:          settings.QueryMaxRows,
		MaxExecutionTime: settings.QueryMaxExecutionTime,
		MaxRowsToRead:    settings.QueryMaxRowsToRead,
		MaxBytesToRead:   settings.QueryMaxBytesToRead,
	}, settings.QuerySuppressionK).
		WithCache(query.NewCache(cache, settings.QueryCacheTTL, settings.QueryCacheSettledTTL))

	liveStore := liveconfig.NewStore(func() *ent.Client { return db.Client }, settings.ConfigCacheTTL)

	// The player's own history, read through Valkey with ClickHouse behind it — and only
	// for the minority of projects whose rules name `commerce.` or `activity.`. The
	// compiled document carries that decision, so a project without such a rule performs
	// no lookup at all.
	playerFacts := facts.NewCache(cache, facts.NewClickHouse(chSQL),
		settings.FactsTTL, settings.FactsAbsentTTL)

	// Geography and network, derived from the request's address. Optional: a deployment
	// without the databases resolves nothing and every `geo.*` rule falls through to
	// NotExists, which is a smaller failure than refusing to answer.
	geo, err := geoip.Open(settings.GeoIPLocationDB, settings.GeoIPPrivacyDB)
	if err != nil {
		return nil, fmt.Errorf("open the geoip databases: %w", err)
	}

	options := muzak.AppOptions{
		Title:   settings.AppName,
		Version: "0.1.0",
		Description: "Organizations, projects and keys; the SDK's ingestion and configuration " +
			"endpoints; cohort reporting; and one player's erasure or export.",
		Servers: []muzak.Server{
			{URL: settings.PublicURL, Description: "this deployment"},
		},
		Tags: []muzak.Tag{
			{Name: "users", Description: "The signed-in person, and where they belong."},
			{Name: "organizations", Description: "The billing and ownership boundary."},
			{Name: "projects", Description: "One game, with its environments."},
			{Name: "api-keys", Description: "Credentials scoped to one environment."},
			{Name: "members", Description: "Who is in an organization, and what they may do."},
			{Name: "billing", Description: "Usage, the wallet and the plan."},
			{Name: "operator", Description: "PlayerSense staff only: putting credit in a wallet, and the plans and prices it is spent against."},
			{Name: "pricing", Description: "The public price list."},
			{Name: "payments", Description: "Stripe's webhook: how money reaches a wallet."},
			{Name: "liveops", Description: "What a studio edits, and publishing it."},
			{Name: "ingestion", Description: "The high-volume SDK path."},
			{Name: "config", Description: "What a player gets."},
			{Name: "reporting", Description: "Reading analytics, with a key or as a person."},
			{Name: "privacy", Description: "Erasing and exporting one player's data."},
		},

		Addr: settings.Addr,
		// Under the ten seconds Cloud Run allows after SIGTERM: a drain the platform
		// kills part way through is not a drain.
		ShutdownTimeout: settings.ShutdownTimeout,

		// The ingestion batch, which is the largest thing anything here accepts (§19.1).
		// This is the outer wall and nothing else: every other route declares its own,
		// much smaller limit — 64 KB for a cohort query, 32 KB for a config request — so
		// merging the services did not widen what a dashboard route will read.
		MaxBodySize: 4 << 20,

		// Nothing is believed from a forwarding header until the proxy that wrote it is
		// named. Without this, every request behind a load balancer is attributed to the
		// balancer and the whole world shares one rate-limit budget.
		ClientIP: muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},

		// The dashboard and the admin console are separate origins from this API, so
		// without a policy here the browser refuses to expose a single response and
		// every page is blocked. The framework's zero value denies everything, which is
		// why this is configured rather than defaulted (§70).
		CORS: muzak.CORSOptions{
			AllowedOrigins: settings.CORSAllowedOrigins,

			// **Off**, and the reason is worth stating because the obvious reading of
			// the field name is wrong. `AllowCredentials` controls one response header
			// and one browser behaviour: whether a request made with `credentials:
			// "include"` — cookies, TLS client certificates — is allowed. It does not
			// gate the `Authorization` header, which is an ordinary request header
			// permitted by `AllowedHeaders` and included in the framework's default.
			//
			// Both browser apps hold a Firebase ID token in client-side state and
			// attach it themselves; neither sends a cookie. Turning this on would
			// therefore buy nothing and widen what a mistake in the origin list costs.
			AllowCredentials: false,

			// Browsers expose only a small safelist of response headers to script —
			// Cache-Control, Content-Language, Content-Type, Expires, Last-Modified,
			// Pragma — and silently hide the rest. Each of these is read by a client:
			//
			//   Retry-After          the limiter's own answer to "when may I come
			//                        back". Unexposed, a refused dashboard request
			//                        can only guess, which is how a 429 becomes a
			//                        retry storm (docs/rate-limits.md).
			//   X-Request-Id         what a support conversation is about.
			//   ETag                 §42's configuration endpoint answers conditional
			//                        requests, and a client that cannot read the tag
			//                        cannot send it back.
			//   Content-Disposition  the filename on a §101 export download.
			ExposedHeaders: []string{
				muzak.HeaderRetryAfter,
				muzak.HeaderRequestID,
				"ETag",
				"Content-Disposition",
			},
		},

		LoggerOptions: muzak.LoggerOptions{Format: logFormat(settings.LogFormat)},

		// A health check polled every second should not drown out real traffic.
		AccessLogOptions: muzak.AccessLogOptions{
			Level:     slog.LevelInfo,
			SkipPaths: []string{"/healthz"},
		},
	}

	shared := []muzak.RouterOption{
		muzak.WithSingleton(db),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),
		muzak.WithSingleton(engine),
		muzak.WithSingleton(store),
		muzak.WithSingleton(liveStore),
		muzak.WithSingleton(playerFacts),
		muzak.WithSingleton(ingest.DefaultLimits()),

		// Registered as lifecycles as well as singletons: the engine and the facts cache
		// hold them but do not own their connections, and the application opens them
		// before traffic and closes them after draining.
		muzak.WithSingleton(ch),
		muzak.WithSingleton(chSQL),

		// Published as the interface, so no handler can depend on which implementation is
		// running — §3.6's whole point.
		muzak.WithSingleton(bus),
		muzak.WithSingleton(sender),

		// Which organizations may not ingest until they pay (ADR 0043), read from Valkey and
		// remembered for seconds; never PostgreSQL on the ingestion path.
		muzak.WithSingleton(&suspension.Gate{Cache: cache}),

		// Stripe, or a stand-in that refuses with "not configured" when there is no key.
		muzak.WithSingleton(gateway),
		muzak.WithSingleton(&payments.Webhooks{
			DB: db, Cache: cache, Secret: settings.StripeWebhookSecret, Gateway: gateway,
			RetryAfter: settings.BillingAutoTopUpRetryAfter,
		}),

		// Verifies the ID token on every request and caches Google's signing
		// certificates. Started with the application so that a deployment which cannot
		// reach Google fails while it is starting rather than on somebody's first
		// sign-in.
		muzak.WithSingleton(verifier, muzak.LifecycleFunc("firebase keys", verifier.Start, nil)),

		muzak.WithSingleton(geo, muzak.LifecycleFunc("geoip", nil,
			func(context.Context) error { geo.Close(); return nil })),

		// One limiter, counted in the handlers rather than by the framework.
		//
		// The framework's own limiter runs before any dependency resolves, so it can only
		// key on the client's address — and an address is not a player. Carrier-grade NAT
		// puts tens of thousands of subscribers behind one IPv4, so a budget keyed on that
		// throttles a country for one bad actor; and because its key carries no project,
		// it lets one studio's hit game throttle another studio's players.
		//
		// Fails **open**: §65's rule is that losing Valkey costs speed rather than data,
		// and refusing every batch because the limiter is unreachable would turn a
		// degraded cache into an outage of the thing customers pay for. The invitation
		// route is the one exception and takes it per call, through Limiter.Strict — an
		// email cannot be taken back. See docs/rate-limits.md.
		//
		// Nil when limits are disabled, which is how one test run's counters stop spending
		// the next one's budget. A handler resolves it with TryFrom and counts nothing
		// when it is absent.
		muzak.WithSingleton(ratelimit.New(
			valkey.NewRateLimitStorage(cache), true, !settings.RateLimitDisabled)),
	}

	// Appended last, so a later declaration of the same type wins.
	app := muzak.New(options, append(shared, overrides...)...)

	// Before anything binds a body, because §19.1's "maximum decompressed bytes" is the
	// route's own body limit and a limit applied after decoding is not a limit (§17, §19).
	//
	// A request with no Content-Encoding passes straight through, so the uncompressed path
	// pays one header lookup.
	app.Use(transport.Decompress(transport.Options{
		MaxCompressedBytes: settings.IngestMaxCompressedBytes,
	}))

	// No application-wide rate limit, deliberately, and the reason differs per surface.
	//
	// The dashboard is authenticated, low-volume and used by people rather than programs,
	// and nothing expensive happens before the sign-in is verified. Reporting is bounded
	// per *query* instead — MaxRows, MaxExecutionTime, MaxRowsToRead, MaxBytesToRead, the
	// refusal to join on an actor key — and a bounded query cannot be expensive, so a
	// great many of them cannot be catastrophic. The two SDK paths count in the handler,
	// where the project and the installation are known. See docs/rate-limits.md.

	// Operational routes answer without a credential; everything else sits behind a
	// credential of some kind. Those decisions are made here rather than inside the
	// routers, which keeps the routers reusable and puts the security decision where a
	// reviewer will look.
	app.Include(routers.Meta())

	// The price list, which is public by nature: the landing page reads it (ADR 0039).
	app.Include(routers.Pricing(), muzak.WithPrefix("/v1"))

	// Stripe's webhook. Its credential is Stripe's signature over the body, checked in the
	// handler, so it sits outside every subtree that resolves a person or a key (ADR 0040).
	app.Include(routers.StripeWebhooks(), muzak.WithPrefix("/v1"))

	// ---- key-authenticated surfaces -----------------------------------------
	//
	// Each resolves an API key rather than a person, and each router names the key class
	// it requires (§52): a project key ingests and reads config, a server key erases and
	// exports, a reporting key queries.
	app.Include(routers.Ingest())
	app.Include(routers.Config())
	app.Include(routers.Cohorts())
	app.Include(routers.Privacy())

	// ---- the dashboard ------------------------------------------------------

	api := muzak.NewRouter()
	api.Include(routers.Organizations())
	api.Include(routers.Projects())
	api.Include(routers.Keys())
	api.Include(routers.Members())
	api.Include(routers.Billing())
	api.Include(routers.Authoring())
	api.Include(routers.LiveConfig())

	// The invitation routes the *invited* person uses, which name no organization: there
	// is no membership yet, which is the whole point. Mounted in the same subtree so they
	// resolve a signed-in person, and guarded individually rather than by a permission —
	// see the router.
	api.Include(routers.Invitations())

	// The signed-in person, for the dashboard's shell. Names no organization either; what each
	// organization shows is decided by the person's role in it.
	api.Include(routers.Users())

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

	// The dashboard's own reporting routes, behind a signed-in person rather than a key.
	//
	// A subtree of its own because the provider differs: ResolveKnownUser refuses an
	// account that has never been seen, where ResolveUser above may create one on first
	// sign-in.
	app.Include(routers.Analytics(),
		muzak.WithPrefix("/v1"),
		muzak.Needs(core.ResolveKnownUser),
		muzak.WithResponseDoc(http.StatusUnauthorized, "No usable sign-in was presented"),
		muzak.WithResponseDoc(http.StatusForbidden, "This account may not read this project's analytics"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"A sign-in could not be verified right now; the credential is not necessarily bad"),
	)

	// The operator surface, behind the shared token rather than a person.
	//
	// Its own subtree because its credential is different in kind: everything above
	// resolves a signed-in person and checks that person's permissions, and this is one
	// secret that says "PlayerSense staff" and nothing more. §56's console and the
	// staff-access model are what replace it; until then the router's own comment carries
	// the cost of the difference.
	operator := muzak.NewRouter()
	operator.Include(routers.OperatorBilling())
	operator.Include(routers.OperatorPricing())
	app.Include(operator,
		muzak.WithPrefix("/v1/operator"),
		muzak.WithDependencies(core.RequireAdminToken(settings)),
		muzak.WithResponseDoc(http.StatusUnauthorized, "The operator token is missing or wrong"),
	)

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
		return eventbus.NewLocal(settings.EventBusDir), nil
	default:
		return nil, fmt.Errorf("unknown EVENT_BUS %q; want local or pubsub", settings.EventBus)
	}
}

// newStore picks where archive objects and export bundles are written.
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
