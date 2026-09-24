package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// EventDefinition is what a studio says an event name means: a category to group it under and a
// sentence for whoever reads the catalogue next. The events themselves are ClickHouse's; this is
// the label on the drawer.
type EventDefinition struct{ ent.Schema }

func (EventDefinition) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (EventDefinition) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (EventDefinition) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(96).Immutable().Comment(dataclass.None),
		field.Enum("category").Values("session", "progression", "economy", "engagement", "custom").
			Default("custom").Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),
		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (EventDefinition) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("event_definitions").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (EventDefinition) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id", "name").Unique()}
}
