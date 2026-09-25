package schemas

import (
	"strings"
	"time"

	muzak "muzak.dev/framework"
)

// Cloud Logging's pages (ADR 0050): the explorer (a chart and the records under it), one record
// whole, the issues and one issue, and the project's log storage and retention.

// ---- the request ------------------------------------------------------------

// LogsScope is the path every log route shares.
type LogsScope struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
}

func (in *LogsScope) validateScope(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
}

// LogsWindow is a range of time to the millisecond, because a chart's bars are dragged across
// rather than picked from a calendar.
type LogsWindow struct {
	From string `query:"from,omitzero" doc:"RFC 3339, inclusive. Defaults to 24 hours before to"`
	To   string `query:"to,omitzero" doc:"RFC 3339, exclusive. Defaults to now"`
}

// MaxLogWindow is the widest range a log read covers: the longest retention a project may choose.
const MaxLogWindow = 90 * 24 * time.Hour

// Resolve turns the strings into a range, defaulting to the last day.
func (in LogsWindow) Resolve(now time.Time) (from, to time.Time, err error) {
	to = now.UTC()
	if in.To != "" {
		if to, err = time.Parse(time.RFC3339Nano, in.To); err != nil {
			return from, to, muzak.BadRequest("to is not an RFC 3339 time")
		}
	}
	from = to.Add(-24 * time.Hour)
	if in.From != "" {
		if from, err = time.Parse(time.RFC3339Nano, in.From); err != nil {
			return from, to, muzak.BadRequest("from is not an RFC 3339 time")
		}
	}
	if !from.Before(to) {
		return from, to, muzak.BadRequest("from is not before to")
	}
	if to.Sub(from) > MaxLogWindow+time.Hour {
		return from, to, muzak.BadRequest("a range covers at most 90 days")
	}
	return from.UTC(), to.UTC(), nil
}

func (in *LogsWindow) validateWindow(v *muzak.Validation) {
	v.String(&in.From).Trim().MaxLen(40)
	v.String(&in.To).Trim().MaxLen(40)
}

// LogsSearchParams is a range and what the search box holds.
type LogsSearchParams struct {
	LogsScope
	LogsWindow
	Query string `query:"q,omitzero" doc:"Words, \"phrases\" and field:value filters: level:error,fatal logger:Net release:1.4.2 -word attr.region:eu"`
}

func (in *LogsSearchParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateWindow(v)
	v.String(&in.Query).Trim().MaxLen(1000)
}

// LogRecordsParams asks for one page of records.
type LogRecordsParams struct {
	LogsSearchParams
	Cursor string `query:"cursor,omitzero" doc:"The next_cursor of the page before"`
	Limit  int    `query:"limit,omitzero" doc:"Records per page, 1 to 500; 200 when absent"`
}

func (in *LogRecordsParams) Validate(v *muzak.Validation) {
	in.LogsSearchParams.Validate(v)
	v.String(&in.Cursor).Trim().MaxLen(80)
	v.Number(&in.Limit).Between(0, 500)
}

// LogNewerParams asks how many records arrived above the page being read.
type LogNewerParams struct {
	LogsScope
	Since string `query:"since" doc:"RFC 3339: the newest record already shown"`
	Query string `query:"q,omitzero"`
}

func (in *LogNewerParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.Since).Trim().Required().MaxLen(40)
	v.String(&in.Query).Trim().MaxLen(1000)
}

// LogRecordParams names one record, with its time so the read is a lookup.
type LogRecordParams struct {
	LogsScope
	RecordID string `path:"record"`
	At       string `query:"at" doc:"RFC 3339: the record's own time, as the list gave it"`
}

func (in *LogRecordParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.RecordID).Trim().Required().UUID()
	v.String(&in.At).Trim().Required().MaxLen(40)
}

// LogIssuesParams asks for the issues list.
type LogIssuesParams struct {
	LogsScope
	LogsWindow
	Status string `query:"status,omitzero" doc:"unresolved (the default), resolved, ignored or all"`
	Query  string `query:"q,omitzero" doc:"Words the issue's title, type or culprit contains"`
	Sort   string `query:"sort,omitzero" doc:"events (the default), installs, last_seen or first_seen"`
}

func (in *LogIssuesParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateWindow(v)
	v.String(&in.Status).Trim().Lower().OneOf("unresolved", "resolved", "ignored", "all")
	v.String(&in.Query).Trim().MaxLen(200)
	v.String(&in.Sort).Trim().Lower().OneOf("events", "installs", "last_seen", "first_seen")
}

// LogIssueParams names one issue.
type LogIssueParams struct {
	LogsScope
	LogsWindow
	Fingerprint string `path:"issue" doc:"The issue's id"`
}

func (in *LogIssueParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateWindow(v)
	v.String(&in.Fingerprint).Trim().Required().MaxLen(20).Matches(`^[0-9]+$`)
}

// SetLogIssueIn decides what an issue is.
type SetLogIssueIn struct {
	LogsScope
	Fingerprint string `path:"issue"`
	Status      string `json:"status" doc:"unresolved, resolved or ignored"`
}

func (in *SetLogIssueIn) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.Fingerprint).Trim().Required().MaxLen(20).Matches(`^[0-9]+$`)
	v.String(&in.Status).Trim().Lower().Required().OneOf("unresolved", "resolved", "ignored")
}

// LogStorageParams names a project's log storage.
type LogStorageParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
}

func (in *LogStorageParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// LogRetentionChoices are the retentions a studio may pick, in days.
var LogRetentionChoices = []uint16{1, 3, 7, 14, 30, 60, 90}

// SetLogRetentionIn changes how long a project's logs are kept.
type SetLogRetentionIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Days        uint16 `json:"days" doc:"1, 3, 7, 14, 30, 60 or 90"`
}

func (in *SetLogRetentionIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.Days).OneOf(1, 3, 7, 14, 30, 60, 90)
}

// ---- the answers ------------------------------------------------------------

// LogHistogramOut is the chart: one bar per step, each split by level.
type LogHistogramOut struct {
	From        time.Time         `json:"from"`
	To          time.Time         `json:"to"`
	StepSeconds int64             `json:"step_seconds"`
	Buckets     []LogBucketOut    `json:"buckets"`
	Totals      LogLevelCountsOut `json:"totals"`
	Earliest    time.Time         `json:"earliest" doc:"The oldest moment the project's retention still holds"`
}

// LogLevelCountsOut is a count per level.
type LogLevelCountsOut struct {
	Trace uint64 `json:"trace"`
	Debug uint64 `json:"debug"`
	Info  uint64 `json:"info"`
	Warn  uint64 `json:"warn"`
	Error uint64 `json:"error"`
	Fatal uint64 `json:"fatal"`
}

// Add counts n records at a level.
func (c *LogLevelCountsOut) Add(level string, n uint64) {
	switch strings.ToLower(level) {
	case "trace":
		c.Trace += n
	case "debug":
		c.Debug += n
	case "info":
		c.Info += n
	case "warn":
		c.Warn += n
	case "error":
		c.Error += n
	case "fatal":
		c.Fatal += n
	}
}

// LogBucketOut is one bar.
type LogBucketOut struct {
	At time.Time `json:"at"`
	LogLevelCountsOut
}

// LogRecordSummaryOut is one row of the list.
type LogRecordSummaryOut struct {
	ID             string    `json:"id"`
	At             time.Time `json:"at"`
	Level          string    `json:"level"`
	Kind           string    `json:"kind" doc:"log, exception or crash"`
	Logger         string    `json:"logger,omitzero"`
	Message        string    `json:"message,omitzero" doc:"The first 400 bytes"`
	ExceptionType  string    `json:"exception_type,omitzero"`
	ExceptionValue string    `json:"exception_value,omitzero"`
	Culprit        string    `json:"culprit,omitzero"`
	Issue          string    `json:"issue,omitzero" doc:"The issue this record belongs to, when it belongs to one"`
	AppVersion     string    `json:"app_version,omitzero"`
	Platform       string    `json:"platform,omitzero"`
	InstallID      string    `json:"install_id"`
	PlayerID       string    `json:"player_id,omitzero" doc:"Only with player.read"`
}

// LogRecordsOut is one page of records, newest first.
type LogRecordsOut struct {
	Records    []LogRecordSummaryOut `json:"records"`
	NextCursor string                `json:"next_cursor,omitzero" doc:"Absent on the last page"`
}

// LogNewerOut is how many records arrived since the newest one shown.
type LogNewerOut struct {
	Count uint64 `json:"count"`
}

// LogFrameOut is one frame of a stack.
type LogFrameOut struct {
	Function string `json:"function,omitzero"`
	Module   string `json:"module,omitzero"`
	File     string `json:"file,omitzero"`
	Line     int    `json:"line,omitzero"`
	InApp    bool   `json:"in_app"`
}

// LogBreadcrumbOut is one thing that happened before.
type LogBreadcrumbOut struct {
	At       time.Time `json:"at"`
	Category string    `json:"category,omitzero"`
	Message  string    `json:"message,omitzero"`
	Level    string    `json:"level,omitzero"`
}

// LogRecordOut is one record whole.
type LogRecordOut struct {
	LogRecordSummaryOut
	ReceivedAt  time.Time          `json:"received_at"`
	Handled     bool               `json:"handled"`
	Frames      []LogFrameOut      `json:"frames"`
	Breadcrumbs []LogBreadcrumbOut `json:"breadcrumbs"`
	SessionID   string             `json:"session_id,omitzero" doc:"Only with player.read"`
	AppBuild    string             `json:"app_build,omitzero"`
	OSVersion   string             `json:"os_version,omitzero"`
	DeviceModel string             `json:"device_model,omitzero"`
	CountryCode string             `json:"country_code,omitzero"`
	SDK         string             `json:"sdk,omitzero"`
	Attributes  map[string]string  `json:"attributes"`
	Size        uint32             `json:"size" doc:"Billed bytes"`
}

// LogSummaryOut is the explorer's figures, the range against the range before it.
type LogSummaryOut struct {
	Current  LogFiguresOut `json:"current"`
	Previous LogFiguresOut `json:"previous"`
}

// LogFiguresOut is one range's figures.
type LogFiguresOut struct {
	Records          uint64 `json:"records"`
	Warnings         uint64 `json:"warnings"`
	Errors           uint64 `json:"errors" doc:"error and fatal"`
	Exceptions       uint64 `json:"exceptions"`
	Crashes          uint64 `json:"crashes"`
	Bytes            uint64 `json:"bytes"`
	Issues           uint64 `json:"issues" doc:"Issues that happened in the range"`
	NewIssues        uint64 `json:"new_issues" doc:"Issues first seen in the range"`
	AffectedInstalls uint64 `json:"affected_installs" doc:"Installs any issue reached, approximately"`
}

// LogIssueOut is one issue.
type LogIssueOut struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind" doc:"log, exception or crash: the gravest of its records"`
	Level         string    `json:"level"`
	ExceptionType string    `json:"exception_type,omitzero"`
	Title         string    `json:"title"`
	Culprit       string    `json:"culprit,omitzero"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	FirstRelease  string    `json:"first_release,omitzero"`
	LastRelease   string    `json:"last_release,omitzero"`
	TotalEvents   uint64    `json:"total_events" doc:"Across everything retained"`
	TotalInstalls uint64    `json:"total_installs"`
	Events        uint64    `json:"events" doc:"Inside the range"`
	Installs      uint64    `json:"installs" doc:"Inside the range"`
	Trend         []uint64  `json:"trend" doc:"Events across the range in equal bars"`

	Status     string     `json:"status" doc:"unresolved, resolved, ignored or regressed"`
	ResolvedAt *time.Time `json:"resolved_at,omitzero"`
	UpdatedBy  string     `json:"updated_by,omitzero"`
}

// LogIssuesOut is the issues list.
type LogIssuesOut struct {
	Issues    []LogIssueOut `json:"issues"`
	Truncated bool          `json:"truncated" doc:"More issues matched than the list shows"`
}

// LogShareOut is one value's part of an issue's events.
type LogShareOut struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// LogPointOut is one bar of a series.
type LogPointOut struct {
	At    time.Time `json:"at"`
	Value uint64    `json:"value"`
}

// LogIssueDetailOut is one issue with its series, its breakdown and its latest record.
type LogIssueDetailOut struct {
	Issue       LogIssueOut              `json:"issue"`
	StepSeconds int64                    `json:"step_seconds"`
	Series      []LogPointOut            `json:"series"`
	Breakdown   map[string][]LogShareOut `json:"breakdown" doc:"release, platform, os, device and country: the five commonest of each"`
	// BreakdownTotal is the events the breakdown split, counted by the same query: each value's
	// share is of this, so what the five do not cover is exactly the rest.
	BreakdownTotal uint64        `json:"breakdown_total"`
	Latest         *LogRecordOut `json:"latest,omitzero" doc:"The newest record of the issue in the range"`
}

// LogStorageOut is a project's log storage: what it keeps, for how long, and what that costs.
type LogStorageOut struct {
	RetentionDays       uint16     `json:"retention_days"`
	RetentionChoices    []uint16   `json:"retention_choices"`
	RetentionChangedAt  *time.Time `json:"retention_changed_at,omitzero"`
	RetentionPurgeUntil *time.Time `json:"retention_purge_until,omitzero" doc:"While a lowered retention is still being enforced on older records"`

	KeptBytes    uint64           `json:"kept_bytes" doc:"Log bytes held now, every environment"`
	Environments []LogEnvBytesOut `json:"environments"`
	Ingested     []LogPointOut    `json:"ingested" doc:"Bytes by the day they happened, over the last 30 days"`

	// The organization's month so far, as the two meters count it (ADR 0050). Organization-wide,
	// because that is what is billed: every project's logs share one free tier.
	PeriodStart time.Time `json:"period_start"`

	IngestedBytes          uint64 `json:"ingested_bytes" doc:"Log bytes taken in this month, every project"`
	IncludedIngestBytes    uint64 `json:"included_ingest_bytes" doc:"The free tier: bytes a month taken in at no charge"`
	IngestPricePerGBMicros int64  `json:"ingest_price_per_gb_micros" doc:"A gigabyte taken in past the free tier"`

	GBDays                       float64 `json:"gb_days" doc:"Gigabytes kept, times days, through yesterday"`
	GBMonths                     float64 `json:"gb_months" doc:"GB-days over the month's days"`
	IncludedGBMonths             float64 `json:"included_gb_months" doc:"The free tier: GB-months kept at no charge"`
	StoragePricePerGBMonthMicros int64   `json:"storage_price_per_gb_month_micros" doc:"A gigabyte kept a 30-day month past the free tier"`

	ChargedMicros   int64 `json:"charged_micros" doc:"What logs have cost this month so far"`
	ProjectedMicros int64 `json:"projected_micros" doc:"The month at its rate so far"`
}

// LogEnvBytesOut is one environment's kept bytes.
type LogEnvBytesOut struct {
	Environment string `json:"environment"`
	Bytes       uint64 `json:"bytes"`
}

// LogSummaryParams asks for the explorer's figures.
type LogSummaryParams struct {
	LogsScope
	LogsWindow
}

func (in *LogSummaryParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateWindow(v)
}

// LogIssueStateOut is what was decided about an issue.
type LogIssueStateOut struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	ResolvedAt *time.Time `json:"resolved_at,omitzero"`
	UpdatedBy  string     `json:"updated_by,omitzero"`
}
