// Package protocol is the SDK wire contract (project.md §17).
//
// It is deliberately separate from the handler and from the storage types: every SDK in
// §15 implements this shape, and §9 requires it to survive the Stage 1 to Stage 2 move
// unchanged. A field added here is a field every SDK has to learn about.
package protocol

import (
	"time"
)

// SchemaVersion is the envelope version this build speaks.
const SchemaVersion = 1

// Batch is one flush from one installation.
//
// Two fields are not where §17's first draft put them, and both moved for a reason:
//
//   - The project key travels in the Authorization header rather than in this struct.
//     A credential in a body cannot authenticate the request under the framework's
//     ordering — guards and providers run before binding — and a key in a body is a key
//     in every proxy that logs one.
//   - install_id is on the batch rather than on each event. A batch is one flush from one
//     installation, so it cannot change within one, and repeating a UUID on every event
//     is bytes §4.1 actually cares about. player_id stays per-event, because that one
//     genuinely can change mid-batch when somebody signs in.
type Batch struct {
	SchemaVersion int    `json:"schema_version" doc:"The envelope version; currently 1"`
	BatchID       string `json:"batch_id" doc:"Stable across retries, so a redelivery is recognisable"`
	Environment   string `json:"environment" doc:"development, staging or production"`

	InstallID string `json:"install_id" doc:"The installation this batch came from"`

	SDK     SDK     `json:"sdk"`
	App     App     `json:"app"`
	Device  Device  `json:"device"`
	Consent Consent `json:"consent"`

	Events []Event `json:"events"`
}

// SDK identifies the client library.
type SDK struct {
	Name    string `json:"name" doc:"unity, unreal, godot, ios, android, javascript…"`
	Version string `json:"version"`
}

// App identifies the game build.
type App struct {
	Version string `json:"version"`
	Build   string `json:"build,omitzero"`
}

// Device carries the coarse attributes analytics slices by.
//
// Nothing here is precise enough to identify a person, which is deliberate: §100 forbids
// fingerprinting, and a device description detailed enough to be a fingerprint is one.
type Device struct {
	Platform  string `json:"platform" doc:"ios, android, windows, macos, linux, web, console"`
	OSVersion string `json:"os_version,omitzero"`
	Locale    string `json:"locale,omitzero"`
}

// Consent is the collection basis (§97).
//
// This is the one field that cannot be reconstructed on the server, and the one that
// becomes impossible to add once data exists. Without it no stored row can prove what it
// was collected under, and a withdrawal cannot be scoped to anything narrower than
// everything.
type Consent struct {
	Analytics     bool      `json:"analytics" doc:"Whether the player agreed to analytics collection"`
	PolicyVersion string    `json:"policy_version" doc:"Which version of the terms they agreed to"`
	ATTStatus     string    `json:"att_status,omitzero" doc:"authorized, denied, not_determined or restricted"`
	GDPRApplies   bool      `json:"gdpr_applies,omitzero"`
	CollectedAt   time.Time `json:"collected_at,omitzero"`
}

// Event is one thing that happened.
type Event struct {
	EventID   string    `json:"event_id" doc:"Unique per event, generated before sending"`
	EventName string    `json:"event_name" doc:"level_completed, session_start, purchase…"`
	EventTime time.Time `json:"event_time"`

	SessionID string `json:"session_id,omitzero"`
	PlayerID  string `json:"player_id,omitzero" doc:"Present once the player has an account"`
	Sequence  int64  `json:"sequence,omitzero" doc:"Monotonic within a session, for ordering"`

	Properties map[string]any `json:"properties,omitzero"`

	RevenueMicros int64  `json:"revenue_micros,omitzero"`
	Currency      string `json:"currency,omitzero"`
}

// Accepted is what the gateway answers with.
//
// It says what was durably queued and what was dropped, so an SDK can stop retrying a
// batch that will never be accepted rather than spooling it forever.
type Accepted struct {
	BatchID  string `json:"batch_id"`
	Accepted int    `json:"accepted" doc:"Events durably queued"`
	Dropped  int    `json:"dropped,omitzero" doc:"Events refused; see reasons"`

	// Reasons is bounded, because an SDK that sent ten thousand malformed events does
	// not need ten thousand explanations to fix its integration.
	Reasons []string `json:"reasons,omitzero"`
}
