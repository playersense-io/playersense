// Package erasure carries a data-subject deletion through to completion (project.md §61,
// §101; docs/compliance/05).
//
// The schema for this existed long before the machinery did: `data_deletion_requests` sat
// there with a status machine and nothing to drive it, which is the shape of gap a
// compliance document is most dangerous around — a control that is described and not built
// is the one somebody relies on.
//
// # The shape of an erasure
//
// Analytics storage is immutable by design, so an erasure is not a DELETE. It is:
//
//	received → tombstoned → purging → completed
//
// The tombstone goes up first and takes effect immediately: from that moment the writer
// drops the actor's events and nothing further is accepted for them (§06). Removal follows
// over hours, because ClickHouse deletion rewrites whole parts and archive deletion rewrites
// whole objects, and because a synchronous erasure endpoint would be an availability problem
// and a denial-of-service vector at once (§05).
//
// # Why purging is a state and not a step
//
// `purging` persists across passes, and that is the point rather than an accident of the
// implementation. Delivery is at-least-once and a queue can hold messages for hours (§21),
// so events for an erased actor keep arriving after the purge has run. The tombstone drops
// them — unless Valkey was unavailable, in which case §06 is explicit that the batch is
// written and "the purge is the guarantee". A purge that ran once could not keep that
// promise, so it runs on every pass until the drain window has elapsed, and only then is the
// request completed. Completed therefore means what a studio would assume it means: not just
// "we deleted it" but "and nothing has arrived since".
//
// # What the order protects
//
// The steps run in an order chosen so that a crash between any two of them leaves a request
// that can simply be run again:
//
//  1. resolve the subject — which devices, over what span — from ClickHouse;
//  2. redact the archive, which is the slow, remote, failure-prone half;
//  3. delete the two device-keyed tables;
//  4. delete the events;
//  5. delete the purchases.
//
// Resolution reads `events_raw`, so the events have to go **last**: deleting them first
// would destroy the very mapping the aggregate deletions need, and a crash in between would
// leave a device-keyed row nothing could ever find again. The archive goes early for the
// opposite reason — it is the step most likely to fail, and failing it while ClickHouse is
// still intact means the retry has everything it needs.
//
// # What this cannot reach, stated rather than implied
//
// Two limits, both consequences of decisions made elsewhere, and both better said out loud
// than discovered by an auditor.
//
// A request identified only by a studio's own actor key resolves its devices through
// `events_raw`. Beyond the project's raw retention there are no events, so a device the
// person used and then abandoned months ago cannot be linked to them by anything PlayerSense
// holds — `player_lifetime` keeps a row keyed on an install that nothing connects to the
// name in the request. A request that names the install instead reaches it directly, which
// is why §05 asks studios for every identifier they have rather than one.
//
// The other is the shared device. `player_lifetime` and `player_activity_daily` are keyed on
// the device, not the actor (§29.2), so two people who signed in on one install share one
// row and it cannot be split — the events that would let it be rebuilt are the events being
// deleted, and past the raw window they are gone anyway. The row is deleted whole. The
// co-player loses their derived totals for that device and keeps every event; §61 already
// says of the cohort facts that "a deleted player simply stops being counted", and this is
// the same trade one table lower down.
package erasure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/datadeletionrequest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/subject"
	"playersense.dev/backend/internal/tombstone"
	"playersense.dev/backend/internal/valkey"
)

// Defaults for the knobs a deployment is unlikely to want to think about.
const (
	// DefaultDrainWindow is how long a request keeps re-purging before it is completed.
	//
	// Sized on the queue rather than on the law. §21 allows a message to sit for hours and
	// §06 makes the purge — not the tombstone — the guarantee that a late arrival is
	// removed, so the window has to outlast any plausible backlog. A day does, comfortably,
	// and it is still two orders of magnitude inside the month §05 is working to.
	DefaultDrainWindow = 24 * time.Hour

	// DefaultRetryAfter is how long a failed request waits before it is tried again.
	//
	// Failures here are things like an unreachable bucket, which fix themselves on a
	// timescale of minutes. Retrying every pass would turn one broken dependency into a
	// log nobody can read; never retrying would leave an erasure unfinished because of a
	// network blip.
	DefaultRetryAfter = time.Hour

	// DefaultMaxRequests bounds one pass, so a backlog is several bounded passes rather
	// than one unbounded one.
	DefaultMaxRequests = 50
)

// Pool supplies the ClickHouse handle.
//
// An interface, and resolved per run rather than held, for the reason scheduler.Pool gives:
// the application starts its lifecycle components after constructing them, so the pool does
// not exist at the moment this is built.
type Pool interface {
	DB() (*sql.DB, error)
}

// Purger drives every open deletion request one step further.
//
// It reads what it needs from the credential rather than looking a project up, which is
// what keeps the erasure path running while PostgreSQL is slow or unreachable (§64).
type Purger struct {
	DB    *pgdb.DB
	CH    Pool
	Store blobstore.Store
	Cache *valkey.Client
	Log   *slog.Logger

	// DrainWindow is how long a request keeps re-purging before it completes.
	DrainWindow time.Duration
	// RetryAfter is how long a failed request rests before the next attempt.
	RetryAfter time.Duration
	// TombstoneTTL is the lease on a raised tombstone. Zero derives one from DrainWindow.
	TombstoneTTL time.Duration
	// MaxRequests bounds one pass.
	MaxRequests int
	// MaxObjects bounds the archive scan within one request.
	MaxObjects int
}

// Result reports what one pass did.
type Result struct {
	Considered int
	Completed  int
	Failed     int

	Devices   int
	Objects   int
	Purchases int
	Exports   int
}

// ErrNoCache reports a purge attempted without Valkey, which cannot raise a tombstone.
//
// Fatal to the pass rather than survivable. Everything else in an erasure removes data, and
// removing data while the tombstone that stops more of it arriving is not up would mean
// deleting a player's history and then writing more of it — the one ordering that turns a
// completed erasure into a false statement.
var ErrNoCache = errors.New("erasure needs Valkey to raise a tombstone")

// Run advances every open request, as of now.
//
// One pass, and never more than one step's worth of surprise: a request that fails is
// recorded with its reason and left for the next pass, and a request that succeeds is either
// completed or held in `purging` until its drain window closes.
func (p *Purger) Run(ctx context.Context, now time.Time) (Result, error) {
	var out Result

	if !p.DB.Ready() {
		return out, pgdb.ErrNotStarted
	}
	if p.Cache == nil || !p.Cache.Ready() {
		return out, ErrNoCache
	}
	log := p.Log
	if log == nil {
		log = slog.Default()
	}
	drain := p.DrainWindow
	if drain <= 0 {
		drain = DefaultDrainWindow
	}
	retry := p.RetryAfter
	if retry <= 0 {
		retry = DefaultRetryAfter
	}
	limit := p.MaxRequests
	if limit <= 0 {
		limit = DefaultMaxRequests
	}

	open, err := p.DB.Client.DataDeletionRequest.Query().
		Where(
			datadeletionrequest.StatusNEQ(datadeletionrequest.StatusCompleted),
			// A failed request is retryable (§05, Appendix C.13) but not on every pass:
			// the failure is usually a dependency that will come back on its own, and
			// hammering it converts one incident into a log nobody reads.
			datadeletionrequest.Or(
				datadeletionrequest.StatusNEQ(datadeletionrequest.StatusFailed),
				datadeletionrequest.UpdatedAtLTE(now.Add(-retry)),
			),
		).
		// Oldest first: the request closest to its deadline is the one that should move.
		Order(ent.Asc(datadeletionrequest.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return out, fmt.Errorf("find open deletion requests: %w", err)
	}
	out.Considered = len(open)
	if out.Considered == 0 {
		return out, nil
	}

	db, err := p.CH.DB()
	if err != nil {
		return out, fmt.Errorf("clickhouse: %w", err)
	}

	for _, req := range open {
		done, err := p.advance(ctx, db, req, now, drain, log)
		out.Devices += done.Devices
		out.Objects += done.Objects
		out.Purchases += done.Purchases
		out.Exports += done.Exports
		if err != nil {
			// Recorded on the row rather than returned. One request failing must not stop
			// the others, and the reason belongs where somebody looking at the request
			// will find it (§05 step 4).
			out.Failed++
			log.Error("an erasure failed",
				"request", req.UID, "project_uid", req.ProjectUID, "error", err)
			if markErr := p.fail(ctx, req, err); markErr != nil {
				return out, markErr
			}
			continue
		}
		if done.Completed {
			out.Completed++
		}
	}
	return out, nil
}

// pass is what one request's turn produced.
type pass struct {
	Devices   int
	Objects   int
	Purchases int
	Exports   int
	Completed bool
}

// advance takes one request as far as it can go this pass.
func (p *Purger) advance(
	ctx context.Context,
	db *sql.DB,
	req *ent.DataDeletionRequest,
	now time.Time,
	drain time.Duration,
	log *slog.Logger,
) (pass, error) {
	var out pass

	// The tombstone, first and every time. Raising it is idempotent and renewing it is the
	// same call, which is what makes the TTL a lease rather than a lifetime: the worker
	// holding it up is the proof that something is still working on this request.
	if err := tombstone.Raise(ctx, p.Cache, req.ProjectUID.String(), req.ActorKey, p.tombstoneTTL(drain)); err != nil {
		return out, fmt.Errorf("raise the tombstone: %w", err)
	}
	if req.Status == datadeletionrequest.StatusReceived {
		update := p.DB.Client.DataDeletionRequest.UpdateOne(req).
			SetStatus(datadeletionrequest.StatusTombstoned)
		if req.TombstonedAt == nil {
			update = update.SetTombstonedAt(now)
		}
		saved, err := update.Save(ctx)
		if err != nil {
			return out, fmt.Errorf("record the tombstone: %w", err)
		}
		req = saved
		log.Info("tombstoned an actor",
			"request", req.UID, "project_uid", req.ProjectUID)
	}

	if req.Status != datadeletionrequest.StatusPurging {
		saved, err := p.DB.Client.DataDeletionRequest.UpdateOne(req).
			SetStatus(datadeletionrequest.StatusPurging).
			ClearFailureReason().
			Save(ctx)
		if err != nil {
			return out, fmt.Errorf("record the purge: %w", err)
		}
		req = saved
	}

	// The wide reading of the identifier: an erasure naming an install must reach the rows
	// produced after that install signed in (subject.Match).
	who, err := subject.Resolve(ctx, db, req.ProjectSeq, req.ActorKey, subject.ActorOrDevice)
	if err != nil {
		return out, err
	}

	// The archive first: the slow half, and the one whose failure should leave ClickHouse
	// intact so the retry can resolve the subject again.
	// Zero on a first purge — read everything that could hold the person — and the last
	// pass's cursor after that, so a request held open for its drain window does not
	// re-download every object in its span on every pass, which is the difference between a
	// background job and an outage.
	redacted, err := p.redactArchive(ctx, req, who, req.ArchiveCursor, log)
	out.Objects = redacted.Objects
	if err != nil {
		return out, err
	}
	if redacted.Truncated {
		log.Info("the archive redaction filled its bound and will continue next pass",
			"request", req.UID, "objects_this_pass", redacted.Objects)
	}

	// Then the device-keyed tables, then the events they were resolved from.
	if err := p.purgeDevices(ctx, db, req, who); err != nil {
		return out, err
	}
	out.Devices = len(who.Devices)

	// The account-level aggregate, before the events it was built from — same ordering
	// reason as the device tables: resolution reads events_raw, so it goes last.
	if err := p.purgeAccount(ctx, db, req); err != nil {
		return out, err
	}

	if err := p.purgeEvents(ctx, db, req, who); err != nil {
		return out, err
	}

	purchases, err := p.purgePurchases(ctx, req)
	if err != nil {
		return out, err
	}
	out.Purchases = purchases

	// Last, and it is not an afterthought: an export bundle is a ready-made copy of
	// everything this erasure just worked to remove.
	exports, err := p.purgeExports(ctx, req)
	if err != nil {
		return out, err
	}
	out.Exports = exports

	// Counters accumulate across passes: a late arrival removed on the third pass is part
	// of what this erasure did.
	update := p.DB.Client.DataDeletionRequest.UpdateOne(req).
		ClearFailureReason().
		SetArchiveCursor(max(req.ArchiveCursor, redacted.Cursor)).
		SetDevicesDeleted(req.DevicesDeleted + uint32(out.Devices)).
		SetObjectsRedacted(req.ObjectsRedacted + uint32(out.Objects)).
		SetPurchasesDeleted(req.PurchasesDeleted + uint32(out.Purchases))

	// Completed only once nothing more can arrive **and** the archive has been read to the
	// end. Until then the request stays in `purging` and comes back next pass — which is
	// the whole reason the drain window exists, and the reason `completed` is worth
	// believing.
	if !redacted.Truncated && !now.Before(req.CreatedAt.Add(drain)) {
		update = update.
			SetStatus(datadeletionrequest.StatusCompleted).
			SetCompletedAt(now)
		out.Completed = true
	}
	if _, err := update.Save(ctx); err != nil {
		return out, fmt.Errorf("record the outcome: %w", err)
	}

	if out.Completed {
		log.Info("an erasure completed",
			"request", req.UID,
			"project_uid", req.ProjectUID,
			"devices", req.DevicesDeleted+uint32(out.Devices),
			"objects_redacted", req.ObjectsRedacted+uint32(out.Objects),
			"purchases", req.PurchasesDeleted+uint32(out.Purchases),
			"exports_removed", out.Exports,
			"took", now.Sub(req.CreatedAt).String())
	}
	return out, nil
}

// tombstoneTTL is how long a raised tombstone survives without a refresh.
//
// Derived from the drain window rather than configured separately, because the two are the
// same quantity seen from different ends: the window is how long late events are expected,
// and the tombstone is what drops them while they are. A margin on top, so a delayed pass
// does not let the lease lapse between refreshes.
func (p *Purger) tombstoneTTL(drain time.Duration) time.Duration {
	if p.TombstoneTTL > 0 {
		return p.TombstoneTTL
	}
	return drain + 2*time.Hour
}

// fail records why a request could not be finished this pass.
func (p *Purger) fail(ctx context.Context, req *ent.DataDeletionRequest, cause error) error {
	err := p.DB.Client.DataDeletionRequest.UpdateOne(req).
		SetStatus(datadeletionrequest.StatusFailed).
		SetFailureReason(pgdb.Reason(cause.Error())).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("record the failure of %s: %w", req.UID, err)
	}
	return nil
}
