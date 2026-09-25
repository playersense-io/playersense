package logs

import (
	"hash/fnv"
	"regexp"
	"strings"

	"playersense.dev/backend/internal/ingest"
)

// Grouping records into issues.
//
// An issue is every record that is the same bug. The test for "the same" has to survive what
// changes between two occurrences of one bug (a line number after a rebuild, an object id in the
// message, the player's name) and not survive what distinguishes two bugs (a different exception,
// thrown from a different place). So:
//
//   - A studio's own fingerprint wins. It knows its code.
//   - An exception or a crash is its type and the functions of the game's own frames, innermost
//     first. Not line numbers, which move with every edit above them; not engine frames, which are
//     the same under every bug that passes through them.
//   - With no frame of its own, the innermost frames whatever they are, then the message with its
//     numbers and ids taken out.
//   - An error or fatal log line is its logger and its message, numbers and ids taken out, so
//     "Failed to load level 12" and "Failed to load level 13" are one issue.
//   - Anything quieter is not an issue at all: fingerprint 0.

// maxGroupingFrames is how many frames decide an issue. Enough to tell two call sites apart, few
// enough that a deep, varying stack above the bug does not split it into many issues.
const maxGroupingFrames = 6

var (
	uuidPattern   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	hexPattern    = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b|\b[0-9a-fA-F]{12,}\b`)
	numberPattern = regexp.MustCompile(`\d+(\.\d+)?`)
	quotedPattern = regexp.MustCompile(`"[^"]{0,200}"|'[^']{0,200}'`)
)

// Template is a message with what varies between occurrences taken out.
func Template(message string) string {
	t := uuidPattern.ReplaceAllString(message, "<id>")
	t = hexPattern.ReplaceAllString(t, "<hex>")
	t = quotedPattern.ReplaceAllString(t, "<text>")
	t = numberPattern.ReplaceAllString(t, "<n>")
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > 256 {
		t = t[:256]
	}
	return t
}

// Culprit is where a record was thrown: the innermost frame of the game's own code, or the
// innermost frame of any code when none of it is the game's.
func Culprit(r *ingest.NormalizedLogRecord) string {
	if len(r.Frames) == 0 {
		return ""
	}
	pick := r.Frames[len(r.Frames)-1]
	for i := len(r.Frames) - 1; i >= 0; i-- {
		if r.Frames[i].InApp {
			pick = r.Frames[i]
			break
		}
	}
	switch {
	case pick.Module != "" && pick.Function != "":
		return pick.Module + "." + pick.Function
	case pick.Function != "":
		return pick.Function
	default:
		return pick.File
	}
}

// Fingerprint is the issue a record belongs to, or 0 when it belongs to none.
func Fingerprint(r *ingest.NormalizedLogRecord) uint64 {
	h := fnv.New64a()
	write := func(parts ...string) {
		for _, part := range parts {
			h.Write([]byte(part))
			h.Write([]byte{0})
		}
	}

	switch {
	case len(r.Fingerprint) > 0:
		write("custom")
		write(r.Fingerprint...)

	case r.Kind == "exception" || r.Kind == "crash":
		write("exception", r.ExceptionType)
		frames := 0
		for i := len(r.Frames) - 1; i >= 0 && frames < maxGroupingFrames; i-- {
			if f := r.Frames[i]; f.InApp {
				write(f.Module, f.Function)
				frames++
			}
		}
		if frames == 0 {
			for i := len(r.Frames) - 1; i >= 0 && frames < maxGroupingFrames; i-- {
				f := r.Frames[i]
				write(f.Module, f.Function)
				frames++
			}
		}
		if frames == 0 {
			text := r.ExceptionValue
			if text == "" {
				text = r.Message
			}
			write(Template(text))
		}

	case r.Severity == "error" || r.Severity == "fatal":
		write("log", r.Logger, Template(r.Message))

	default:
		return 0
	}

	// Zero means "no issue", so a hash that lands on it moves one along.
	if sum := h.Sum64(); sum != 0 {
		return sum
	}
	return 1
}
