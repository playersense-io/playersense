package globaltier

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// Segment is an audience a studio named, so it can be reused (§41).
//
// "Whales", "paid users" and "returning users" are not features PlayerSense ships. Each is a
// rule one organisation wrote over a vocabulary it did not write — one calls $100 a whale and
// the next calls it $1000 — and naming it is what lets the same audience appear in a flag, a
// campaign and next month's campaign without being retyped.
//
// # Why this lives in the global tier
//
// It names no player. A rule is a *definition*, like a project or an API key, so §99's
// residency boundary does not apply to it: what stays in the cell is the player's context and
// the answer, never the question. A studio therefore writes a campaign once rather than once
// per region, and a rule that exists in Europe and not in America stops being a way to lose a
// campaign.
type Segment struct{ ent.Schema }

func (Segment) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Segment) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Segment) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Comment(dataclass.None),

		// The stable name a rule refers to. Immutable, because a published config holds
		// the segment's *inlined* rule and its key is how an editor finds what to
		// republish — renaming one would orphan every campaign that named it.
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// The rule itself, as internal/targeting parses it. Stored as JSON rather than in
		// columns because it is a tree and a schema that could hold it would be a schema
		// nobody could read.
		field.JSON("rule", map[string]any{}).Comment(dataclass.None),

		// Archived rather than deleted. A published config version holds this rule
		// inlined, so deleting the row would leave a live campaign whose audience nobody
		// can explain — and §41's versions are immutable precisely so that history stays
		// answerable.
		field.Bool("archived").Default(false).Comment(dataclass.None),
	}
}

func (Segment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("segments").
			Field("project_id").Unique().Required(),
	}
}

func (Segment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key").Edges("project").Unique()}
}

// FeatureFlag is one value a game reads at run time (§41).
type FeatureFlag struct{ ent.Schema }

func (FeatureFlag) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (FeatureFlag) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (FeatureFlag) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Comment(dataclass.None),
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// What the game gets back. Declared so the SDK can return a typed value and so a
		// dashboard can refuse "true" in a number flag while somebody is typing it, rather
		// than at the point a game reads it.
		field.Enum("kind").Values("bool", "string", "number", "json").
			Immutable().Comment(dataclass.None),

		// What every player gets when no rule matches, and what the SDK falls back to when
		// it cannot reach the server at all. Required: a flag with no answer is a flag
		// that makes a game's behaviour depend on the network.
		field.JSON("fallback", map[string]any{}).Comment(dataclass.None),

		// Ordered rules, each with its own audience and value. The first whose audience
		// matches wins, which is why this is a list rather than a set.
		field.JSON("rules", []any{}).Default([]any{}).Comment(dataclass.None),

		// Hundredths of a percent, so 2500 is 25% (§43). Integer rather than a float
		// because a boundary that moves by rounding moves real players between arms.
		field.Int("rollout").Default(10000).Min(0).Max(10000).Comment(dataclass.None),

		// Changing it draws a fresh sample. Reusing an audience across two experiments
		// correlates them, and this is how a studio says "start again".
		field.String("salt").MaxLen(64).Default("").Comment(dataclass.None),

		field.Bool("archived").Default(false).Comment(dataclass.None),
	}
}

func (FeatureFlag) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("feature_flags").
			Field("project_id").Unique().Required(),
	}
}

func (FeatureFlag) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key").Edges("project").Unique()}
}

// Campaign is §45's LiveOps envelope: a schedule, an audience, and a payload PlayerSense does
// not understand.
//
// The split is the whole design. PlayerSense owns `key`, `kind`, `version`, `starts_at`,
// `ends_at` and eligibility; the studio owns everything inside `payload`. A backend that
// understood the payload would be a backend that had to be changed every time a studio
// invented an event type.
type Campaign struct{ ent.Schema }

func (Campaign) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Campaign) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Campaign) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Comment(dataclass.None),
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// A free string the studio chooses — "seasonal_event", "offer", "announcement".
		// Not an enum: PlayerSense does not know what kinds of thing a game has, and an
		// enum here would be a list somebody has to ask us to extend.
		field.String("kind").NotEmpty().MaxLen(64).Comment(dataclass.None),

		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.Enum("status").Values("draft", "scheduled", "live", "paused", "ended").
			Default("draft").Comment(dataclass.None),

		// UTC, and nullable because a campaign with no end runs until it is stopped.
		// Timezone policy is the studio's: a payload can carry a local hour, and the
		// server decides eligibility on an instant.
		field.Time("starts_at").Optional().Nillable().Comment(dataclass.None),
		field.Time("ends_at").Optional().Nillable().Comment(dataclass.None),

		// Higher wins when two campaigns of the same kind are both eligible, so a studio
		// can put a limited offer in front of an evergreen one without ending it.
		field.Int("priority").Default(0).Comment(dataclass.None),

		field.JSON("audience", map[string]any{}).Default(map[string]any{}).Comment(dataclass.None),

		// Arbitrary, and deliberately opaque. This is what makes the same backend serve a
		// holiday event, an economy multiplier and a shop layout.
		field.JSON("payload", map[string]any{}).Comment(dataclass.None),

		field.Int("rollout").Default(10000).Min(0).Max(10000).Comment(dataclass.None),
		field.String("salt").MaxLen(64).Default("").Comment(dataclass.None),
	}
}

func (Campaign) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("campaigns").
			Field("project_id").Unique().Required(),
	}
}

func (Campaign) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Edges("project").Unique(),
		// The scheduler's query: which campaigns in this project are due to start or end.
		index.Fields("status", "starts_at"),
	}
}

// ConfigVersion is one immutable published snapshot (§41, §42).
//
// Publishing compiles every active flag and campaign into a single document, with each
// segment reference replaced by the segment's own rule. That inlining is why the document is
// immutable and why it is worth storing rather than recomputing: it is the only record of what
// a rule *meant* on the day it ran, and editing a segment afterwards cannot rewrite history.
//
// It is also what makes a cycle impossible at request time. A segment that refers to itself is
// refused here, once, rather than being detected on every evaluation forever.
type ConfigVersion struct{ ent.Schema }

func (ConfigVersion) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (ConfigVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (ConfigVersion) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Int("environment_id").Immutable().Comment(dataclass.None),

		// Monotonic per environment. The SDK sends it back as an ETag, so it has to be
		// cheap to compare and meaningful to a human reading a log.
		field.Int("version").Immutable().Positive().Comment(dataclass.None),

		// The compiled document. Everything immutable, because an SDK that fetched
		// version 7 and a dashboard explaining version 7 must be looking at the same
		// thing.
		field.JSON("document", map[string]any{}).Immutable().Comment(dataclass.None),

		// Who published it, as an opaque actor string rather than an edge: it may be a
		// person, an API token or the scheduler publishing a campaign boundary, and an
		// edge would force all three to be the same kind of thing.
		field.String("published_by").MaxLen(120).Default("").Immutable().Comment(dataclass.None),
		field.String("note").MaxLen(500).Default("").Immutable().Comment(dataclass.None),
	}
}

func (ConfigVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("config_versions").
			Field("project_id").Unique().Required().Immutable(),
		edge.From("environment", Environment.Type).Ref("config_versions").
			Field("environment_id").Unique().Required().Immutable(),
	}
}

func (ConfigVersion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("version").Edges("environment").Unique(),
	}
}
