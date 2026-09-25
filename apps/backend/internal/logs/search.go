package logs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// The search box's language: words, "phrases" and field:value filters, which is what every log
// tool has taught people to type.
//
//	timeout "failed to connect"          both words... and the phrase
//	level:error,fatal logger:Network     one of the levels, and that logger
//	release:1.4.2 platform:android       app version and platform
//	-heartbeat  -logger:Ads              leave these out
//	attr.region:eu  @region:eu            an attribute the game sent
//	install:<uuid> player:<id> session:<id> issue:<fingerprint>
//
// Words are matched whole, as the text index stores them, with CamelCase and numbers split off:
// "timeout" finds "Timeout: 30s" and TimeoutException, and not "timeouts". A phrase is matched as
// written, case aside, once the index has narrowed the granules to those holding each of its words.

// searchText is the index's own expression. A query must spell it exactly as the table does or
// ClickHouse will not use the index (db/clickhouse/migrations/00025_logs.sql).
const searchText = `lower(replaceRegexpAll(concat(message, ' ', exception_type, ' ', exception_value), ` +
	`'([a-z])([A-Z])|([A-Za-z])([0-9])|([0-9])([A-Za-z])', '\\1\\3\\5 \\2\\4\\6'))`

// rawText is the same text as written, which a phrase is matched against.
const rawText = "concat(message, ' ', exception_type, ' ', exception_value)"

// wordBreaks is the index expression's regular expression, applied the same way: leftmost first,
// matches never overlapping, both RE2. So "0x7f" splits here exactly as it splits in the table.
var wordBreaks = regexp.MustCompile(`([a-z])([A-Z])|([A-Za-z])([0-9])|([0-9])([A-Za-z])`)

// Search is a parsed query.
type Search struct {
	Words    []string
	NotWords []string
	Phrases  []string

	Severities []string
	Kinds      []string

	// Equality filters by column, and the columns excluded the same way.
	Fields    map[string][]string
	NotFields map[string][]string

	Attributes    map[string]string
	NotAttributes map[string]string

	Fingerprint uint64
}

// NamesPlayer reports whether the search reads by a player's own identifier, which needs player.read.
func (s *Search) NamesPlayer() bool {
	return len(s.Fields["player_id"]) > 0 || len(s.NotFields["player_id"]) > 0
}

// fieldColumns maps what a person types onto the column it filters.
var fieldColumns = map[string]string{
	"logger": "logger", "source": "logger",
	"release": "app_version", "version": "app_version", "app_version": "app_version",
	"build":    "app_build",
	"platform": "platform",
	"os":       "os_version",
	"device":   "device_model", "model": "device_model",
	"country": "country_code",
	"type":    "exception_type", "exception": "exception_type",
	"culprit": "culprit",
	"install": "install_id",
	"player":  "player_id",
	"session": "session_id",
	"sdk":     "sdk_version",
}

var severityOrder = []string{"trace", "debug", "info", "warn", "error", "fatal"}

// ParseSearch reads the search box.
func ParseSearch(q string) (*Search, error) {
	s := &Search{Fields: map[string][]string{}, NotFields: map[string][]string{},
		Attributes: map[string]string{}, NotAttributes: map[string]string{}}
	for _, term := range splitTerms(q) {
		negated := strings.HasPrefix(term, "-") && len(term) > 1
		if negated {
			term = term[1:]
		}
		if strings.HasPrefix(term, "\"") {
			phrase := strings.Trim(term, "\"")
			if phrase == "" {
				continue
			}
			if negated {
				s.NotWords = append(s.NotWords, tokens(phrase)...)
				continue
			}
			s.Phrases = append(s.Phrases, phrase)
			s.Words = append(s.Words, tokens(phrase)...)
			continue
		}

		key, value, isField := strings.Cut(term, ":")
		if isField && key != "" && value != "" {
			value = strings.Trim(value, "\"")
			key = strings.ToLower(key)
			if err := s.addField(key, value, negated); err != nil {
				return nil, err
			}
			continue
		}
		if negated {
			s.NotWords = append(s.NotWords, tokens(term)...)
		} else {
			s.Words = append(s.Words, tokens(term)...)
		}
	}
	return s, nil
}

func (s *Search) addField(key, value string, negated bool) error {
	switch {
	case key == "level" || key == "severity":
		for _, v := range strings.Split(strings.ToLower(value), ",") {
			if !knownSeverity(v) {
				return fmt.Errorf("level %q is not trace, debug, info, warn, error or fatal", v)
			}
			if !negated {
				s.Severities = append(s.Severities, v)
			}
		}
		if negated {
			// "-level:debug" is every level but those.
			excluded := strings.Split(strings.ToLower(value), ",")
			for _, level := range severityOrder {
				if !contains(excluded, level) {
					s.Severities = append(s.Severities, level)
				}
			}
		}
	case key == "kind" || key == "is":
		for _, v := range strings.Split(strings.ToLower(value), ",") {
			if v != "log" && v != "exception" && v != "crash" {
				return fmt.Errorf("kind %q is not log, exception or crash", v)
			}
			s.Kinds = append(s.Kinds, v)
		}
	case key == "issue" || key == "fingerprint":
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("issue %q is not an issue id", value)
		}
		s.Fingerprint = n
	case strings.HasPrefix(key, "attr.") || strings.HasPrefix(key, "@"):
		name := strings.TrimPrefix(strings.TrimPrefix(key, "attr."), "@")
		if name == "" {
			return fmt.Errorf("an attribute filter needs a name, as in attr.region:eu")
		}
		if negated {
			s.NotAttributes[name] = value
		} else {
			s.Attributes[name] = value
		}
	default:
		column, known := fieldColumns[key]
		if !known {
			// Not a field we know: read it as the words it is, so "http://x" still searches.
			words := tokens(key + " " + value)
			if negated {
				s.NotWords = append(s.NotWords, words...)
			} else {
				s.Words = append(s.Words, words...)
			}
			return nil
		}
		if column == "install_id" {
			if _, err := uuid.Parse(value); err != nil {
				return fmt.Errorf("install %q is not an install id", value)
			}
		}
		if negated {
			s.NotFields[column] = append(s.NotFields[column], value)
		} else {
			s.Fields[column] = append(s.Fields[column], strings.Split(value, ",")...)
		}
	}
	return nil
}

// Where writes the search as SQL conditions over `logs`, with their arguments.
func (s *Search) Where() ([]string, []any) {
	var where []string
	var args []any
	if len(s.Words) > 0 {
		where = append(where, "hasAllTokens("+searchText+", ?)")
		args = append(args, dedupe(s.Words))
	}
	for _, phrase := range s.Phrases {
		where = append(where, "positionCaseInsensitiveUTF8("+rawText+", ?) > 0")
		args = append(args, phrase)
	}
	if len(s.NotWords) > 0 {
		where = append(where, "NOT hasAnyTokens("+searchText+", ?)")
		args = append(args, dedupe(s.NotWords))
	}
	if len(s.Severities) > 0 {
		where = append(where, "severity IN ?")
		args = append(args, dedupe(s.Severities))
	}
	if len(s.Kinds) > 0 {
		where = append(where, "kind IN ?")
		args = append(args, dedupe(s.Kinds))
	}
	for _, column := range sortedKeys(s.Fields) {
		values := s.Fields[column]
		switch column {
		case "install_id":
			where = append(where, "toString(install_id) IN ?")
		case "player_id":
			where = append(where, "ifNull(player_id, '') IN ?")
		default:
			where = append(where, column+" IN ?")
		}
		args = append(args, values)
	}
	for _, column := range sortedKeys(s.NotFields) {
		values := s.NotFields[column]
		switch column {
		case "install_id":
			where = append(where, "toString(install_id) NOT IN ?")
		case "player_id":
			where = append(where, "ifNull(player_id, '') NOT IN ?")
		default:
			where = append(where, column+" NOT IN ?")
		}
		args = append(args, values)
	}
	for _, name := range sortedKeys(s.Attributes) {
		where = append(where, "attributes[?] = ?")
		args = append(args, name, s.Attributes[name])
	}
	for _, name := range sortedKeys(s.NotAttributes) {
		where = append(where, "attributes[?] != ?")
		args = append(args, name, s.NotAttributes[name])
	}
	if s.Fingerprint != 0 {
		where = append(where, "fingerprint = ?")
		args = append(args, s.Fingerprint)
	}
	return where, args
}

// OnlyVolume reports whether the search can be answered from the per-minute counts: nothing in it
// but levels and kinds.
func (s *Search) OnlyVolume() bool {
	return len(s.Words) == 0 && len(s.NotWords) == 0 && len(s.Phrases) == 0 &&
		len(s.Fields) == 0 && len(s.NotFields) == 0 && len(s.Attributes) == 0 &&
		len(s.NotAttributes) == 0 && s.Fingerprint == 0
}

// splitTerms splits on spaces outside quotes, keeping a quoted phrase, or a field's quoted value,
// as one term.
func splitTerms(q string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range q {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case unicode.IsSpace(r) && !quoted:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// tokens splits text the way the index's splitByNonAlpha tokenizer does, lower-cased: runs of
// ASCII letters and digits, with everything else a separator. Anything else would search for a
// token the index never stored.
func tokens(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(wordBreaks.ReplaceAllString(text, "${1}${3}${5} ${2}${4}${6}")) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) || r >= 128 {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func knownSeverity(level string) bool { return contains(severityOrder, level) }

func contains(list []string, v string) bool {
	for _, entry := range list {
		if entry == v {
			return true
		}
	}
	return false
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
