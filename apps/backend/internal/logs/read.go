package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/protocol"
)

// What the dashboard reads. One table per question and none joined: the chart from the per-minute
// counts when it can be, the list from the records, newest first along the sort key, and the
// issues from the two tables built for them.

// ErrNotFound is a record or an issue that is not there, or no longer is.
var ErrNotFound = errors.New("not found")

// Scope is one environment of one project.
type Scope struct {
	ProjectSeq     uint32
	EnvironmentSeq uint16
}

// Range is a half-open window, [From, To).
type Range struct {
	From, To time.Time
}

// Severities lists the levels in order, the chart's stacking order and the filter's.
func Severities() []string { return append([]string(nil), severityOrder...) }

// bounded is every read's ceiling: a page waits no longer than this, and a question that would
// read more is refused rather than left running.
func bounded(ctx context.Context) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"max_execution_time": 20,
		"max_memory_usage":   2 << 30,
	}))
}

// at writes a time as a DateTime64(3) literal's text.
func at(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000") }

// ---- the chart -------------------------------------------------------------------------------

// steps are the bar widths the chart may use, narrowest first. A range is drawn in about ninety
// bars, at the first of these that gets there.
var steps = []time.Duration{
	time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// StepFor chooses a bar width for a range.
func StepFor(r Range) time.Duration {
	span := r.To.Sub(r.From)
	for _, step := range steps {
		if span/step <= 90 {
			return step
		}
	}
	return steps[len(steps)-1]
}

// Bucket is one bar: how many records of each level began inside it.
type Bucket struct {
	At     time.Time
	Counts map[string]uint64
}

// Histogram counts records per bar and level. From the per-minute table whenever the search is
// only levels and kinds and the bars are a minute or wider, which is a read of a few thousand rows
// whatever the volume; from the records otherwise, where the text index and the sort key keep it
// to the granules that can match.
func Histogram(ctx context.Context, conn driver.Conn, scope Scope, r Range, s *Search) (time.Duration, []Bucket, error) {
	step := StepFor(r)
	from := r.From.Truncate(step)
	seconds := int64(step / time.Second)

	var (
		q    string
		args []any
	)
	if s.OnlyVolume() && step >= time.Minute {
		where := []string{"project_seq = ?", "environment_seq = ?",
			"minute >= toDateTime(?, 'UTC')", "minute < toDateTime(?, 'UTC')"}
		args = []any{seconds, scope.ProjectSeq, scope.EnvironmentSeq,
			from.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime)}
		if len(s.Severities) > 0 {
			where = append(where, "severity IN ?")
			args = append(args, dedupe(s.Severities))
		}
		if len(s.Kinds) > 0 {
			where = append(where, "kind IN ?")
			args = append(args, dedupe(s.Kinds))
		}
		q = `SELECT toStartOfInterval(minute, toIntervalSecond(?)) AS bar, toString(severity), sum(records)
			FROM logs_volume_1m
			WHERE ` + strings.Join(where, " AND ") + `
			GROUP BY bar, severity`
	} else {
		where, filterArgs := recordWhere(scope, Range{From: from, To: r.To}, s)
		args = append([]any{seconds}, filterArgs...)
		q = `SELECT toStartOfInterval(timestamp, toIntervalSecond(?)) AS bar, toString(severity), count()
			FROM logs
			WHERE ` + strings.Join(where, " AND ") + `
			GROUP BY bar, severity`
	}

	rows, err := conn.Query(bounded(ctx), q, args...)
	if err != nil {
		return step, nil, fmt.Errorf("read the log histogram: %w", err)
	}
	defer rows.Close()

	// Every bar, empty ones included, so the chart's axis is the range and not the data.
	var buckets []Bucket
	index := map[int64]int{}
	for t := from; t.Before(r.To); t = t.Add(step) {
		index[t.Unix()] = len(buckets)
		buckets = append(buckets, Bucket{At: t, Counts: map[string]uint64{}})
	}
	for rows.Next() {
		var (
			bar      time.Time
			severity string
			count    uint64
		)
		if err := rows.Scan(&bar, &severity, &count); err != nil {
			return step, nil, err
		}
		if i, ok := index[bar.Unix()]; ok {
			buckets[i].Counts[severity] += count
		}
	}
	return step, buckets, rows.Err()
}

// recordWhere is the scope, the range and the search as conditions on `logs`.
func recordWhere(scope Scope, r Range, s *Search) ([]string, []any) {
	where := []string{"project_seq = ?", "environment_seq = ?",
		"timestamp >= toDateTime64(?, 3, 'UTC')", "timestamp < toDateTime64(?, 3, 'UTC')"}
	args := []any{scope.ProjectSeq, scope.EnvironmentSeq, at(r.From), at(r.To)}
	if s != nil {
		more, moreArgs := s.Where()
		where = append(where, more...)
		args = append(args, moreArgs...)
	}
	return where, args
}

// ---- the list --------------------------------------------------------------------------------

// Summary is one record as the list shows it: enough for a row, and cut short where long.
type Summary struct {
	ID             uuid.UUID
	At             time.Time
	Severity       string
	Kind           string
	Logger         string
	Message        string
	ExceptionType  string
	ExceptionValue string
	Culprit        string
	Fingerprint    uint64
	AppVersion     string
	Platform       string
	InstallID      uuid.UUID
	PlayerID       string
}

// Cursor is where a page ended: the last row's time and id, since two records can share a
// millisecond.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// String writes a cursor as the API hands it out.
func (c Cursor) String() string {
	return c.At.UTC().Format("2006-01-02T15:04:05.000Z") + "_" + c.ID.String()
}

// ParseCursor reads a cursor back.
func ParseCursor(s string) (*Cursor, error) {
	stamp, id, ok := strings.Cut(s, "_")
	if !ok {
		return nil, fmt.Errorf("no separator")
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", stamp)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &Cursor{At: t, ID: u}, nil
}

// previewBytes is how much of a message the list carries. A row shows one line of it; the whole
// record is one click away.
const previewBytes = 400

// Records is one page of the records matching a search, newest first, and where the next begins.
// Newest first is the sort key read backwards, so a page is a few granules however large the
// range: ClickHouse stops reading once it has the page.
func Records(ctx context.Context, conn driver.Conn, scope Scope, r Range, s *Search, after *Cursor, limit int) ([]Summary, *Cursor, error) {
	where, args := recordWhere(scope, r, s)
	if after != nil {
		where = append(where, "(timestamp < toDateTime64(?, 3, 'UTC') OR (timestamp = toDateTime64(?, 3, 'UTC') AND record_id < toUUID(?)))")
		args = append(args, at(after.At), at(after.At), after.ID.String())
	}
	q := `SELECT record_id, timestamp, toString(severity), toString(kind), toString(logger),
			substring(message, 1, ` + fmt.Sprint(previewBytes) + `), toString(exception_type),
			substring(exception_value, 1, ` + fmt.Sprint(previewBytes) + `), toString(culprit), fingerprint,
			toString(app_version), toString(platform), install_id, ifNull(player_id, '')
		FROM logs
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY timestamp DESC, record_id DESC
		LIMIT ` + fmt.Sprint(limit+1)

	rows, err := conn.Query(bounded(ctx), q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read logs: %w", err)
	}
	defer rows.Close()
	out := make([]Summary, 0, limit)
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.ID, &s.At, &s.Severity, &s.Kind, &s.Logger, &s.Message, &s.ExceptionType,
			&s.ExceptionValue, &s.Culprit, &s.Fingerprint, &s.AppVersion, &s.Platform, &s.InstallID, &s.PlayerID); err != nil {
			return nil, nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = &Cursor{At: last.At, ID: last.ID}
	}
	return out, next, nil
}

// Newer counts the records matching a search since a moment: what the live view says is waiting
// above the page the person is reading, without moving it under them.
func Newer(ctx context.Context, conn driver.Conn, scope Scope, since time.Time, s *Search) (uint64, error) {
	where, args := recordWhere(scope, Range{From: since.Add(time.Millisecond), To: time.Now().Add(time.Minute)}, s)
	var n uint64
	err := conn.QueryRow(bounded(ctx), `SELECT count() FROM logs WHERE `+strings.Join(where, " AND "), args...).Scan(&n)
	return n, err
}

// ---- one record ------------------------------------------------------------------------------

// Record is one record whole.
type Record struct {
	Summary
	ReceivedAt  time.Time
	Handled     bool
	Frames      []protocol.StackFrame
	Breadcrumbs []protocol.Breadcrumb
	SessionID   string
	AppBuild    string
	OSVersion   string
	DeviceModel string
	CountryCode string
	SDKName     string
	SDKVersion  string
	Attributes  map[string]string
	Size        uint32
}

// ReadRecord reads one record. Its time comes with its id, from the row the person clicked, so the
// read is a lookup inside one granule rather than a search through the retention.
func ReadRecord(ctx context.Context, conn driver.Conn, scope Scope, id uuid.UUID, when time.Time) (*Record, error) {
	q := `SELECT record_id, timestamp, toString(severity), toString(kind), toString(logger), message,
			toString(exception_type), exception_value, toString(culprit), fingerprint, toString(app_version),
			toString(platform), install_id, ifNull(player_id, ''),
			received_at, handled, stacktrace, breadcrumbs, session_id, toString(app_build), toString(os_version),
			toString(device_model), toString(country_code), toString(sdk_name), toString(sdk_version),
			CAST(attributes, 'Map(String, String)'), size
		FROM logs
		WHERE project_seq = ? AND environment_seq = ?
		  AND timestamp BETWEEN toDateTime64(?, 3, 'UTC') - INTERVAL 1 SECOND AND toDateTime64(?, 3, 'UTC') + INTERVAL 1 SECOND
		  AND record_id = toUUID(?)
		LIMIT 1`
	row := conn.QueryRow(bounded(ctx), q, scope.ProjectSeq, scope.EnvironmentSeq, at(when), at(when), id.String())
	var (
		r                   Record
		handled             uint8
		frames, breadcrumbs string
	)
	if err := row.Scan(&r.ID, &r.At, &r.Severity, &r.Kind, &r.Logger, &r.Message, &r.ExceptionType,
		&r.ExceptionValue, &r.Culprit, &r.Fingerprint, &r.AppVersion, &r.Platform, &r.InstallID, &r.PlayerID,
		&r.ReceivedAt, &handled, &frames, &breadcrumbs, &r.SessionID, &r.AppBuild, &r.OSVersion,
		&r.DeviceModel, &r.CountryCode, &r.SDKName, &r.SDKVersion, &r.Attributes, &r.Size); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read a log record: %w", err)
	}
	r.Handled = handled == 1
	if frames != "" {
		_ = json.Unmarshal([]byte(frames), &r.Frames)
	}
	if breadcrumbs != "" {
		_ = json.Unmarshal([]byte(breadcrumbs), &r.Breadcrumbs)
	}
	return &r, nil
}

// ---- the figures -----------------------------------------------------------------------------

// Volume is a range's records by what they were, from the per-minute counts.
type Volume struct {
	Records    uint64
	Warnings   uint64
	Errors     uint64 // error and fatal
	Exceptions uint64
	Crashes    uint64
	Bytes      uint64
}

// VolumeOf adds up a range.
func VolumeOf(ctx context.Context, conn driver.Conn, scope Scope, r Range) (Volume, error) {
	var v Volume
	err := conn.QueryRow(bounded(ctx), `
		SELECT sum(records),
			sumIf(records, severity = 'warn'),
			sumIf(records, severity IN ('error', 'fatal')),
			sumIf(records, kind = 'exception'),
			sumIf(records, kind = 'crash'),
			sum(bytes)
		FROM logs_volume_1m
		WHERE project_seq = ? AND environment_seq = ?
		  AND minute >= toDateTime(?, 'UTC') AND minute < toDateTime(?, 'UTC')`,
		scope.ProjectSeq, scope.EnvironmentSeq,
		r.From.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime)).
		Scan(&v.Records, &v.Warnings, &v.Errors, &v.Exceptions, &v.Crashes, &v.Bytes)
	return v, err
}

// IssueCounts is how many issues happened in a range, how many of them first happened in it, and
// how many installs any of them reached.
func IssueCounts(ctx context.Context, conn driver.Conn, scope Scope, r Range) (active, fresh, installs uint64, err error) {
	err = conn.QueryRow(bounded(ctx), `
		SELECT uniqExact(fingerprint), uniqCombinedMerge(installs)
		FROM log_issue_hours
		WHERE project_seq = ? AND environment_seq = ?
		  AND hour >= toStartOfHour(toDateTime(?, 'UTC')) AND hour < toDateTime(?, 'UTC')`,
		scope.ProjectSeq, scope.EnvironmentSeq,
		r.From.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime)).Scan(&active, &installs)
	if err != nil {
		return
	}
	err = conn.QueryRow(bounded(ctx), `
		SELECT count() FROM (
			SELECT fingerprint, min(first_seen) AS first
			FROM log_issues
			WHERE project_seq = ? AND environment_seq = ?
			GROUP BY fingerprint
			HAVING first >= toDateTime64(?, 3, 'UTC') AND first < toDateTime64(?, 3, 'UTC')
		)`, scope.ProjectSeq, scope.EnvironmentSeq, at(r.From), at(r.To)).Scan(&fresh)
	return
}

// ---- issues ----------------------------------------------------------------------------------

// Issue is an issue as it stands, and what it did inside the range asked about.
type Issue struct {
	Fingerprint   uint64
	Kind          string
	Severity      string
	ExceptionType string
	Title         string
	Culprit       string
	FirstSeen     time.Time
	LastSeen      time.Time
	TotalEvents   uint64
	TotalInstalls uint64
	FirstRelease  string
	LastRelease   string

	// Inside the range.
	Events   uint64
	Installs uint64
	Trend    []uint64
}

var (
	kindNames     = []string{"", "log", "exception", "crash"}
	severityNames = []string{"", "trace", "debug", "info", "warn", "error", "fatal"}
)

func nameOf(names []string, n uint8) string {
	if int(n) < len(names) {
		return names[n]
	}
	return ""
}

// maxIssues bounds how many issues one list considers. An environment with more distinct bugs in
// one range than this has a grouping problem the list cannot solve by being longer.
const maxIssues = 2000

// trendBars is how many bars an issue's sparkline has.
const trendBars = 24

// Issues is every issue that happened in a range, with its figures for the range and its trend.
func Issues(ctx context.Context, conn driver.Conn, scope Scope, r Range) ([]Issue, error) {
	rows, err := conn.Query(bounded(ctx), `
		SELECT fingerprint, sum(events), uniqCombinedMerge(installs)
		FROM log_issue_hours
		WHERE project_seq = ? AND environment_seq = ?
		  AND hour >= toStartOfHour(toDateTime(?, 'UTC')) AND hour < toDateTime(?, 'UTC')
		GROUP BY fingerprint
		ORDER BY sum(events) DESC
		LIMIT `+fmt.Sprint(maxIssues),
		scope.ProjectSeq, scope.EnvironmentSeq,
		r.From.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("read issue hours: %w", err)
	}
	inRange := map[uint64]*Issue{}
	var order []uint64
	for rows.Next() {
		issue := &Issue{}
		if err := rows.Scan(&issue.Fingerprint, &issue.Events, &issue.Installs); err != nil {
			rows.Close()
			return nil, err
		}
		inRange[issue.Fingerprint] = issue
		order = append(order, issue.Fingerprint)
	}
	rows.Close()
	if len(order) == 0 {
		return nil, nil
	}
	if err := describe(ctx, conn, scope, order, inRange); err != nil {
		return nil, err
	}
	if err := trends(ctx, conn, scope, r, order, inRange); err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(order))
	for _, fp := range order {
		if issue := inRange[fp]; issue.Kind != "" {
			out = append(out, *issue)
		}
	}
	return out, nil
}

// OneIssue is an issue as it stands, and what it did inside the range.
func OneIssue(ctx context.Context, conn driver.Conn, scope Scope, r Range, fingerprint uint64) (*Issue, error) {
	issue := &Issue{Fingerprint: fingerprint}
	found := map[uint64]*Issue{fingerprint: issue}
	if err := describe(ctx, conn, scope, []uint64{fingerprint}, found); err != nil {
		return nil, err
	}
	if issue.Kind == "" {
		return nil, ErrNotFound
	}
	err := conn.QueryRow(bounded(ctx), `
		SELECT sum(events), uniqCombinedMerge(installs)
		FROM log_issue_hours
		WHERE project_seq = ? AND environment_seq = ? AND fingerprint = ?
		  AND hour >= toStartOfHour(toDateTime(?, 'UTC')) AND hour < toDateTime(?, 'UTC')`,
		scope.ProjectSeq, scope.EnvironmentSeq, fingerprint,
		r.From.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime)).Scan(&issue.Events, &issue.Installs)
	if err != nil {
		return nil, fmt.Errorf("read an issue's range: %w", err)
	}
	return issue, nil
}

// describe fills in what each issue is from log_issues, merging its partial rows.
func describe(ctx context.Context, conn driver.Conn, scope Scope, fingerprints []uint64, into map[uint64]*Issue) error {
	for start := 0; start < len(fingerprints); start += 500 {
		chunk := fingerprints[start:min(start+500, len(fingerprints))]
		rows, err := conn.Query(bounded(ctx), `
			SELECT fingerprint, max(kind), max(severity), min(first_seen), max(last_seen), sum(events),
				argMaxMerge(exception_type), argMaxMerge(title), argMaxMerge(culprit),
				argMinMerge(first_release), argMaxMerge(last_release), uniqCombinedMerge(installs)
			FROM log_issues
			WHERE project_seq = ? AND environment_seq = ? AND fingerprint IN ?
			GROUP BY fingerprint`, scope.ProjectSeq, scope.EnvironmentSeq, chunk)
		if err != nil {
			return fmt.Errorf("read issues: %w", err)
		}
		for rows.Next() {
			var (
				fp             uint64
				kind, severity uint8
				d              Issue
			)
			if err := rows.Scan(&fp, &kind, &severity, &d.FirstSeen, &d.LastSeen, &d.TotalEvents,
				&d.ExceptionType, &d.Title, &d.Culprit, &d.FirstRelease, &d.LastRelease, &d.TotalInstalls); err != nil {
				rows.Close()
				return err
			}
			issue := into[fp]
			if issue == nil {
				continue
			}
			issue.Kind, issue.Severity = nameOf(kindNames, kind), nameOf(severityNames, severity)
			issue.FirstSeen, issue.LastSeen, issue.TotalEvents = d.FirstSeen, d.LastSeen, d.TotalEvents
			issue.ExceptionType, issue.Title, issue.Culprit = d.ExceptionType, d.Title, d.Culprit
			issue.FirstRelease, issue.LastRelease, issue.TotalInstalls = d.FirstRelease, d.LastRelease, d.TotalInstalls
		}
		rows.Close()
	}
	return nil
}

// trends fills each issue's sparkline: its events in trendBars equal bars across the range.
func trends(ctx context.Context, conn driver.Conn, scope Scope, r Range, fingerprints []uint64, into map[uint64]*Issue) error {
	step := r.To.Sub(r.From) / trendBars
	if step < time.Hour {
		step = time.Hour
	}
	step = step.Truncate(time.Hour)
	from := r.From.Truncate(step)
	bars := int(r.To.Sub(from)/step) + 1
	if bars > trendBars+1 {
		bars = trendBars + 1
	}
	for _, issue := range into {
		issue.Trend = make([]uint64, bars)
	}
	for start := 0; start < len(fingerprints); start += 500 {
		chunk := fingerprints[start:min(start+500, len(fingerprints))]
		rows, err := conn.Query(bounded(ctx), `
			SELECT fingerprint, toStartOfInterval(hour, toIntervalSecond(?)) AS bar, sum(events)
			FROM log_issue_hours
			WHERE project_seq = ? AND environment_seq = ? AND fingerprint IN ?
			  AND hour >= toDateTime(?, 'UTC') AND hour < toDateTime(?, 'UTC')
			GROUP BY fingerprint, bar`,
			int64(step/time.Second), scope.ProjectSeq, scope.EnvironmentSeq, chunk,
			from.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime))
		if err != nil {
			return fmt.Errorf("read issue trends: %w", err)
		}
		for rows.Next() {
			var (
				fp     uint64
				bar    time.Time
				events uint64
			)
			if err := rows.Scan(&fp, &bar, &events); err != nil {
				rows.Close()
				return err
			}
			if issue := into[fp]; issue != nil {
				if i := int(bar.Sub(from) / step); i >= 0 && i < len(issue.Trend) {
					issue.Trend[i] += events
				}
			}
		}
		rows.Close()
	}
	return nil
}

// Point is one bar of a series.
type Point struct {
	At    time.Time
	Value uint64
}

// IssueSeries is an issue's events per bar across a range, at the chart's own bar width but no
// narrower than the hour the table keeps.
func IssueSeries(ctx context.Context, conn driver.Conn, scope Scope, r Range, fingerprint uint64) (time.Duration, []Point, error) {
	step := StepFor(r)
	if step < time.Hour {
		step = time.Hour
	}
	from := r.From.Truncate(step)
	rows, err := conn.Query(bounded(ctx), `
		SELECT toStartOfInterval(hour, toIntervalSecond(?)) AS bar, sum(events)
		FROM log_issue_hours
		WHERE project_seq = ? AND environment_seq = ? AND fingerprint = ?
		  AND hour >= toDateTime(?, 'UTC') AND hour < toDateTime(?, 'UTC')
		GROUP BY bar`,
		int64(step/time.Second), scope.ProjectSeq, scope.EnvironmentSeq, fingerprint,
		from.UTC().Format(time.DateTime), r.To.UTC().Format(time.DateTime))
	if err != nil {
		return step, nil, fmt.Errorf("read an issue's series: %w", err)
	}
	defer rows.Close()
	var points []Point
	index := map[int64]int{}
	for t := from; t.Before(r.To); t = t.Add(step) {
		index[t.Unix()] = len(points)
		points = append(points, Point{At: t})
	}
	for rows.Next() {
		var (
			bar    time.Time
			events uint64
		)
		if err := rows.Scan(&bar, &events); err != nil {
			return step, nil, err
		}
		if i, ok := index[bar.Unix()]; ok {
			points[i].Value = events
		}
	}
	return step, points, rows.Err()
}

// Share is one value's part of an issue's events.
type Share struct {
	Value string
	Count uint64
}

// breakdownColumns are the tags an issue is split by, in the order the page shows them.
var breakdownColumns = []struct{ name, column string }{
	{"release", "app_version"},
	{"platform", "platform"},
	{"os", "os_version"},
	{"device", "device_model"},
	{"country", "country_code"},
}

// Breakdown splits an issue's events in a range by release, platform, OS, device and country, the
// five most common values of each, and says how many events it split: the whole each value is a
// share of, counted by the same query to the millisecond, so a page never shows a remainder that
// is only the difference between two ways of counting. Read from the records, which the
// fingerprint's bloom filter narrows to the granules holding the issue.
func Breakdown(ctx context.Context, conn driver.Conn, scope Scope, r Range, fingerprint uint64) (map[string][]Share, uint64, error) {
	out := map[string][]Share{}
	parts := make([]string, 0, len(breakdownColumns))
	for _, c := range breakdownColumns {
		parts = append(parts, "('"+c.name+"', toString("+c.column+"))")
	}
	rows, err := conn.Query(bounded(ctx), `
		SELECT tag.1 AS name, tag.2 AS value, count() AS n, sum(count()) OVER (PARTITION BY name) AS whole
		FROM logs
		ARRAY JOIN [`+strings.Join(parts, ", ")+`] AS tag
		WHERE project_seq = ? AND environment_seq = ? AND fingerprint = ?
		  AND timestamp >= toDateTime64(?, 3, 'UTC') AND timestamp < toDateTime64(?, 3, 'UTC')
		GROUP BY name, value
		ORDER BY name, n DESC
		LIMIT 5 BY name`,
		scope.ProjectSeq, scope.EnvironmentSeq, fingerprint, at(r.From), at(r.To))
	if err != nil {
		return nil, 0, fmt.Errorf("read an issue's breakdown: %w", err)
	}
	defer rows.Close()
	var total uint64
	for rows.Next() {
		var (
			name, value  string
			count, whole uint64
		)
		if err := rows.Scan(&name, &value, &count, &whole); err != nil {
			return nil, 0, err
		}
		// Every record carries every tag, so each tag's whole is the same number.
		total = whole
		out[name] = append(out[name], Share{Value: value, Count: count})
	}
	return out, total, rows.Err()
}

// ---- storage ---------------------------------------------------------------------------------
//
// Read from log_usage_hourly, which outlives the logs: a month is billed after a one-day retention
// has already removed its records.

// Kept is the log bytes one project holds at a moment, by environment: every hour still inside the
// retention its rows were stamped with and inside the project's retention now, which is the lower
// of the two when somebody has just lowered it. The purge follows; the bill does not wait for it.
func Kept(ctx context.Context, conn driver.Conn, projectSeq uint32, retention uint16, when time.Time) (map[uint16]uint64, error) {
	rows, err := conn.Query(bounded(ctx), `
		SELECT environment_seq, sum(bytes)
		FROM log_usage_hourly
		WHERE project_seq = ?
		  AND hour > toDateTime(?, 'UTC') - toIntervalDay(least(retention_days, ?))
		  AND hour <= toDateTime(?, 'UTC')
		GROUP BY environment_seq`,
		projectSeq, when.UTC().Format(time.DateTime), retention, when.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("read kept log bytes: %w", err)
	}
	defer rows.Close()
	out := map[uint16]uint64{}
	for rows.Next() {
		var (
			env   uint16
			bytes uint64
		)
		if err := rows.Scan(&env, &bytes); err != nil {
			return nil, err
		}
		out[env] = bytes
	}
	return out, rows.Err()
}

// Ingested is one project's log bytes by the day they arrived, for the storage page's chart.
func Ingested(ctx context.Context, conn driver.Conn, projectSeq uint32, from time.Time) ([]Point, error) {
	rows, err := conn.Query(bounded(ctx), `
		SELECT toStartOfDay(hour) AS day, sum(bytes)
		FROM log_usage_hourly
		WHERE project_seq = ? AND hour >= toDateTime(?, 'UTC')
		GROUP BY day
		ORDER BY day`, projectSeq, from.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("read ingested log bytes: %w", err)
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.At, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Usage is one project's reading of both meters.
type Usage struct {
	OrgSeq   uint32
	Kept     uint64 // bytes held at the end of the day
	Ingested uint64 // bytes taken in during the day
}

// DayUsage reads both meters for every project for the day ending at dayEnd: what each took in that
// day, and what each held when it ended. One query serves every project whatever its retention:
// rows come back by the retention they were stamped with and the day they arrived, and the
// project's current retention is applied here.
func DayUsage(ctx context.Context, conn driver.Conn, dayEnd time.Time, retention func(projectSeq uint32) (uint16, bool)) (map[uint32]Usage, error) {
	rows, err := conn.Query(bounded(ctx), `
		SELECT org_seq, project_seq, retention_days, toDate(hour) AS day, sum(bytes)
		FROM log_usage_hourly
		WHERE hour >= toDateTime(?, 'UTC') - INTERVAL 91 DAY AND hour < toDateTime(?, 'UTC')
		GROUP BY org_seq, project_seq, retention_days, day`,
		dayEnd.UTC().Format(time.DateTime), dayEnd.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("read log usage: %w", err)
	}
	defer rows.Close()
	lastDay := dayEnd.AddDate(0, 0, -1)
	out := map[uint32]Usage{}
	for rows.Next() {
		var (
			org, project uint32
			stamped      uint16
			day          time.Time
			bytes        uint64
		)
		if err := rows.Scan(&org, &project, &stamped, &day, &bytes); err != nil {
			return nil, err
		}
		u := out[project]
		u.OrgSeq = org
		if day.Equal(lastDay) {
			u.Ingested += bytes
		}
		// A day's bytes are held on each of the `keep` days starting with the day they arrived,
		// so they count toward the day ending at dayEnd when that day is one of those.
		if current, known := retention(project); known {
			keep := min(stamped, current)
			if !day.Before(dayEnd.AddDate(0, 0, -int(keep))) {
				u.Kept += bytes
			}
		}
		out[project] = u
	}
	return out, rows.Err()
}

// IngestedSince is each organization's log bytes taken in since a moment: the ingestion meter,
// read hour by hour through the month so a charge follows the bytes within the hour.
func IngestedSince(ctx context.Context, conn driver.Conn, from, to time.Time) (map[uint32]uint64, error) {
	rows, err := conn.Query(bounded(ctx), `
		SELECT org_seq, sum(bytes)
		FROM log_usage_hourly
		WHERE hour >= toDateTime(?, 'UTC') AND hour < toDateTime(?, 'UTC')
		GROUP BY org_seq`, from.UTC().Format(time.DateTime), to.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("read ingested log bytes: %w", err)
	}
	defer rows.Close()
	out := map[uint32]uint64{}
	for rows.Next() {
		var (
			org   uint32
			bytes uint64
		)
		if err := rows.Scan(&org, &bytes); err != nil {
			return nil, err
		}
		out[org] = bytes
	}
	return out, rows.Err()
}

// sortedKeys lists a map's keys in order, so a query's text does not depend on map order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
