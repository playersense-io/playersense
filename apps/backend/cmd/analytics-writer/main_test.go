package main

import (
	"context"
	"encoding/json"
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
	"playersense.dev/backend/internal/tombstone"
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
		filepath.Join(p.settings.EventBusDir, p.settings.Cell, ".offsets",
			eventbus.GroupAnalyticsWriter, "*.offset"))
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

// waitForMessages polls the writer's own counter until it has finished the delivery.
//
// ClickHouse reports the rows the instant the insert returns, and the writer increments its
// counters immediately afterwards — correctly, because they measure what actually landed.
// A test that reads Stats() the moment the rows appear can therefore catch the writer
// between the two, so it waits on the writer's signal rather than on storage's.
func (p *pipeline) waitForMessages(t *testing.T, want int) map[string]int64 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var stats map[string]int64
	for time.Now().Before(deadline) {
		stats = p.writer.Stats()
		if stats["messages"] >= int64(want) {
			return stats
		}
		time.Sleep(20 * time.Millisecond)
	}
	return stats
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

// TestABackdatedEventKeepsTodaysCohort is §29.4.
//
// A device whose clock is behind, or whose batch sat in an offline spool, must still be
// assigned to the cohort of the day the server saw it. Using the client's event time
// instead has two failure modes that produce no error at all: a late event bearing an
// earlier timestamp relocates a player into an already-reported cohort, and a wrong clock
// lands a device in a 1970 cohort with a day_offset around 20,700.
//
// It is also the only way the numbers add up. cohort_sizes is derived from
// player_lifetime, whose first_seen is min(ingest_time), so a cohort_date keyed on event
// time gives every retention rate a denominator that belongs to a different cohort.
func TestABackdatedEventKeepsTodaysCohort(t *testing.T) {
	p := newPipeline(t, "standard")
	p.run(t)

	backdated := time.Now().UTC().AddDate(0, 0, -5)
	p.client.Post("/v1/batches", testclient.JSON(batch(
		event("session_start", map[string]any{
			"event_time": backdated.Format(time.RFC3339Nano),
		})))).AssertStatus(http.StatusAccepted)

	if got := p.waitForRows(t, 1); got != 1 {
		t.Fatalf("stored %d rows, want 1", got)
	}

	conn, err := p.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	var stamped, ingested time.Time
	if err := conn.QueryRow(context.Background(), `
		SELECT assumeNotNull(cohort_date), toDate(ingest_time)
		FROM events_raw WHERE project_seq = ? LIMIT 1`,
		uint32(p.project.ProjectSeq)).Scan(&stamped, &ingested); err != nil {
		t.Fatalf("read the stamped cohort: %v", err)
	}

	if !stamped.Equal(ingested) {
		t.Fatalf("cohort_date is %s but the batch arrived on %s; the client's clock decided "+
			"the cohort", stamped.Format(time.DateOnly), ingested.Format(time.DateOnly))
	}
	if stamped.Equal(backdated.Truncate(24 * time.Hour)) {
		t.Fatal("the cohort followed the backdated event time")
	}
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
// TestAnAnonymousEventStoresNoPlayerAtAll is the reason `player_id` is Nullable.
//
// Go has one empty string and no concept of absence, so a writer that passed `ev.PlayerID`
// straight through would store `”` for an event that has no player. That still satisfies
// `IS NOT NULL`, so every anonymous install in the product would flow into
// `account_lifetime` as a single account whose key is the empty string — one enormous fake
// player that every account-level targeting rule would match, and nothing would report it.
//
// The distinction only exists if it is preserved at the boundary, so this checks the
// boundary.
func TestAnAnonymousEventStoresNoPlayerAtAll(t *testing.T) {
	p := newPipeline(t, "standard")
	p.run(t)

	identified := batch(event("session_start", map[string]any{"player_id": "player-7"}))
	anonymous := batch(event("session_start", nil))

	p.client.Post("/v1/batches", testclient.JSON(identified)).AssertStatus(http.StatusAccepted)
	p.client.Post("/v1/batches", testclient.JSON(anonymous)).AssertStatus(http.StatusAccepted)

	if got := p.waitForRows(t, 2); got != 2 {
		t.Fatalf("events_raw holds %d rows, want 2", got)
	}

	conn, _ := p.ch.DB()
	var nulls, empties uint64
	if err := conn.QueryRow(context.Background(), `
		SELECT countIf(player_id IS NULL), countIf(player_id = '')
		FROM events_raw WHERE project_seq = ?`,
		uint32(p.project.ProjectSeq)).Scan(&nulls, &empties); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if nulls != 1 {
		t.Errorf("%d rows have a NULL player, want 1", nulls)
	}
	if empties != 0 {
		t.Errorf("%d rows store the empty string instead of NULL", empties)
	}

	// And the consequence that matters: the anonymous install is not an account.
	//
	// Polled, because a materialized view's target becomes visible a moment after the table
	// it reads from. Asserting the instant events_raw has both rows is asserting an ordering
	// nothing promised — the same mistake as reading an offset file the moment its object
	// appears.
	accounts := p.waitForAccounts(t, 1)
	if accounts != 1 {
		t.Errorf("account_lifetime holds %d accounts, want exactly the identified one", accounts)
	}

	// actor_key still falls back to the install, so a player timeline is unaffected.
	var actors uint64
	if err := conn.QueryRow(context.Background(), `
		SELECT uniqExact(actor_key) FROM events_raw WHERE project_seq = ?`,
		uint32(p.project.ProjectSeq)).Scan(&actors); err != nil {
		t.Fatalf("read actors: %v", err)
	}
	if actors != 2 {
		t.Errorf("%d distinct actor keys, want 2 — actor_key must still coalesce", actors)
	}
}

// waitForAccounts waits for the account-level aggregate to catch up with events_raw.
func (p *pipeline) waitForAccounts(t *testing.T, want uint64) uint64 {
	t.Helper()
	conn, _ := p.ch.DB()
	deadline := time.Now().Add(10 * time.Second)
	var got uint64
	for {
		if err := conn.QueryRow(context.Background(),
			`SELECT count() FROM account_lifetime_v WHERE project_seq = ?`,
			uint32(p.project.ProjectSeq)).Scan(&got); err != nil {
			t.Fatalf("read accounts: %v", err)
		}
		if got >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

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

	key := tombstone.Project(p.cache, p.project.ProjectUID)
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

// TestAnErasedActorsEventsAreDropped is §61's other tombstone.
//
// The project tombstone above is per message, because a deleted project deletes every batch.
// This one is per event, and the difference is §29.2's: a batch is one flush from one
// installation, but the actor inside it can change when an anonymous player signs in half
// way through. A message is not the unit an erasure applies to.
//
// The two events here share a batch and differ only in their player, which is the shape that
// would pass a per-message check and store the erased half anyway.
func TestAnErasedActorsEventsAreDropped(t *testing.T) {
	p := newPipeline(t, "standard")

	// Accepted first, tombstoned after — the order that actually happens, and the reason a
	// tombstone is needed rather than a check at the gateway.
	body := batch(
		event("session_start", map[string]any{"player_id": "player-erased"}),
		event("session_start", map[string]any{"player_id": "player-bystander"}),
	)
	p.client.Post("/v1/batches", testclient.JSON(body)).AssertStatus(http.StatusAccepted)

	key := tombstone.Actor(p.cache, p.project.ProjectUID, "player-erased")
	if err := p.cache.Set(t.Context(), key, []byte("erased"), time.Hour); err != nil {
		t.Fatalf("raise the tombstone: %v", err)
	}
	t.Cleanup(func() { _ = p.cache.Del(context.Background(), key) })

	p.run(t)
	p.waitForRows(t, 1)
	time.Sleep(500 * time.Millisecond)

	if got := p.countRows(t); got != 1 {
		t.Fatalf("events_raw holds %d rows, want 1 — the erased actor's event and no other", got)
	}
	if got := p.countActor(t, "player-erased"); got != 0 {
		t.Errorf("the erased actor has %d rows, want 0", got)
	}
	if got := p.countActor(t, "player-bystander"); got != 1 {
		t.Errorf("the bystander has %d rows, want 1 — one erasure is not two", got)
	}
	if got := p.writer.Stats()["dropped_erased"]; got != 1 {
		t.Errorf("dropped_erased = %d, want 1 — the drop must be counted, not silent", got)
	}
}

// countActor counts this project's rows for one actor.
func (p *pipeline) countActor(t *testing.T, actorKey string) uint64 {
	t.Helper()
	conn, err := p.ch.DB()
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	var n uint64
	if err := conn.QueryRow(context.Background(),
		"SELECT count() FROM events_raw WHERE project_seq = ? AND actor_key = ?",
		uint32(p.project.ProjectSeq), actorKey).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
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

// TestADeliveryBecomesOneBlockPerPartition is the property the whole ingestion path's
// scale ceiling rests on (ADR 0023).
//
// One insert per message is the natural thing to write and it does not survive §4's
// envelope. `insert_deduplication_token` is part of an async insert's grouping key, so
// ClickHouse never coalesces two inserts carrying different tokens — measured on the local
// stack, 200 concurrent inserts produce 200 parts with a token each and 9 without. At a
// billion events a day and five hundred events a batch that is two million parts a day
// arriving in a handful of daily partitions, against a default `parts_to_throw_insert` of
// three thousand. The node stops accepting inserts.
//
// The batches are published before the writer starts, so the whole spool is one delivery
// and the assertion is about the writer rather than about poll timing.
func TestADeliveryBecomesOneBlockPerPartition(t *testing.T) {
	p := newPipeline(t, "standard")

	const batches = 40
	for range batches {
		p.client.Post("/v1/batches", testclient.JSON(batch(
			event("session_start", nil), event("level_complete", nil), event("session_end", nil),
		))).AssertStatus(http.StatusAccepted)
	}

	// Only now, so everything published above arrives as one delivery.
	p.run(t)

	if got := p.waitForRows(t, batches*3); got != batches*3 {
		t.Fatalf("stored %d rows, want %d", got, batches*3)
	}

	stats := p.waitForMessages(t, batches)
	inserts, rows := stats["inserts"], stats["rows"]
	if rows != batches*3 {
		t.Fatalf("the writer counted %d rows, want %d", rows, batches*3)
	}

	// Every event carries today's date and one retention tier, so all 40 batches share a
	// partition and belong in one block. Allowing a couple covers the writer picking the
	// spool up in more than one poll.
	if inserts > 3 {
		t.Errorf("%d batches became %d inserts; they share one partition and should coalesce "+
			"— a part per batch is what makes a node unusable at scale", batches, inserts)
	}
	if inserts == 0 {
		t.Fatal("the writer reported no inserts, so this test proved nothing")
	}
	t.Logf("%d batches / %d rows became %d insert(s)", batches, rows, inserts)
}

// TestABlockNeverSpansTwoPartitions keeps the coalescing from trading one problem for
// another.
//
// An insert spanning two partitions writes a part in each, so grouping rows by arrival
// rather than by partition would put a day boundary inside a block and produce exactly the
// small parts the grouping exists to avoid. Grouping on ClickHouse's own partition key —
// (retention_tier, toDate(event_time)) — is what makes one insert one part.
func TestABlockNeverSpansTwoPartitions(t *testing.T) {
	p := newPipeline(t, "standard")

	// Inside §19.2's clamp window, so the gateway keeps the dates rather than pulling them
	// forward. Two days means two partitions.
	twoDaysAgo := time.Now().UTC().AddDate(0, 0, -2).Format(time.RFC3339Nano)
	for range 10 {
		p.client.Post("/v1/batches", testclient.JSON(batch(
			event("session_start", nil),
			event("level_complete", map[string]any{"event_time": twoDaysAgo}),
		))).AssertStatus(http.StatusAccepted)
	}

	p.run(t)

	if got := p.waitForRows(t, 20); got != 20 {
		t.Fatalf("stored %d rows, want 20", got)
	}

	inserts := p.waitForMessages(t, 10)["inserts"]
	if inserts < 2 {
		t.Errorf("rows spanning two days became %d insert(s); a block spanning two "+
			"partitions writes a part in each, so they must be split", inserts)
	}
	if inserts > 4 {
		t.Errorf("rows spanning two days became %d inserts; two partitions should need "+
			"about two blocks", inserts)
	}
	t.Logf("two partitions became %d insert(s)", inserts)
}

// TestAPoisonedMessageIsParkedAndTheQueueMovesOn is the failure this path exists for.
//
// A message that ClickHouse will never accept used to have two possible outcomes, both
// bad. On the local bus a handler error ended the subscription outright, so the writer
// stopped consuming while its health probe kept answering. On Pub/Sub it was nacked
// forever, stalling every batch behind it. Batching made the blast radius worse: one bad
// message now fails a delivery of hundreds.
//
// The poisoned message is published straight onto the bus rather than through the gateway,
// and that is the honest shape of this test rather than a shortcut.
//
// It used to go through the gateway carrying an event_id that was not a UUID, which the
// gateway accepted and the writer then choked on. That hole is closed — a bad event_id is
// now a per-event drop with a reason the SDK can read — so the gateway can no longer produce
// a message the writer cannot store. The dead-letter path still has to work, because "the
// gateway cannot produce one *today*" is not the same claim as "one can never arrive": a
// redelivery written by an older build, or a message published by hand during an incident,
// reaches the same consumer.
func TestAPoisonedMessageIsParkedAndTheQueueMovesOn(t *testing.T) {
	p := newPipeline(t, "standard")

	good := batch(event("session_start", nil))
	alsoGood := batch(event("session_end", nil))

	p.client.Post("/v1/batches", testclient.JSON(good)).AssertStatus(http.StatusAccepted)
	p.publishPoison(t)
	p.client.Post("/v1/batches", testclient.JSON(alsoGood)).AssertStatus(http.StatusAccepted)

	p.run(t)

	// The two healthy batches must land even though a poisoned one sits between them.
	if got := p.waitForRows(t, 2); got != 2 {
		t.Fatalf("stored %d rows, want the 2 healthy ones — the poisoned batch stalled the queue", got)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if p.writer.Stats()["dead_lettered"] > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if got := p.writer.Stats()["dead_lettered"]; got != 1 {
		t.Fatalf("dead_lettered = %d, want 1", got)
	}

	// Parked, not discarded: the bytes are what an operator needs to find the bug.
	parked, err := p.bus.DeadLettered(eventbus.GroupAnalyticsWriter, time.Now().UTC())
	if err != nil {
		t.Fatalf("read the dead-letter spool: %v", err)
	}
	if len(parked) != 1 {
		t.Fatalf("the dead-letter spool holds %d messages, want 1", len(parked))
	}
	if parked[0].EventCount != 1 {
		t.Errorf("the parked message lost its payload: %+v", parked[0])
	}
}

// publishPoison puts a message on the bus that the writer can never store.
//
// An event_id the column's type cannot hold, which is what `uuid.Parse` refuses in the
// writer. Published directly because the gateway would now reject it, which is the point of
// the note on the test above.
func (p *pipeline) publishPoison(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"batch_id":   uuid.NewString(),
		"install_id": uuid.NewString(),
		"sdk_name":   "unity", "sdk_version": "1.4.0",
		"app_version": "2.7.1", "platform": "ios",
		"consent_analytics": 1,
		"received_at":       time.Now().UTC(),
		"ingest_time":       time.Now().UTC(),
		"events": []map[string]any{{
			"event_id":   "not-a-uuid",
			"event_name": "level_complete",
			"event_type": "custom",
			"event_time": time.Now().UTC(),
			"install_id": uuid.NewString(),
		}},
	})
	if err != nil {
		t.Fatalf("build the poisoned payload: %v", err)
	}

	if err := p.bus.Publish(t.Context(), &eventbus.Message{
		PublishedAt:    time.Now().UTC(),
		Cell:           p.settings.Cell,
		DedupKey:       "poison-" + uuid.NewString(),
		OrgSeq:         uint32(p.project.OrgSeq),
		ProjectSeq:     uint32(p.project.ProjectSeq),
		EnvironmentSeq: 1,
		OrgID:          uuid.NewString(),
		ProjectID:      p.project.ProjectUID,
		EnvironmentID:  uuid.NewString(),
		Environment:    "production",
		PolicyTier:     "standard",
		RetentionTier:  30,
		EventCount:     1,
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish the poisoned message: %v", err)
	}
}

// TestAnOutageDoesNotDeadLetterTheBacklog is the other half, and the more dangerous one.
//
// Attempt counting alone cannot tell a poisoned message from an unavailable database. A
// writer that dead-letters on attempt count would move an entire backlog to the
// dead-letter topic during a ClickHouse outage and call it poison — turning a recoverable
// incident into a data-loss one. The writer probes the database before parking anything,
// so "this data is unstorable" is a question it asks ClickHouse rather than a conclusion it
// draws from a counter.
func TestAnOutageDoesNotDeadLetterTheBacklog(t *testing.T) {
	p := newPipeline(t, "standard")

	for range 5 {
		p.client.Post("/v1/batches", testclient.JSON(batch(event("session_start", nil)))).
			AssertStatus(http.StatusAccepted)
	}

	// Close ClickHouse underneath the writer, so every insert and every probe fails.
	if err := p.ch.Stop(context.Background()); err != nil {
		t.Fatalf("stop clickhouse: %v", err)
	}

	p.run(t)

	// Long enough for the delivery to exhaust its attempts several times over.
	time.Sleep(6 * time.Second)

	if got := p.writer.Stats()["dead_lettered"]; got != 0 {
		t.Fatalf("dead_lettered = %d during an outage; a backlog must never be parked "+
			"because the database is down", got)
	}
	if got := p.writer.Stats()["retries"]; got == 0 {
		t.Error("the writer never retried, so this test proved nothing about an outage")
	}
}
