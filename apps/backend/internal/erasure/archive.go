package erasure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"playersense.dev/backend/ent/regional"
	"playersense.dev/backend/ent/regional/archivemanifest"
	"playersense.dev/backend/internal/archive"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/subject"
)

// DefaultMaxObjects bounds the archive work inside one request's turn.
//
// The archive is the expensive half of an erasure — every candidate object is downloaded,
// decoded and, if it holds the person, written back — so a request covering a long span is
// several bounded passes rather than one that runs for an hour. The request stays in
// `purging` between them, which is a state it was going to be in anyway.
const DefaultMaxObjects = 500

// redaction reports what one request's turn did to the archive.
type redaction struct {
	// Objects is how many objects were rewritten or removed — the number that goes on the
	// audit record. Objects merely read and found not to contain the person are not
	// counted, because nothing happened to them.
	Objects int

	Rewritten int
	Removed   int
	Missing   int

	// Cursor is the id of the last manifest this pass examined. The next pass resumes
	// strictly after it. Zero when the pass examined nothing, which leaves the stored
	// cursor where it was.
	Cursor int

	// Truncated says the pass filled its bound and there is more archive behind it. The
	// request cannot complete while this is true, however long its drain window has been
	// open: an erasure that stopped at five hundred objects and reported success would be
	// the worst failure this package can produce.
	Truncated bool
}

// redactArchive removes one person from the cold copy (§61, §14).
//
// # Why rewriting, rather than deleting
//
// An archive object holds one project, one environment and one hour (§14.1) — hundreds of
// people. Deleting it to erase one of them would destroy everyone else's events, so the
// object is read, the person's rows are dropped, and what is left is written back as a new
// object. Only an object that held nobody else is deleted outright.
//
// # Why this is safe to interrupt
//
// The new object is written and its manifest committed **before** the old object is touched,
// which is the same ordering the writer uses and for the same reason (Appendix C.2): the
// window a crash can land in contains two copies rather than none.
//
// A retry then re-reads the old object — its manifest is still `durable` — and produces
// byte-for-byte the same redacted content, therefore the same digest, therefore the same
// path. The second Put overwrites the first and the second manifest insert is a conflict
// that does nothing. That is §22's content-addressing doing a second job it was not designed
// for: it was chosen to make a redelivered flush idempotent, and it makes an interrupted
// redaction idempotent for free.
func (p *Purger) redactArchive(
	ctx context.Context,
	req *regional.DataDeletionRequest,
	s subject.Subject,
	cursor int,
	log *slog.Logger,
) (redaction, error) {
	var out redaction

	limit := p.MaxObjects
	if limit <= 0 {
		limit = DefaultMaxObjects
	}

	q := p.Cell.Client.ArchiveManifest.Query().Where(
		archivemanifest.ProjectUID(req.ProjectUID),
		// This cell's bucket and no other. An object path means something only inside the
		// bucket it was written to, and a manifest naming a different one belongs to a
		// store this process is not holding.
		archivemanifest.Bucket(p.Store.Bucket()),
		// Every state in which the bytes may still be there. `deleting` is a §60 pass that
		// was interrupted, and `failed` is one whose object refused to go — in both the
		// object may exist, and an erasure that skipped them would leave the person in the
		// archive because a different job had a bad day.
		archivemanifest.StatusIn(
			archivemanifest.StatusDurable,
			archivemanifest.StatusDeleting,
			archivemanifest.StatusFailed,
		),
	)
	if !s.First.IsZero() {
		// Objects that overlap the span the person was active in. An object entirely
		// before their first event or after their last cannot hold them.
		q = q.Where(
			archivemanifest.MaxEventTimeGTE(s.First),
			archivemanifest.MinEventTimeLTE(s.Last),
		)
	}
	if cursor > 0 {
		// Strictly after the last manifest examined. A manifest written while this pass is
		// running gets a higher id and is picked up by the next one, which is why the
		// cursor needs no margin the way a wall-clock resume point would.
		q = q.Where(archivemanifest.IDGT(cursor))
	}

	// Ordered by the column the cursor is taken from, which is what makes resuming well
	// defined. Ordering by event time instead — the intuitive choice, since that is what the
	// window filters on — would leave a truncated pass with no way to say where it stopped
	// that the next one could act on.
	//
	// One row past the bound, and never examined. Asking for exactly the bound cannot tell
	// "that was the last object" from "there is more behind it" — both return the same count
	// — and a pass that always believes it was truncated is a request that never completes.
	// The extra row answers that question and nothing else.
	candidates, err := q.
		Order(regional.Asc(archivemanifest.FieldID)).
		Limit(limit + 1).
		All(ctx)
	if err != nil {
		return out, fmt.Errorf("find the subject's archive objects: %w", err)
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
		out.Truncated = true
	}
	if len(candidates) > 0 {
		out.Cursor = candidates[len(candidates)-1].ID
	}

	for _, m := range candidates {
		// The layout the object was written in has to be the layout this build reads and
		// writes, because a rewrite re-encodes from the decoded rows: decoding an older
		// object into a newer struct would silently drop whatever the two do not share,
		// and the rewrite would then be the only surviving copy. Refusing is the only
		// safe answer until there is a migration to run instead.
		if m.SchemaVersion != archive.SchemaVersion {
			return out, fmt.Errorf(
				"archive object %s was written at schema version %d and this build reads %d; "+
					"redacting it would rewrite it in a layout it was not written in",
				m.ObjectPath, m.SchemaVersion, archive.SchemaVersion)
		}

		body, err := p.Store.Get(ctx, m.ObjectPath)
		if errors.Is(err, blobstore.ErrNotFound) {
			// A durable manifest whose object is not there breaks the invariant the
			// writer's acknowledgement rests on (Appendix C.2). It is not this job's to
			// repair and it is emphatically not this job's to hide: nothing here can
			// erase what is not there, so it is counted, said loudly, and stepped over.
			out.Missing++
			log.Error("an archive object named by a durable manifest is missing",
				"object_path", m.ObjectPath, "bucket", m.Bucket, "project_uid", m.ProjectUID)
			continue
		}
		if err != nil {
			return out, fmt.Errorf("read archive object %s: %w", m.ObjectPath, err)
		}

		rows, err := archive.Decode(body)
		if err != nil {
			return out, fmt.Errorf("decode archive object %s: %w", m.ObjectPath, err)
		}

		kept := make([]archive.Row, 0, len(rows))
		for _, row := range rows {
			if !row.Names(req.ActorKey) {
				kept = append(kept, row)
			}
		}
		if len(kept) == len(rows) {
			// The person is not in this object. Nothing is written, nothing is deleted,
			// and nothing is counted — an object that was read is not an object that was
			// redacted.
			continue
		}

		if len(kept) == 0 {
			// Everything in it was theirs. There is nothing to write back.
			if err := p.Store.Delete(ctx, m.ObjectPath); err != nil {
				return out, fmt.Errorf("delete archive object %s: %w", m.ObjectPath, err)
			}
			if err := p.markRedacted(ctx, m); err != nil {
				return out, err
			}
			out.Removed++
			out.Objects++
			log.Info("removed an archive object that held only the erased subject",
				"request", req.UID, "object_path", m.ObjectPath, "rows", len(rows))
			continue
		}

		if err := p.rewrite(ctx, req, m, kept, len(rows)-len(kept), log); err != nil {
			return out, err
		}
		out.Rewritten++
		out.Objects++
	}

	return out, nil
}

// rewrite writes the object back without the erased rows and retires the original.
func (p *Purger) rewrite(
	ctx context.Context,
	req *regional.DataDeletionRequest,
	m *regional.ArchiveManifest,
	kept []archive.Row,
	dropped int,
	log *slog.Logger,
) error {
	body, err := archive.Encode(kept)
	if err != nil {
		return fmt.Errorf("re-encode %s: %w", m.ObjectPath, err)
	}
	path := redactedPath(m.ObjectPath, kept)

	// A digest collision with the object being replaced would mean writing over the source
	// before its replacement is recorded. It cannot happen — the content differs, so the
	// digest does — and the check costs nothing next to what it prevents.
	if path == m.ObjectPath {
		return fmt.Errorf("the redacted form of %s hashes to its own path", m.ObjectPath)
	}

	object, err := p.Store.Put(ctx, path, body)
	if err != nil {
		return fmt.Errorf("write the redacted form of %s: %w", m.ObjectPath, err)
	}

	first, last := span(kept)
	var inputBytes uint64
	for _, row := range kept {
		inputBytes += uint64(row.PayloadBytes)
	}

	// The replacement inherits `delete_after` unchanged. A redaction must not extend a
	// customer's retention, and computing a fresh one from today would do exactly that —
	// the object would outlive the events inside it (§59, §60).
	err = p.Cell.Client.ArchiveManifest.Create().
		SetBucket(m.Bucket).
		SetObjectPath(object.Path).
		SetRegion(m.Region).
		SetOrgUID(m.OrgUID).
		SetProjectUID(m.ProjectUID).
		SetEnvironmentUID(m.EnvironmentUID).
		SetMinEventTime(first).
		SetMaxEventTime(last).
		SetEventCount(uint64(len(kept))).
		SetObjectSizeBytes(object.Size).
		SetNormalizedInputBytes(inputBytes).
		SetSchemaVersion(m.SchemaVersion).
		SetDeleteAfter(m.DeleteAfter).
		SetStatus(archivemanifest.StatusDurable).
		OnConflictColumns(archivemanifest.FieldBucket, archivemanifest.FieldObjectPath).
		DoNothing().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("record the redacted form of %s: %w", m.ObjectPath, err)
	}

	// Only now is the original removed. Before this line a crash leaves two objects and one
	// extra manifest; after it, one of each. Neither state loses an event.
	if err := p.Store.Delete(ctx, m.ObjectPath); err != nil {
		return fmt.Errorf("delete the replaced object %s: %w", m.ObjectPath, err)
	}
	if err := p.markRedacted(ctx, m); err != nil {
		return err
	}

	log.Info("redacted an archive object",
		"request", req.UID,
		"was", m.ObjectPath,
		"now", object.Path,
		"rows_dropped", dropped,
		"rows_kept", len(kept))
	return nil
}

// markRedacted retires a manifest whose object an erasure removed.
//
// A state of its own rather than `deleted`, because the two are different facts about the
// same absence: §60 expired that one, an erasure replaced this one. §60's sweeper looks only
// at `durable` and `deleting`, so a redacted row is never picked back up, and §39 stops
// billing for bytes that are gone either way.
func (p *Purger) markRedacted(ctx context.Context, m *regional.ArchiveManifest) error {
	err := p.Cell.Client.ArchiveManifest.UpdateOne(m).
		SetStatus(archivemanifest.StatusRedacted).
		ClearFailureReason().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark %s redacted: %w", m.ObjectPath, err)
	}
	return nil
}

// redactedPath names the replacement from its contents, in the prefix the original sat in.
//
// The prefix is kept because it encodes the tenant, the day and the hour (§14.1), all of
// which are still true of the redacted object — and because §34's cold queries will prune on
// exactly that prefix.
//
// The name is a digest of the rows that remain, which is what makes a repeated redaction
// idempotent: the same surviving rows produce the same object at the same path, so a retry
// after a crash overwrites rather than accumulates. §22's reasoning, applied to a second
// producer of archive objects.
func redactedPath(original string, kept []archive.Row) string {
	prefix := original[:strings.LastIndex(original, "/")+1]
	return prefix + "part-" + redactedDigest(kept) + ".parquet"
}

// redactedDigest hashes the surviving event identifiers, in a fixed order.
//
// Sorted, because the row order after a filter is the row order of the source and that is
// not a property worth depending on. The count is mixed in for the same reason the writer's
// digest mixes in its chunk boundary: two different sets must not be able to hash alike
// through some accident of concatenation.
func redactedDigest(kept []archive.Row) string {
	ids := make([]string, 0, len(kept))
	for _, row := range kept {
		ids = append(ids, row.EventID)
	}
	slices.Sort(ids)

	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "|%d|redacted", len(ids))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// span is the time range the surviving rows cover.
//
// Recomputed rather than inherited. The manifest's bounds are what §104 scopes a breach
// from, and an object whose first event was the erased person's would otherwise keep
// claiming a range it no longer holds.
func span(rows []archive.Row) (first, last time.Time) {
	for i, row := range rows {
		t := row.EventTime.UTC()
		if i == 0 || t.Before(first) {
			first = t
		}
		if i == 0 || t.After(last) {
			last = t
		}
	}
	return first, last
}
