package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
)

// mixin gives every entity an integer primary key and a public UUID.
//
// The integer primary key is deliberate. ClickHouse sorts on org_seq / project_seq /
// environment_seq (§23.1), and three UUIDs there would be 48 bytes of incompressible
// prefix on every row of every analytics table. So the integer id allocated here is the
// surrogate the data plane sorts on, and the UUID is what the outside world sees.
type mixin struct{ ent.Schema }

func (mixin) Fields() []ent.Field {
	return []ent.Field{
		// The default id is declared explicitly so it can carry a classification;
		// §96 admits no unclassified column, generated or not.
		field.Int("id").Comment(dataclass.None),
		field.UUID("uid", uuid.UUID{}).
			Default(uuid.New).
			Immutable().
			Unique().
			Comment(dataclass.None),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment(dataclass.None),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment(dataclass.None),
	}
}

// appendOnlyMixin is the mixin for a row that is written once and never changed.
//
// The same as mixin without `updated_at`, and dropping that field is the whole point rather
// than a tidy-up. An append-only row's `updated_at` can never differ from its `created_at`, so
// it carries no information — and it is the one column that would otherwise be mutable.
//
// ent still emits an `...Update` type, because it emits one per entity. What it does not emit
// is a single `Set` method on it: with every field Immutable and no `updated_at`, the builder
// has `Where`, `Save` and `Exec` and nothing that could change what a row says. Editing an
// audit entry is therefore a compile error rather than a policy somebody has to remember, and
// `TestAnAppendOnlyRowHasNoSetter` fails the day a field stops being Immutable.
//
// §08: "a log the operator can edit proves nothing."
type appendOnlyMixin struct{ ent.Schema }

func (appendOnlyMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").Comment(dataclass.None),
		field.UUID("uid", uuid.UUID{}).
			Default(uuid.New).
			Immutable().
			Unique().
			Comment(dataclass.None),
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment(dataclass.None),
	}
}
