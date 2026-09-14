// Package ingest normalizes an accepted batch into the rows the writer will store.
//
// Everything here is a cheap, local operation on a buffer already in memory. Nothing
// queries PostgreSQL, nothing touches ClickHouse, and nothing acquires a dependency that
// can make ingestion fail — §3.2's rule, and the reason a 202 can be answered in
// milliseconds.
package ingest

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/protocol"
)

// Limits bound what one batch may contain (§19.1).
//
// These protect PlayerSense from an accidental telemetry explosion, which is a far more
// common failure than a deliberate one: a debug build left logging every frame will
// otherwise arrive as a bill.
type Limits struct {
	MaxEventsPerBatch    int
	MaxPropertiesPerItem int
	MaxPropertyKeyLen    int
	MaxStringValueLen    int

	// SkewPast and SkewFuture bound how far a client clock may disagree with the server
	// before its timestamps are clamped (§19.2).
	SkewPast   time.Duration
	SkewFuture time.Duration
}

// DefaultLimits are the starting values. They want benchmarking against real SDK traffic
// before being called final.
func DefaultLimits() Limits {
	return Limits{
		MaxEventsPerBatch:    1000,
		MaxPropertiesPerItem: 64,
		MaxPropertyKeyLen:    64,
		MaxStringValueLen:    1024,
		SkewPast:             7 * 24 * time.Hour,
		SkewFuture:           time.Hour,
	}
}

// NormalizedEvent is one row, shaped the way events_raw wants it.
type NormalizedEvent struct {
	EventID   string    `json:"event_id"`
	EventName string    `json:"event_name"`
	EventType string    `json:"event_type"`
	EventTime time.Time `json:"event_time"`

	// ClientEventTime is the timestamp as the device reported it, kept when clamping
	// changed it so the correction stays auditable (§19.2).
	ClientEventTime *time.Time `json:"client_event_time,omitzero"`

	SessionID string `json:"session_id,omitzero"`
	PlayerID  string `json:"player_id,omitzero"`
	InstallID string `json:"install_id"`
	Sequence  int64  `json:"sequence,omitzero"`

	PropsString map[string]string  `json:"props_string,omitzero"`
	PropsInt    map[string]int64   `json:"props_int,omitzero"`
	PropsFloat  map[string]float64 `json:"props_float,omitzero"`
	PropsBool   map[string]uint8   `json:"props_bool,omitzero"`

	RevenueMicros int64  `json:"revenue_micros,omitzero"`
	Currency      string `json:"currency,omitzero"`
	PayloadBytes  uint32 `json:"payload_bytes"`
}

// NormalizedBatch is what the bus carries and the writer stores.
type NormalizedBatch struct {
	BatchID    string `json:"batch_id"`
	InstallID  string `json:"install_id"`
	SDKName    string `json:"sdk_name"`
	SDKVersion string `json:"sdk_version"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`

	CountryCode string `json:"country_code,omitzero"`

	ConsentAnalytics     uint8  `json:"consent_analytics"`
	ConsentPolicyVersion string `json:"consent_policy_version,omitzero"`
	ATTStatus            string `json:"att_status,omitzero"`

	ReceivedAt time.Time `json:"received_at"`
	IngestTime time.Time `json:"ingest_time"`

	Events []NormalizedEvent `json:"events"`
}

// Result reports what survived normalization.
type Result struct {
	Batch   *NormalizedBatch
	Dropped int
	Reasons []string
}

var (
	// An event name is a key in a column and a label in a chart, so it is bounded to
	// something both can hold.
	eventNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,96}$`)
	propKeyPattern   = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)

	// Direct identifiers a studio should never have sent (§19.3). This is a blunt
	// instrument and will not catch everything; its purpose is to stop the common
	// accident, not to make the platform's guarantees depend on it.
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-zA-Z]{2,}$`)
	phonePattern = regexp.MustCompile(`^\+[0-9]{9,15}$`)
	cardPattern  = regexp.MustCompile(`^[0-9]{13,19}$`)
)

const maxReasons = 8

// Normalize turns an accepted envelope into rows, dropping what it must and saying why.
//
// A malformed event is dropped rather than failing the batch. An SDK that sends one bad
// event among five hundred good ones would otherwise retry the whole batch forever, and
// the good events would never arrive.
func Normalize(batch *protocol.Batch, tenant *core.Tenant, clientIP string, now time.Time, limits Limits) (*Result, error) {
	if len(batch.Events) > limits.MaxEventsPerBatch {
		return nil, fmt.Errorf("a batch may carry at most %d events, got %d",
			limits.MaxEventsPerBatch, len(batch.Events))
	}

	out := &NormalizedBatch{
		BatchID:    batch.BatchID,
		InstallID:  batch.InstallID,
		SDKName:    truncate(batch.SDK.Name, 32),
		SDKVersion: truncate(batch.SDK.Version, 32),
		AppVersion: truncate(batch.App.Version, 32),
		Platform:   strings.ToLower(truncate(batch.Device.Platform, 16)),

		// Coarse only: a country, never an address. §100 forbids fingerprinting, and the
		// address itself is not retained.
		CountryCode: countryFromIP(clientIP),

		ConsentPolicyVersion: truncate(batch.Consent.PolicyVersion, 32),
		ATTStatus:            truncate(batch.Consent.ATTStatus, 24),

		ReceivedAt: now,
		IngestTime: now,
		Events:     make([]NormalizedEvent, 0, len(batch.Events)),
	}
	if batch.Consent.Analytics {
		out.ConsentAnalytics = 1
	}

	result := &Result{Batch: out}

	for i := range batch.Events {
		ev := &batch.Events[i]
		normalized, err := normalizeEvent(ev, batch, now, limits)
		if err != nil {
			result.Dropped++
			if len(result.Reasons) < maxReasons {
				result.Reasons = append(result.Reasons, fmt.Sprintf("event %d: %v", i, err))
			}
			continue
		}
		out.Events = append(out.Events, *normalized)
	}
	return result, nil
}

func normalizeEvent(ev *protocol.Event, batch *protocol.Batch, now time.Time, limits Limits) (*NormalizedEvent, error) {
	if ev.EventID == "" {
		return nil, fmt.Errorf("event_id is required")
	}
	if !eventNamePattern.MatchString(ev.EventName) {
		return nil, fmt.Errorf("event_name %q is not an accepted name", truncate(ev.EventName, 32))
	}

	out := &NormalizedEvent{
		EventID:       ev.EventID,
		EventName:     ev.EventName,
		EventType:     eventType(ev),
		SessionID:     truncate(ev.SessionID, 128),
		PlayerID:      truncate(ev.PlayerID, 128),
		InstallID:     batch.InstallID,
		Sequence:      ev.Sequence,
		RevenueMicros: ev.RevenueMicros,
		Currency:      strings.ToUpper(truncate(ev.Currency, 3)),
	}

	// §19.2: a device clock cannot be trusted. A wrong one produces events dated 1970 or
	// years ahead, and those propagate into a 1970 cohort with a day-offset of twenty
	// thousand. Clamp, and keep the original so the correction is auditable.
	eventTime := ev.EventTime
	if eventTime.IsZero() {
		eventTime = now
	}
	floor, ceil := now.Add(-limits.SkewPast), now.Add(limits.SkewFuture)
	if eventTime.Before(floor) || eventTime.After(ceil) {
		original := eventTime
		out.ClientEventTime = &original
		if eventTime.Before(floor) {
			eventTime = floor
		} else {
			eventTime = ceil
		}
	}
	out.EventTime = eventTime.UTC()

	if len(ev.Properties) > limits.MaxPropertiesPerItem {
		return nil, fmt.Errorf("an event may carry at most %d properties, got %d",
			limits.MaxPropertiesPerItem, len(ev.Properties))
	}
	if err := splitProperties(out, ev.Properties, limits); err != nil {
		return nil, err
	}

	out.PayloadBytes = uint32(estimateBytes(out))
	return out, nil
}

// splitProperties sorts a property bag into typed maps (§22).
//
// Typed maps rather than one JSON blob, because a filter on a number should compare
// numbers. A value whose type has no map is dropped rather than stringified, so nothing
// silently changes meaning between what the SDK sent and what a chart reads.
func splitProperties(out *NormalizedEvent, props map[string]any, limits Limits) error {
	for key, value := range props {
		if len(key) > limits.MaxPropertyKeyLen {
			return fmt.Errorf("property key %q is longer than %d characters",
				truncate(key, 24), limits.MaxPropertyKeyLen)
		}
		if !propKeyPattern.MatchString(key) {
			return fmt.Errorf("property key %q is not an accepted name", truncate(key, 24))
		}

		switch v := value.(type) {
		case string:
			if looksLikeDirectPII(v) {
				// §19.3: refuse rather than store. A studio that sends an email address
				// as a property makes PlayerSense a processor of data neither party
				// intended it to hold, and neither notices until it matters.
				return fmt.Errorf("property %q looks like direct personal data and was refused", key)
			}
			set(&out.PropsString, key, truncate(v, limits.MaxStringValueLen))
		case bool:
			var b uint8
			if v {
				b = 1
			}
			set(&out.PropsBool, key, b)
		case float64:
			// JSON has one number type, so an integral value is stored as one.
			if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
				set(&out.PropsInt, key, int64(v))
			} else {
				set(&out.PropsFloat, key, v)
			}
		case int64:
			set(&out.PropsInt, key, v)
		case nil:
			// An absent property and a null one mean the same thing here.
		default:
			return fmt.Errorf("property %q has a type that cannot be stored", key)
		}
	}
	return nil
}

func looksLikeDirectPII(v string) bool {
	s := strings.TrimSpace(v)
	switch {
	case emailPattern.MatchString(s):
		return true
	case phonePattern.MatchString(s):
		return true
	case cardPattern.MatchString(strings.ReplaceAll(s, " ", "")):
		return true
	}
	return false
}

func eventType(ev *protocol.Event) string {
	switch {
	case ev.RevenueMicros > 0:
		return "purchase"
	case strings.HasPrefix(ev.EventName, "session_"):
		return "session"
	default:
		return "custom"
	}
}

func set[T any](m *map[string]T, key string, value T) {
	if *m == nil {
		*m = make(map[string]T, 4)
	}
	(*m)[key] = value
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// estimateBytes is what the storage meter counts (§39). It is the normalized logical
// size rather than the compressed one, which is the figure §39 says to document.
func estimateBytes(e *NormalizedEvent) int {
	n := len(e.EventID) + len(e.EventName) + len(e.EventType) +
		len(e.SessionID) + len(e.PlayerID) + len(e.InstallID) + 32
	for k, v := range e.PropsString {
		n += len(k) + len(v)
	}
	n += (len(e.PropsInt) + len(e.PropsFloat) + len(e.PropsBool)) * 16
	return n
}

// countryFromIP is the coarse network metadata §19 step 9 allows.
//
// A real deployment resolves this from a geo database or from the load balancer's own
// header. It deliberately returns a country and never the address: §100's identifier
// policy, and an address is not retained anywhere in this path.
func countryFromIP(string) string { return "" }
