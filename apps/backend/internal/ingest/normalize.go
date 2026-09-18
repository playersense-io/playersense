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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

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
	MaxContextFrames     int
	MaxContextKeys       int
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
		MaxEventsPerBatch: 1000,
		// A frame per event would be a context change between every pair of events, which
		// is not a thing a game does; it is a client with a bug in its batching.
		MaxContextFrames: 64,
		// §context: the SDK bounds a scope at 64 keys, and the merged view of two scopes
		// cannot exceed both.
		MaxContextKeys:       128,
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

	if err := checkContext(batch.Context, limits); err != nil {
		return nil, err
	}

	// The context as it stands at the event being read, advanced by the timeline.
	//
	// The loop index is the event's position **in the batch as sent**, and that is the index
	// the frames are keyed on. Dropping a malformed event must therefore not shift it — which
	// is why the cursor advances inside this loop rather than over `out.Events`. Get that
	// wrong and every event after a dropped one silently inherits somebody else's context,
	// which is a corruption rather than a loss and nothing would report it.
	context := map[string]any{}
	frame := 0

	for i := range batch.Events {
		for frame < len(batch.Context) && batch.Context[frame].At <= i {
			applyFrame(context, batch.Context[frame])
			frame++
		}

		ev := &batch.Events[i]
		normalized, err := normalizeEvent(ev, batch, i, context, now, limits)
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

// checkContext bounds the timeline before any of it is applied.
//
// A whole-batch refusal rather than a per-event one, because a malformed timeline is not a
// property of any single event: it is a client that cannot be trusted about which context
// belongs to what, and expanding it would attribute real events to invented state.
func checkContext(frames []protocol.ContextFrame, limits Limits) error {
	if len(frames) > limits.MaxContextFrames {
		return fmt.Errorf("a batch may carry at most %d context frames, got %d",
			limits.MaxContextFrames, len(frames))
	}
	last := -1
	for i, f := range frames {
		if f.At < 0 {
			return fmt.Errorf("context frame %d applies from a negative index", i)
		}
		// Ascending, so one pass over the events resolves the whole timeline. A frame out
		// of order would silently never be applied.
		if f.At < last {
			return fmt.Errorf("context frame %d applies from index %d, before frame %d's %d",
				i, f.At, i-1, last)
		}
		last = f.At
		if len(f.Values)+len(f.Set) > limits.MaxContextKeys {
			return fmt.Errorf("context frame %d carries more than %d keys",
				i, limits.MaxContextKeys)
		}
	}
	return nil
}

// applyFrame advances the running context. Values replaces, then Set applies, then Unset
// removes — the order the envelope documents, so a frame carrying more than one is
// unambiguous.
func applyFrame(context map[string]any, frame protocol.ContextFrame) {
	if frame.Values != nil {
		clear(context)
		for key, value := range frame.Values {
			context[key] = value
		}
	}
	for key, value := range frame.Set {
		context[key] = value
	}
	for _, key := range frame.Unset {
		delete(context, key)
	}
}

// contextPrefix marks a property that came from the studio's context rather than from the
// event itself.
//
// Prefixed rather than merged flat, so an event named property and a context key of the same
// name cannot collide — and so a reader can always tell what described the occurrence from
// what described the player at the time.
const contextPrefix = "ctx."

func normalizeEvent(ev *protocol.Event, batch *protocol.Batch, index int, context map[string]any, now time.Time, limits Limits) (*NormalizedEvent, error) {
	id, err := resolveEventID(ev.EventID, batch.BatchID, index)
	if err != nil {
		return nil, err
	}
	if !eventNamePattern.MatchString(ev.EventName) {
		return nil, fmt.Errorf("event_name %q is not an accepted name", truncate(ev.EventName, 32))
	}

	out := &NormalizedEvent{
		EventID:       id,
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

	// Metered **before** the context is expanded, and the order is the whole point.
	//
	// §39 bills on normalized logical bytes, and the studio sent the context once for the
	// batch. Counting it again on every event would bill them two hundred and fifty times
	// for something they transmitted once — which is precisely the cost the timeline exists
	// to avoid, reintroduced at the meter.
	out.PayloadBytes = uint32(estimateBytes(out))

	applyContext(out, context, limits)
	return out, nil
}

// applyContext materializes the studio's context onto one event (§context frames).
//
// This is what keeps §29.1 intact. The row ClickHouse stores is fully denormalized, so a
// breakdown by `rank` is a GROUP BY over one table and never a join to reconstruct what the
// player's rank was at the time.
//
// An event's own property wins a collision, though the prefix makes one all but impossible:
// a property describes the occurrence and is the more specific statement.
func applyContext(out *NormalizedEvent, context map[string]any, limits Limits) {
	for key, value := range context {
		if len(key) > limits.MaxPropertyKeyLen {
			continue
		}
		prefixed := contextPrefix + key
		switch v := value.(type) {
		case string:
			setIfAbsent(&out.PropsString, prefixed, truncate(v, limits.MaxStringValueLen))
		case bool:
			setIfAbsent(&out.PropsBool, prefixed, boolToUint8(v))
		case float64:
			// JSON has one number type, so an integral value is an integer. The same rule
			// splitProperties applies, and the two must not disagree or a key would land in
			// one map from an event and the other from the context.
			if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
				setIfAbsent(&out.PropsInt, prefixed, int64(v))
			} else {
				setIfAbsent(&out.PropsFloat, prefixed, v)
			}
		case int64:
			setIfAbsent(&out.PropsInt, prefixed, v)
		}
	}
}

// setIfAbsent writes unless the event already said something under that name.
func setIfAbsent[T any](m *map[string]T, key string, value T) {
	if *m == nil {
		*m = make(map[string]T, 4)
	}
	if _, taken := (*m)[key]; !taken {
		(*m)[key] = value
	}
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
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

// eventIDNamespace names the derivation, so an id produced here cannot collide with a v4
// UUID a client generated or with a v5 UUID anything else derives.
var eventIDNamespace = uuid.MustParse("80049b13-30ea-4b53-9718-71a1ac5add36")

// eventID takes the client's identifier, or derives one.
//
// Derived from the batch and the event's position in it, which is unique by construction
// and stable across a redelivery — because §18 requires a batch to be immutable once
// created, so the same events arrive in the same order under the same batch_id however many
// times it is retried. That is exactly the property §21's read-side deduplication needs, and
// it is the property a client-generated UUID was buying at a cost of sixteen incompressible
// bytes an event.
//
// A client-supplied id is used as given and must be a UUID, because that is the column's
// type. Rejecting it per event rather than per batch keeps §19's promise that one bad event
// does not fail the batch around it.
func resolveEventID(supplied, batchID string, index int) (string, error) {
	if supplied == "" {
		return uuid.NewSHA1(eventIDNamespace,
			[]byte(batchID+"\x00"+strconv.Itoa(index))).String(), nil
	}
	if _, err := uuid.Parse(supplied); err != nil {
		return "", fmt.Errorf("event_id %q is not a uuid", truncate(supplied, 40))
	}
	return supplied, nil
}

// ExposureEventName is what an SDK records when a player reads a feature flag (§44).
//
// Matched exactly rather than by prefix: the other two rules here are prefix and amount, both
// of which a studio can trip by accident, and an event named `feature_exposure_v2` is a
// studio's own event rather than one of ours.
const ExposureEventName = "feature_exposure"

func eventType(ev *protocol.Event) string {
	switch {
	case ev.RevenueMicros > 0:
		return "purchase"
	case strings.HasPrefix(ev.EventName, "session_"):
		return "session"
	case ev.EventName == ExposureEventName:
		// Its own type rather than "custom", and the column is LowCardinality(String) so it
		// costs nothing to add. It earns the place by how it is read: "which players saw
		// variant B, and what did they do next" scans a type rather than filtering a name
		// out of every custom event a game sends, which at §4.2's volumes is the difference
		// between a dashboard and a warehouse job.
		return "exposure"
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
