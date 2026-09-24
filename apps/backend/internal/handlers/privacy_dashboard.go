package handlers

import (
	"cmp"
	"net/http"
	"slices"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/datadeletionrequest"
	"playersense.dev/backend/ent/dataexportrequest"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/schemas"
)

// Data-subject requests from the dashboard (§61, §101): the same requests a server key makes,
// asked by a signed-in member with a reason, and listed for the project.

// ListPrivacyRequests is a project's erasures and exports, newest first.
func ListPrivacyRequests(ctx *muzak.Context, in schemas.PrivacyListParams) (schemas.PrivacyListOut, error) {
	db := muzak.From[*pgdb.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	out := schemas.PrivacyListOut{Requests: []schemas.PrivacyRequestOut{}}
	if !db.Ready() {
		return out, muzak.ServiceUnavailable("the database is unavailable")
	}
	base := dashboardBase(ctx)

	if in.Kind != "export" {
		q := db.Client.DataDeletionRequest.Query().Where(datadeletionrequest.ProjectUID(proj.UID))
		if in.Player != "" {
			q = q.Where(datadeletionrequest.ActorKey(in.Player))
		}
		rows, err := q.Order(ent.Desc(datadeletionrequest.FieldID)).Limit(in.Limit).All(ctx.Context())
		if err != nil {
			return out, core.AsHTTPError(err)
		}
		for _, r := range rows {
			out.Requests = append(out.Requests, schemas.PrivacyRequestOut{
				ID: r.UID, Kind: "erase", PlayerID: r.ActorKey, Status: string(r.Status),
				RequestedBy: r.RequestedBy, Reference: r.Reference, RequestedAt: r.CreatedAt.UTC(),
				CompletedAt: utc(r.CompletedAt), Failure: r.FailureReason,
			})
		}
	}
	if in.Kind != "erase" {
		q := db.Client.DataExportRequest.Query().Where(dataexportrequest.ProjectUID(proj.UID))
		if in.Player != "" {
			q = q.Where(dataexportrequest.ActorKey(in.Player))
		}
		rows, err := q.Order(ent.Desc(dataexportrequest.FieldID)).Limit(in.Limit).All(ctx.Context())
		if err != nil {
			return out, core.AsHTTPError(err)
		}
		for _, r := range rows {
			one := schemas.PrivacyRequestOut{
				ID: r.UID, Kind: "export", PlayerID: r.ActorKey, Status: string(r.Status),
				RequestedBy: r.RequestedBy, Reference: r.Reference, RequestedAt: r.CreatedAt.UTC(),
				CompletedAt: utc(r.CompletedAt), ExpiresAt: utc(r.ExpiresAt), Failure: r.FailureReason,
			}
			if r.Status == dataexportrequest.StatusCompleted {
				one.Download = base + "/privacy/exports/" + r.UID.String() + "/download"
			}
			out.Requests = append(out.Requests, one)
		}
	}
	slices.SortFunc(out.Requests, func(a, b schemas.PrivacyRequestOut) int {
		return cmp.Compare(b.RequestedAt.UnixNano(), a.RequestedAt.UnixNano())
	})
	if len(out.Requests) > in.Limit {
		out.Requests = out.Requests[:in.Limit]
	}
	return out, nil
}

func dashboardBase(ctx *muzak.Context) string {
	principal := muzak.From[*core.Principal](ctx)
	return "/v1/organizations/" + principal.Organization.Slug + "/projects/" + principal.Project.Slug
}

// RequestErasure asks, from the dashboard, for one player's data to be erased.
func RequestErasure(ctx *muzak.Context, in schemas.DashboardPrivacyIn) (schemas.ErasureOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	req, err := submitErasure(ctx, proj.UID, uint32(proj.ID), in.PlayerID, actorOf(ctx).Label, in.Reference)
	if err != nil {
		return schemas.ErasureOut{}, err
	}
	// Recorded in the organization's log without the player: the log is read by everyone with
	// audit.read, and who asked for a person's erasure is not whose.
	if err := logPrivacy(ctx, audit.PrivacyErasure, req.UID.String(), proj.Slug, in.Reference); err != nil {
		ctx.Logger().Warn("could not record an erasure in the audit log", "request", req.UID, "error", err)
	}
	ctx.SetStatus(http.StatusAccepted)
	return erasureOut(req), nil
}

// RequestExport asks, from the dashboard, for everything held about one player.
func RequestExport(ctx *muzak.Context, in schemas.DashboardPrivacyIn) (schemas.PrivacyRequestOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	req, err := submitExport(ctx, proj.UID, uint32(proj.ID), in.PlayerID, actorOf(ctx).Label, in.Reference)
	if err != nil {
		return schemas.PrivacyRequestOut{}, err
	}
	if err := logPrivacy(ctx, audit.PrivacyExport, req.UID.String(), proj.Slug, in.Reference); err != nil {
		ctx.Logger().Warn("could not record an export in the audit log", "request", req.UID, "error", err)
	}
	ctx.SetStatus(http.StatusAccepted)
	return schemas.PrivacyRequestOut{
		ID: req.UID, Kind: "export", PlayerID: req.ActorKey, Status: string(req.Status),
		RequestedBy: req.RequestedBy, Reference: req.Reference, RequestedAt: req.CreatedAt.UTC(),
	}, nil
}

// DownloadDashboardExport streams a completed export's bundle to a member.
func DownloadDashboardExport(ctx *muzak.Context, in schemas.DashboardExportParams) (muzak.Empty, error) {
	db := muzak.From[*pgdb.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	if !db.Ready() {
		return muzak.Empty{}, muzak.ServiceUnavailable("the database is unavailable")
	}
	req, err := db.Client.DataExportRequest.Query().
		Where(dataexportrequest.UID(in.RequestID), dataexportrequest.ProjectUID(proj.UID)).Only(ctx.Context())
	if err != nil {
		return muzak.Empty{}, requestNotFound(err, "export")
	}
	return streamExport(ctx, req)
}

// logPrivacy records a request in the organization's log. The request itself lives in the cell
// and the log in the global tier, so the two are not one transaction; the request is the one that
// must not be lost, and it was written first.
func logPrivacy(ctx *muzak.Context, action audit.Action, id, project, reference string) error {
	_, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, action, "privacy_request", id,
			map[string]any{"project": project, "reference": reference})
	})
	return err
}
