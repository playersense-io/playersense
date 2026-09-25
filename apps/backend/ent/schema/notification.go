package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"

	"playersense.dev/backend/internal/dataclass"
)

// NotificationRule says which events reach which channels (ADR 0051): "when a top-up fails, tell
// #billing and PagerDuty", "when a config is published to production, tell #live-ops".
//
// A rule of a project hears that project's events. A rule of the organization (project_id empty)
// hears the organization's own, which are about its money and account, and may notify the
// channels of any of its projects. Channels stay where they are set up; a rule only points at
// them, so a destination changed once is changed for every rule.
type NotificationRule struct{ ent.Schema }

func (NotificationRule) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (NotificationRule) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (NotificationRule) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Immutable().Comment(dataclass.None),
		// Empty for a rule of the organization.
		field.Int("project_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.Bool("enabled").Default(true).Comment(dataclass.None),
		// Event types, or "*" for every event of the rule's scope, those added later included.
		field.JSON("events", []string{}).Comment(dataclass.None),
		// Whose events: production, staging, development. Empty is every environment; an event
		// of no environment (a project-level one) is heard whatever this says.
		field.JSON("environments", []string{}).Default([]string{}).Comment(dataclass.None),
		// The channels it notifies, by their public ids. A channel deleted is skipped.
		field.JSON("channels", []uuid.UUID{}).Comment(dataclass.None),
		// At most one notification per event type this often; 0 is every event. For an issue's
		// events it is per issue, so one noisy issue cannot silence another.
		field.Int("cooldown_minutes").Default(0).Comment(dataclass.None),
		// At most this many notifications an hour; past it, one "held back" notice and nothing
		// more until the hour turns. 0 is no limit.
		field.Int("max_per_hour").Default(20).Comment(dataclass.None),
		// Quiet until then: events are not queued, and nothing is caught up afterwards.
		field.Time("muted_until").Optional().Nillable().Comment(dataclass.None),
		field.Time("last_fired_at").Optional().Nillable().Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (NotificationRule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("organization_id", "project_id")}
}

// NotificationOutbox is one notification on its way: an event, for one channel of one rule, or for
// the organization's owners by email. Written when the event is (in its transaction, for an event
// of the organization; by the webhook dispatcher, for a project's), taken by the worker, and
// deleted once sent or given up on. It holds only what is in flight.
type NotificationOutbox struct{ ent.Schema }

func (NotificationOutbox) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (NotificationOutbox) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (NotificationOutbox) Fields() []ent.Field {
	return []ent.Field{
		field.Int("organization_id").Immutable().Comment(dataclass.None),
		field.Int("project_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		// The event it is about: its id, so a second enqueue of the same event is one row.
		field.UUID("event_uid", uuid.UUID{}).Immutable().Comment(dataclass.None),
		field.String("type").MaxLen(80).Immutable().Comment(dataclass.None),
		field.String("environment").MaxLen(20).Default("").Immutable().Comment(dataclass.None),
		// The rule and channel it goes to; both empty for the owners' email.
		field.Int("rule_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		field.Int("channel_id").Optional().Nillable().Immutable().Comment(dataclass.None),
		// What the message is made from. Billing events carry amounts and reasons, not people.
		field.JSON("data", map[string]any{}).Default(map[string]any{}).Immutable().Comment(dataclass.None),
		field.Time("occurred_at").Immutable().Comment(dataclass.None),
		field.Int("attempts").Default(0).Comment(dataclass.None),
		field.Time("next_at").Default(time.Now).Comment(dataclass.None),
		field.Time("claimed_until").Optional().Nillable().Comment(dataclass.None),
		field.String("last_error").MaxLen(500).Default("").Comment(dataclass.None),
	}
}

func (NotificationOutbox) Indexes() []ent.Index {
	return []ent.Index{
		// One row per event, rule and channel, however often the event is enqueued.
		index.Fields("event_uid", "rule_id", "channel_id").Unique(),
		index.Fields("next_at"),
	}
}

// NotificationMark remembers when something last notified, so it does not again too soon: a rule's
// cooldown per event type ("rule:12:billing.topup_failed"), and the owners' daily email per event
// type ("owners:7:billing.topup_failed"). A key per thing remembered; rewritten, never grown.
type NotificationMark struct{ ent.Schema }

func (NotificationMark) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (NotificationMark) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").Comment(dataclass.None),
		field.String("key").MaxLen(200).Unique().Comment(dataclass.None),
		field.Time("fired_at").Comment(dataclass.None),
	}
}

// LogIssueAlert is what the issue detector has already said about one issue (ADR 0051): so an issue
// is announced as new once, as regressed once per resolution, and as spiking once per episode,
// however many events it has. A crash that happens ten thousand times is one row and one message.
type LogIssueAlert struct{ ent.Schema }

func (LogIssueAlert) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (LogIssueAlert) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (LogIssueAlert) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.String("environment").MaxLen(20).Immutable().Comment(dataclass.None),
		// ClickHouse's UInt64, as its decimal text, as LogIssue keeps it.
		field.String("fingerprint").MaxLen(20).Immutable().Comment(dataclass.None),
		// When it was announced as new; empty for an issue already old when first looked at.
		field.Time("announced_at").Optional().Nillable().Comment(dataclass.None),
		// The resolution a regression was announced for, so the same one is not announced twice.
		field.Time("regressed_for").Optional().Nillable().Comment(dataclass.None),
		// In a spike now, and since when: one announcement an episode.
		field.Bool("spiking").Default(false).Comment(dataclass.None),
		field.Time("spiking_since").Optional().Nillable().Comment(dataclass.None),
		field.Time("last_seen").Comment(dataclass.None),
	}
}

func (LogIssueAlert) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "environment", "fingerprint").Unique(),
		index.Fields("last_seen"),
	}
}

// NotificationCount counts what a rule sent in the current hour, for its cap: past it, the rest of
// the hour is held back and said once. One row a rule, its window rewritten as the hour turns.
type NotificationCount struct{ ent.Schema }

func (NotificationCount) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (NotificationCount) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").Comment(dataclass.None),
		field.String("key").MaxLen(200).Unique().Comment(dataclass.None),
		field.Time("window_start").Comment(dataclass.None),
		field.Int("count").Default(0).Comment(dataclass.None),
	}
}
