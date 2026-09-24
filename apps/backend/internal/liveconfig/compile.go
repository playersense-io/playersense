package liveconfig

import (
	"fmt"

	"playersense.dev/backend/internal/targeting"
)

// MaxSegmentDepth bounds how far a segment may refer through other segments.
//
// Cycles are detected exactly, so this is not the cycle guard — it is a readability bound. A
// segment eight references deep is one nobody can explain, and an audience nobody can explain
// is one that gets used wrongly.
const MaxSegmentDepth = 8

// Segments is the set a document is compiled against, keyed by the segment's own key.
type Segments map[string]targeting.Rule

// Inline replaces every segment reference in a rule with the segment's own rule.
//
// This is what lets the evaluator stay a pure function over a context. A resolver at
// evaluation time would mean a map lookup per node, a shared structure to synchronise, and a
// cycle that hangs a request — here a cycle is an error a person sees while they are looking
// at the rule that caused it.
//
// The error names the path, because "cycle detected" in a project with forty segments is not
// an actionable message.
func (s Segments) Inline(rule targeting.Rule) (targeting.Rule, error) {
	return s.inline(rule, nil)
}

// Check is [Segments.Inline] run for its error, when a rule is written rather than published.
//
// The same walk answers both questions, and running it at the write is what moves "segment
// 'whales' does not exist" from the publish — where it is a 422 in front of somebody who did
// not write the mistake, often days later — to the request that made it, where the rule is
// still on screen.
//
// A cycle is the case that argues for this most strongly. A -> B -> A cannot exist until the
// write that closes the loop, so that write is the only moment at which one person, one rule
// and one error coincide. Afterwards the project simply does not publish, and nothing points at
// which of the two rules to change.
//
// The inlined rule is discarded on purpose: what is stored is what the studio wrote, references
// and all, because a segment edited tomorrow has to reach every rule that names it. Inlining is
// the publish's job, and this is only the rehearsal.
func (s Segments) Check(rule targeting.Rule) error {
	_, err := s.Inline(rule)
	return err
}

func (s Segments) inline(rule targeting.Rule, path []string) (targeting.Rule, error) {
	if len(path) > MaxSegmentDepth {
		return targeting.Rule{}, fmt.Errorf(
			"segments nest more than %d deep: %s", MaxSegmentDepth, join(path))
	}

	if rule.Segment != "" {
		key := rule.Segment
		for _, seen := range path {
			if seen == key {
				return targeting.Rule{}, fmt.Errorf(
					"segment %q refers to itself: %s", key, join(append(path, key)))
			}
		}
		referenced, found := s[key]
		if !found {
			// Refused rather than treated as "matches nobody". An event whose audience
			// silently became empty is the failure this whole design keeps guarding
			// against, and a publish is exactly the moment a person can fix it.
			return targeting.Rule{}, fmt.Errorf("segment %q does not exist", key)
		}
		return s.inline(referenced, append(path, key))
	}

	out := targeting.Rule{
		Key:    rule.Key,
		Op:     rule.Op,
		Value:  rule.Value,
		Values: rule.Values,
	}

	for _, child := range rule.All {
		inlined, err := s.inline(child, path)
		if err != nil {
			return targeting.Rule{}, err
		}
		out.All = append(out.All, inlined)
	}
	for _, child := range rule.Any {
		inlined, err := s.inline(child, path)
		if err != nil {
			return targeting.Rule{}, err
		}
		out.Any = append(out.Any, inlined)
	}
	if rule.Not != nil {
		inlined, err := s.inline(*rule.Not, path)
		if err != nil {
			return targeting.Rule{}, err
		}
		out.Not = &inlined
	}
	return out, nil
}

func join(path []string) string {
	out := ""
	for i, key := range path {
		if i > 0 {
			out += " -> "
		}
		out += key
	}
	if out == "" {
		return "(none)"
	}
	return out
}

// Input is everything a publish reads.
type Input struct {
	Version  int
	Segments Segments
	Flags    []Flag
	Events   []LiveOpsEvent
}

// Compile turns what a studio edited into what a player is served.
//
// Every audience is inlined and re-validated, so a document that compiles is a document that
// evaluates — there is no second class of error waiting at request time.
func Compile(in Input) (Document, error) {
	doc := Document{Version: in.Version}

	for _, flag := range in.Flags {
		compiled := Flag{
			Key: flag.Key, Kind: flag.Kind, Fallback: flag.Fallback,
			Rollout: clampRollout(flag.Rollout), Salt: flag.Salt,
		}
		for i, variant := range flag.Rules {
			audience, err := in.Segments.Inline(variant.Audience)
			if err != nil {
				return Document{}, fmt.Errorf("flag %q rule %d: %w", flag.Key, i+1, err)
			}
			if err := audience.Validate(); err != nil {
				return Document{}, fmt.Errorf("flag %q rule %d: %w", flag.Key, i+1, err)
			}
			compiled.Rules = append(compiled.Rules, Variant{
				Audience: audience,
				Value:    variant.Value,
				Rollout:  clampRollout(variant.Rollout),
			})
			doc.NeedsPlayerFacts = doc.NeedsPlayerFacts || audience.NeedsPlayerFacts()
		}
		if flag.Split != nil {
			split, err := compileSplit(in.Segments, *flag.Split)
			if err != nil {
				return Document{}, fmt.Errorf("flag %q experiment: %w", flag.Key, err)
			}
			compiled.Split = &split
			doc.NeedsPlayerFacts = doc.NeedsPlayerFacts || split.Audience.NeedsPlayerFacts()
		}
		doc.Flags = append(doc.Flags, compiled)
	}

	for _, event := range in.Events {
		audience, err := in.Segments.Inline(event.Audience)
		if err != nil {
			return Document{}, fmt.Errorf("liveops event %q: %w", event.Key, err)
		}
		if err := audience.Validate(); err != nil {
			return Document{}, fmt.Errorf("liveops event %q: %w", event.Key, err)
		}
		// The schedule is re-checked here for the same reason the audience is: a document
		// that compiles has to be a document that evaluates. A schedule saved by an older
		// dashboard, or edited in the database by hand, otherwise becomes an event that is
		// never on and gives no reason — and publishing is the last moment a person is
		// looking at it.
		if err := event.Schedule.Validate(); err != nil {
			return Document{}, fmt.Errorf("liveops event %q: %w", event.Key, err)
		}
		compiled := event
		compiled.Audience = audience
		compiled.Rollout = clampRollout(event.Rollout)
		doc.Events = append(doc.Events, compiled)
		doc.NeedsPlayerFacts = doc.NeedsPlayerFacts || audience.NeedsPlayerFacts()
	}

	return doc, nil
}

// compileSplit inlines an experiment's audience and checks its arms divide the test whole.
func compileSplit(segments Segments, split Split) (Split, error) {
	audience, err := segments.Inline(split.Audience)
	if err != nil {
		return Split{}, err
	}
	if err := audience.Validate(); err != nil {
		return Split{}, err
	}
	if len(split.Arms) < 2 {
		return Split{}, fmt.Errorf("an experiment needs at least two arms, not %d", len(split.Arms))
	}
	total := 0
	for _, arm := range split.Arms {
		if arm.Weight <= 0 {
			return Split{}, fmt.Errorf("arm %q has no weight", arm.ID)
		}
		total += arm.Weight
	}
	if total != targeting.Buckets {
		return Split{}, fmt.Errorf("the arms' weights add up to %d hundredths, not %d", total, targeting.Buckets)
	}
	split.Audience = audience
	split.Traffic = clampRollout(split.Traffic)
	return split, nil
}

// clampRollout keeps a percentage inside its range.
//
// Clamped rather than rejected: the schema already bounds it, so a value outside the range
// here is a bug in something upstream, and refusing to publish the whole project's config
// because one number is wrong is a worse outcome than serving that flag to everybody or
// nobody.
func clampRollout(percent int) int {
	switch {
	case percent < 0:
		return 0
	case percent > targeting.Buckets:
		return targeting.Buckets
	default:
		return percent
	}
}
