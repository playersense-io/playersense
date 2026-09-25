package ingest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/protocol"
)

// Cloud Logging's half of the gateway (ADR 0050): the same cheap, bounded work AcceptBatch does
// for events, for the game's logs, exceptions and crashes.
//
// Nothing is refused for being long. A log line cut at eight kilobytes is still the log line
// somebody was looking for, and a stack cut to its innermost frames still says where it broke;
// refusing either would lose the one record that explained the crash. So text is truncated and
// counted, and only a record with no usable content at all is dropped.

// LogLimits bounds one log batch. Every bound here is a bound on what one record can cost the
// studio in storage, so they are generous for real logs and short of what abuse wants.
type LogLimits struct {
	MaxRecordsPerBatch int
	MaxMessageBytes    int
	MaxExceptionBytes  int
	MaxFrames          int
	MaxBreadcrumbs     int
	MaxAttributes      int
	MaxAttributeKey    int
	MaxAttributeValue  int

	// A crash is written to disk as it happens and sent on the next launch, which can be days
	// later; so a log reaches further back than an event does before its clock is doubted.
	SkewPast time.Duration
}

// DefaultLogLimits are the production bounds.
func DefaultLogLimits() LogLimits {
	return LogLimits{
		MaxRecordsPerBatch: 1000,
		MaxMessageBytes:    8 << 10,
		MaxExceptionBytes:  4 << 10,
		MaxFrames:          128,
		MaxBreadcrumbs:     100,
		MaxAttributes:      32,
		MaxAttributeKey:    64,
		MaxAttributeValue:  1 << 10,
		SkewPast:           30 * 24 * time.Hour,
	}
}

// Severities, in order. The dashboard filters "warn and above", so the order is part of the
// contract.
var severities = map[string]string{
	"trace": "trace", "verbose": "trace",
	"debug": "debug",
	"info":  "info", "information": "info", "log": "info",
	"warn": "warn", "warning": "warn",
	"error": "error", "err": "error", "exception": "error", "assert": "error",
	"fatal": "fatal", "critical": "fatal",
}

// NormalizedLogRecord is one row of `logs`, less what the writer adds (the fingerprint, the
// culprit) and what travels on the message (the tenant).
type NormalizedLogRecord struct {
	RecordID string    `json:"record_id"`
	Time     time.Time `json:"time"`
	Severity string    `json:"severity"`
	Kind     string    `json:"kind"`
	Logger   string    `json:"logger,omitzero"`
	Message  string    `json:"message,omitzero"`

	SessionID string `json:"session_id,omitzero"`
	PlayerID  string `json:"player_id,omitzero"`

	ExceptionType  string                `json:"exception_type,omitzero"`
	ExceptionValue string                `json:"exception_value,omitzero"`
	Handled        bool                  `json:"handled,omitzero"`
	Frames         []protocol.StackFrame `json:"frames,omitzero"`
	Breadcrumbs    []protocol.Breadcrumb `json:"breadcrumbs,omitzero"`
	Fingerprint    []string              `json:"fingerprint,omitzero"`

	Attributes map[string]string `json:"attributes,omitzero"`

	// Size is what the record is billed as: its content's bytes as sent, uncompressed.
	Size uint32 `json:"size"`
}

// NormalizedLogBatch is what the gateway publishes for a log batch.
type NormalizedLogBatch struct {
	BatchID     string    `json:"batch_id"`
	InstallID   string    `json:"install_id"`
	SDKName     string    `json:"sdk_name"`
	SDKVersion  string    `json:"sdk_version"`
	AppVersion  string    `json:"app_version"`
	AppBuild    string    `json:"app_build,omitzero"`
	Platform    string    `json:"platform"`
	OSVersion   string    `json:"os_version,omitzero"`
	DeviceModel string    `json:"device_model,omitzero"`
	CountryCode string    `json:"country_code,omitzero"`
	ReceivedAt  time.Time `json:"received_at"`

	Records []NormalizedLogRecord `json:"records"`
}

// LogResult is what normalization kept, and why it dropped the rest.
type LogResult struct {
	Batch   *NormalizedLogBatch
	Bytes   uint64
	Dropped int
	Reasons []string
}

// recordIDNamespace derives a record's id from its batch, so a retried batch lands on the same
// ids and the writer's deduplication has something to hold on to.
var recordIDNamespace = uuid.MustParse("5b0f3f1e-8a0a-4d55-9c1f-1d2b6c1e0a50")

// NormalizeLogs bounds, cleans and sizes a log batch.
//
// A kids_strict project's player_id is removed here as well as its install id being replaced by
// the handler: §98's promise is about every row this product stores, and a log line is a row.
func NormalizeLogs(batch *protocol.LogBatch, tenant *core.Tenant, clientIP string, now time.Time, limits LogLimits) (*LogResult, error) {
	if len(batch.Records) > limits.MaxRecordsPerBatch {
		return nil, fmt.Errorf("a batch may carry at most %d records, got %d",
			limits.MaxRecordsPerBatch, len(batch.Records))
	}

	out := &NormalizedLogBatch{
		BatchID:     batch.BatchID,
		InstallID:   batch.InstallID,
		SDKName:     truncate(batch.SDK.Name, 32),
		SDKVersion:  truncate(batch.SDK.Version, 32),
		AppVersion:  truncate(batch.App.Version, 32),
		AppBuild:    truncate(batch.App.Build, 32),
		Platform:    strings.ToLower(truncate(batch.Device.Platform, 16)),
		OSVersion:   truncate(batch.Device.OSVersion, 64),
		DeviceModel: truncate(batch.Device.Model, 64),
		CountryCode: countryFromIP(clientIP),
		ReceivedAt:  now,
		Records:     make([]NormalizedLogRecord, 0, len(batch.Records)),
	}
	result := &LogResult{Batch: out}
	strict := tenant != nil && tenant.PolicyTier == "kids_strict"

	for i := range batch.Records {
		record, err := normalizeRecord(&batch.Records[i], batch.BatchID, i, now, limits)
		if err != nil {
			result.Dropped++
			if len(result.Reasons) < maxReasons {
				result.Reasons = append(result.Reasons, fmt.Sprintf("record %d: %v", i, err))
			}
			continue
		}
		if strict {
			record.PlayerID = ""
		}
		result.Bytes += uint64(record.Size)
		out.Records = append(out.Records, *record)
	}
	return result, nil
}

func normalizeRecord(in *protocol.LogRecord, batchID string, index int, now time.Time, limits LogLimits) (*NormalizedLogRecord, error) {
	severity, known := severities[strings.ToLower(strings.TrimSpace(in.Level))]
	if !known {
		return nil, fmt.Errorf("level %q is not trace, debug, info, warn, error or fatal", truncate(in.Level, 16))
	}

	id := in.RecordID
	if id == "" {
		id = uuid.NewSHA1(recordIDNamespace, []byte(batchID+"\x00"+strconv.Itoa(index))).String()
	} else if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("record_id %q is not a uuid", truncate(id, 40))
	}

	// Never in the future, and never older than a crash could plausibly wait to be sent. A
	// clock outside that is pinned to the edge rather than dropping the record: the line is
	// still worth reading, only its position on the chart is doubtful.
	at := in.Time
	if at.IsZero() || at.After(now) {
		at = now
	}
	if floor := now.Add(-limits.SkewPast); at.Before(floor) {
		at = floor
	}

	out := &NormalizedLogRecord{
		RecordID:  id,
		Time:      at.UTC(),
		Severity:  severity,
		Kind:      "log",
		Logger:    clip(strings.TrimSpace(in.Logger), 128),
		Message:   Scrub(clip(in.Message, limits.MaxMessageBytes)),
		SessionID: clip(in.SessionID, 64),
		PlayerID:  clip(strings.TrimSpace(in.PlayerID), 128),
	}

	if ex := in.Exception; ex != nil {
		out.Kind = "exception"
		out.ExceptionType = clip(strings.TrimSpace(ex.Type), 256)
		out.ExceptionValue = Scrub(clip(ex.Value, limits.MaxExceptionBytes))
		out.Handled = ex.Handled
		frames := ex.Frames
		// The innermost frames are the ones that say where it broke, so a stack too deep keeps
		// its end rather than its beginning.
		if len(frames) > limits.MaxFrames {
			frames = frames[len(frames)-limits.MaxFrames:]
		}
		out.Frames = make([]protocol.StackFrame, len(frames))
		for i, f := range frames {
			out.Frames[i] = protocol.StackFrame{
				Function: clip(f.Function, 256), Module: clip(f.Module, 256),
				File: Scrub(clip(f.File, 256)), Line: max(f.Line, 0), InApp: f.InApp,
			}
		}
	}
	if in.Crash {
		out.Kind = "crash"
		out.Handled = false
		if severity != "fatal" {
			out.Severity = "fatal"
		}
	}
	// An error carries the trail that led to it whether or not it came with an exception: a
	// Debug.LogError is often the only report a failure gets.
	if (out.Kind != "log" || out.Severity == "error" || out.Severity == "fatal") && len(in.Breadcrumbs) > 0 {
		crumbs := in.Breadcrumbs
		if len(crumbs) > limits.MaxBreadcrumbs {
			crumbs = crumbs[len(crumbs)-limits.MaxBreadcrumbs:]
		}
		out.Breadcrumbs = make([]protocol.Breadcrumb, len(crumbs))
		for i, b := range crumbs {
			out.Breadcrumbs[i] = protocol.Breadcrumb{
				At: b.At.UTC(), Category: clip(b.Category, 64),
				Message: Scrub(clip(b.Message, 512)), Level: clip(b.Level, 16),
			}
		}
	}
	if len(in.Fingerprint) > 0 {
		for _, part := range in.Fingerprint[:min(len(in.Fingerprint), 8)] {
			out.Fingerprint = append(out.Fingerprint, clip(part, 256))
		}
	}

	if len(in.Attributes) > 0 {
		keys := make([]string, 0, len(in.Attributes))
		for key := range in.Attributes {
			if key != "" && len(key) <= limits.MaxAttributeKey && propKeyPattern.MatchString(key) {
				keys = append(keys, key)
			}
		}
		// Sorted before the cut, so which attributes survive a batch with too many does not
		// depend on map order.
		sort.Strings(keys)
		if len(keys) > limits.MaxAttributes {
			keys = keys[:limits.MaxAttributes]
		}
		out.Attributes = make(map[string]string, len(keys))
		for _, key := range keys {
			out.Attributes[key] = Scrub(clip(attributeText(in.Attributes[key]), limits.MaxAttributeValue))
		}
	}

	if out.Message == "" && out.ExceptionType == "" && out.ExceptionValue == "" {
		return nil, fmt.Errorf("a record needs a message or an exception")
	}
	out.Size = uint32(recordBytes(out))
	return out, nil
}

// recordBytes is what a record is billed as: every byte of content it carries, plus a fixed
// allowance for the fields every record has (time, level, ids). Content rather than the JSON the
// SDK happened to send, so an SDK that pretty-prints does not cost its studio more.
func recordBytes(r *NormalizedLogRecord) int {
	n := 64 + len(r.Logger) + len(r.Message) + len(r.SessionID) + len(r.PlayerID) +
		len(r.ExceptionType) + len(r.ExceptionValue)
	for _, f := range r.Frames {
		n += len(f.Function) + len(f.Module) + len(f.File) + 8
	}
	for _, b := range r.Breadcrumbs {
		n += len(b.Category) + len(b.Message) + len(b.Level) + 8
	}
	for key, value := range r.Attributes {
		n += len(key) + len(value)
	}
	return n
}

// attributeText writes an attribute's value as the text it is stored and searched as.
func attributeText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// clip cuts text to at most n bytes without splitting a character, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
