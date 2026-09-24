package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// SavedFunnel is a funnel kept by name (§31): the definition a funnel query takes, without the
// range, so the whole team asks the same question the same way.
type SavedFunnel struct{ ent.Schema }

func (SavedFunnel) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (SavedFunnel) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (SavedFunnel) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// schemas.FunnelDefinition as JSON: kind, window, steps or progression, breakdown.
		// Event names and property names only, never a player's value.
		field.JSON("definition", map[string]any{}).Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (SavedFunnel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("saved_funnels").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (SavedFunnel) Indexes() []ent.Index {
	// One name per project: two funnels called "Onboarding" is a team asking two questions and
	// thinking it asked one.
	return []ent.Index{index.Fields("project_id", "name").Unique()}
}
