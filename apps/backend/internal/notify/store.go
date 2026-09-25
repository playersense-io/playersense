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

// Deliver sends to a stored channel and records the send: a row of its delivery log and a count in
// its hour's rollup, in one transaction, so the Activity page's figures never drift from the log.
// ruleID is the notification rule that sent it, if one did, so a renamed rule keeps one history. A
// test is also kept as the channel's last test, which is what its status says.
//
// The send itself is never undone: a record that could not be written is a warning in the process
// log, not a notification that did not happen.
func (s *Sender) Deliver(ctx context.Context, client *ent.Client, row *ent.NotificationChannel, m Message,
	source string, ruleID *int) Result {
	r := s.Send(ctx, ChannelOf(row), m)
	now := time.Now().UTC()
	if err := record(ctx, client, row, m, r, source, ruleID, now); err != nil {
		slog.Default().Warn("could not record a channel delivery", "channel", row.UID, "error", err)
	}
	if m.Test {
		if err := client.NotificationChannel.UpdateOne(row).SetLastTestAt(now).
			SetLastTestOk(r.OK).SetLastTestDetail(r.Detail).Exec(ctx); err != nil {
			slog.Default().Warn("could not keep a channel's last test", "channel", row.UID, "error", err)
		}
	}
	return r
}

// record writes one send to the log and to its hour's rollup, both or neither.
func record(ctx context.Context, client *ent.Client, row *ent.NotificationChannel, m Message, r Result,
	source string, ruleID *int, at time.Time) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.ChannelDelivery.Create().SetNillableRuleID(ruleID).
		SetProjectID(row.ProjectID).SetChannelID(row.ID).SetChannelName(row.Name).
		SetSource(clip(source, 200)).SetTitle(clip(m.Title, 300)).SetOk(r.OK).SetDetail(r.Detail).
		SetTest(m.Test).SetDurationMs(int(r.Duration / time.Millisecond)).SetCreatedAt(at).Exec(ctx); err != nil {
		return err
	}
	if err := RollUp(ctx, tx.Client(), row, r, m.Test, source, ruleID, at); err != nil {
		return err
	}
	return tx.Commit()
}

// LatencyBounds are the upper edges, in milliseconds, of the rollup's answer-time histogram; a
// ninth bucket holds everything slower.
var LatencyBounds = []int{100, 250, 500, 1000, 2500, 5000, 10000}

var latencyColumns = []string{"lat_100", "lat_250", "lat_500", "lat_1000", "lat_2500", "lat_5000", "lat_10000", "lat_over"}

// RollUp adds one send, made at `at`, to its hour's row (channel_delivery_hours), in one upsert. A
// rule's sends carry no source, so a renamed rule stays one history; a test is counted in its own
// column.
func RollUp(ctx context.Context, client *ent.Client, row *ent.NotificationChannel, r Result, test bool,
	source string, ruleID *int, at time.Time) error {
	now := at.UTC()
	rule := 0
	if ruleID != nil {
		rule, source = *ruleID, ""
	}
	if test {
		source = ""
	}
	delivered, failed, tests := 0, 0, 0
	switch {
	case test:
		tests = 1
	case r.OK:
		delivered = 1
	default:
		failed = 1
	}
	ms := int(r.Duration / time.Millisecond)
	bucket := len(LatencyBounds)
	for i, bound := range LatencyBounds {
		if ms <= bound {
			bucket = i
			break
		}
	}
	lat := latencyColumns[bucket]
	_, err := client.ExecContext(ctx, `
		INSERT INTO channel_delivery_hours
			(project_id, hour, channel_id, rule_id, source, channel_name, delivered, failed, tests, `+lat+`, last_at, last_ok,
			 `+strings.Join(otherColumns(lat), ", ")+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $11`+strings.Repeat(", 0", len(latencyColumns)-1)+`)
		ON CONFLICT (project_id, hour, channel_id, rule_id, source) DO UPDATE SET
			channel_name = EXCLUDED.channel_name,
			delivered = channel_delivery_hours.delivered + EXCLUDED.delivered,
			failed = channel_delivery_hours.failed + EXCLUDED.failed,
			tests = channel_delivery_hours.tests + EXCLUDED.tests,
			`+lat+` = channel_delivery_hours.`+lat+` + 1,
			last_ok = CASE WHEN EXCLUDED.last_at >= channel_delivery_hours.last_at
				THEN EXCLUDED.last_ok ELSE channel_delivery_hours.last_ok END,
			last_at = GREATEST(channel_delivery_hours.last_at, EXCLUDED.last_at)`,
		row.ProjectID, now.Truncate(time.Hour), row.ID, rule, clip(source, 200), clip(row.Name, 120),
		delivered, failed, tests, now, r.OK)
	return err
}

func otherColumns(except string) []string {
	out := make([]string, 0, len(latencyColumns)-1)
	for _, c := range latencyColumns {
		if c != except {
			out = append(out, c)
		}
	}
	return out
}
