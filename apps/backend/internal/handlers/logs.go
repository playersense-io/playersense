package handlers

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	environmentpkg "playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/logissue"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/logs"
	"playersense.dev/backend/internal/schemas"
)

// Cloud Logging's pages (ADR 0050). The project comes from the path, checked against the caller's
// access list before any permission, and the environment is looked up inside it, as the explorer's
// are. Every range is clamped to the project's log retention: what is past it is gone, or about to
// be, and is not shown whether the purge has reached it yet or not.

// logContext is what every log route starts from.
type logContext struct {
	conn        driver.Conn
	scope       logs.Scope
	project     *ent.Project
	environment string
	players     bool // the caller holds player.read
}

func openLogs(ctx *muzak.Context, environment string) (*logContext, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	proj := principal.Project

	env, err := db.Client.Environment.Query().
		Where(environmentpkg.ProjectID(proj.ID), environmentpkg.KindEQ(environmentpkg.Kind(environment))).
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no " + environment + " environment")
		}
		return nil, core.AsHTTPError(err)
	}
	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return nil, logsUnavailable(err)
	}
	return &logContext{
		conn:        conn,
		scope:       logs.Scope{ProjectSeq: uint32(proj.ID), EnvironmentSeq: uint16(env.ID)},
		project:     proj,
		environment: environment,
		players:     principal.Actor.Require(authz.PlayerRead, proj.ID) == nil,
	}, nil
}

func logsUnavailable(err error) error {
	return muzak.ServiceUnavailable("the logs could not be read; retry shortly").Wrap(err)
}

// logRetention is how many days a project keeps its logs.
func logRetention(p *ent.Project) uint16 {
	if p.LogRetentionDays == 0 {
		return core.DefaultLogRetentionDays
	}
	return p.LogRetentionDays
}

// earliest is the oldest moment a project's logs still hold.
func (lc *logContext) earliest(now time.Time) time.Time {
	return now.Add(-time.Duration(logRetention(lc.project)) * 24 * time.Hour).Truncate(time.Minute)
}

// window resolves a range and clamps it to the retention.
func (lc *logContext) window(w schemas.LogsWindow) (logs.Range, error) {
	now := time.Now().UTC()
	from, to, err := w.Resolve(now)
	if err != nil {
		return logs.Range{}, err
	}
	if earliest := lc.earliest(now); from.Before(earliest) {
		from = earliest
	}
	if !from.Before(to) {
		// Wholly before the retention: an empty range rather than an error, so a bookmark to
		// last month's logs opens on "nothing kept" and not on a refusal.
		from = to
	}
	return logs.Range{From: from, To: to}, nil
}

// search parses the search box, refusing a player filter the caller may not use.
func (lc *logContext) search(q string) (*logs.Search, error) {
	s, err := logs.ParseSearch(q)
	if err != nil {
		return nil, muzak.UnprocessableEntity(err.Error()).WithCode("bad_search")
	}
	if s.NamesPlayer() && !lc.players {
		return nil, muzak.Forbidden("searching by player needs player.read")
	}
	return s, nil
}

// LogHistogram is the explorer's chart.
func LogHistogram(ctx *muzak.Context, in schemas.LogsSearchParams) (schemas.LogHistogramOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogHistogramOut{}, err
	}
	r, err := lc.window(in.LogsWindow)
	if err != nil {
		return schemas.LogHistogramOut{}, err
	}
	s, err := lc.search(in.Query)
	if err != nil {
		return schemas.LogHistogramOut{}, err
	}
	out := schemas.LogHistogramOut{From: r.From, To: r.To, Earliest: lc.earliest(time.Now().UTC()),
		Buckets: []schemas.LogBucketOut{}}
	if !r.From.Before(r.To) {
		return out, nil
	}
	step, buckets, err := logs.Histogram(ctx.Context(), lc.conn, lc.scope, r, s)
	if err != nil {
		return schemas.LogHistogramOut{}, logsUnavailable(err)
	}
	out.StepSeconds = int64(step / time.Second)
	for _, b := range buckets {
		bar := schemas.LogBucketOut{At: b.At}
		for level, n := range b.Counts {
			bar.Add(level, n)
			out.Totals.Add(level, n)
		}
		out.Buckets = append(out.Buckets, bar)
	}
	return out, nil
}

// LogRecords is one page of the records under the chart.
func LogRecords(ctx *muzak.Context, in schemas.LogRecordsParams) (schemas.LogRecordsOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogRecordsOut{}, err
	}
	r, err := lc.window(in.LogsWindow)
	if err != nil {
		return schemas.LogRecordsOut{}, err
	}
	s, err := lc.search(in.Query)
	if err != nil {
		return schemas.LogRecordsOut{}, err
	}
	var after *logs.Cursor
	if in.Cursor != "" {
		if after, err = logs.ParseCursor(in.Cursor); err != nil {
			return schemas.LogRecordsOut{}, muzak.BadRequest("cursor is not one this API handed out")
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	out := schemas.LogRecordsOut{Records: []schemas.LogRecordSummaryOut{}}
	if !r.From.Before(r.To) {
		return out, nil
	}
	rows, next, err := logs.Records(ctx.Context(), lc.conn, lc.scope, r, s, after, limit)
	if err != nil {
		return schemas.LogRecordsOut{}, logsUnavailable(err)
	}
	for _, row := range rows {
		out.Records = append(out.Records, lc.summaryOut(row))
	}
	if next != nil {
		out.NextCursor = next.String()
	}
	return out, nil
}

// LogNewer counts what arrived above the page being read.
func LogNewer(ctx *muzak.Context, in schemas.LogNewerParams) (schemas.LogNewerOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogNewerOut{}, err
	}
	since, err := time.Parse(time.RFC3339Nano, in.Since)
	if err != nil {
		return schemas.LogNewerOut{}, muzak.BadRequest("since is not an RFC 3339 time")
	}
	s, err := lc.search(in.Query)
	if err != nil {
		return schemas.LogNewerOut{}, err
	}
	n, err := logs.Newer(ctx.Context(), lc.conn, lc.scope, since, s)
	if err != nil {
		return schemas.LogNewerOut{}, logsUnavailable(err)
	}
	return schemas.LogNewerOut{Count: n}, nil
}

// LogRecord is one record whole.
func LogRecord(ctx *muzak.Context, in schemas.LogRecordParams) (schemas.LogRecordOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogRecordOut{}, err
	}
	when, err := time.Parse(time.RFC3339Nano, in.At)
	if err != nil {
		return schemas.LogRecordOut{}, muzak.BadRequest("at is not an RFC 3339 time")
	}
	if when.Before(lc.earliest(time.Now().UTC())) {
		return schemas.LogRecordOut{}, muzak.NotFound("this record is past the project's log retention")
	}
	record, err := logs.ReadRecord(ctx.Context(), lc.conn, lc.scope, uuid.MustParse(in.RecordID), when)
	if err != nil {
		if errors.Is(err, logs.ErrNotFound) {
			return schemas.LogRecordOut{}, muzak.NotFound("this project has no such log record")
		}
		return schemas.LogRecordOut{}, logsUnavailable(err)
	}
	return lc.recordOut(record), nil
}

func (lc *logContext) summaryOut(r logs.Summary) schemas.LogRecordSummaryOut {
	out := schemas.LogRecordSummaryOut{
		ID: r.ID.String(), At: r.At, Level: r.Severity, Kind: r.Kind, Logger: r.Logger,
		Message: r.Message, ExceptionType: r.ExceptionType, ExceptionValue: r.ExceptionValue,
		Culprit: r.Culprit, AppVersion: r.AppVersion, Platform: r.Platform, InstallID: r.InstallID.String(),
	}
	if r.Fingerprint != 0 {
		out.Issue = strconv.FormatUint(r.Fingerprint, 10)
	}
	if lc.players {
		out.PlayerID = r.PlayerID
	}
	return out
}

func (lc *logContext) recordOut(r *logs.Record) schemas.LogRecordOut {
	out := schemas.LogRecordOut{
		LogRecordSummaryOut: lc.summaryOut(r.Summary),
		ReceivedAt:          r.ReceivedAt, Handled: r.Handled,
		Frames: make([]schemas.LogFrameOut, 0, len(r.Frames)), Breadcrumbs: make([]schemas.LogBreadcrumbOut, 0, len(r.Breadcrumbs)),
		AppBuild: r.AppBuild, OSVersion: r.OSVersion, DeviceModel: r.DeviceModel, CountryCode: r.CountryCode,
		Attributes: r.Attributes, Size: r.Size,
	}
	if r.SDKName != "" {
		out.SDK = r.SDKName + " " + r.SDKVersion
	}
	if lc.players {
		out.SessionID = r.SessionID
	}
	if out.Attributes == nil {
		out.Attributes = map[string]string{}
	}
	for _, f := range r.Frames {
		out.Frames = append(out.Frames, schemas.LogFrameOut{Function: f.Function, Module: f.Module, File: f.File, Line: f.Line, InApp: f.InApp})
	}
	for _, b := range r.Breadcrumbs {
		out.Breadcrumbs = append(out.Breadcrumbs, schemas.LogBreadcrumbOut{At: b.At, Category: b.Category, Message: b.Message, Level: b.Level})
	}
	return out
}

// LogSummary is the explorer's figures, the range against the one before it.
func LogSummary(ctx *muzak.Context, in schemas.LogSummaryParams) (schemas.LogSummaryOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogSummaryOut{}, err
	}
	r, err := lc.window(in.LogsWindow)
	if err != nil {
		return schemas.LogSummaryOut{}, err
	}
	span := r.To.Sub(r.From)
	previous := logs.Range{From: r.From.Add(-span), To: r.From}
	var out schemas.LogSummaryOut
	for _, part := range []struct {
		r    logs.Range
		into *schemas.LogFiguresOut
	}{{r, &out.Current}, {previous, &out.Previous}} {
		if !part.r.From.Before(part.r.To) {
			continue
		}
		v, err := logs.VolumeOf(ctx.Context(), lc.conn, lc.scope, part.r)
		if err != nil {
			return schemas.LogSummaryOut{}, logsUnavailable(err)
		}
		active, fresh, installs, err := logs.IssueCounts(ctx.Context(), lc.conn, lc.scope, part.r)
		if err != nil {
			return schemas.LogSummaryOut{}, logsUnavailable(err)
		}
		*part.into = schemas.LogFiguresOut{Records: v.Records, Warnings: v.Warnings, Errors: v.Errors,
			Exceptions: v.Exceptions, Crashes: v.Crashes, Bytes: v.Bytes,
			Issues: active, NewIssues: fresh, AffectedInstalls: installs}
	}
	return out, nil
}

// issueStates reads what was decided about an environment's issues, by fingerprint.
func issueStates(ctx *muzak.Context, lc *logContext, fingerprints []string) (map[string]*ent.LogIssue, error) {
	db := muzak.From[*core.DB](ctx)
	q := db.Client.LogIssue.Query().Where(logissue.ProjectID(lc.project.ID),
		logissue.EnvironmentEQ(logissue.Environment(lc.environment)))
	if fingerprints != nil {
		q = q.Where(logissue.FingerprintIn(fingerprints...))
	}
	rows, err := q.All(ctx.Context())
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	out := make(map[string]*ent.LogIssue, len(rows))
	for _, row := range rows {
		out[row.Fingerprint] = row
	}
	return out, nil
}

// issueOut writes an issue with its decision. Resolved and seen since is regressed: the claim that
// it was fixed was wrong, and that is the most important thing the list can say about it.
func issueOut(issue logs.Issue, state *ent.LogIssue) schemas.LogIssueOut {
	out := schemas.LogIssueOut{
		ID: strconv.FormatUint(issue.Fingerprint, 10), Kind: issue.Kind, Level: issue.Severity,
		ExceptionType: issue.ExceptionType, Title: issue.Title, Culprit: issue.Culprit,
		FirstSeen: issue.FirstSeen, LastSeen: issue.LastSeen,
		FirstRelease: issue.FirstRelease, LastRelease: issue.LastRelease,
		TotalEvents: issue.TotalEvents, TotalInstalls: issue.TotalInstalls,
		Events: issue.Events, Installs: issue.Installs, Trend: issue.Trend, Status: "unresolved",
	}
	if out.Trend == nil {
		out.Trend = []uint64{}
	}
	if out.Title == "" {
		out.Title = issue.ExceptionType
	}
	if state != nil {
		out.Status, out.ResolvedAt, out.UpdatedBy = string(state.Status), state.ResolvedAt, state.UpdatedBy
		if state.Status == logissue.StatusResolved && state.ResolvedAt != nil && issue.LastSeen.After(*state.ResolvedAt) {
			out.Status = "regressed"
		}
	}
	return out
}

// maxListedIssues is how many issues one list answers with.
const maxListedIssues = 200

// LogIssues is the issues list.
func LogIssues(ctx *muzak.Context, in schemas.LogIssuesParams) (schemas.LogIssuesOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogIssuesOut{}, err
	}
	r, err := lc.window(in.LogsWindow)
	if err != nil {
		return schemas.LogIssuesOut{}, err
	}
	out := schemas.LogIssuesOut{Issues: []schemas.LogIssueOut{}}
	if !r.From.Before(r.To) {
		return out, nil
	}
	issues, err := logs.Issues(ctx.Context(), lc.conn, lc.scope, r)
	if err != nil {
		return schemas.LogIssuesOut{}, logsUnavailable(err)
	}
	states, err := issueStates(ctx, lc, nil)
	if err != nil {
		return schemas.LogIssuesOut{}, err
	}

	status := in.Status
	if status == "" {
		status = "unresolved"
	}
	needle := strings.ToLower(in.Query)
	for _, issue := range issues {
		written := issueOut(issue, states[strconv.FormatUint(issue.Fingerprint, 10)])
		// Unresolved includes regressed: both are open.
		switch {
		case status == "all":
		case status == "unresolved" && (written.Status == "unresolved" || written.Status == "regressed"):
		case written.Status == status:
		default:
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(written.Title+" "+written.ExceptionType+" "+written.Culprit), needle) {
			continue
		}
		out.Issues = append(out.Issues, written)
	}

	switch in.Sort {
	case "installs":
		slices.SortStableFunc(out.Issues, func(a, b schemas.LogIssueOut) int { return compareDesc(a.Installs, b.Installs) })
	case "last_seen":
		slices.SortStableFunc(out.Issues, func(a, b schemas.LogIssueOut) int { return b.LastSeen.Compare(a.LastSeen) })
	case "first_seen":
		slices.SortStableFunc(out.Issues, func(a, b schemas.LogIssueOut) int { return b.FirstSeen.Compare(a.FirstSeen) })
	default:
		slices.SortStableFunc(out.Issues, func(a, b schemas.LogIssueOut) int { return compareDesc(a.Events, b.Events) })
	}
	if len(out.Issues) > maxListedIssues {
		out.Issues = out.Issues[:maxListedIssues]
		out.Truncated = true
	}
	return out, nil
}

func compareDesc(a, b uint64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

// LogIssue is one issue: what it is, its series over the range, where it happens and its latest record.
func LogIssue(ctx *muzak.Context, in schemas.LogIssueParams) (schemas.LogIssueDetailOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogIssueDetailOut{}, err
	}
	r, err := lc.window(in.LogsWindow)
	if err != nil {
		return schemas.LogIssueDetailOut{}, err
	}
	fingerprint, err := strconv.ParseUint(in.Fingerprint, 10, 64)
	if err != nil {
		return schemas.LogIssueDetailOut{}, muzak.NotFound("this project has no such issue")
	}
	issue, err := logs.OneIssue(ctx.Context(), lc.conn, lc.scope, r, fingerprint)
	if err != nil {
		if errors.Is(err, logs.ErrNotFound) {
			return schemas.LogIssueDetailOut{}, muzak.NotFound("this project has no such issue")
		}
		return schemas.LogIssueDetailOut{}, logsUnavailable(err)
	}
	states, err := issueStates(ctx, lc, []string{in.Fingerprint})
	if err != nil {
		return schemas.LogIssueDetailOut{}, err
	}
	out := schemas.LogIssueDetailOut{Issue: issueOut(*issue, states[in.Fingerprint]),
		Series: []schemas.LogPointOut{}, Breakdown: map[string][]schemas.LogShareOut{}}
	if !r.From.Before(r.To) {
		return out, nil
	}

	step, points, err := logs.IssueSeries(ctx.Context(), lc.conn, lc.scope, r, fingerprint)
	if err != nil {
		return schemas.LogIssueDetailOut{}, logsUnavailable(err)
	}
	out.StepSeconds = int64(step / time.Second)
	for _, p := range points {
		out.Series = append(out.Series, schemas.LogPointOut{At: p.At, Value: p.Value})
	}

	shares, err := logs.Breakdown(ctx.Context(), lc.conn, lc.scope, r, fingerprint)
	if err != nil {
		return schemas.LogIssueDetailOut{}, logsUnavailable(err)
	}
	for name, list := range shares {
		for _, share := range list {
			out.Breakdown[name] = append(out.Breakdown[name], schemas.LogShareOut{Value: share.Value, Count: share.Count})
		}
	}

	// The newest record of the issue: a page of one from the list's own query.
	rows, _, err := logs.Records(ctx.Context(), lc.conn, lc.scope, r, &logs.Search{Fingerprint: fingerprint}, nil, 1)
	if err != nil {
		return schemas.LogIssueDetailOut{}, logsUnavailable(err)
	}
	if len(rows) == 1 {
		record, err := logs.ReadRecord(ctx.Context(), lc.conn, lc.scope, rows[0].ID, rows[0].At)
		if err == nil {
			written := lc.recordOut(record)
			out.Latest = &written
		}
	}
	return out, nil
}

// SetLogIssue records what was decided about an issue.
func SetLogIssue(ctx *muzak.Context, in schemas.SetLogIssueIn) (schemas.LogIssueStateOut, error) {
	lc, err := openLogs(ctx, in.Environment)
	if err != nil {
		return schemas.LogIssueStateOut{}, err
	}
	who := actorOf(ctx).Label
	now := nowUTC()
	saved, err := inTx(ctx, func(tx *ent.Tx) (*ent.LogIssue, error) {
		create := tx.LogIssue.Create().
			SetProjectID(lc.project.ID).
			SetEnvironment(logissue.Environment(lc.environment)).
			SetFingerprint(in.Fingerprint).
			SetStatus(logissue.Status(in.Status)).
			SetUpdatedBy(who)
		if in.Status == "resolved" {
			create = create.SetResolvedAt(now)
		}
		id, err := create.OnConflictColumns(logissue.FieldProjectID, logissue.FieldEnvironment, logissue.FieldFingerprint).
			Update(func(u *ent.LogIssueUpsert) {
				u.SetStatus(logissue.Status(in.Status)).SetUpdatedBy(who).UpdateUpdatedAt()
				if in.Status == "resolved" {
					u.SetResolvedAt(now)
				} else {
					u.ClearResolvedAt()
				}
			}).ID(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		saved, err := tx.LogIssue.Get(ctx.Context(), id)
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return saved, auditFor(ctx).record(ctx.Context(), tx, audit.LogIssueSet, "log_issue", in.Fingerprint,
			map[string]any{"status": in.Status, "environment": lc.environment})
	})
	if err != nil {
		return schemas.LogIssueStateOut{}, err
	}
	return schemas.LogIssueStateOut{ID: saved.Fingerprint, Status: string(saved.Status),
		ResolvedAt: saved.ResolvedAt, UpdatedBy: saved.UpdatedBy}, nil
}
