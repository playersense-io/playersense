package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
	"playersense.dev/backend/internal/stores"
)

// PurchaseProvider is how a project reaches one store to verify its players' purchases (§47):
// Apple, Google or Steam. One per store per project.
type PurchaseProvider struct{ ent.Schema }

func (PurchaseProvider) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (PurchaseProvider) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (PurchaseProvider) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Enum("platform").Values("apple", "google", "steam").Immutable().Comment(dataclass.None),
		field.Bool("enabled").Default(true).Comment(dataclass.None),

		// What may be shown: the bundle id, package name or app id, and the key's id and issuer.
		field.JSON("config", map[string]any{}).Default(map[string]any{}).Comment(dataclass.None),
		// What may not: the In-App Purchase key, the service account key, the publisher key.
		// Sensitive, so never in a log line, and never returned by the API.
		field.JSON("secret", map[string]string{}).Default(map[string]string{}).Sensitive().Comment(dataclass.None),
		// The token in the notification URL, which is how a push says it came from the store the
		// project pointed at it.
		field.String("notify_token").MaxLen(64).Sensitive().Comment(dataclass.None),

		// From when this store's revenue is its verified purchases rather than the ones the game
		// reported ("verified wins"). Set when it is connected.
		field.Time("verified_since").Comment(dataclass.None),

		field.Time("last_verified_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("last_notification_at").Optional().Nillable().Comment(dataclass.None),
		field.String("last_error").MaxLen(500).Default("").Comment(dataclass.None),
		field.Time("last_error_at").Optional().Nillable().Comment(dataclass.None),

		field.Time("last_test_at").Optional().Nillable().Comment(dataclass.None),
		field.Bool("last_test_ok").Optional().Nillable().Comment(dataclass.None),
		field.String("last_test_detail").MaxLen(500).Default("").Comment(dataclass.None),

		// Where the refund sweep reads from next.
		field.Time("sweep_cursor").Optional().Nillable().Comment(dataclass.None),
		field.Time("swept_at").Optional().Nillable().Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (PurchaseProvider) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "platform").Unique()}
}

// PurchaseAttempt is one verification: every call, whatever came of it. The log a studio reads
// to see why a receipt was turned away, and the one staff read to debug it. Kept thirty days.
type PurchaseAttempt struct{ ent.Schema }

func (PurchaseAttempt) Mixin() []ent.Mixin { return []ent.Mixin{appendOnlyMixin{}} }
func (PurchaseAttempt) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (PurchaseAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Int("environment_id").Immutable().Comment(dataclass.None),
		field.Enum("platform").Values("apple", "google", "steam").Immutable().Comment(dataclass.None),
		// Which key class asked: project (the SDK), server, or debug (staff, recording nothing).
		field.String("via").MaxLen(20).Immutable().Comment(dataclass.None),
		field.String("player_id").MaxLen(128).Default("").Immutable().Comment(dataclass.Identifier),
		field.String("install_id").MaxLen(64).Default("").Immutable().Comment(dataclass.Identifier),
		field.String("product_id").MaxLen(255).Default("").Immutable().Comment(dataclass.None),
		field.String("store_transaction_id").MaxLen(255).Default("").Immutable().Comment(dataclass.Identifier),

		field.Enum("outcome").
			Values("verified", "duplicate", "rejected", "unavailable", "credentials", "not_configured").
			Immutable().Comment(dataclass.None),
		// The rejection's code, from internal/stores.
		field.String("code").MaxLen(40).Default("").Immutable().Comment(dataclass.None),
		field.String("detail").MaxLen(500).Default("").Immutable().Comment(dataclass.None),
		field.Int("duration_ms").Default(0).Immutable().Comment(dataclass.None),
		field.Int("purchase_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Bool("sandbox").Default(false).Immutable().Comment(dataclass.None),
		// What the verification did, step by step.
		field.JSON("trace", []stores.Step{}).Optional().Immutable().Comment(dataclass.None),
	}
}

func (PurchaseAttempt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "created_at"),
		index.Fields("project_id", "outcome", "created_at"),
		index.Fields("created_at"),
	}
}

// PurchaseChange is the outbox between PostgreSQL and ClickHouse: one row per thing that changed
// what a purchase counts for (verified, refunded, a refund reversed, a receipt rejected), written
// in the transaction that made the change. The worker claims a batch, writes it to ClickHouse
// with the batch as the insert's deduplication token, and marks it shipped; a batch claimed and
// not shipped goes again with the same token, so a crash between the two lands it once.
type PurchaseChange struct{ ent.Schema }

func (PurchaseChange) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (PurchaseChange) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (PurchaseChange) Fields() []ent.Field {
	return []ent.Field{
		// By id rather than an edge: an erasure deletes the purchase, and its changes then ship
		// nothing.
		field.Int("purchase_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Int("attempt_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Enum("kind").Values("purchase", "refund", "reinstate", "rejection").Immutable().Comment(dataclass.None),
		// When it happened: a refund is booked on the day the store gave the money back.
		field.Time("at").Immutable().Comment(dataclass.None),
		field.UUID("batch", uuid.UUID{}).Optional().Nillable().Comment(dataclass.None),
		field.Time("shipped_at").Optional().Nillable().Comment(dataclass.None),
	}
}

func (PurchaseChange) Indexes() []ent.Index {
	return []ent.Index{
		// What is left to ship, which is almost nothing: the index holds only that.
		index.Fields("id").StorageKey("purchasechange_unshipped").
			Annotations(entsql.IndexWhere("shipped_at IS NULL")),
		index.Fields("batch"),
		index.Fields("shipped_at"),
	}
}
