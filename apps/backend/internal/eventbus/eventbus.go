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

	// Environment is the kind — production, staging or development — as opposed to the
	// UUID above. It travels because §14.1's archive path is keyed on the readable name:
	// a prefix of `env=production` is one a human can scan and a lifecycle rule can match,
	// and `env=<uuid>` is neither.
	Environment string `json:"environment,omitzero"`

	PolicyTier    string `json:"policy_tier"`
	RetentionTier uint16 `json:"retention_tier"`

	// EventCount is how many events the payload holds.
	//
	// It travels so a consumer can group deliveries by *rows* without decoding every
	// payload to find out how many there are. Message count is the wrong unit for that
	// decision and the difference is large: an SDK batch is anywhere from one event to a
	// thousand, so two hundred messages is two hundred rows from one integration and two
	// hundred thousand from another (ADR 0023).
	EventCount int `json:"event_count,omitzero"`

	// Payload is the normalized batch, as JSON.
	Payload []byte `json:"payload"`
}

// Handler processes a group of messages.
//
// **It must not return nil until their effect is durable.** The bus advances its position
// only on a nil return, so a nil returned before the insert has landed is a batch that was
// acknowledged and then lost — Appendix C.2, and the one contract in this package that
// cannot be relaxed.
//
// A group rather than a single message because the consumer wants to amortise a round trip
// over several, and because the bus is the only thing that knows how many are available.
type Handler func(ctx context.Context, msgs []*Message) error

// Consumer groups (§20).
//
// One topic, several independent subscriptions: the storage writer and the archive writer
// consume the same stream and must not consume it *for* each other. A group names a
// position, so each consumer has its own and a slow archive cannot hold back storage or
// skip what storage has already taken.
//
// The names are §20's own, because the Pub/Sub subscription is created with them and a
// deployment reading the document should find the same words in the console.
const (
	GroupAnalyticsWriter = "clickhouse-writer"
	GroupArchiveWriter   = "archive-writer"
)

// DeadLetterReason travels with a parked message so an operator can tell why it is there
// without reproducing the failure.
type DeadLetterReason struct {
	Group    string    `json:"group"`
	Reason   string    `json:"reason"`
	FailedAt time.Time `json:"failed_at"`
	Attempts int       `json:"attempts"`
}

// Bus is the whole of what ingestion and the writers need from the outside world.
//
// Publish returns only once the message is durable. §19's contract is that a 202 means
// the batch reached durable queueing, and a Publish that returns before that would make
// the status a lie.
//
// Subscribe runs until the context is cancelled, consuming on behalf of one group.
// Delivery is at-least-once (§21), so a handler must make its effect idempotent rather than
// assume it is called once — which is what the DedupKey above is for.
// DeadLetter parks a message no number of redeliveries will fix, so the subscription can
// move past it.
//
// Without it a consumer has exactly two options for a message it cannot process, and both
// are bad: retry forever, which stalls every message behind it, or drop it, which loses
// data silently. §20 names an `analytics-events-dlq` topic for precisely this and nothing
// used to write to it.
//
// It must be durable before it returns, for the same reason Publish must: the caller
// acknowledges the original immediately afterwards, so a dead letter that was not written
// is a batch that was accepted, refused and then forgotten.
type Bus interface {
	Publish(ctx context.Context, msg *Message) error
	Subscribe(ctx context.Context, group string, handle Handler) error
	DeadLetter(ctx context.Context, msg *Message, reason DeadLetterReason) error
	Name() string
}
