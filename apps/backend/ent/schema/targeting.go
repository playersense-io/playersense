package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
	"playersense.dev/backend/internal/liveops"
)

// Segment is an audience a studio named, so it can be reused (§41).
//
// "Whales", "paid users" and "returning users" are not features PlayerSense ships. Each is a
// rule one organisation wrote over a vocabulary it did not write — one calls $100 a whale and
// the next calls it $1000 — and naming it is what lets the same audience appear in a flag, in
// this month's LiveOps event and in next month's without being retyped.
//
// # Why a rule holds no player data
//
// A rule is a *definition*, like a project or an API key. What is stored is the question,
// never the answer: the player's context and the match are evaluated per request and kept
// nowhere, so a segment names no player however many it describes.
type Segment struct{ ent.Schema }

func (Segment) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (Segment) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (Segment) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Comment(dataclass.None),

		// The stable name a rule refers to. Immutable, because a published config holds
		// the segment's *inlined* rule and its key is how an editor finds what to
		// republish — renaming one would orphan every event that named it.
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.String("description").MaxLen(500).Default("").Comment(dataclass.None),

		// The rule itself, as internal/targeting parses it. Stored as JSON rather than in
		// columns because it is a tree and a schema that could hold it would be a schema
		// nobody could read.
		field.JSON("rule", map[string]any{}).Comment(dataclass.None),

		// Archived rather than deleted. A published config version holds this rule
		// inlined, so deleting the row would leave a running event whose audience nobody
		// can explain — and §41's versions are immutable precisely so that history stays
		// answerable.
		field.Bool("archived").Default(false).Comment(dataclass.None),

		// Who wrote it and who last changed it, as the audit log names an actor.
		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
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
		//
		// `json.RawMessage` and not `map[string]any`, which is what this was until the
		// authoring API tried to write the first flag. A bool flag's fallback is `false`,
		// and `false` does not unmarshal into a map — so the commonest kind of flag, the
		// one every test in liveconfig uses, could not be stored. The column was jsonb
		// either way; only Go's idea of it was wrong, and nothing had ever written a row
		// to find out.
		//
		// Raw rather than `any` because it is carried, not inspected: the kind says what
		// shape it must be and [liveconfig.Kind.Accepts] checks that once, at the write.
		field.JSON("fallback", json.RawMessage{}).Comment(dataclass.None),

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

		// Who wrote it and who last changed it, as the audit log names an actor.
		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),

		// Why the flag exists, which decides when it should go: a release is removed once it
		// is out, a kill switch and a remote value stay, and an experiment ends with a
		// decision. Tags sort and group. Neither reaches a player.
		field.Enum("purpose").Values("release", "kill_switch", "config", "experiment").
			Default("config").Comment(dataclass.None),
		field.Strings("tags").Default([]string{}).
			Annotations(entsql.DefaultExpr("'[]'::jsonb")).Comment(dataclass.None),

		// It grants currency, items, discounts or paid content, so a rule on a key the
		// client can forge is worth a warning (ADR 0027).
		field.Bool("grants_value").Default(false).Comment(dataclass.None),
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

// LiveOpsEvent is §45's LiveOps envelope: a schedule, an audience, and a payload PlayerSense
// does not understand.
//
// Named `LiveOpsEvent` and never `Event`. A studio calls these events — a double-XP weekend,
// a holiday shop — but `events_raw`, `event_name` and `event_time` mean analytics events
// everywhere else in this codebase, and two things called Event in one product is how a
// dashboard ends up with a screen nobody can name.
//
// The split is the whole design. PlayerSense owns `key`, `kind`, `version`, the schedule and
// eligibility; the studio owns everything inside `payload`. A backend that understood the
// payload would be a backend that had to be changed every time a studio invented an event
// type.
type LiveOpsEvent struct{ ent.Schema }

func (LiveOpsEvent) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }

// Annotations names the table, because ent would derive `live_ops_events` from the Go type
// and the product spells it one word everywhere else — the package, the permissions, the
// dashboard section.
func (LiveOpsEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments(), entsql.Annotation{Table: "liveops_events"}}
}

func (LiveOpsEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Comment(dataclass.None),
		field.String("key").NotEmpty().MaxLen(64).Immutable().Comment(dataclass.None),

		// A free string the studio chooses — "seasonal_event", "offer", "announcement".
		// Not an enum: PlayerSense does not know what kinds of thing a game has, and an
		// enum here would be a list somebody has to ask us to extend.
		field.String("kind").NotEmpty().MaxLen(64).Comment(dataclass.None),

		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),

		// What a *person* decided, which is all that is stored. "Live", "scheduled" and
		// "ended" used to be values here and every one of them was a lie waiting to
		// happen: an event is live when its calendar says so, so keeping the word in a
		// column means a cron job writing rows at midnight to keep it true — and a
		// recurring event is never ended at all.
		field.Enum("status").Values("draft", "active", "paused", "archived").
			Default("draft").Comment(dataclass.None),

		// When it is on, as internal/liveops evaluates it: an anchor, a duration and how
		// it repeats. One field rather than the `starts_at`/`ends_at` pair it replaces,
		// because "the first five days of every month" is not expressible as two
		// timestamps and a studio would otherwise create twelve events a year by hand.
		field.JSON("schedule", liveops.Schedule{}).Comment(dataclass.None),

		// Higher wins when two events of the same kind are both on, so a studio can put a
		// limited offer in front of an evergreen one without ending it.
		field.Int("priority").Default(0).Comment(dataclass.None),

		field.JSON("audience", map[string]any{}).Default(map[string]any{}).Comment(dataclass.None),

		// Arbitrary, and deliberately opaque. This is what makes the same backend serve a
		// holiday event, an economy multiplier and a shop layout.
		//
		// Raw for the same reason `fallback` is: a map could only hold an object, so the
		// economy multiplier in that sentence — `2.0` — was the one payload this field
		// could not carry. "Arbitrary" has to include the scalars.
		field.JSON("payload", json.RawMessage{}).Comment(dataclass.None),

		field.Int("rollout").Default(10000).Min(0).Max(10000).Comment(dataclass.None),
		field.String("salt").MaxLen(64).Default("").Comment(dataclass.None),

		// What it is for, in words; the metric it is meant to move; and the ones it must not
		// hurt. They decide what its impact page compares, and nothing a player receives.
		field.String("description").MaxLen(1000).Default("").Comment(dataclass.None),
		field.String("goal").MaxLen(64).Optional().Nillable().Comment(dataclass.None),
		field.Strings("watch").Default([]string{}).
			Annotations(entsql.DefaultExpr("'[]'::jsonb")).Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (LiveOpsEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("liveops_events").
			Field("project_id").Unique().Required(),
	}
}

// Indexes is only the uniqueness constraint.
//
// There used to be a second, on `(status, starts_at)`, for the scheduler that woke at every
// campaign boundary to republish. Recurrence removed that job rather than complicated it: a
// published document carries the schedule and the server answers "is it on now" per request,
// so there is no boundary row to find and nothing to wake up for. The listing the dashboard
// does run is one project's events, which is tens of rows.
func (LiveOpsEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Edges("project").Unique(),
	}
}

// ConfigVersion is one immutable published snapshot (§41, §42).
//
// Publishing compiles every active flag and LiveOps event into a single document, with each
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

		// Who published it, in the same shape AuditEvent uses for an actor: a user, a key,
		// or a label when neither resolves.
		//
		// It used to be one free string. Two edges and a label instead, because "who
		// published version 7" is the first question asked when version 7 is wrong, and a
		// string that said "ada@studio.example" could not be joined to a person who has since
		// changed their address — while a string that said "ci" could not be told from a
		// person called ci.
		//
		// The label is still written and still immutable, because an actor who is later
		// deactivated must not turn a year-old publish into "unknown".
		field.Int("published_by_user_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Int("published_by_key_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.String("published_by_label").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("note").MaxLen(500).Default("").Immutable().Comment(dataclass.None),

		// A rollback is a publish of an earlier version's document, unchanged: this is the
		// version it republished. Nothing is deleted, so the history reads forwards.
		field.Int("rollback_of").Optional().Nillable().Immutable().Comment(dataclass.None),
	}
}

func (ConfigVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("config_versions").
			Field("project_id").Unique().Required().Immutable(),
		edge.From("environment", Environment.Type).Ref("config_versions").
			Field("environment_id").Unique().Required().Immutable(),
		edge.From("published_by_user", User.Type).Ref("published_versions").
			Field("published_by_user_id").Unique().Immutable(),
		edge.From("published_by_key", APIKey.Type).Ref("published_versions").
			Field("published_by_key_id").Unique().Immutable(),
	}
}

func (ConfigVersion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("version").Edges("environment").Unique(),
	}
}
