package erasure

import (
	"context"
	"database/sql"
	"fmt"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/dataexportrequest"
	"playersense.dev/backend/ent/purchaseattempt"
	"playersense.dev/backend/ent/verifiedpurchase"
	"playersense.dev/backend/internal/subject"
)

// purgeDevices removes the device-keyed tables' rows (§61).
//
// By identifier alone, with no predicate on the actor, because none of them has one: all
// are keyed on the device (§29.2), and the decision about which devices belong to this
// person was made once, in subject.Resolve, against the only sources that can answer it.
//
// The session, purchase and flag-assignment facts go first. They hold the device, and the first
// two the studio's player id, so they are personal data, and none of them feeds a cohort rate, so
// where they fall in the order does not matter.
//
// `player_activity_daily` goes before `player_lifetime`, and the order is not arbitrary. The
// first holds the numerators of every cohort rate and the second holds the denominators
// (§29.9); a crash between them leaves a device counted in a denominator and absent from the
// numerators, which reads as retention falling. The other order leaves it counted in the
// numerators and missing from the denominator, which reads as retention above 100% — the
// failure `cohortcheck`'s check 7 exists to catch, and the one that looks like a product bug
// rather than an interrupted job. Both are repaired by the next derivation; one of them is
// far easier to recognise while it lasts.
// deviceTables are the ClickHouse tables keyed on the device, in the order they are purged.
var deviceTables = []string{
	"sessions", "purchases", "payers", "flag_assignments",
	"player_activity_daily", "player_lifetime",
}

func (p *Purger) purgeDevices(ctx context.Context, db *sql.DB, req *ent.DataDeletionRequest, s subject.Subject) error {
	if len(s.Devices) == 0 {
		return nil
	}
	for _, table := range deviceTables {
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

// purgeAccount removes the account-level aggregate (§61).
//
// `account_lifetime` holds `account_key`, which is the studio's own player identifier, so it
// is personal data and it is in scope. A per-player table outside the erasure path is a
// person who was told they were deleted and was not — which is the whole failure §61 exists
// to prevent, and the kind that surfaces in an audit rather than in a log.
//
// Matched on `account_key` rather than on the surrogate. The surrogate is
// `sipHash64(project_seq, environment_seq, player_id)` computed by ClickHouse, and
// reproducing that hash in Go would be a second implementation of a key that must agree
// exactly or delete the wrong person's row. The string predicate does not use the sort key,
// so it scans — acceptable for work §05 budgets in hours and puts a tombstone in front of.
//
// An erasure naming an install rather than a player matches nothing here, correctly: an
// account table has no row for somebody who never signed in.
func (p *Purger) purgeAccount(ctx context.Context, db *sql.DB, req *ent.DataDeletionRequest) error {
	if req.ActorKey == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE account_lifetime
		DELETE WHERE project_seq = ? AND account_key = ?
		SETTINGS mutations_sync = 2`, req.ProjectSeq, req.ActorKey); err != nil {
		return fmt.Errorf("purge account: %w", err)
	}
	// A purchase verified for a player alone sits on a stand-in device that no event names, so
	// the device purge cannot reach it; its rows carry the player id, and are matched on that.
	for _, table := range []string{"purchases", "payers"} {
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE `+table+`
			DELETE WHERE project_seq = ? AND player_id = ?
			SETTINGS mutations_sync = 2`, req.ProjectSeq, req.ActorKey); err != nil {
			return fmt.Errorf("purge %s by player: %w", table, err)
		}
	}
	return nil
}

// purgeLogs removes the person's Cloud Logging records (ADR 0050): every record naming their
// player id, and every record from one of their installs. A log line is free text the game wrote,
// so a record from their device is theirs whatever it says.
//
// Matched on the install as well as on the device surrogate the other tables use, because the
// logs table has no device_id column: the surrogate is computed here exactly as events_raw
// materializes it, sipHash64 of project, environment and install.
func (p *Purger) purgeLogs(ctx context.Context, db *sql.DB, req *ent.DataDeletionRequest, s subject.Subject) error {
	if req.ActorKey == "" && len(s.Devices) == 0 {
		return nil
	}
	where := "project_seq = ? AND (ifNull(player_id, '') = ? OR toString(install_id) = ?"
	args := []any{req.ProjectSeq, req.ActorKey, req.ActorKey}
	for start := 0; start < len(s.Devices) || start == 0; start += subject.ChunkSize {
		clause, chunkArgs := where+")", args
		if len(s.Devices) > 0 {
			end := min(start+subject.ChunkSize, len(s.Devices))
			batch := s.Devices[start:end]
			clause = where + " OR sipHash64(project_seq, environment_seq, toString(install_id)) IN (" +
				subject.Placeholders(len(batch)) + "))"
			for _, device := range batch {
				chunkArgs = append(chunkArgs, device)
			}
		}
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE logs
			DELETE WHERE `+clause+`
			SETTINGS mutations_sync = 2`, chunkArgs...); err != nil {
			return fmt.Errorf("purge logs: %w", err)
		}
		if len(s.Devices) == 0 {
			break
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
func (p *Purger) purgeEvents(ctx context.Context, db *sql.DB, req *ent.DataDeletionRequest, s subject.Subject) error {
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

// purgePurchases removes the record of what this person bought (§05, §48).
//
// Matched on `player_id` rather than on an actor key, because that is the column §47 writes:
// a purchase is verified server-to-server against an account, so an install that never
// signed in has none. The `(project_uid, player_id)` index makes this a lookup rather than a
// scan, which §05 promises.
//
// The invoice is not touched. §06 keeps billing records, which are aggregate counts in the
// global tier and name no player — a receipt for one person's purchase is personal data, and
// a count of a studio's monthly players is not.
func (p *Purger) purgePurchases(ctx context.Context, req *ent.DataDeletionRequest) (int, error) {
	n, err := p.DB.Client.VerifiedPurchase.Delete().
		Where(
			verifiedpurchase.ProjectUID(req.ProjectUID),
			verifiedpurchase.Or(verifiedpurchase.PlayerID(req.ActorKey), verifiedpurchase.InstallID(req.ActorKey)),
		).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("purge verified purchases: %w", err)
	}
	// The verification log names them too: every receipt they sent, for thirty days.
	if _, err := p.DB.Client.PurchaseAttempt.Delete().
		Where(
			purchaseattempt.ProjectID(int(req.ProjectSeq)),
			purchaseattempt.Or(purchaseattempt.PlayerID(req.ActorKey), purchaseattempt.InstallID(req.ActorKey)),
		).
		Exec(ctx); err != nil {
		return 0, fmt.Errorf("purge purchase attempts: %w", err)
	}
	return n, nil
}

// purgeExports removes any export bundle held for this person (§101).
//
// An export is a second copy of everything held about someone, assembled on request and
// parked in the archive bucket until it expires. An erasure that deleted the events and left
// the bundle would have deleted the harder copy and kept the easier one.
//
// The request rows survive as audit records — that an export was produced is a fact about
// processing, like the deletion request itself (§06) — but the objects go, and the row's
// status says so.
func (p *Purger) purgeExports(ctx context.Context, req *ent.DataDeletionRequest) (int, error) {
	bundles, err := p.DB.Client.DataExportRequest.Query().
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
		if err := p.DB.Client.DataExportRequest.UpdateOne(bundle).
			SetStatus(dataexportrequest.StatusExpired).
			SetFailureReason("removed by the erasure of this actor").
			Exec(ctx); err != nil {
			return removed, fmt.Errorf("record the removal of export %s: %w", bundle.UID, err)
		}
		removed++
	}
	return removed, nil
}
