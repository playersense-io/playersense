package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/cohort"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
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
	writer    *writer.Writer
	deriver   *cohort.Deriver
	rollups   *rollup.Deriver
	project   testsupport.Project
	ch        *chdb.Conn
}

func newReporting(t *testing.T, tier string) *reporting {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.EventBusDir = t.TempDir()
	settings.WriterFlushInterval = 20 * time.Millisecond

	db := testsupport.Global(t, settings)
	cache := testsupport.Cache(t, settings)
	ch := testsupport.ClickHouse(t, settings)

	home := core.CellFrom(settings)
	seeded := testsupport.SeedProject(t, db, tier, testsupport.SeedCell(t, db, home).ID)
	reportingSecret := testsupport.SeedKey(t, db, core.KindReporting, seeded.EnvironmentID)

	bus := eventbus.NewLocal(home.Slug, settings.EventBusDir)
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
		muzak.WithSingleton(home),
	)
	gateway.Include(routers.Ingest())

	reader := muzak.New(muzak.AppOptions{
		Title: "reporting", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(ch),
		muzak.WithSingleton(query.NewEngine(ch, query.DefaultLimits(), settings.QuerySuppressionK).
			WithCache(query.NewCache(cache, settings.QueryCacheTTL, settings.QueryCacheSettledTTL))),
		muzak.WithSingleton(home),
	)
	reader.Include(routers.Cohorts())

	consumer := writer.New(bus, cache, ch, quiet(), writer.Options{
		Cell:             settings.Cell,
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

func TestAppBuilds(t *testing.T) {
	settings := testsupport.Settings(t)
	app, err := build(settings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := app.Build(); err != nil {
		t.Fatalf("the application does not assemble: %v", err)
	}
}

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
	if result.Source != "events_1h" {
		t.Fatalf("an hour grain read %s", result.Source)
	}
	if !result.Approximate {
		t.Fatal("a sketch-counted series did not say it was approximate")
	}
	if !result.ComputedAt.IsZero() {
		t.Fatal("a continuously updated view reported a derivation time it does not have")
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
