package schemas

import (
	"encoding/json/jsontext"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/liveops"
)

// The authoring surface: what a studio edits, before a publish turns it into what a player is
// served (§41).
//
// # Why a rule arrives as raw JSON
//
// A rule is a tree over a vocabulary internal/targeting owns, with its own bounds on depth,
// condition count and operators. Declaring it as a Go struct here would put a second, weaker
// description of that grammar in a second package, and the two would drift — the first symptom
// being a rule this package accepted and the compiler refused. So the wire type is
// `jsontext.Value` and the authority is [targeting.Parse], called by the handler.
//
// The cost is that a malformed rule is a 422 from the handler rather than from the binder, and
// its message names the rule rather than the JSON pointer inside it. That is the better error
// anyway: "unknown operator \"greater\"" is what the person typed, and `/rule/all/2/op` is where
// it landed.

// ---- common -----------------------------------------------------------------

// Buckets is a rollout's full range, in hundredths of a percent (§43).
//
// Named here because it appears in three validators and a raw 10000 in each is three chances
// for one of them to become 1000.
const Buckets = 10000

// keyPattern is what a key may be.
//
// Not Slug(), which this used until the first test ran: a slug forbids the underscore, and
// every authored key in this codebase is snake_case — `vip_shop`, `max_level`,
// `month_start_offer`, `seasonal_event`. The validator would have refused the project's own
// examples, and a studio would have discovered it while typing the first flag they ever made.
//
// A dot is allowed because a studio with forty flags namespaces them — `shop.layout`,
// `shop.currency` — and a leading letter or digit is required so that a key is never mistaken
// for a flag, an option or a path segment with a meaning of its own.
const keyPattern = `^[A-Za-z0-9][A-Za-z0-9_.-]*$`

// keyIssue is the sentence somebody typing a key needs, rather than "is not in the expected
// format".
const keyIssue = "may contain only letters, digits, underscores, dots and hyphens, and must " +
	"start with a letter or a digit"

// TargetParams addresses one authored object by its key.
//
// The key is in the path rather than the body, so the URL a dashboard bookmarks is the URL that
// identifies the thing. It is immutable in every one of these tables for the same reason.
type TargetParams struct {
	OrgSlug     string `path:"org" doc:"The organization's slug"`
	ProjectSlug string `path:"project" doc:"The project's slug"`
	Key         string `path:"key" doc:"The object's key, unique within the project"`
}

func (in *TargetParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MaxLen(64)
}

// ---- segments ---------------------------------------------------------------

// SegmentOut is one named audience as clients see it.
type SegmentOut struct {
	ID          uuid.UUID      `json:"id"`
	Key         string         `json:"key" doc:"The stable name rules refer to. Immutable"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Rule        jsontext.Value `json:"rule" doc:"The audience, as internal/targeting parses it"`
	Archived    bool           `json:"archived" doc:"An archived segment is left out of the next publish"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// SegmentListOut is a project's segments.
type SegmentListOut struct {
	Segments []SegmentOut `json:"segments"`
}

// CreateSegmentIn is the body for naming an audience.
type CreateSegmentIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`

	Key         string         `json:"key" doc:"The stable name rules will refer to. Cannot be changed afterwards"`
	Name        string         `json:"name" doc:"Display name"`
	Description string         `json:"description,omitzero"`
	Rule        jsontext.Value `json:"rule" doc:"The audience. An empty rule matches every player"`
}

func (in *CreateSegmentIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	// Not Lower(): a key is what a studio typed, and `vipPlayers` is theirs to spell.
	v.String(&in.Key).Trim().Required().MinLen(2).MaxLen(64).
		Matches(keyPattern).Message(keyIssue)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
}

// UpdateSegmentIn replaces a segment's editable fields.
//
// A whole-object write rather than a patch, which is the choice that matters for a rule: a
// partial update of a tree means addressing nodes inside it, and two people editing one
// audience would then merge into a rule neither of them wrote. A replace makes the last write
// win, visibly.
type UpdateSegmentIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Key         string `path:"key"`

	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Rule        jsontext.Value `json:"rule"`

	// A pointer, so that absent means "leave the archive state alone".
	//
	// A plain bool would resurrect things. DELETE archives a segment; a later PUT correcting
	// its name, sent by a dashboard that did not think to echo the field back, would then
	// silently return it to the next publish — a change to a live configuration that nobody
	// asked for and no response mentions. Archiving is a lifecycle gesture, not a field
	// somebody edits by omission, so un-archiving has to be said out loud.
	Archived *bool `json:"archived,omitzero" doc:"Absent leaves it as it is; false un-archives"`
}

func (in *UpdateSegmentIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MaxLen(64)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
}

// ---- feature flags ----------------------------------------------------------

// FlagOut is one flag as clients see it.
type FlagOut struct {
	ID          uuid.UUID      `json:"id"`
	Key         string         `json:"key" doc:"What the game reads. Immutable"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Kind        string         `json:"kind" doc:"bool, string, number or json. Immutable"`
	Fallback    jsontext.Value `json:"fallback" doc:"What a player gets when no rule matches, and what the SDK uses offline"`
	Rules       []VariantOut   `json:"rules" doc:"Ordered; the first whose audience matches wins"`
	Rollout     int            `json:"rollout" doc:"Hundredths of a percent: 2500 is 25%"`
	Salt        string         `json:"salt,omitzero" doc:"Changing it draws a fresh sample"`
	Archived    bool           `json:"archived"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// VariantOut is one of a flag's ordered rules.
type VariantOut struct {
	Audience jsontext.Value `json:"audience,omitzero" doc:"Who this value is for. Absent means everybody the flag reaches"`
	Value    jsontext.Value `json:"value" doc:"What they get. Must match the flag's kind"`
	Rollout  int            `json:"rollout" doc:"This variant's own share, within the audience"`
}

// FlagListOut is a project's flags.
type FlagListOut struct {
	Flags []FlagOut `json:"flags"`
}

// VariantIn is one authored rule of a flag.
type VariantIn struct {
	Audience jsontext.Value `json:"audience,omitzero"`
	Value    jsontext.Value `json:"value"`

	// Absent means the whole audience, which is what a plain override is. A pointer so
	// that an explicit 0 — a variant deliberately switched off while its audience is kept —
	// is not read as "not chosen".
	Rollout *int `json:"rollout,omitzero" doc:"Hundredths of a percent; defaults to the whole audience"`
}

func (in *VariantIn) Validate(v *muzak.Validation) {
	v.Number(&in.Rollout).Min(0).Max(Buckets)
}

// CreateFlagIn is the body for declaring a flag.
type CreateFlagIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`

	Key         string `json:"key" doc:"What the game reads. Cannot be changed afterwards"`
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`

	// Immutable once set, and the reason is the SDK: a game calls FlagBool on this key, so
	// a kind that changed would be a published document the installed build cannot read.
	// Changing one is a new flag.
	Kind     string         `json:"kind" doc:"bool, string, number or json. Cannot be changed afterwards"`
	Fallback jsontext.Value `json:"fallback" doc:"Required: a flag with no answer makes the game depend on the network"`

	Rules []VariantIn `json:"rules,omitzero"`

	// A pointer for the same reason retention_days is one: absent means "the whole game",
	// and an explicit 0 means a flag that is live but reaching nobody yet, which is a real
	// thing a studio sets on purpose before a launch.
	Rollout *int   `json:"rollout,omitzero" doc:"Hundredths of a percent; defaults to 10000, the whole game"`
	Salt    string `json:"salt,omitzero"`
}

func (in *CreateFlagIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MinLen(2).MaxLen(64).
		Matches(keyPattern).Message(keyIssue)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
	v.String(&in.Kind).Trim().Lower().Required().OneOf("bool", "string", "number", "json")
	v.Number(&in.Rollout).Min(0).Max(Buckets)
	v.String(&in.Salt).Trim().MaxLen(64)
	for i := range in.Rules {
		v.Nested(&in.Rules[i])
	}
}

// UpdateFlagIn replaces a flag's editable fields.
//
// `kind` is absent rather than ignored. A field a caller may send and that has no effect is
// worse than one that does not exist: somebody will send it, watch the response come back 200,
// and believe the kind changed.
type UpdateFlagIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Key         string `path:"key"`

	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	Fallback    jsontext.Value `json:"fallback"`
	Rules       []VariantIn    `json:"rules,omitzero"`
	Rollout     *int           `json:"rollout,omitzero"`
	Salt        string         `json:"salt,omitzero"`

	// A pointer for the reason [UpdateSegmentIn.Archived] is one, and it matters more here:
	// a flag quietly returning to the next publish is a value a shipped game starts reading
	// again.
	Archived *bool `json:"archived,omitzero" doc:"Absent leaves it as it is; false un-archives"`
}

func (in *UpdateFlagIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MaxLen(64)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Description).Trim().MaxLen(500)
	v.Number(&in.Rollout).Min(0).Max(Buckets)
	v.String(&in.Salt).Trim().MaxLen(64)
	for i := range in.Rules {
		v.Nested(&in.Rules[i])
	}
}

// ---- LiveOps events ---------------------------------------------------------

// LiveOpsEventOut is one scheduled event as clients see it.
type LiveOpsEventOut struct {
	ID       uuid.UUID   `json:"id"`
	Key      string      `json:"key" doc:"Immutable"`
	Kind     string      `json:"kind" doc:"The studio's own type: seasonal_event, offer, announcement"`
	Name     string      `json:"name"`
	Status   string      `json:"status" doc:"draft, active, paused or archived"`
	Schedule ScheduleOut `json:"schedule"`
	Priority int         `json:"priority" doc:"Higher wins when two events of one kind are both on"`

	Audience jsontext.Value `json:"audience,omitzero"`
	Payload  jsontext.Value `json:"payload" doc:"Opaque to PlayerSense; the studio's own shape"`
	Rollout  int            `json:"rollout"`
	Salt     string         `json:"salt,omitzero"`

	// Occurrences is what the schedule means, which is not the same as what it says.
	//
	// "The first five days of every month" is two numbers and a mode, and nobody can read
	// back from those whether they got it right. A studio about to publish an event to a
	// live game needs to see dates, so the next few are computed and returned — the same
	// expansion the config path performs, so what is shown is what a player will get.
	Occurrences []WindowOut `json:"occurrences,omitzero" doc:"The next few concrete windows this schedule produces"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ScheduleOut is when an event is on.
type ScheduleOut struct {
	Mode     string         `json:"mode" doc:"once, hourly, daily, weekly, monthly or yearly"`
	Anchor   time.Time      `json:"anchor" doc:"The first instant it is on; its date parts are what recurs"`
	Duration string         `json:"duration" doc:"How long one occurrence lasts, as a Go duration: 2h, 120h"`
	Interval int            `json:"interval,omitzero" doc:"Periods to skip: 3 with daily is every third day"`
	Until    *time.Time     `json:"until,omitzero" doc:"When repetition stops. Absent means evergreen"`
	Weekdays []time.Weekday `json:"weekdays,omitzero" doc:"0 is Sunday. Empty means the anchor's own weekday"`
	Timezone string         `json:"timezone,omitzero" doc:"An IANA name. Empty means UTC"`
}

// WindowOut is one concrete occurrence.
type WindowOut struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end" doc:"Exclusive, so two back-to-back occurrences never claim one instant"`
}

// LiveOpsEventListOut is a project's events.
type LiveOpsEventListOut struct {
	Events []LiveOpsEventOut `json:"liveops_events"`
}

// ScheduleIn is the authored schedule.
//
// `duration` is a string rather than a number because the unit is the whole question: a studio
// typing 5 for "five days" and a server reading nanoseconds is a happy-hour event that lasts
// five billionths of a second, and it would publish without complaint. "120h" cannot be
// misread.
type ScheduleIn struct {
	Mode     string         `json:"mode"`
	Anchor   time.Time      `json:"anchor"`
	Duration string         `json:"duration" doc:"A Go duration: 2h, 30m, 120h"`
	Interval int            `json:"interval,omitzero"`
	Until    *time.Time     `json:"until,omitzero"`
	Weekdays []time.Weekday `json:"weekdays,omitzero"`
	Timezone string         `json:"timezone,omitzero"`
}

func (in *ScheduleIn) Validate(v *muzak.Validation) {
	v.String(&in.Mode).Trim().Lower().Required().
		OneOf("once", "hourly", "daily", "weekly", "monthly", "yearly")
	v.String(&in.Duration).Trim().Required().MaxLen(32)
	v.Number(&in.Interval).Min(0)
	v.String(&in.Timezone).Trim().MaxLen(64)
	// Bounds only. Whether the weekday set makes sense for the mode is
	// [liveops.Schedule.Validate]'s question, and asking it twice is how two answers
	// eventually disagree.
	for i := range in.Weekdays {
		v.Number(&in.Weekdays[i]).Min(0).Max(6)
	}
}

// CreateLiveOpsEventIn is the body for scheduling an event.
type CreateLiveOpsEventIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`

	Key  string `json:"key" doc:"Immutable"`
	Kind string `json:"kind" doc:"The studio's own type. Not an enum: PlayerSense does not know what kinds of thing a game has"`
	Name string `json:"name"`

	// Absent means draft, which is what a new event should be: publishing reads only the
	// active ones, so an event cannot reach a player by being created.
	Status string `json:"status,omitzero" default:"draft" doc:"draft, active, paused or archived. Defaults to draft"`

	Schedule ScheduleIn     `json:"schedule"`
	Priority int            `json:"priority,omitzero"`
	Audience jsontext.Value `json:"audience,omitzero" doc:"Absent means every player"`
	Payload  jsontext.Value `json:"payload" doc:"Whatever the game needs. PlayerSense does not read it"`
	Rollout  *int           `json:"rollout,omitzero"`
	Salt     string         `json:"salt,omitzero"`
}

func (in *CreateLiveOpsEventIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MinLen(2).MaxLen(64).
		Matches(keyPattern).Message(keyIssue)
	v.String(&in.Kind).Trim().Required().MinLen(2).MaxLen(64).
		Matches(keyPattern).Message(keyIssue)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Status).Trim().Lower().OneOf("draft", "active", "paused", "archived")
	v.Number(&in.Rollout).Min(0).Max(Buckets)
	v.String(&in.Salt).Trim().MaxLen(64)
	v.Nested(&in.Schedule)
}

// UpdateLiveOpsEventIn replaces an event's editable fields.
type UpdateLiveOpsEventIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Key         string `path:"key"`

	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status,omitzero" default:"draft"`

	Schedule ScheduleIn     `json:"schedule"`
	Priority int            `json:"priority,omitzero"`
	Audience jsontext.Value `json:"audience,omitzero"`
	Payload  jsontext.Value `json:"payload"`
	Rollout  *int           `json:"rollout,omitzero"`
	Salt     string         `json:"salt,omitzero"`
}

func (in *UpdateLiveOpsEventIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Key).Trim().Required().MaxLen(64)
	v.String(&in.Kind).Trim().Required().MinLen(2).MaxLen(64).
		Matches(keyPattern).Message(keyIssue)
	v.String(&in.Name).Trim().Required().MinLen(2).MaxLen(120)
	v.String(&in.Status).Trim().Lower().OneOf("draft", "active", "paused", "archived")
	v.Number(&in.Rollout).Min(0).Max(Buckets)
	v.String(&in.Salt).Trim().MaxLen(64)
	v.Nested(&in.Schedule)
}

// ---- drafts -----------------------------------------------------------------

// DraftOut is what the next publish would produce, without producing it.
//
// The publish is the irreversible half and the permission for it is separate (§41), so the
// person who presses it is often not the person who wrote the change. This is what they read
// first: whether it compiles, what is in it, and what is being left out.
type DraftOut struct {
	Environment string `json:"environment"`

	// PublishedVersion is what players have now; 0 if nothing has ever been published.
	PublishedVersion int `json:"published_version"`
	NextVersion      int `json:"next_version"`

	Compiles bool   `json:"compiles" doc:"Whether a publish would succeed"`
	Problem  string `json:"problem,omitzero" doc:"Why it would not, naming the flag or event"`

	Flags            int  `json:"flags" doc:"Flags the next version would carry"`
	Events           int  `json:"liveops_events" doc:"Active LiveOps events it would carry"`
	NeedsPlayerFacts bool `json:"needs_player_facts" doc:"Whether serving it costs a history lookup per request"`

	// Excluded says what is deliberately not in the draft, because "my event is missing"
	// is otherwise indistinguishable from a bug. An archived segment, an archived flag and
	// a draft or paused event are all left out on purpose, and each is named.
	Excluded []ExcludedOut `json:"excluded,omitzero"`
}

// ExcludedOut is one object the next publish would leave out, and why.
type ExcludedOut struct {
	Kind   string `json:"kind" doc:"segment, flag or liveops_event"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// DraftParams addresses one environment's draft.
//
// Its own type rather than [PublishConfigIn], which the publish route uses: that carries a
// `note`, and a note on a GET is a member somebody will fill in expecting it to be recorded.
type DraftParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
}

func (in *DraftParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
}

// Schedule converts an authored schedule into the one internal/liveops evaluates.
//
// The duration is parsed here and the error is the caller's to report, because this is the one
// field whose text can be wrong in a way validation cannot see: MaxLen accepts "2 hours", and
// only a parse knows it is not a duration.
func (in ScheduleIn) Schedule() (liveops.Schedule, error) {
	duration, err := time.ParseDuration(in.Duration)
	if err != nil {
		return liveops.Schedule{}, err
	}
	out := liveops.Schedule{
		Mode:     liveops.Mode(in.Mode),
		Anchor:   in.Anchor,
		Duration: duration,
		Interval: in.Interval,
		Weekdays: in.Weekdays,
		Timezone: in.Timezone,
	}
	if in.Until != nil {
		out.Until = *in.Until
	}
	return out, nil
}

// ScheduleFrom is the reverse, for a response.
func ScheduleFrom(s liveops.Schedule) ScheduleOut {
	out := ScheduleOut{
		Mode:     string(s.Mode),
		Anchor:   s.Anchor,
		Duration: s.Duration.String(),
		Interval: s.Interval,
		Weekdays: s.Weekdays,
		Timezone: s.Timezone,
	}
	if !s.Until.IsZero() {
		until := s.Until
		out.Until = &until
	}
	return out
}
