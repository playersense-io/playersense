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

// ---- revenue and purchases --------------------------------------------------

// RevenueFilter narrows revenue by what was bought and where. Each is a comma-separated list.
type RevenueFilter struct {
	Store   string `query:"store,omitzero" doc:"Comma-separated stores: app_store, google_play, or what the game reported"`
	Country string `query:"country,omitzero" doc:"Comma-separated ISO country codes"`
	Product string `query:"product,omitzero" doc:"Comma-separated product ids"`
}

// Lists splits the filter into its values.
func (f RevenueFilter) Lists() (stores, countries, products []string) {
	return splitList(f.Store), splitList(f.Country), splitList(f.Product)
}

func (f *RevenueFilter) validateFilter(v *muzak.Validation) {
	v.String(&f.Store).Trim().MaxLen(512)
	v.String(&f.Country).Trim().Upper().MaxLen(512)
	v.String(&f.Product).Trim().MaxLen(2048)
}

// RevenueParams asks for the Revenue page.
type RevenueParams struct {
	AnalyticsScope
	AnalyticsRange
	RevenueFilter
	Timezone string `query:"tz,omitzero" doc:"IANA timezone for the weekday-by-hour heatmap; defaults to UTC"`
}

func (in *RevenueParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
	in.validateFilter(v)
	v.String(&in.Timezone).Trim().MaxLen(64)
}

// RevenueOut is the Revenue page.
//
// Revenue the game reported with TrackRevenue, not receipts verified with a store (§46 is not
// built), and in the currency it was reported in: there is no conversion, so every figure that
// sums money says which currencies went into it.
type RevenueOut struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Verified   bool      `json:"verified" doc:"Always false: these are purchases the game reported, not store receipts"`
	Currencies []string  `json:"currencies" doc:"Every currency summed into these figures"`

	Totals RevenueTotalsOut `json:"totals"`
	Days   []RevenueDayOut  `json:"days"`

	Products  []RevenueSliceOut `json:"products"`
	Stores    []RevenueSliceOut `json:"stores"`
	Countries []RevenueSliceOut `json:"countries"`

	PricePoints []PricePointOut `json:"price_points"`

	// NewPayers is each day's first-time payers, and SpenderTiers splits everybody who bought in
	// the range by what they have spent in their whole life. Neither follows the filters: both
	// are about the payer, not about one product or store.
	NewPayers    []NewPayersOut   `json:"new_payers"`
	SpenderTiers []SpenderTierOut `json:"spender_tiers"`

	// Heatmap is purchases by weekday (Monday first) and hour, in Timezone, unfiltered.
	Heatmap  [7][24]uint64 `json:"heatmap"`
	Timezone string        `json:"timezone"`

	Approximate          bool `json:"approximate" doc:"Buyers are distinct counts from sketches"`
	SuppressedCells      int  `json:"suppressed_cells"`
	SuppressionThreshold int  `json:"suppression_threshold"`
}

// RevenueTotalsOut is the range against the range before it.
type RevenueTotalsOut struct {
	RevenueMicros         int64   `json:"revenue_micros"`
	Purchases             uint64  `json:"purchases"`
	Buyers                *uint64 `json:"buyers,omitzero"`
	PreviousRevenueMicros int64   `json:"previous_revenue_micros"`
	PreviousPurchases     uint64  `json:"previous_purchases"`
	PreviousBuyers        *uint64 `json:"previous_buyers,omitzero"`
}

// RevenueDayOut is one day, and each store's share of it.
//
// A day whose buyers are too few to disclose carries no money: what two people spent is what two
// people spent, whatever else is on the chart.
type RevenueDayOut struct {
	Day           time.Time        `json:"day"`
	RevenueMicros int64            `json:"revenue_micros"`
	Purchases     uint64           `json:"purchases"`
	Withheld      bool             `json:"withheld,omitzero"`
	ByStore       map[string]int64 `json:"by_store,omitzero"`
}

// RevenueSliceOut is one product, store or country. Slices bought by too few to disclose are
// left out and counted in suppressed_cells.
type RevenueSliceOut struct {
	Key                   string `json:"key"`
	RevenueMicros         int64  `json:"revenue_micros"`
	Purchases             uint64 `json:"purchases"`
	Buyers                uint64 `json:"buyers"`
	PreviousRevenueMicros int64  `json:"previous_revenue_micros"`
}

// PricePointOut is how often one price was paid.
type PricePointOut struct {
	PriceMicros   int64  `json:"price_micros"`
	Currency      string `json:"currency"`
	Purchases     uint64 `json:"purchases"`
	RevenueMicros int64  `json:"revenue_micros"`
}

// NewPayersOut is one day's first-time payers.
type NewPayersOut struct {
	Day    time.Time `json:"day"`
	Payers *uint64   `json:"payers,omitzero"`
}

// SpenderTierOut is the payers whose lifetime spend falls in one band.
type SpenderTierOut struct {
	FromMicros    int64   `json:"from_micros"`
	ToMicros      int64   `json:"to_micros,omitzero" doc:"Absent on the last band, which has no end"`
	Payers        *uint64 `json:"payers,omitzero"`
	RevenueMicros *int64  `json:"revenue_micros,omitzero"`
}

// PurchasesParams pages the purchases ledger.
type PurchasesParams struct {
	AnalyticsScope
	AnalyticsRange
	RevenueFilter
	Limit int `query:"limit,omitzero" default:"50" doc:"Newest first, up to 200"`

	// Before is the cursor a previous page returned, so paging is stable while purchases keep
	// arriving.
	Before string `query:"before,omitzero" doc:"The next cursor of the previous page"`
}

func (in *PurchasesParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
	in.validateFilter(v)
	v.Number(&in.Limit).Min(1).Max(200)
	v.String(&in.Before).Trim().MaxLen(96)
}

// PurchasesOut is one page of the ledger.
type PurchasesOut struct {
	Purchases []PurchaseOut `json:"purchases"`
	Next      string        `json:"next,omitzero" doc:"Pass as before for the next page; absent on the last"`
	Verified  bool          `json:"verified" doc:"Always false: these are purchases the game reported"`
}

// PurchaseOut is one purchase the game reported.
type PurchaseOut struct {
	EventID       string    `json:"event_id"`
	At            time.Time `json:"at"`
	InstallID     string    `json:"install_id"`
	PlayerID      string    `json:"player_id,omitzero"`
	SessionID     string    `json:"session_id,omitzero"`
	ProductID     string    `json:"product_id"`
	Store         string    `json:"store"`
	RevenueMicros int64     `json:"revenue_micros"`
	Currency      string    `json:"currency"`
	Platform      string    `json:"platform"`
	Country       string    `json:"country,omitzero"`
	AppVersion    string    `json:"app_version"`

	// FirstPurchase is whether this was the install's first purchase ever.
	FirstPurchase bool `json:"first_purchase"`
}

// ---- flag exposures ---------------------------------------------------------

// FlagExposureListParams asks how often every flag was read.
type FlagExposureListParams struct {
	AnalyticsScope
	AnalyticsRange
}

func (in *FlagExposureListParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
}

// FlagExposureListOut is every flag the game read in the range.
type FlagExposureListOut struct {
	From  time.Time            `json:"from"`
	To    time.Time            `json:"to"`
	Flags []FlagExposureSumOut `json:"flags"`

	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// FlagExposureSumOut is one flag's reads over the range, the range before, and by day.
type FlagExposureSumOut struct {
	Flag              string   `json:"flag"`
	Exposures         uint64   `json:"exposures"`
	PreviousExposures uint64   `json:"previous_exposures"`
	Players           *uint64  `json:"players,omitzero"`
	Daily             []uint64 `json:"daily" doc:"Exposures on each day of the range, in order"`
}

// FlagExposureParams asks about one flag.
type FlagExposureParams struct {
	AnalyticsScope
	AnalyticsRange
	Flag string `path:"flag"`

	// Version narrows to one published config version, which is how a rollout is compared
	// before and after a publish.
	Version int `query:"version,omitzero" doc:"One config version; every version when absent"`
}

func (in *FlagExposureParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
	in.validateRange(v)
	v.String(&in.Flag).Trim().Required().MaxLen(128)
	v.Number(&in.Version).Min(0)
}

// FlagExposureOut is one flag's insights: who saw which value, when, from which version and on
// which build, and how many fell back to the game's own default because nothing was published.
type FlagExposureOut struct {
	Flag string    `json:"flag"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	Exposures uint64 `json:"exposures"`
	// DefaultShare is the share of exposures that got the game's own default, the players who
	// are in no arm of anything.
	DefaultShare float64 `json:"default_share"`

	Values      []FlagValueOut      `json:"values"`
	Days        []FlagValueDayOut   `json:"days"`
	Versions    []FlagVersionOut    `json:"versions"`
	AppVersions []FlagAppVersionOut `json:"app_versions"`

	Approximate          bool `json:"approximate" doc:"Players are distinct counts from sketches"`
	SuppressedCells      int  `json:"suppressed_cells"`
	SuppressionThreshold int  `json:"suppression_threshold"`
}

// FlagValueOut is one value over the range.
type FlagValueOut struct {
	Value     string  `json:"value" doc:"As text, whatever type it was"`
	Published bool    `json:"published" doc:"False for the game's own default"`
	Exposures uint64  `json:"exposures"`
	Players   *uint64 `json:"players,omitzero"`
}

// FlagValueDayOut is one value on one day.
type FlagValueDayOut struct {
	Day       time.Time `json:"day"`
	Value     string    `json:"value"`
	Published bool      `json:"published"`
	Exposures uint64    `json:"exposures"`
	Players   *uint64   `json:"players,omitzero"`
}

// FlagVersionOut is one config version's exposures.
type FlagVersionOut struct {
	ConfigVersion uint32  `json:"config_version"`
	Exposures     uint64  `json:"exposures"`
	Players       *uint64 `json:"players,omitzero"`
}

// FlagAppVersionOut is one build's exposures, and how many of them fell back to the default.
type FlagAppVersionOut struct {
	AppVersion       string  `json:"app_version"`
	Exposures        uint64  `json:"exposures"`
	DefaultExposures uint64  `json:"default_exposures"`
	Players          *uint64 `json:"players,omitzero"`
}

// ---- the organization's overview --------------------------------------------

// ProjectsOverviewParams asks for every project's headline figures.
type ProjectsOverviewParams struct {
	OrgSlug     string `path:"org"`
	Environment string `path:"environment" doc:"development, staging or production"`
	AnalyticsRange
}

func (in *ProjectsOverviewParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Environment).Trim().Lower().Required().OneOf("development", "staging", "production")
	in.validateRange(v)
}

// ProjectsOverviewOut is the Overview page's table of projects: every project the caller may read
// analytics for, in one environment, over the range and the range before it.
type ProjectsOverviewOut struct {
	From     time.Time            `json:"from"`
	To       time.Time            `json:"to"`
	Projects []ProjectOverviewOut `json:"projects"`

	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// ProjectOverviewOut is one project's sums over the range. The player figures are player-days,
// each day's exact actives summed, so a daily average is a division by the days and sessions per
// player per day is sessions over player-days.
type ProjectOverviewOut struct {
	Slug string `json:"slug"`
	Name string `json:"name"`

	PlayerDays         *uint64 `json:"player_days,omitzero"`
	PreviousPlayerDays *uint64 `json:"previous_player_days,omitzero"`
	NewPlayers         uint64  `json:"new_players"`
	Sessions           uint64  `json:"sessions"`
	PlaytimeMs         uint64  `json:"playtime_ms"`
	Events             uint64  `json:"events"`

	RevenueMicros         int64  `json:"revenue_micros"`
	PreviousRevenueMicros int64  `json:"previous_revenue_micros"`
	PayerDays             uint64 `json:"payer_days"`
}

// ---- funnels ----------------------------------------------------------------

// Funnel windows: how long after entering a player may take to finish. A session window
// requires every step inside one session.
var FunnelWindows = map[string]int64{
	"session": 0, "1h": 3600, "1d": 86400, "7d": 7 * 86400, "30d": 30 * 86400,
}

// Funnel bounds (§31, §32): what one exploratory funnel may ask of events_raw.
const (
	MaxFunnelSteps      = 8
	MaxFunnelConditions = 4
	MaxFunnelDays       = 90
	MaxProgressionSpan  = 200
)

// FunnelCondition narrows a step to occurrences whose property holds a value, compared as text.
type FunnelCondition struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// FunnelStep is one event a player must do, in order.
type FunnelStep struct {
	Event string            `json:"event"`
	Where []FunnelCondition `json:"where,omitzero"`
}

// FunnelProgression is one event whose numeric property climbs: a level, a chapter, a rank.
type FunnelProgression struct {
	Event    string            `json:"event"`
	Property string            `json:"property" doc:"An integer property"`
	From     int64             `json:"from"`
	To       int64             `json:"to"`
	Where    []FunnelCondition `json:"where,omitzero"`
}

// FunnelQueryIn is one funnel question (§31).
type FunnelQueryIn struct {
	AnalyticsScope

	Kind   string `json:"kind" doc:"steps or progression"`
	From   string `json:"from" doc:"YYYY-MM-DD; players entering from this day"`
	To     string `json:"to" doc:"YYYY-MM-DD; players entering up to this day"`
	Window string `json:"window,omitzero" doc:"session, 1h, 1d (default), 7d or 30d"`

	Steps       []FunnelStep       `json:"steps,omitzero" doc:"For kind=steps: two to eight steps, in order"`
	Progression *FunnelProgression `json:"progression,omitzero" doc:"For kind=progression"`

	// Breakdown splits a steps funnel by one arrival dimension.
	Breakdown string `json:"breakdown,omitzero" doc:"install_platform, install_country or first_app_version"`
}

func (in *FunnelQueryIn) Validate(v *muzak.Validation) {
	in.validateScope(v)
	v.String(&in.Kind).Trim().Lower().Required().OneOf("steps", "progression")
	v.String(&in.From).Trim().Required().MaxLen(10)
	v.String(&in.To).Trim().Required().MaxLen(10)
	v.String(&in.Window).Trim().Lower().OneOf("session", "1h", "1d", "7d", "30d")
	v.String(&in.Breakdown).Trim().Lower().OneOf(CohortDimensions...)

	conditions := func(where *[]FunnelCondition, at string) {
		v.Slice(where).As(at).MaxItems(MaxFunnelConditions)
		for i := range *where {
			v.String(&(*where)[i].Property).As(at + ".property").Trim().Required().MaxLen(64)
			v.String(&(*where)[i].Value).As(at + ".value").MaxLen(256)
		}
	}
	switch in.Kind {
	case "steps":
		v.Slice(&in.Steps).MinItems(2).MaxItems(MaxFunnelSteps)
		for i := range in.Steps {
			v.String(&in.Steps[i].Event).As("steps.event").Trim().Required().MaxLen(96)
			conditions(&in.Steps[i].Where, "steps.where")
		}
	case "progression":
		if in.Progression == nil {
			v.Reject(&in.Progression, "a progression funnel needs its event, property and range")
			return
		}
		p := in.Progression
		v.String(&p.Event).As("progression.event").Trim().Required().MaxLen(96)
		v.String(&p.Property).As("progression.property").Trim().Required().MaxLen(64)
		if p.To <= p.From || p.To-p.From > MaxProgressionSpan {
			v.Reject(&p.To, "to must be above from, by at most 200")
		}
		conditions(&p.Where, "progression.where")
		if in.Breakdown != "" {
			v.Reject(&in.Breakdown, "a progression funnel is not split")
		}
	}
}

// FunnelOut answers a steps funnel.
type FunnelOut struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Window string    `json:"window"`

	Total FunnelSeriesOut `json:"total"`

	// Breakdown is the same funnel for each value of the dimension asked for, the values with
	// too few entrants to disclose left out.
	Breakdown []FunnelSeriesOut `json:"breakdown,omitzero"`

	// Trend is entrants and converters by the day they entered.
	Trend []FunnelDayOut `json:"trend"`

	// TimeToConvert is converters by how long they took, on a fixed ladder of seconds.
	TimeToConvert   []FunnelBucketOut `json:"time_to_convert"`
	MedianToConvert *float64          `json:"median_to_convert_seconds,omitzero"`

	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// FunnelSeriesOut is one funnel: how many reached each step, and the median wait for each.
type FunnelSeriesOut struct {
	Value   string     `json:"value,omitzero"`
	Players []*uint64  `json:"players" doc:"Reached each step; absent where too few to disclose"`
	Medians []*float64 `json:"median_wait_seconds" doc:"From the step before, for each step after the first"`
}

// FunnelDayOut is one entry day.
type FunnelDayOut struct {
	Day       time.Time `json:"day"`
	Entered   *uint64   `json:"entered,omitzero"`
	Converted *uint64   `json:"converted,omitzero"`
}

// FunnelBucketOut is one rung of the time-to-convert ladder.
type FunnelBucketOut struct {
	FromSeconds int64   `json:"from_seconds"`
	ToSeconds   int64   `json:"to_seconds,omitzero" doc:"Absent on the last rung"`
	Players     *uint64 `json:"players,omitzero"`
}

// ProgressionOut answers a progression funnel: how far players got up the ladder.
type ProgressionOut struct {
	From time.Time           `json:"from"`
	To   time.Time           `json:"to"`
	Rows []ProgressionRowOut `json:"rows"`

	MedianToLast *float64 `json:"median_to_last_seconds,omitzero" doc:"From reaching the first value to reaching the last, for those who did"`

	SuppressedCells      int `json:"suppressed_cells"`
	SuppressionThreshold int `json:"suppression_threshold"`
}

// ProgressionRowOut is one value of the ladder.
type ProgressionRowOut struct {
	Value int64 `json:"value"`

	// Reached is how many got at least this far; Tries how many occurrences at this value per
	// player who had one, which is how hard a level is.
	Reached *uint64  `json:"reached,omitzero"`
	Tries   *float64 `json:"tries,omitzero"`
}

// FunnelAnswerOut is a funnel's answer, whichever kind was asked.
type FunnelAnswerOut struct {
	Kind        string          `json:"kind"`
	Steps       *FunnelOut      `json:"steps,omitzero"`
	Progression *ProgressionOut `json:"progression,omitzero"`
}
