// Package targeting decides which players a flag or a campaign applies to.
//
// A rule is a boolean expression over the context a player arrives with (§41, §45). It is
// data, not code: there is no expression language to parse, no user-supplied function to
// call, and no way for a rule to loop. A studio writes one in a dashboard, it is stored as
// JSON, and this package answers true or false against one player's context.
//
// Three properties it has to hold, because each is a way a targeting engine goes wrong:
//
//   - **Evaluation is server-side and the rule never leaves.** The client is told it is
//     eligible, never how to become eligible (ADR 0027). A rule that reached a modified
//     build would be a rule that build could satisfy.
//   - **An unknown key is false, not an error.** A studio that renames a context key should
//     see a campaign stop matching, not a 500 for every player in the game.
//   - **Nothing here panics.** A malformed rule is refused when it is written, and an
//     unexpected type at evaluation time is a false rather than a crash — a bad rule must
//     cost one campaign, not the config endpoint.
package targeting

import (
	"encoding/json"
	"fmt"
)

// Limits bound what a rule may be, checked when one is written rather than when it runs.
//
// A rule is evaluated once per session per player and stored once, so the cost of a deep one
// is paid on every evaluation forever. These are generous for anything a human writes in a
// dashboard and small enough that a pathological rule cannot become a denial of service.
const (
	MaxDepth      = 8
	MaxConditions = 64
	MaxValues     = 256
)

// Op is a comparison. The set is closed: a studio picks from a dropdown, and an operator
// this package does not know is a rule that refuses to save rather than one that silently
// never matches.
type Op string

const (
	OpEq         Op = "eq"
	OpNe         Op = "ne"
	OpIn         Op = "in"
	OpNotIn      Op = "not_in"
	OpLt         Op = "lt"
	OpLte        Op = "lte"
	OpGt         Op = "gt"
	OpGte        Op = "gte"
	OpExists     Op = "exists"
	OpNotExists  Op = "not_exists"
	OpStartsWith Op = "starts_with"
	OpContains   Op = "contains"

	// Version comparison is its own operator rather than a flavour of Lt/Gt, because the
	// ordinary ones are wrong for versions in a way that looks right: "1.9.0" sorts after
	// "1.12.0" as a string, so a rule targeting "1.12.0 and later" quietly excludes every
	// player who upgraded. §41's own example is `app_version >= "1.12.0"`.
	OpVersionLt  Op = "version_lt"
	OpVersionLte Op = "version_lte"
	OpVersionGt  Op = "version_gt"
	OpVersionGte Op = "version_gte"
)

// Rule is one node: a group (All/Any/Not) or a leaf condition.
//
// Exactly one shape is populated. A node with none is an empty rule, which matches
// everybody — that is what "no audience" means for a campaign, and it is deliberate rather
// than a degenerate case to reject.
type Rule struct {
	All []Rule `json:"all,omitempty"`
	Any []Rule `json:"any,omitempty"`
	Not *Rule  `json:"not,omitempty"`

	// Segment names another rule by key, so an audience can be reused rather than retyped.
	//
	// **Authored only.** Publishing replaces it with the segment's own rule, so a rule that
	// is being evaluated never contains one — which is why the evaluator needs no resolver
	// and cannot recurse into shared state, and why a segment that refers to itself is a
	// publish-time error rather than a request-time hang.
	Segment string `json:"segment,omitempty"`

	// Key is a context key: "geo.country", "context.progress.level", "app.version".
	Key string `json:"key,omitempty"`
	Op  Op     `json:"op,omitempty"`

	// Value for the single-value operators, Values for the set ones. Kept apart rather
	// than overloading one field, so a rule that means `in ["TR"]` cannot be written as
	// `in "TR"` and match nothing.
	Value  any   `json:"value,omitempty"`
	Values []any `json:"values,omitempty"`
}

// Empty reports whether this rule constrains nothing, and therefore matches everyone.
//
// Every field, not just the key: `{"op":"eq","value":1}` has no key but is plainly a
// condition somebody meant to write, and calling it empty would turn a typo into a campaign
// for the entire game. The only empty rule is the one with nothing in it at all.
func (r Rule) Empty() bool {
	return len(r.All) == 0 && len(r.Any) == 0 && r.Not == nil && r.Segment == "" &&
		r.Key == "" && r.Op == "" && r.Value == nil && len(r.Values) == 0
}

// Validate checks a rule when it is written.
//
// The whole point of validating at write time is that a studio sees "unknown operator
// 'greater'" in the dashboard while they are looking at the rule, rather than a campaign
// that reaches nobody and gives no reason.
func (r Rule) Validate() error {
	conditions := 0
	return r.validate(0, &conditions)
}

func (r Rule) validate(depth int, conditions *int) error {
	if depth > MaxDepth {
		return fmt.Errorf("a rule may nest at most %d deep", MaxDepth)
	}
	if r.Empty() {
		if depth == 0 {
			return nil // No audience: everyone.
		}
		return fmt.Errorf("an empty condition at depth %d matches everything; remove it", depth)
	}

	if r.Segment != "" {
		if len(r.All) > 0 || len(r.Any) > 0 || r.Not != nil || r.Key != "" {
			return fmt.Errorf("a segment reference (%q) stands alone", r.Segment)
		}
		return nil
	}

	groups := 0
	if len(r.All) > 0 {
		groups++
	}
	if len(r.Any) > 0 {
		groups++
	}
	if r.Not != nil {
		groups++
	}
	if groups > 1 {
		return fmt.Errorf("a node is one of all, any or not — not %d of them", groups)
	}
	if groups == 1 && r.Key != "" {
		return fmt.Errorf("a node is a group or a condition, not both (key %q)", r.Key)
	}

	for _, child := range append(append([]Rule{}, r.All...), r.Any...) {
		if err := child.validate(depth+1, conditions); err != nil {
			return err
		}
	}
	if r.Not != nil {
		if err := r.Not.validate(depth+1, conditions); err != nil {
			return err
		}
	}
	if groups == 1 {
		return nil
	}

	if *conditions++; *conditions > MaxConditions {
		return fmt.Errorf("a rule may hold at most %d conditions", MaxConditions)
	}
	return r.validateCondition()
}

func (r Rule) validateCondition() error {
	if r.Key == "" {
		return fmt.Errorf("a condition needs a key")
	}
	// Refused rather than accepted-and-never-matched. A key with no column behind it would
	// make a campaign reach nobody and give no reason, which is the failure mode this
	// repository keeps finding in its own work.
	if why, missing := Unavailable[r.Key]; missing {
		return fmt.Errorf("%q is not available yet: %s", r.Key, why)
	}
	switch r.Op {
	case OpExists, OpNotExists:
		if r.Value != nil || len(r.Values) > 0 {
			return fmt.Errorf("%s on %q takes no value", r.Op, r.Key)
		}
		return nil

	case OpIn, OpNotIn:
		if len(r.Values) == 0 {
			return fmt.Errorf("%s on %q needs values", r.Op, r.Key)
		}
		if len(r.Values) > MaxValues {
			return fmt.Errorf("%s on %q has %d values; the limit is %d",
				r.Op, r.Key, len(r.Values), MaxValues)
		}
		return nil

	case OpEq, OpNe, OpLt, OpLte, OpGt, OpGte, OpStartsWith, OpContains,
		OpVersionLt, OpVersionLte, OpVersionGt, OpVersionGte:
		if r.Value == nil {
			return fmt.Errorf("%s on %q needs a value", r.Op, r.Key)
		}
		return nil

	case "":
		return fmt.Errorf("a condition on %q needs an operator", r.Key)
	default:
		return fmt.Errorf("unknown operator %q on %q", r.Op, r.Key)
	}
}

// Parse reads a rule and validates it in one step, so an invalid rule cannot be constructed
// from JSON and then evaluated.
func Parse(raw []byte) (Rule, error) {
	var rule Rule
	if len(raw) == 0 {
		return rule, nil
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		return Rule{}, fmt.Errorf("rule is not valid JSON: %w", err)
	}
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}
