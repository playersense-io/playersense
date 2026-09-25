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
		// The notification rule that sent it, by id; empty for a report's send and a channel's
		// test. By id rather than by the source's name, so a renamed rule keeps its history.
		field.Int("rule_id").Optional().Nillable().Immutable().Comment(dataclass.None),
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
		index.Fields("rule_id", "created_at"),
		index.Fields("created_at"),
	}
}

// ChannelDeliveryHour is the delivery log rolled up by the hour: one row per project, hour,
// channel, rule and source, incremented as each send is logged. The Activity page and each rule's
// sparkline read these, a few hundred small rows for thirty days whatever the volume, rather than
// scanning the log. Kept as long as the log is.
//
// A rule's sends are keyed by the rule and carry no source, so a renamed rule is one history. A
// report's are keyed by its source. Tests are counted in their own column of the same row.
type ChannelDeliveryHour struct{ ent.Schema }

func (ChannelDeliveryHour) Annotations() []schema.Annotation {
	return []schema.Annotation{withComments()}
}

func (ChannelDeliveryHour) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").Comment(dataclass.None),
		field.Int("project_id").Immutable().Comment(dataclass.None),
		field.Time("hour").Immutable().Comment(dataclass.None),
		field.Int("channel_id").Immutable().Comment(dataclass.None),
		// 0 for a send no rule made.
		field.Int("rule_id").Default(0).Immutable().Comment(dataclass.None),
		// A report's source; empty for a rule's send and for a test.
		field.String("source").MaxLen(200).Default("").Immutable().Comment(dataclass.None),
		// The channel's name at its latest send, for a channel deleted since.
		field.String("channel_name").MaxLen(120).Default("").Comment(dataclass.None),
		field.Int("delivered").Default(0).Comment(dataclass.None),
		field.Int("failed").Default(0).Comment(dataclass.None),
		field.Int("tests").Default(0).Comment(dataclass.None),
		// Answer times, tests included, as a histogram: how many answered within 100 ms, 250 ms,
		// 500 ms, 1 s, 2.5 s, 5 s, 10 s, and beyond. Enough for a median and a 95th percentile
		// that add up across rows, which a stored percentile would not. Columns rather than an
		// array, so a window's histogram is a SUM per column.
		field.Int("lat_100").Default(0).Comment(dataclass.None),
		field.Int("lat_250").Default(0).Comment(dataclass.None),
		field.Int("lat_500").Default(0).Comment(dataclass.None),
		field.Int("lat_1000").Default(0).Comment(dataclass.None),
		field.Int("lat_2500").Default(0).Comment(dataclass.None),
		field.Int("lat_5000").Default(0).Comment(dataclass.None),
		field.Int("lat_10000").Default(0).Comment(dataclass.None),
		field.Int("lat_over").Default(0).Comment(dataclass.None),
		field.Time("last_at").Comment(dataclass.None),
		field.Bool("last_ok").Comment(dataclass.None),
	}
}

func (ChannelDeliveryHour) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project_id", "hour", "channel_id", "rule_id", "source").Unique(),
		index.Fields("rule_id", "hour"),
		index.Fields("hour"),
	}
}
