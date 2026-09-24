package schema

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
// policy_tier is immutable. §75 says a tier change applies from the moment it is set and
// never retroactively, and §98 says the data carries its own basis — so changing it is a
// new project, not an update.
type Project struct{ ent.Schema }

func (Project) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Project) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Project) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("slug").NotEmpty().MaxLen(64).Comment(dataclass.None),

		// standard | child_directed | kids_strict (§98). kids_strict has no persistent
		// identifier at all, so cross-day retention is not computable for it — the
		// product says so when the tier is chosen rather than shipping an empty chart.
		field.Enum("policy_tier").
			Values("standard", "child_directed", "kids_strict").
			Default("standard").Immutable().Comment(dataclass.None),

		// Raw retention, from the discrete tiers §24 defines.
		field.Uint16("retention_days").Default(30).Comment(dataclass.None),

		// When the tier last changed. Ingestion stamps the tier on every row it writes, so a
		// change reaches new rows as the key cache turns over (minutes), and rows already
		// written keep the tier they were stamped with.
		field.Time("retention_changed_at").Optional().Nillable().Comment(dataclass.None),

		// A lowered tier is enforced on rows already written, too: the worker deletes raw rows
		// older than it, a pass a day, until retention_purge_until, after which no row stamped
		// with the old tier can remain. Nil when there is nothing to enforce.
		field.Time("retention_purge_until").Optional().Nillable().Comment(dataclass.None),
		field.Time("retention_purged_at").Optional().Nillable().Comment(dataclass.None),

		// Deletion asked for: the project goes at this moment, and can be kept until then.
		// Thirty days, because a deletion is not undoable and the person who asked is not
		// always the person who would have said no.
		field.Time("deletion_at").Optional().Nillable().Comment(dataclass.None),
		field.String("deletion_requested_by").MaxLen(254).Default("").Comment(dataclass.None),

		// When the worker carried the deletion out: keys revoked, analytics and archive gone.
		// The row stays, because usage and invoices name it.
		field.Time("deleted_at").Optional().Nillable().Comment(dataclass.None),
		// And when its data went: an hour after deleted_at, so batches already queued when the
		// keys were revoked have landed and go with the rest.
		field.Time("deletion_purged_at").Optional().Nillable().Comment(dataclass.None),

		field.Int("organization_id").Comment(dataclass.None),
	}
}

func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("organization", Organization.Type).Ref("projects").
			Field("organization_id").Unique().Required(),
		edge.To("environments", Environment.Type),

		// Targeting definitions (§41, §45).
		edge.To("segments", Segment.Type),
		edge.To("feature_flags", FeatureFlag.Type),
		edge.To("liveops_events", LiveOpsEvent.Type),
		edge.To("config_versions", ConfigVersion.Type),
		edge.To("experiments", Experiment.Type),
		edge.To("saved_funnels", SavedFunnel.Type),

		// Who can see this project, when a membership is not "all projects".
		edge.To("member_access", MemberProject.Type),

		// The meter (§40). Per project rather than per organisation, because a studio
		// with five games needs to know which one costs what.
		edge.To("usage_daily", UsageDaily.Type),
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

		// A published config belongs to one environment, so development can be republished
		// without touching what players are running (§76).
		edge.To("config_versions", ConfigVersion.Type),
	}
}

func (Environment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("kind").Edges("project").Unique()}
}
