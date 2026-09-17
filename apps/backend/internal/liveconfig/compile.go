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
			// Refused rather than treated as "matches nobody". A campaign whose audience
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
	Version   int
	Segments  Segments
	Flags     []Flag
	Campaigns []Campaign
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
		doc.Flags = append(doc.Flags, compiled)
	}

	for _, campaign := range in.Campaigns {
		audience, err := in.Segments.Inline(campaign.Audience)
		if err != nil {
			return Document{}, fmt.Errorf("campaign %q: %w", campaign.Key, err)
		}
		if err := audience.Validate(); err != nil {
			return Document{}, fmt.Errorf("campaign %q: %w", campaign.Key, err)
		}
		compiled := campaign
		compiled.Audience = audience
		compiled.Rollout = clampRollout(campaign.Rollout)
		doc.Campaigns = append(doc.Campaigns, compiled)
		doc.NeedsPlayerFacts = doc.NeedsPlayerFacts || audience.NeedsPlayerFacts()
	}

	return doc, nil
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
