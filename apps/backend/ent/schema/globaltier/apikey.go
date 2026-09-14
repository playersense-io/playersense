package globaltier

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// APIKey is a credential scoped to one environment (§52, §53).
//
// Only a hash is stored. The plaintext is returned once, at creation, and never again:
// a key readable from the database is a key readable by anyone who reaches the database.
type APIKey struct{ ent.Schema }

func (APIKey) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (APIKey) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (APIKey) Fields() []ent.Field {
	return []ent.Field{
		// publishable ships inside a game binary and is assumed discoverable (§71);
		// secret is server-to-server and never leaves a server.
		field.Enum("kind").Values("publishable", "secret").Immutable().Comment(dataclass.None),

		// The displayable head of the key. Enough to identify which key a dashboard row
		// refers to, not enough to use.
		field.String("prefix").NotEmpty().MaxLen(32).Immutable().Comment(dataclass.None),

		// SHA-256 of the full key, compared in constant time (§53).
		field.Bytes("hash").MaxLen(32).Immutable().Sensitive().Comment(dataclass.None),

		field.String("name").MaxLen(120).Optional().Comment(dataclass.None),
		field.Time("last_used_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("revoked_at").Optional().Nillable().Comment(dataclass.None),
		field.Int("environment_id").Comment(dataclass.None),
	}
}

func (APIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("environment", Environment.Type).Ref("api_keys").
			Field("environment_id").Unique().Required(),
	}
}

func (APIKey) Indexes() []ent.Index {
	return []ent.Index{index.Fields("hash").Unique(), index.Fields("prefix")}
}
