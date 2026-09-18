package liveconfig

import "fmt"

// Kind is what a flag answers with.
//
// Declared per flag rather than inferred per value, because a flag whose kind is whatever was
// typed last is a flag a game cannot read: the SDK returns a typed value, and `FlagBool` on a
// flag somebody set to `"true"` has to either guess or fail at run time. Declaring it means the
// refusal happens in the dashboard, to the person who can fix it.
type Kind string

// The closed set. A kind this package does not know is a flag that refuses to save, rather than
// one an SDK discovers it cannot read.
const (
	KindBool   Kind = "bool"
	KindString Kind = "string"
	KindNumber Kind = "number"
	KindJSON   Kind = "json"
)

// Kinds is every kind, in the order a dashboard should offer them.
func Kinds() []Kind { return []Kind{KindBool, KindString, KindNumber, KindJSON} }

// ParseKind reads a kind, naming what was expected when it is not one.
func ParseKind(name string) (Kind, error) {
	for _, kind := range Kinds() {
		if Kind(name) == kind {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown flag kind %q; want bool, string, number or json", name)
}

// Accepts reports whether a value can be this kind's answer.
//
// Checked against the *decoded* value rather than against text, so `1` and `1.0` are one number
// and `"1"` is not a number at all — which is the confusion this exists to stop, because a
// number flag holding `"1"` is a flag every SDK reads as zero.
//
// # Absence is refused for every kind, including json
//
// A flag exists to give a game an answer without the network, so a fallback of `null` is a flag
// with no answer — and the game reaches the same state it would have reached had PlayerSense
// been unreachable, except now it is PlayerSense's fault and there is nothing in a log. A
// studio that wants "no opinion" writes the value that means it in their own vocabulary: `{}`,
// `""`, `0`. Each of those is a decision, and `null` is the absence of one.
//
// This is why a `json` flag is not simply "anything": it is any JSON *value*, and `null` is not
// one a game can use.
func (k Kind) Accepts(value any) error {
	if value == nil {
		return fmt.Errorf("a %s flag needs a value; null is not one", k)
	}
	switch k {
	case KindBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("a bool flag takes true or false, not %s", describe(value))
		}
	case KindString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("a string flag takes a string, not %s", describe(value))
		}
	case KindNumber:
		if !isNumber(value) {
			return fmt.Errorf("a number flag takes a number, not %s", describe(value))
		}
	case KindJSON:
		// Any JSON value: an object, an array, or a scalar. The kind a studio picks when
		// the shape is theirs — a shop layout, a tuning table — and the one PlayerSense
		// deliberately has no opinion about beyond it being present.
	default:
		return fmt.Errorf("unknown flag kind %q", k)
	}
	return nil
}

// isNumber accepts every numeric shape a value can arrive in.
//
// A decoded JSON number is a float64, and the rest are here because a value can also be
// constructed in Go — by a test, or by an import — and refusing `int(50)` from a caller who
// meant fifty would be a distinction with no meaning on the wire.
func isNumber(value any) bool {
	switch value.(type) {
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// describe names what arrived, in the words somebody editing a flag would use.
//
// Not %T: "a number flag takes a number, not map[string]interface {}" is a Go error in a
// dashboard, and the person reading it wrote an object.
func describe(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "true or false"
	case string:
		return "a string"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	if isNumber(value) {
		return "a number"
	}
	return fmt.Sprintf("%T", value)
}
