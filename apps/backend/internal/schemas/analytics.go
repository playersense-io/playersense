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
