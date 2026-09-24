package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// explorer adds the dashboard's page reads to the analytics router (project.md §55).
//
// GET rather than §57's POST, because each is a page rather than a document: a range, a name, a
// filter or two, all of which fit a query string a browser can bookmark.
//
// Two permissions, and the line between them is whether the answer names a player. The counts,
// the splits and the curves need analytics.read and withhold any count of people below the
// suppression threshold. Anything listing individual players, their identifiers or their events
// needs player.read as well, which a studio can leave out of a role (authz.PlayerRead).
func explorer(r *muzak.Router, base string) {
	unreadable := muzak.WithResponseDoc(http.StatusServiceUnavailable, "The facts could not be read")
	refused := muzak.WithResponseDoc(http.StatusUnprocessableEntity,
		"The facts cannot answer this question, and the body explains why")

	r.Get(base+"/events", handlers.EventCatalogue,
		muzak.Summary("List every event the game sends"),
		muzak.Description("Each event name the project has sent, with its count and players over the "+
			"range and the range before it, when it was first and last accepted, and how many "+
			"property keys it carried. Read from event_names, events_daily and "+
			"event_properties_daily; nothing reads events_raw."),
		core.Require(authz.AnalyticsRead),
		unreadable)

	r.Get(base+"/events/{name}", handlers.EventDetail,
		muzak.Summary("Describe one event"),
		muzak.Description("One event's series against the previous range, its split by the platform, "+
			"country and build it arrived with, and what each of its properties held: how often it "+
			"was filled, how many distinct values it had, and its most frequent ones."),
		core.Require(authz.AnalyticsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "The project has never sent this event"),
		refused, unreadable)

	r.Get(base+"/events/{name}/occurrences", handlers.EventOccurrences,
		muzak.Summary("List an event's latest occurrences"),
		muzak.Description("The newest occurrences from the last seven days, whole: who sent them, "+
			"on what, and every property. Names players, so it needs player.read."),
		core.Require(authz.AnalyticsRead, authz.PlayerRead),
		refused, unreadable)

	r.Get(base+"/sessions", handlers.Sessions,
		muzak.Summary("Describe the game's sessions"),
		muzak.Description("Sessions by day against the previous range, how long they lasted on a fixed "+
			"ladder with the median and 90th percentile read off it, how they ended, by platform, and "+
			"when they start by weekday and hour in the timezone asked for. A day is counted once it "+
			"is over; complete_through says which is the last."),
		core.Require(authz.AnalyticsRead),
		unreadable)

	r.Get(base+"/sessions/recent", handlers.RecentSessions,
		muzak.Summary("List the newest sessions"),
		muzak.Description("Today's sessions, newest first, and yesterday's when today has fewer than "+
			"asked for: who played, on what, for how long, and whether the session is still going. "+
			"Names players, so it needs player.read."),
		core.Require(authz.AnalyticsRead, authz.PlayerRead),
		refused, unreadable)

	r.Get(base+"/players", handlers.Players,
		muzak.Summary("List the game's players"),
		muzak.Description("One page of installs, filtered by status, arrival platform or country, and "+
			"whether they have paid, sorted by when they were last seen or by what they did. Each is "+
			"one install (§29.2): a reinstall is a new one. Needs player.read, and is not served for a "+
			"kids_strict project."),
		core.Require(authz.AnalyticsRead, authz.PlayerRead),
		refused, unreadable)

	r.Get(base+"/players/{id}", handlers.Player,
		muzak.Summary("Describe one player"),
		muzak.Description("An install by its id, or a signed-in player by the id the game gave them, "+
			"across every install they played on in the last ninety days: their life so far, each "+
			"day of the last ninety, and their newest events."),
		core.Require(authz.AnalyticsRead, authz.PlayerRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No such install or player in this project"),
		refused, unreadable)

	r.Get(base+"/revenue", handlers.Revenue,
		muzak.Summary("Describe the game's revenue"),
		muzak.Description("Revenue, purchases and buyers against the previous range, by day and store, "+
			"by product, store and country, by price, the day's first-time payers, spender tiers by "+
			"lifetime spend, and purchases by weekday and hour. Revenue the game reported, in the "+
			"currencies it reported, not receipts verified with a store."),
		core.Require(authz.AnalyticsRead),
		unreadable)

	r.Get(base+"/purchases", handlers.Purchases,
		muzak.Summary("List purchases"),
		muzak.Description("The purchases ledger, newest first, a page at a time: what was bought, "+
			"where, for how much, by whom, and whether it was their first. Names players, so it needs "+
			"player.read."),
		core.Require(authz.AnalyticsRead, authz.PlayerRead),
		refused, unreadable)

	r.Get(base+"/flags/exposures", handlers.FlagExposures,
		muzak.Summary("List how often every flag was read"),
		muzak.Description("Each flag the game recorded an exposure for, with its exposures over the "+
			"range and the range before, its players, and each day. An exposure is the first time a "+
			"session read a flag under a config version (SDK protocol §14)."),
		core.Require(authz.AnalyticsRead),
		unreadable)

	r.Get(base+"/flags/{flag}/exposures", handlers.FlagExposure,
		muzak.Summary("Describe who saw which value of a flag"),
		muzak.Description("Exposures and players by value and day, by config version and by build, and "+
			"the share that fell back to the game's own default because nothing was published."),
		core.Require(authz.AnalyticsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No exposure of this flag in the range"),
		unreadable)

	// The organization's overview names no project, so it is not under base and has no
	// permission guard: the handler keeps the projects the caller holds analytics.read on.
	r.Get("/organizations/{org}/analytics/{environment}/projects", handlers.ProjectsOverview,
		muzak.Summary("Compare every project's headline figures"),
		muzak.Description("Active player-days, new players, sessions, playtime, events and revenue for "+
			"each project you may read analytics for, over the range and the range before, from the "+
			"exact daily metrics."),
		unreadable)

	r.Post(base+"/funnels/query", handlers.QueryFunnel,
		muzak.Summary("Answer a funnel"),
		muzak.Description("A steps funnel (two to eight events in order, within a window or one "+
			"session, optionally split by an arrival dimension) or a progression funnel (how far up "+
			"an integer property players climbed). Read from the step events alone, each player "+
			"decided in one pass over at most a thousand of their events, over at most ninety days "+
			"of entries, and cached. A funnel that would read more than one question may is refused "+
			"with what to narrow."),
		core.Require(authz.AnalyticsRead),
		muzak.MaxBodySize(64<<10),
		refused, unreadable)
}
