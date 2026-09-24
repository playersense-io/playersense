// Package projects carries out what a project's settings ask for over time: a lowered raw
// retention enforced on rows already written, and a scheduled deletion (project.md §24, §75).
//
// Both run in the worker's janitor, a pass every quarter of an hour, and both are idempotent: a
// pass that stops half way is finished by the next.
package projects

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// Pool supplies the ClickHouse handle, resolved per pass.
type Pool interface {
	DB() (*sql.DB, error)
}

// rawTables are the ClickHouse tables that carry each row's retention tier: what raw retention
// means. The rollups keep their own terms.
var rawTables = []struct{ table, at string }{
	{"events_raw", "event_time"},
	{"sessions", "activity_date"},
	{"purchases", "purchase_date"},
}

// keyCacheSettle is how long after a retention change the enforcement waits, so the key cache
// has turned over and no new row is stamped with the old tier.
const keyCacheSettle = 15 * time.Minute

// Retention enforces a lowered tier on rows written before it.
type Retention struct {
	DB  *pgdb.DB
	CH  Pool
	Log *slog.Logger
}

// RetentionResult is what one pass did.
type RetentionResult struct {
	Projects int
}

// Run deletes, for each project whose tier was lowered and whose enforcement is due, the raw rows
// older than the new tier. Due is once a day until retention_purge_until, the moment no row
// stamped with the old tier can remain.
func (r *Retention) Run(ctx context.Context, now time.Time) (RetentionResult, error) {
	var out RetentionResult
	due, err := r.DB.Client.Project.Query().Where(
		project.DeletedAtIsNil(),
		project.RetentionPurgeUntilGT(now),
		project.RetentionChangedAtLT(now.Add(-keyCacheSettle)),
		project.Or(project.RetentionPurgedAtIsNil(), project.RetentionPurgedAtLT(now.Add(-23*time.Hour))),
	).All(ctx)
	if err != nil || len(due) == 0 {
		return out, err
	}
	db, err := r.CH.DB()
	if err != nil {
		return out, err
	}
	for _, p := range due {
		cutoff := now.AddDate(0, 0, -int(p.RetentionDays))
		for _, t := range rawTables {
			if _, err := db.ExecContext(ctx, `
				ALTER TABLE `+t.table+`
				DELETE WHERE project_seq = ? AND `+t.at+` < ?
				SETTINGS mutations_sync = 2`, uint32(p.ID), cutoff); err != nil {
				return out, fmt.Errorf("enforce %d days on %s for project %s: %w", p.RetentionDays, t.table, p.UID, err)
			}
		}
		if err := r.DB.Client.Project.UpdateOne(p).SetRetentionPurgedAt(now).Exec(ctx); err != nil {
			return out, err
		}
		out.Projects++
		r.log().Info("enforced a lowered retention", "project", p.UID, "days", p.RetentionDays)
	}
	return out, nil
}

func (r *Retention) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// purgeDelay is how long after a project's keys are revoked its data goes: long enough for the
// batches already queued to land, so they go with the rest instead of outliving it.
const purgeDelay = time.Hour

// Deletion carries out a scheduled deletion in two steps. When it is due, the project's keys are
// revoked, so nothing more is accepted or served, and it disappears from the dashboard. An hour
// later its analytics are deleted from every ClickHouse table and its archive objects handed to
// the expiry sweep. The row stays, because the organization's usage and invoices name it.
type Deletion struct {
	DB    *pgdb.DB
	CH    Pool
	Cache *valkey.Client
	Log   *slog.Logger
}

// DeletionResult is what one pass did.
type DeletionResult struct {
	Closed int // keys revoked, hidden
	Purged int // data deleted
}

// Run takes every due project one step further.
func (d *Deletion) Run(ctx context.Context, now time.Time) (DeletionResult, error) {
	var out DeletionResult

	due, err := d.DB.Client.Project.Query().
		Where(project.DeletionAtLTE(now), project.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range due {
		if err := d.close(ctx, p, now); err != nil {
			return out, err
		}
		out.Closed++
	}

	purge, err := d.DB.Client.Project.Query().
		Where(project.DeletedAtLTE(now.Add(-purgeDelay)), project.DeletionPurgedAtIsNil()).All(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range purge {
		if err := d.purge(ctx, p, now); err != nil {
			return out, err
		}
		out.Purged++
	}
	return out, nil
}

// close revokes every key the project has and hides it.
func (d *Deletion) close(ctx context.Context, p *ent.Project, now time.Time) error {
	keys, err := d.DB.Client.APIKey.Query().
		Where(apikey.HasEnvironmentWith(environment.ProjectID(p.ID)), apikey.RevokedAtIsNil()).All(ctx)
	if err != nil {
		return err
	}
	tx, err := d.DB.Client.Tx(ctx)
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		ids := make([]int, len(keys))
		for i, k := range keys {
			ids[i] = k.ID
		}
		if err := tx.APIKey.Update().Where(apikey.IDIn(ids...)).SetRevokedAt(now).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Project.UpdateOne(p).SetDeletedAt(now).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The cached keys stop at once rather than when their entries expire.
	if d.Cache != nil && d.Cache.Ready() {
		for _, k := range keys {
			if err := d.Cache.Set(ctx, core.RevokedKey(d.Cache, k.PublicID), []byte("revoked"),
				core.RevocationTombstoneTTL); err != nil {
				d.log().Warn("could not tombstone a deleted project's key; it stops when its entry expires",
					"project", p.UID, "error", err)
			}
		}
	}
	d.log().Info("closed a deleted project", "project", p.UID, "keys_revoked", len(keys))
	return nil
}

// purge deletes the project's analytics and hands its archive to the expiry sweep.
func (d *Deletion) purge(ctx context.Context, p *ent.Project, now time.Time) error {
	db, err := d.CH.DB()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT c.table
		FROM system.columns AS c
		WHERE c.database = currentDatabase() AND c.name = 'project_seq'
		  AND c.table IN (SELECT name FROM system.tables WHERE database = currentDatabase() AND engine NOT LIKE '%View%')`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, table)
	}
	rows.Close()
	for _, table := range tables {
		if _, err := db.ExecContext(ctx, "ALTER TABLE `"+table+"` DELETE WHERE project_seq = ? SETTINGS mutations_sync = 2",
			uint32(p.ID)); err != nil {
			return fmt.Errorf("delete project %s from %s: %w", p.UID, table, err)
		}
	}

	// The sweep refuses an object whose delete_after is not after its newest event, so each is
	// due a second after that or now, whichever is later.
	manifests, err := d.DB.Client.ArchiveManifest.Query().Where(
		archivemanifest.ProjectUID(p.UID), archivemanifest.StatusEQ(archivemanifest.StatusDurable),
		archivemanifest.DeleteAfterGT(now)).All(ctx)
	if err != nil {
		return err
	}
	for _, m := range manifests {
		due := now
		if !due.After(m.MaxEventTime) {
			due = m.MaxEventTime.Add(time.Second)
		}
		if err := d.DB.Client.ArchiveManifest.UpdateOne(m).SetDeleteAfter(due).Exec(ctx); err != nil {
			return err
		}
	}
	if err := d.DB.Client.Project.UpdateOne(p).SetDeletionPurgedAt(now).Exec(ctx); err != nil {
		return err
	}
	d.log().Info("deleted a project's data", "project", p.UID, "tables", len(tables), "archive_objects", len(manifests))
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
