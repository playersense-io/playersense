package handlers

import (
	"context"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/archivemanifest"
	"playersense.dev/backend/ent/datadeletionrequest"
	"playersense.dev/backend/ent/dataexportrequest"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/ops"
	"playersense.dev/backend/internal/schemas"
)

// The worker's jobs, for the operations console's retention jobs page (§56, §79).

// erasureDeadline is how long an erasure may take: the month GDPR Art. 12(3) gives a controller to
// act on a request, which a studio can only meet if its processor meets it first.
const erasureDeadline = 30 * 24 * time.Hour

// OperatorJobs reads how the worker's jobs have been running and what is waiting on them: each
// job's passes from job_runs, and the erasures, exports and archive objects in PostgreSQL. Every
// failure is listed, not the first.
func OperatorJobs(ctx *muzak.Context, in schemas.JobsParams) (schemas.JobsOut, error) {
	console := muzak.From[*ops.Console](ctx)
	db := muzak.From[*core.DB](ctx)
	from, to, err := operatorRange(in.From, in.To, 7)
	if err != nil {
		return schemas.JobsOut{}, err
	}
	out := schemas.JobsOut{From: from, To: to}
	out.Jobs, out.Recent, err = console.Jobs(ctx.Context(), from, to, in.Job, in.Failed)
	if err != nil {
		return schemas.JobsOut{}, muzak.ServiceUnavailable("the job history could not be read").Wrap(err)
	}

	now := nowUTC()
	if out.Erasures, err = erasureQueue(ctx.Context(), db, now); err != nil {
		return schemas.JobsOut{}, core.AsHTTPError(err)
	}
	if out.Exports, err = exportQueue(ctx.Context(), db); err != nil {
		return schemas.JobsOut{}, core.AsHTTPError(err)
	}
	if out.Archive, err = archiveQueue(ctx.Context(), db, now); err != nil {
		return schemas.JobsOut{}, core.AsHTTPError(err)
	}

	// Project slugs, for a person reading the list; the ids are what they would search for.
	seqs := map[int]bool{}
	for _, r := range out.Erasures.Open {
		seqs[int(r.ProjectSeq)] = true
	}
	for _, r := range out.Exports.Open {
		seqs[int(r.ProjectSeq)] = true
	}
	list := make([]int, 0, len(seqs))
	for seq := range seqs {
		list = append(list, seq)
	}
	_, slugs := names(ctx, nil, list)
	for i := range out.Erasures.Open {
		out.Erasures.Open[i].Project = slugs[int(out.Erasures.Open[i].ProjectSeq)]
	}
	for i := range out.Exports.Open {
		out.Exports.Open[i].Project = slugs[int(out.Exports.Open[i].ProjectSeq)]
	}
	return out, nil
}

func erasureQueue(ctx context.Context, db *core.DB, now time.Time) (schemas.ErasureQueueOut, error) {
	out := schemas.ErasureQueueOut{ByStatus: map[string]int{}, Open: []schemas.ErasureRequestOut{}}
	var counts []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := db.Client.DataDeletionRequest.Query().GroupBy(datadeletionrequest.FieldStatus).
		Aggregate(ent.Count()).Scan(ctx, &counts); err != nil {
		return out, err
	}
	for _, c := range counts {
		out.ByStatus[c.Status] = c.Count
	}
	open := datadeletionrequest.StatusNEQ(datadeletionrequest.StatusCompleted)
	overdue, err := db.Client.DataDeletionRequest.Query().
		Where(open, datadeletionrequest.CreatedAtLT(now.Add(-erasureDeadline))).Count(ctx)
	if err != nil {
		return out, err
	}
	out.Overdue = overdue
	rows, err := db.Client.DataDeletionRequest.Query().Where(open).
		Order(ent.Asc(datadeletionrequest.FieldCreatedAt)).Limit(100).All(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Open = append(out.Open, schemas.ErasureRequestOut{
			ID: r.UID, ProjectUID: r.ProjectUID, ProjectSeq: r.ProjectSeq, Status: string(r.Status),
			ReceivedAt: r.CreatedAt, DueAt: r.CreatedAt.Add(erasureDeadline), TombstonedAt: r.TombstonedAt,
			UpdatedAt: r.UpdatedAt, FailureReason: r.FailureReason, DevicesDeleted: r.DevicesDeleted,
			ObjectsRedacted: r.ObjectsRedacted,
		})
	}
	return out, nil
}

func exportQueue(ctx context.Context, db *core.DB) (schemas.ExportQueueOut, error) {
	out := schemas.ExportQueueOut{ByStatus: map[string]int{}, Open: []schemas.ExportRequestOut{}}
	var counts []struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := db.Client.DataExportRequest.Query().GroupBy(dataexportrequest.FieldStatus).
		Aggregate(ent.Count()).Scan(ctx, &counts); err != nil {
		return out, err
	}
	for _, c := range counts {
		out.ByStatus[c.Status] = c.Count
	}
	rows, err := db.Client.DataExportRequest.Query().
		Where(dataexportrequest.StatusIn(dataexportrequest.StatusReceived, dataexportrequest.StatusRunning,
			dataexportrequest.StatusFailed)).
		Order(ent.Asc(dataexportrequest.FieldCreatedAt)).Limit(100).All(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Open = append(out.Open, schemas.ExportRequestOut{
			ID: r.UID, ProjectUID: r.ProjectUID, ProjectSeq: r.ProjectSeq, Status: string(r.Status),
			ReceivedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, FailureReason: r.FailureReason,
		})
	}
	return out, nil
}

func archiveQueue(ctx context.Context, db *core.DB, now time.Time) (schemas.ArchiveQueueOut, error) {
	out := schemas.ArchiveQueueOut{Failures: []schemas.ArchiveFailureOut{}}
	var err error
	if out.Due, err = db.Client.ArchiveManifest.Query().Where(
		archivemanifest.StatusEQ(archivemanifest.StatusDurable), archivemanifest.DeleteAfterLTE(now)).
		Count(ctx); err != nil {
		return out, err
	}
	if out.Deleting, err = db.Client.ArchiveManifest.Query().
		Where(archivemanifest.StatusEQ(archivemanifest.StatusDeleting)).Count(ctx); err != nil {
		return out, err
	}
	failed := archivemanifest.StatusEQ(archivemanifest.StatusFailed)
	if out.Failed, err = db.Client.ArchiveManifest.Query().Where(failed).Count(ctx); err != nil {
		return out, err
	}
	rows, err := db.Client.ArchiveManifest.Query().Where(failed).
		Order(ent.Desc(archivemanifest.FieldUpdatedAt)).Limit(100).All(ctx)
	if err != nil {
		return out, err
	}
	for _, m := range rows {
		out.Failures = append(out.Failures, schemas.ArchiveFailureOut{
			ID: m.UID, ProjectUID: m.ProjectUID, Bucket: m.Bucket, ObjectPath: m.ObjectPath,
			DeleteAfter: m.DeleteAfter, SizeBytes: m.ObjectSizeBytes, FailureReason: m.FailureReason,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return out, nil
}

// RetryArchiveManifest hands a failed archive object back to the sweep, for after its cause is
// fixed: a permission on the path, a hold released. The next pass tries it again, and fails it
// again with the reason if the cause is still there. An object the sweep refused because its dates
// cannot be right is refused again, by the same rail.
func RetryArchiveManifest(ctx *muzak.Context, in schemas.RetryManifestIn) (schemas.ArchiveFailureOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[core.StaffAccessRecord](ctx).Project

	n, err := db.Client.ArchiveManifest.Update().
		Where(archivemanifest.UID(in.ManifestID), archivemanifest.ProjectUID(proj.UID),
			archivemanifest.StatusEQ(archivemanifest.StatusFailed)).
		SetStatus(archivemanifest.StatusDurable).
		SetFailureReason("").
		Save(ctx.Context())
	if err != nil {
		return schemas.ArchiveFailureOut{}, core.AsHTTPError(err)
	}
	m, err := db.Client.ArchiveManifest.Query().
		Where(archivemanifest.UID(in.ManifestID), archivemanifest.ProjectUID(proj.UID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.ArchiveFailureOut{}, muzak.NotFound("this project has no archive object with that id")
		}
		return schemas.ArchiveFailureOut{}, core.AsHTTPError(err)
	}
	if n == 0 {
		return schemas.ArchiveFailureOut{}, muzak.Conflict("the object is " + string(m.Status) + ", not failed")
	}
	return schemas.ArchiveFailureOut{
		ID: m.UID, ProjectUID: m.ProjectUID, Bucket: m.Bucket, ObjectPath: m.ObjectPath,
		DeleteAfter: m.DeleteAfter, SizeBytes: m.ObjectSizeBytes, UpdatedAt: m.UpdatedAt,
	}, nil
}
