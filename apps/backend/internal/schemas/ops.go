package schemas

import (
	"time"

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
