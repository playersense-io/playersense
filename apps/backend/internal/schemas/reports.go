package schemas

import (
	"encoding/json/jsontext"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// Reports: a graph of sources, steps and outputs, run by hand or on a schedule.

// ReportIn creates or replaces a report.
type ReportIn struct {
	OrgSlug     string         `path:"org"`
	ProjectSlug string         `path:"project"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Environment string         `json:"environment,omitzero" doc:"Whose rows its sources read; production by default"`
	Graph       jsontext.Value `json:"graph" doc:"{nodes, edges}, as the report builder saves it"`
}

func (in *ReportIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(1000)
	v.String(&in.Environment).Trim().Lower().OneOf("", "development", "staging", "production")
}

// ReportParams names one report.
type ReportParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	ReportID    uuid.UUID `path:"report"`
}

func (in *ReportParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// UpdateReportIn replaces a report.
type UpdateReportIn struct {
	ReportParams
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Environment string         `json:"environment,omitzero"`
	Graph       jsontext.Value `json:"graph"`
}

func (in *UpdateReportIn) Validate(v *muzak.Validation) {
	in.ReportParams.Validate(v)
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(1000)
	v.String(&in.Environment).Trim().Lower().OneOf("", "development", "staging", "production")
}

// ReportOut is one report.
type ReportOut struct {
	ID          uuid.UUID      `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Environment string         `json:"environment"`
	Graph       jsontext.Value `json:"graph"`
	NextRunAt   *time.Time     `json:"next_run_at,omitzero" doc:"When a schedule next runs it; absent when none does"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	CreatedBy   string         `json:"created_by,omitzero"`
	UpdatedBy   string         `json:"updated_by,omitzero"`
	LastRun     *ReportRunOut  `json:"last_run,omitzero"`
}

// ReportListOut is a project's reports.
type ReportListOut struct {
	Reports []ReportOut `json:"reports"`
}

// ReportPreviewIn runs a graph once over the project's rows, sending nothing.
type ReportPreviewIn struct {
	OrgSlug     string         `path:"org"`
	ProjectSlug string         `path:"project"`
	Name        string         `json:"name,omitzero" doc:"Filled into a Notify's {{report}}"`
	Environment string         `json:"environment,omitzero"`
	Graph       jsontext.Value `json:"graph"`
}

func (in *ReportPreviewIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Name).Trim().MaxLen(120)
	v.String(&in.Environment).Trim().Lower().OneOf("", "development", "staging", "production")
}

// ReportPreviewOut is every node's result, by node id.
type ReportPreviewOut struct {
	Nodes map[string]NodePreviewOut `json:"nodes"`
}

// NodePreviewOut is what one node produced.
type NodePreviewOut struct {
	Columns []ReportColumnOut `json:"columns"`
	Rows    []map[string]any  `json:"rows" doc:"The first fifty: text, numbers, and times as milliseconds"`
	Total   int               `json:"total"`
	Error   string            `json:"error,omitzero"`
	Notify  *NotifyOutcomeOut `json:"notify,omitzero" doc:"For a Notify: whether it would send, why, and what"`
}

// ReportColumnOut is one column.
type ReportColumnOut struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type" doc:"string, number, money, percent, time, country or platform"`
}

// NotifyOutcomeOut is a Notify's decision.
type NotifyOutcomeOut struct {
	Send     bool     `json:"send"`
	Reason   string   `json:"reason"`
	Title    string   `json:"title"`
	Body     string   `json:"body,omitzero"`
	Severity string   `json:"severity"`
	Lines    []string `json:"lines,omitzero"`
}

// StartRunIn runs a report now.
type StartRunIn struct {
	ReportParams
	Focus string `json:"focus,omitzero" doc:"One Notify's node id: run only it and what it reads"`
	Dry   bool   `json:"dry,omitzero" doc:"Send nothing; say what would have been sent"`
}

func (in *StartRunIn) Validate(v *muzak.Validation) {
	in.ReportParams.Validate(v)
	v.String(&in.Focus).Trim().MaxLen(64)
}

// StartedRunOut is a run just started, and where to follow it.
type StartedRunOut struct {
	Run       ReportRunOut `json:"run"`
	StreamURL string       `json:"stream_url" doc:"Its events, as server-sent events: run_started, node_started, node_finished, node_failed, node_skipped, delivery and run_finished. Opens without an Authorization header for an hour"`
}

// ReportRunParams names one run.
type ReportRunParams struct {
	ReportParams
	RunID uuid.UUID `path:"run"`
}

// ReportRunOut is one run.
type ReportRunOut struct {
	ID          uuid.UUID         `json:"id"`
	Trigger     string            `json:"trigger" doc:"schedule or test"`
	Dry         bool              `json:"dry,omitzero"`
	Focus       string            `json:"focus,omitzero"`
	Status      string            `json:"status" doc:"running, succeeded, partial, failed, quiet or stopped"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  *time.Time        `json:"finished_at,omitzero"`
	Deliveries  []map[string]any  `json:"deliveries"`
	RequestedBy string            `json:"requested_by,omitzero"`
	Error       string            `json:"error,omitzero"`
	Exports     []ReportExportOut `json:"exports,omitzero"`
}

// ReportExportOut is a file a run wrote.
type ReportExportOut struct {
	Node         string    `json:"node"`
	FileName     string    `json:"file_name"`
	Rows         int       `json:"rows"`
	SizeBytes    int64     `json:"size_bytes"`
	ExpiresAt    time.Time `json:"expires_at"`
	DownloadPath string    `json:"download_path"`
}

// ReportRunListOut is a report's latest runs.
type ReportRunListOut struct {
	Runs []ReportRunOut `json:"runs"`
}

// ReportExportParams names one run's file.
type ReportExportParams struct {
	ReportRunParams
	Node string `path:"node"`
}

// RunStreamIn opens a run's event stream.
type RunStreamIn struct {
	RunID uuid.UUID `path:"run"`
	Token string    `query:"token" doc:"The token stream_url carries"`
}

// RunEventOut is one event on a run's stream: the builder's RunEvent.
type RunEventOut = map[string]any
