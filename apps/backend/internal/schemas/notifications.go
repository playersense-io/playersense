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

// ChannelActivityParams is the window the Activity page asks about.
type ChannelActivityParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Range       string `query:"range,omitzero" default:"7d" doc:"24h (by the hour), 7d or 30d (by the day)"`
}

func (in *ChannelActivityParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Range).Trim().OneOf("", "24h", "7d", "30d")
}

// ActivityTotalsOut is one window's figures. Tests are counted apart from the rest.
type ActivityTotalsOut struct {
	Sent      int `json:"sent" doc:"Sent by rules and reports, tests aside"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Tests     int `json:"tests"`
	P50Ms     int `json:"p50_ms" doc:"Half of the sends, tests included, were answered faster than this"`
	P95Ms     int `json:"p95_ms"`
}

// ActivityBucketOut is one hour or day.
type ActivityBucketOut struct {
	At        time.Time `json:"at"`
	Delivered int       `json:"delivered"`
	Failed    int       `json:"failed"`
	Tests     int       `json:"tests"`
}

// ChannelActivityRowOut is one channel's share of the window.
type ChannelActivityRowOut struct {
	ChannelID *uuid.UUID `json:"channel_id,omitzero" doc:"Absent for a channel deleted since"`
	Name      string     `json:"name"`
	Type      string     `json:"type,omitzero"`
	Sent      int        `json:"sent"`
	Failed    int        `json:"failed"`
	P50Ms     int        `json:"p50_ms"`
	LastAt    *time.Time `json:"last_at,omitzero"`
	LastOK    bool       `json:"last_ok"`
	Spark     []int      `json:"spark" doc:"Sends a bucket, tests aside"`
}

// ActivitySourceOut is what one rule or report sent.
type ActivitySourceOut struct {
	Source string     `json:"source"`
	RuleID *uuid.UUID `json:"rule_id,omitzero"`
	Sent   int        `json:"sent"`
	Failed int        `json:"failed"`
}

// ChannelActivityOut is the Activity page's figures and charts.
type ChannelActivityOut struct {
	Range    string                  `json:"range"`
	Bucket   string                  `json:"bucket" doc:"hour or day"`
	Buckets  []ActivityBucketOut     `json:"buckets"`
	Current  ActivityTotalsOut       `json:"current"`
	Previous ActivityTotalsOut       `json:"previous" doc:"The same length of time just before"`
	Channels []ChannelActivityRowOut `json:"channels"`
	Sources  []ActivitySourceOut     `json:"sources"`
	Waiting  int                     `json:"waiting" doc:"Queued and not tried yet"`
	Retrying int                     `json:"retrying" doc:"Failed at least once and due another try"`
}

// RuleStatOut is one rule's last seven days.
type RuleStatOut struct {
	RuleID uuid.UUID  `json:"rule_id"`
	Sent   int        `json:"sent"`
	Failed int        `json:"failed"`
	LastAt *time.Time `json:"last_at,omitzero"`
	Spark  []int      `json:"spark" doc:"Sends a day, oldest first, tests aside"`
}

// RuleStatsOut is a scope's rules' last seven days.
type RuleStatsOut struct {
	Days  int           `json:"days"`
	Rules []RuleStatOut `json:"rules"`
}
