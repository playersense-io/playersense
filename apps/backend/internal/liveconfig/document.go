// Package liveconfig compiles what a studio edits into what a player is served.
//
// A studio edits segments, flags and campaigns independently. A player needs one document,
// evaluated once. Publishing is the step between, and it is where every cost that would
// otherwise be paid per request gets paid once (§41, §42).
//
// # What compiling does
//
//   - **Inlines every segment reference.** A flag's audience that says `{"segment":"whales"}`
//     becomes the whales rule itself. The evaluator therefore needs no resolver, no shared
//     map and no recursion into state — and a segment that refers to itself is refused here,
//     once, rather than detected on every evaluation forever.
//   - **Decides once whether the player's history is needed.** A project whose rules never
//     name `commerce.` or `activity.` performs **zero** lookups per request, and most
//     projects are that project: a flag fully rolled out to everybody has no audience at all.
//   - **Freezes the result.** The document is the only record of what a rule *meant* on the
//     day it ran, so editing a segment afterwards cannot rewrite history.
//
// # What it deliberately does not do
//
// It does not produce something a CDN can serve. §42 suggested an optional GCS/CDN copy, and
// a CDN can only serve one document to everybody — which means evaluating rules on the client,
// which ADR 0027 forbids because a rule that reaches a modified build is a rule that build can
// satisfy. At roughly 20M sessions a day config traffic is ~230 requests a second, three
// orders of magnitude below ingestion, so evaluating every request on the server is
// comfortable and the CDN buys nothing worth the contradiction.
package liveconfig

import (
	"playersense.dev/backend/internal/targeting"
)

// Document is one compiled, immutable snapshot for a project and environment.
type Document struct {
	Version int `json:"version"`

	Flags     []Flag     `json:"flags"`
	Campaigns []Campaign `json:"campaigns"`

	// NeedsPlayerFacts is the optimisation this whole package exists to make possible: the
	// request path checks one boolean instead of walking every rule to discover whether a
	// lookup is required.
	NeedsPlayerFacts bool `json:"needs_player_facts"`
}

// Flag is one value, with the ordered rules that decide it.
type Flag struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Fallback any    `json:"fallback"`

	// First match wins, which is why this is a list. A studio orders them in the dashboard
	// and the order is part of the meaning.
	Rules []Variant `json:"rules,omitempty"`

	Rollout int    `json:"rollout"`
	Salt    string `json:"salt,omitempty"`
}

// Variant is one audience and the value it gets.
type Variant struct {
	Audience targeting.Rule `json:"audience"`
	Value    any            `json:"value"`

	// A variant may be rolled out independently of its flag, which is how an experiment
	// runs inside an audience rather than across the whole game.
	Rollout int `json:"rollout"`
}

// Campaign is §45's envelope with its audience already inlined.
//
// `Payload` is opaque and stays that way. PlayerSense understands the envelope — key, kind,
// schedule, eligibility — and a backend that understood the payload would be a backend that
// had to change every time a studio invented an event type.
type Campaign struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Priority int    `json:"priority"`

	// Milliseconds since the epoch, UTC, and zero for "no boundary". The SDK anchors these
	// to a monotonic clock rather than the device's, because a player whose clock is wrong
	// must not be able to start a holiday event early.
	StartsAtMS int64 `json:"starts_at_ms,omitempty"`
	EndsAtMS   int64 `json:"ends_at_ms,omitempty"`

	Audience targeting.Rule `json:"audience"`
	Payload  any            `json:"payload"`

	Rollout int    `json:"rollout"`
	Salt    string `json:"salt,omitempty"`
}
