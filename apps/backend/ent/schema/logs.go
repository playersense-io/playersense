package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// LogIssue is what a studio decided about one Cloud Logging issue (ADR 0050).
//
// The issue itself lives in ClickHouse, built from the records as they arrive: what it is, when it
// was seen, how often. This row holds only the decision, which is the one part a person makes and
// the one part that must survive a record's retention. A row exists only once somebody has acted;
// an issue with no row is unresolved.
//
// Resolved is a claim about the future: "this is fixed". The dashboard shows an issue seen again
// after resolved_at as regressed, from the two timestamps, without anything writing it here.
type LogIssue struct{ ent.Schema }

func (LogIssue) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (LogIssue) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (LogIssue) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Enum("environment").Values("development", "staging", "production").Immutable().
			Comment(dataclass.None),
		// ClickHouse's UInt64, carried as its decimal text: PostgreSQL has no unsigned 64-bit
		// integer and a signed one would read half of them as negative.
		field.String("fingerprint").MaxLen(20).Immutable().Comment(dataclass.None),

		field.Enum("status").Values("unresolved", "resolved", "ignored").Default("unresolved").
			Comment(dataclass.None),
		field.Time("resolved_at").Optional().Nillable().Comment(dataclass.None),
		// Who changed it last, as the audit log names people.
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (LogIssue) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("log_issues").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (LogIssue) Indexes() []ent.Index {
	return []ent.Index{index.Fields("environment", "fingerprint").Edges("project").Unique()}
}
