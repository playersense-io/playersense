package regionaltier

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
