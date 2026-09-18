package targeting

import (
	"encoding/json"
	"strings"
)

// Context is one player's world at the moment of evaluation: the SDK's own facts, the
// studio's vocabulary, and what the server derived from the connection.
//
// A map rather than a struct, because §41's whole premise is that PlayerSense does not know
// what a studio's keys mean. The values are the four scalar types the protocol carries —
// string, int64, float64, bool — and anything else is treated as absent rather than
// coerced, because a silently coerced value is a rule that matches the wrong players.
type Context map[string]any

// Matches reports whether a player is in a rule's audience.
//
// An empty rule matches everyone: an event with no audience is an event for the whole
// game, which is the common case and should not require writing `{"all": []}`.
func (r Rule) Matches(ctx Context) bool {
	if r.Empty() {
		return true
	}

	switch {
	case len(r.All) > 0:
		for _, child := range r.All {
			if !child.Matches(ctx) {
				return false
			}
		}
		return true

	case len(r.Any) > 0:
		for _, child := range r.Any {
			if child.Matches(ctx) {
				return true
			}
		}
		return false

	case r.Not != nil:
		return !r.Not.Matches(ctx)

	case r.Segment != "":
		// A reference that survived publishing is a compiler bug, not a runtime case.
		// False rather than a panic: analytics must never crash a game, and an event
		// that reaches nobody is findable while a crash in a config handler is an
		// outage. `liveconfig.Compile` is what should have replaced this.
		return false
	}

	return r.match(ctx)
}

// match evaluates one condition.
//
// The rule for a key that is not there is the important one, and it is "false" for every
// operator but NotExists. A studio that renames `progress.level` should see the event stop
// reaching people — not an error, and not an event that suddenly reaches everyone
// because a missing value compared equal to zero.
func (r Rule) match(ctx Context) bool {
	value, present := ctx[r.Key]

	switch r.Op {
	case OpExists:
		return present
	case OpNotExists:
		return !present
	}
	if !present {
		return false
	}

	switch r.Op {
	case OpEq:
		return equal(value, r.Value)
	case OpNe:
		return !equal(value, r.Value)

	case OpIn:
		for _, want := range r.Values {
			if equal(value, want) {
				return true
			}
		}
		return false
	case OpNotIn:
		for _, want := range r.Values {
			if equal(value, want) {
				return false
			}
		}
		return true

	case OpLt, OpLte, OpGt, OpGte:
		order, ok := compare(value, r.Value)
		if !ok {
			// Two values with no ordering between them — a string against a number.
			// False, because "is this player's rank less than 5" has no answer when the
			// rank is "gold", and inventing one would put somebody in the wrong audience.
			return false
		}
		switch r.Op {
		case OpLt:
			return order < 0
		case OpLte:
			return order <= 0
		case OpGt:
			return order > 0
		default:
			return order >= 0
		}

	case OpVersionLt, OpVersionLte, OpVersionGt, OpVersionGte:
		left, okLeft := text(value)
		right, okRight := text(r.Value)
		if !okLeft || !okRight {
			return false
		}
		order := compareVersions(left, right)
		switch r.Op {
		case OpVersionLt:
			return order < 0
		case OpVersionLte:
			return order <= 0
		case OpVersionGt:
			return order > 0
		default:
			return order >= 0
		}

	case OpStartsWith:
		left, okLeft := text(value)
		right, okRight := text(r.Value)
		return okLeft && okRight && strings.HasPrefix(left, right)

	case OpContains:
		left, okLeft := text(value)
		right, okRight := text(r.Value)
		return okLeft && okRight && strings.Contains(left, right)
	}
	return false
}

// equal compares two values without coercing across kinds.
//
// A string "10" is not the integer 10. JSON has one number type, so 10 and 10.0 must be the
// same value — but a number and a string must not be, or a rule written against a key whose
// type changed starts matching players it was never meant to.
func equal(left, right any) bool {
	if leftText, ok := text(left); ok {
		rightText, ok := text(right)
		return ok && leftText == rightText
	}
	if leftBool, ok := left.(bool); ok {
		rightBool, ok := right.(bool)
		return ok && leftBool == rightBool
	}
	order, ok := compare(left, right)
	return ok && order == 0
}

// compare orders two numbers, or two strings. Returns false for anything else, including a
// number against a string.
func compare(left, right any) (int, bool) {
	if leftNum, ok := number(left); ok {
		rightNum, ok := number(right)
		if !ok {
			return 0, false
		}
		switch {
		case leftNum < rightNum:
			return -1, true
		case leftNum > rightNum:
			return 1, true
		default:
			return 0, true
		}
	}
	if leftText, ok := text(left); ok {
		rightText, ok := text(right)
		if !ok {
			return 0, false
		}
		return strings.Compare(leftText, rightText), true
	}
	return 0, false
}

// number accepts every numeric shape that can arrive from JSON or from the SDK's four
// types. A bool is deliberately not a number: `true` is not 1 here.
func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	}
	return 0, false
}

func text(value any) (string, bool) {
	typed, ok := value.(string)
	return typed, ok
}
