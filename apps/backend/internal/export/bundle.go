package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// BundleVersion is the layout of the exported document.
//
// Recorded on the first line of every bundle. An export is handed to a studio, who hands it
// to a person; both may read it long after this build is gone, and "what did these fields
// mean when this was written" is a question somebody will actually ask — the same reasoning
// that puts a schema version on every archive manifest (§14.2).
const BundleVersion = 1

// line is one record of the bundle.
//
// Newline-delimited JSON, one object per line, with a `section` naming what the line is.
// Chosen over Parquet — which the archive uses and this code already has — because the
// recipient of a portability request is a person, by way of their studio, and "structured,
// commonly used and machine-readable" describes NDJSON far better than it describes a
// columnar format with a footer.
//
// Typed fields rather than a map, so the documented format is documented in the type system
// and not only in prose.
type line struct {
	Section string `json:"section"`

	Meta     *Meta        `json:"meta,omitempty"`
	Event    *EventRow    `json:"event,omitempty"`
	Device   *DeviceRow   `json:"device,omitempty"`
	Day      *DayRow      `json:"activity_day,omitempty"`
	Purchase *PurchaseRow `json:"purchase,omitempty"`
	Summary  *Summary     `json:"summary,omitempty"`
}

// Meta opens the bundle and says what it is and what it is not.
type Meta struct {
	BundleVersion int       `json:"bundle_version"`
	Request       string    `json:"request"`
	ProjectID     string    `json:"project_id"`
	ActorKey      string    `json:"actor_key"`
	GeneratedAt   time.Time `json:"generated_at"`

	// Limits travel with the data on purpose. §05 is explicit that "saying this plainly is
	// better than producing a partial export labelled as complete", and the only place a
	// caveat cannot be separated from the answer is inside the answer.
	Limits []string `json:"limits"`
}

// EventRow is one raw event as held (§23).
type EventRow struct {
	EventTime  time.Time `json:"event_time"`
	IngestTime time.Time `json:"ingest_time"`
	ReceivedAt time.Time `json:"received_at"`

	EventID   string `json:"event_id"`
	BatchID   string `json:"batch_id"`
	SessionID string `json:"session_id,omitempty"`
	PlayerID  string `json:"player_id,omitempty"`
	InstallID string `json:"install_id"`

	EventName string `json:"event_name"`
	EventType string `json:"event_type"`

	Platform    string `json:"platform,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	AppVersion  string `json:"app_version,omitempty"`
	SDKName     string `json:"sdk_name,omitempty"`
	SDKVersion  string `json:"sdk_version,omitempty"`

	// The lawful basis the event was collected under, exported with it. An event whose
	// consent state is in a different document from the event is an event whose consent
	// state can be argued about (§97).
	ConsentAnalytics     uint8  `json:"consent_analytics"`
	ConsentPolicyVersion string `json:"consent_policy_version,omitempty"`
	ATTStatus            string `json:"att_status,omitempty"`

	PropsString json.RawMessage `json:"props_string,omitempty"`
	PropsInt    json.RawMessage `json:"props_int,omitempty"`
	PropsFloat  json.RawMessage `json:"props_float,omitempty"`
	PropsBool   json.RawMessage `json:"props_bool,omitempty"`

	RevenueMicros int64  `json:"revenue_micros,omitempty"`
	Currency      string `json:"currency,omitempty"`
	PlaytimeMS    uint64 `json:"playtime_ms,omitempty"`
}

// DeviceRow is one device's lifetime record (§29.6).
//
// Derived rather than collected, and exported anyway: it is keyed on a device and holds
// what that device did, which is §59's test for whether something is player-level. An
// export that returned only the events would be returning less than is held.
type DeviceRow struct {
	DeviceID  uint64    `json:"device_id"`
	InstallID string    `json:"install_id"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	CohortDate      string `json:"cohort_date"`
	InstallPlatform string `json:"install_platform,omitempty"`
	InstallCountry  string `json:"install_country,omitempty"`
	FirstAppVersion string `json:"first_app_version,omitempty"`

	PlaytimeMS    uint64 `json:"playtime_ms"`
	RevenueMicros int64  `json:"revenue_micros"`
	Purchases     uint64 `json:"purchases"`
	ActiveDays    uint64 `json:"active_days"`
}

// DayRow is one device-day (§29.7).
type DayRow struct {
	DeviceID     uint64 `json:"device_id"`
	ActivityDate string `json:"activity_date"`

	Sessions      uint64 `json:"sessions"`
	PlaytimeMS    uint64 `json:"playtime_ms"`
	Events        uint64 `json:"events"`
	RevenueMicros int64  `json:"revenue_micros"`
	Purchases     uint64 `json:"purchases"`
}

// PurchaseRow is one verified store transaction (§47).
type PurchaseRow struct {
	Platform           string    `json:"platform"`
	StoreTransactionID string    `json:"store_transaction_id"`
	ProductID          string    `json:"product_id"`
	AmountMicros       int64     `json:"amount_micros"`
	Currency           string    `json:"currency"`
	PurchasedAt        time.Time `json:"purchased_at"`
}

// Summary closes the bundle.
//
// Last rather than first, which makes it the completeness proof: a bundle that was truncated
// by a crash, a cut connection or a full disk has no summary line, so "is this the whole
// answer" is a property of the file rather than a thing to take on trust.
type Summary struct {
	Events       uint64 `json:"events"`
	Devices      uint64 `json:"devices"`
	ActivityDays uint64 `json:"activity_days"`
	Purchases    uint64 `json:"purchases"`
	Rows         uint64 `json:"rows"`
}

// bundle accumulates the document.
//
// In memory, and bounded by MaxRows for exactly that reason: the whole thing is written as
// one object, so a person with more history than the bound allows is refused with a number
// rather than served a truncated file that claims to be complete.
type bundle struct {
	buf  bytes.Buffer
	enc  *json.Encoder
	rows uint64

	counts Summary
}

func newBundle() *bundle {
	b := &bundle{}
	b.enc = json.NewEncoder(&b.buf)
	return b
}

func (b *bundle) write(l line) error {
	if err := b.enc.Encode(l); err != nil {
		return fmt.Errorf("encode %s line: %w", l.Section, err)
	}
	b.rows++
	return nil
}

// rawOrNil drops an empty ClickHouse map rather than exporting `{}` four times per event.
func rawOrNil(s string) json.RawMessage {
	if s == "" || s == "{}" {
		return nil
	}
	return json.RawMessage(s)
}
