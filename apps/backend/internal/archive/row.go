// Package archive writes the cold, canonical copy of accepted events (project.md §10.4,
// §14).
//
// ClickHouse holds a bounded hot window (§33); this holds everything, in an open format, in
// the cell's own region. Three things depend on it and none of them is the dashboard:
//
//   - §33/§59, where a customer's paid retention outlives the local hot window;
//   - §34's cold queries and §101's data-subject exports, which read objects rather than
//     rows;
//   - §104's breach scoping, where "whose data, and how much" has to be a query.
//
// It consumes the same stream as analytics-writer through its own subscription (§20), so a
// slow archive cannot hold storage back and a broken archive cannot lose events that
// storage already took.
package archive

import "time"

// SchemaVersion is the archive row layout this build writes.
//
// Recorded on every manifest so a reader years from now can tell which shape an object
// holds without opening it. The archive is the copy that outlives everything else, which
// makes "what did this mean when it was written" a question somebody will actually ask.
const SchemaVersion = 1

// Row is one event as the archive stores it.
//
// Flat, and deliberately close to events_raw rather than to the SDK envelope: the archive is
// the canonical raw copy, so it has to be able to rebuild the table (§35's "reprocessing").
// The identity columns ClickHouse materializes — device_key, device_id, actor_key — are
// omitted because they are derivable, and a stored copy of a derived column is a second
// definition waiting to disagree with the first.
//
// Times are stored as TIMESTAMP(MILLIS) rather than as strings or epochs, so an outside
// reader — the portability §35 wants — gets real timestamps without a convention to learn.
type Row struct {
	OrgSeq         uint32 `parquet:"org_seq"`
	ProjectSeq     uint32 `parquet:"project_seq"`
	EnvironmentSeq uint32 `parquet:"environment_seq"`

	OrgID         string `parquet:"org_id"`
	ProjectID     string `parquet:"project_id"`
	EnvironmentID string `parquet:"environment_id"`

	RetentionTier uint32 `parquet:"retention_tier"`
	PolicyTier    string `parquet:"policy_tier"`

	EventTime  time.Time `parquet:"event_time,timestamp(millisecond)"`
	IngestTime time.Time `parquet:"ingest_time,timestamp(millisecond)"`
	ReceivedAt time.Time `parquet:"received_at,timestamp(millisecond)"`

	// ClientEventTime is present only where §19.2's clamp changed the timestamp, which is
	// what keeps the correction auditable in the copy that outlives the hot window.
	ClientEventTime *time.Time `parquet:"client_event_time,optional,timestamp(millisecond)"`

	EventID   string `parquet:"event_id"`
	BatchID   string `parquet:"batch_id"`
	SessionID string `parquet:"session_id"`
	PlayerID  string `parquet:"player_id"`
	InstallID string `parquet:"install_id"`
	Sequence  int64  `parquet:"sequence"`

	EventName string `parquet:"event_name"`
	EventType string `parquet:"event_type"`

	Platform    string `parquet:"platform"`
	CountryCode string `parquet:"country_code"`
	AppVersion  string `parquet:"app_version"`
	SDKName     string `parquet:"sdk_name"`
	SDKVersion  string `parquet:"sdk_version"`

	// The collection basis, per row, because §97 requires it to survive into the archive.
	// An event whose lawful basis is in a different file from the event is an event whose
	// lawful basis can be lost.
	ConsentAnalytics     uint32 `parquet:"consent_analytics"`
	ConsentPolicyVersion string `parquet:"consent_policy_version"`
	ATTStatus            string `parquet:"att_status"`

	PropsString map[string]string  `parquet:"props_string"`
	PropsInt    map[string]int64   `parquet:"props_int"`
	PropsFloat  map[string]float64 `parquet:"props_float"`
	PropsBool   map[string]uint32  `parquet:"props_bool"`

	RevenueMicros int64  `parquet:"revenue_micros"`
	Currency      string `parquet:"currency"`
	PayloadBytes  uint32 `parquet:"payload_bytes"`
}
