package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/regional/datadeletionrequest"
	"playersense.dev/backend/ent/regional/dataexportrequest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/export"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/tombstone"
	"playersense.dev/backend/internal/valkey"
)

// §101's data-subject surface, on the cell-local API that is allowed to see a player
// identifier at all (§99).
//
// The endpoints are thin by design — they record, tombstone and report — so what these tests
// are actually about is the things a thin surface still has to get right: who may call it,
// whose requests it will show you, and what happens when a studio retries.

type privacy struct {
	settings config.Settings
	server   *testclient.Client
	reader   *testclient.Client
	cell     *pgdb.Cell
	cache    *valkey.Client
	store    *blobstore.Local
	project  testsupport.Project
}

func newPrivacy(t *testing.T) *privacy {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.ArchiveDir = t.TempDir()

	db := testsupport.Global(t, settings)
	cache := testsupport.Cache(t, settings)
	cell := testsupport.CellDB(t, settings)

	home := core.CellFrom(settings)
	seeded := testsupport.SeedProject(t, db, "standard", testsupport.SeedCell(t, db, home).ID)
	serverSecret := testsupport.SeedKey(t, db, core.KindServer, seeded.EnvironmentID)

	store := blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("start store: %v", err)
	}

	resolver := core.NewKeyResolver(db, cache, core.KeyResolverOptions{
		TTL: time.Minute, NegativeTTL: time.Second, LocalTTL: time.Second, MaxLocal: 100,
	})

	app := muzak.New(muzak.AppOptions{
		Title: "privacy", Addr: ":0",
		LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
	},
		muzak.WithSingleton(db),
		muzak.WithSingleton(resolver),
		muzak.WithSingleton(settings),
		muzak.WithSingleton(cell),
		muzak.WithSingleton(cache),
		muzak.WithSingleton(blobstore.Store(store)),
		muzak.WithSingleton(home),
	)
	app.Include(routers.Privacy())

	return &privacy{
		settings: settings,
		server: testclient.New(t, app,
			testclient.WithHeader("Authorization", "Bearer "+serverSecret)),
		reader: testclient.New(t, app,
			testclient.WithHeader("Authorization", "Bearer "+
				testsupport.SeedKey(t, db, core.KindReporting, seeded.EnvironmentID))),
		cell:    cell,
		cache:   cache,
		store:   store,
		project: seeded,
	}
}

// cleanupActor removes whatever a test left in the cell tier for one actor.
func (p *privacy) cleanupActor(t *testing.T, actorKey string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		project := uuid.MustParse(p.project.ProjectUID)
		_, _ = p.cell.Client.DataDeletionRequest.Delete().Where(
			datadeletionrequest.ProjectUID(project),
			datadeletionrequest.ActorKey(actorKey),
		).Exec(ctx)
		_, _ = p.cell.Client.DataExportRequest.Delete().Where(
			dataexportrequest.ProjectUID(project),
			dataexportrequest.ActorKey(actorKey),
		).Exec(ctx)
		_ = p.cache.Del(ctx, tombstone.Actor(p.cache, p.project.ProjectUID, actorKey))
	})
}

// TestAnErasureIsRecordedAndTombstonedInOneRoundTrip is §05's first two steps.
//
// The handler does not delete anything and is not supposed to: removal is hours of rewriting
// whole parts and whole objects. What has to be immediate is the record and the tombstone,
// and a caller must be able to see both before the response is over.
func TestAnErasureIsRecordedAndTombstonedInOneRoundTrip(t *testing.T) {
	p := newPrivacy(t)
	actor := "player-" + uuid.NewString()[:8]
	p.cleanupActor(t, actor)

	var got map[string]any
	p.server.Post("/v1/privacy/erasures", testclient.JSON(map[string]any{"actor_key": actor})).
		AssertStatus(http.StatusAccepted).
		JSON(&got)

	if got["status"] != string(datadeletionrequest.StatusTombstoned) {
		t.Errorf("status = %v, want tombstoned before the response is over", got["status"])
	}
	if got["request_id"] == "" || got["request_id"] == nil {
		t.Fatal("no request id; there is nothing for the studio to ask about later")
	}

	up, err := p.cache.Exists(t.Context(), tombstone.Actor(p.cache, p.project.ProjectUID, actor))
	if err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if !up {
		t.Error("no tombstone; the writer would keep storing this actor's events")
	}

	// And it can be read back.
	var read map[string]any
	p.server.Get("/v1/privacy/erasures/" + got["request_id"].(string)).
		AssertStatus(http.StatusOK).
		JSON(&read)
	if read["actor_key"] != actor {
		t.Errorf("read back %v, want %s", read["actor_key"], actor)
	}
}

// TestASecondErasureForTheSamePersonIsTheFirstOne is what a retry has to do.
//
// A studio whose request timed out will send it again. Two records of one erasure would be
// two audit trails of one event, and §48's reasoning about purchases applies at least as
// strongly to something that cannot be undone.
func TestASecondErasureForTheSamePersonIsTheFirstOne(t *testing.T) {
	p := newPrivacy(t)
	actor := "player-" + uuid.NewString()[:8]
	p.cleanupActor(t, actor)

	body := testclient.JSON(map[string]any{"actor_key": actor})
	var first, second map[string]any
	p.server.Post("/v1/privacy/erasures", body).AssertStatus(http.StatusAccepted).JSON(&first)
	p.server.Post("/v1/privacy/erasures", testclient.JSON(map[string]any{"actor_key": actor})).
		AssertStatus(http.StatusAccepted).JSON(&second)

	if first["request_id"] != second["request_id"] {
		t.Errorf("two requests for one erasure: %v and %v", first["request_id"], second["request_id"])
	}
	n, err := p.cell.Client.DataDeletionRequest.Query().Where(
		datadeletionrequest.ProjectUID(uuid.MustParse(p.project.ProjectUID)),
		datadeletionrequest.ActorKey(actor),
	).Count(t.Context())
	if err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if n != 1 {
		t.Errorf("the cell tier holds %d records of one erasure, want 1", n)
	}
}

// TestAReportingKeyCannotEraseAnyone is §52's separation of privileges.
//
// Reading a studio's aggregates and erasing one of its players are not the same power. A
// project key would be worse still — it ships inside a game binary (§71) — which is why this
// surface takes a server key and nothing else.
func TestAReportingKeyCannotEraseAnyone(t *testing.T) {
	p := newPrivacy(t)

	p.reader.Post("/v1/privacy/erasures",
		testclient.JSON(map[string]any{"actor_key": "player-nope"})).
		AssertStatus(http.StatusForbidden)
}

// TestAnExportIsRefusedWhileAnErasureIsOpen keeps the two rights from undoing each other.
//
// Assembling a bundle for somebody who is being erased would create a fresh copy of exactly
// what the erasure is working to remove, and park it in a bucket. The order between the two
// rights is the studio's to choose, so this is refused with the reason rather than silently
// reordered.
func TestAnExportIsRefusedWhileAnErasureIsOpen(t *testing.T) {
	p := newPrivacy(t)
	actor := "player-" + uuid.NewString()[:8]
	p.cleanupActor(t, actor)

	p.server.Post("/v1/privacy/erasures", testclient.JSON(map[string]any{"actor_key": actor})).
		AssertStatus(http.StatusAccepted)

	p.server.Post("/v1/privacy/exports", testclient.JSON(map[string]any{"actor_key": actor})).
		AssertStatus(http.StatusConflict)
}

// TestAnExportCanBeCollected is the delivery half of §101.
//
// An export that cannot be fetched is not an answer to an access request. It is served
// through the API on the credential the caller already holds rather than as a signed bucket
// URL, which would be a second credential with different lifetime rules and no revocation,
// issued for an object holding one named person's entire history.
func TestAnExportCanBeCollected(t *testing.T) {
	p := newPrivacy(t)
	actor := "player-" + uuid.NewString()[:8]
	p.cleanupActor(t, actor)

	var submitted map[string]any
	p.server.Post("/v1/privacy/exports", testclient.JSON(map[string]any{"actor_key": actor})).
		AssertStatus(http.StatusAccepted).
		JSON(&submitted)
	id := submitted["request_id"].(string)

	// Not ready yet, and the refusal says so rather than 404ing on a request that exists.
	p.server.Get("/v1/privacy/exports/" + id + "/download").AssertStatus(http.StatusConflict)

	// Run the job the worker would run.
	sqlDB := clickhouse.OpenDB(mustParse(t, p.settings.ClickHouseDSN))
	t.Cleanup(func() { _ = sqlDB.Close() })
	exporter := &export.Exporter{
		Cell: p.cell, CH: chPool{sqlDB}, Store: p.store, Log: quiet(),
	}
	if _, err := exporter.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("export: %v", err)
	}

	var ready map[string]any
	p.server.Get("/v1/privacy/exports/" + id).AssertStatus(http.StatusOK).JSON(&ready)
	if ready["status"] != string(dataexportrequest.StatusCompleted) {
		t.Fatalf("status = %v, want completed (%v)", ready["status"], ready["failure_reason"])
	}
	if ready["download_url"] == nil || ready["download_url"] == "" {
		t.Error("completed with nowhere to collect it from")
	}

	body := p.server.Get("/v1/privacy/exports/" + id + "/download").
		AssertStatus(http.StatusOK).
		String()
	if body == "" {
		t.Fatal("the bundle is empty")
	}
	// The first line says what the answer is; the last says where it stops.
	if !strings.Contains(body, `"section":"meta"`) || !strings.Contains(body, `"section":"summary"`) {
		t.Errorf("the bundle is missing its opening or closing line:\n%s", body)
	}
}

// TestOneStudioCannotReadAnothersRequest is the scoping every surface here shares.
func TestOneStudioCannotReadAnothersRequest(t *testing.T) {
	p := newPrivacy(t)

	// A well-formed identifier for a request that belongs to nobody in this project.
	p.server.Get("/v1/privacy/erasures/" + uuid.NewString()).AssertStatus(http.StatusNotFound)
}

// chPool hands a job the ClickHouse handle the application would normally open for it.
type chPool struct{ db *sql.DB }

func (p chPool) DB() (*sql.DB, error) { return p.db, nil }
