package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// Notification rules: which events reach which channels (ADR 0051).

// NotificationKindOut is one type of event a rule can hear.
type NotificationKindOut struct {
	Type         string `json:"type"`
	Group        string `json:"group"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Scope        string `json:"scope" doc:"project or organization"`
	Environments bool   `json:"environments" doc:"Whether a rule's environments apply to it"`
	Emitted      bool   `json:"emitted" doc:"Whether PlayerSense sends it yet"`
	Severity     string `json:"severity" doc:"critical, error, warning or info"`
	Owners       bool   `json:"owners" doc:"Owners, admins and billing members are always emailed, at most once a day"`
}

// NotificationKindsOut is the catalog.
type NotificationKindsOut struct {
	Kinds []NotificationKindOut `json:"kinds"`
}

// NotificationRuleBody is what a rule says.
type NotificationRuleBody struct {
	Name            string      `json:"name"`
	Enabled         *bool       `json:"enabled,omitzero" doc:"Defaults to on"`
	Events          []string    `json:"events" doc:"Event types, or * for every event of the rule's scope"`
	Environments    []string    `json:"environments,omitzero" doc:"production, staging, development; none is every environment"`
	Channels        []uuid.UUID `json:"channels" doc:"The channels it notifies"`
	CooldownMinutes int         `json:"cooldown_minutes,omitzero" doc:"At most one notification per event type this often; 0 is every event"`
	MutedUntil      *time.Time  `json:"muted_until,omitzero" doc:"Quiet until then; null is not muted"`
}

func (in *NotificationRuleBody) validate(v *muzak.Validation) {
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.Slice(&in.Events).MinItems(1).MaxItems(64)
	v.Slice(&in.Environments).MaxItems(3)
	v.Slice(&in.Channels).MinItems(1).MaxItems(20)
	v.Number(&in.CooldownMinutes).As("cooldown_minutes").Between(0, 7*24*60)
}

// OrgNotificationRuleIn creates a rule of the organization.
type OrgNotificationRuleIn struct {
	OrgSlug string `path:"org"`
	NotificationRuleBody
}

func (in *OrgNotificationRuleIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	in.validate(v)
}

// OrgNotificationRuleParams names one rule of the organization.
type OrgNotificationRuleParams struct {
	OrgSlug string    `path:"org"`
	RuleID  uuid.UUID `path:"rule"`
}

func (in *OrgNotificationRuleParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
}

// UpdateOrgNotificationRuleIn changes a rule of the organization.
type UpdateOrgNotificationRuleIn struct {
	OrgNotificationRuleParams
	NotificationRuleBody
}

func (in *UpdateOrgNotificationRuleIn) Validate(v *muzak.Validation) {
	in.OrgNotificationRuleParams.Validate(v)
	in.validate(v)
}

// NotificationRuleIn creates a rule of a project.
type NotificationRuleIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	NotificationRuleBody
}

func (in *NotificationRuleIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	in.validate(v)
}

// NotificationRuleParams names one rule of a project.
type NotificationRuleParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	RuleID      uuid.UUID `path:"rule"`
}

func (in *NotificationRuleParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// UpdateNotificationRuleIn changes a rule of a project.
type UpdateNotificationRuleIn struct {
	NotificationRuleParams
	NotificationRuleBody
}

func (in *UpdateNotificationRuleIn) Validate(v *muzak.Validation) {
	in.NotificationRuleParams.Validate(v)
	in.validate(v)
}

// NotificationRuleOut is a rule.
type NotificationRuleOut struct {
	ID              uuid.UUID   `json:"id"`
	Scope           string      `json:"scope" doc:"project or organization"`
	Name            string      `json:"name"`
	Enabled         bool        `json:"enabled"`
	Events          []string    `json:"events"`
	Environments    []string    `json:"environments"`
	Channels        []uuid.UUID `json:"channels"`
	CooldownMinutes int         `json:"cooldown_minutes"`
	MutedUntil      *time.Time  `json:"muted_until,omitzero"`
	LastFiredAt     *time.Time  `json:"last_fired_at,omitzero"`
	CreatedAt       time.Time   `json:"created_at"`
	CreatedBy       string      `json:"created_by,omitzero"`
	UpdatedBy       string      `json:"updated_by,omitzero"`
}

// NotificationRuleListOut is a scope's rules.
type NotificationRuleListOut struct {
	Rules []NotificationRuleOut `json:"rules"`
}

// NotificationRuleTestOut is what a rule's test sent, to each of its channels.
type NotificationRuleTestOut struct {
	Type     string                       `json:"type" doc:"The event type the sample was made of"`
	Channels []NotificationRuleTestResult `json:"channels"`
}

// NotificationRuleTestResult is one channel's answer to a rule's test.
type NotificationRuleTestResult struct {
	ChannelID uuid.UUID `json:"channel_id"`
	Name      string    `json:"name"`
	OK        bool      `json:"ok"`
	Detail    string    `json:"detail"`
}

// OrgChannelOut is a channel an organization's rule may notify: any of its projects'.
type OrgChannelOut struct {
	ID      uuid.UUID `json:"id"`
	Type    string    `json:"type"`
	Name    string    `json:"name"`
	Enabled bool      `json:"enabled"`
	Project string    `json:"project" doc:"The project it is set up in"`
}

// OrgChannelListOut is every channel in the organization's projects.
type OrgChannelListOut struct {
	Channels []OrgChannelOut `json:"channels"`
}
