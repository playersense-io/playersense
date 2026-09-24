// Package liveconfig compiles what a studio edits into what a player is served.
//
// A studio edits segments, flags and LiveOps events independently. A player needs one document,
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
// # What it deliberately leaves open
//
// A LiveOps event's schedule is carried, not expanded. Compiling "the first five days of
// every month" into concrete windows would mean choosing a horizon, and a horizon means
// something has to republish the document before it runs out — a cron job whose only purpose
// is to keep a frozen answer from going stale, and a window that is wrong the moment it does
// not run.
//
// The alternative is evaluating the schedule per request, which BenchmarkActiveAt puts at
// 66-658ns for schedules anchored six years back, against a config path already spending
// milliseconds on the network. So the document stays honest about *when*, the server answers
// "is it on now", and the response carries the concrete window.
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
	"playersense.dev/backend/internal/liveops"
	"playersense.dev/backend/internal/targeting"
)

// Document is one compiled, immutable snapshot for a project and environment.
type Document struct {
	Version int `json:"version"`

	Flags  []Flag         `json:"flags"`
	Events []LiveOpsEvent `json:"liveops_events"`

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

	// Split is the experiment running on this flag, when one is (§43, §44). It is decided
	// before the rules: a player the test takes in is answered by their arm, and everybody
	// else by the rules as usual.
	Split *Split `json:"split,omitempty"`
}

// Split is an experiment dividing an audience between arms.
//
// Two draws, each against its own key, and the separation is the point. Traffic decides who is
// in the test and the arm decides which variant, so raising traffic from 10% to 50% only adds
// players: everybody already in stays in, and in the same arm.
type Split struct {
	Experiment string         `json:"experiment"`
	Audience   targeting.Rule `json:"audience"`

	// Traffic is hundredths of the audience let into the test.
	Traffic int   `json:"traffic"`
	Arms    []Arm `json:"arms"`
}

// Arm is one variant of an experiment.
type Arm struct {
	ID    string `json:"id"`
	Value any    `json:"value"`

	// Weight is hundredths of the test's players; an experiment's arms add up to Buckets.
	Weight int `json:"weight"`
}

// Variant is one audience and the value it gets.
type Variant struct {
	Audience targeting.Rule `json:"audience"`
	Value    any            `json:"value"`

	// A variant may be rolled out independently of its flag, which is how an experiment
	// runs inside an audience rather than across the whole game.
	Rollout int `json:"rollout"`
}

// LiveOpsEvent is §45's envelope with its audience already inlined.
//
// Called LiveOpsEvent and never Event: `events_raw` and `event_name` mean analytics events
// throughout this codebase, and the two must not share a word.
//
// `Payload` is opaque and stays that way. PlayerSense understands the envelope — key, kind,
// schedule, eligibility — and a backend that understood the payload would be a backend that
// had to change every time a studio invented an event type.
type LiveOpsEvent struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Priority int    `json:"priority"`

	// When it is on. The server evaluates this and sends the player concrete instants, so
	// an SDK compares two numbers and holds no calendar — which is also why a studio
	// changing "every Friday" to "every Saturday" does not need players to update the game.
	//
	// It is the server's clock that decides, not the device's: a player who sets their
	// phone forward a week must not be able to open a holiday event early.
	Schedule liveops.Schedule `json:"schedule"`

	Audience targeting.Rule `json:"audience"`
	Payload  any            `json:"payload"`

	Rollout int    `json:"rollout"`
	Salt    string `json:"salt,omitempty"`
}
