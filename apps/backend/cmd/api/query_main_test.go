package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	muzak "muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/cohort"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/firebase"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/rollup"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/writer"
)

// reporting is the whole product in one fixture: a batch is accepted by the real gateway,
// stored by the real writer, turned into cohort facts by the real derivation, and read back
// through the real reporting API.
//
// Nothing here is a stub. That is the point — every other test proves one component keeps
// its own promises, and this one proves the numbers survive the trip between them. A
// retention curve is the product; this is the first test that asks for one.
type reporting struct {
	settings  config.Settings
	ingest    *testclient.Client
	reader    *testclient.Client
	projectCl *testclient.Client
	// The dashboard's own client: a signed-in growth manager rather than a reporting key.
	// GROWTH MANAGER because it is the narrowest role holding analytics.read, so a test using
	// it proves the permission rather than the fact that owners can do everything.
	member  *testclient.Client
	google  *fbtest.Google
	readerA *muzak.App
	db      *pgdb.DB

	writer  *writer.Writer
	deriver *cohort.Deriver
	rollups *rollup.Deriver
	project testsupport.Project
	ch      *chdb.Conn
}

func newReporting(t *testing.T, tier string) *reporting {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.EventBusDir = t.TempDir()
	settings.WriterFlushInterval = 20 * time.Millisecond

	db := testsupport.Postgres(t, settings)
	cache := testsupport.Cache(t, settings)
	ch := testsupport.ClickHouse(t, settings)

	seeded := testsupport.SeedProject(t, db, tier)
	reportingSecret := testsupport.SeedKey(t, db, core.KindReporting, seeded.EnvironmentID)

	bus := eventbus.NewLocal(settings.EventBusDir)
	if err := bus.Start(t.Context()); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Stop(context.Background()) })
	bus.SetPollInterval(settings.WriterFlushInterval)

	resolver := core.NewKeyResolver(db, cache, core.KeyResolverOptions{
		TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Second, MaxLocal: 100,
	})

	gateway := muzak.New(muzak.AppOptions{
		Title: "ingest", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(bus)),
	)
	gateway.Include(routers.Ingest())

	// One Firebase identity, in the seeded organization, holding the narrowest role that may
	// read analytics. The verifier is the real one against a local certificate endpoint, which
	// is the only part of the sign-in that is not production's.
	google := fbtest.New(t, settings.FirebaseProjectID)
	verifier := firebase.NewVerifier(settings.FirebaseProjectID)
	verifier.Keys.URL = google.URL
	reader_person := fbtest.Person{
		UID: "uid-" + uuid.NewString()[:12], Email: "growth-" + uuid.NewString()[:8] + "@studio.example",
		Name: "Grace Hopper", EmailVerified: true, Provider: "google.com",
	}
	testsupport.SeedMember(t, db, seeded.OrgSeq, identity.Claims{
		UID: reader_person.UID, Email: reader_person.Email,
		EmailVerified: true, Name: reader_person.Name,
	}, authz.RoleGrowthManager)

	reader := muzak.New(muzak.AppOptions{
		Title: "reporting", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(ch),
		muzak.WithSingleton(query.NewEngine(ch, query.DefaultLimits(), settings.QuerySuppressionK).
			WithCache(query.NewCache(cache, settings.QueryCacheTTL, settings.QueryCacheSettledTTL))),
		muzak.WithSingleton(verifier),
	)
	reader.Include(routers.Cohorts())
	reader.Include(routers.Analytics(),
		muzak.WithPrefix("/v1"),
		muzak.Needs(core.ResolveKnownUser))

	consumer := writer.New(bus, cache, ch, quiet(), writer.Options{
		MaxRowsPerInsert: settings.WriterBatchSize,
		DeviceStateTTL:   time.Hour,
	})

	sqlDB := clickhouse.OpenDB(mustParse(t, settings.ClickHouseDSN))
	t.Cleanup(func() { _ = sqlDB.Close() })

	return &reporting{
		settings: settings,
		ingest: testclient.New(t, gateway,
			testclient.WithHeader("Authorization", "Bearer "+seeded.Secret)),
		reader: testclient.New(t, reader,
			testclient.WithHeader("Authorization", "Bearer "+reportingSecret)),
		projectCl: testclient.New(t, reader,
			testclient.WithHeader("Authorization", "Bearer "+seeded.Secret)),
		member: testclient.New(t, reader,
			testclient.WithHeader("Authorization", "Bearer "+google.Token(reader_person))),
		google:  google,
		readerA: reader,
		db:      db,
		writer:  consumer,
		deriver: &cohort.Deriver{DB: sqlDB},
		rollups: &rollup.Deriver{DB: sqlDB},
		project: seeded,
		ch:      ch,
	}
}

func mustParse(t *testing.T, dsn string) *clickhouse.Options {
	t.Helper()
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse clickhouse dsn: %v", err)
	}
	return opts
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (r *reporting) run(t *testing.T) {
	t.Helper()
	if err := r.writer.Start(t.Context()); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.writer.Stop(ctx)
	})
}

// The shape of the seeded population, chosen so the suppression rule has something real to
// act on: thirteen installs on one day, three of them on a platform that will therefore fall
// under k=5 while the cohort as a whole does not.
const (
	iosInstalls     = 10
	androidInstalls = 3
	totalInstalls   = iosInstalls + androidInstalls
	returners       = 7
	returnOffset    = 3
)

// cohortDay is six days ago.
func (r *reporting) cohortDay() time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)
}

// seedCurve writes a multi-day cohort straight into events_raw, then derives.
//
// Not through the gateway, and the reason is a property of the product rather than a
// shortcut. §29.4 assigns a cohort from the server's ingest_time, so every batch a test
// posts today belongs to today's cohort however its events are dated — a real curve takes
// real days to form. Controlling ingest_time is the only way to have one in a test.
//
// Everything downstream of the insert is still the real thing: mv_player_lifetime and
// mv_player_activity_daily fire on these rows, and the real derivation builds every fact
// the API reads. What this skips is the gateway and the writer, and
// TestABatchBecomesAnAnswer covers those against the same API.
func (r *reporting) seedCurve(t *testing.T) {
	t.Helper()
	day := r.cohortDay()

	type row struct {
		install  string
		platform string
		at       time.Time
	}
	var rows []row
	installs := make([]string, 0, totalInstalls)
	for i := range totalInstalls {
		platform := "ios"
		if i >= iosInstalls {
			platform = "android"
		}
		install := uuid.NewString()
		installs = append(installs, install)
		rows = append(rows, row{install, platform, day})
	}
	for i := range returners {
		rows = append(rows, row{installs[i], "ios", day.AddDate(0, 0, returnOffset)})
	}

	conn, err := r.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	batch, err := conn.PrepareBatch(context.Background(), `INSERT INTO events_raw (
		org_seq, project_seq, environment_seq, org_id, project_id, environment_id,
		retention_tier, policy_tier, event_time, ingest_time, received_at,
		event_id, batch_id, session_id, player_id, install_id,
		event_name, event_type, platform, country_code, app_version, sdk_name, sdk_version,
		cohort_date, install_platform, install_country, first_app_version,
		consent_analytics, consent_policy_version, att_status,
		revenue_micros, currency, playtime_ms, payload_bytes)`)
	if err != nil {
		t.Fatalf("prepare seed: %v", err)
	}
	for _, e := range rows {
		// event_time and ingest_time are the same instant, which is what a batch sent
		// promptly looks like. cohort_date is the install day, exactly as the writer would
		// have stamped it from ingest_time on that day.
		at := e.at.Add(9 * time.Hour)
		if err := batch.Append(
			uint32(r.project.OrgSeq), uint32(r.project.ProjectSeq), uint16(r.project.EnvironmentID),
			uuid.New(), uuid.MustParse(r.project.ProjectUID), uuid.New(),
			uint16(30), "standard", at, at, at,
			uuid.New(), uuid.New(), "", "", uuid.MustParse(e.install),
			"session_start", "standard", e.platform, "", "2.7.1", "unity", "1.4.0",
			day, e.platform, "", "2.7.1",
			uint8(1), "2026-01-15", "not_determined",
			int64(0), "", uint64(5*60*1000), uint32(256),
		); err != nil {
			t.Fatalf("append seed row: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send seed: %v", err)
	}

	if got := r.waitForRows(t, len(rows)); got != len(rows) {
		t.Fatalf("seeded %d of %d rows", got, len(rows))
	}
	r.waitForDevices(t, totalInstalls)
	r.derive(t, day, day.AddDate(0, 0, returnOffset))
}

func (r *reporting) post(t *testing.T, install, platform string, at time.Time) {
	t.Helper()
	body := map[string]any{
		"schema_version": 1,
		"batch_id":       uuid.NewString(),
		"environment":    "production",
		"install_id":     install,
		"sdk":            map[string]any{"name": "unity", "version": "1.4.0"},
		"app":            map[string]any{"version": "2.7.1", "build": "342"},
		"device":         map[string]any{"platform": platform, "os_version": "18.2"},
		"consent": map[string]any{
			"analytics": true, "policy_version": "2026-01-15", "att_status": "not_determined",
		},
		"events": []map[string]any{{
			"event_id":   uuid.NewString(),
			"event_name": "session_start",
			"event_time": at.Format(time.RFC3339Nano),
		}},
	}
	r.ingest.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)
}

// derive runs the nightly job over every month the seeded window touches.
func (r *reporting) derive(t *testing.T, from, to time.Time) {
	t.Helper()
	ctx := context.Background()
	months := map[string]cohort.Month{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		m := cohort.MonthOf(d)
		months[m.Partition()] = m
	}
	for _, m := range months {
		if err := r.deriver.DeriveMonth(ctx, m); err != nil {
			t.Fatalf("derive %s: %v", m, err)
		}
		if err := r.deriver.DeriveSizes(ctx, m, time.Now().UTC()); err != nil {
			t.Fatalf("derive sizes %s: %v", m, err)
		}
		if err := r.deriver.DeriveBuckets(ctx, m); err != nil {
			t.Fatalf("derive buckets %s: %v", m, err)
		}
		if err := r.rollups.DeriveMonth(ctx, m); err != nil {
			t.Fatalf("derive metrics %s: %v", m, err)
		}
		if err := r.rollups.DeriveSessions(ctx, m); err != nil {
			t.Fatalf("derive sessions %s: %v", m, err)
		}
		if err := r.rollups.DeriveLifecycle(ctx, m); err != nil {
			t.Fatalf("derive lifecycle %s: %v", m, err)
		}
	}
}

func (r *reporting) waitForRows(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var got uint64
	for time.Now().Before(deadline) {
		got = r.countRows(t)
		if int(got) >= want {
			return int(got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return int(got)
}

// waitForDevices waits for the derivation's *input* to be complete.
//
// waitForRows watches events_raw, which is the wrong precondition: the derivation reads
// player_activity_daily, and a materialized view's target becomes visible a moment after the
// table it reads from. Deriving in that window produces a correct answer about incomplete
// data — a cohort short one player, with nothing anywhere reporting a problem.
//
// It costs nothing in production, where the job runs nightly and is idempotent. It costs a
// flaky test here, which is how it was noticed.
func (r *reporting) waitForDevices(t *testing.T, want int) {
	t.Helper()
	conn, err := r.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var got uint64
	for time.Now().Before(deadline) {
		if err := conn.QueryRow(context.Background(),
			"SELECT uniqExact(device_id) FROM player_activity_daily WHERE project_seq = ?",
			uint32(r.project.ProjectSeq)).Scan(&got); err != nil {
			t.Fatalf("count devices: %v", err)
		}
		if int(got) >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the derivation's input settled at %d of %d devices", got, want)
}

func (r *reporting) countRows(t *testing.T) uint64 {
	t.Helper()
	conn, err := r.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	var n uint64
	if err := conn.QueryRow(context.Background(),
		"SELECT count() FROM events_raw WHERE project_seq = ?", uint32(r.project.ProjectSeq)).
		Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// ask sends a cohort query and decodes the answer.
func (r *reporting) ask(t *testing.T, body map[string]any) protocol.CohortResult {
	t.Helper()
	res := r.reader.Post("/v1/cohorts/query", testclient.JSON(body))
	res.AssertStatus(http.StatusOK)
	var out protocol.CohortResult
	res.JSON(&out)
	return out
}

// series sends a time-series query and decodes the answer.
func (r *reporting) series(t *testing.T, body map[string]any) protocol.MetricResult {
	t.Helper()
	res := r.reader.Post("/v1/metrics/query", testclient.JSON(body))
	res.AssertStatus(http.StatusOK)
	var out protocol.MetricResult
	res.JSON(&out)
	return out
}

// join adds a person to the seeded organization with one of its roles and returns their client.
//
// Each call is a different identity, so a test that gives "two people" different roles is asserting
// about two people rather than about whichever write won.
func (r *reporting) join(t *testing.T, roleKey string) *testclient.Client {
	t.Helper()
	who := fbtest.Person{
		UID:           "uid-" + uuid.NewString()[:12],
		Email:         roleKey + "-" + uuid.NewString()[:8] + "@studio.example",
		Name:          "Ada Lovelace",
		EmailVerified: true,
		Provider:      "google.com",
	}
	testsupport.SeedMember(t, r.db, r.project.OrgSeq, identity.Claims{
		UID: who.UID, Email: who.Email, EmailVerified: true, Name: who.Name,
	}, roleKey)
	return testclient.New(t, r.readerA,
		testclient.WithHeader("Authorization", "Bearer "+r.google.Token(who)))
}

// outsider is signed in, real, and a member of nothing here.
func (r *reporting) outsider(t *testing.T) *testclient.Client {
	t.Helper()
	who := fbtest.Person{
		UID: "uid-" + uuid.NewString()[:12], Email: uuid.NewString()[:8] + "@elsewhere.example",
		Name: "Stranger", EmailVerified: true, Provider: "google.com",
	}
	// Given an account — in an organization of their own — so that the refusal below is about
	// the access list rather than about the account not existing.
	if _, err := identity.EnsureUser(context.Background(), r.db.Client, identity.Claims{
		UID: who.UID, Email: who.Email, EmailVerified: true, Name: who.Name,
	}, time.Now()); err != nil {
		t.Fatalf("seed the outsider: %v", err)
	}
	return testclient.New(t, r.readerA,
		testclient.WithHeader("Authorization", "Bearer "+r.google.Token(who)))
}

// restrict takes every project away from the most recently joined member.
//
// `all_projects` false with no grants is the narrowest possible access list, which is what makes it
// the right shape for the test: the member is in the organization and holds the permission, and the
// only thing missing is this project.
func (r *reporting) restrict(t *testing.T, client *testclient.Client) {
	t.Helper()
	// The client carries the token; the membership is found by the most recent join, which is
	// the one the caller just made.
	members, err := r.db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationID(r.project.OrgSeq)).
		Order(ent.Desc(organizationmember.FieldID)).
		All(context.Background())
	if err != nil || len(members) == 0 {
		t.Fatalf("read members: %v", err)
	}
	if _, err := r.db.Client.OrganizationMember.UpdateOneID(members[0].ID).
		SetAllProjects(false).Save(context.Background()); err != nil {
		t.Fatalf("restrict the member: %v", err)
	}
}

// countUsers is how many accounts exist in the global tier.
func (r *reporting) countUsers(t *testing.T) int {
	t.Helper()
	n, err := r.db.Client.User.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

// analytics is the dashboard's base path for this project's production environment.
func (r *reporting) analytics() string {
	return "/v1/organizations/" + r.project.OrgSlug +
		"/projects/" + r.project.ProjectSlug + "/analytics/production"
}

// askAsMember is [reporting.ask] with the dashboard's credential instead of a key.
func (r *reporting) askAsMember(t *testing.T, body map[string]any) protocol.CohortResult {
	t.Helper()
	res := r.member.Post(r.analytics()+"/cohorts/query", testclient.JSON(body))
	res.AssertStatus(http.StatusOK)
	var out protocol.CohortResult
	res.JSON(&out)
	return out
}

// seriesAsMember is [reporting.series] with the dashboard's credential.
func (r *reporting) seriesAsMember(t *testing.T, body map[string]any) protocol.MetricResult {
	t.Helper()
	res := r.member.Post(r.analytics()+"/metrics/query", testclient.JSON(body))
	res.AssertStatus(http.StatusOK)
	var out protocol.MetricResult
	res.JSON(&out)
	return out
}

// seriesQuery covers the window seedCurve populated.
func (r *reporting) seriesQuery(over map[string]any) map[string]any {
	q := map[string]any{
		"metric":   "active_players",
		"from":     r.cohortDay().Format("2006-01-02"),
		"to":       r.cohortDay().AddDate(0, 0, returnOffset).Format("2006-01-02"),
		"interval": "day",
	}
	for k, v := range over {
		q[k] = v
	}
	return q
}

// retentionQuery is the plain shape, over the day seedCurve used.
func (r *reporting) retentionQuery(over map[string]any) map[string]any {
	day := r.cohortDay().Format("2006-01-02")
	q := map[string]any{
		"metric":          "retention",
		"retention_basis": "classic",
		"cohort":          map[string]any{"from": day, "to": day},
		"offsets":         []int{0, returnOffset},
	}
	for k, v := range over {
		q[k] = v
	}
	return q
}

// today is the cohort a batch posted now belongs to (§29.4).
func today() string { return time.Now().UTC().Format("2006-01-02") }

// TestABatchBecomesAnAnswer is the whole product in one test: a batch is posted to the real
// gateway, stored by the real writer, derived by the real nightly job, and read back
// through the real reporting API.
//
// One day rather than a curve, because §29.4 assigns a cohort from the server's ingest
// time — everything posted now is today's cohort, and a curve takes real days. Six devices
// rather than one, because five is the suppression threshold and a cohort of one is
// correctly refused.
func TestABatchBecomesAnAnswer(t *testing.T) {
	r := newReporting(t, "standard")
	r.run(t)

	const installs = 6
	for range installs {
		r.post(t, uuid.NewString(), "ios", time.Now().UTC())
	}
	if got := r.waitForRows(t, installs); got != installs {
		t.Fatalf("stored %d of %d batches", got, installs)
	}
	r.waitForDevices(t, installs)
	now := time.Now().UTC()
	r.derive(t, now, now)

	result := r.ask(t, map[string]any{
		"metric":          "retention",
		"retention_basis": "classic",
		"cohort":          map[string]any{"from": today(), "to": today()},
		"offsets":         []int{0},
	})

	var found bool
	for _, row := range result.Rows {
		if row.DayOffset != 0 {
			continue
		}
		found = true
		if row.Players < installs || row.CohortSize < installs {
			t.Fatalf("day 0: %d of %d players, want at least %d of %d",
				row.Players, row.CohortSize, installs, installs)
		}
		if row.Rate != 1 {
			t.Fatalf("every installed player was active on day 0, but the rate is %v", row.Rate)
		}
	}
	if !found {
		t.Fatalf("the batch never reached an answer: %+v", result)
	}
}

// TestARetentionCurveIsReadBackCorrectly is the numbers, over a real multi-day window.
func TestARetentionCurveIsReadBackCorrectly(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	result := r.ask(t, r.retentionQuery(nil))

	byOffset := map[int]protocol.CohortRow{}
	for _, row := range result.Rows {
		byOffset[row.DayOffset] = row
	}

	d0, ok := byOffset[0]
	if !ok {
		t.Fatalf("no day-0 cell in %+v", result.Rows)
	}
	if d0.Players != totalInstalls || d0.CohortSize != totalInstalls {
		t.Fatalf("day 0: %d of %d players, want %d of %d",
			d0.Players, d0.CohortSize, totalInstalls, totalInstalls)
	}
	if d0.Rate != 1 {
		t.Fatalf("day 0 retention is %v, want 1", d0.Rate)
	}

	d3, ok := byOffset[returnOffset]
	if !ok {
		t.Fatalf("no day-%d cell in %+v", returnOffset, result.Rows)
	}
	if d3.Players != returners {
		t.Fatalf("day %d: %d players, want %d", returnOffset, d3.Players, returners)
	}
	if want := float64(returners) / float64(totalInstalls); d3.Rate != want {
		t.Fatalf("day %d retention is %v, want %v", returnOffset, d3.Rate, want)
	}
}

// TestAnEventCurveCountsTheShareThatHasDoneIt is metric=event against real data.
//
// Every seeded row is a session_start, so the cohort did it thirteen times on day 0 and seven on
// day 3. "Had done it by day N" is a distinct count across the days, which is why it stays at
// thirteen on day 3 rather than becoming twenty: the seven who came back had already done it.
func TestAnEventCurveCountsTheShareThatHasDoneIt(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	day := r.cohortDay().Format("2006-01-02")
	result := r.ask(t, map[string]any{
		"metric":  "event",
		"event":   "session_start",
		"cohort":  map[string]any{"from": day, "to": day},
		"offsets": []int{0, returnOffset, returnOffset + 1},
	})

	byOffset := map[int]protocol.CohortRow{}
	for _, row := range result.Rows {
		byOffset[row.DayOffset] = row
	}
	d0, d3, d4 := byOffset[0], byOffset[returnOffset], byOffset[returnOffset+1]
	if d0.Players != totalInstalls || d0.Events != totalInstalls || d0.PlayersCumulative != totalInstalls {
		t.Fatalf("day 0: %+v, want %d players, events and players so far", d0, totalInstalls)
	}
	if d3.Players != returners || d3.Events != returners || d3.PlayersCumulative != totalInstalls {
		t.Fatalf("day %d: %+v, want %d players and events, %d so far", returnOffset, d3, returners, totalInstalls)
	}
	if d3.Rate != 1 {
		t.Fatalf("day %d share is %v; everybody had done it on day 0", returnOffset, d3.Rate)
	}
	if d4.Players != 0 || d4.PlayersCumulative != totalInstalls {
		t.Fatalf("day %d: %+v, want nobody that day and %d so far", returnOffset+1, d4, totalInstalls)
	}
}

// TestSessionsPerInstallAreOnEveryCohortRow, whole and split.
func TestSessionsPerInstallAreOnEveryCohortRow(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	whole := r.ask(t, r.retentionQuery(nil))
	for _, row := range whole.Rows {
		want := uint64(totalInstalls)
		if row.DayOffset == returnOffset {
			want = returners
		}
		if row.Sessions != want {
			t.Errorf("day %d carries %d sessions, want %d", row.DayOffset, row.Sessions, want)
		}
	}

	split := r.ask(t, r.retentionQuery(map[string]any{
		"breakdown": map[string]any{"dimension": "install_platform"},
	}))
	for _, row := range split.Rows {
		if row.DimValue == "ios" && row.DayOffset == 0 && row.Sessions != iosInstalls {
			t.Errorf("iOS day 0 carries %d sessions, want %d", row.Sessions, iosInstalls)
		}
	}
}

// TestAMonthCohortHoldsTheDaysAskedFor.
func TestAMonthCohortHoldsTheDaysAskedFor(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	day := r.cohortDay()
	result := r.ask(t, r.retentionQuery(map[string]any{
		"cohort": map[string]any{
			"from": day.Format("2006-01-02"), "to": day.Format("2006-01-02"), "grain": "month",
		},
	}))
	first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	if len(result.Rows) == 0 {
		t.Fatal("a month cohort asked for from the seeded day came back empty")
	}
	for _, row := range result.Rows {
		if !row.CohortDate.Equal(first) {
			t.Errorf("a month cohort is keyed on %s, want %s", row.CohortDate.Format("2006-01-02"), first.Format("2006-01-02"))
		}
		if row.DayOffset == 0 && (row.Players != totalInstalls || row.CohortSize != totalInstalls) {
			t.Errorf("the month's day 0 is %d of %d, want %d of %d", row.Players, row.CohortSize, totalInstalls, totalInstalls)
		}
	}
}

// TestAThinBreakdownValueIsSuppressedButStillRollsUp is ADR 0017's central claim, proved
// against real data rather than a constructed row.
//
// Three Android installs is a cell that identifies three people, so it is withheld. Those
// three are still in the total, because suppression happens at query time and the stored
// cells stay complete — which is the entire reason the record refuses to suppress at write
// time.
func TestAThinBreakdownValueIsSuppressedButStillRollsUp(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	split := r.ask(t, r.retentionQuery(map[string]any{
		"breakdown": map[string]any{"dimension": "install_platform"},
	}))

	seen := map[string]uint64{}
	for _, row := range split.Rows {
		if row.DayOffset == 0 {
			seen[row.DimValue] = row.CohortSize
		}
	}
	if seen["ios"] != iosInstalls {
		t.Fatalf("ios cohort is %d, want %d", seen["ios"], iosInstalls)
	}
	if _, disclosed := seen["android"]; disclosed {
		t.Fatalf("a cohort of %d Android players was disclosed under k=%d",
			androidInstalls, split.SuppressionThreshold)
	}
	if split.SuppressedCells == 0 {
		t.Fatal("cells were withheld without saying so, which reads as a genuine zero")
	}

	// The withheld three are still counted in the unbroken-down curve.
	total := r.ask(t, r.retentionQuery(nil))
	for _, row := range total.Rows {
		if row.DayOffset == 0 && row.CohortSize != totalInstalls {
			t.Fatalf("the total lost the suppressed cell: %d, want %d",
				row.CohortSize, totalInstalls)
		}
	}
}

// TestRollingRetentionCountsAPlayerOnce is §29.10.
//
// The seeded devices are active on day 0 and day 3, so rolling D0 must be the whole cohort
// — not thirteen plus seven. Summing the per-offset counters is exactly how it becomes
// twenty, and that is the bug this shape exists to prevent.
func TestRollingRetentionCountsAPlayerOnce(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	result := r.ask(t, r.retentionQuery(map[string]any{"retention_basis": "rolling"}))

	for _, row := range result.Rows {
		if row.DayOffset != 0 {
			continue
		}
		if row.Players != totalInstalls {
			t.Fatalf("rolling D0 counts %d players from a cohort of %d; a device active on "+
				"two days was counted twice", row.Players, totalInstalls)
		}
	}

	// Rolling D3 is everyone whose last activity was day 3 or later: the seven returners.
	for _, row := range result.Rows {
		if row.DayOffset == returnOffset && row.Players != returners {
			t.Fatalf("rolling D%d counts %d, want %d", returnOffset, row.Players, returners)
		}
	}
}

// TestTheResponseSaysHowFreshAndHowCensoredItIs is §57.1's provenance contract.
func TestTheResponseSaysHowFreshAndHowCensoredItIs(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	result := r.ask(t, r.retentionQuery(nil))

	if result.ComputedAt.IsZero() {
		t.Fatal("the response does not say when the facts were derived")
	}
	if result.SuppressionThreshold != r.settings.QuerySuppressionK {
		t.Fatalf("threshold reported as %d, configured as %d",
			result.SuppressionThreshold, r.settings.QuerySuppressionK)
	}
	// The cohort is six days old, so the curve cannot yet be claimed beyond that.
	if result.CohortMaturityDays < 5 || result.CohortMaturityDays > 7 {
		t.Fatalf("a six-day-old cohort reports %d days of maturity", result.CohortMaturityDays)
	}
}

// TestACachedAnswerKeepsTheDerivationsTimestamp is §57.1's provenance rule meeting §58's
// cache.
//
// computed_at says when the *facts* were built, so re-deriving and asking again must return
// the timestamp the cached answer was built from — not the new one, and not the moment the
// response was assembled. Getting this wrong is how a cache makes stale numbers look fresh,
// which is worse than serving them slowly.
//
// It doubles as the proof that the cache is actually used: DeriveSizes stamps computed_at
// with now(), so an unchanged timestamp after a re-derivation could only have come from the
// cache.
func TestACachedAnswerKeepsTheDerivationsTimestamp(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	first := r.ask(t, r.retentionQuery(nil))
	if first.ComputedAt.IsZero() {
		t.Fatal("the first answer has no derivation timestamp")
	}

	// A second derivation, a second later, so the stamp genuinely moves.
	time.Sleep(1100 * time.Millisecond)
	day := r.cohortDay()
	r.derive(t, day, day.AddDate(0, 0, returnOffset))

	second := r.ask(t, r.retentionQuery(nil))
	if !second.ComputedAt.Equal(first.ComputedAt) {
		t.Fatalf("the cached answer reported %s after a re-derivation at %s; either the "+
			"cache was not used or it rewrote the provenance",
			second.ComputedAt.Format(time.RFC3339), first.ComputedAt.Format(time.RFC3339))
	}
}

// TestOnlyAReportingKeyMayQuery is §52.
//
// A project key ships inside a game and is therefore public. Accepting one here would
// publish every studio's analytics to anyone who unpacked their build.
func TestOnlyAReportingKeyMayQuery(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.projectCl.Post("/v1/cohorts/query", testclient.JSON(r.retentionQuery(nil)))
	res.AssertStatus(http.StatusForbidden)
}

// TestScopeInTheBodyIsRefused is Appendix C #3.
//
// Refused rather than ignored: a client that sends project_id believes it selected a
// project, and silently answering about a different one is worse than any error.
func TestScopeInTheBodyIsRefused(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.reader.Post("/v1/cohorts/query", testclient.JSON(r.retentionQuery(map[string]any{"project_id": uuid.NewString()})))
	res.AssertStatus(http.StatusUnprocessableEntity)
}

// TestTwoDimensionsAtOnceIsRefusedWithAReason is §29.13 reaching the wire.
func TestTwoDimensionsAtOnceIsRefusedWithAReason(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.reader.Post("/v1/cohorts/query", testclient.JSON(r.retentionQuery(map[string]any{
		"breakdown": map[string]any{"dimension": "install_country"},
		"filters": []map[string]any{
			{"field": "install_platform", "op": "eq", "value": "ios"},
		},
	})))
	res.AssertStatus(http.StatusUnprocessableEntity)
}

// TestKidsStrictIsToldWhyRatherThanShownNothing is §29.15.
func TestKidsStrictIsToldWhyRatherThanShownNothing(t *testing.T) {
	r := newReporting(t, "kids_strict")
	res := r.reader.Post("/v1/cohorts/query", testclient.JSON(r.retentionQuery(nil)))
	res.AssertStatus(http.StatusUnprocessableEntity)

	// Same-day aggregates remain available, which is what makes the refusal actionable.
	ok := r.reader.Post("/v1/cohorts/query", testclient.JSON(map[string]any{
		"metric":  "revenue",
		"cohort":  map[string]any{"from": r.cohortDay().Format("2006-01-02"), "to": r.cohortDay().Format("2006-01-02")},
		"offsets": []int{0},
	}))
	ok.AssertStatus(http.StatusOK)
}

// TestADailySeriesIsReadBackCorrectly is the other half of the read path.
//
// The same seeded population as the cohort tests, asked the other kind of question: not "of
// the players who arrived on day C, how many came back" but "how many were active each day".
// Thirteen on the install day, seven three days later, and every one of the thirteen is new
// on the day they arrived.
func TestADailySeriesIsReadBackCorrectly(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	result := r.series(t, r.seriesQuery(nil))
	if result.Interval != "day" || result.Source != "metrics_daily" {
		t.Fatalf("answered from %s at a %s grain", result.Source, result.Interval)
	}
	if result.Approximate {
		t.Fatal("the daily tier is derived and exact; it must not be marked approximate")
	}

	byDay := map[string]protocol.MetricRow{}
	for _, row := range result.Rows {
		byDay[row.Bucket.UTC().Format("2006-01-02")] = row
	}

	install := byDay[r.cohortDay().Format("2006-01-02")]
	if install.ActivePlayers != totalInstalls {
		t.Fatalf("install day: %d active, want %d", install.ActivePlayers, totalInstalls)
	}
	if install.NewPlayers != totalInstalls || install.ReturningPlayers != 0 {
		t.Fatalf("install day: %d new and %d returning, want %d and 0",
			install.NewPlayers, install.ReturningPlayers, totalInstalls)
	}
	if install.Value != float64(totalInstalls) {
		t.Fatalf("value is %v, want the requested metric %d", install.Value, totalInstalls)
	}

	back := byDay[r.cohortDay().AddDate(0, 0, returnOffset).Format("2006-01-02")]
	if back.ActivePlayers != returners {
		t.Fatalf("day %d: %d active, want %d", returnOffset, back.ActivePlayers, returners)
	}
	if back.NewPlayers != 0 || back.ReturningPlayers != returners {
		t.Fatalf("day %d: %d new and %d returning, want 0 and %d",
			returnOffset, back.NewPlayers, back.ReturningPlayers, returners)
	}
}

// TestMonthlyActivesAreNotTheSumOfTheDays is the property a summed DAU would break.
//
// The seeded month has twenty device-days across thirteen devices, because seven of them
// came back. MAU is thirteen.
func TestMonthlyActivesAreNotTheSumOfTheDays(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	result := r.series(t, r.seriesQuery(map[string]any{"interval": "month"}))
	if result.Source != "metrics_monthly" {
		t.Fatalf("a month grain read %s", result.Source)
	}

	var mau uint64
	for _, row := range result.Rows {
		mau += row.ActivePlayers
	}
	if mau != totalInstalls {
		t.Fatalf("MAU is %d; %d devices were active, %d of them twice",
			mau, totalInstalls, returners)
	}
}

// TestAMetricBreakdownSuppressesAThinValue is §32.2 on the time-series path.
//
// A day's active players split by platform is still a count of people, and three of them in
// one cell identify three people exactly as well as a cohort cell would.
func TestAMetricBreakdownSuppressesAThinValue(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	split := r.series(t, r.seriesQuery(map[string]any{
		"group_by": []string{"install_platform"},
	}))
	if split.Source != "metrics_breakdown_daily" {
		t.Fatalf("a grouped query read %s", split.Source)
	}

	seen := map[string]uint64{}
	for _, row := range split.Rows {
		if row.Bucket.UTC().Format("2006-01-02") == r.cohortDay().Format("2006-01-02") {
			seen[row.DimValue] = row.ActivePlayers
		}
	}
	if seen["ios"] != iosInstalls {
		t.Fatalf("ios shows %d active, want %d", seen["ios"], iosInstalls)
	}
	if _, disclosed := seen["android"]; disclosed {
		t.Fatalf("%d Android players were disclosed under k=%d",
			androidInstalls, split.SuppressionThreshold)
	}
	if split.SuppressedCells == 0 {
		t.Fatal("cells were withheld without saying so")
	}
}

// TestAGrainFinerThanItsRangeIsRefused is §26 enforced rather than advised.
//
// A minute grain over ninety days is 129,600 points. Nothing draws that, and answering it
// would read the minute tier for a quarter to produce a chart nobody can look at.
func TestAGrainFinerThanItsRangeIsRefused(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.reader.Post("/v1/metrics/query", testclient.JSON(map[string]any{
		"metric":   "active_players",
		"from":     time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02"),
		"to":       time.Now().UTC().Format("2006-01-02"),
		"interval": "minute",
	}))
	res.AssertStatus(http.StatusUnprocessableEntity)
}

// TestARealtimeGrainCannotAnswerAPlayerLifecycleQuestion.
//
// events_1m and events_1h are built from insert blocks. A block knows how many events it
// holds; it does not know whether the device sending them arrived today, because that is a
// property of the player. Refused with the grain that can answer it.
func TestARealtimeGrainCannotAnswerAPlayerLifecycleQuestion(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.reader.Post("/v1/metrics/query", testclient.JSON(map[string]any{
		"metric":   "new_players",
		"from":     time.Now().UTC().Format("2006-01-02"),
		"to":       time.Now().UTC().Format("2006-01-02"),
		"interval": "hour",
	}))
	res.AssertStatus(http.StatusUnprocessableEntity)
}

// TestARealtimeSeriesSaysItIsApproximate is §27's trade, made visible.
//
// The minute and hour tiers count players with a sketch. That is the right trade for a chart
// of the last hour and the wrong one for a number compared against an invoice, so the answer
// has to say which it is.
func TestARealtimeSeriesSaysItIsApproximate(t *testing.T) {
	r := newReporting(t, "standard")
	r.run(t)
	r.post(t, uuid.NewString(), "ios", time.Now().UTC())
	if got := r.waitForRows(t, 1); got != 1 {
		t.Fatalf("stored %d rows", got)
	}

	result := r.series(t, map[string]any{
		"metric":   "events",
		"from":     time.Now().UTC().Format("2006-01-02"),
		"to":       time.Now().UTC().Format("2006-01-02"),
		"interval": "hour",
	})
	// The whole project reads the tier that carries its total; one event reads the tier keyed
	// by event name. Both are sketches.
	if result.Source != "activity_1h" {
		t.Fatalf("an hour grain for the whole project read %s", result.Source)
	}
	if !result.Approximate {
		t.Fatal("a sketch-counted series did not say it was approximate")
	}
	if !result.ComputedAt.IsZero() {
		t.Fatal("a continuously updated view reported a derivation time it does not have")
	}

	split := r.series(t, map[string]any{
		"metric":   "events",
		"from":     time.Now().UTC().Format("2006-01-02"),
		"to":       time.Now().UTC().Format("2006-01-02"),
		"interval": "hour",
		"group_by": []string{"event_name"},
	})
	if split.Source != "events_1h" || !split.Approximate {
		t.Fatalf("an hour grain split by event read %s (approximate=%v)", split.Source, split.Approximate)
	}
	var total float64
	for _, row := range split.Rows {
		if row.DimValue == "" {
			t.Fatalf("a row of a split by event names no event: %+v", row)
		}
		total += row.Value
	}
	var whole float64
	for _, row := range result.Rows {
		whole += row.Value
	}
	if total != whole {
		t.Fatalf("the events split by name sum to %v, and the whole project counted %v", total, whole)
	}
}

// TestKidsStrictHasNoNewReturningSplit is §29.15 on the time-series path.
//
// §98 gives every batch a fresh installation identifier, so every player looks new every
// day. The number would be confidently wrong rather than missing, which is the worst kind.
func TestKidsStrictHasNoNewReturningSplit(t *testing.T) {
	r := newReporting(t, "kids_strict")

	for _, body := range []map[string]any{
		{"metric": "new_players", "interval": "day"},
		{"metric": "active_players", "interval": "month"},
	} {
		body["from"] = r.cohortDay().Format("2006-01-02")
		body["to"] = time.Now().UTC().Format("2006-01-02")
		res := r.reader.Post("/v1/metrics/query", testclient.JSON(body))
		res.AssertStatus(http.StatusUnprocessableEntity)
	}

	// Active players per day is a same-day aggregate and stays available.
	ok := r.reader.Post("/v1/metrics/query", testclient.JSON(map[string]any{
		"metric":   "active_players",
		"from":     r.cohortDay().Format("2006-01-02"),
		"to":       time.Now().UTC().Format("2006-01-02"),
		"interval": "day",
	}))
	ok.AssertStatus(http.StatusOK)
}

// TestTwoDimensionsInASeriesAreRefused is §29.13 on the summary tables.
func TestTwoDimensionsInASeriesAreRefused(t *testing.T) {
	r := newReporting(t, "standard")
	res := r.reader.Post("/v1/metrics/query", testclient.JSON(r.seriesQuery(map[string]any{
		"group_by": []string{"install_country"},
		"filters": []map[string]any{
			{"field": "install_platform", "op": "eq", "value": "ios"},
		},
	})))
	res.AssertStatus(http.StatusUnprocessableEntity)
}

// The dashboard's own reporting path: the same questions, the credential a browser can hold.
//
// §52 makes a reporting key a secret. A browser cannot hold one — shipping it to the page publishes
// every studio's analytics to anybody who opens the network tab — so until these routes existed the
// dashboard had no way to read the numbers the product exists to produce, and `analytics.read` was a
// permission guarding nothing.

// TestAPersonAndAKeyGetTheSameAnswer is the whole claim of this surface.
//
// Not "it returns something plausible": the *same* numbers, from one seeding, through one planner.
// Two reporting paths that could disagree would be two products, and the one a studio checks
// against the other is the one they stop trusting.
func TestAPersonAndAKeyGetTheSameAnswer(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	byKey := r.ask(t, r.retentionQuery(nil))
	byPerson := r.askAsMember(t, r.retentionQuery(nil))

	if len(byKey.Rows) == 0 {
		t.Fatal("the key path returned no rows, so there is nothing to compare")
	}
	if len(byPerson.Rows) != len(byKey.Rows) {
		t.Fatalf("person saw %d rows, key saw %d", len(byPerson.Rows), len(byKey.Rows))
	}
	for i := range byKey.Rows {
		k, p := byKey.Rows[i], byPerson.Rows[i]
		if k.DayOffset != p.DayOffset || k.CohortSize != p.CohortSize || k.Players != p.Players {
			t.Errorf("offset %d: key %+v, person %+v", k.DayOffset, k, p)
		}
	}
	// And the provenance, which is what a dashboard puts beside the chart: a person must not be
	// told an answer is fresher or less censored than the same answer given to a key.
	if byPerson.ComputedAt != byKey.ComputedAt ||
		byPerson.SuppressedCells != byKey.SuppressedCells ||
		byPerson.SuppressionThreshold != byKey.SuppressionThreshold {
		t.Errorf("provenance differs:\nkey    %+v\nperson %+v", byKey, byPerson)
	}

	// And the time series, which reads a different family of tables through the same route shape.
	seriesByKey := r.series(t, r.seriesQuery(nil))
	seriesByPerson := r.seriesAsMember(t, r.seriesQuery(nil))
	if len(seriesByPerson.Rows) != len(seriesByKey.Rows) {
		t.Fatalf("person saw %d series rows, key saw %d",
			len(seriesByPerson.Rows), len(seriesByKey.Rows))
	}
	for i := range seriesByKey.Rows {
		if seriesByKey.Rows[i].Value != seriesByPerson.Rows[i].Value {
			t.Errorf("row %d: key %v, person %v", i,
				seriesByKey.Rows[i].Value, seriesByPerson.Rows[i].Value)
		}
	}
	// The planner's own report, because §57's point is that it chooses the table and the client
	// does not have to know — which only holds if both callers are told the same choice.
	if seriesByPerson.Source != seriesByKey.Source ||
		seriesByPerson.Interval != seriesByKey.Interval {
		t.Errorf("the planner chose differently: key %s/%s, person %s/%s",
			seriesByKey.Source, seriesByKey.Interval,
			seriesByPerson.Source, seriesByPerson.Interval)
	}
}

// TestAnalyticsReadIsWhatDecides is the permission, observed.
//
// A DEVELOPER holds analytics.read and a LIVEOPS MANAGER deliberately does not: reading what every
// player did is a different privilege from scheduling what they will see, and a role whose name says
// LiveOps must not quietly also read revenue. That pair is the whole point of the permission
// existing, and until this route it decided nothing.
func TestAnalyticsReadIsWhatDecides(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	developer := r.join(t, authz.RoleDeveloper)
	if got := developer.Post(r.analytics()+"/cohorts/query",
		testclient.JSON(r.retentionQuery(nil))); got.Status != http.StatusOK {
		t.Errorf("a developer holding analytics.read got %d: %s", got.Status, got)
	}

	manager := r.join(t, authz.RoleLiveOpsManager)
	refused := manager.Post(r.analytics()+"/cohorts/query",
		testclient.JSON(r.retentionQuery(nil)))
	refused.AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.AnalyticsRead)) {
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}
}

// TestSomebodyElsesProjectIsAbsentRatherThanForbidden is the tenancy check, and it is the reason the
// access list is consulted before the permission.
//
// A 403 would confirm the project exists, which turns this endpoint into a way to enumerate a
// competitor's games by guessing names. A stranger and a colleague who was not given the project get
// the same answer.
func TestSomebodyElsesProjectIsAbsentRatherThanForbidden(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	stranger := r.outsider(t)
	stranger.Post(r.analytics()+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusNotFound)

	// A member of the organization whose access list does not include this project. Same answer:
	// "the owner chooses which people see which apps" has to be indistinguishable from the
	// project not existing, or the names of the games they were not shown leak.
	restricted := r.join(t, authz.RoleDeveloper)
	r.restrict(t, restricted)
	restricted.Post(r.analytics()+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusNotFound)
}

// TestAnUnknownEnvironmentIsRefusedRatherThanEmpty keeps the one caller-named part of the scope
// honest.
//
// A person names an environment because a dashboard switches between them, unlike a key, which is
// minted for one. Naming one the project does not have has to be an error: answering with no rows
// would read as "nothing happened in staging" rather than "there is no staging".
func TestAnUnknownEnvironmentIsRefusedRatherThanEmpty(t *testing.T) {
	r := newReporting(t, "standard")
	r.seedCurve(t)

	// The fixture seeds production only.
	base := "/v1/organizations/" + r.project.OrgSlug +
		"/projects/" + r.project.ProjectSlug + "/analytics/staging"
	r.member.Post(base+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusNotFound)

	// And one that is not an environment at all is refused by validation, so the set of names
	// is stated once rather than discovered.
	bad := "/v1/organizations/" + r.project.OrgSlug +
		"/projects/" + r.project.ProjectSlug + "/analytics/prod"
	r.member.Post(bad+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestScopeInTheBodyIsRefusedForAPersonToo is Appendix C #3 on the new path.
//
// The scope moved from the credential to the path, and the decoys have to survive that move: a
// client sending project_id believes it selected a project, and silently answering about a different
// one is worse than any error.
func TestScopeInTheBodyIsRefusedForAPersonToo(t *testing.T) {
	r := newReporting(t, "standard")
	r.member.Post(r.analytics()+"/cohorts/query", testclient.JSON(
		r.retentionQuery(map[string]any{"project_id": uuid.NewString()}))).
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestTheDashboardStillNeedsToBeSignedIn is the door itself.
func TestTheDashboardStillNeedsToBeSignedIn(t *testing.T) {
	r := newReporting(t, "standard")
	anon := testclient.New(t, r.readerA)

	anon.Post(r.analytics()+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusUnauthorized)

	// A reporting key is not a sign-in. It works on its own endpoint and not on this one: the
	// two credentials are not interchangeable, which is what keeps "who is asking" answerable.
	r.reader.Post(r.analytics()+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusUnauthorized)
}

// TestReportingDoesNotCreateAccounts is the boundary [core.ResolveKnownUser] exists for.
//
// `identity.EnsureUser` writes a user row *and a personal organization* on a first sign-in, which
// is right where somebody is signing up and wrong everywhere else. Opening a chart is everywhere
// else: a valid token from a stranger must not grow an organization as a side effect.
func TestReportingDoesNotCreateAccounts(t *testing.T) {
	r := newReporting(t, "standard")

	before := r.countUsers(t)
	newcomer := fbtest.Person{
		UID: "uid-" + uuid.NewString()[:12], Email: uuid.NewString()[:8] + "@studio.example",
		Name: "Nobody", EmailVerified: true, Provider: "google.com",
	}
	client := testclient.New(t, r.readerA,
		testclient.WithHeader("Authorization", "Bearer "+r.google.Token(newcomer)))

	// Refused as "no such organization", which is also what a real member of no organization
	// would be told — so this leaks nothing about whether the slug is real.
	client.Post(r.analytics()+"/cohorts/query", testclient.JSON(r.retentionQuery(nil))).
		AssertStatus(http.StatusNotFound)

	if after := r.countUsers(t); after != before {
		t.Errorf("reading a chart created %d account row(s)", after-before)
	}
}
