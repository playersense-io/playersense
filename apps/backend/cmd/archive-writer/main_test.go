package main

import (
	"bytes"
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
	"github.com/parquet-go/parquet-go"
	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/regional/archivemanifest"
	"playersense.dev/backend/internal/archive"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/routers"
	"playersense.dev/backend/internal/testsupport"
)

// pipeline is the archive path end to end: the real gateway, the real bus, the real writer,
// real Parquet on disk and a real manifest in the cell tier.
type pipeline struct {
	settings config.Settings
	client   *testclient.Client
	bus      *eventbus.Local
	writer   *archive.Writer
	store    *blobstore.Local
	cell     *pgdb.Cell
	project  testsupport.Project
	root     string
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.EventBusDir = t.TempDir()
	settings.ArchiveDir = t.TempDir()
	settings.WriterFlushInterval = 20 * time.Millisecond

	db := testsupport.Global(t, settings)
	cache := testsupport.Cache(t, settings)
	cell := testsupport.CellDB(t, settings)

	home := core.CellFrom(settings)
	seeded := testsupport.SeedProject(t, db, "standard", testsupport.SeedCell(t, db, home).ID)

	bus := eventbus.NewLocal(home.Slug, settings.EventBusDir)
	if err := bus.Start(t.Context()); err != nil {
		t.Fatalf("start bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Stop(context.Background()) })
	bus.SetPollInterval(settings.WriterFlushInterval)

	store := blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("start store: %v", err)
	}

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

	writer := archive.New(bus, store, cell, quiet(), archive.Options{
		Cell:   settings.Cell,
		Region: settings.CellRegion,
	})

	return &pipeline{
		settings: settings,
		client: testclient.New(t, gateway,
			testclient.WithHeader("Authorization", "Bearer "+seeded.Secret)),
		bus:     bus,
		writer:  writer,
		store:   store,
		cell:    cell,
		project: seeded,
		root:    filepath.Join(settings.ArchiveDir, settings.ArchiveBucket),
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (p *pipeline) run(t *testing.T) {
	t.Helper()
	if err := p.writer.Start(t.Context()); err != nil {
		t.Fatalf("start archive writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.writer.Stop(ctx)
	})
}

func (p *pipeline) post(t *testing.T, events ...map[string]any) {
	t.Helper()
	p.client.Post("/v1/batches", testclient.JSON(map[string]any{
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
	})).AssertStatus(http.StatusAccepted)
}

func event(name string, at time.Time) map[string]any {
	return map[string]any{
		"event_id":   uuid.NewString(),
		"event_name": name,
		"event_time": at.UTC().Format(time.RFC3339Nano),
	}
}

// objects lists every archive object on disk.
func (p *pipeline) objects(t *testing.T) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(p.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".parquet" {
			return err
		}
		rel, relErr := filepath.Rel(p.root, path)
		if relErr != nil {
			return relErr
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk the archive: %v", err)
	}
	return found
}

// waitForObjects polls until the archive holds at least want objects.
func (p *pipeline) waitForObjects(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var found []string
	for time.Now().Before(deadline) {
		found = p.objects(t)
		if len(found) >= want {
			return found
		}
		time.Sleep(50 * time.Millisecond)
	}
	return found
}

// rowsIn reads an archive object back.
func (p *pipeline) rowsIn(t *testing.T, object string) []archive.Row {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(object)))
	if err != nil {
		t.Fatalf("read %s: %v", object, err)
	}
	rows, err := parquet.Read[archive.Row](bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("decode %s: %v", object, err)
	}
	return rows
}

func (p *pipeline) manifests(t *testing.T) int {
	t.Helper()
	n, err := p.cell.Client.ArchiveManifest.Query().
		Where(archivemanifest.ProjectUIDEQ(uuid.MustParse(p.project.ProjectUID))).
		Count(context.Background())
	if err != nil {
		t.Fatalf("count manifests: %v", err)
	}
	return n
}

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

// TestABatchBecomesAnArchiveObject is the whole archive path.
//
// A batch is accepted by the real gateway, consumed on the archive's own subscription, and
// written as Parquet with a manifest describing it. The object is then read back, because an
// archive that cannot be read is not an archive — it is a storage bill.
func TestABatchBecomesAnArchiveObject(t *testing.T) {
	p := newPipeline(t)
	p.run(t)

	at := time.Now().UTC()
	p.post(t, event("session_start", at), event("level_completed", at))

	objects := p.waitForObjects(t, 1)
	if len(objects) != 1 {
		t.Fatalf("archived %d objects, want 1", len(objects))
	}

	// §14.1's prefix, which §34's cold queries prune on and §61's deletion acts on.
	path := objects[0]
	for _, want := range []string{
		"cell=" + p.settings.Cell + "/",
		"org=", "project=" + p.project.ProjectUID + "/", "env=production/",
		"dt=" + at.Format(time.DateOnly) + "/",
	} {
		if !contains(path, want) {
			t.Errorf("the archive path is missing %q: %s", want, path)
		}
	}

	rows := p.rowsIn(t, path)
	if len(rows) != 2 {
		t.Fatalf("the object holds %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.ProjectID != p.project.ProjectUID {
			t.Errorf("a row names project %s, want %s", row.ProjectID, p.project.ProjectUID)
		}
		// §97: the collection basis has to survive into the archive, or an event's lawful
		// basis lives in a file the event does not.
		if row.ConsentAnalytics != 1 || row.ConsentPolicyVersion != "2026-01-15" {
			t.Errorf("the consent basis did not reach the archive: %+v", row)
		}
		if row.EventTime.IsZero() {
			t.Error("a row has no event time")
		}
	}

	if got := p.manifests(t); got != 1 {
		t.Fatalf("recorded %d manifests, want 1", got)
	}
}

// TestTheManifestDescribesItsObject is what §39, §60 and §104 all read.
//
// Breach scoping, expiry and billing are three different jobs asking the same row three
// different questions, and none of them opens the object to do it.
func TestTheManifestDescribesItsObject(t *testing.T) {
	p := newPipeline(t)
	p.run(t)

	// Both inside one hour, deliberately. An object holds one project, one environment and
	// one hour (§14.1), so two events either side of an hour boundary are two objects —
	// which is what TestEachHourIsItsOwnObject checks.
	hour := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	first := hour.Add(5 * time.Minute)
	last := hour.Add(50 * time.Minute)
	p.post(t, event("session_start", first), event("session_end", last))

	objects := p.waitForObjects(t, 1)
	if len(objects) != 1 {
		t.Fatalf("archived %d objects, want 1", len(objects))
	}

	m, err := p.cell.Client.ArchiveManifest.Query().
		Where(archivemanifest.ProjectUIDEQ(uuid.MustParse(p.project.ProjectUID))).
		Only(context.Background())
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	if m.ObjectPath != objects[0] {
		t.Fatalf("the manifest points at %s but the object is %s", m.ObjectPath, objects[0])
	}
	if m.EventCount != 2 {
		t.Fatalf("the manifest claims %d events, want 2", m.EventCount)
	}
	if m.ObjectSizeBytes == 0 {
		t.Fatal("§39 bills on object_size_bytes and it is zero")
	}
	if m.Region != p.settings.CellRegion {
		t.Fatalf("the manifest records region %q, want %q", m.Region, p.settings.CellRegion)
	}
	if m.Status != archivemanifest.StatusDurable {
		t.Fatalf("a manifest written after the object is %q", m.Status)
	}

	// §104 asks "whose data, and how much, between when and when". The bounds have to be
	// the events', not the object's write time.
	if m.MinEventTime.After(first.Add(time.Second)) || m.MinEventTime.Before(first.Add(-time.Second)) {
		t.Errorf("min_event_time is %s, want the earliest event at %s", m.MinEventTime, first)
	}
	if m.MaxEventTime.Before(last.Add(-time.Second)) {
		t.Errorf("max_event_time is %s, want the latest event at %s", m.MaxEventTime, last)
	}

	// §60 expires on this, derived from the project's retention tier at write time.
	wantExpiry := m.MaxEventTime.AddDate(0, 0, 30)
	if m.DeleteAfter.Sub(wantExpiry).Abs() > time.Minute {
		t.Errorf("delete_after is %s, want %s (30-day tier)", m.DeleteAfter, wantExpiry)
	}
}

// TestARedeliveredBatchArchivesOnce is §21.
//
// Delivery is at-least-once, so the same batch will be handed over twice. The object name is
// derived from the batches inside it, so the second delivery writes the same path with the
// same bytes and the manifest insert is a conflict rather than a duplicate — which is also
// the only outcome that does not double the storage §39 bills for.
func TestARedeliveredBatchArchivesOnce(t *testing.T) {
	p := newPipeline(t)
	p.run(t)

	p.post(t, event("session_start", time.Now().UTC()))
	objects := p.waitForObjects(t, 1)
	if len(objects) != 1 {
		t.Fatalf("archived %d objects, want 1", len(objects))
	}

	// Rewind the archive's own position, which is exactly what a crash between the object
	// and the acknowledgement leaves behind.
	p.stop(t)
	p.rewind(t)

	second := archive.New(p.bus, p.store, p.cell, quiet(), archive.Options{
		Cell: p.settings.Cell, Region: p.settings.CellRegion,
	})
	if err := second.Start(t.Context()); err != nil {
		t.Fatalf("restart archive writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = second.Stop(ctx)
	})

	// Long enough that a second object would have appeared; the assertion is about a
	// number that must *not* grow.
	time.Sleep(2 * time.Second)

	if after := p.objects(t); len(after) != 1 {
		t.Fatalf("a redelivery produced %d objects: %v", len(after), after)
	}
	if got := p.manifests(t); got != 1 {
		t.Fatalf("a redelivery produced %d manifests, want 1", got)
	}
}

// TestTheArchiveHasItsOwnPosition is §20's fan-out.
//
// Two subscriptions on one topic. Before consumer groups existed both writers shared a
// position and each would have skipped whatever the other took first — a loss that no error
// reports, because the offset had already advanced.
func TestTheArchiveHasItsOwnPosition(t *testing.T) {
	p := newPipeline(t)
	p.run(t)

	p.post(t, event("session_start", time.Now().UTC()))
	if objects := p.waitForObjects(t, 1); len(objects) != 1 {
		t.Fatalf("archived %d objects, want 1", len(objects))
	}

	offsets, err := filepath.Glob(filepath.Join(
		p.settings.EventBusDir, p.settings.Cell, ".offsets", eventbus.GroupArchiveWriter, "*.offset"))
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) == 0 {
		t.Fatal("the archive committed no position of its own")
	}

	shared, err := filepath.Glob(filepath.Join(
		p.settings.EventBusDir, p.settings.Cell, ".offsets", "*.offset"))
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 0 {
		t.Fatalf("a position was committed outside any group: %v", shared)
	}
}

func (p *pipeline) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.writer.Stop(ctx); err != nil {
		t.Fatalf("stop archive writer: %v", err)
	}
}

func (p *pipeline) rewind(t *testing.T) {
	t.Helper()
	offsets, err := filepath.Glob(filepath.Join(
		p.settings.EventBusDir, p.settings.Cell, ".offsets", eventbus.GroupArchiveWriter, "*.offset"))
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) == 0 {
		t.Fatal("no offset to rewind; the archive never committed one")
	}
	for _, path := range offsets {
		if err := os.Remove(path); err != nil {
			t.Fatalf("rewind %s: %v", path, err)
		}
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

// TestEachHourIsItsOwnObject is §14.1's prefix, and §104 is why it is worth enforcing.
//
// The hour is part of the path, so an object cannot span two. That keeps the time bounds on
// every manifest tight, and breach scoping is only as precise as those bounds: an object
// filed wholesale under its first event's hour would widen every range it touched, and the
// question "whose data, between when and when" would start returning more than the truth.
func TestEachHourIsItsOwnObject(t *testing.T) {
	p := newPipeline(t)
	p.run(t)

	hour := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
	p.post(t,
		event("session_start", hour.Add(50*time.Minute)),
		event("session_end", hour.Add(70*time.Minute)), // the next hour
	)

	objects := p.waitForObjects(t, 2)
	if len(objects) != 2 {
		t.Fatalf("a batch spanning two hours produced %d objects: %v", len(objects), objects)
	}

	hours := map[string]bool{}
	for _, object := range objects {
		rows := p.rowsIn(t, object)
		if len(rows) != 1 {
			t.Fatalf("%s holds %d rows, want 1", object, len(rows))
		}
		hours[rows[0].EventTime.UTC().Truncate(time.Hour).Format(time.RFC3339)] = true
	}
	if len(hours) != 2 {
		t.Fatalf("the two objects cover %d distinct hours, want 2", len(hours))
	}
	if got := p.manifests(t); got != 2 {
		t.Fatalf("recorded %d manifests, want one per object", got)
	}
}

// TestAnUnarchivableMessageIsParkedAndTheStreamMovesOn is the archive's half of ADR 0023's
// failure path.
//
// The storage writer and the archive writer consume the same stream on their own
// subscriptions (§20), so a message neither can process stalls both — independently. The
// archive had no dead-letter path at all: a message it could not write retried forever and
// every batch behind it waited. An archive that quietly stops is the worst version of this,
// because §60 expires local data on the assumption the archive is keeping up.
//
// The poison is published straight to the bus rather than through the gateway, because the
// gateway cannot produce it: the tenant identifiers come from the resolved key and are
// always well formed. What this simulates is a corrupt message already on the queue, which
// is the case the dead-letter path exists for — the archive writes those identifiers into a
// typed manifest column, so one that is not a UUID can never be written, however many times
// it is redelivered.
func TestAnUnarchivableMessageIsParkedAndTheStreamMovesOn(t *testing.T) {
	p := newPipeline(t)
	at := time.Now().UTC()

	p.post(t, event("session_start", at))
	p.publishPoison(t, at)
	p.post(t, event("session_end", at))

	p.run(t)

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if p.writer.Stats()["dead_lettered"] > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	stats := p.writer.Stats()
	if stats["dead_lettered"] != 1 {
		t.Fatalf("dead_lettered = %d, want 1 — the poisoned message stalled the archive",
			stats["dead_lettered"])
	}
	if stats["objects"] == 0 {
		t.Error("nothing was archived; the healthy batches were held up by the poisoned one")
	}

	parked, err := p.bus.DeadLettered(eventbus.GroupArchiveWriter, time.Now().UTC())
	if err != nil {
		t.Fatalf("read the dead-letter spool: %v", err)
	}
	if len(parked) != 1 {
		t.Fatalf("the dead-letter spool holds %d messages, want 1", len(parked))
	}
	if parked[0].ProjectID != "not-a-uuid" {
		t.Errorf("the wrong message was parked: %s", parked[0].ProjectID)
	}
}

// publishPoison puts a message on the bus that the archive can never write.
func (p *pipeline) publishPoison(t *testing.T, at time.Time) {
	t.Helper()
	payload, err := json.Marshal(ingest.NormalizedBatch{
		BatchID:    uuid.NewString(),
		InstallID:  uuid.NewString(),
		SDKName:    "unity",
		SDKVersion: "1.4.0",
		AppVersion: "2.7.1",
		Platform:   "ios",
		ReceivedAt: at,
		IngestTime: at,
		Events: []ingest.NormalizedEvent{{
			EventID:   uuid.NewString(),
			EventName: "level_complete",
			EventTime: at,
		}},
	})
	if err != nil {
		t.Fatalf("encode the poisoned payload: %v", err)
	}
	if err := p.bus.Publish(context.Background(), &eventbus.Message{
		PublishedAt:   at,
		Cell:          p.settings.Cell,
		DedupKey:      "poison-" + uuid.NewString(),
		OrgID:         uuid.NewString(),
		ProjectID:     "not-a-uuid", // the manifest column is typed; this can never be written
		EnvironmentID: uuid.NewString(),
		Environment:   "production",
		RetentionTier: 30,
		EventCount:    1,
		Payload:       payload,
	}); err != nil {
		t.Fatalf("publish the poisoned message: %v", err)
	}
}

// TestAnUnreachableBucketDoesNotDeadLetterTheBacklog is the counterpart, and the one that
// protects data rather than availability.
//
// A writer that parks on attempt count alone would empty a backlog into the dead-letter
// spool during a GCS outage and call it poison. The archive probes the object store and the
// cell tier before parking anything, so "these bytes are unarchivable" is a question it asks
// rather than a conclusion it draws from a counter.
func TestAnUnreachableBucketDoesNotDeadLetterTheBacklog(t *testing.T) {
	p := newPipeline(t)
	at := time.Now().UTC()

	for range 4 {
		p.post(t, event("session_start", at))
	}

	// p.root *is* the bucket directory. Replacing it with a file makes both Put and Ping
	// fail, which is what an unreachable bucket looks like from inside the writer.
	if err := os.RemoveAll(p.root); err != nil {
		t.Fatalf("remove the bucket: %v", err)
	}
	if err := os.WriteFile(p.root, []byte("x"), 0o600); err != nil {
		t.Fatalf("block the bucket path: %v", err)
	}

	p.run(t)
	time.Sleep(6 * time.Second)

	if got := p.writer.Stats()["dead_lettered"]; got != 0 {
		t.Fatalf("dead_lettered = %d while the bucket was unreachable; a backlog must never "+
			"be parked because storage is down", got)
	}
	if got := p.writer.Stats()["retries"]; got == 0 {
		t.Error("the writer never retried, so this test proved nothing about an outage")
	}
}

// TestTheArchivePathWorksAgainstRealGCS runs the whole path against a real bucket.
//
// Opt-in, because the rest of this file must stay runnable without a cloud account — §14's
// local store exists for exactly that reason. But "the deployment target is never
// exercised" is how a path that compiles turns out not to work, and the two halves that
// only exist in the GCS implementation are precisely the ones a local directory cannot
// stand in for: whether the client authenticates, and whether Put is genuinely durable and
// idempotent on the path.
//
//	ARCHIVE_GCS_BUCKET=playersense-events-eu-1-dev go test ./apps/backend/cmd/archive-writer/ -run RealGCS -v
func TestTheArchivePathWorksAgainstRealGCS(t *testing.T) {
	bucket := os.Getenv("ARCHIVE_GCS_BUCKET")
	if bucket == "" {
		t.Skip("set ARCHIVE_GCS_BUCKET to run this against a real bucket")
	}

	p := newPipeline(t)

	// Swap the local store for the real one. Everything else in the pipeline — the
	// gateway, the bus, the writer, the manifest in the cell tier — is unchanged.
	store := blobstore.NewGCS(bucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("dial cloud storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Stop(context.Background()) })

	if err := store.Ping(t.Context()); err != nil {
		t.Fatalf("the bucket is not reachable: %v", err)
	}

	writer := archive.New(p.bus, store, p.cell, quiet(), archive.Options{
		Cell:        p.settings.Cell,
		Region:      p.settings.CellRegion,
		MaxAttempts: 3,
	})
	if err := writer.Start(t.Context()); err != nil {
		t.Fatalf("start the writer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = writer.Stop(ctx)
	})

	at := time.Now().UTC()
	p.post(t, event("session_start", at), event("level_completed", at))

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && writer.Stats()["objects"] == 0 {
		time.Sleep(200 * time.Millisecond)
	}
	if got := writer.Stats()["objects"]; got != 1 {
		t.Fatalf("wrote %d objects, want 1", got)
	}

	// The manifest is what makes the object visible to §60's expiry, §39's billing and
	// §104's breach scoping. An object without one is indistinguishable from data nobody
	// knows about, so the manifest is the assertion, not the object.
	if got := p.manifests(t); got != 1 {
		t.Fatalf("the cell tier holds %d manifests, want 1", got)
	}

	manifest, err := p.cell.Client.ArchiveManifest.Query().
		Where(archivemanifest.ProjectUIDEQ(uuid.MustParse(p.project.ProjectUID))).
		Only(context.Background())
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	if manifest.Bucket != bucket {
		t.Errorf("the manifest names bucket %q, want %q", manifest.Bucket, bucket)
	}
	if manifest.EventCount != 2 {
		t.Errorf("the manifest counts %d events, want 2", manifest.EventCount)
	}
	if manifest.ObjectSizeBytes == 0 {
		t.Error("the manifest records no object size; §39 bills on this")
	}

	t.Logf("wrote gs://%s/%s (%d bytes, %d events)",
		bucket, manifest.ObjectPath, manifest.ObjectSizeBytes, manifest.EventCount)

	// Leave nothing behind: this is a real bucket and the retention worker (§60) is not
	// built, so nothing else would ever remove it.
	t.Cleanup(func() {
		if err := store.Delete(context.Background(), manifest.ObjectPath); err != nil {
			t.Errorf("clean up %s: %v", manifest.ObjectPath, err)
		}
	})
}
