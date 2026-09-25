package alerts

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/ent/notificationrule"
	"playersense.dev/backend/internal/notify"
)

// What was sent to a project's channels, measured: the figures and charts of the Activity page, and
// the sparkline beside each rule. Read from the hourly rollup every logged send increments
// (notify.rollUp), so a page costs the same at ten notifications a day and at ten thousand.
//
// Tests are counted apart. A test is a real send, and its answer time is a real answer time, but
// "how many notifications did we send this week" does not mean the ones somebody sent by hand.

// Range is the window a page asks about, and the grain it is drawn at.
type Range struct {
	Name   string
	Bucket string // hour or day, a PostgreSQL date_trunc unit
	Count  int
}

// Ranges are the windows the Activity page offers.
var Ranges = map[string]Range{
	"24h": {Name: "24h", Bucket: "hour", Count: 24},
	"7d":  {Name: "7d", Bucket: "day", Count: 7},
	"30d": {Name: "30d", Bucket: "day", Count: 30},
}

func (r Range) step() time.Duration {
	if r.Bucket == "hour" {
		return time.Hour
	}
	return 24 * time.Hour
}

// Start is where the window begins: the first bucket's start, in UTC.
func (r Range) Start(now time.Time) time.Time {
	now = now.UTC()
	var last time.Time
	if r.Bucket == "hour" {
		last = now.Truncate(time.Hour)
	} else {
		last = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	return last.Add(-time.Duration(r.Count-1) * r.step())
}

// Totals are one window's figures.
type Totals struct {
	Sent      int
	Delivered int
	Failed    int
	Tests     int
	P50Ms     int
	P95Ms     int
}

// Bucket is one hour or day of sends.
type Bucket struct {
	At        time.Time
	Delivered int
	Failed    int
	Tests     int
}

// ChannelActivity is one channel's share of the window.
type ChannelActivity struct {
	ChannelID int
	UID       uuid.UUID // zero for a channel deleted since
	Name      string
	Type      string
	Sent      int
	Failed    int
	P50Ms     int
	LastAt    *time.Time
	LastOK    bool
	Spark     []int
}

// SourceActivity is what one rule or report sent in the window.
type SourceActivity struct {
	Source string
	RuleID *int
	Sent   int
	Failed int
}

// Activity is everything the Activity page draws.
type Activity struct {
	Range    Range
	Buckets  []Bucket
	Current  Totals
	Previous Totals
	Channels []ChannelActivity
	Sources  []SourceActivity
	// Waiting is queued and not tried yet; Retrying failed at least once and is due again.
	Waiting  int
	Retrying int
}

// ProjectActivity measures what was sent to a project's channels in a window, beside the window
// before it. Read from the hourly rollup (channel_delivery_hours), never from the log: thirty days
// is at most 720 rows a channel and source, however many notifications went out.
func ProjectActivity(ctx context.Context, client *ent.Client, projectID int, r Range, now time.Time) (Activity, error) {
	start := r.Start(now)
	span := time.Duration(r.Count) * r.step()
	out := Activity{Range: r}

	var err error
	if out.Current, err = totals(ctx, client, projectID, start, start.Add(span)); err != nil {
		return out, err
	}
	if out.Previous, err = totals(ctx, client, projectID, start.Add(-span), start); err != nil {
		return out, err
	}
	if out.Buckets, err = buckets(ctx, client, projectID, r, start); err != nil {
		return out, err
	}
	if out.Channels, err = channelActivity(ctx, client, projectID, r, start); err != nil {
		return out, err
	}
	if out.Sources, err = sources(ctx, client, projectID, start); err != nil {
		return out, err
	}
	err = scan(ctx, client, `
		SELECT count(*) FILTER (WHERE o.attempts = 0), count(*) FILTER (WHERE o.attempts > 0)
		FROM notification_outboxes o JOIN notification_channels c ON c.id = o.channel_id
		WHERE c.project_id = $1`, []any{projectID}, func(rows *sql.Rows) error {
		return rows.Scan(&out.Waiting, &out.Retrying)
	})
	return out, err
}

// histogram is the rollup's answer-time columns, summed.
const histogram = `sum(lat_100), sum(lat_250), sum(lat_500), sum(lat_1000), sum(lat_2500), sum(lat_5000),
	sum(lat_10000), sum(lat_over)`

// percentile reads a quantile off a histogram of answer times, interpolating within its bucket: an
// estimate, good to within the bucket's width, which is all a "median answer" needs.
func percentile(counts []int64, q float64) int {
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0
	}
	want := q * float64(total)
	var seen int64
	lower := 0
	for i, c := range counts {
		upper := 20000
		if i < len(notify.LatencyBounds) {
			upper = notify.LatencyBounds[i]
		}
		if c > 0 && float64(seen+c) >= want {
			within := (want - float64(seen)) / float64(c)
			return lower + int(within*float64(upper-lower))
		}
		seen += c
		lower = upper
	}
	return lower
}

func scanHistogram(rows *sql.Rows, dest ...any) ([]int64, error) {
	counts := make([]int64, 8)
	targets := append([]any{}, dest...)
	for i := range counts {
		targets = append(targets, &counts[i])
	}
	return counts, rows.Scan(targets...)
}

func totals(ctx context.Context, client *ent.Client, projectID int, from, to time.Time) (Totals, error) {
	var t Totals
	err := scan(ctx, client, `
		SELECT coalesce(sum(delivered), 0), coalesce(sum(failed), 0), coalesce(sum(tests), 0), `+coalesced(histogram)+`
		FROM channel_delivery_hours
		WHERE project_id = $1 AND hour >= $2 AND hour < $3`, []any{projectID, from, to},
		func(rows *sql.Rows) error {
			counts, err := scanHistogram(rows, &t.Delivered, &t.Failed, &t.Tests)
			t.Sent = t.Delivered + t.Failed
			t.P50Ms, t.P95Ms = percentile(counts, 0.5), percentile(counts, 0.95)
			return err
		})
	return t, err
}

// coalesced turns each sum in a list into a zero when there were no rows.
func coalesced(sums string) string {
	parts := strings.Split(sums, ",")
	for i, p := range parts {
		parts[i] = "coalesce(" + strings.TrimSpace(p) + ", 0)"
	}
	return strings.Join(parts, ", ")
}

// buckets is the window hour by hour or day by day, every bucket present, empty ones as zeros.
func buckets(ctx context.Context, client *ent.Client, projectID int, r Range, start time.Time) ([]Bucket, error) {
	out := make([]Bucket, r.Count)
	for i := range out {
		out[i].At = start.Add(time.Duration(i) * r.step())
	}
	err := scan(ctx, client, `
		SELECT date_trunc($2, hour AT TIME ZONE 'UTC') AS at, sum(delivered), sum(failed), sum(tests)
		FROM channel_delivery_hours
		WHERE project_id = $1 AND hour >= $3
		GROUP BY at`, []any{projectID, r.Bucket, start}, func(rows *sql.Rows) error {
		var at time.Time
		var b Bucket
		if err := rows.Scan(&at, &b.Delivered, &b.Failed, &b.Tests); err != nil {
			return err
		}
		if i := index(r, start, at); i >= 0 {
			b.At = out[i].At
			out[i] = b
		}
		return nil
	})
	return out, err
}

// index is which bucket a truncated time falls in, or -1 outside the window.
func index(r Range, start, at time.Time) int {
	at = time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), 0, 0, 0, time.UTC)
	i := int(at.Sub(start) / r.step())
	if at.Before(start) || i >= r.Count {
		return -1
	}
	return i
}

func channelActivity(ctx context.Context, client *ent.Client, projectID int, r Range, start time.Time) ([]ChannelActivity, error) {
	var out []ChannelActivity
	at := map[int]int{}
	err := scan(ctx, client, `
		SELECT channel_id, (array_agg(channel_name ORDER BY last_at DESC))[1],
		       sum(delivered) + sum(failed), sum(failed), max(last_at),
		       (array_agg(last_ok ORDER BY last_at DESC))[1], `+histogram+`
		FROM channel_delivery_hours
		WHERE project_id = $1 AND hour >= $2
		GROUP BY channel_id
		ORDER BY sum(delivered) + sum(failed) DESC, max(last_at) DESC`, []any{projectID, start},
		func(rows *sql.Rows) error {
			var c ChannelActivity
			var last time.Time
			counts, err := scanHistogram(rows, &c.ChannelID, &c.Name, &c.Sent, &c.Failed, &last, &c.LastOK)
			if err != nil {
				return err
			}
			c.P50Ms, c.LastAt, c.Spark = percentile(counts, 0.5), &last, make([]int, r.Count)
			at[c.ChannelID] = len(out)
			out = append(out, c)
			return nil
		})
	if err != nil || len(out) == 0 {
		return out, err
	}
	err = scan(ctx, client, `
		SELECT channel_id, date_trunc($2, hour AT TIME ZONE 'UTC') AS at, sum(delivered) + sum(failed)
		FROM channel_delivery_hours
		WHERE project_id = $1 AND hour >= $3
		GROUP BY channel_id, at`, []any{projectID, r.Bucket, start}, func(rows *sql.Rows) error {
		var id, n int
		var when time.Time
		if err := rows.Scan(&id, &when, &n); err != nil {
			return err
		}
		if c, found := at[id]; found {
			if b := index(r, start, when); b >= 0 {
				out[c].Spark[b] = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(out))
	for _, c := range out {
		ids = append(ids, c.ChannelID)
	}
	channels, err := client.NotificationChannel.Query().Where(notificationchannel.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read channels: %w", err)
	}
	for _, ch := range channels {
		i := at[ch.ID]
		out[i].UID, out[i].Type, out[i].Name = ch.UID, string(ch.Type), ch.Name
	}
	return out, nil
}

// sources is what each rule and report sent, most first: at most eight, the rest are rarely read.
// A rule is one row whatever it was called at the time, under its current name.
func sources(ctx context.Context, client *ent.Client, projectID int, start time.Time) ([]SourceActivity, error) {
	var out []SourceActivity
	var ruleIDs []int
	err := scan(ctx, client, `
		SELECT rule_id, source, sum(delivered) + sum(failed), sum(failed)
		FROM channel_delivery_hours
		WHERE project_id = $1 AND hour >= $2
		GROUP BY rule_id, source
		HAVING sum(delivered) + sum(failed) > 0
		ORDER BY sum(delivered) + sum(failed) DESC
		LIMIT 8`, []any{projectID, start}, func(rows *sql.Rows) error {
		var s SourceActivity
		var rule int
		if err := rows.Scan(&rule, &s.Source, &s.Sent, &s.Failed); err != nil {
			return err
		}
		if rule != 0 {
			id := rule
			s.RuleID = &id
			ruleIDs = append(ruleIDs, rule)
		}
		out = append(out, s)
		return nil
	})
	if err != nil || len(ruleIDs) == 0 {
		return out, err
	}
	rules, err := client.NotificationRule.Query().Where(notificationrule.IDIn(ruleIDs...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read rules: %w", err)
	}
	names := map[int]string{}
	for _, r := range rules {
		names[r.ID] = r.Name
	}
	for i, s := range out {
		if s.RuleID == nil {
			continue
		}
		if name, found := names[*s.RuleID]; found {
			out[i].Source = "Rule: " + name
		} else {
			out[i].Source = "A deleted rule"
		}
	}
	return out, nil
}

// RuleStat is one rule's last few days: what it sent, what failed, and a count a day.
type RuleStat struct {
	Sent   int
	Failed int
	LastAt *time.Time
	Spark  []int
}

// RuleStats is what each of these rules sent in the last `days` days, tests aside.
func RuleStats(ctx context.Context, client *ent.Client, ruleIDs []int, days int, now time.Time) (map[int]*RuleStat, error) {
	out := make(map[int]*RuleStat, len(ruleIDs))
	for _, id := range ruleIDs {
		out[id] = &RuleStat{Spark: make([]int, days)}
	}
	if len(ruleIDs) == 0 {
		return out, nil
	}
	r := Range{Bucket: "day", Count: days}
	start := r.Start(now)
	ids := make([]int64, len(ruleIDs))
	for i, id := range ruleIDs {
		ids[i] = int64(id)
	}
	err := scan(ctx, client, `
		SELECT rule_id, date_trunc('day', hour AT TIME ZONE 'UTC') AS at,
		       sum(delivered) + sum(failed), sum(failed),
		       max(last_at) FILTER (WHERE delivered + failed > 0)
		FROM channel_delivery_hours
		WHERE rule_id = ANY($1) AND hour >= $2
		GROUP BY rule_id, at`, []any{ids, start}, func(rows *sql.Rows) error {
		var id, sent, failed int
		var at time.Time
		var last sql.NullTime
		if err := rows.Scan(&id, &at, &sent, &failed, &last); err != nil {
			return err
		}
		s := out[id]
		if s == nil {
			return nil
		}
		s.Sent += sent
		s.Failed += failed
		if last.Valid && (s.LastAt == nil || last.Time.After(*s.LastAt)) {
			t := last.Time
			s.LastAt = &t
		}
		if i := index(r, start, at); i >= 0 {
			s.Spark[i] = sent
		}
		return nil
	})
	return out, err
}

func scan(ctx context.Context, client *ent.Client, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("alerts: activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
