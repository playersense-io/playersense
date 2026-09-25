package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// Report is a graph the report builder saves: sources, steps and outputs, and the schedules that
// run it (internal/reports).
type Report struct{ ent.Schema }

func (Report) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Report) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Report) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(1000).Default("").Comment(dataclass.None),
		// Which environment's rows its sources read.
		field.Enum("environment").Values("development", "staging", "production").Default("production").
			Comment(dataclass.None),
		// reports.Graph: {nodes, edges}. Event names, column names and channel ids only.
		field.JSON("graph", map[string]any{}).Comment(dataclass.None),
		// The soonest any running schedule fires; what the worker's scheduler claims on. Nil
		// when nothing runs it on its own.
		field.Time("next_run_at").Optional().Nillable().Comment(dataclass.None),
		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (Report) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("reports").Field("project_id").Unique().Required().Immutable(),
		edge.To("runs", ReportRun.Type),
	}
}

func (Report) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id"), index.Fields("next_run_at")}
}

// ReportRun is one run of a report: by hand as a test, or on its schedule.
type ReportRun struct{ ent.Schema }

func (ReportRun) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (ReportRun) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (ReportRun) Fields() []ent.Field {
	return []ent.Field{
		field.Int("report_id").Immutable().Comment(dataclass.None),
		field.Enum("trigger").Values("schedule", "test").Immutable().Comment(dataclass.None),
		// A dry test sends nothing and says what it would have.
		field.Bool("dry").Default(false).Immutable().Comment(dataclass.None),
		// One Notify only, when the run was asked for from it.
		field.String("focus").MaxLen(64).Default("").Immutable().Comment(dataclass.None),
		field.Enum("status").Values("running", "succeeded", "partial", "failed", "quiet", "stopped").
			Default("running").Comment(dataclass.None),
		field.Time("started_at").Comment(dataclass.None),
		field.Time("finished_at").Optional().Nillable().Comment(dataclass.None),
		// [{node, title, kind, channel, ok, quiet, detail}], as the builder keeps them.
		field.JSON("deliveries", []map[string]any{}).Default([]map[string]any{}).Comment(dataclass.None),
		field.Bool("cancel_requested").Default(false).Comment(dataclass.None),
		field.String("requested_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("error").MaxLen(500).Default("").Comment(dataclass.None),
	}
}

func (ReportRun) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("report", Report.Type).Ref("runs").Field("report_id").Unique().Required().Immutable(),
	}
}

func (ReportRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("report_id", "id"), index.Fields("status", "started_at")}
}

// ReportRunEvent is one step of a run as its stream sends it: numbered, so a stream that drops
// resumes where it stopped, and kept, so a run in another process streams the same.
type ReportRunEvent struct{ ent.Schema }

func (ReportRunEvent) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (ReportRunEvent) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (ReportRunEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("run_id").Immutable().Comment(dataclass.None),
		field.Int("seq").Immutable().Comment(dataclass.None),
		field.String("type").MaxLen(32).Immutable().Comment(dataclass.None),
		field.JSON("payload", map[string]any{}).Immutable().Comment(dataclass.None),
	}
}

func (ReportRunEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "seq").Unique()}
}

// ReportExport is a file an Export node wrote on a run, to download for seven days.
type ReportExport struct{ ent.Schema }

func (ReportExport) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (ReportExport) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (ReportExport) Fields() []ent.Field {
	return []ent.Field{
		field.Int("run_id").Immutable().Comment(dataclass.None),
		field.String("node").MaxLen(64).Immutable().Comment(dataclass.None),
		field.String("file_name").MaxLen(200).Immutable().Comment(dataclass.None),
		field.String("object_path").MaxLen(1024).Immutable().Comment(dataclass.None),
		field.String("content_type").MaxLen(64).Immutable().Comment(dataclass.None),
		field.Int64("size_bytes").Immutable().Comment(dataclass.None),
		field.Int("rows").Immutable().Comment(dataclass.None),
		field.Time("expires_at").Immutable().Comment(dataclass.None),
	}
}

func (ReportExport) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "node").Unique(), index.Fields("expires_at")}
}
