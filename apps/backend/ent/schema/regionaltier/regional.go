package regionaltier

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
)

// Everything in this package holds a player identifier, which is why none of it is in the
// global tier (§99):
//
//	a player identifier never appears in the global tier
//
// The split is a package boundary rather than an annotation, so global code cannot
// reference a regional entity even by mistake — the compiler refuses. `migrate verify`
// checks the same rule against the live database, which catches anything added by hand.
//
// A project is referenced by its public UUID rather than by an edge. The two tiers are
// separate PostgreSQL schemas today and separate databases later, and a foreign key
// across that boundary would not survive the split.

func withComments() schema.Annotation {
	yes := true
	return entsql.Annotation{WithComments: &yes}
}

// VerifiedPurchase is a normalized store transaction (§47, §48).
type VerifiedPurchase struct{ ent.Schema }

func (VerifiedPurchase) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (VerifiedPurchase) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (VerifiedPurchase) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("project_uid", uuid.UUID{}).Comment(dataclass.None),
		field.String("player_id").NotEmpty().MaxLen(128).Comment(dataclass.Identifier),
		field.Enum("platform").Values("apple", "google", "steam").Comment(dataclass.None),
		field.String("store_transaction_id").NotEmpty().MaxLen(255).Comment(dataclass.Identifier),
		field.String("product_id").NotEmpty().MaxLen(255).Comment(dataclass.None),
		field.Int64("amount_micros").Comment(dataclass.Financial),
		field.String("currency").MaxLen(3).Comment(dataclass.Financial),
		field.Time("purchased_at").Comment(dataclass.Behavioural),
	}
}

func (VerifiedPurchase) Indexes() []ent.Index {
	// §48: the database enforces uniqueness on the platform transaction identifier, so a
	// retry returns the existing purchase rather than granting value twice.
	return []ent.Index{
		index.Fields("platform", "store_transaction_id").Unique(),
		index.Fields("project_uid", "player_id"),
	}
}

// DataDeletionRequest tracks an erasure through to completion (§61, §101).
type DataDeletionRequest struct{ ent.Schema }

func (DataDeletionRequest) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (DataDeletionRequest) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (DataDeletionRequest) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("project_uid", uuid.UUID{}).Comment(dataclass.None),
		field.String("actor_key").NotEmpty().MaxLen(128).Comment(dataclass.Identifier),
		field.Enum("status").
			Values("received", "tombstoned", "purging", "completed", "failed").
			Default("received").Comment(dataclass.None),
		field.Time("completed_at").Optional().Nillable().Comment(dataclass.None),
		field.String("failure_reason").MaxLen(512).Optional().Comment(dataclass.None),
	}
}

func (DataDeletionRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_uid", "actor_key")}
}
