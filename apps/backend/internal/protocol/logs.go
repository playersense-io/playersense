package protocol

import "time"

// LogBatch is one flush of the game's own logs, exceptions and crashes from one installation
// (ADR 0050, docs/sdk/protocol.md §15).
//
// A separate envelope and a separate endpoint from Batch, for three reasons. Logs are an order of
// magnitude more bytes than events, so a storm of them must not queue ahead of the analytics a
// studio is paying for. They are billed by the gigabyte stored rather than by the player. And they
// have a shape events do not: a stack of frames, a trail of breadcrumbs, a severity.
//
// The header is Batch's, field for field, so an SDK reuses what it already builds; only the
// records differ.
type LogBatch struct {
	SchemaVersion int    `json:"schema_version" doc:"The envelope version; currently 1"`
	BatchID       string `json:"batch_id" doc:"Stable across retries, so a redelivery is recognisable"`
	Environment   string `json:"environment" doc:"development, staging or production"`

	InstallID string `json:"install_id" doc:"The installation this batch came from"`

	SDK     SDK       `json:"sdk"`
	App     App       `json:"app"`
	Device  LogDevice `json:"device"`
	Consent Consent   `json:"consent"`

	Records []LogRecord `json:"records"`
}

// LogDevice is Device with the model added, because "which phones does this crash on" is the
// first question a crash is asked. A model name is shared by millions of devices; it describes a
// product, not a person (§100).
type LogDevice struct {
	Platform  string `json:"platform" doc:"ios, android, windows, macos, linux, web, console"`
	OSVersion string `json:"os_version,omitzero"`
	Locale    string `json:"locale,omitzero"`
	Model     string `json:"model,omitzero" doc:"The hardware model, such as iPhone15,2 or SM-S918B"`
}

// LogRecord is one line, one caught exception, or one crash.
//
// What kind it is follows from what it carries: a record with `crash` set is a crash, one with an
// exception is an exception, and anything else is a log line. There is no kind field to disagree
// with the rest of the record.
type LogRecord struct {
	// Optional, like Event.EventID: when absent the gateway derives one from the batch and the
	// record's position, which is stable across retries by construction (ADR 0026).
	RecordID string `json:"record_id,omitzero" doc:"A UUID; derived from the batch when absent"`

	Time  time.Time `json:"time" doc:"When it happened, on the device's clock"`
	Level string    `json:"level" doc:"trace, debug, info, warn, error or fatal"`

	Message string `json:"message,omitzero"`
	// Where it was logged from: a category, a subsystem, a class. Short and repeated, so it is
	// something to filter by rather than something to read.
	Logger string `json:"logger,omitzero"`

	SessionID string `json:"session_id,omitzero"`
	PlayerID  string `json:"player_id,omitzero"`

	// Anything else the game wants to search by. Values are written as text.
	Attributes map[string]any `json:"attributes,omitzero"`

	Exception *LogException `json:"exception,omitzero"`

	// The process died of this. An SDK writes a crash to disk as it happens and sends it on the
	// next launch, which is why the record's own time, not the batch's, is when it happened.
	Crash bool `json:"crash,omitzero"`

	// What the game did just before, oldest first. Sent with exceptions and crashes; ignored on
	// a plain log line.
	Breadcrumbs []Breadcrumb `json:"breadcrumbs,omitzero"`

	// Overrides how the record is grouped into an issue: records with the same fingerprint are
	// one issue. Absent, the server groups by the exception and where it was thrown.
	Fingerprint []string `json:"fingerprint,omitzero"`
}

// LogException is what was thrown.
type LogException struct {
	Type    string `json:"type" doc:"The exception's class, such as NullReferenceException"`
	Value   string `json:"value,omitzero" doc:"Its message"`
	Handled bool   `json:"handled,omitzero" doc:"Whether the game caught it"`
	// Outermost first, innermost (where it was thrown) last: the order a stack is printed in
	// by most runtimes, reversed so the thrower is at the end.
	Frames []StackFrame `json:"frames,omitzero"`
}

// StackFrame is one call on the stack.
type StackFrame struct {
	Function string `json:"function,omitzero"`
	Module   string `json:"module,omitzero" doc:"The class, namespace or library"`
	File     string `json:"file,omitzero"`
	Line     int    `json:"line,omitzero"`
	// The game's own code rather than the engine's or a library's. Grouping and the culprit
	// read only these, so the same engine frame under two different bugs does not merge them.
	InApp bool `json:"in_app,omitzero"`
}

// Breadcrumb is one thing that happened before an exception.
type Breadcrumb struct {
	At       time.Time `json:"at"`
	Category string    `json:"category,omitzero" doc:"scene, ui, network, log…"`
	Message  string    `json:"message,omitzero"`
	Level    string    `json:"level,omitzero"`
}

// LogsAccepted is the answer to a log batch: how many records were stored and how many dropped.
type LogsAccepted struct {
	BatchID  string   `json:"batch_id"`
	Accepted int      `json:"accepted"`
	Dropped  int      `json:"dropped"`
	Reasons  []string `json:"reasons,omitzero"`
}
