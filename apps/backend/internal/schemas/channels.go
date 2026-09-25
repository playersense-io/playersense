package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// Notification channels: where a project tells people something.

// ChannelConfigIn is what a channel sends to. The secret parts, a webhook URL, a PagerDuty key
// or a header's value, are written and never read back.
type ChannelConfigIn struct {
	Addresses   []string `json:"addresses,omitzero" doc:"email: 1 to 20 addresses"`
	URL         string   `json:"url,omitzero" doc:"slack, discord, teams and webhook: the incoming webhook URL. A secret; shown back masked"`
	RoutingKey  string   `json:"routing_key,omitzero" doc:"pagerduty: the Events API v2 integration key. A secret"`
	Severity    string   `json:"severity,omitzero" doc:"pagerduty: critical, error, warning or info; defaults to error"`
	HeaderName  string   `json:"header_name,omitzero" doc:"webhook: an extra header, for a gateway in front of it"`
	HeaderValue string   `json:"header_value,omitzero" doc:"webhook: that header's value. A secret"`
}

// ChannelIn creates a channel.
type ChannelIn struct {
	OrgSlug     string          `path:"org"`
	ProjectSlug string          `path:"project"`
	Type        string          `json:"type" doc:"email, slack, discord, teams, pagerduty or webhook"`
	Name        string          `json:"name"`
	Config      ChannelConfigIn `json:"config"`
}

func (in *ChannelIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Type).Trim().Lower().Required().OneOf("email", "slack", "discord", "teams", "pagerduty", "webhook")
	v.String(&in.Name).Trim().Required().MaxLen(120)
	in.Config.validate(v)
}

func (in *ChannelConfigIn) validate(v *muzak.Validation) {
	v.Slice(&in.Addresses).As("config.addresses").MaxItems(20)
	v.String(&in.URL).As("config.url").Trim().MaxLen(1000)
	v.String(&in.RoutingKey).As("config.routing_key").Trim().MaxLen(64)
	v.String(&in.Severity).As("config.severity").Trim().Lower().OneOf("", "critical", "error", "warning", "info")
	v.String(&in.HeaderName).As("config.header_name").Trim().MaxLen(64)
	v.String(&in.HeaderValue).As("config.header_value").MaxLen(500)
}

// ChannelParams names one channel.
type ChannelParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	ChannelID   uuid.UUID `path:"channel"`
}

func (in *ChannelParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// UpdateChannelIn changes a channel. The type is fixed. A secret not sent is kept, so renaming a
// channel does not mean pasting its webhook again.
type UpdateChannelIn struct {
	ChannelParams
	Name    string          `json:"name"`
	Enabled *bool           `json:"enabled,omitzero"`
	Config  ChannelConfigIn `json:"config"`
}

func (in *UpdateChannelIn) Validate(v *muzak.Validation) {
	in.ChannelParams.Validate(v)
	v.String(&in.Name).Trim().Required().MaxLen(120)
	in.Config.validate(v)
}

// TestDraftChannelIn sends a test to a channel not yet saved, from the dialog that makes it.
type TestDraftChannelIn struct {
	OrgSlug     string          `path:"org"`
	ProjectSlug string          `path:"project"`
	Type        string          `json:"type"`
	Config      ChannelConfigIn `json:"config"`
}

func (in *TestDraftChannelIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Type).Trim().Lower().Required().OneOf("email", "slack", "discord", "teams", "pagerduty", "webhook")
	in.Config.validate(v)
}

// ChannelOut is one channel, its secrets masked.
type ChannelOut struct {
	ID        uuid.UUID        `json:"id"`
	Type      string           `json:"type"`
	Name      string           `json:"name"`
	Enabled   bool             `json:"enabled"`
	Config    ChannelConfigOut `json:"config"`
	Status    string           `json:"status" doc:"working, failing, untested or off"`
	LastTest  *ChannelTestOut  `json:"last_test,omitzero"`
	CreatedAt time.Time        `json:"created_at"`
	CreatedBy string           `json:"created_by,omitzero"`
	UpdatedBy string           `json:"updated_by,omitzero"`
	// SigningSecret is a webhook channel's, returned once, when it is made.
	SigningSecret string `json:"signing_secret,omitzero" doc:"A webhook channel's signing secret. Returned once, at creation"`
}

// ChannelConfigOut is what may be shown of where a channel sends.
type ChannelConfigOut struct {
	Addresses  []string `json:"addresses,omitzero"`
	URL        string   `json:"url,omitzero" doc:"Masked"`
	Severity   string   `json:"severity,omitzero"`
	HeaderName string   `json:"header_name,omitzero"`
	HasKey     bool     `json:"has_routing_key,omitzero"`
}

// ChannelTestOut is what a test send did.
type ChannelTestOut struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Detail string    `json:"detail"`
}

// ChannelListOut is a project's channels.
type ChannelListOut struct {
	Channels []ChannelOut `json:"channels"`
}

// ChannelDeliveriesParams pages the delivery log.
type ChannelDeliveriesParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Channel     string `query:"channel,omitzero" doc:"One channel's id"`
	Before      string `query:"before,omitzero"`
	Limit       int    `query:"limit,omitzero" default:"100" doc:"Newest first, up to 500"`
}

func (in *ChannelDeliveriesParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Channel).Trim().MaxLen(36)
	v.String(&in.Before).Trim().MaxLen(20)
	v.Number(&in.Limit).Min(1).Max(500)
}

// ChannelDeliveriesOut is one page of the log.
type ChannelDeliveriesOut struct {
	Deliveries []ChannelDeliveryOut `json:"deliveries"`
	Next       string               `json:"next,omitzero"`
}

// ChannelDeliveryOut is one send.
type ChannelDeliveryOut struct {
	At          time.Time  `json:"at"`
	ChannelID   *uuid.UUID `json:"channel_id,omitzero" doc:"Absent when the channel has since been deleted"`
	ChannelName string     `json:"channel_name"`
	Source      string     `json:"source" doc:"What sent it: a test, or a report"`
	Title       string     `json:"title"`
	OK          bool       `json:"ok"`
	Detail      string     `json:"detail"`
	Test        bool       `json:"test"`
	DurationMs  int        `json:"duration_ms"`
}
