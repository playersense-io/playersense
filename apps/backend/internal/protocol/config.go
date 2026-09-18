package protocol

// ConfigRequest is what an SDK asks for when it wants to know what this player gets.
//
// The shape mirrors Batch on purpose: the same installation, the same app, the same device,
// described the same way. An SDK that can build one can build the other, and a studio reading
// two request shapes should not have to learn two vocabularies.
//
// # What is not in here, and cannot be
//
// **Nothing about where the player is or how they connect.** No country, no VPN flag, no
// address. Those are derived from the request's own connection, because a value the client
// sends is a value a modified build can choose — and an audience for something of value is
// exactly what somebody would choose it for (ADR 0027).
//
// **Nothing about what the player has bought or played.** `commerce.*` and `activity.*` come
// from PlayerSense's own record of them, for the same reason and more so.
type ConfigRequest struct {
	SchemaVersion int `json:"schema_version"`

	// InstallID is the installation, and is what every percentage rollout is drawn
	// against (§43).
	InstallID string `json:"install_id"`

	// PlayerID is the studio's own identifier for the account, when the player has
	// identified themselves. Empty for anonymous play.
	//
	// It decides which *facts* are read — an account's history spans its installations —
	// and deliberately not which bucket the player lands in, so that signing in does not
	// move somebody between experiment arms mid-session.
	PlayerID string `json:"player_id,omitempty"`

	Environment string `json:"environment"`

	App    App    `json:"app"`
	Device Device `json:"device"`
	SDK    SDK    `json:"sdk"`

	// Context is the studio's own vocabulary: level, guild, tutorial step, whatever they
	// segment on.
	//
	// Every key is prefixed with `context.` by the server before a rule sees it, which is
	// what makes the reserved namespaces unreachable from here. Not a check that could be
	// forgotten — a build that sends `"geo.country": "TR"` produces the fact
	// `context.geo.country`, which no reserved rule reads. The forgery has nowhere to land
	// rather than being caught on the way past.
	Context map[string]any `json:"context,omitempty"`
}

// ConfigResponse is what the player gets: values and windows, never rules.
type ConfigResponse struct {
	// Version is the published config version these answers came from.
	Version int `json:"version"`

	// Flags is every flag in the project, resolved for this player. Complete rather than
	// sparse: a game asking about a flag it was not sent has no answer at all.
	Flags map[string]any `json:"flags"`

	// Events are the LiveOps events on right now, highest priority first.
	Events []ConfigEvent `json:"liveops_events"`

	// Upcoming are the ones starting soon, so a game can say "back on Friday" without
	// holding a calendar.
	Upcoming []ConfigEvent `json:"upcoming,omitempty"`

	// ServerTimeMS is what the server thinks the time is.
	//
	// The SDK anchors every window against this rather than against the device clock. A
	// player who sets their phone forward must not open a holiday event early, and one
	// whose clock is behind must not miss the end of an offer they paid attention to.
	//
	// Deliberately outside the ETag: it changes on every request, so hashing it would mean
	// no two responses ever matched and the 304 path would never fire.
	ServerTimeMS int64 `json:"server_time_ms"`
}

// ConfigEvent is one LiveOps event as a player sees it.
type ConfigEvent struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Priority int    `json:"priority"`

	StartsAtMS int64 `json:"starts_at_ms"`
	EndsAtMS   int64 `json:"ends_at_ms"`

	// NextStartsAtMS is when it comes back, zero when it never does.
	NextStartsAtMS int64 `json:"next_starts_at_ms,omitempty"`

	// Payload is the studio's own, and PlayerSense does not understand it (§45).
	Payload any `json:"payload"`
}
