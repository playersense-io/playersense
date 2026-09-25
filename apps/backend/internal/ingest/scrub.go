package ingest

// Personal data taken out of log text on arrival.
//
// The SDKs scrub on the device first (sdk/core/src/scrub.rs), and this is the same list again
// on the server: for an SDK that predates it, one written by a studio against the protocol
// document, and a device whose studio turned scrubbing off. Data that is never stored cannot
// leak, cannot be subpoenaed and never has to be found for an erasure request.
//
// Each pattern is guarded by a check that costs a byte scan, so a clean line, which is almost
// every line, never reaches the regular-expression engine.

import (
	"regexp"
	"strings"
)

var (
	userPathPattern = regexp.MustCompile(`(?i)(/Users/|/home/|\\Users\\)([^/\\\s"']+)`)
	jwtPattern      = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_=-]+\.[A-Za-z0-9_=-]+`)
	bearerPattern   = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[^\s,;"'()\[\]{}<>&?]+`)
	pairPattern     = regexp.MustCompile(
		`(?i)([A-Za-z0-9_.-]*(?:password|passwd|secret|token|apikey|api_key|api-key|authorization|session)[A-Za-z0-9_.-]*[=:])([^\s,;"'()\[\]{}<>&?]+)`)
	scrubEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	scrubCardPattern  = regexp.MustCompile(`\d(?:[ -]?\d){12,18}`)
	ipv4Pattern       = regexp.MustCompile(`(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)`)
)

// Scrub replaces emails, credentials, card numbers, IPv4 addresses and the account name in a
// file path with a marker naming what was there, so the line still reads and still groups.
func Scrub(text string) string {
	if text == "" {
		return text
	}
	if strings.Contains(text, "sers") || strings.Contains(text, "home/") {
		text = userPathPattern.ReplaceAllStringFunc(text, func(m string) string {
			parts := userPathPattern.FindStringSubmatch(m)
			if strings.EqualFold(parts[2], "shared") {
				return m
			}
			return parts[1] + "[user]"
		})
	}
	if strings.Contains(text, "eyJ") {
		text = jwtPattern.ReplaceAllStringFunc(text, func(m string) string {
			if len(m) > 30 {
				return "[token]"
			}
			return m
		})
	}
	if strings.ContainsAny(text, "=:") && mentionsSecret(text) {
		text = pairPattern.ReplaceAllString(text, "${1}[redacted]")
	}
	if containsFold(text, "bearer") || containsFold(text, "basic") {
		text = bearerPattern.ReplaceAllString(text, "${1}${2}[token]")
	}
	if strings.IndexByte(text, '@') >= 0 {
		text = scrubEmailPattern.ReplaceAllString(text, "[email]")
	}
	if digits(text) >= 13 {
		text = replaceBounded(text, scrubCardPattern, func(m string) bool { return luhn(m) }, "[card]")
	}
	if strings.Count(text, ".") >= 3 {
		text = replaceBounded(text, ipv4Pattern, func(string) bool { return true }, "[ip]")
	}
	return text
}

// replaceBounded replaces matches that stand alone: not part of a longer word, number or
// dotted version. RE2 has no lookaround, so the neighbours are checked here.
func replaceBounded(text string, pattern *regexp.Regexp, keep func(string) bool, marker string) string {
	matches := pattern.FindAllStringIndex(text, -1)
	if matches == nil {
		return text
	}
	var out strings.Builder
	copied := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > 0 && isWordOrDot(text[start-1]) {
			continue
		}
		if end < len(text) && isWordOrDot(text[end]) {
			continue
		}
		if !keep(text[start:end]) {
			continue
		}
		out.WriteString(text[copied:start])
		out.WriteString(marker)
		copied = end
	}
	if copied == 0 {
		return text
	}
	out.WriteString(text[copied:])
	return out.String()
}

func isWordOrDot(b byte) bool {
	return b == '.' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func digits(text string) int {
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] >= '0' && text[i] <= '9' {
			n++
		}
	}
	return n
}

func luhn(number string) bool {
	sum, n := 0, 0
	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if n%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		n++
	}
	return n >= 13 && sum%10 == 0
}

// mentionsSecret is the credential pattern's keywords, checked without the regex engine.
func mentionsSecret(text string) bool {
	for _, word := range [...]string{"pass", "secret", "token", "key", "auth", "session"} {
		if containsFold(text, word) {
			return true
		}
	}
	return false
}

// containsFold reports whether text holds word in any ASCII case, without allocating. word
// is lower case.
func containsFold(text, word string) bool {
	for i := 0; i+len(word) <= len(text); i++ {
		j := 0
		for ; j < len(word); j++ {
			c := text[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != word[j] {
				break
			}
		}
		if j == len(word) {
			return true
		}
	}
	return false
}
