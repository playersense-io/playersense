package schemas

import (
	"strings"
	"time"

	muzak "muzak.dev/framework"
)

// The explorer's answers: the Events, Sessions, Players, Revenue, Purchases and flag pages
// (project.md §55). Each is one page's worth of facts, read from the summaries built for it.
//
// Every answer that counts people carries suppressed_cells and suppression_threshold, as the
// cohort and series answers do (§32.2): a count below the threshold is withheld and the answer
// says how many were, so a missing number is never mistaken for a zero.

// ---- the request ------------------------------------------------------------

// AnalyticsScope is the path every explorer route shares.
type AnalyticsScope struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Environment string `path:"environment" doc:"development, staging or production"`
}

func (in *AnalyticsScope) validateScope(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().
		OneOf("development", "staging", "production")
}

// AnalyticsRange is a range of whole days, both ends included.
type AnalyticsRange struct {
	From string `query:"from,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to six days before to"`
	To   string `query:"to,omitzero" doc:"YYYY-MM-DD, inclusive. Defaults to today (UTC)"`
}

// MaxExplorerDays bounds an explorer range. A year and a month, the longest a summary behind
// these pages keeps whole (§59): a longer range would thin out for reasons that have nothing to
// do with the game.
const MaxExplorerDays = 400

// Resolve turns the strings into a range, defaulting to the last seven days.
func (in AnalyticsRange) Resolve(now time.Time) (from, to time.Time, err error) {
	to = now.UTC().Truncate(24 * time.Hour)
	if in.To != "" {
		if to, err = time.Parse(time.DateOnly, in.To); err != nil {
			return from, to, muzak.BadRequest("to is not a YYYY-MM-DD date")
		}
	}
	from = to.AddDate(0, 0, -6)
	if in.From != "" {
		if from, err = time.Parse(time.DateOnly, in.From); err != nil {
			return from, to, muzak.BadRequest("from is not a YYYY-MM-DD date")
		}
	}
	if to.Before(from) {
		return from, to, muzak.BadRequest("to is before from")
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > MaxExplorerDays {
		return from, to, muzak.BadRequest("a range covers at most 400 days")
	}
	return from, to, nil
}

func (in *AnalyticsRange) validateRange(v *muzak.Validation) {
	v.String(&in.From).Trim().MaxLen(10)
	v.String(&in.To).Trim().MaxLen(10)
}

// splitList reads a comma-separated query value into its trimmed, non-empty parts.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ---- events -----------------------------------------------------------------

// EventCatalogueParams asks for every event the game sends, over a range.
type EventCatalogueParams struct {
	AnalyticsScope
	AnalyticsRange
}

func (in *EventCatalogueParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
}

// EventCatalogueOut is the Events page: every event name the project has sent.
type EventCatalogueOut struct {
	From   time.Time         `json:"from"`
	To     time.Time         `json:"to"`
	Events []EventSummaryOut `json:"events"`

	// Approximate, because players are distinct counts from sketches: exact below a few
	// thousand, within about one percent above.
	Approximate          bool `json:"approximate"`
	SuppressedCells      int  `json:"suppressed_cells"`
	SuppressionThreshold int  `json:"suppression_threshold"`
}

// EventSummaryOut is one event name over the range, against the range before it.
type EventSummaryOut struct {
	Name string `json:"name"`
	Type string `json:"type" doc:"custom, session, purchase or exposure, as ingest classified it"`

	// FirstSeen and LastSeen are when the gateway first and last accepted one, over the
	// project's whole history rather than the range: "is it still arriving" is about now.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	Events         uint64 `json:"events"`
	PreviousEvents uint64 `json:"previous_events"`

	// Players and PreviousPlayers are absent when too few to disclose (§32.2).
	Players         *uint64 `json:"players,omitzero"`
	PreviousPlayers *uint64 `json:"previous_players,omitzero"`

	// Properties is how many distinct property keys it carried in the range.
	Properties int `json:"properties"`
}

// EventDetailParams asks about one event.
type EventDetailParams struct {
	AnalyticsScope
	AnalyticsRange
	Name string `path:"name"`

	// Interval is hour or day. Hour reads the hourly tier and is bounded to 14 days, which is
	// 336 points; day reads the daily one.
	Interval string `query:"interval,omitzero" doc:"hour or day (default)"`
}

func (in *EventDetailParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
	v.String(&in.Name).Trim().Required().MaxLen(96)
	v.String(&in.Interval).Trim().Lower().OneOf("hour", "day")
}

// EventDetailOut is one event's page.
type EventDetailOut struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Interval  string    `json:"interval"`

	Totals EventTotalsOut   `json:"totals"`
	Series []EventBucketOut `json:"series" doc:"Every bucket of the range, with the same bucket of the range before"`

	// Breakdowns splits the range by the attributes the event arrived with: platform,
	// country and app_version, one at a time, the busiest twenty values of each.
	Breakdowns map[string][]EventSliceOut `json:"breakdowns"`

	Properties []EventPropertyOut `json:"properties"`

	Approximate          bool `json:"approximate"`
	SuppressedCells      int  `json:"suppressed_cells"`
	SuppressionThreshold int  `json:"suppression_threshold"`
}

// EventTotalsOut is the range and the one before it.
type EventTotalsOut struct {
	Events          uint64  `json:"events"`
	PreviousEvents  uint64  `json:"previous_events"`
	Players         *uint64 `json:"players,omitzero"`
	PreviousPlayers *uint64 `json:"previous_players,omitzero"`
}

// EventBucketOut is one hour or day, and the matching one of the previous range.
type EventBucketOut struct {
	At             time.Time `json:"at"`
	Events         uint64    `json:"events"`
	PreviousEvents uint64    `json:"previous_events"`
	Players        *uint64   `json:"players,omitzero"`
}

// EventSliceOut is one value of one attribute.
type EventSliceOut struct {
	Value   string  `json:"value"`
	Events  uint64  `json:"events"`
	Players *uint64 `json:"players,omitzero"`
}

// EventPropertyOut is what one property held over the range.
type EventPropertyOut struct {
	Key  string `json:"key"`
	Type string `json:"type" doc:"string, int, float or bool"`

	Occurrences uint64  `json:"occurrences"`
	Fill        float64 `json:"fill" doc:"The share of the event's occurrences that carried it"`
	Distinct    uint64  `json:"distinct" doc:"Distinct values, from a sketch"`

	Top []TopValueOut `json:"top"`
}

// TopValueOut is one of a property's most frequent values.
type TopValueOut struct {
	Value string  `json:"value"`
	Count uint64  `json:"count"`
	Share float64 `json:"share" doc:"Of the property's occurrences"`
}

// EventOccurrencesParams asks for an event's latest occurrences.
type EventOccurrencesParams struct {
	AnalyticsScope
	Name  string `path:"name"`
	Limit int    `query:"limit,omitzero" default:"25" doc:"Newest first, up to 100"`
}

func (in *EventOccurrencesParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.Name).Trim().Required().MaxLen(96)
	v.Number(&in.Limit).Min(1).Max(100)
}

// EventOccurrencesOut is the newest occurrences, from the last seven days.
type EventOccurrencesOut struct {
	Occurrences []EventOccurrenceOut `json:"occurrences"`
}

// EventOccurrenceOut is one event as it was stored.
type EventOccurrenceOut struct {
	EventID    string         `json:"event_id"`
	Name       string         `json:"name"`
	At         time.Time      `json:"at"`
	ReceivedAt time.Time      `json:"received_at"`
	InstallID  string         `json:"install_id"`
	PlayerID   string         `json:"player_id,omitzero"`
	SessionID  string         `json:"session_id,omitzero"`
	Platform   string         `json:"platform"`
	AppVersion string         `json:"app_version"`
	Country    string         `json:"country,omitzero"`
	Revenue    int64          `json:"revenue_micros,omitzero"`
	Currency   string         `json:"currency,omitzero"`
	Properties map[string]any `json:"properties,omitzero"`
}

// ---- sessions ---------------------------------------------------------------

// SessionsParams asks for the Sessions page.
type SessionsParams struct {
	AnalyticsScope
	AnalyticsRange

	// Timezone lays out the heatmap: "when players play" is a question about the players' day,
	// not UTC's. An IANA name.
	Timezone string `query:"tz,omitzero" doc:"IANA timezone for the weekday-by-hour heatmap; defaults to UTC"`
}

func (in *SessionsParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
	v.String(&in.Timezone).Trim().MaxLen(64)
}

// SessionsOut is the Sessions page.
//
// Built from sessions_daily, which counts a session once its day is over: CompleteThrough is the
// last day it holds, and a day after it is absent rather than short.
type SessionsOut struct {
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	CompleteThrough time.Time `json:"complete_through,omitzero" doc:"The last whole day the session facts hold"`
	ComputedAt      time.Time `json:"computed_at,omitzero"`

	Totals SessionTotalsOut `json:"totals"`
	Days   []SessionDayOut  `json:"days"`

	// Lengths is the whole ladder, from under 30 seconds to over four hours, so a chart can
	// group it however it likes. Bucket i runs from FromMs up to ToMs; the last has no end.
	Lengths   []SessionLengthOut   `json:"lengths"`
	Outcomes  []SessionOutcomeOut  `json:"outcomes"`
	Platforms []SessionPlatformOut `json:"platforms"`

	// Heatmap is session starts by weekday (Monday first) and hour, in Timezone.
	Heatmap  [7][24]uint64 `json:"heatmap"`
	Timezone string        `json:"timezone"`
}

// SessionTotalsOut is the range against the range before it.
type SessionTotalsOut struct {
	Sessions           uint64 `json:"sessions"`
	PreviousSessions   uint64 `json:"previous_sessions"`
	DurationMs         uint64 `json:"duration_ms"`
	PreviousDurationMs uint64 `json:"previous_duration_ms"`
	PlaytimeMs         uint64 `json:"playtime_ms"`
	Events             uint64 `json:"events"`

	// PlayerDays is the sum of each day's active players, the denominator of sessions per
	// player per day.
	PlayerDays uint64 `json:"player_days"`

	// MedianMs and P90Ms are read off the length ladder, interpolated inside the bucket they
	// fall in, so they are as fine as the ladder and no finer.
	MedianMs uint64 `json:"median_ms"`
	P90Ms    uint64 `json:"p90_ms"`
}

// SessionDayOut is one day, and the matching day of the range before.
type SessionDayOut struct {
	Day                time.Time `json:"day"`
	Sessions           uint64    `json:"sessions"`
	DurationMs         uint64    `json:"duration_ms"`
	PlaytimeMs         uint64    `json:"playtime_ms"`
	ActivePlayers      uint64    `json:"active_players"`
	PreviousSessions   uint64    `json:"previous_sessions"`
	PreviousDurationMs uint64    `json:"previous_duration_ms"`
}

// SessionLengthOut is one rung of the length ladder.
type SessionLengthOut struct {
	FromMs   uint64 `json:"from_ms"`
	ToMs     uint64 `json:"to_ms,omitzero" doc:"Absent on the last rung, which has no end"`
	Sessions uint64 `json:"sessions"`
}

// SessionOutcomeOut is how many sessions ended which way.
type SessionOutcomeOut struct {
	Outcome  string `json:"outcome" doc:"ended (a session_end arrived) or timed_out (none did; the SDK closed it)"`
	Sessions uint64 `json:"sessions"`
}

// SessionPlatformOut is one platform's sessions.
type SessionPlatformOut struct {
	Platform   string `json:"platform"`
	Sessions   uint64 `json:"sessions"`
	DurationMs uint64 `json:"duration_ms"`
	PlaytimeMs uint64 `json:"playtime_ms"`
	PlayerDays uint64 `json:"player_days" doc:"By the platform players installed on"`
}

// RecentSessionsParams asks for the newest sessions.
type RecentSessionsParams struct {
	AnalyticsScope
	Limit int `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`
}

func (in *RecentSessionsParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.Number(&in.Limit).Min(1).Max(200)
}

// RecentSessionsOut is the newest sessions, from today and yesterday.
type RecentSessionsOut struct {
	Sessions []SessionOut `json:"sessions"`
}

// SessionOut is one session as far as it has got.
type SessionOut struct {
	SessionID     string    `json:"session_id"`
	InstallID     string    `json:"install_id"`
	PlayerID      string    `json:"player_id,omitzero"`
	Platform      string    `json:"platform"`
	AppVersion    string    `json:"app_version"`
	Country       string    `json:"country,omitzero"`
	StartedAt     time.Time `json:"started_at"`
	LastEventAt   time.Time `json:"last_event_at"`
	DurationMs    uint64    `json:"duration_ms"`
	Events        uint64    `json:"events"`
	PlaytimeMs    uint64    `json:"playtime_ms"`
	RevenueMicros int64     `json:"revenue_micros,omitzero"`
	Outcome       string    `json:"outcome" doc:"ended, timed_out, or open (an event in the last thirty minutes)"`
}

// ---- players ----------------------------------------------------------------

// Player statuses, by days since the player was last seen. Seven is where lifecycle_daily stops
// calling a returner continuing (rollup.LapseDays), so a player the directory shows as lapsed and
// the lifecycle chart would count as reactivated are the same player.
const (
	PlayerLapsedAfterDays  = 7
	PlayerChurnedAfterDays = 30
)

// PlayerListParams filters and pages the directory.
type PlayerListParams struct {
	AnalyticsScope

	Status   string `query:"status,omitzero" doc:"active (seen within 7 days), lapsed (7 to 30) or churned (over 30)"`
	Platform string `query:"platform,omitzero" doc:"The platform the player installed on"`
	Country  string `query:"country,omitzero" doc:"The country the player installed in"`
	Spend    string `query:"spend,omitzero" doc:"paying or never"`
	Sort     string `query:"sort,omitzero" doc:"last_seen (default), first_seen, revenue, playtime, sessions or active_days; newest or largest first"`
	Limit    int    `query:"limit,omitzero" default:"50" doc:"Up to 200"`
	Offset   int    `query:"offset,omitzero" doc:"Up to 10,000; narrow the filters to reach further"`
}

func (in *PlayerListParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.Status).Trim().Lower().OneOf("active", "lapsed", "churned")
	v.String(&in.Platform).Trim().Lower().MaxLen(32)
	v.String(&in.Country).Trim().Upper().MaxLen(8)
	v.String(&in.Spend).Trim().Lower().OneOf("paying", "never")
	v.String(&in.Sort).Trim().Lower().OneOf("last_seen", "first_seen", "revenue", "playtime", "sessions", "active_days")
	v.Number(&in.Limit).Min(1).Max(200)
	v.Number(&in.Offset).Min(0).Max(10_000)
}

// PlayerListOut is one page of the directory.
//
// Keyed on the install (§29.2, ADR 0004): a reinstall is a new row, and a player with a phone
// and a tablet is two. The attributes are the ones the install arrived with.
type PlayerListOut struct {
	Players    []PlayerSummaryOut `json:"players"`
	NextOffset int                `json:"next_offset,omitzero" doc:"Absent on the last page"`
}

// PlayerSummaryOut is one install's life so far.
type PlayerSummaryOut struct {
	InstallID  string    `json:"install_id"`
	Platform   string    `json:"platform"`
	Country    string    `json:"country,omitzero"`
	AppVersion string    `json:"app_version"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Status     string    `json:"status" doc:"active, lapsed or churned"`

	ActiveDays    uint64 `json:"active_days"`
	Sessions      uint64 `json:"sessions"`
	PlaytimeMs    uint64 `json:"playtime_ms"`
	RevenueMicros int64  `json:"revenue_micros"`
	Purchases     uint64 `json:"purchases"`

	FirstPurchaseAt *time.Time `json:"first_purchase_at,omitzero"`
	LastPurchaseAt  *time.Time `json:"last_purchase_at,omitzero"`
}

// PlayerParams names one player: an install id, or the studio's own player id.
type PlayerParams struct {
	AnalyticsScope
	ID string `path:"id" doc:"An install id (a UUID) or the player id the game signed the player in with"`
}

func (in *PlayerParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.ID).Trim().Required().MaxLen(128)
}

// PlayerProfileOut is one player's page.
//
// An install is one device's life; an account is everything a signed-in player did, across every
// install the account was seen on in the last ninety days. A reinstall without signing in is a new
// install that nothing links back (ADR 0004), and the page cannot join what the game never said
// was the same person.
type PlayerProfileOut struct {
	Kind     string   `json:"kind" doc:"install or account"`
	ID       string   `json:"id"`
	Installs []string `json:"installs" doc:"The installs this profile covers"`

	Summary PlayerSummaryOut `json:"summary"`

	// Days is every day with activity in the last ninety.
	Days []PlayerDayOut `json:"days"`

	// Events is the newest hundred events of the last seven days.
	Events []EventOccurrenceOut `json:"events"`
}

// PlayerDayOut is one day of one player.
type PlayerDayOut struct {
	Day           time.Time `json:"day"`
	Sessions      uint64    `json:"sessions"`
	PlaytimeMs    uint64    `json:"playtime_ms"`
	Events        uint64    `json:"events"`
	RevenueMicros int64     `json:"revenue_micros"`
	Purchases     uint64    `json:"purchases"`
}
