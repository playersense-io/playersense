package globaltier

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// Project is one game (§75).
//
// home_region and policy_tier are immutable. §75 says a tier change applies from the
// moment it is set and never retroactively, and §98 says the data carries its own basis —
// so changing either is a new project, not an update.
type Project struct{ ent.Schema }

func (Project) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Project) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Project) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("slug").NotEmpty().MaxLen(64).Comment(dataclass.None),

		// Which cell holds this project's data (§6, §99).
		field.String("home_region").NotEmpty().MaxLen(16).Immutable().
			Default("eu-1").Comment(dataclass.None),

		// standard | child_directed | kids_strict (§98). kids_strict has no persistent
		// identifier at all, so cross-day retention is not computable for it — the
		// product says so when the tier is chosen rather than shipping an empty chart.
		field.Enum("policy_tier").
			Values("standard", "child_directed", "kids_strict").
			Default("standard").Immutable().Comment(dataclass.None),

		// Raw retention, from the discrete tiers §24 defines.
		field.Uint16("retention_days").Default(30).Comment(dataclass.None),

		field.Int("organization_id").Comment(dataclass.None),
	}
}

func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("projects").
			Field("organization_id").Unique().Required(),
		edge.To("environments", Environment.Type),
	}
}

func (Project) Indexes() []ent.Index {
	return []ent.Index{index.Fields("slug").Edges("organization").Unique()}
}

// Environment isolates development traffic from production analytics (§76).
type Environment struct{ ent.Schema }

func (Environment) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Environment) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Environment) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("kind").Values("development", "staging", "production").Comment(dataclass.None),
		field.Int("project_id").Comment(dataclass.None),
	}
}

func (Environment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("environments").
			Field("project_id").Unique().Required(),
		edge.To("api_keys", APIKey.Type),
	}
}

func (Environment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("kind").Edges("project").Unique()}
}
