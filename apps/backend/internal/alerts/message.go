package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
	{"reason", "Reason"},
	{"amount", "Amount"},
	{"balance", "Balance"},
	{"owed", "Owed"},
	{"suspends_at", "Suspended at"},
	{"environment", "Environment"},
}

// Render is the message an event makes: the kind's headline and sentence, a few rows of its data,
// and a link to where it is dealt with. A webhook channel is also given the event itself.
func Render(kind Kind, e Event, where Where, dashboard string) notify.Message {
	title := kind.Title
	switch {
	case where.Project != "" && where.Environment != "":
		title += " · " + where.Project + " (" + where.Environment + ")"
	case where.Project != "":
		title += " · " + where.Project
	case where.Organization != "":
		title += " · " + where.Organization
	}
	return notify.Message{
		Title:    title,
		Summary:  kind.Summary,
		Lines:    lines(e.Data),
		Severity: kind.Severity,
		Link:     linkFor(kind, dashboard),
		Payload: map[string]any{
			"id": e.UID, "type": e.Type, "occurred_at": e.At.UTC().Format(time.RFC3339),
			"organization": where.Organization, "project": where.Project,
			"environment": e.Environment, "data": e.Data,
		},
	}
}

func lines(data map[string]any) []string {
	out := make([]string, 0, maxLines)
	used := map[string]bool{}
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
		return fmt.Sprintf("%g", x), true
	case int, int64:
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
