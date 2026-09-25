package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"playersense.dev/backend/internal/dataclass"
)

// NotificationChannel is one place a project tells people something: an email list, a Slack,
// Discord or Teams incoming webhook, a PagerDuty service, or a signed webhook of the studio's.
// Set up once and chosen by name wherever something notifies, so a destination that changes is
// changed in one place.
type NotificationChannel struct{ ent.Schema }

func (NotificationChannel) Mixin() []ent.Mixin { return []ent.Mixin{mixin{}} }
func (NotificationChannel) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (NotificationChannel) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Enum("type").Values("email", "slack", "discord", "teams", "pagerduty", "webhook").
			Immutable().Comment(dataclass.None),
		field.String("name").NotEmpty().MaxLen(120).Comment(dataclass.None),
		field.Bool("enabled").Default(true).Comment(dataclass.None),

		// What may be shown: addresses, a severity, a header's name, and the destination with
		// its secret part masked.
		field.JSON("config", map[string]any{}).Default(map[string]any{}).Comment(dataclass.None),

		// What may not: an incoming webhook's URL is its credential, as are a PagerDuty key, a
		// header's value and a webhook's signing secret. Sensitive, so never in a log line, and
		// never returned by the API.
		field.JSON("secret", map[string]string{}).Default(map[string]string{}).Sensitive().Comment(dataclass.None),

		field.Time("last_test_at").Optional().Nillable().Comment(dataclass.None),
		field.Bool("last_test_ok").Optional().Nillable().Comment(dataclass.None),
		field.String("last_test_detail").MaxLen(500).Default("").Comment(dataclass.None),

		field.String("created_by").MaxLen(254).Default("").Immutable().Comment(dataclass.None),
		field.String("updated_by").MaxLen(254).Default("").Comment(dataclass.None),
	}
}

func (NotificationChannel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).Ref("notification_channels").
			Field("project_id").Unique().Required().Immutable(),
	}
}

func (NotificationChannel) Indexes() []ent.Index {
	return []ent.Index{index.Fields("project_id")}
}

// ChannelDelivery is one send to a channel: a test, or a report's notify step. The log the
// channels page shows, kept ninety days.
type ChannelDelivery struct{ ent.Schema }

func (ChannelDelivery) Mixin() []ent.Mixin               { return []ent.Mixin{mixin{}} }
func (ChannelDelivery) Annotations() []schema.Annotation { return []schema.Annotation{withComments()} }

func (ChannelDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.Int("project_id").Immutable().Comment(dataclass.None),
		// By id rather than an edge: a channel deleted leaves its log.
		field.Int("channel_id").Immutable().Comment(dataclass.None),
		field.String("channel_name").MaxLen(120).Default("").Immutable().Comment(dataclass.None),
		field.String("source").MaxLen(200).Default("").Immutable().Comment(dataclass.None),
		field.String("title").MaxLen(300).Default("").Immutable().Comment(dataclass.None),
		field.Bool("ok").Immutable().Comment(dataclass.None),
		field.String("detail").MaxLen(500).Default("").Immutable().Comment(dataclass.None),
		field.Bool("test").Default(false).Immutable().Comment(dataclass.None),
		field.Int("duration_ms").Default(0).Immutable().Comment(dataclass.None),
	}
}

func (ChannelDelivery) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "id"),
		index.Fields("channel_id", "id"),
		index.Fields("created_at"),
	}
}
