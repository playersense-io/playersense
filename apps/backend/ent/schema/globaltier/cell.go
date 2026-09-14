package globaltier

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// Cell is one complete, self-contained copy of the data plane (§6).
//
// A cell owns an ingest endpoint, a queue, a ClickHouse, a Valkey, an archive bucket and
// its own PostgreSQL for player-linked records. A project is assigned to exactly one cell
// at creation and its data never leaves it.
//
// Region and cell are different axes and split for different reasons. The region is the
// legal boundary — what law applies and where bytes may sit — and the cell is a stack
// inside it, so one region may hold several cells. Regions split for law; cells split for
// capacity, which is what §83 means by moving one large customer onto its own.
//
// The registry deliberately holds identity and the *public* endpoint, never credentials.
// A table of database passwords is a table of database passwords; DSNs live in each
// service's own environment, and a service asserts at start-up that the cell it was
// configured as exists here and is active.
type Cell struct{ ent.Schema }

func (Cell) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Cell) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Cell) Fields() []ent.Field {
	return []ent.Field{
		// The cell's name, e.g. eu-1. Immutable: it appears in Valkey key prefixes, in
		// ClickHouse replica paths and in the event bus, so renaming one would orphan
		// everything already written under the old name.
		field.String("slug").NotEmpty().MaxLen(32).Immutable().Unique().
			Comment(dataclass.None),

		// The jurisdiction, e.g. eu or us. Immutable for a stronger reason than the slug:
		// changing it would silently reclassify every row already stored in the cell as
		// belonging to a different legal regime.
		field.String("region").NotEmpty().MaxLen(16).Immutable().Comment(dataclass.None),

		// active accepts new projects and new events; draining accepts events for the
		// projects it already holds but no new projects; readonly accepts neither and
		// serves queries only, which is what a migration out of a cell needs.
		field.Enum("status").
			Values("active", "draining", "readonly").
			Default("active").Comment(dataclass.None),

		// The public address an SDK sends batches to. Returned to a client that reaches
		// the wrong cell, so a misconfigured integration is told where to go rather than
		// merely refused (§6).
		field.String("ingest_url").NotEmpty().MaxLen(255).Comment(dataclass.None),

		// Human-readable placement, for the dashboard and for a customer's privacy
		// review. Not used for routing.
		field.String("location").MaxLen(64).Optional().Comment(dataclass.None),
	}
}

func (Cell) Edges() []ent.Edge {
	return []ent.Edge{edge.To("projects", Project.Type)}
}

func (Cell) Indexes() []ent.Index {
	return []ent.Index{index.Fields("region"), index.Fields("status")}
}
