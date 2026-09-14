// Package eventbus is the durable queue between ingestion and the writers (§3.6, §20).
//
// It exists as an interface because §3.6 asks for one: the ingestion path must not be
// coupled to a single deployment product, so Pub/Sub is the default implementation rather
// than the design. A local implementation makes ingest-api runnable and testable without
// a cloud dependency, which matters more than it sounds — the Pub/Sub emulator needs a
// Java runtime, and an ingestion path that cannot be exercised locally is one nobody
// exercises.
package eventbus

import (
	"context"
	"time"
)

// Message is one accepted SDK batch, normalized and ready to store.
//
// One message is one batch rather than one event (§20): at the document's ceiling that is
// ten million messages a day instead of a billion, and the writer wants batches anyway
// because ClickHouse does (§25).
type Message struct {
	// PublishedAt is when the gateway accepted the batch.
	PublishedAt time.Time `json:"published_at"`

	// DedupKey is derived from the batch identity and is stable across retries, so a
	// redelivery is recognisable downstream (§21).
	DedupKey string `json:"dedup_key"`

	// Tenant is resolved from the key before publishing, so the writer never has to
	// reach PostgreSQL — which is what lets ingestion survive a control-plane outage
	// (§64).
	OrgSeq         uint32 `json:"org_seq"`
	ProjectSeq     uint32 `json:"project_seq"`
	EnvironmentSeq uint16 `json:"environment_seq"`

	OrgID         string `json:"org_id"`
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`

	PolicyTier    string `json:"policy_tier"`
	RetentionTier uint16 `json:"retention_tier"`

	// Payload is the normalized batch, as JSON.
	Payload []byte `json:"payload"`
}

// Bus is the whole of what ingestion needs from the outside world.
//
// Publish returns only once the message is durable. §19's contract is that a 202 means
// the batch reached durable queueing, and a Publish that returns before that would make
// the status a lie.
type Bus interface {
	Publish(ctx context.Context, msg *Message) error
	Name() string
}
