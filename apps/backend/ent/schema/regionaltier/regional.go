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

// Everything in this package either holds a player identifier or maps where player data
// physically sits, which is why none of it is in the global tier (§99):
//
//	a player identifier never appears in the global tier
//
// The split is a package boundary rather than an annotation, so global code cannot
// reference a regional entity even by mistake — the compiler refuses. `migrate verify`
// checks the same rule against the live database, which catches anything added by hand.
//
// A project is referenced by its public UUID rather than by an edge. The two tiers are
// separate databases — in production, separate servers in separate jurisdictions — and a
// foreign key across that boundary is not a thing PostgreSQL can express.

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

// ArchiveManifest records one immutable archive object (§14.2).
//
// It holds no player identifier, and it belongs here anyway. It is the map of where a
// region's player data physically sits: every row names a bucket path, the project and
// environment whose events are inside, and the span of time they cover. A copy of that in
// the US-hosted global tier would be an index of EU player data — not the data, but the
// thing anyone would want first, and §99's rule is about what a region lets out rather than
// only about column types. The worker that acts on these rows (§60) runs in the cell too.
//
// Three jobs depend on it, and each explains a field that would otherwise look like
// bookkeeping:
//
//   - §39 bills storage from object_size_bytes, which is why the size is recorded rather
//     than measured later — an object deleted at month end still has to be billed for the
//     days it existed.
//   - §60 expires objects by delete_after, which is application-controlled rather than left
//     to a bucket lifecycle rule so that a project's retention tier decides it.
//   - §104 scopes a breach from min_event_time, max_event_time and the project: "the
//     question a notification deadline turns on — whose data, and how much — is a query
//     rather than an investigation."
type ArchiveManifest struct{ ent.Schema }

func (ArchiveManifest) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (ArchiveManifest) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (ArchiveManifest) Fields() []ent.Field {
	return []ent.Field{
		// Where the object is. Immutable: the path is the object's identity, and an
		// archive whose manifest can be repointed is an archive nothing can be proved
		// about.
		field.String("bucket").NotEmpty().MaxLen(255).Immutable().Comment(dataclass.None),
		field.String("object_path").NotEmpty().MaxLen(1024).Immutable().Comment(dataclass.None),
		field.String("region").NotEmpty().MaxLen(16).Immutable().Comment(dataclass.None),

		// Whose events are inside. UUIDs rather than edges, because the tiers are separate
		// databases and a foreign key across that boundary is not expressible.
		field.UUID("org_uid", uuid.UUID{}).Immutable().Comment(dataclass.None),
		field.UUID("project_uid", uuid.UUID{}).Immutable().Comment(dataclass.None),
		field.UUID("environment_uid", uuid.UUID{}).Immutable().Comment(dataclass.None),

		// What span they cover. Behavioural rather than none: these are bounds on when
		// real people played, and a narrow one describes a single session.
		field.Time("min_event_time").Immutable().Comment(dataclass.Behavioural),
		field.Time("max_event_time").Immutable().Comment(dataclass.Behavioural),

		field.Uint64("event_count").Immutable().Comment(dataclass.Derived),
		field.Uint64("object_size_bytes").Immutable().Comment(dataclass.None),

		// What the same events weighed before compression, so §39 can bill on a number
		// that does not move when the codec changes.
		field.Uint64("normalized_input_bytes").Immutable().Comment(dataclass.None),

		field.Uint16("schema_version").Immutable().Comment(dataclass.None),

		// When this object may be removed (§60). Derived from the project's retention tier
		// at write time rather than read at deletion time, so lowering a customer's
		// retention does not retroactively destroy an archive they already paid for, and
		// raising it does not resurrect what was already expired.
		field.Time("delete_after").Comment(dataclass.None),

		// durable is the only state a row is created in: the manifest is written after the
		// object is, so a row existing means the bytes do (Appendix C.2). The rest belong
		// to §60's retention worker, and `failed` exists so a stuck deletion is visible
		// rather than retried forever in silence (Appendix C.13).
		field.Enum("status").
			Values("durable", "deleting", "deleted", "failed").
			Default("durable").Comment(dataclass.None),

		field.String("failure_reason").MaxLen(512).Optional().Comment(dataclass.None),
	}
}

func (ArchiveManifest) Indexes() []ent.Index {
	return []ent.Index{
		// One row per object. This is what makes a redelivered flush idempotent: the same
		// content produces the same path, and the second manifest write is a conflict
		// rather than a duplicate (§21).
		index.Fields("bucket", "object_path").Unique(),

		// §104's breach scoping and §34's cold queries both ask "which objects hold this
		// project's events between these times".
		index.Fields("project_uid", "min_event_time"),

		// §60's retention worker sweeps by expiry, and only over rows still present.
		index.Fields("status", "delete_after"),
	}
}
