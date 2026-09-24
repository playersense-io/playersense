package erasure_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/ent/datadeletionrequest"
	"playersense.dev/backend/ent/dataexportrequest"
	"playersense.dev/backend/internal/archive"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/erasure"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/tombstone"
	"playersense.dev/backend/internal/valkey"
)

// §61's erasure, against the real stack: real events in ClickHouse, a real Parquet object in
// a real bucket, a real manifest and a real tombstone.
//
// The interesting assertions are not "the row is gone". They are the ones about the parts of
// an erasure that are easy to get wrong and impossible to notice:
//
//   - the archive is rewritten rather than deleted, so erasing one person does not erase the
//     hundreds of others whose events share the object;
//   - the tombstone is up before anything is removed, so the queue stops feeding the person
//     back in while the purge runs (§06);
//   - `completed` waits for the drain window, so it means "and nothing has arrived since"
//     rather than "we ran once".

const (
	erased    = "player-erased"
	bystander = "player-bystander"
)

type fixture struct {
	purger *erasure.Purger
	pg     *pgdb.DB
	db     *sql.DB
	store  *blobstore.Local
	cache  *valkey.Client

	settings   config.Settings
	project    uint32
	projectUID uuid.UUID
	orgUID     uuid.UUID
	envUID     uuid.UUID

	installErased    uuid.UUID
	installBystander uuid.UUID
}

// pool hands the purger a handle the application would normally open for it.
type pool struct{ db *sql.DB }

func (p pool) DB() (*sql.DB, error) { return p.db, nil }

func newFixture(t *testing.T, drain time.Duration) *fixture {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.ArchiveDir = t.TempDir()

	pg := testsupport.Postgres(t, settings)
	cache := testsupport.Cache(t, settings)

	opts, err := clickhouse.ParseDSN(settings.ClickHouseDSN)
	if err != nil {
		t.Fatal(err)
	}
	db := clickhouse.OpenDB(opts)
	if err := db.Ping(); err != nil {
		t.Skipf("no clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("start store: %v", err)
	}

	f := &fixture{
		pg:    pg,
		db:    db,
		store: store,
		cache: cache,

		settings: settings,
		// A tenant surrogate no previous run has used. `ALTER TABLE ... DELETE` leaves a
		// mutation that keeps being applied to parts inserted afterwards, so reusing a
		// project_seq means a later run's rows vanish silently — the same hazard the cohort
		// backfill tests record, and this package is the one that creates those mutations.
		project:          uniqueProject(),
		projectUID:       uuid.New(),
		orgUID:           uuid.New(),
		envUID:           uuid.New(),
		installErased:    uuid.New(),
		installBystander: uuid.New(),
	}
	f.purger = &erasure.Purger{
		DB:          pg,
		CH:          pool{db},
		Store:       store,
		Cache:       cache,
		Log:         quiet(),
		DrainWindow: drain,
	}
	return f
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func uniqueProject() uint32 {
	return uint32(900_000_000 + time.Now().UnixNano()%50_000_000)
}

// seedEvents writes one event for each actor, on its own install.
func (f *fixture) seedEvents(t *testing.T, at time.Time) {
	t.Helper()
	f.insertEvent(t, erased, f.installErased, at)
	f.insertEvent(t, bystander, f.installBystander, at)
}

func (f *fixture) insertEvent(t *testing.T, player string, install uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `
		INSERT INTO events_raw
		(org_seq, project_seq, environment_seq, org_id, project_id, environment_id,
		 retention_tier, policy_tier, event_time, ingest_time, received_at,
		 event_id, batch_id, session_id, player_id, install_id,
		 event_name, event_type, platform, country_code, app_version, sdk_name, sdk_version,
		 cohort_date, install_platform, install_country, first_app_version,
		 consent_analytics, consent_policy_version, att_status,
		 revenue_micros, currency, playtime_ms, payload_bytes)
		VALUES (1, ?, 1, toUUID(?), toUUID(?), toUUID(?),
		        30, 'standard', ?, ?, ?,
		        generateUUIDv4(), generateUUIDv4(), 'session-1', ?, toUUID(?),
		        'gem_pack', 'purchase', 'ios', 'TR', '2.7.1', 'unity', '1.4.0',
		        toDate(?), 'ios', 'TR', '2.7.1',
		        1, '2026-01-15', 'authorized',
		        990000, 'USD', 1000, 200)`,
		f.project, f.orgUID.String(), f.projectUID.String(), f.envUID.String(),
		at, at, at, player, install.String(), at); err != nil {
		t.Fatalf("seed event for %s: %v", player, err)
	}
}

// deviceID computes the surrogate the way events_raw materializes it, by asking the server.
//
// Not reimplemented in Go on purpose: a test that computed the hash itself would keep passing
// after the column's definition changed, which is the one thing it must not do.
func (f *fixture) deviceID(t *testing.T, install uuid.UUID) uint64 {
	t.Helper()
	var id uint64
	if err := f.db.QueryRowContext(t.Context(),
		`SELECT sipHash64(?, toUInt16(1), ?)`, f.project, install.String()).Scan(&id); err != nil {
		t.Fatalf("compute device id: %v", err)
	}
	return id
}

// seedArchive writes one object holding both actors' events, and the manifest for it.
func (f *fixture) seedArchive(t *testing.T, at time.Time) *ent.ArchiveManifest {
	return f.seedArchiveNamed(t, at, "seed")
}

func (f *fixture) seedArchiveNamed(t *testing.T, at time.Time, name string) *ent.ArchiveManifest {
	t.Helper()

	rows := []archive.Row{
		f.archiveRow(erased, f.installErased, at),
		f.archiveRow(bystander, f.installBystander, at),
		f.archiveRow(erased, f.installErased, at.Add(time.Minute)),
	}
	body, err := archive.Encode(rows)
	if err != nil {
		t.Fatalf("encode archive object: %v", err)
	}
	path := "org=" + f.orgUID.String() + "/project=" + f.projectUID.String() +
		"/env=production/dt=" + at.Format(time.DateOnly) + "/hour=00/part-" + name + ".parquet"

	object, err := f.store.Put(t.Context(), path, body)
	if err != nil {
		t.Fatalf("write archive object: %v", err)
	}
	m, err := f.pg.Client.ArchiveManifest.Create().
		SetBucket(f.settings.ArchiveBucket).
		SetObjectPath(object.Path).
		SetOrgUID(f.orgUID).
		SetProjectUID(f.projectUID).
		SetEnvironmentUID(f.envUID).
		SetMinEventTime(at).
		SetMaxEventTime(at.Add(time.Minute)).
		SetEventCount(uint64(len(rows))).
		SetObjectSizeBytes(object.Size).
		SetNormalizedInputBytes(600).
		SetSchemaVersion(archive.SchemaVersion).
		SetDeleteAfter(at.AddDate(0, 0, 30)).
		Save(t.Context())
	if err != nil {
		t.Fatalf("record manifest: %v", err)
	}
	return m
}

func (f *fixture) archiveRow(player string, install uuid.UUID, at time.Time) archive.Row {
	return archive.Row{
		OrgSeq: 1, ProjectSeq: f.project, EnvironmentSeq: 1,
		OrgID: f.orgUID.String(), ProjectID: f.projectUID.String(), EnvironmentID: f.envUID.String(),
		RetentionTier: 30, PolicyTier: "standard",
		EventTime: at.UTC(), IngestTime: at.UTC(), ReceivedAt: at.UTC(),
		EventID: uuid.NewString(), BatchID: uuid.NewString(),
		PlayerID: player, InstallID: install.String(),
		EventName: "level_completed", EventType: "custom",
		Platform: "ios", CountryCode: "TR", AppVersion: "2.7.1",
		ConsentAnalytics: 1, PayloadBytes: 200,
	}
}

// request records a deletion request the way the API surface does.
func (f *fixture) request(t *testing.T, actorKey string) *ent.DataDeletionRequest {
	t.Helper()
	req, err := f.pg.Client.DataDeletionRequest.Create().
		SetProjectUID(f.projectUID).
		SetProjectSeq(f.project).
		SetActorKey(actorKey).
		Save(t.Context())
	if err != nil {
		t.Fatalf("record deletion request: %v", err)
	}
	t.Cleanup(func() {
		_ = f.pg.Client.DataDeletionRequest.DeleteOneID(req.ID).Exec(context.Background())
	})
	return req
}

func (f *fixture) countEvents(t *testing.T, player string) int {
	t.Helper()
	var n uint64
	if err := f.db.QueryRowContext(t.Context(),
		`SELECT count() FROM events_raw WHERE project_seq = ? AND actor_key = ?`,
		f.project, player).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return int(n)
}

func (f *fixture) countDevice(t *testing.T, table string, install uuid.UUID) int {
	t.Helper()
	var n uint64
	if err := f.db.QueryRowContext(t.Context(),
		`SELECT count() FROM `+table+` WHERE project_seq = ? AND device_id = ?`,
		f.project, f.deviceID(t, install)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return int(n)
}

// countAccount counts the account-level aggregate's rows for one player.
//
// On account_key rather than on the surrogate, for the same reason the purge matches on it:
// reproducing ClickHouse's sipHash64 in Go would be a second implementation of a key that
// has to agree exactly.
func (f *fixture) countAccount(t *testing.T, player string) int {
	t.Helper()
	var n uint64
	if err := f.db.QueryRowContext(t.Context(),
		`SELECT count() FROM account_lifetime WHERE project_seq = ? AND account_key = ?`,
		f.project, player).Scan(&n); err != nil {
		t.Fatalf("count account_lifetime: %v", err)
	}
	return int(n)
}

func (f *fixture) reloadRequest(t *testing.T, req *ent.DataDeletionRequest) *ent.DataDeletionRequest {
	t.Helper()
	got, err := f.pg.Client.DataDeletionRequest.Get(t.Context(), req.ID)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	return got
}

// TestAnErasureRemovesThePersonAndKeepsEveryoneElse is the whole flow.
func TestAnErasureRemovesThePersonAndKeepsEveryoneElse(t *testing.T) {
	// No drain window: the request completes in one pass, which is what makes the
	// assertions about `completed` meaningful here rather than a timing race.
	f := newFixture(t, time.Nanosecond)
	at := time.Now().UTC().Add(-time.Hour)

	f.seedEvents(t, at)
	manifest := f.seedArchive(t, at)
	req := f.request(t, erased)

	got, err := f.purger.Run(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got.Completed == 0 {
		t.Fatalf("completed %d requests, want at least this one", got.Completed)
	}

	// The events.
	if n := f.countEvents(t, erased); n != 0 {
		t.Errorf("%d events survive for the erased actor, want 0", n)
	}
	if n := f.countEvents(t, bystander); n != 1 {
		t.Errorf("%d events survive for the bystander, want 1 — an erasure is not a project wipe", n)
	}

	// The device-keyed tables, which §61 names explicitly because they are easy to overlook:
	// none holds events, every one holds a person. All are filled by the materialized views
	// behind the insert above, so this is the production path rather than a fixture.
	for _, table := range []string{
		"sessions", "purchases", "payers", "player_lifetime", "player_activity_daily",
	} {
		if n := f.countDevice(t, table, f.installErased); n != 0 {
			t.Errorf("%s still holds %d rows for the erased device, want 0", table, n)
		}
		// At least one: an AggregatingMergeTree holds a device as several partial rows
		// until its parts merge (§29.6), so the count is "still there" rather than a
		// number.
		if n := f.countDevice(t, table, f.installBystander); n == 0 {
			t.Errorf("%s lost the bystander's device; an erasure is not a project wipe", table)
		}
	}

	// The account-level aggregate, which holds the studio's own player identifier and is
	// therefore personal data. A per-player table outside the erasure path is a person who
	// was told they were deleted and was not.
	if n := f.countAccount(t, erased); n != 0 {
		t.Errorf("account_lifetime still holds %d rows for the erased player, want 0", n)
	}
	if n := f.countAccount(t, bystander); n == 0 {
		t.Error("account_lifetime lost the bystander; an erasure is not a project wipe")
	}

	// The archive: rewritten, not deleted. An object holds one project-hour — hundreds of
	// people — so removing it to erase one of them would erase all of them.
	after := f.reloadManifest(t, manifest)
	if after.Status != archivemanifest.StatusRedacted {
		t.Errorf("the original manifest is %s, want redacted", after.Status)
	}
	if _, err := f.store.Get(t.Context(), manifest.ObjectPath); err == nil {
		t.Error("the original object is still in the bucket")
	}

	replacement := f.replacement(t, manifest)
	rows := f.rowsIn(t, replacement.ObjectPath)
	if len(rows) != 1 {
		t.Fatalf("the redacted object holds %d rows, want 1", len(rows))
	}
	if rows[0].PlayerID != bystander {
		t.Errorf("the surviving row belongs to %q, want the bystander", rows[0].PlayerID)
	}
	// Retention is not extended by a redaction: the replacement expires when the original
	// would have.
	if !replacement.DeleteAfter.Equal(after.DeleteAfter) {
		t.Errorf("delete_after moved from %s to %s; a rewrite must not extend retention",
			after.DeleteAfter, replacement.DeleteAfter)
	}
	// And the bounds describe what is actually inside it now.
	if replacement.EventCount != 1 {
		t.Errorf("event_count = %d, want 1", replacement.EventCount)
	}

	// The audit record, which outlives the data it describes.
	record := f.reloadRequest(t, req)
	if record.Status != datadeletionrequest.StatusCompleted {
		t.Errorf("status = %s, want completed", record.Status)
	}
	if record.CompletedAt == nil {
		t.Error("completed with no timestamp; the SLA is measured against it")
	}
	if record.TombstonedAt == nil {
		t.Error("no tombstone timestamp; §05's first milestone is missing")
	}
	if record.DevicesDeleted == 0 {
		t.Error("the record does not say how many devices were removed")
	}
	if record.ObjectsRedacted == 0 {
		t.Error("the record does not say the archive was touched")
	}
}

// TestTheTombstoneGoesUpBeforeAnythingIsRemoved is §06's ordering rule.
//
// The tombstone is what makes the asynchrony safe: the data stops being *used* immediately,
// hours before it stops existing. A purge that removed rows first and tombstoned afterwards
// would leave a window in which the queue refills what was just deleted.
func TestTheTombstoneGoesUpBeforeAnythingIsRemoved(t *testing.T) {
	f := newFixture(t, 24*time.Hour)
	at := time.Now().UTC().Add(-time.Hour)

	f.seedEvents(t, at)
	req := f.request(t, erased)

	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("purge: %v", err)
	}

	up, err := f.cache.Exists(t.Context(), tombstone.Actor(f.cache, f.projectUID.String(), erased))
	if err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if !up {
		t.Fatal("no tombstone; the writer would keep storing this actor's events")
	}

	// And a bystander is not silenced by somebody else's erasure.
	other, err := f.cache.Exists(t.Context(), tombstone.Actor(f.cache, f.projectUID.String(), bystander))
	if err != nil {
		t.Fatalf("read the tombstone: %v", err)
	}
	if other {
		t.Error("the bystander was tombstoned too")
	}

	record := f.reloadRequest(t, req)
	if record.TombstonedAt == nil {
		t.Error("the tombstone is up and the record does not say so")
	}
}

// TestCompletionWaitsForTheDrainWindow is what makes `completed` worth believing.
//
// §21 guarantees at-least-once delivery and a queue can hold messages for hours, so events
// for an erased actor keep arriving after the purge has run. A request that completed on the
// first pass would be claiming something it cannot know yet.
func TestCompletionWaitsForTheDrainWindow(t *testing.T) {
	f := newFixture(t, 24*time.Hour)
	at := time.Now().UTC().Add(-time.Hour)

	f.seedEvents(t, at)
	req := f.request(t, erased)

	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("purge: %v", err)
	}

	record := f.reloadRequest(t, req)
	if record.Status != datadeletionrequest.StatusPurging {
		t.Errorf("status = %s, want purging — the window has not closed", record.Status)
	}
	if record.CompletedAt != nil {
		t.Error("completed before the drain window closed")
	}
	// The removal itself has already happened; only the claim is being withheld.
	if n := f.countEvents(t, erased); n != 0 {
		t.Errorf("%d events survive, want 0 — the wait is on the claim, not on the work", n)
	}

	// A pass after the window closes finishes it.
	if _, err := f.purger.Run(t.Context(), time.Now().UTC().Add(25*time.Hour)); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if got := f.reloadRequest(t, req).Status; got != datadeletionrequest.StatusCompleted {
		t.Errorf("status = %s, want completed", got)
	}
}

// TestAnErasureIsIdempotent covers the retry after a crash.
//
// Every step is a mutation or an overwrite, and the archive rewrite names its output from the
// rows that survive — so running the whole thing twice has to land in the same place rather
// than accumulate a second redacted object.
func TestAnErasureIsIdempotent(t *testing.T) {
	f := newFixture(t, time.Nanosecond)
	at := time.Now().UTC().Add(-time.Hour)

	f.seedEvents(t, at)
	manifest := f.seedArchive(t, at)
	req := f.request(t, erased)

	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	first := f.replacement(t, manifest)

	// Back to purging, as a crashed pass would leave it, and run again.
	if err := f.pg.Client.DataDeletionRequest.UpdateOne(req).
		SetStatus(datadeletionrequest.StatusPurging).
		ClearCompletedAt().Exec(t.Context()); err != nil {
		t.Fatalf("stage the retry: %v", err)
	}
	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("second purge: %v", err)
	}

	objects, err := f.pg.Client.ArchiveManifest.Query().
		Where(
			archivemanifest.ProjectUID(f.projectUID),
			archivemanifest.StatusEQ(archivemanifest.StatusDurable),
		).
		All(t.Context())
	if err != nil {
		t.Fatalf("list manifests: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("%d live manifests after two passes, want 1 — the rewrite must overwrite, not accumulate", len(objects))
	}
	if objects[0].ObjectPath != first.ObjectPath {
		t.Errorf("the second pass wrote %s, want the same path as the first (%s)",
			objects[0].ObjectPath, first.ObjectPath)
	}
}

// reloadManifest re-reads one manifest.
func (f *fixture) reloadManifest(t *testing.T, m *ent.ArchiveManifest) *ent.ArchiveManifest {
	t.Helper()
	got, err := f.pg.Client.ArchiveManifest.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	return got
}

// replacement finds the durable manifest the redaction produced.
func (f *fixture) replacement(t *testing.T, original *ent.ArchiveManifest) *ent.ArchiveManifest {
	t.Helper()
	got, err := f.pg.Client.ArchiveManifest.Query().
		Where(
			archivemanifest.ProjectUID(f.projectUID),
			archivemanifest.StatusEQ(archivemanifest.StatusDurable),
			archivemanifest.ObjectPathNEQ(original.ObjectPath),
		).
		Only(t.Context())
	if err != nil {
		t.Fatalf("find the redacted object's manifest: %v", err)
	}
	return got
}

func (f *fixture) rowsIn(t *testing.T, path string) []archive.Row {
	t.Helper()
	body, err := f.store.Get(t.Context(), path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rows, err := archive.Decode(body)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return rows
}

// TestAnErasureRemovesTheExportBundleToo closes the easiest way to leave a copy behind.
//
// An export is a ready-made file holding everything PlayerSense knows about one person,
// parked in the cell's bucket until it expires. An erasure that rewrote the archive, emptied
// three ClickHouse tables and left that file would have deleted every hard copy and kept the
// easy one.
func TestAnErasureRemovesTheExportBundleToo(t *testing.T) {
	f := newFixture(t, time.Nanosecond)
	at := time.Now().UTC().Add(-time.Hour)

	f.seedEvents(t, at)

	// A bundle, as §101's exporter would have left it.
	path := "export/project=" + f.projectUID.String() + "/" + uuid.NewString() + ".ndjson"
	if _, err := f.store.Put(t.Context(), path, []byte(`{"section":"meta"}`+"\n")); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	bundle, err := f.pg.Client.DataExportRequest.Create().
		SetProjectUID(f.projectUID).
		SetProjectSeq(f.project).
		SetActorKey(erased).
		SetStatus(dataexportrequest.StatusCompleted).
		SetBucket(f.settings.ArchiveBucket).
		SetObjectPath(path).
		SetObjectSizeBytes(19).
		SetRowCount(1).
		Save(t.Context())
	if err != nil {
		t.Fatalf("record export: %v", err)
	}
	t.Cleanup(func() {
		_ = f.pg.Client.DataExportRequest.DeleteOneID(bundle.ID).Exec(context.Background())
	})

	f.request(t, erased)
	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := f.store.Get(t.Context(), path); err == nil {
		t.Error("the export bundle is still in the bucket after the erasure")
	}
	after, err := f.pg.Client.DataExportRequest.Get(t.Context(), bundle.ID)
	if err != nil {
		t.Fatalf("reload export: %v", err)
	}
	if after.Status != dataexportrequest.StatusExpired {
		t.Errorf("status = %s, want expired", after.Status)
	}
	// The record survives: that an export was produced is a fact about processing, and the
	// audit trail of a deletion has to outlive the data (§06).
	if after.ActorKey != erased {
		t.Error("the export record was destroyed rather than marked")
	}
}

// TestABoundedArchiveScanResumesRatherThanSkipping is the hole a per-pass bound opens.
//
// The archive half of a purge is bounded, so a subject with a long span takes several passes.
// The obvious resume point — "objects written since the last pass ran" — is wrong in exactly
// the case that matters: a first pass that filled its bound left every *older* object
// unexamined, and that cut-off would skip them permanently. A completed erasure would then be
// a false statement, which is the worst thing this package can produce.
//
// So a pass that filled its bound refuses to complete however long the drain window has been
// open, and the next one resumes from the manifest it stopped at.
func TestABoundedArchiveScanResumesRatherThanSkipping(t *testing.T) {
	f := newFixture(t, time.Nanosecond)
	f.purger.MaxObjects = 1

	at := time.Now().UTC().Add(-time.Hour)
	f.seedEvents(t, at)
	f.seedArchiveNamed(t, at, "one")
	f.seedArchiveNamed(t, at.Add(2*time.Minute), "two")
	req := f.request(t, erased)

	// One pass, one object. The drain window has closed and it still must not complete.
	if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	after := f.reloadRequest(t, req)
	if after.Status != datadeletionrequest.StatusPurging {
		t.Errorf("status = %s after a truncated scan, want purging — an erasure that stopped "+
			"at its bound must not report success", after.Status)
	}
	if after.ArchiveCursor == 0 {
		t.Fatal("nothing records where the scan stopped, so the next pass cannot resume")
	}

	// Further passes finish it. More than two, because each redaction writes a replacement
	// whose own manifest is newer than the watermark and is therefore read once and found
	// clean — one extra object per rewrite, which is what a resume point built from
	// `created_at` costs and is why a real bound is five hundred rather than one.
	var status datadeletionrequest.Status
	for pass := 0; pass < 6; pass++ {
		if _, err := f.purger.Run(t.Context(), time.Now().UTC()); err != nil {
			t.Fatalf("purge %d: %v", pass+2, err)
		}
		if status = f.reloadRequest(t, req).Status; status == datadeletionrequest.StatusCompleted {
			break
		}
	}
	if status != datadeletionrequest.StatusCompleted {
		t.Errorf("status = %s after six more passes, want completed — a bounded scan has to converge", status)
	}

	// And both seeded objects were actually redacted, which is the claim the status makes.
	redacted, err := f.pg.Client.ArchiveManifest.Query().
		Where(
			archivemanifest.ProjectUID(f.projectUID),
			archivemanifest.StatusEQ(archivemanifest.StatusRedacted),
		).
		Count(t.Context())
	if err != nil {
		t.Fatalf("count redacted manifests: %v", err)
	}
	if redacted != 2 {
		t.Errorf("%d objects were redacted, want both", redacted)
	}
}
