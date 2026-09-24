package eventbus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// How far behind each consumer group is, for the operations console's queue page (§56, §67).

// Backlog is one group's position against the stream.
type Backlog struct {
	Group string

	// Messages and Bytes are what the group has not yet consumed.
	Messages int64
	Bytes    int64

	// OldestUnread is when the gateway accepted the oldest message the group has not
	// consumed: how far behind it is, as a time. Nil when it is caught up.
	OldestUnread *time.Time

	// DeadLettered is how many messages the group parked over the window it is read for.
	DeadLettered int64

	// Parked is the latest of them, without their payloads: why, when and whose. Only the
	// local bus can list them; Pub/Sub's are counted.
	Parked []Parked
}

// Parked is one dead-lettered message's reason.
type Parked struct {
	DeadLetterReason
	DedupKey   string `json:"dedup_key"`
	ProjectID  string `json:"project_id"`
	EventCount int    `json:"event_count"`
}

// BacklogReader answers how far behind each group is.
type BacklogReader interface {
	Backlog(ctx context.Context, group string, deadLetterWindow time.Duration) (Backlog, error)
	Name() string
}

// maxBacklogScan bounds how much of the local spool a backlog read counts lines in. The count
// is the only reason to read the bytes at all, and a development spool larger than this is
// behind by an amount the exact number does not change.
const maxBacklogScan = 256 << 20

// Backlog reads the local spool: every segment's unread bytes against the group's offsets, the
// first unread message's publish time, and the group's dead letters over the window.
func (l *Local) Backlog(ctx context.Context, group string, window time.Duration) (Backlog, error) {
	out := Backlog{Group: group}
	segments, err := filepath.Glob(filepath.Join(l.dir, "analytics-events-*.ndjson"))
	if err != nil {
		return out, err
	}
	sort.Strings(segments)

	scanned := int64(0)
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		info, err := os.Stat(segment)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return out, err
		}
		offset, err := l.readOffset(group, segment)
		if err != nil {
			return out, err
		}
		unread := info.Size() - offset
		if unread <= 0 {
			continue
		}
		out.Bytes += unread

		if out.OldestUnread == nil {
			if published, err := firstPublished(segment, offset); err == nil && !published.IsZero() {
				out.OldestUnread = &published
			}
		}
		if scanned < maxBacklogScan {
			n, read, err := countLines(segment, offset, maxBacklogScan-scanned)
			if err != nil {
				return out, err
			}
			out.Messages += n
			scanned += read
		}
	}

	parked, err := l.parked(group, time.Now().UTC().Add(-window))
	if err != nil {
		return out, err
	}
	out.DeadLettered = int64(len(parked))
	// The latest fifty: enough to see a pattern, and never a page of payloads.
	if len(parked) > 50 {
		parked = parked[len(parked)-50:]
	}
	for i, j := 0, len(parked)-1; i < j; i, j = i+1, j-1 {
		parked[i], parked[j] = parked[j], parked[i]
	}
	out.Parked = parked
	return out, nil
}

// parked reads a group's dead letters since a moment, oldest first.
func (l *Local) parked(group string, since time.Time) ([]Parked, error) {
	files, err := filepath.Glob(filepath.Join(l.dir, ".dead", group+"-*.ndjson"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	cutoff := since.Format("2006-01-02")
	var out []Parked
	for _, path := range files {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), group+"-"), ".ndjson")
		if day < cutoff {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for scanner.Scan() {
			var entry struct {
				DeadLetterReason
				Message *Message `json:"message"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
				continue
			}
			if entry.FailedAt.Before(since) {
				continue
			}
			one := Parked{DeadLetterReason: entry.DeadLetterReason}
			if entry.Message != nil {
				one.DedupKey, one.ProjectID, one.EventCount = entry.Message.DedupKey, entry.Message.ProjectID, entry.Message.EventCount
			}
			out = append(out, one)
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// firstPublished reads the publish time of the message starting at offset.
func firstPublished(path string, offset int64) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	reader := bufio.NewReader(io.NewSectionReader(f, offset, 16<<20))
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return time.Time{}, err
	}
	var head struct {
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &head); err != nil {
		return time.Time{}, err
	}
	return head.PublishedAt.UTC(), nil
}

// countLines counts whole messages from offset, reading at most limit bytes.
func countLines(path string, offset, limit int64) (int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	section := io.NewSectionReader(f, offset, limit)
	var lines, read int64
	for {
		n, err := section.Read(buf)
		lines += int64(bytes.Count(buf[:n], []byte{'\n'}))
		read += int64(n)
		if errors.Is(err, io.EOF) {
			return lines, read, nil
		}
		if err != nil {
			return lines, read, err
		}
	}
}

// PubSubBacklog reads a Pub/Sub deployment's backlog from Cloud Monitoring, which is where Pub/Sub
// publishes it: the subscription's undelivered messages and bytes and its oldest unacknowledged
// message's age, and the dead-letter topic's publishes over the window. The numbers trail by a
// minute or two, which is Monitoring's delay and not ours.
type PubSubBacklog struct {
	ProjectID string
	Topic     string

	once   sync.Once
	client *monitoring.MetricClient
	err    error
}

// NewPubSubBacklog returns a reader for the subscriptions NewPubSub creates.
func NewPubSubBacklog(projectID, topic string) *PubSubBacklog {
	return &PubSubBacklog{ProjectID: projectID, Topic: topic}
}

// Name identifies the reader.
func (p *PubSubBacklog) Name() string { return "pubsub" }

func (p *PubSubBacklog) metrics(ctx context.Context) (*monitoring.MetricClient, error) {
	p.once.Do(func() { p.client, p.err = monitoring.NewMetricClient(context.WithoutCancel(ctx)) })
	return p.client, p.err
}

// Backlog reads one group's subscription.
func (p *PubSubBacklog) Backlog(ctx context.Context, group string, window time.Duration) (Backlog, error) {
	out := Backlog{Group: group}
	client, err := p.metrics(ctx)
	if err != nil {
		return out, fmt.Errorf("cloud monitoring: %w", err)
	}
	subscription := p.Topic + "-" + group
	now := time.Now().UTC()
	latest := func(metric, label, id string) (int64, bool, error) {
		it := client.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
			Name: "projects/" + p.ProjectID,
			Filter: fmt.Sprintf(`metric.type = "pubsub.googleapis.com/%s" AND resource.labels.%s = "%s"`,
				metric, label, id),
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(now.Add(-10 * time.Minute)), EndTime: timestamppb.New(now),
			},
			View: monitoringpb.ListTimeSeriesRequest_FULL,
		})
		series, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		if len(series.Points) == 0 {
			return 0, false, nil
		}
		return series.Points[0].GetValue().GetInt64Value(), true, nil
	}

	if out.Messages, _, err = latest("subscription/num_undelivered_messages", "subscription_id", subscription); err != nil {
		return out, err
	}
	if out.Bytes, _, err = latest("subscription/backlog_bytes", "subscription_id", subscription); err != nil {
		return out, err
	}
	age, found, err := latest("subscription/oldest_unacked_message_age", "subscription_id", subscription)
	if err != nil {
		return out, err
	}
	if found && age > 0 {
		oldest := now.Add(-time.Duration(age) * time.Second)
		out.OldestUnread = &oldest
	}

	// Dead letters: every group parks to one topic, so the count is the topic's, and the same
	// for each group. The local bus can tell them apart; Pub/Sub can with the dlq_group
	// attribute, which is a subscription's to read, not a metric.
	it := client.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
		Name: "projects/" + p.ProjectID,
		Filter: fmt.Sprintf(`metric.type = "pubsub.googleapis.com/topic/send_message_operation_count" AND resource.labels.topic_id = "%s"`,
			p.Topic+"-dlq"),
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(now.Add(-window)), EndTime: timestamppb.New(now),
		},
		View: monitoringpb.ListTimeSeriesRequest_FULL,
	})
	for {
		series, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return out, err
		}
		for _, point := range series.Points {
			out.DeadLettered += point.GetValue().GetInt64Value()
		}
	}
	return out, nil
}

var _ BacklogReader = (*Local)(nil)
var _ BacklogReader = (*PubSubBacklog)(nil)
