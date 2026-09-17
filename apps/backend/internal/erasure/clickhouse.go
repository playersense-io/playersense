package erasure

import (
	"context"
	"database/sql"
	"fmt"

	"playersense.dev/backend/ent/regional"
	"playersense.dev/backend/ent/regional/dataexportrequest"
	"playersense.dev/backend/ent/regional/verifiedpurchase"
	"playersense.dev/backend/internal/subject"
)

// purgeDevices removes the two device-keyed tables' rows (§61).
//
// By identifier alone, with no predicate on the actor, because neither table has one: both
// are keyed on the device (§29.2), and the decision about which devices belong to this
// person was made once, in subject.Resolve, against the only sources that can answer it.
//
// `player_activity_daily` goes before `player_lifetime`, and the order is not arbitrary. The
// first holds the numerators of every cohort rate and the second holds the denominators
// (§29.9); a crash between them leaves a device counted in a denominator and absent from the
// numerators, which reads as retention falling. The other order leaves it counted in the
// numerators and missing from the denominator, which reads as retention above 100% — the
// failure `cohortcheck`'s check 7 exists to catch, and the one that looks like a product bug
// rather than an interrupted job. Both are repaired by the next derivation; one of them is
// far easier to recognise while it lasts.
func (p *Purger) purgeDevices(ctx context.Context, db *sql.DB, req *regional.DataDeletionRequest, s subject.Subject) error {
	if len(s.Devices) == 0 {
		return nil
	}
	for _, table := range []string{"player_activity_daily", "player_lifetime"} {
		for start := 0; start < len(s.Devices); start += subject.ChunkSize {
			end := min(start+subject.ChunkSize, len(s.Devices))
			batch := s.Devices[start:end]
			if _, err := db.ExecContext(ctx, `
				ALTER TABLE `+table+`
				DELETE WHERE project_seq = ?
				  AND device_id IN (`+subject.Placeholders(len(batch))+`)
				SETTINGS mutations_sync = 2`,
				subject.DeviceArgs(req.ProjectSeq, batch)...); err != nil {
				return fmt.Errorf("purge %d devices from %s: %w", len(batch), table, err)
			}
		}
	}
	return nil
}

// purgeEvents removes the raw events themselves.
//
// Last of the ClickHouse work, because resolution reads this table: deleting it first would
// destroy the mapping the device deletions depend on, and a crash in between would leave
// device-keyed rows that nothing could ever find again.
//
// The predicate is subject.ActorOrDevice — the wide reading — and the archive is filtered by
// the same rule, so the hot copy and the cold copy cannot end up disagreeing about which
// rows were the person's.
//
// `mutations_sync = 2` waits for the rewrite. That blocks the pass, which is acceptable here
// in a way it would not be on a request path: §05 budgets this work in hours and puts a
// tombstone in front of it precisely so that nothing is waiting on it.
func (p *Purger) purgeEvents(ctx context.Context, db *sql.DB, req *regional.DataDeletionRequest, s subject.Subject) error {
	if !s.HotRows {
		return nil
	}
	where, args := subject.ActorOrDevice.Predicate(req.ProjectSeq, req.ActorKey)
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE events_raw
		DELETE WHERE `+where+`
		SETTINGS mutations_sync = 2`, args...); err != nil {
		return fmt.Errorf("purge events: %w", err)
	}
	return nil
}

// purgePurchases removes the cell tier's record of what this person bought (§05, §48).
//
// Matched on `player_id` rather than on an actor key, because that is the column §47 writes:
// a purchase is verified server-to-server against an account, so an install that never
// signed in has none. The `(project_uid, player_id)` index makes this a lookup rather than a
// scan, which §05 promises of the cell tier.
//
// The invoice is not touched. §06 keeps billing records, which are aggregate counts in the
// global tier and name no player — a receipt for one person's purchase is personal data, and
// a count of a studio's monthly players is not.
func (p *Purger) purgePurchases(ctx context.Context, req *regional.DataDeletionRequest) (int, error) {
	n, err := p.Cell.Client.VerifiedPurchase.Delete().
		Where(
			verifiedpurchase.ProjectUID(req.ProjectUID),
			verifiedpurchase.PlayerID(req.ActorKey),
		).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("purge verified purchases: %w", err)
	}
	return n, nil
}

// purgeExports removes any export bundle held for this person (§101).
//
// An export is a second copy of everything held about someone, assembled on request and
// parked in the cell's bucket until it expires. An erasure that deleted the events and left
// the bundle would have deleted the harder copy and kept the easier one.
//
// The request rows survive as audit records — that an export was produced is a fact about
// processing, like the deletion request itself (§06) — but the objects go, and the row's
// status says so.
func (p *Purger) purgeExports(ctx context.Context, req *regional.DataDeletionRequest) (int, error) {
	bundles, err := p.Cell.Client.DataExportRequest.Query().
		Where(
			dataexportrequest.ProjectUID(req.ProjectUID),
			dataexportrequest.ActorKey(req.ActorKey),
			dataexportrequest.StatusNEQ(dataexportrequest.StatusExpired),
			dataexportrequest.ObjectPathNEQ(""),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("find this subject's exports: %w", err)
	}

	removed := 0
	for _, bundle := range bundles {
		// Only this process's own bucket. A bundle recorded against another one belongs
		// to a store this process is not holding, and its path means nothing here.
		if bundle.Bucket != p.Store.Bucket() {
			continue
		}
		if err := p.Store.Delete(ctx, bundle.ObjectPath); err != nil {
			return removed, fmt.Errorf("delete export bundle %s: %w", bundle.ObjectPath, err)
		}
		if err := p.Cell.Client.DataExportRequest.UpdateOne(bundle).
			SetStatus(dataexportrequest.StatusExpired).
			SetFailureReason("removed by the erasure of this actor").
			Exec(ctx); err != nil {
			return removed, fmt.Errorf("record the removal of export %s: %w", bundle.UID, err)
		}
		removed++
	}
	return removed, nil
}
