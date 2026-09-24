package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// The operations console's analytics (project.md §56): ingestion across every tenant, the
// ClickHouse node's health, and storage by table and by project. Behind a signed-in staff member,
// and aggregate only: nothing here names a player.

// IngestionParams asks for the fleet's ingestion over a range.
type IngestionParams struct {
	From     string `query:"from,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to six days before to"`
	To       string `query:"to,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to today (UTC)"`
	Interval string `query:"interval,omitzero" doc:"minute (the last day at most) or hour (default)"`
}

func (in *IngestionParams) Validate(v *muzak.Validation) {
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
	v.String(&in.Interval).Trim().Lower().OneOf("minute", "hour")
}

// IngestionOut is every tenant's ingestion together, and the busiest projects.
type IngestionOut struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Interval string    `json:"interval"`

	// Now is the last five minutes: how fast events are arriving and how far behind the gateway
	// analytics-writer is storing them.
	Now IngestionNowOut `json:"now"`

	Series   []IngestionBucketOut  `json:"series"`
	Projects []IngestionProjectOut `json:"projects" doc:"The hundred busiest projects over the range"`
}

// IngestionNowOut is the pipeline right now.
type IngestionNowOut struct {
	EventsPerMinute float64   `json:"events_per_minute"`
	LagMsAvg        float64   `json:"lag_ms_avg" doc:"From the gateway accepting a row to analytics-writer storing it"`
	LagMsMax        uint32    `json:"lag_ms_max"`
	LastMinute      time.Time `json:"last_minute,omitzero" doc:"The newest minute anything was stored for"`
}

// IngestionBucketOut is one minute or hour across every tenant.
type IngestionBucketOut struct {
	At         time.Time `json:"at"`
	Events     uint64    `json:"events"`
	Bytes      uint64    `json:"bytes" doc:"The metered logical size (§39)"`
	Batches    uint64    `json:"batches" doc:"From a sketch"`
	Players    uint64    `json:"players" doc:"From a sketch"`
	Sessions   uint64    `json:"sessions"`
	Purchases  uint64    `json:"purchases"`
	LateEvents uint64    `json:"late_events" doc:"Accepted more than an hour after they happened: a device's spool draining"`
	LagMsAvg   float64   `json:"lag_ms_avg"`
	LagMsMax   uint32    `json:"lag_ms_max"`
}

// IngestionProjectOut is one project's share of the range.
type IngestionProjectOut struct {
	OrgSeq         uint32  `json:"org_seq"`
	Organization   string  `json:"organization,omitzero"`
	ProjectSeq     uint32  `json:"project_seq"`
	Project        string  `json:"project,omitzero"`
	EnvironmentSeq uint16  `json:"environment_seq"`
	Events         uint64  `json:"events"`
	Bytes          uint64  `json:"bytes"`
	Batches        uint64  `json:"batches"`
	Players        uint64  `json:"players"`
	LateEvents     uint64  `json:"late_events"`
	LagMsAvg       float64 `json:"lag_ms_avg"`
	LagMsMax       uint32  `json:"lag_ms_max"`
}

// ClickHouseHealthOut is the analytics node's state.
type ClickHouseHealthOut struct {
	Version       string    `json:"version"`
	UptimeSeconds uint64    `json:"uptime_seconds"`
	CheckedAt     time.Time `json:"checked_at"`

	Replicas  []ReplicaOut       `json:"replicas"`
	Merges    []MergeOut         `json:"merges"`
	Mutations []MutationOut      `json:"mutations" doc:"Unfinished ones: erasures and cohort-key repairs run as mutations"`
	Tables    []TableOut         `json:"tables" doc:"Active parts by table and disk: which data sits on the hot and the cold volume"`
	Hotspots  []PartitionOut     `json:"hotspots" doc:"The partitions with the most parts, where merges are falling behind"`
	Disks     []DiskOut          `json:"disks"`
	Metrics   map[string]float64 `json:"metrics" doc:"Memory, and queries, merges and mutations running now"`
	Errors    []ServerErrorOut   `json:"errors" doc:"Errors the server raised in the last day"`
}

type ReplicaOut struct {
	Table          string `json:"table"`
	ReadOnly       bool   `json:"read_only"`
	SessionExpired bool   `json:"session_expired"`
	QueueSize      uint32 `json:"queue_size"`
	InsertsInQueue uint32 `json:"inserts_in_queue"`
	MergesInQueue  uint32 `json:"merges_in_queue"`
	AbsoluteDelay  uint64 `json:"absolute_delay_seconds"`
	ActiveReplicas uint32 `json:"active_replicas"`
	TotalReplicas  uint32 `json:"total_replicas"`
}

type MergeOut struct {
	Table      string  `json:"table"`
	Elapsed    float64 `json:"elapsed_seconds"`
	Progress   float64 `json:"progress"`
	Parts      uint64  `json:"parts"`
	Bytes      uint64  `json:"bytes"`
	IsMutation bool    `json:"is_mutation"`
}

type MutationOut struct {
	Table        string    `json:"table"`
	ID           string    `json:"id"`
	Command      string    `json:"command"`
	CreatedAt    time.Time `json:"created_at"`
	PartsToDo    int64     `json:"parts_to_do"`
	LatestFailed string    `json:"latest_fail_reason,omitzero"`
}

type TableOut struct {
	Table            string    `json:"table"`
	Disk             string    `json:"disk"`
	Parts            uint64    `json:"parts"`
	Partitions       uint64    `json:"partitions"`
	Rows             uint64    `json:"rows"`
	BytesOnDisk      uint64    `json:"bytes_on_disk"`
	UncompressedSize uint64    `json:"uncompressed_bytes"`
	LastModified     time.Time `json:"last_modified"`
}

type PartitionOut struct {
	Table     string `json:"table"`
	Partition string `json:"partition"`
	Parts     uint64 `json:"parts"`
}

type DiskOut struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Free  uint64 `json:"free_bytes"`
	Total uint64 `json:"total_bytes"`
}

type ServerErrorOut struct {
	Name    string    `json:"name"`
	Count   uint64    `json:"count"`
	LastAt  time.Time `json:"last_at"`
	Message string    `json:"message"`
}

// StorageParams asks what each project stored over a range.
type StorageParams struct {
	From string `query:"from,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to 29 days before to"`
	To   string `query:"to,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to today (UTC)"`
}

func (in *StorageParams) Validate(v *muzak.Validation) {
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
}

// StorageOut is storage by table on disk, and each project's logical bytes over the range.
type StorageOut struct {
	From     time.Time           `json:"from"`
	To       time.Time           `json:"to"`
	Tables   []TableOut          `json:"tables"`
	Projects []StorageProjectOut `json:"projects" doc:"By metered logical bytes, the largest first"`
}

// StorageProjectOut is one project's logical bytes and events over the range, and its share of
// everything events_raw holds on disk, which is what §39's accounting apportions.
type StorageProjectOut struct {
	OrgSeq         uint32  `json:"org_seq"`
	Organization   string  `json:"organization,omitzero"`
	ProjectSeq     uint32  `json:"project_seq"`
	Project        string  `json:"project,omitzero"`
	EnvironmentSeq uint16  `json:"environment_seq"`
	Events         uint64  `json:"events"`
	Bytes          uint64  `json:"bytes"`
	Share          float64 `json:"share" doc:"Of all logical bytes over the range"`
}

// ---- the worker's jobs ------------------------------------------------------

// JobsParams narrows the retention jobs page.
type JobsParams struct {
	From   string `query:"from,omitzero" doc:"YYYY-MM-DD. Defaults to seven days ago"`
	To     string `query:"to,omitzero" doc:"YYYY-MM-DD. Defaults to today"`
	Job    string `query:"job,omitzero" doc:"Only this job's runs: derivation, erasure, export, archive_expiry, usage_meter or credit_expiry"`
	Failed bool   `query:"failed,omitzero" doc:"Only runs that failed"`
}

func (in *JobsParams) Validate(v *muzak.Validation) {
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
	v.String(&in.Job).Trim().Lower().
		OneOf("", "derivation", "erasure", "export", "archive_expiry", "usage_meter", "credit_expiry")
}

// JobsOut is the worker's jobs: how each has been running, its recent passes, and the requests
// and objects waiting on them. Every failure is listed rather than the first, as the janitor
// reports them.
type JobsOut struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Jobs   []JobSummaryOut `json:"jobs" doc:"One per job, including any that has not run in the range"`
	Recent []JobRunOut     `json:"recent" doc:"The latest two hundred passes, newest first"`

	Erasures ErasureQueueOut `json:"erasures"`
	Exports  ExportQueueOut  `json:"exports"`
	Archive  ArchiveQueueOut `json:"archive"`
}

// JobSummaryOut is how one job has been running over the range.
type JobSummaryOut struct {
	Job           string           `json:"job"`
	Runs          uint64           `json:"runs"`
	Failures      uint64           `json:"failures"`
	LastRunAt     *time.Time       `json:"last_run_at,omitzero"`
	LastOK        bool             `json:"last_ok"`
	LastSuccessAt *time.Time       `json:"last_success_at,omitzero"`
	LastFailureAt *time.Time       `json:"last_failure_at,omitzero"`
	LastError     string           `json:"last_error,omitzero"`
	AvgMs         float64          `json:"avg_ms"`
	MaxMs         int64            `json:"max_ms"`
	LastCounts    map[string]int64 `json:"last_counts,omitzero" doc:"What the latest pass did"`
}

// JobRunOut is one pass of one job.
type JobRunOut struct {
	Job        string           `json:"job"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	Host       string           `json:"host"`
	OK         bool             `json:"ok"`
	Error      string           `json:"error,omitzero"`
	Counts     map[string]int64 `json:"counts,omitzero"`
}

// ErasureQueueOut is §61's requests: how many are in each state, and the ones open or failed. No
// player's identifier: the console needs to know that one is stuck, not whose it is.
type ErasureQueueOut struct {
	ByStatus map[string]int      `json:"by_status"`
	Overdue  int                 `json:"overdue" doc:"Open past the one-month deadline"`
	Open     []ErasureRequestOut `json:"open" doc:"Not completed, oldest first, up to a hundred"`
}

// ErasureRequestOut is one erasure, without whose it is.
type ErasureRequestOut struct {
	ID              uuid.UUID  `json:"id"`
	ProjectUID      uuid.UUID  `json:"project_id"`
	ProjectSeq      uint32     `json:"project_seq"`
	Project         string     `json:"project,omitzero"`
	Status          string     `json:"status"`
	ReceivedAt      time.Time  `json:"received_at"`
	DueAt           time.Time  `json:"due_at"`
	TombstonedAt    *time.Time `json:"tombstoned_at,omitzero"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FailureReason   string     `json:"failure_reason,omitzero"`
	DevicesDeleted  uint32     `json:"devices_deleted"`
	ObjectsRedacted uint32     `json:"objects_redacted"`
}

// ExportQueueOut is §101's requests.
type ExportQueueOut struct {
	ByStatus map[string]int     `json:"by_status"`
	Open     []ExportRequestOut `json:"open" doc:"Received, running or failed, oldest first, up to a hundred"`
}

// ExportRequestOut is one export, without whose it is.
type ExportRequestOut struct {
	ID            uuid.UUID `json:"id"`
	ProjectUID    uuid.UUID `json:"project_id"`
	ProjectSeq    uint32    `json:"project_seq"`
	Project       string    `json:"project,omitzero"`
	Status        string    `json:"status"`
	ReceivedAt    time.Time `json:"received_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	FailureReason string    `json:"failure_reason,omitzero"`
}

// ArchiveQueueOut is §60's expiry: what is due and not yet gone, and what could not be.
type ArchiveQueueOut struct {
	Due      int                 `json:"due" doc:"Durable objects past delete_after: the sweep's backlog"`
	Deleting int                 `json:"deleting" doc:"Marked deleting; a pass stopped between the mark and the delete"`
	Failed   int                 `json:"failed"`
	Failures []ArchiveFailureOut `json:"failures" doc:"Failed objects, newest first, up to a hundred; each can be retried once its cause is fixed"`
}

// ArchiveFailureOut is one archive object the sweep could not expire.
type ArchiveFailureOut struct {
	ID            uuid.UUID `json:"id"`
	ProjectUID    uuid.UUID `json:"project_id"`
	Bucket        string    `json:"bucket"`
	ObjectPath    string    `json:"object_path"`
	DeleteAfter   time.Time `json:"delete_after"`
	SizeBytes     uint64    `json:"size_bytes"`
	FailureReason string    `json:"failure_reason,omitzero"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RetryManifestIn hands a failed archive object back to the sweep.
type RetryManifestIn struct {
	StaffTicket
	Project    string    `path:"project" doc:"The project's id"`
	ManifestID uuid.UUID `path:"manifest_id"`
}

// ---- the queue --------------------------------------------------------------

// QueueParams narrows the queue page.
type QueueParams struct {
	Hours int `query:"hours,omitzero" default:"24" doc:"How far back dead letters are counted, up to a week"`
}

func (in *QueueParams) Validate(v *muzak.Validation) {
	v.Number(&in.Hours).Min(1).Max(168)
}

// QueueOut is how far behind each writer is.
type QueueOut struct {
	Bus       string          `json:"bus" doc:"local (the NDJSON spool) or pubsub"`
	CheckedAt time.Time       `json:"checked_at"`
	Groups    []QueueGroupOut `json:"groups"`
}

// QueueGroupOut is one consumer group: analytics-writer's, archive-writer's or billing-writer's.
type QueueGroupOut struct {
	Group        string           `json:"group"`
	Messages     int64            `json:"messages" doc:"Batches not yet consumed"`
	Bytes        int64            `json:"bytes"`
	OldestUnread *time.Time       `json:"oldest_unread_at,omitzero" doc:"When the gateway accepted the oldest batch not yet consumed; absent when caught up"`
	LagSeconds   int64            `json:"lag_seconds" doc:"How long that batch has waited"`
	DeadLettered int64            `json:"dead_lettered" doc:"Batches parked over the window"`
	Parked       []ParkedBatchOut `json:"parked,omitzero" doc:"The latest parked, newest first; the local bus only"`
	Error        string           `json:"error,omitzero" doc:"Why this group could not be read; the others still are"`
}

// ParkedBatchOut is why one batch was dead-lettered, without its events.
type ParkedBatchOut struct {
	Reason     string    `json:"reason"`
	FailedAt   time.Time `json:"failed_at"`
	Attempts   int       `json:"attempts"`
	DedupKey   string    `json:"dedup_key"`
	ProjectID  string    `json:"project_id"`
	EventCount int       `json:"event_count"`
}

// ---- rate limits ------------------------------------------------------------

// RateLimitsParams narrows the rate-limit page.
type RateLimitsParams struct {
	Hours   int    `query:"hours,omitzero" default:"24" doc:"How far back refusals are read, up to a week"`
	Project string `query:"project,omitzero" doc:"A project id: where its project-wide budgets stand right now"`
}

func (in *RateLimitsParams) Validate(v *muzak.Validation) {
	v.Number(&in.Hours).Min(1).Max(168)
	v.String(&in.Project).Trim().Lower().MaxLen(36)
}

// RateLimitsOut is the limits on the SDK paths and the invitations, and who has been refused
// (docs/rate-limits.md). There are no overrides: §9 of that document says why.
type RateLimitsOut struct {
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	CountedOK bool      `json:"counted_ok" doc:"Valkey answered; when it does not, the SDK paths admit everything (fail open) and invitations are refused"`

	Budgets []BudgetOut         `json:"budgets"`
	Totals  map[string]int64    `json:"totals" doc:"Refusals over the range, by quota"`
	Series  []RefusalHourOut    `json:"series" doc:"Refusals by hour and quota"`
	Top     []RefusalScopeOut   `json:"top" doc:"The fifty projects and organizations refused most, by quota"`
	Project *ProjectCountersOut `json:"project,omitzero"`
}

// BudgetOut is one limit.
type BudgetOut struct {
	Path          string `json:"path" doc:"ingest, config or invitations"`
	Quota         string `json:"quota"`
	Per           string `json:"per" doc:"Whose budget it is"`
	WindowSeconds int64  `json:"window_seconds"`
	Limit         int    `json:"limit"`
	FailOpen      bool   `json:"fail_open" doc:"Admits when Valkey cannot count"`
}

// RefusalHourOut is one hour's refusals for one quota.
type RefusalHourOut struct {
	Hour  time.Time `json:"hour"`
	Quota string    `json:"quota"`
	Count int64     `json:"count"`
}

// RefusalScopeOut is one project's or organization's refusals for one quota.
type RefusalScopeOut struct {
	Quota        string `json:"quota"`
	Scope        string `json:"scope" doc:"A project's id, or an organization's slug for invitations"`
	Project      string `json:"project,omitzero" doc:"The project's slug"`
	Organization string `json:"organization,omitzero" doc:"The organization's slug"`
	Count        int64  `json:"count"`
}

// ProjectCountersOut is where one project's project-wide budgets stand in their current window.
type ProjectCountersOut struct {
	ProjectID string           `json:"project_id"`
	Counters  []LiveCounterOut `json:"counters"`
}

// LiveCounterOut is one counter now.
type LiveCounterOut struct {
	Quota   string `json:"quota"`
	Count   int    `json:"count"`
	Limit   int    `json:"limit"`
	ResetMs int64  `json:"reset_ms" doc:"Until the window ends"`
}
