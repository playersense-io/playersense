package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/internal/notify"
)

// Where is whose event a message is about, by name, for its headline.
type Where struct {
	Organization string
	Project      string
	Environment  string
}

// maxLines bounds a message's body: a notification is read in a glance, and the full event is one
// link away.
const maxLines = 8

// labels name the data a message shows, in the order it shows them. Anything else in the data is
// shown after, by its key, if it is a plain value.
var labels = []struct{ key, label string }{
	{"culprit", "Where"},
	{"exception_type", "Exception"},
	{"level", "Level"},
	{"release", "Release"},
	{"events", "Events"},
	{"players", "Players"},
	{"this_hour", "This hour"},
	{"usual_per_hour", "Usually an hour"},
	{"reason", "Reason"},
	{"amount", "Amount"},
	{"balance", "Balance"},
	{"owed", "Owed"},
	{"suspends_at", "Suspended at"},
	{"environment", "Environment"},
}

// Render is the message an event makes: the kind's headline and sentence, a few rows of its data,
// and a link to where it is dealt with. A webhook channel is also given the event itself. An issue's
// event leads with the issue itself: its title as the sentence, and a link to its page.
func Render(kind Kind, e Event, where Where, dashboard string) notify.Message {
	title, summary, link := kind.Title, kind.Summary, linkFor(kind, dashboard)
	if IsIssueEvent(e.Type) {
		title = issueHeadline(e.Type, 1, e.Data["kind"] == "crash")
		if t, _ := e.Data["issue_title"].(string); t != "" {
			summary = clipText(t, 300)
		}
		if fp, _ := e.Data["fingerprint"].(string); fp != "" && dashboard != "" {
			link = strings.TrimRight(dashboard, "/") + "/logs/issues/" + fp
		}
	}
	return notify.Message{
		Title:    title + suffix(where),
		Summary:  summary,
		Lines:    lines(e.Data),
		Severity: kind.Severity,
		Link:     link,
		Payload: map[string]any{
			"id": e.UID, "type": e.Type, "occurred_at": e.At.UTC().Format(time.RFC3339),
			"organization": where.Organization, "project": where.Project,
			"environment": e.Environment, "data": e.Data,
		},
	}
}

// unlisted are data fields a message never shows as a line: the issue's own title is its sentence,
// its kind is in the headline, and a fingerprint is for machines.
var unlisted = map[string]bool{"issue_title": true, "kind": true, "fingerprint": true, "first_seen": true}

func lines(data map[string]any) []string {
	out := make([]string, 0, maxLines)
	used := map[string]bool{}
	for k := range unlisted {
		used[k] = true
	}
	for _, l := range labels {
		if v, ok := plain(data[l.key]); ok {
			out = append(out, l.label+": "+v)
			used[l.key] = true
		}
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		if !used[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(out) == maxLines {
			break
		}
		if v, ok := plain(data[k]); ok {
			out = append(out, headline(k)+": "+v)
		}
	}
	return out
}

// plain is a value as a line shows it; nested objects are for the webhook's payload, not a line.
func plain(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		if x == "" {
			return "", false
		}
		if len(x) > 200 {
			x = x[:200] + "…"
		}
		return x, true
	case bool:
		if x {
			return "yes", true
		}
		return "no", true
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x)), true
		}
		return fmt.Sprintf("%.1f", x), true
	case int, int64, uint64:
		return fmt.Sprintf("%d", x), true
	default:
		return "", false
	}
}

func linkFor(kind Kind, dashboard string) string {
	base := strings.TrimRight(dashboard, "/")
	if base == "" {
		return ""
	}
	if kind.Group == "billing" {
		return base + "/settings/billing"
	}
	return base + "/"
}

// ownersBody is the owners' email: the message as text, and why they are getting it.
func ownersBody(m notify.Message, organization string) string {
	var b strings.Builder
	b.WriteString(m.Summary)
	b.WriteString("\n\n")
	for _, l := range m.Lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	if m.Link != "" {
		b.WriteString("\n")
		b.WriteString(m.Link)
		b.WriteString("\n")
	}
	b.WriteString("\nYou are receiving this because you are an owner, admin or billing member of ")
	b.WriteString(organization)
	b.WriteString(". It is sent whatever the notification rules say, at most once a day for each kind of event.\n")
	return b.String()
}

// suffix is whose event a headline is about: " · Moonrise (production)".
func suffix(where Where) string {
	switch {
	case where.Project != "" && where.Environment != "":
		return " · " + where.Project + " (" + where.Environment + ")"
	case where.Project != "":
		return " · " + where.Project
	case where.Organization != "":
		return " · " + where.Organization
	}
	return ""
}

// issueHeadline is "New crash", "3 new issues", "Issue regressed", "12 issues spiking".
func issueHeadline(t string, n int, crashes bool) string {
	noun, nouns := "issue", "issues"
	if crashes {
		noun, nouns = "crash", "crashes"
	}
	switch {
	case t == IssueNew && n == 1:
		return "New " + noun
	case t == IssueNew:
		return fmt.Sprintf("%d new %s", n, nouns)
	case t == IssueRegressed && n == 1:
		return strings.ToUpper(noun[:1]) + noun[1:] + " regressed"
	case t == IssueRegressed:
		return fmt.Sprintf("%d %s regressed", n, nouns)
	case n == 1:
		return strings.ToUpper(noun[:1]) + noun[1:] + " spiking"
	default:
		return fmt.Sprintf("%d %s spiking", n, nouns)
	}
}

// digestLines is how many of a digest's issues are listed; the rest are counted.
const digestLines = 6

// RenderDigest is one message for many issues' events of one kind: a bad release's hundred new
// issues as one post, listing the most frequent, rather than a hundred posts.
func RenderDigest(kind Kind, events []Event, where Where, dashboard string) notify.Message {
	sorted := append([]Event(nil), events...)
	count := func(e Event) float64 {
		n, _ := e.Data["events"].(float64)
		return n
	}
	sort.SliceStable(sorted, func(i, j int) bool { return count(sorted[i]) > count(sorted[j]) })
	crashes := true
	for _, e := range sorted {
		if e.Data["kind"] != "crash" {
			crashes = false
		}
	}
	lines := make([]string, 0, digestLines+1)
	payload := make([]map[string]any, 0, len(sorted))
	for i, e := range sorted {
		payload = append(payload, e.Data)
		if i >= digestLines {
			continue
		}
		title, _ := e.Data["issue_title"].(string)
		if title == "" {
			title, _ = e.Data["exception_type"].(string)
		}
		line := clipText(title, 140)
		if n := count(e); n > 0 {
			line += fmt.Sprintf(" (%d events)", int64(n))
		}
		lines = append(lines, line)
	}
	if len(sorted) > digestLines {
		lines = append(lines, fmt.Sprintf("and %d more", len(sorted)-digestLines))
	}
	link := ""
	if dashboard != "" {
		link = strings.TrimRight(dashboard, "/") + "/logs/issues"
	}
	return notify.Message{
		Title:    issueHeadline(events[0].Type, len(events), crashes) + suffix(where),
		Summary:  "The most frequent first. Each is on the Issues page with its stack and who it hit.",
		Lines:    lines,
		Severity: kind.Severity,
		Link:     link,
		Payload: map[string]any{"type": events[0].Type, "count": len(events), "project": where.Project,
			"environment": where.Environment, "issues": payload},
	}
}

// RenderHeldBack is a rule's one notice that it reached its limit for the hour.
func RenderHeldBack(rule *ent.NotificationRule, hour time.Time, where Where, dashboard string) notify.Message {
	link := ""
	if dashboard != "" {
		link = strings.TrimRight(dashboard, "/") + "/notifications/activity"
	}
	return notify.Message{
		Title: "Notifications held back · " + rule.Name,
		Summary: fmt.Sprintf("This rule sent its %d notifications for the hour. The rest are held back until %s UTC, "+
			"so a flood cannot bury this channel; everything is still on the dashboard.",
			rule.MaxPerHour, hour.Add(time.Hour).Format("15:04")),
		Severity: "warning",
		Link:     link,
		Payload:  map[string]any{"type": HeldBack, "rule": rule.Name, "max_per_hour": rule.MaxPerHour, "project": where.Project},
	}
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
