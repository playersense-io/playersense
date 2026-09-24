package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// Experiment is a test run on one flag (§43, §44): who is in it, how they are divided between
// arms, what it is judged on, and what was decided.
//
// A record of its own rather than rules on the flag, because the flag's rules cannot say what a
// test needs to: a weighted split across several arms, a share of the audience let in, and the
// hypothesis and metrics the result is read against. While it runs, every publish compiles it
// into the flag as a split (liveconfig.Split); once it stops, the next publish serves the flag's
// own rules again. The record stays, which is what answers "when did we try this, and what
// happened" a year later.
type Experiment struct{ ent.Schema }

func (Experiment) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Experiment) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Experiment) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),

		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("hypothesis").MaxLen(1000).Default("").Comment(dataclass.None),

		// The flag it runs on, by key: a flag is a project's and its key never changes.
		field.String("flag_key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// Who may be in it, as a targeting rule (segments by reference, inlined at publish),
		// and the same in words for a person reading the list.
		field.JSON("audience", map[string]any{}).Default(map[string]any{}).Comment(dataclass.None),
		field.String("audience_label").MaxLen(200).Default("").Comment(dataclass.None),

		// Percent of the audience let in. Raising it only adds players (liveconfig.Split).
		field.Int("traffic").Default(100).Min(1).Max(100).Comment(dataclass.None),

		// draft: being written. running: compiled into every publish. concluded: ended with
		// a decision. stopped: ended early.
		field.Enum("status").Values("draft", "running", "concluded", "stopped").
			Default("draft").Comment(dataclass.None),

		field.String("primary_metric").NotEmpty().MaxLen(64).Comment(dataclass.None),
		field.Strings("guardrails").Default([]string{}).Comment(dataclass.None),

		// The arms: [{id, name, value, weight, control}], weights in percent adding to 100.
		field.JSON("variants", []map[string]any{}).Comment(dataclass.None),

		field.Int("planned_days").Default(14).Min(1).Max(365).Comment(dataclass.None),

		field.Time("started_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("ended_at").Optional().Nillable().Comment(dataclass.None),

		// The arm that won, by id; empty with a decision of "no clear winner".
		field.String("winner").MaxLen(64).Optional().Nillable().Comment(dataclass.None),
		field.Bool("shipped").Default(false).Comment(dataclass.None),
		field.String("conclusion").MaxLen(2000).Default("").Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),

		// Its own history, a line per decision: [{at, actor, text}]. Beside the audit log
		// rather than instead of it, because this is read on the experiment's page by people
		// who cannot read the log.
		field.JSON("timeline", []map[string]any{}).Default([]map[string]any{}).Comment(dataclass.None),
	}
}

func (Experiment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("experiments").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (Experiment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "status"),
		// One running experiment per flag: two splits on one flag would be one test whose
		// arms nobody chose.
		index.Fields("project_id", "flag_key").Unique().
			Annotations(entsql.IndexWhere("status = 'running'")),
	}
}
