// Package schemas holds the request and response models the control API exposes.
//
// Response models are deliberately separate from the Ent entities. A column added to an
// entity tomorrow cannot appear in a response, because it is not part of the response
// type and the compiler is what says so rather than a bug report.
package schemas

import (
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// ---- organizations ----------------------------------------------------------

// OrganizationOut is one organization as clients see it.
type OrganizationOut struct {
	ID        uuid.UUID `json:"id" doc:"The organization's identifier"`
	Name      string    `json:"name" doc:"Display name"`
	Slug      string    `json:"slug" doc:"URL-safe short name, unique across PlayerSense"`
	CreatedAt time.Time `json:"created_at"`
}

// OrganizationListOut is a page of organizations.
type OrganizationListOut struct {
	Organizations []OrganizationOut `json:"organizations"`
}

// CreateOrganizationIn is the body for creating an organization.
type CreateOrganizationIn struct {
	Name string `json:"name" doc:"Display name"`
	Slug string `json:"slug" doc:"URL-safe short name; lower case letters, digits and hyphens"`
}

// Validate constrains a new organization to what the store can hold.
func (in *CreateOrganizationIn) Validate(v *muzak.Validation) {
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Slug).Trim().Lower().Required().MinLen(2).MaxLen(64).Slug()
}

// OrganizationParams addresses one organization by slug.
type OrganizationParams struct {
	Slug string `path:"org" doc:"The organization's slug"`
}

func (in *OrganizationParams) Validate(v *muzak.Validation) {
	v.String(&in.Slug).Trim().Lower().Required().MaxLen(64)
}

// ---- projects ---------------------------------------------------------------

// ProjectOut is one project as clients see it.
type ProjectOut struct {
	ID            uuid.UUID        `json:"id"`
	Name          string           `json:"name"`
	Slug          string           `json:"slug"`
	PolicyTier    string           `json:"policy_tier" doc:"standard, child_directed or kids_strict"`
	RetentionDays uint16           `json:"retention_days" doc:"Raw event retention, in days"`
	Environments  []EnvironmentOut `json:"environments,omitzero"`
	CreatedAt     time.Time        `json:"created_at"`

	// Notes carries anything a studio has to know about this project's tier that it
	// cannot infer from the numbers. kids_strict has no persistent identifier, so
	// cross-day retention is not computable for it (§98) — saying so here is cheaper
	// than letting someone discover an empty retention page after integrating.
	Notes []string `json:"notes,omitzero"`
}

// ProjectListOut is a page of projects.
type ProjectListOut struct {
	Projects []ProjectOut `json:"projects"`
}

// EnvironmentOut is one environment of a project.
type EnvironmentOut struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind" doc:"development, staging or production"`
}

// CreateProjectIn is the body for creating a project.
//
// home_region and policy_tier are set here and never again: §75 says a tier change
// applies from the moment it is set and never retroactively, so changing one is a new
// project rather than an update.
type CreateProjectIn struct {
	OrgSlug string `path:"org" doc:"The organization to create the project under"`

	Name       string `json:"name"`
	Slug       string `json:"slug" doc:"URL-safe short name, unique within the organization"`
	PolicyTier string `json:"policy_tier,omitzero" default:"standard" doc:"standard, child_directed or kids_strict"`

	// A pointer, not a uint16. An absent member arrives as zero, and this field's
	// accepted set excludes zero — so a plain number could not tell "the client did not
	// choose" from "the client chose 0", and every request omitting it would be
	// rejected. nil is skipped; an explicit 0 is still refused.
	RetentionDays *uint16 `json:"retention_days,omitzero" doc:"Raw retention; one of 7, 30, 90, 180 or 365. Defaults to 30"`
}

func (in *CreateProjectIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Slug).Trim().Lower().Required().MinLen(2).MaxLen(64).Slug()
	v.String(&in.PolicyTier).Trim().Lower().OneOf("standard", "child_directed", "kids_strict")
	// §24 prefers discrete retention tiers over arbitrary values: fewer combinations,
	// predictable lifecycle, and a partition key that can include the tier.
	v.Number(&in.RetentionDays).OneOf(7, 30, 90, 180, 365)
}

// ProjectParams addresses one project.
type ProjectParams struct {
	OrgSlug     string `path:"org" doc:"The organization's slug"`
	ProjectSlug string `path:"project" doc:"The project's slug"`
}

func (in *ProjectParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// ---- API keys ---------------------------------------------------------------

// APIKeyOut describes a key without disclosing it.
type APIKeyOut struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind" doc:"project, reporting or server"`
	PublicID    string     `json:"public_id" doc:"The public half of the key; not a secret"`
	Name        string     `json:"name,omitzero"`
	Environment string     `json:"environment" doc:"Which environment the key is scoped to"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitzero"`
	RevokedAt   *time.Time `json:"revoked_at,omitzero"`

	CreatedBy  string     `json:"created_by,omitzero"`
	RevokedBy  string     `json:"revoked_by,omitzero"`
	RolledAt   *time.Time `json:"rolled_at,omitzero" doc:"When another key took its place"`
	ReplacedBy *uuid.UUID `json:"replaced_by,omitzero" doc:"The key that took its place"`
	ExpiresAt  *time.Time `json:"expires_at,omitzero" doc:"When a rolled key stops working"`

	// Hourly is requests over the last 24 hours, an hour at a time, oldest first. Filled on the
	// list, where the page draws it.
	Hourly []int64 `json:"hourly,omitzero"`
}

// APIKeyListOut is a page of keys.
type APIKeyListOut struct {
	Keys []APIKeyOut `json:"keys"`
}

// APIKeyCreatedOut is the one and only time the secret is returned.
type APIKeyCreatedOut struct {
	APIKeyOut

	// Secret is the full `<public>:<private>` credential. It is shown once because only
	// an Argon2id digest of the private half is stored, so there is no path by which it
	// could be shown again (§53).
	Secret string `json:"secret" doc:"The full key. Shown once and never again"`
}

// CreateAPIKeyIn is the body for minting a key.
type CreateAPIKeyIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`

	Kind        string `json:"kind" doc:"project for a game binary, reporting for analytics reads, server for server-to-server"`
	Environment string `json:"environment" doc:"development, staging or production"`
	Name        string `json:"name,omitzero" doc:"A label, so a dashboard row is identifiable"`
}

func (in *CreateAPIKeyIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Kind).Trim().Lower().Required().OneOf("project", "reporting", "server")
	v.String(&in.Environment).Trim().Lower().Required().OneOf("development", "staging", "production")
	v.String(&in.Name).Trim().MaxLen(120)
}

// RevokeAPIKeyIn addresses one key for revocation.
type RevokeAPIKeyIn struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	KeyID       uuid.UUID `path:"key_id" doc:"The key to revoke"`
}

func (in *RevokeAPIKeyIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// RollAPIKeyIn replaces a key with a new one of its kind and environment.
type RollAPIKeyIn struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	KeyID       uuid.UUID `path:"key_id" doc:"The key to replace"`
	GraceHours  int       `json:"grace_hours,omitzero" doc:"How long the old key keeps working, 0 to 168; 0 stops it at once"`
}

func (in *RollAPIKeyIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.GraceHours).Min(0).Max(168)
}

// RolledAPIKeyOut is the new key, shown once, and the one it replaced.
type RolledAPIKeyOut struct {
	Key      APIKeyCreatedOut `json:"key"`
	Replaced APIKeyOut        `json:"replaced"`
}

// KeyUsageParams reads one key's usage.
type KeyUsageParams struct {
	OrgSlug     string    `path:"org"`
	ProjectSlug string    `path:"project"`
	KeyID       uuid.UUID `path:"key_id"`
	Hours       int       `query:"hours,omitzero" default:"24" doc:"Up to 48"`
}

func (in *KeyUsageParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.Hours).Min(1).Max(48)
}

// KeyUsageOut is one key's requests by hour.
type KeyUsageOut struct {
	KeyID      uuid.UUID    `json:"key_id"`
	From       time.Time    `json:"from" doc:"The first hour"`
	Hours      []KeyHourOut `json:"hours"`
	Total      int64        `json:"total"`
	LastUsedAt *time.Time   `json:"last_used_at,omitzero"`
}

// KeyHourOut is one hour's requests.
type KeyHourOut struct {
	Hour     time.Time `json:"hour"`
	Requests int64     `json:"requests"`
}

// ---- meta -------------------------------------------------------------------

// HealthOut is the liveness probe's answer.
type HealthOut struct {
	Status string `json:"status"`
}

// UpdateProjectIn renames a project. The slug and the policy tier are fixed at creation.
type UpdateProjectIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Name        string `json:"name"`
}

func (in *UpdateProjectIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Name).Trim().Required().MinLen(1).MaxLen(120)
}

// SetRetentionIn changes a project's raw retention tier.
type SetRetentionIn struct {
	OrgSlug       string `path:"org"`
	ProjectSlug   string `path:"project"`
	RetentionDays uint16 `json:"retention_days" doc:"7, 30, 90, 180 or 365, at most the policy tier's and the plan's"`
}

func (in *SetRetentionIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.Number(&in.RetentionDays).OneOf(7, 30, 90, 180, 365)
}

// ProjectSettingsOut is a project as its settings page shows it.
type ProjectSettingsOut struct {
	ProjectOut

	// RetentionCap is the longest tier this project may choose: the lower of its policy tier's
	// cap and its plan's max_raw_retention_days.
	RetentionCap        int        `json:"retention_cap"`
	RetentionChangedAt  *time.Time `json:"retention_changed_at,omitzero"`
	RetentionPurgeUntil *time.Time `json:"retention_purge_until,omitzero" doc:"While a lowered tier is still being enforced on rows written before it"`

	DeletionAt          *time.Time `json:"deletion_at,omitzero" doc:"When the project is deleted; absent unless asked for"`
	DeletionRequestedBy string     `json:"deletion_requested_by,omitzero"`
}

// ProjectAppsOut is where a project's events come from.
type ProjectAppsOut struct {
	Apps []ProjectAppOut `json:"apps"`
}

// ProjectAppOut is one platform and SDK a project's events arrived from.
type ProjectAppOut struct {
	Platform   string    `json:"platform"`
	SDK        string    `json:"sdk"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Events     uint64    `json:"events"`
	AppVersion string    `json:"app_version" doc:"The newest build seen"`
	SDKVersion string    `json:"sdk_version"`
}
