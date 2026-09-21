package retention_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/retention"
	"playersense.dev/backend/internal/testsupport"
)

// §60's retention worker, against the real database and a real object store.
//
// The thing being tested is not "does Delete delete". It is the state machine around it: an
// object is expired only when its manifest says its time is up, the manifest survives the
// object so §39 can still bill for the days it existed, and the two ways a deletion can fail
// are told apart rather than counted the same.

type fixture struct {
	sweeper *retention.Sweeper
	db      *pgdb.DB
	store   *blobstore.Local
	bucket  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	settings := testsupport.Settings(t)
	settings.ArchiveDir = t.TempDir()

	db := testsupport.Postgres(t, settings)
	store := blobstore.NewLocal(settings.ArchiveDir, settings.ArchiveBucket)
	if err := store.Start(t.Context()); err != nil {
		t.Fatalf("start store: %v", err)
	}

	return &fixture{
		sweeper: &retention.Sweeper{DB: db, Store: store, Log: quiet()},
		db:      db,
		store:   store,
		bucket:  settings.ArchiveBucket,
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// seed writes an object and the manifest that claims it.
//
// `deleteAfter` is relative to the object's newest event, which is how the archive writer
// computes it: the retention tier is counted from the last event in the object, not from
// when it happened to be written.
func (f *fixture) seed(t *testing.T, name string, lastEvent time.Time, tier time.Duration) *ent.ArchiveManifest {
	t.Helper()
	path := "project=" + uuid.NewString() + "/part-" + name + ".parquet"

	object, err := f.store.Put(t.Context(), path, []byte("not really parquet, and nothing here reads it"))
	if err != nil {
		t.Fatalf("write object: %v", err)
	}
	m, err := f.db.Client.ArchiveManifest.Create().
		SetBucket(f.bucket).
		SetObjectPath(object.Path).
		SetOrgUID(uuid.New()).
		SetProjectUID(uuid.New()).
		SetEnvironmentUID(uuid.New()).
		SetMinEventTime(lastEvent.Add(-time.Hour)).
		SetMaxEventTime(lastEvent).
		SetEventCount(10).
		SetObjectSizeBytes(object.Size).
		SetNormalizedInputBytes(100).
		SetSchemaVersion(1).
		SetDeleteAfter(lastEvent.Add(tier)).
		Save(t.Context())
	if err != nil {
		t.Fatalf("record manifest: %v", err)
	}
	t.Cleanup(func() {
		_ = f.db.Client.ArchiveManifest.DeleteOneID(m.ID).Exec(context.Background())
	})
	return m
}

func (f *fixture) reload(t *testing.T, m *ent.ArchiveManifest) *ent.ArchiveManifest {
	t.Helper()
	got, err := f.db.Client.ArchiveManifest.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	return got
}

func (f *fixture) exists(t *testing.T, m *ent.ArchiveManifest) bool {
	t.Helper()
	_, err := f.store.Get(t.Context(), m.ObjectPath)
	if errors.Is(err, blobstore.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	return true
}

// TestAnExpiredObjectIsRemovedAndItsManifestKept is the whole of §60 in one pass.
func TestAnExpiredObjectIsRemovedAndItsManifestKept(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	expired := f.seed(t, "expired", now.AddDate(0, 0, -40), 30*24*time.Hour)

	got, err := f.sweeper.Run(t.Context(), now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.Deleted == 0 {
		t.Fatalf("deleted %d objects, want at least the one that expired", got.Deleted)
	}

	if f.exists(t, expired) {
		t.Error("the object is still in the bucket; an expiry that does not expire is a storage bill")
	}
	after := f.reload(t, expired)
	if after.Status != archivemanifest.StatusDeleted {
		t.Errorf("status = %s, want deleted", after.Status)
	}
	// The row outlives the object deliberately: §39 bills from object_size_bytes, and an
	// object deleted on the 20th still has to be billed for the twenty days it existed.
	if after.ObjectSizeBytes == 0 {
		t.Error("the billable size was lost with the object")
	}
}

// TestAnUnexpiredObjectIsLeftAlone is the half that matters more.
//
// The failure it guards is the one the `expire_at` bug already caused once in ClickHouse
// (docs/compliance/06): a lifecycle mechanism that deletes everything the first time it runs.
func TestAnUnexpiredObjectIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	live := f.seed(t, "live", now.AddDate(0, 0, -1), 30*24*time.Hour)

	if _, err := f.sweeper.Run(t.Context(), now); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !f.exists(t, live) {
		t.Fatal("an object 29 days inside its retention was deleted")
	}
	if got := f.reload(t, live).Status; got != archivemanifest.StatusDurable {
		t.Errorf("status = %s, want durable", got)
	}
}

// TestAnInterruptedDeletionIsResumed covers the crash window between intent and outcome.
//
// A pass that recorded `deleting` and then died leaves a row nothing else would look at.
// Deleting an object that is already gone has to succeed for the resumption to work, which
// is the property both stores promise.
func TestAnInterruptedDeletionIsResumed(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	stuck := f.seed(t, "stuck", now.AddDate(0, 0, -40), 30*24*time.Hour)
	if err := f.db.Client.ArchiveManifest.UpdateOne(stuck).
		SetStatus(archivemanifest.StatusDeleting).Exec(t.Context()); err != nil {
		t.Fatalf("stage the interrupted state: %v", err)
	}
	// And the object is already gone, which is the harder half of the same crash.
	if err := f.store.Delete(t.Context(), stuck.ObjectPath); err != nil {
		t.Fatalf("remove the object: %v", err)
	}

	if _, err := f.sweeper.Run(t.Context(), now); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := f.reload(t, stuck).Status; got != archivemanifest.StatusDeleted {
		t.Errorf("status = %s, want deleted — a row cannot be stranded in `deleting`", got)
	}
}

// TestAManifestStampedBornExpiredIsRefused is the rail against a bad retention tier.
//
// `delete_after` is computed once, at write time, from a number that arrives on a message.
// If that number is ever zero — the shape of mistake `expire_at` already made — every object
// expires as fast as it is written. The sweeper refuses rather than obeys, and says so on the
// row.
func TestAManifestStampedBornExpiredIsRefused(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	// A retention tier of zero: delete_after lands exactly on the newest event.
	bad := f.seed(t, "borked", now.AddDate(0, 0, -1), 0)

	got, err := f.sweeper.Run(t.Context(), now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.Deleted != 0 {
		t.Errorf("deleted %d objects; a manifest that cannot be right must not be acted on", got.Deleted)
	}
	if !f.exists(t, bad) {
		t.Fatal("the object was destroyed on the strength of a delete_after that cannot be right")
	}
	after := f.reload(t, bad)
	if after.Status != archivemanifest.StatusFailed {
		t.Errorf("status = %s, want failed — the refusal has to be visible", after.Status)
	}
	if after.FailureReason == "" {
		t.Error("nothing on the row says why it was refused")
	}
}

// TestARedactedObjectIsNeverExpired keeps §60 and §61 out of each other's way.
//
// An erasure replaces an object and marks the old manifest `redacted`. The object is already
// gone, so a sweeper that treated that state as expirable would spend a delete on it every
// pass and record a failure when the store said it did not exist.
func TestARedactedObjectIsNeverExpired(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	replaced := f.seed(t, "replaced", now.AddDate(0, 0, -40), 30*24*time.Hour)
	if err := f.db.Client.ArchiveManifest.UpdateOne(replaced).
		SetStatus(archivemanifest.StatusRedacted).Exec(t.Context()); err != nil {
		t.Fatalf("stage the redacted state: %v", err)
	}

	if _, err := f.sweeper.Run(t.Context(), now); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := f.reload(t, replaced).Status; got != archivemanifest.StatusRedacted {
		t.Errorf("status = %s, want redacted — §60 must not pick up §61's rows", got)
	}
}
