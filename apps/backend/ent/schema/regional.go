package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Everything in this file holds a player identifier, which is why none of it is in the
// global tier (§99). The rule these entities exist to demonstrate is one line:
//
//	a player identifier never appears in the global tier
//
// `migrate verify` enforces it by refusing any column classified `identifier` in `global`.

// VerifiedPurchase is a normalized store transaction (§47, §48).
type VerifiedPurchase struct{ ent.Schema }

func (VerifiedPurchase) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (VerifiedPurchase) Annotations() []schema.Annotation {
	return []schema.Annotation{regionalTier()}
}

func (VerifiedPurchase) Fields() []ent.Field {
	return []ent.Field{
		// The project this belongs to, by its public identifier rather than an edge:
		// the two tiers are separate schemas today and separate databases later, so a
		// foreign key across them would not survive the split.
		field.UUID("project_uid", uuid.UUID{}).Comment(ClassNone),

		field.String("player_id").NotEmpty().MaxLen(128).Comment(ClassIdentifier),
		field.Enum("platform").Values("apple", "google", "steam").Comment(ClassNone),
		field.String("store_transaction_id").NotEmpty().MaxLen(255).Comment(ClassIdentifier),
		field.String("product_id").NotEmpty().MaxLen(255).Comment(ClassNone),
		field.Int64("amount_micros").Comment(ClassFinancial),
		field.String("currency").MaxLen(3).Comment(ClassFinancial),
		field.Time("purchased_at").Comment(ClassBehavioural),
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
	return []schema.Annotation{regionalTier()}
}

func (DataDeletionRequest) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("project_uid", uuid.UUID{}).Comment(ClassNone),
		field.String("actor_key").NotEmpty().MaxLen(128).Comment(ClassIdentifier),
		field.Enum("status").
			Values("received", "tombstoned", "purging", "completed", "failed").
			Default("received").Comment(ClassNone),
		field.Time("completed_at").Optional().Nillable().Comment(ClassNone),
		field.String("failure_reason").MaxLen(512).Optional().Comment(ClassNone),
	}
}

func (DataDeletionRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_uid", "actor_key")}
}
