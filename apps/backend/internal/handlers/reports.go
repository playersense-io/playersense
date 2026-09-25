package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/report"
	"playersense.dev/backend/ent/reportexport"
	"playersense.dev/backend/ent/reportrun"
	"playersense.dev/backend/ent/reportrunevent"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/blobstore"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/reports"
	"playersense.dev/backend/internal/schemas"
)

// Reports: saved graphs, a preview over the project's rows, and runs followed as a stream.

// ReportLauncher runs reports in the background of the API, a few at a time, so a test run
// asked for on the dashboard answers at once and is followed on its stream.
type ReportLauncher struct {
	Runner *reports.Runner
	Key    []byte
	slots  chan struct{}
}

// NewReportLauncher returns a launcher running at most parallel runs at once. An empty key is
// replaced by one drawn for this process.
func NewReportLauncher(runner *reports.Runner, key string, parallel int) *ReportLauncher {
	k := []byte(key)
	if len(k) == 0 {
		k = make([]byte, 32)
		_, _ = rand.Read(k)
		slog.Default().Warn("STREAM_SIGNING_KEY is not set; report run streams open only on the instance that started them")
	}
	return &ReportLauncher{Runner: runner, Key: k, slots: make(chan struct{}, parallel)}
}

// Launch starts a run in the background, sending through the given sender. Refused when every
// slot is taken.
func (l *ReportLauncher) Launch(t reports.Target, sender *notify.Sender) bool {
	runner := *l.Runner
	if sender != nil {
		runner.Notify = sender
	}
	select {
	case l.slots <- struct{}{}:
	default:
		return false
	}
	go func() {
		defer func() { <-l.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		runner.Execute(ctx, t)
	}()
	return true
}

func graphOf(raw []byte) (reports.Graph, error) {
	g, err := reports.Decode(raw)
	if err != nil {
		return g, muzak.UnprocessableEntity(err.Error()).WithCode("invalid_graph")
	}
	return g, nil
}

func graphMap(raw []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func reportOut(row *ent.Report, last *ent.ReportRun) schemas.ReportOut {
	graph, _ := json.Marshal(row.Graph)
	out := schemas.ReportOut{ID: row.UID, Name: row.Name, Description: row.Description,
		Environment: string(row.Environment), Graph: graph, NextRunAt: row.NextRunAt, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy}
	if last != nil {
		r := runOut(row, last, nil)
		out.LastRun = &r
	}
	return out
}

func runOut(rep *ent.Report, run *ent.ReportRun, exports []*ent.ReportExport) schemas.ReportRunOut {
	out := schemas.ReportRunOut{ID: run.UID, Trigger: string(run.Trigger), Dry: run.Dry, Focus: run.Focus,
		Status: string(run.Status), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Deliveries: run.Deliveries,
		RequestedBy: run.RequestedBy, Error: run.Error}
	if out.Deliveries == nil {
		out.Deliveries = []map[string]any{}
	}
	for _, e := range exports {
		out.Exports = append(out.Exports, schemas.ReportExportOut{Node: e.Node, FileName: e.FileName, Rows: e.Rows,
			SizeBytes: e.SizeBytes, ExpiresAt: e.ExpiresAt,
			DownloadPath: "/v1/organizations/{org}/projects/{project}/reports/" + rep.UID.String() + "/runs/" +
				run.UID.String() + "/exports/" + e.Node})
	}
	return out
}

func findReport(ctx *muzak.Context, id uuid.UUID) (*ent.Report, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := db.Client.Report.Query().Where(report.UID(id), report.ProjectID(proj.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no such report")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

func environmentOr(e string) string {
	if e == "" {
		return "production"
	}
	return e
}

// ListReports is a project's reports, each with its last run.
func ListReports(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ReportListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.Report.Query().Where(report.ProjectID(proj.ID)).
		Order(ent.Desc(report.FieldUpdatedAt)).All(ctx.Context())
	if err != nil {
		return schemas.ReportListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ReportListOut{Reports: make([]schemas.ReportOut, 0, len(rows))}
	for _, row := range rows {
		last, err := db.Client.ReportRun.Query().Where(reportrun.ReportID(row.ID)).
			Order(ent.Desc(reportrun.FieldID)).First(ctx.Context())
		if err != nil && !ent.IsNotFound(err) {
			return out, core.AsHTTPError(err)
		}
		out.Reports = append(out.Reports, reportOut(row, last))
	}
	return out, nil
}

// ReadReport is one report.
func ReadReport(ctx *muzak.Context, in schemas.ReportParams) (schemas.ReportOut, error) {
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return schemas.ReportOut{}, err
	}
	return reportOut(row, nil), nil
}

// CreateReport saves a report.
func CreateReport(ctx *muzak.Context, in schemas.ReportIn) (schemas.ReportOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	g, err := graphOf(in.Graph)
	if err != nil {
		return schemas.ReportOut{}, err
	}
	who := actorOf(ctx).Label
	next := g.NextRun(nowUTC())
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.Report, error) {
		create := tx.Report.Create().SetProjectID(proj.ID).SetName(in.Name).SetDescription(in.Description).
			SetEnvironment(report.Environment(environmentOr(in.Environment))).SetGraph(graphMap(in.Graph)).
			SetCreatedBy(who).SetUpdatedBy(who)
		if !next.IsZero() {
			create = create.SetNextRunAt(next)
		}
		row, err := create.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.ReportWritten, "report", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": in.Name, "created": true})
	})
	if err != nil {
		return schemas.ReportOut{}, err
	}
	ctx.SetStatus(201)
	return reportOut(row, nil), nil
}

// UpdateReport replaces a report. Its schedules are read again, so a schedule added, changed or
// paused takes effect from its next time.
func UpdateReport(ctx *muzak.Context, in schemas.UpdateReportIn) (schemas.ReportOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return schemas.ReportOut{}, err
	}
	g, err := graphOf(in.Graph)
	if err != nil {
		return schemas.ReportOut{}, err
	}
	next := g.NextRun(nowUTC())
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Report, error) {
		update := tx.Report.UpdateOne(row).SetName(in.Name).SetDescription(in.Description).
			SetEnvironment(report.Environment(environmentOr(in.Environment))).SetGraph(graphMap(in.Graph)).
			SetUpdatedBy(actorOf(ctx).Label)
		if next.IsZero() {
			update = update.ClearNextRunAt()
		} else {
			update = update.SetNextRunAt(next)
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.ReportWritten, "report", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": in.Name, "created": false})
	})
	if err != nil {
		return schemas.ReportOut{}, err
	}
	return reportOut(updated, nil), nil
}

// DeleteReport removes a report and its runs.
func DeleteReport(ctx *muzak.Context, in schemas.ReportParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return muzak.Empty{}, err
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		runs, err := tx.ReportRun.Query().Where(reportrun.ReportID(row.ID)).IDs(ctx.Context())
		if err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		if _, err := tx.ReportRunEvent.Delete().Where(reportrunevent.RunIDIn(runs...)).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		if _, err := tx.ReportExport.Delete().Where(reportexport.RunIDIn(runs...)).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		if _, err := tx.ReportRun.Delete().Where(reportrun.ReportID(row.ID)).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		if err := tx.Report.DeleteOne(row).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.ReportDeleted, "report", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": row.Name})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}

func reportSources(ctx *muzak.Context, environment string) (reports.SourceReader, error) {
	scope, err := scopeForMember(ctx, environment)
	if err != nil {
		return nil, err
	}
	return reports.EngineSources{Engine: muzak.From[*query.Engine](ctx), Scope: scope}, nil
}

// PreviewReport runs a graph once over the project's rows and sends nothing: every node's first
// rows, and what each Notify would do.
func PreviewReport(ctx *muzak.Context, in schemas.ReportPreviewIn) (schemas.ReportPreviewOut, error) {
	g, err := graphOf(in.Graph)
	if err != nil {
		return schemas.ReportPreviewOut{}, err
	}
	sources, err := reportSources(ctx, environmentOr(in.Environment))
	if err != nil {
		return schemas.ReportPreviewOut{}, err
	}
	name := in.Name
	if name == "" {
		name = "Untitled report"
	}
	out := schemas.ReportPreviewOut{Nodes: map[string]schemas.NodePreviewOut{}}
	for id, p := range reports.Preview(ctx.Context(), g, sources, name, nowUTC()) {
		n := schemas.NodePreviewOut{Rows: p.Rows, Total: p.Total, Error: p.Error, Columns: []schemas.ReportColumnOut{}}
		for _, c := range p.Columns {
			n.Columns = append(n.Columns, schemas.ReportColumnOut{Key: c.Key, Label: c.Label, Type: c.Type})
		}
		if p.Notify != nil {
			n.Notify = &schemas.NotifyOutcomeOut{Send: p.Notify.Send, Reason: p.Notify.Reason, Title: p.Notify.Title,
				Body: p.Notify.Body, Severity: p.Notify.Severity, Lines: p.Notify.Lines}
		}
		out.Nodes[id] = n
	}
	return out, nil
}

// StartRun runs a report now, as a test, and answers at once with the run and where to follow it.
func StartRun(ctx *muzak.Context, in schemas.StartRunIn) (schemas.StartedRunOut, error) {
	db := muzak.From[*core.DB](ctx)
	launcher := muzak.From[*ReportLauncher](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return schemas.StartedRunOut{}, err
	}
	raw, _ := json.Marshal(row.Graph)
	g, err := graphOf(raw)
	if err != nil {
		return schemas.StartedRunOut{}, err
	}
	if in.Focus != "" {
		if n, found := g.Node(in.Focus); !found || n.Data.Kind != reports.KindNotify {
			return schemas.StartedRunOut{}, muzak.UnprocessableEntity("focus is not a Notify in this report").
				WithCode("invalid_focus")
		}
	}
	sources, err := reportSources(ctx, string(row.Environment))
	if err != nil {
		return schemas.StartedRunOut{}, err
	}
	run, err := db.Client.ReportRun.Create().SetReportID(row.ID).SetTrigger(reportrun.TriggerTest).
		SetDry(in.Dry).SetFocus(in.Focus).SetStartedAt(nowUTC()).SetRequestedBy(actorOf(ctx).Label).
		Save(ctx.Context())
	if err != nil {
		return schemas.StartedRunOut{}, core.AsHTTPError(err)
	}
	if !launcher.Launch(reports.Target{Report: row, Run: run, Graph: g, Sources: sources, Project: proj},
		muzak.From[*notify.Sender](ctx)) {
		_ = db.Client.ReportRun.UpdateOne(run).SetStatus(reportrun.StatusFailed).SetFinishedAt(nowUTC()).
			SetError("too many reports are running; try again in a minute").Exec(ctx.Context())
		return schemas.StartedRunOut{}, muzak.ServiceUnavailable("too many reports are running; try again in a minute").
			WithCode("busy")
	}
	ctx.SetStatus(202)
	return schemas.StartedRunOut{Run: runOut(row, run, nil),
		StreamURL: "/v1/report-runs/" + run.UID.String() + "/events?token=" +
			reports.StreamToken(launcher.Key, run.UID.String(), nowUTC())}, nil
}

func findRun(ctx *muzak.Context, in schemas.ReportRunParams) (*ent.Report, *ent.ReportRun, error) {
	db := muzak.From[*core.DB](ctx)
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return nil, nil, err
	}
	run, err := db.Client.ReportRun.Query().Where(reportrun.UID(in.RunID), reportrun.ReportID(row.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil, muzak.NotFound("this report has no such run")
		}
		return nil, nil, core.AsHTTPError(err)
	}
	return row, run, nil
}

// ListRuns is a report's thirty latest runs.
func ListRuns(ctx *muzak.Context, in schemas.ReportParams) (schemas.ReportRunListOut, error) {
	db := muzak.From[*core.DB](ctx)
	row, err := findReport(ctx, in.ReportID)
	if err != nil {
		return schemas.ReportRunListOut{}, err
	}
	runs, err := db.Client.ReportRun.Query().Where(reportrun.ReportID(row.ID)).
		Order(ent.Desc(reportrun.FieldID)).Limit(30).All(ctx.Context())
	if err != nil {
		return schemas.ReportRunListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ReportRunListOut{Runs: make([]schemas.ReportRunOut, 0, len(runs))}
	for _, run := range runs {
		out.Runs = append(out.Runs, runOut(row, run, nil))
	}
	return out, nil
}

// ReadRun is one run, with the files it wrote.
func ReadRun(ctx *muzak.Context, in schemas.ReportRunParams) (schemas.ReportRunOut, error) {
	db := muzak.From[*core.DB](ctx)
	row, run, err := findRun(ctx, in)
	if err != nil {
		return schemas.ReportRunOut{}, err
	}
	exports, err := db.Client.ReportExport.Query().Where(reportexport.RunID(run.ID)).All(ctx.Context())
	if err != nil {
		return schemas.ReportRunOut{}, core.AsHTTPError(err)
	}
	return runOut(row, run, exports), nil
}

// CancelRun stops a run where it is. What it already sent stays sent.
func CancelRun(ctx *muzak.Context, in schemas.ReportRunParams) (schemas.ReportRunOut, error) {
	db := muzak.From[*core.DB](ctx)
	row, run, err := findRun(ctx, in)
	if err != nil {
		return schemas.ReportRunOut{}, err
	}
	if run.Status != reportrun.StatusRunning {
		return schemas.ReportRunOut{}, muzak.Conflict("the run has finished").WithCode("finished")
	}
	run, err = db.Client.ReportRun.UpdateOne(run).SetCancelRequested(true).Save(ctx.Context())
	if err != nil {
		return schemas.ReportRunOut{}, core.AsHTTPError(err)
	}
	return runOut(row, run, nil), nil
}

// DownloadReportExport streams a file a run wrote, for seven days.
func DownloadReportExport(ctx *muzak.Context, in schemas.ReportExportParams) (muzak.Empty, error) {
	db := muzak.From[*core.DB](ctx)
	_, run, err := findRun(ctx, in.ReportRunParams)
	if err != nil {
		return muzak.Empty{}, err
	}
	e, err := db.Client.ReportExport.Query().Where(reportexport.RunID(run.ID), reportexport.Node(in.Node)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return muzak.Empty{}, muzak.NotFound("the run wrote no file for that node")
		}
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	if nowUTC().After(e.ExpiresAt) {
		return muzak.Empty{}, muzak.NotFound("the file has expired; run the report again").WithCode("expired")
	}
	body, err := muzak.From[blobstore.Store](ctx).Get(ctx.Context(), e.ObjectPath)
	if err != nil {
		return muzak.Empty{}, muzak.NotFound("the file is no longer available; run the report again")
	}
	w := ctx.ResponseWriter()
	w.Header().Set("Content-Type", e.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Content-Disposition", `attachment; filename="`+e.FileName+`"`)
	w.WriteHeader(200)
	_, _ = w.Write(body)
	return muzak.Empty{}, nil
}

// FollowRun streams a run's events from the start, or from the Last-Event-ID a reconnecting
// browser sends, until the run finishes. Opened with the token StartRun's stream_url carries or a
// member's ordinary sign-in, since a browser's EventSource cannot send an Authorization header.
func FollowRun(ctx *muzak.Context, in schemas.RunStreamIn, stream *muzak.SSEStream[schemas.RunEventOut]) error {
	db := muzak.From[*core.DB](ctx)
	run, err := db.Client.ReportRun.Query().Where(reportrun.UID(in.RunID)).Only(ctx.Context())
	if err != nil {
		return err
	}
	after, _ := strconv.Atoi(stream.LastEventID())
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		events, err := db.Client.ReportRunEvent.Query().
			Where(reportrunevent.RunID(run.ID), reportrunevent.SeqGT(after)).
			Order(ent.Asc(reportrunevent.FieldSeq)).Limit(200).All(stream.Context())
		if err != nil {
			return err
		}
		for _, e := range events {
			payload := schemas.RunEventOut(e.Payload)
			if err := stream.SendEvent(muzak.SSEEvent[schemas.RunEventOut]{Name: e.Type, ID: strconv.Itoa(e.Seq), Data: &payload}); err != nil {
				return err
			}
			after = e.Seq
			if e.Type == "run_finished" {
				return nil
			}
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-tick.C:
		}
	}
	return nil
}

// StreamAccess lets a run's stream be opened with its token.
func StreamAccess(ctx *muzak.Context) (StreamAllowed, error) {
	launcher := muzak.From[*ReportLauncher](ctx)
	run := ctx.PathValue("run")
	if err := reports.CheckStreamToken(launcher.Key, run, ctx.Query("token"), time.Now()); err != nil {
		return StreamAllowed{}, muzak.Unauthorized("this stream needs the token its run was started with")
	}
	return StreamAllowed{}, nil
}

// StreamAllowed is the proof a stream token was checked.
type StreamAllowed struct{}

// FollowRunSignedIn is the same stream for a member's ordinary sign-in, for a client that can send
// an Authorization header (fetch rather than EventSource).
func FollowRunSignedIn(ctx *muzak.Context, in schemas.ReportRunParams, stream *muzak.SSEStream[schemas.RunEventOut]) error {
	if _, _, err := findRun(ctx, in); err != nil {
		return err
	}
	return FollowRun(ctx, schemas.RunStreamIn{RunID: in.RunID}, stream)
}
