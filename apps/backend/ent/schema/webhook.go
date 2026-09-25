package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
)

// WebhookHeader is a header sent with every delivery to an endpoint. A secret one's value is kept
// apart, in the endpoint's header_secrets, and never shown again once saved.
type WebhookHeader struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// WebhookEndpoint is one HTTPS URL of a studio's that hears the project's events (§49-51): which
// events, from which environments, signed with its own secret.
type WebhookEndpoint struct{ ent.Schema }

func (WebhookEndpoint) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (WebhookEndpoint) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (WebhookEndpoint) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),
		field.String("url").NotEmpty().MaxLen(2048).Comment(dataclass.None),
		// Event types, or "*" for every event, those added later included.
		field.JSON("events", []string{}).Comment(dataclass.None),
		// Whose events it hears: production, staging, development. A project-level event (a
		// member added, a key made) belongs to no environment and is heard by every endpoint.
		field.JSON("environments", []string{}).Comment(dataclass.None),
		field.Bool("enabled").Default(true).Comment(dataclass.None),
		// manual, or failing: turned off by PlayerSense after three days of nothing but failures.
		field.String("disabled_reason").MaxLen(20).Default("").Comment(dataclass.None),
		field.Time("disabled_at").Optional().Nillable().Comment(dataclass.None),
		field.String("api_version").MaxLen(20).Comment(dataclass.None),
		field.JSON("headers", []WebhookHeader{}).Default([]WebhookHeader{}).Comment(dataclass.None),
		field.JSON("header_secrets", map[string]string{}).Default(map[string]string{}).Sensitive().Comment(dataclass.None),

		field.String("secret").Sensitive().Comment(dataclass.None),
		field.Time("secret_created_at").Comment(dataclass.None),
		// The secret a roll replaced, still signing beside the new one until previous_expires_at.
		field.String("previous_secret").Default("").Sensitive().Comment(dataclass.None),
		field.Time("previous_expires_at").Optional().Nillable().Comment(dataclass.None),

		// Since when every try has failed; cleared by a success. Written only when it changes, not
		// per attempt.
		field.Time("failing_since").Optional().Nillable().Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (WebhookEndpoint) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id"), index.Fields("enabled", "failing_since")}
}

// WebhookOutbox is an event on its way out: written in the transaction that made it happen, taken
// by the worker, delivered to every endpoint that hears it, and deleted. Its uid is the event's id.
// It holds only what is in flight, so it stays small however many events pass through.
type WebhookOutbox struct{ ent.Schema }

func (WebhookOutbox) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (WebhookOutbox) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (WebhookOutbox) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("environment").MaxLen(20).Default("").Immutable().Comment(dataclass.None),
		field.String("type").MaxLen(64).Immutable().Comment(dataclass.None),
		// The event as it is POSTed, JSON, written once so every endpoint gets the same bytes.
		field.Bytes("body").Immutable().Comment(dataclass.Identifier),
		// A worker's lease: another may take the event once it has passed.
		field.Time("claimed_until").Optional().Nillable().Comment(dataclass.None),
	}
}

// WebhookRetry is a delivery still owed: one that failed and is due again, or one held back from an
// endpoint that was busy or failing. It holds only those, so it is small when endpoints answer; a
// delivery that succeeds at its first try is never written here at all. Its uid is the delivery's.
type WebhookRetry struct{ ent.Schema }

func (WebhookRetry) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (WebhookRetry) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (WebhookRetry) Fields() []ent.Field {
	return []ent.Field{
		field.Int("endpoint_id").Immutable().Comment(dataclass.None),
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.UUID("event_uid", uuid.UUID{}).Immutable().Comment(dataclass.None),
		field.String("type").MaxLen(64).Immutable().Comment(dataclass.None),
		field.String("environment").MaxLen(20).Default("").Immutable().Comment(dataclass.None),
		field.Bytes("body").Immutable().Comment(dataclass.Identifier),
		// When the delivery was made: its first try, or when it was held back.
		field.Time("first_at").Immutable().Comment(dataclass.None),
		// Tries made so far.
		field.Int("attempts").Default(0).Comment(dataclass.None),
		field.Time("next_at").Comment(dataclass.None),
		field.Time("claimed_until").Optional().Nillable().Comment(dataclass.None),
		// Sent again by somebody from the dashboard, outside the schedule.
		field.Bool("manual").Default(false).Comment(dataclass.None),
	}
}

func (WebhookRetry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("next_at"), index.Fields("endpoint_id")}
}
