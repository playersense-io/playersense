package notify

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"playersense.dev/backend/ent"
)

// ChannelOf is a stored channel with its secrets, ready to send to.
func ChannelOf(row *ent.NotificationChannel) Channel {
	ch := Channel{Type: Type(row.Type), Name: row.Name,
		URL: row.Secret["url"], RoutingKey: row.Secret["routing_key"],
		HeaderValue: row.Secret["header_value"], SigningSecret: row.Secret["signing_secret"]}
	if v, ok := row.Config["addresses"].([]any); ok {
		for _, a := range v {
			if s, ok := a.(string); ok {
				ch.Addresses = append(ch.Addresses, s)
			}
		}
	}
	ch.Severity, _ = row.Config["severity"].(string)
	ch.HeaderName, _ = row.Config["header_name"].(string)
	return ch
}

// Mask is a destination's URL with its credential hidden: the scheme, the host and the start of
// the path, which is enough to recognise it and not enough to use it.
func Mask(raw string) string {
	if raw == "" {
		return ""
	}
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return "…"
	}
	host, path, _ := strings.Cut(rest, "/")
	if len(path) > 8 {
		path = path[:8]
	}
	return scheme + "://" + host + "/" + path + "…"
}

// Deliver sends to a stored channel and writes the send to its delivery log. A test is also
// kept as the channel's last test, which is what its status says.
func (s *Sender) Deliver(ctx context.Context, client *ent.Client, row *ent.NotificationChannel, m Message, source string) Result {
	r := s.Send(ctx, ChannelOf(row), m)
	if err := client.ChannelDelivery.Create().
		SetProjectID(row.ProjectID).SetChannelID(row.ID).SetChannelName(row.Name).
		SetSource(clip(source, 200)).SetTitle(clip(m.Title, 300)).SetOk(r.OK).SetDetail(r.Detail).
		SetTest(m.Test).SetDurationMs(int(r.Duration / time.Millisecond)).Exec(ctx); err != nil {
		slog.Default().Warn("could not log a channel delivery", "channel", row.UID, "error", err)
	}
	if m.Test {
		if err := client.NotificationChannel.UpdateOne(row).SetLastTestAt(time.Now().UTC()).
			SetLastTestOk(r.OK).SetLastTestDetail(r.Detail).Exec(ctx); err != nil {
			slog.Default().Warn("could not keep a channel's last test", "channel", row.UID, "error", err)
		}
	}
	return r
}
