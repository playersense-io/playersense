// Package retention expires archive objects whose time is up (project.md §60).
//
// `delete_after` is stamped onto every manifest when the object is written (§14.2), derived
// from the project's retention tier at that moment rather than read back at deletion time —
// so lowering a customer's retention does not retroactively destroy an archive they already
// paid for, and raising it does not resurrect what already expired. Until this package
// existed, nothing read that column: every object ever written was still in the bucket,
// which is a storage bill that only grows and a retention promise that was never kept.
//
// # Application-controlled, not a lifecycle rule
//
// §60 allows a bucket lifecycle rule as "a global safety net" and insists the real expiry
// stay in the application. The reason is the column above. A lifecycle rule can only express
// an age — one number for the whole bucket — and retention here is a per-project term. A
// rule set to the longest tier keeps every short-tier object far too long; one set to the
// shortest destroys data a customer is paying to keep. Neither is a retention policy.
//
// # What a pass does, and what it refuses to do
//
// The order is: record the intent, delete the object, record the outcome. A crash anywhere
// in the middle leaves a row in `deleting`, which the next pass picks up and finishes —
// object deletion is idempotent in both stores, so a second attempt on an object that is
// already gone succeeds rather than failing.
//
// The manifest row is never removed, only marked. §39 bills storage from
// `object_size_bytes`, and an object deleted on the 20th still has to be billed for the
// twenty days it existed; a row deleted with its object takes that history with it. The row
// is also the audit record §60 item 5 asks for — who expired, when, and with what outcome is
// a query rather than a log search.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/pgdb"
)

// DefaultMaxObjects bounds one pass.
//
// A bound rather than a target. The first pass over a bucket that has never been swept has
// every expired object in it at once, and an unbounded pass would hold a transaction-shaped
// amount of work open against both the database and the bucket. Several bounded passes
// reach the same place, and the sweeper runs on an interval rather than nightly.
const DefaultMaxObjects = 5_000

// Sweeper expires the objects whose manifests say their time is up.
//
// It only ever considers manifests naming the bucket it holds. A manifest for some other
// bucket — a rename, a migration, a process pointed at the wrong store — is left alone,
// because the object path in a manifest is meaningful only inside the bucket it was written
// to, and deleting that path from a different bucket would remove an unrelated object.
type Sweeper struct {
	DB    *pgdb.DB
	Store blobstore.Store
	Log   *slog.Logger

	// MaxObjects bounds one pass. Zero takes DefaultMaxObjects.
	MaxObjects int
}

// Result reports what one pass did.
type Result struct {
	// Due is how many expired manifests the pass considered, which is capped by
	// MaxObjects — Due == MaxObjects means there is more to do and the next pass will
	// find it.
	Due int

	Deleted int
	Failed  int

	// Bytes is what the deleted objects occupied, as billed (§39).
	Bytes uint64
}

// ErrStoreUnreachable reports a pass abandoned because the bucket stopped answering.
//
// Distinguished from an object that cannot be deleted, and the distinction is the whole of
// the failure handling. A bucket outage is transient and affects every object, so the pass
// stops and the rows it had claimed go back to `durable` to be retried. An object that
// cannot be deleted while the bucket is demonstrably healthy is a problem with that object,
// so it is marked `failed` and made visible rather than retried forever in silence
// (Appendix C.13). Telling the two apart by counting attempts cannot work — the counts look
// identical — so the store is asked (ADR 0023).
var ErrStoreUnreachable = errors.New("the archive bucket is unreachable")

// Run expires everything due as of now, up to the pass bound.
func (s *Sweeper) Run(ctx context.Context, now time.Time) (Result, error) {
	var out Result

	if !s.DB.Ready() {
		return out, pgdb.ErrNotStarted
	}
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	limit := s.MaxObjects
	if limit <= 0 {
		limit = DefaultMaxObjects
	}

	due, err := s.DB.Client.ArchiveManifest.Query().
		Where(
			archivemanifest.Bucket(s.Store.Bucket()),
			archivemanifest.DeleteAfterLTE(now.UTC()),
			// `deleting` as well as `durable`: that is how a pass interrupted between the
			// intent and the outcome is resumed. A row cannot be stranded in it.
			archivemanifest.StatusIn(archivemanifest.StatusDurable, archivemanifest.StatusDeleting),
		).
		// Oldest expiry first, so a backlog is worked off in the order it accrued and the
		// object that has been over-retained longest is the one that goes first.
		Order(ent.Asc(archivemanifest.FieldDeleteAfter)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return out, fmt.Errorf("find expired objects: %w", err)
	}
	out.Due = len(due)
	if out.Due == 0 {
		return out, nil
	}

	for _, m := range due {
		// The rail this package needs most, and it exists because this system has already
		// been bitten once by a lifecycle column with a wrong default: `expire_at` on
		// events_raw defaulted to the epoch and `TTL expire_at DELETE` then expired every
		// row on the first merge (docs/compliance/06). The same mistake here — a
		// `delete_after` left zero, or a retention tier that arrived as zero — would empty
		// the archive as fast as it was written, and the archive is the copy that has no
		// second copy.
		//
		// The invariant is that an object cannot expire before the last event inside it,
		// which holds for every retention tier of a day or more and fails only for a
		// number nobody meant. A tier of zero is not "delete immediately"; it is a
		// misconfiguration, and saying so is better than acting on it.
		if !m.DeleteAfter.After(m.MaxEventTime) {
			out.Failed++
			log.Error("refusing to expire an object that was stamped as born expired",
				"object_path", m.ObjectPath,
				"delete_after", m.DeleteAfter,
				"max_event_time", m.MaxEventTime,
				"project_uid", m.ProjectUID)
			if err := s.mark(ctx, m, archivemanifest.StatusFailed,
				"delete_after is not after max_event_time; the retention tier on this "+
					"manifest cannot be right, and expiring it would destroy an archive "+
					"nobody asked to expire"); err != nil {
				return out, err
			}
			continue
		}

		// Intent before action. The row says `deleting` before the object is touched, so
		// an object that disappears without a manifest to explain it is not a state this
		// code can produce.
		if err := s.mark(ctx, m, archivemanifest.StatusDeleting, ""); err != nil {
			return out, err
		}

		if err := s.Store.Delete(ctx, m.ObjectPath); err != nil {
			if pingErr := s.Store.Ping(ctx); pingErr != nil {
				// Transient and not about this object. Hand the row back so the next
				// pass retries it, and stop: every remaining object would fail the same
				// way, and a thousand identical failures is a thousand rows to clean up
				// rather than one error to read.
				if markErr := s.mark(ctx, m, archivemanifest.StatusDurable,
					"the bucket was unreachable: "+pingErr.Error()); markErr != nil {
					return out, markErr
				}
				return out, fmt.Errorf("%w: %w", ErrStoreUnreachable, pingErr)
			}
			// The bucket answers and this object still will not go. That is a problem
			// with the object — a permission on the path, a retention lock, a hold — and
			// no number of retries changes it.
			out.Failed++
			log.Error("an archive object could not be expired",
				"object_path", m.ObjectPath, "bucket", m.Bucket, "error", err)
			if markErr := s.mark(ctx, m, archivemanifest.StatusFailed, err.Error()); markErr != nil {
				return out, markErr
			}
			continue
		}

		if err := s.mark(ctx, m, archivemanifest.StatusDeleted, ""); err != nil {
			return out, err
		}
		out.Deleted++
		out.Bytes += m.ObjectSizeBytes
	}

	return out, nil
}

// mark records a manifest's new state, and the reason when there is one.
//
// The reason is cleared on every state that is not a failure, so a row that failed once and
// then succeeded does not keep an explanation of something that is no longer true.
func (s *Sweeper) mark(ctx context.Context, m *ent.ArchiveManifest, status archivemanifest.Status, reason string) error {
	update := s.DB.Client.ArchiveManifest.UpdateOne(m).SetStatus(status)
	if reason == "" {
		update = update.ClearFailureReason()
	} else {
		update = update.SetFailureReason(pgdb.Reason(reason))
	}
	if err := update.Exec(ctx); err != nil {
		return fmt.Errorf("mark %s as %s: %w", m.ObjectPath, status, err)
	}
	return nil
}
