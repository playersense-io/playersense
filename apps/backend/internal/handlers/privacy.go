package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"muzak.dev/framework"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/regional"
	"playersense.dev/backend/ent/regional/datadeletionrequest"
	"playersense.dev/backend/ent/regional/dataexportrequest"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/tombstone"
	"playersense.dev/backend/internal/valkey"
)

// SubmitErasure records a data-subject deletion request and stops the data being used
// (§61, §101; docs/compliance/05).
//
// It returns as soon as the request is durable, which is the whole design rather than a
// shortcut. Removal is a rewrite of ClickHouse parts and of archive objects and takes hours;
// a synchronous erasure endpoint would be an availability problem and a denial-of-service
// vector at once. What happens inside this handler is the part that has to be immediate: the
// record, and the tombstone that stops the actor's events being accepted or served from the
// next message onward.
//
// Cell-local by necessity, not by preference. The request names a player, and a player
// identifier never reaches the global tier (§99) — so this cannot live on control-api, which
// is US-hosted and knows nothing about anyone's players.
func SubmitErasure(ctx *muzak.Context, in schemas.ErasureIn) (schemas.ErasureOut, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	cell := muzak.From[*pgdb.Cell](ctx)
	cache := muzak.From[*valkey.Client](ctx)

	if tenant.Cell != home.Slug {
		return schemas.ErasureOut{}, misdirected(tenant)
	}
	projectUID, err := uuid.Parse(tenant.ProjectID)
	if err != nil {
		return schemas.ErasureOut{}, core.AsHTTPError(err)
	}
	if !cell.Ready() {
		return schemas.ErasureOut{}, muzak.ServiceUnavailable(
			"this cell's database is unavailable; the request was not recorded, so send it again")
	}

	// An open request for the same person is the answer to this one. §48's reasoning about
	// purchases applies at least as strongly here: a studio retrying after a timeout must
	// not create a second erasure, and two requests for one person would be two audit
	// records of one event.
	existing, err := cell.Client.DataDeletionRequest.Query().
		Where(
			datadeletionrequest.ProjectUID(projectUID),
			datadeletionrequest.ActorKey(in.ActorKey),
			datadeletionrequest.StatusNEQ(datadeletionrequest.StatusCompleted),
		).
		First(ctx.Context())
	switch {
	case err == nil:
		ctx.SetStatus(http.StatusAccepted)
		return erasureOut(existing), nil
	case !regional.IsNotFound(err):
		return schemas.ErasureOut{}, core.AsHTTPError(err)
	}

	req, err := cell.Client.DataDeletionRequest.Create().
		SetProjectUID(projectUID).
		// The surrogate, taken from the credential because it is the only place it is
		// known: the worker that runs the purge is cell-local and the projects table is in
		// another jurisdiction (§99). Without it the purge would have to scan every
		// tenant's parts to find one project's rows.
		SetProjectSeq(tenant.ProjectSeq).
		SetActorKey(in.ActorKey).
		Save(ctx.Context())
	if err != nil {
		return schemas.ErasureOut{}, core.AsHTTPError(err)
	}

	// The tombstone goes up after the record and never before it. The two failure modes are
	// not symmetrical: a record with no tombstone is repaired by the worker's next pass,
	// while a tombstone with no record would drop a living player's events with nothing
	// anywhere to explain why. A cache that cannot answer is therefore not an error here —
	// the purge is the guarantee and the tombstone is the fast path (§06).
	if cache != nil && cache.Ready() {
		if err := tombstone.Raise(ctx.Context(), cache, projectUID.String(), in.ActorKey,
			erasureTombstoneLease(ctx)); err != nil {
			ctx.Logger().Warn("could not raise the erasure tombstone; the purge will remove the data anyway",
				"request", req.UID, "error", err)
		} else if updated, err := cell.Client.DataDeletionRequest.UpdateOne(req).
			SetStatus(datadeletionrequest.StatusTombstoned).
			SetTombstonedAt(time.Now().UTC()).
			Save(ctx.Context()); err == nil {
			req = updated
		}
	}

	ctx.SetStatus(http.StatusAccepted)
	return erasureOut(req), nil
}

// ReadErasure reports where one erasure has got to.
func ReadErasure(ctx *muzak.Context, in schemas.PrivacyRequestParams) (schemas.ErasureOut, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	cell := muzak.From[*pgdb.Cell](ctx)

	if tenant.Cell != home.Slug {
		return schemas.ErasureOut{}, misdirected(tenant)
	}
	projectUID, err := uuid.Parse(tenant.ProjectID)
	if err != nil {
		return schemas.ErasureOut{}, core.AsHTTPError(err)
	}
	if !cell.Ready() {
		return schemas.ErasureOut{}, muzak.ServiceUnavailable("this cell's database is unavailable")
	}

	// Scoped to the credential's project as well as to the identifier. A request id is a
	// UUID and guessing one is not a plausible attack, but scoping it is free and the
	// alternative is an endpoint whose safety rests on an argument about entropy.
	req, err := cell.Client.DataDeletionRequest.Query().
		Where(
			datadeletionrequest.UID(in.RequestID),
			datadeletionrequest.ProjectUID(projectUID),
		).
		Only(ctx.Context())
	if err != nil {
		return schemas.ErasureOut{}, requestNotFound(err, "erasure")
	}
	return erasureOut(req), nil
}

// SubmitExport asks for everything held about one actor (§101).
func SubmitExport(ctx *muzak.Context, in schemas.ExportIn) (schemas.ExportOut, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	cell := muzak.From[*pgdb.Cell](ctx)
	settings := muzak.From[config.Settings](ctx)

	if tenant.Cell != home.Slug {
		return schemas.ExportOut{}, misdirected(tenant)
	}
	projectUID, err := uuid.Parse(tenant.ProjectID)
	if err != nil {
		return schemas.ExportOut{}, core.AsHTTPError(err)
	}
	if !cell.Ready() {
		return schemas.ExportOut{}, muzak.ServiceUnavailable(
			"this cell's database is unavailable; the request was not recorded, so send it again")
	}

	// An export for somebody who is being erased would assemble a fresh copy of exactly the
	// data an erasure is working to remove, and park it in a bucket. The two rights are both
	// real and the order between them is the studio's to choose — so this is refused with
	// the reason rather than silently reordered.
	open, err := cell.Client.DataDeletionRequest.Query().
		Where(
			datadeletionrequest.ProjectUID(projectUID),
			datadeletionrequest.ActorKey(in.ActorKey),
			datadeletionrequest.StatusNEQ(datadeletionrequest.StatusCompleted),
		).
		Exist(ctx.Context())
	if err != nil {
		return schemas.ExportOut{}, core.AsHTTPError(err)
	}
	if open {
		return schemas.ExportOut{}, muzak.Conflict(
			"this actor has an erasure in progress, so there is nothing left to export; " +
				"an access request has to be made before the erasure that answers it").
			WithCode("erasure_in_progress")
	}

	existing, err := cell.Client.DataExportRequest.Query().
		Where(
			dataexportrequest.ProjectUID(projectUID),
			dataexportrequest.ActorKey(in.ActorKey),
			dataexportrequest.StatusIn(
				dataexportrequest.StatusReceived,
				dataexportrequest.StatusRunning,
			),
		).
		First(ctx.Context())
	switch {
	case err == nil:
		ctx.SetStatus(http.StatusAccepted)
		return exportOut(existing, settings), nil
	case !regional.IsNotFound(err):
		return schemas.ExportOut{}, core.AsHTTPError(err)
	}

	req, err := cell.Client.DataExportRequest.Create().
		SetProjectUID(projectUID).
		SetProjectSeq(tenant.ProjectSeq).
		SetActorKey(in.ActorKey).
		Save(ctx.Context())
	if err != nil {
		return schemas.ExportOut{}, core.AsHTTPError(err)
	}

	ctx.SetStatus(http.StatusAccepted)
	return exportOut(req, settings), nil
}

// ReadExport reports whether a bundle is ready, and where to collect it.
func ReadExport(ctx *muzak.Context, in schemas.PrivacyRequestParams) (schemas.ExportOut, error) {
	req, err := findExport(ctx, in.RequestID)
	if err != nil {
		return schemas.ExportOut{}, err
	}
	return exportOut(req, muzak.From[config.Settings](ctx)), nil
}

// DownloadExport streams one bundle.
//
// Through the API rather than by handing out a signed bucket URL, and the reason is the same
// one that keeps ClickHouse credentials out of the browser (§10.5): the credential that
// reaches the object should be the one the caller already holds, scoped to their project,
// revocable in one place. A signed URL is a second credential with different lifetime rules
// and no revocation, issued for an object that holds one named person's entire history.
func DownloadExport(ctx *muzak.Context, in schemas.PrivacyRequestParams) (muzak.Empty, error) {
	store := muzak.From[blobstore.Store](ctx)

	req, err := findExport(ctx, in.RequestID)
	if err != nil {
		return muzak.Empty{}, err
	}
	if req.Status != dataexportrequest.StatusCompleted || req.ObjectPath == "" {
		return muzak.Empty{}, muzak.Conflict(
			"this export is " + string(req.Status) + "; there is nothing to download yet").
			WithCode("export_not_ready")
	}
	if req.Bucket != store.Bucket() {
		return muzak.Empty{}, muzak.ServiceUnavailable(
			"this bundle was written to a store this service is not holding")
	}

	body, err := store.Get(ctx.Context(), req.ObjectPath)
	if errors.Is(err, blobstore.ErrNotFound) {
		// Expired between the status read and the fetch, or removed by an erasure that
		// arrived in between. Either way it is gone and saying so is the answer.
		return muzak.Empty{}, muzak.NotFound(
			"this bundle is no longer available; submit the request again").
			WithCode("export_gone")
	}
	if err != nil {
		return muzak.Empty{}, muzak.ServiceUnavailable("the bundle could not be read").Wrap(err)
	}

	w := ctx.ResponseWriter()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	// Named after the request rather than after the person. A file called `player-42.ndjson`
	// sitting in somebody's downloads folder is an identifier PlayerSense put there.
	w.Header().Set("Content-Disposition",
		`attachment; filename="playersense-export-`+req.UID.String()+`.ndjson"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return muzak.Empty{}, nil
}

// findExport loads one export, scoped to the credential's project.
func findExport(ctx *muzak.Context, id uuid.UUID) (*regional.DataExportRequest, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	home := muzak.From[core.CellIdentity](ctx)
	cell := muzak.From[*pgdb.Cell](ctx)

	if tenant.Cell != home.Slug {
		return nil, misdirected(tenant)
	}
	projectUID, err := uuid.Parse(tenant.ProjectID)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	if !cell.Ready() {
		return nil, muzak.ServiceUnavailable("this cell's database is unavailable")
	}

	req, err := cell.Client.DataExportRequest.Query().
		Where(
			dataexportrequest.UID(id),
			dataexportrequest.ProjectUID(projectUID),
		).
		Only(ctx.Context())
	if err != nil {
		return nil, requestNotFound(err, "export")
	}
	return req, nil
}

// erasureTombstoneLease is how long the handler's tombstone survives unrefreshed.
//
// Long enough to cover the gap until the worker's first pass picks the request up and starts
// renewing it, and derived from the same setting so the two cannot drift: if nothing renews
// it, the tombstone lapses and §61's purge is left as the guarantee it always was.
func erasureTombstoneLease(ctx *muzak.Context) time.Duration {
	settings := muzak.From[config.Settings](ctx)
	if settings.ErasureDrainWindow > 0 {
		return settings.ErasureDrainWindow + 2*time.Hour
	}
	return 26 * time.Hour
}

func erasureOut(req *regional.DataDeletionRequest) schemas.ErasureOut {
	return schemas.ErasureOut{
		RequestID:        req.UID,
		ActorKey:         req.ActorKey,
		Status:           string(req.Status),
		ReceivedAt:       req.CreatedAt.UTC(),
		TombstonedAt:     utc(req.TombstonedAt),
		CompletedAt:      utc(req.CompletedAt),
		DevicesDeleted:   req.DevicesDeleted,
		ObjectsRedacted:  req.ObjectsRedacted,
		PurchasesDeleted: req.PurchasesDeleted,
		FailureReason:    req.FailureReason,
	}
}

func exportOut(req *regional.DataExportRequest, settings config.Settings) schemas.ExportOut {
	out := schemas.ExportOut{
		RequestID:     req.UID,
		ActorKey:      req.ActorKey,
		Status:        string(req.Status),
		ReceivedAt:    req.CreatedAt.UTC(),
		CompletedAt:   utc(req.CompletedAt),
		ExpiresAt:     utc(req.ExpiresAt),
		Events:        req.RowCount,
		SizeBytes:     req.ObjectSizeBytes,
		FailureReason: req.FailureReason,
	}
	if req.Status == dataexportrequest.StatusCompleted {
		out.DownloadURL = settings.QueryAPIPublicURL + "/v1/privacy/exports/" + req.UID.String() + "/download"
	}
	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}

// requestNotFound hides the difference between "no such request" and "not yours".
//
// One answer for both, for the reason ErrUnknownKey gives about credentials: telling them
// apart tells a caller which project a request id belongs to.
func requestNotFound(err error, kind string) error {
	if regional.IsNotFound(err) {
		return muzak.NotFound("no such " + kind + " request for this project")
	}
	return core.AsHTTPError(err)
}
