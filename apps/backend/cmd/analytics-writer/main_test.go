package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/valkey"
	"playersense.dev/backend/internal/writer"
)

// pipeline is the whole ingestion path, end to end: the real gateway handler, the real
// bus, the real writer, the real ClickHouse.
//
// This is the first test in the repository that exercises more than one service, and it is
// the one that matters most — every earlier test proves a component does what it says, and
// this one proves the components agree about what they are saying.
type pipeline struct {
	settings config.Settings
	client   *testclient.Client
	bus      *eventbus.Local
	writer   *writer.Writer
	project  testsupport.Project
	cache    *valkey.Client
	ch       *chdb.Conn
}

func newPipeline(t *testing.T, tier string) *pipeline {
	t.Helper()

	settings := testsupport.Settings(t)
	// Each test gets its own spool, so one test's backlog is not another's.
	settings.EventBusDir = t.TempDir()
	settings.WriterFlushInterval = 20 * time.Millisecond

	db := testsupport.Global(t, settings)
	cache := testsupport.Cache(t, settings)
	ch := testsupport.ClickHouse(t, settings)

	home := core.CellFrom(settings)
	seeded := testsupport.SeedProject(t, db, tier, testsupport.SeedCell(t, db, home).ID)

	bus := eventbus.NewLocal(home.Slug, settings.EventBusDir)
	if err := bus.Start(t.Context()); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Stop(context.Background()) })
	bus.SetPollInterval(settings.WriterFlushInterval)

	gateway := muzak.New(muzak.AppOptions{
		Title: "ingest", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(core.NewKeyResolver(db, cache, core.KeyResolverOptions{
			TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Second, MaxLocal: 100,
		})),
		muzak.WithSingleton(ingest.DefaultLimits()),
		muzak.WithSingleton(eventbus.Bus(bus)),
		muzak.WithSingleton(home),
	)
	gateway.Include(routers.Ingest())

	consumer := writer.New(bus, cache, ch, noLogger(), writer.Options{
		Cell:             settings.Cell,
		MaxRowsPerInsert: settings.WriterBatchSize,
		DeviceStateTTL:   time.Hour,
	})

	return &pipeline{
		settings: settings,
		client: testclient.New(t, gateway,
			testclient.WithHeader("Authorization", "Bearer "+seeded.Secret)),
		bus:     bus,
		writer:  consumer,
		project: seeded,
		cache:   cache,
		ch:      ch,
	}
}

// offsets are the committed positions on disk.
func (p *pipeline) offsets(t *testing.T) []string {
	t.Helper()
	found, err := filepath.Glob(
		filepath.Join(p.settings.EventBusDir, p.settings.Cell, ".offsets", "*.offset"))
	if err != nil {
		t.Fatalf("find offsets: %v", err)
	}
	return found
}

// rewind forgets the committed offsets, which is what a crash between the insert and the
// acknowledgement leaves behind: the rows are in ClickHouse and the bus still believes they
// were never delivered.
//
// It waits for an offset to exist first. The rows become visible when the insert block is
// sent and the position is committed immediately afterwards, so a test that has only
// watched for rows is a moment ahead of the thing it wants to undo.
func (p *pipeline) rewind(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(p.offsets(t)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no offset was ever committed; the bus is not tracking its position")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, o := range p.offsets(t) {
		if err := os.Remove(o); err != nil {
			t.Fatalf("rewind %s: %v", o, err)
		}
	}
}

// stopWriter ends the subscription so the spool can be manipulated without racing it.
func (p *pipeline) stopWriter(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.writer.Stop(ctx); err != nil {
		t.Fatalf("stop writer: %v", err)
	}
}

// noLogger keeps the test output about the test.
func noLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// run starts the writer and stops it when the test ends.
func (p *pipeline) run(t *testing.T) {
	t.Helper()
	if err := p.writer.Start(t.Context()); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.writer.Stop(ctx)
	})
}

// waitForRows polls ClickHouse until the project has the expected number of rows, or gives
// up. Polling rather than sleeping: the pipeline is asynchronous by design, and a fixed
// sleep is either flaky or slow.
func (p *pipeline) waitForRows(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got uint64
	for time.Now().Before(deadline) {
		got = p.countRows(t)
		if int(got) >= want {
			return int(got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return int(got)
}

func (p *pipeline) countRows(t *testing.T) uint64 {
	t.Helper()
	conn, err := p.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	var n uint64
	if err := conn.QueryRow(context.Background(),
		"SELECT count() FROM events_raw WHERE project_seq = ?", uint32(p.project.ProjectSeq)).
		Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func batch(events ...map[string]any) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"batch_id":       uuid.NewString(),
		"environment":    "production",
		"install_id":     uuid.NewString(),
		"sdk":            map[string]any{"name": "unity", "version": "1.4.0"},
		"app":            map[string]any{"version": "2.7.1", "build": "342"},
		"device":         map[string]any{"platform": "ios", "os_version": "18.2"},
		"consent": map[string]any{
			"analytics": true, "policy_version": "2026-01-15", "att_status": "not_determined",
		},
		"events": events,
	}
}

func event(name string, over map[string]any) map[string]any {
	e := map[string]any{
		"event_id":   uuid.NewString(),
		"event_name": name,
		"event_time": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range over {
		e[k] = v
	}
	return e
}

func TestAppBuilds(t *testing.T) {
	settings := testsupport.Settings(t)
	app, err := build(settings)
	if err != nil {
		t.Fatalf("build = %v", err)
	}
	if err := app.Build(); err != nil {
		t.Fatalf("Build = %v", err)
	}
}

// TestABatchTravelsFromTheGatewayToClickHouse is the whole pipeline in one test.
//
// Everything before this proved a component does what it claims. This proves the claims
// agree: the gateway's normalized batch is the shape the writer inserts, the tenant it
// resolved is the tenant stored, and the cohort key it never computed is stamped by the
// writer from Valkey (§29.5).
func TestABatchTravelsFromTheGatewayToClickHouse(t *testing.T) {
	p := newPipeline(t, "standard")
	p.run(t)

	p.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", map[string]any{"session_id": "s1",
			"properties": map[string]any{"level": 3, "difficulty": "hard", "premium": true}}),
		event("purchase", map[string]any{"revenue_micros": 990000, "currency": "USD"}),
	))).AssertStatus(http.StatusAccepted)

	if got := p.waitForRows(t, 2); got != 2 {
		t.Fatalf("events_raw holds %d rows for this project, want 2", got)
	}

	conn, _ := p.ch.DB()
	var (
		cohort          *time.Time
		installPlatform string
		firstVersion    string
		consent         uint8
		policyVersion   string
		revenue         int64
	)
	if err := conn.QueryRow(context.Background(), `
		SELECT cohort_date, install_platform, first_app_version,
		       consent_analytics, consent_policy_version, max(revenue_micros)
		FROM events_raw WHERE project_seq = ?
		GROUP BY cohort_date, install_platform, first_app_version,
		         consent_analytics, consent_policy_version`,
		uint32(p.project.ProjectSeq)).
		Scan(&cohort, &installPlatform, &firstVersion, &consent, &policyVersion, &revenue); err != nil {
		t.Fatalf("read back: %v", err)
	}

	// §29.5: the cohort key is stamped at write time, which is what makes every cohort
	// query a single-table GROUP BY.
	if cohort == nil {
		t.Error("cohort_date was not stamped; every cohort query would need the nightly backfill")
	} else if got, want := cohort.Format(time.DateOnly), time.Now().UTC().Format(time.DateOnly); got != want {
		t.Errorf("cohort_date = %s, want %s", got, want)
	}
	if installPlatform != "ios" || firstVersion != "2.7.1" {
		t.Errorf("sticky dimensions = (%q, %q), want (ios, 2.7.1)", installPlatform, firstVersion)
	}
	// §97: the basis travels with the row.
	if consent != 1 || policyVersion != "2026-01-15" {
		t.Errorf("consent = (%d, %q), want (1, 2026-01-15)", consent, policyVersion)
	}
	if revenue != 990000 {
		t.Errorf("revenue_micros = %d, want 990000", revenue)
	}
}

// TestARedeliveredBatchLandsOnce is §21's at-least-once delivery meeting Appendix C.2.
//
// The offset is rewound, which is exactly what a crash between the insert and the
// acknowledgement produces. The rows must not double: insert_deduplication_token is what
// makes that true, and it works because events_raw is a ReplicatedMergeTree.
func TestARedeliveredBatchLandsOnce(t *testing.T) {
	p := newPipeline(t, "standard")
	p.run(t)

	p.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", nil), event("level_complete", nil), event("session_end", nil),
	))).AssertStatus(http.StatusAccepted)

	if got := p.waitForRows(t, 3); got != 3 {
		t.Fatalf("first delivery stored %d rows, want 3", got)
	}

	// Rewind with the subscription stopped, so the manipulation is not racing a poll.
	p.rewind(t)
	p.stopWriter(t)

	// A second writer over the same spool now reads the same bytes again and hands
	// ClickHouse the same block with the same token.
	second := writer.New(p.bus, p.cache, p.ch, noLogger(), writer.Options{
		Cell: p.settings.Cell, MaxRowsPerInsert: p.settings.WriterBatchSize, DeviceStateTTL: time.Hour,
	})
	if err := second.Start(t.Context()); err != nil {
		t.Fatalf("start the second writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = second.Stop(ctx)
	})

	// Waiting out the clock rather than polling, because the assertion is about a number
	// that must *not* arrive. If deduplication were broken this would be six.
	time.Sleep(3 * time.Second)

	if got := p.countRows(t); got != 3 {
		t.Fatalf("after redelivery events_raw holds %d rows, want 3 — the deduplication token is not working", got)
	}
	if got := second.Stats()["messages"]; got == 0 {
		t.Error("the second writer consumed nothing, so this test proved nothing about deduplication")
	}
}

// TestABatchForADeletedProjectIsDropped is §61 meeting the fact that a queue holds
// messages for hours.
//
// A batch arriving for a project that has since been deleted is ordinary, not exceptional,
// and writing it would resurrect data somebody asked to have erased.
func TestABatchForADeletedProjectIsDropped(t *testing.T) {
	p := newPipeline(t, "standard")

	// Accept the batch first, then delete the project — which is the order that actually
	// happens, and the order that makes the tombstone necessary.
	p.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)

	key := p.cache.Key("project", "tombstoned", p.project.ProjectUID)
	if err := p.cache.Set(t.Context(), key, []byte("deleted"), time.Hour); err != nil {
		t.Fatalf("write tombstone: %v", err)
	}
	t.Cleanup(func() { _ = p.cache.Del(context.Background(), key) })

	p.run(t)
	time.Sleep(2 * time.Second)

	if got := p.countRows(t); got != 0 {
		t.Fatalf("events_raw holds %d rows for a deleted project, want 0", got)
	}
	if got := p.writer.Stats()["dropped_tombstoned"]; got != 1 {
		t.Errorf("dropped_tombstoned = %d, want 1 — the drop must be counted, not silent", got)
	}
}

// TestAMessageFromAnotherCellIsNotStored is the gateway's 421 arriving by a different road.
//
// A message that reached this cell's queue but belongs to another is a routing mistake, and
// storing it would put one region's player data in another region's ClickHouse (§6, §99).
func TestAMessageFromAnotherCellIsNotStored(t *testing.T) {
	p := newPipeline(t, "standard")

	if err := p.bus.Publish(t.Context(), &eventbus.Message{
		PublishedAt:   time.Now().UTC(),
		Cell:          "us-1",
		DedupKey:      uuid.NewString(),
		ProjectSeq:    uint32(p.project.ProjectSeq),
		OrgID:         uuid.NewString(),
		ProjectID:     p.project.ProjectUID,
		EnvironmentID: uuid.NewString(),
		RetentionTier: 30,
		Payload:       []byte(`{"batch_id":"` + uuid.NewString() + `","install_id":"` + uuid.NewString() + `","events":[{"event_id":"` + uuid.NewString() + `","event_name":"session_start","event_time":"2026-09-14T00:00:00Z"}]}`),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	p.run(t)
	time.Sleep(2 * time.Second)

	if got := p.countRows(t); got != 0 {
		t.Fatalf("events_raw holds %d rows from another cell, want 0", got)
	}
	if got := p.writer.Stats()["dropped_wrong_cell"]; got != 1 {
		t.Errorf("dropped_wrong_cell = %d, want 1", got)
	}
}

// TestRowsAreStoredWithValkeyDown is §65: losing the cache costs accuracy until the nightly
// job runs, never rows.
//
// The cohort key is stamped from Valkey, so with Valkey gone it is null — and
// player_activity_daily's derivation is written to backfill exactly that. What must not
// happen is the batch being refused or lost.
func TestRowsAreStoredWithValkeyDown(t *testing.T) {
	p := newPipeline(t, "standard")

	// Accept while the cache is up, because the gateway needs it to resolve the key.
	p.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
		AssertStatus(http.StatusAccepted)

	// Now give the writer a cache that is not there. Never started, so every accessor
	// reports ErrUnavailable — which is what the writer sees during a real outage.
	dead := valkey.New(p.settings.Cell, "redis://127.0.0.1:1/0")

	blind := writer.New(p.bus, dead, p.ch, noLogger(), writer.Options{
		Cell: p.settings.Cell, MaxRowsPerInsert: 5000, DeviceStateTTL: time.Hour,
	})
	if err := blind.Start(t.Context()); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = blind.Stop(ctx)
	})

	if got := p.waitForRows(t, 1); got != 1 {
		t.Fatalf("events_raw holds %d rows, want 1 — losing Valkey must not lose data", got)
	}

	conn, _ := p.ch.DB()
	var cohort *time.Time
	if err := conn.QueryRow(context.Background(),
		"SELECT any(cohort_date) FROM events_raw WHERE project_seq = ?",
		uint32(p.project.ProjectSeq)).Scan(&cohort); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cohort != nil {
		t.Errorf("cohort_date = %v with no cache to read it from; it must be null for the nightly job to backfill", cohort)
	}
	if got := blind.Stats()["device_lookup_failures"]; got == 0 {
		t.Error("the failed device lookup must be counted, so a cache outage is visible")
	}
}
