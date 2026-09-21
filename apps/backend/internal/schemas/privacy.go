package schemas

import (
	"time"

	muzak "muzak.dev/framework"

	"github.com/google/uuid"
)

// ---- erasure (§61, §101) -----------------------------------------------------

// ErasureIn asks for one actor to be erased.
//
// One field, and that is the whole contract. Everything else about the request — which
// project, which cell, which retention tier — comes from the credential, for the reason
// Appendix C #3 gives about every other scoped surface: a caller who can name their own
// tenant is a caller who can name somebody else's.
type ErasureIn struct {
	ActorKey string `json:"actor_key" doc:"The actor key or install_id the player is known by in this project"`
}

// Validate constrains the identifier to what the tables can hold.
//
// No format is imposed. It is the studio's own identifier (§16) and a processor that
// insisted on a shape would be refusing to erase people whose id it merely disapproved of.
func (in *ErasureIn) Validate(v *muzak.Validation) {
	v.String(&in.ActorKey).Trim().Required().MaxLen(128)
}

// PrivacyRequestParams addresses one submitted request.
type PrivacyRequestParams struct {
	RequestID uuid.UUID `path:"request_id" doc:"The identifier returned when the request was submitted"`
}

// ErasureOut is the state of one erasure.
//
// The counts are part of the answer rather than an internal detail: "it is done" is a claim,
// and a studio answering a player deserves to be able to say what was removed. They
// accumulate across passes, so a late delivery removed on the third pass is included.
type ErasureOut struct {
	RequestID uuid.UUID `json:"request_id"`
	ActorKey  string    `json:"actor_key"`
	Status    string    `json:"status" doc:"received, tombstoned, purging, completed or failed"`

	ReceivedAt   time.Time  `json:"received_at"`
	TombstonedAt *time.Time `json:"tombstoned_at,omitzero" doc:"When the data stopped being accepted or served"`
	CompletedAt  *time.Time `json:"completed_at,omitzero" doc:"When nothing further could arrive and removal was finished"`

	DevicesDeleted   uint32 `json:"devices_deleted"`
	ObjectsRedacted  uint32 `json:"archive_objects_redacted"`
	PurchasesDeleted uint32 `json:"purchases_deleted"`

	FailureReason string `json:"failure_reason,omitzero" doc:"Why the last attempt did not finish; the request is retried"`
}

// ---- export (§101, §05) ------------------------------------------------------

// ExportIn asks for everything held about one actor.
type ExportIn struct {
	ActorKey string `json:"actor_key" doc:"The actor key or install_id the player is known by in this project"`
}

func (in *ExportIn) Validate(v *muzak.Validation) {
	v.String(&in.ActorKey).Trim().Required().MaxLen(128)
}

// ExportOut is the state of one export, and where to collect it.
type ExportOut struct {
	RequestID uuid.UUID `json:"request_id"`
	ActorKey  string    `json:"actor_key"`
	Status    string    `json:"status" doc:"received, running, completed, failed or expired"`

	ReceivedAt  time.Time  `json:"received_at"`
	CompletedAt *time.Time `json:"completed_at,omitzero"`
	ExpiresAt   *time.Time `json:"expires_at,omitzero" doc:"When the bundle is deleted; collect it before this"`

	Events    uint64 `json:"events" doc:"How many events the bundle holds; zero is a real answer"`
	SizeBytes uint64 `json:"size_bytes"`

	DownloadURL string `json:"download_url,omitzero" doc:"Where to fetch the bundle while it exists"`

	FailureReason string `json:"failure_reason,omitzero"`
}
