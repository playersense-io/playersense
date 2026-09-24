package liveconfig

import (
	"sort"
	"time"

	"playersense.dev/backend/internal/targeting"
)

// Subject is the player a document is being evaluated for.
//
// The rollout key is deliberately the **installation**, not the account. A player who signs
// in mid-session would otherwise cross a bucket boundary and watch a flag change under them,
// and an experiment whose arms swap when somebody logs in measures the login rather than the
// feature (§43: "the same player remains in the same variant"). The account still decides
// *audiences* — `commerce.player.*` facts are keyed on it — which is the opposite question.
type Subject struct {
	// InstallID identifies the installation, and is what every bucket is drawn against.
	InstallID string

	// Context is the facts a rule is evaluated over: what the client sent, what the server
	// derived from the request address, and the player's history when the document says it
	// is needed. Assembled by the caller, because the rules about what may come from where
	// belong at the edge (ADR 0027).
	Context targeting.Context
}

// Result is what one player is served.
//
// Values and windows, never rules. ADR 0027: a rule that reaches a modified build is a rule
// that build can satisfy, so the audience never leaves the server — the player learns what
// they get and not who else gets it.
type Result struct {
	Version int `json:"version"`

	// Flags is every flag in the document, resolved. Complete rather than sparse: a game
	// asking for a flag it was not sent has no answer at all, and "absent" and "false" are
	// different things to a caller that has to pick one.
	Flags map[string]any `json:"flags"`

	// Events are the LiveOps events on right now, highest priority first.
	Events []ActiveEvent `json:"liveops_events"`

	// Experiments names, by flag, the test and arm a player is in, for the SDK to put on the
	// flag's exposure so the arm is counted as an arm rather than inferred from its value.
	Experiments map[string]Assignment `json:"experiments,omitempty"`
}

// Assignment is one player's place in one experiment.
type Assignment struct {
	Experiment string `json:"experiment"`
	Arm        string `json:"arm"`
}

// ActiveEvent is one event a player is inside, with the instants the SDK counts against.
type ActiveEvent struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Priority int    `json:"priority"`

	// Milliseconds since the epoch, UTC. The server's clock decides, not the device's: a
	// player who sets their phone forward must not open a holiday event early, and a player
	// whose clock is behind must not miss the end of one.
	StartsAtMS int64 `json:"starts_at_ms"`
	EndsAtMS   int64 `json:"ends_at_ms"`

	// NextStartsAtMS is when this event comes back, zero when it never does. It is what
	// lets a game say "back in 3 days" without holding a calendar, which is the whole
	// reason the schedule stays on the server.
	NextStartsAtMS int64 `json:"next_starts_at_ms,omitempty"`

	Payload any `json:"payload"`
}

// Evaluate resolves a document for one player at one instant.
//
// Pure: no clock of its own, no network, no state. `at` is passed rather than read so that a
// test can stand on a date and so that every flag and event in one response is decided at the
// same instant — an event that ended between two lines of this function would otherwise be
// both on and off in the same answer.
func (d Document) Evaluate(at time.Time, projectID string, subject Subject) Result {
	result := Result{
		Version: d.Version,
		Flags:   make(map[string]any, len(d.Flags)),
	}

	for _, flag := range d.Flags {
		value, arm := flag.resolve(projectID, subject)
		result.Flags[flag.Key] = value
		if arm != "" {
			if result.Experiments == nil {
				result.Experiments = map[string]Assignment{}
			}
			result.Experiments[flag.Key] = Assignment{Experiment: flag.Split.Experiment, Arm: arm}
		}
	}

	for _, event := range d.Events {
		if active, on := event.resolve(at, projectID, subject); on {
			result.Events = append(result.Events, active)
		}
	}

	// Highest priority first, then by key so that two events of the same priority come
	// back in the same order every time. A list whose order wobbles between requests is a
	// list a game cannot render without flicker.
	sort.Slice(result.Events, func(i, j int) bool {
		if result.Events[i].Priority != result.Events[j].Priority {
			return result.Events[i].Priority > result.Events[j].Priority
		}
		return result.Events[i].Key < result.Events[j].Key
	})

	return result
}

// resolve decides one flag's value.
//
// Three gates, in order, and the order is the meaning:
//
//  1. The **flag's own rollout**. Outside it, the player gets the fallback and no rule is
//     even considered — that is what "this flag is 25% live" means.
//  2. The **first rule whose audience matches**. First rather than best: a studio orders them
//     in the dashboard and the order is part of what they wrote.
//  3. That rule's **own rollout**, which is how an experiment runs inside an audience rather
//     than across the whole game.
//
// A rule that matches but whose rollout excludes this player does **not** fall through to the
// next rule. It is the audience that was chosen; being outside the sample means the default,
// not somebody else's variant — falling through would quietly enrol the control group into
// whatever rule came next.
//
// An experiment on the flag comes before the rules, inside the flag's own rollout. The arm is
// returned beside the value, empty for a player the test did not take in.
func (f Flag) resolve(projectID string, subject Subject) (any, string) {
	if !targeting.InRollout(f.Rollout, projectID, f.Key, subject.InstallID, f.Salt) {
		return f.Fallback, ""
	}

	if f.Split != nil {
		if arm, in := f.Split.assign(projectID, f.Key, f.Salt, subject); in {
			return arm.Value, arm.ID
		}
	}

	for i, variant := range f.Rules {
		if !variant.Audience.Matches(subject.Context) {
			continue
		}
		// Salted with the rule's position as well as the flag, so two rules in one flag
		// draw different samples rather than the same players twice.
		if !targeting.InRollout(variant.Rollout, projectID, f.Key+"#"+itoa(i), subject.InstallID, f.Salt) {
			return f.Fallback, ""
		}
		return variant.Value, ""
	}
	return f.Fallback, ""
}

// assign places a player in the test and in an arm, or reports that the test did not take them.
func (s Split) assign(projectID, flagKey, salt string, subject Subject) (Arm, bool) {
	if len(s.Arms) == 0 || !s.Audience.Matches(subject.Context) {
		return Arm{}, false
	}
	if !targeting.InRollout(s.Traffic, projectID, flagKey+"!"+s.Experiment, subject.InstallID, salt) {
		return Arm{}, false
	}
	bucket := targeting.Bucket(projectID, flagKey+"~"+s.Experiment, subject.InstallID, salt)
	for _, arm := range s.Arms {
		if bucket < arm.Weight {
			return arm, true
		}
		bucket -= arm.Weight
	}
	// Weights that do not reach Buckets are refused when compiled; the last arm takes any
	// remainder rather than leaving a player half in the test.
	return s.Arms[len(s.Arms)-1], true
}

// resolve decides whether a player is inside an event right now.
//
// The calendar is checked before the audience, and that ordering is worth the line: an event
// that is off costs one arithmetic comparison, while an audience can walk a tree and read the
// player's purchase history. Most events are off most of the time.
func (e LiveOpsEvent) resolve(at time.Time, projectID string, subject Subject) (ActiveEvent, bool) {
	window, on := e.Schedule.ActiveAt(at)
	if !on {
		return ActiveEvent{}, false
	}
	if !e.Audience.Matches(subject.Context) {
		return ActiveEvent{}, false
	}
	if !targeting.InRollout(e.Rollout, projectID, e.Key, subject.InstallID, e.Salt) {
		return ActiveEvent{}, false
	}

	active := ActiveEvent{
		Key: e.Key, Kind: e.Kind, Priority: e.Priority,
		StartsAtMS: window.Start.UnixMilli(),
		EndsAtMS:   window.End.UnixMilli(),
		Payload:    e.Payload,
	}
	// Asked from `at`, which is already inside this window, and NextAfter returns the first
	// window starting strictly after the instant it is given — so the current occurrence
	// cannot come back as its own successor, and no adjustment is needed to prevent it.
	// Occurrences never overlap (liveops.Schedule.Validate refuses a window longer than its
	// own period), so every instant inside this window gives the same answer.
	if next, found := e.Schedule.NextAfter(at); found {
		active.NextStartsAtMS = next.Start.UnixMilli()
	}
	return active, true
}

// Upcoming returns the next window of every event a player is eligible for but not yet
// inside, so a game can show "starts Friday" for something that has not begun.
//
// Separate from Evaluate rather than folded into it, because an SDK that only wants to know
// what is on now should not pay for a forward scan of every schedule in the project.
func (d Document) Upcoming(at time.Time, within time.Duration, projectID string, subject Subject) []ActiveEvent {
	var out []ActiveEvent
	horizon := at.Add(within)

	for _, event := range d.Events {
		if _, on := event.Schedule.ActiveAt(at); on {
			continue
		}
		next, found := event.Schedule.NextAfter(at)
		if !found || next.Start.After(horizon) {
			continue
		}
		if !event.Audience.Matches(subject.Context) {
			continue
		}
		if !targeting.InRollout(event.Rollout, projectID, event.Key, subject.InstallID, event.Salt) {
			continue
		}
		out = append(out, ActiveEvent{
			Key: event.Key, Kind: event.Kind, Priority: event.Priority,
			StartsAtMS: next.Start.UnixMilli(),
			EndsAtMS:   next.End.UnixMilli(),
			Payload:    event.Payload,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StartsAtMS < out[j].StartsAtMS })
	return out
}

// itoa is strconv.Itoa without the import, for the one place a rule's index becomes part of a
// hash key.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for i > 0 {
		pos--
		digits[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(digits[pos:])
}
