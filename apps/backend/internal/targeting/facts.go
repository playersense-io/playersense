package targeting

import "strings"

// The namespaces a rule may name, and who is allowed to say what a value is.
//
// This is the whole of §41's flexibility question. A studio does not enumerate its segments
// from a list we wrote — it composes rules over these — and the split is by *authority*
// rather than by subject, because that is the distinction a dashboard has to show when
// somebody writes "give the legendary sword if lifetime spend is over $100".
const (
	// NamespaceContext is the studio's own vocabulary, set through the SDK. Arbitrary, and
	// **client supplied**: a modified build can claim any value it likes. Free to evaluate,
	// because it arrives with the request.
	NamespaceContext = "context."

	// NamespaceGeo and NamespaceNetwork are derived from the connection (ADR 0027). Free,
	// and unforgeable — the client never sends them.
	NamespaceGeo     = "geo."
	NamespaceNetwork = "network."

	// NamespaceApp, NamespaceDevice and NamespaceSDK come from the SDK itself. Client
	// supplied, but not studio-defined: PlayerSense owns the key names.
	NamespaceApp    = "app."
	NamespaceDevice = "device."
	NamespaceSDK    = "sdk."

	// NamespaceCommerce and NamespaceActivity are what PlayerSense knows about a player
	// from their own history. **Server authoritative**, and the only namespaces safe for a
	// rule that grants something of value.
	//
	// They are the answer to "how do I segment whales?". A studio does not get to define a
	// new *fact* — that would be an arbitrary query over the event stream, which is a
	// reporting engine rather than a targeting one — but it composes any rule it likes over
	// these, and a rule is what a segment is.
	NamespaceCommerce = "commerce."
	NamespaceActivity = "activity."
)

// The server-authoritative fact vocabulary, in full.
//
// Deliberately small and closed, and split by *subject* because the two answer different
// questions and a studio must not have to guess which one it got:
//
//	commerce.install.*   this installation. What `player_lifetime` has always keyed on
//	                     (§29, ADR 0004): a reinstall is a new install, and a player with a
//	                     phone and a tablet is two of these.
//	commerce.player.*    this account, across their installations. Only exists once they
//	                     have identified themselves with SetPlayer.
//
// "Whales", "paid users" and "returning users" are not in this list, and that is the point.
// They are *rules* a studio writes over it, and every studio draws those lines somewhere
// different: one calls $100 a whale, the next calls it $1000. The vocabulary is ours and
// closed; the thresholds and the combinations are theirs and arbitrary.
//
// Every one of these is already derived for §29's cohort reporting, so a fact costs nothing
// new to compute — and a closed set is what keeps the per-player record small enough to hold
// in Valkey for every active player in a cell.
const (
	FactInstallSpend    = "commerce.install.lifetime_spend_micros"
	FactInstallPurchase = "commerce.install.purchases"
	FactPlayerSpend     = "commerce.player.lifetime_spend_micros"
	FactPlayerPurchase  = "commerce.player.purchases"

	// Nullable in storage and therefore absent here for a player who has never bought
	// (ADR 0031). "Has not bought in 30 days" and "has never bought" are different
	// audiences, and a sentinel date would have merged them.
	FactInstallFirstPurchase = "commerce.install.days_since_first_purchase"
	FactInstallLastPurchase  = "commerce.install.days_since_last_purchase"
	FactPlayerFirstPurchase  = "commerce.player.days_since_first_purchase"
	FactPlayerLastPurchase   = "commerce.player.days_since_last_purchase"

	FactInstallDaysActive    = "activity.install.days_active"
	FactInstallSessions      = "activity.install.sessions"
	FactInstallPlaytime      = "activity.install.playtime_seconds"
	FactInstallAge           = "activity.install.days_since_install"
	FactInstallDaysSinceSeen = "activity.install.days_since_last_seen"

	FactPlayerDaysActive    = "activity.player.days_active"
	FactPlayerSessions      = "activity.player.sessions"
	FactPlayerPlaytime      = "activity.player.playtime_seconds"
	FactPlayerAge           = "activity.player.days_since_install"
	FactPlayerDaysSinceSeen = "activity.player.days_since_last_seen"
)

// Facts that are declared and have no column behind them yet.
//
// Empty, and kept rather than deleted: it is the mechanism, and the next fact somebody
// declares ahead of its migration belongs here. A key in this map is refused when a rule is
// written, because the failure this repository keeps finding is a vocabulary that accepts a
// key and then silently never matches — a campaign that reaches nobody and gives no reason.
var Unavailable = map[string]string{}

// Needs reports which namespaces a rule reads.
//
// This is the optimisation that keeps a config request cheap. Most flags are fully rolled
// out with no audience at all, and most audiences are geography or app version — none of
// which need a lookup. Only a rule that actually names `commerce.` or `activity.` costs a
// round trip to fetch the player's facts, and the compiled document carries the answer so
// the decision is made once at publish rather than per request.
//
// At §4.2's ceiling that is the difference between 230 lookups a second and none.
func (r Rule) Needs() map[string]bool {
	needs := map[string]bool{}
	r.collect(needs)
	return needs
}

func (r Rule) collect(into map[string]bool) {
	for _, child := range r.All {
		child.collect(into)
	}
	for _, child := range r.Any {
		child.collect(into)
	}
	if r.Not != nil {
		r.Not.collect(into)
	}
	if r.Key == "" {
		return
	}
	if at := strings.Index(r.Key, "."); at >= 0 {
		into[r.Key[:at+1]] = true
	}
}

// NeedsPlayerFacts reports whether evaluating this rule requires the server-authoritative
// record, and therefore a lookup.
func (r Rule) NeedsPlayerFacts() bool {
	needs := r.Needs()
	return needs[NamespaceCommerce] || needs[NamespaceActivity]
}

// ClientSupplied reports the namespaces in this rule that a modified build could choose.
//
// Not used to refuse anything — a studio may legitimately target `context.rank`, and most
// do. It is what a dashboard needs to show the warning §2 of the context document asks for,
// at the moment somebody writes the rule rather than after they have shipped it:
//
//	⚠ context.progress.level is client supplied. Not suitable for rules that grant value.
func (r Rule) ClientSupplied() []string {
	needs := r.Needs()
	var found []string
	for _, namespace := range []string{
		NamespaceContext, NamespaceApp, NamespaceDevice, NamespaceSDK,
	} {
		if needs[namespace] {
			found = append(found, strings.TrimSuffix(namespace, "."))
		}
	}
	return found
}
