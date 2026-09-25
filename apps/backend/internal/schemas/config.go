package schemas

import (
	"fmt"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/protocol"
)

// MaxContextKeys bounds the studio's own vocabulary in one request.
//
// A rule can only name so many facts before nobody can read it, and a context map is
// evaluated in full on every request. The bound is here rather than in the handler because it
// is a property of the wire shape: an SDK sending two hundred keys is misusing the field, and
// finding that out as a 422 naming the field is more useful than a slow response.
const MaxContextKeys = 64

// ConfigIn is a config request as a bindable input.
//
// The wire shape lives in protocol because every SDK implements it; this adds only the rules
// that police it, so neither drifts silently. The same split IngestBatchIn uses.
type ConfigIn struct {
	protocol.ConfigRequest
}

// Validate bounds the request before any of it is trusted.
//
// Shape only. Whether the key is good, whether the environment matches the credential and
// what this player is actually entitled to are decisions the handler makes with the resolved
// tenant — none of them belong in a 422 that would tell an unauthenticated caller about the
// project.
func (in *ConfigIn) Validate(v *muzak.Validation) {
	v.Number(&in.SchemaVersion).Required().Between(1, 1)

	// A UUID because that is what the installation is everywhere else. An SDK that sent
	// eight characters here and a UUID to ingestion would be two different players to two
	// halves of the product.
	v.String(&in.InstallID).Trim().Required().UUID()

	// Not a UUID: it is the studio's own identifier for their own account, and they choose
	// its shape. Bounded so it cannot be used as a place to put a megabyte.
	v.String(&in.PlayerID).Trim().MaxLen(128)

	v.String(&in.Environment).Trim().Lower().
		OneOf("development", "staging", "production")

	v.String(&in.SDK.Name).As("sdk.name").Trim().Lower().Required().MaxLen(32).
		Matches(`^[a-z0-9._-]+$`).
		Message("must be an SDK name such as unity, unreal or javascript")
	v.String(&in.SDK.Version).As("sdk.version").Trim().Required().MaxLen(32)

	v.String(&in.App.Version).As("app.version").Trim().Required().MaxLen(32)
	v.String(&in.App.Build).As("app.build").Trim().MaxLen(32)

	v.String(&in.Device.Platform).As("device.platform").Trim().Lower().Required().
		OneOf("ios", "android", "windows", "macos", "linux", "web", "console")
	v.String(&in.Device.OSVersion).As("device.os_version").Trim().MaxLen(64)
	v.String(&in.Device.Locale).As("device.locale").Trim().MaxLen(24)

	// A cross-field check reads better as an ordinary if than as a rule, which is what
	// Reject is for.
	if len(in.Context) > MaxContextKeys {
		v.Reject(&in.Context, fmt.Sprintf(
			"must hold no more than %d values; a rule nobody can read is a rule nobody "+
				"should write", MaxContextKeys))
	}
}

// PublishConfigIn is a request to compile and publish (§41).
//
// Deliberately tiny. There is nothing to send: the thing being published is whatever the
// studio has already saved, and a publish that took a document in the body would be a way to
// ship configuration that never passed through the dashboard's own validation.
type PublishConfigIn struct {
	// Environment is which of the project's three this goes to. Named rather than
	// defaulted: publishing to production by accident is not a mistake anybody should be
	// able to make by leaving a field out.
	Environment string `json:"environment" path:"environment"`

	// Note is what a person reading the version history a month from now needs.
	Note string `json:"note,omitempty"`
}

func (in *PublishConfigIn) Validate(v *muzak.Validation) {
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
	v.String(&in.Note).Trim().MaxLen(500)
}

// ConfigVersionOut describes what was published.
//
// The counts and the fact flag are here because they are the three things somebody checks
// immediately after pressing the button: how much went out, and whether this publish just
// made every config request cost a lookup it did not cost before.
type ConfigVersionOut struct {
	Version     int    `json:"version"`
	Environment string `json:"environment"`

	Flags  int `json:"flags"`
	Events int `json:"liveops_events"`

	// NeedsPlayerFacts is the one number with a cost behind it: true means some rule in
	// this project now names `commerce.` or `activity.`, so every config request performs
	// a lookup of the player's history where before it performed none.
	NeedsPlayerFacts bool `json:"needs_player_facts"`

	PublishedBy string    `json:"published_by"`
	PublishedAt time.Time `json:"published_at"`
	Note        string    `json:"note,omitempty"`

	RollbackOf int  `json:"rollback_of,omitzero" doc:"A rollback: the earlier version this one republished, unchanged"`
	Current    bool `json:"current,omitzero" doc:"The version being served now"`
}

// ConfigVersionsParams pages an environment's published versions.
type ConfigVersionsParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
	Before      int    `query:"before,omitzero" doc:"Versions below this one: the next cursor of the previous page"`
	Limit       int    `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *ConfigVersionsParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().OneOf("development", "staging", "production")
	v.Number(&in.Before).Min(0)
	v.Number(&in.Limit).Min(1).Max(200)
}

// ConfigVersionListOut is one page of an environment's history.
type ConfigVersionListOut struct {
	Current  int                `json:"current" doc:"The version being served; 0 before the first publish"`
	Versions []ConfigVersionOut `json:"versions"`
	Next     int                `json:"next,omitzero" doc:"Pass as before for the page after this one; absent on the last"`
}

// ConfigVersionParams names one published version.
type ConfigVersionParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
	Version     int    `path:"version"`
}

func (in *ConfigVersionParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().OneOf("development", "staging", "production")
	v.Number(&in.Version).Min(1)
}

// ConfigVersionDocumentOut is one version with the document players were served.
type ConfigVersionDocumentOut struct {
	ConfigVersionOut
	Document map[string]any `json:"document" doc:"The compiled document, exactly as the SDK receives it"`
}

// RollbackConfigIn puts an environment back on an earlier version.
type RollbackConfigIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
	Version     int    `path:"version" doc:"The version to go back to"`
	Reason      string `json:"reason" doc:"Why, for whoever reads the history next"`
}

func (in *RollbackConfigIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().OneOf("development", "staging", "production")
	v.Number(&in.Version).Min(1)
	v.String(&in.Reason).Trim().Required().MaxLen(400)
}

// DraftRestoredOut is what restoring a version into the draft changed.
type DraftRestoredOut struct {
	Environment      string   `json:"environment"`
	Version          int      `json:"version"`
	FlagsRestored    []string `json:"flags_restored"`
	FlagsArchived    []string `json:"flags_archived" doc:"In the draft, not in the version: archived, and back with a restore of a later one"`
	FlagsSkipped     []string `json:"flags_skipped" doc:"Made again since with another kind, which a flag cannot change: left as they are"`
	SegmentsRestored []string `json:"segments_restored"`
}
