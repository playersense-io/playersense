package eventbus

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
)

// PubSub publishes to Google Pub/Sub, which §7.2 makes the default from day one.
//
// The publisher batches at the RPC level, so many SDK batches share a publish request —
// §20's point that the message count is not the hard part.
type PubSub struct {
	projectID string
	topicID   string

	// maxRows, maxMessages and maxWait decide when a group is handed over: whichever
	// comes first.
	//
	// Rows is the primary trigger, because rows are what ClickHouse cares about and
	// messages are not. An SDK batch is anywhere from one event to a thousand (§19.1), so
	// two hundred messages is two hundred rows from one integration and two hundred
	// thousand from another — sizing blocks on message count means not sizing them at all.
	//
	// maxMessages is a memory and outstanding-message bound rather than a target, and
	// maxWait is what stops a partly-filled group waiting forever. See ADR 0023.
	maxRows     int
	maxMessages int
	maxWait     time.Duration

	client *pubsub.Client
	topic  *pubsub.Topic

	dlqOnce sync.Once
	dlq     *pubsub.Topic
}

// NewPubSub returns an unconnected bus.
func NewPubSub(projectID, topicID string) *PubSub {
	return &PubSub{projectID: projectID, topicID: topicID}
}

// SetDelivery bounds a group by rows, by messages and by age. Whichever is reached first
// hands the group over.
//
// The wait is the cost of the grouping and it is paid on a quiet cell rather than a busy
// one: a trickle waits the full interval, and a cell near §4's ceiling reaches the row
// target in a fraction of it.
func (p *PubSub) SetDelivery(maxRows, maxMessages int, maxWait time.Duration) {
	p.maxRows, p.maxMessages, p.maxWait = maxRows, maxMessages, maxWait
}

func (p *PubSub) delivery() (int, int, time.Duration) {
	rows, msgs, wait := p.maxRows, p.maxMessages, p.maxWait
	if rows <= 0 {
		rows = 100_000
	}
	if msgs <= 0 {
		msgs = 1_000
	}
	if wait <= 0 {
		wait = 2 * time.Second
	}
	return rows, msgs, wait
}

// Name identifies the component in start-up and shutdown logs.
func (p *PubSub) Name() string { return "eventbus-pubsub" }

// Start dials Pub/Sub and resolves the topic.
func (p *PubSub) Start(ctx context.Context) error {
	client, err := pubsub.NewClient(ctx, p.projectID)
	if err != nil {
		return fmt.Errorf("dial pubsub: %w", err)
	}
	p.client = client
	p.topic = client.Topic(p.topicID)
	return nil
}

// Stop flushes anything buffered and closes the client.
//
// Stop runs after the server has drained, so the buffered publishes here belong to
// requests that were already answered 202. Losing them would make that answer false,
// which is why the topic is stopped rather than the client simply closed.
func (p *PubSub) Stop(context.Context) error {
	if p.topic != nil {
		p.topic.Stop()
	}
	if p.dlq != nil {
		p.dlq.Stop()
	}
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// Publish sends one batch and waits for the server to acknowledge it.
func (p *PubSub) Publish(ctx context.Context, msg *Message) error {
	if p.topic == nil {
		return fmt.Errorf("pubsub bus is not started")
	}
	result := p.topic.Publish(ctx, &pubsub.Message{
		Data: msg.Payload,
		// Attributes let a subscription filter without decoding the payload, and carry
		// the tenant the writer needs so it never reaches PostgreSQL (§64).
		Attributes: map[string]string{
			"dedup_key":       msg.DedupKey,
			"org_seq":         strconv.FormatUint(uint64(msg.OrgSeq), 10),
			"project_seq":     strconv.FormatUint(uint64(msg.ProjectSeq), 10),
			"environment_seq": strconv.FormatUint(uint64(msg.EnvironmentSeq), 10),
			"org_id":          msg.OrgID,
			"project_id":      msg.ProjectID,
			"environment_id":  msg.EnvironmentID,
			"policy_tier":     msg.PolicyTier,
			"retention_tier":  strconv.FormatUint(uint64(msg.RetentionTier), 10),
			"cell":            msg.Cell,
			// Both of these are read back by applyAttributes and neither was sent, so a
			// Pub/Sub deployment wrote its archive under `env=` with a zero timestamp
			// (§14.1). The local bus round-trips the whole struct as JSON, which is why
			// nothing local ever noticed.
			"environment":  msg.Environment,
			"published_at": msg.PublishedAt.UTC().Format(time.RFC3339Nano),
			// So a consumer can group by rows rather than by messages, without decoding
			// every payload to find out how many events are in it.
			"event_count": strconv.Itoa(msg.EventCount),
		},
		// Pub/Sub's own deduplication is best effort; §21 says to design the observable
		// result to be idempotent rather than to claim exactly-once. The key travels so
		// the writer can do that.
		OrderingKey: "",
	})
	// Waiting is the point: §19 answers 202 only once the batch is durably queued.
	if _, err := result.Get(ctx); err != nil {
		return fmt.Errorf("publish to %s: %w", p.topicID, err)
	}
	return nil
}

// DeadLetter publishes to the topic §20 names, so the subscription can move past a message
// nothing can process.
//
// A separate topic rather than Pub/Sub's own dead-letter policy, which is configured on the
// subscription and moves a message after a fixed number of delivery attempts. That is the
// right mechanism for a consumer that crashes and the wrong one here: a writer that cannot
// insert because ClickHouse is down would have its whole backlog moved to the dead-letter
// topic by attempt count alone. The consumer knows the difference between a poisoned
// message and an unavailable database; the subscription does not. Both can be configured,
// with the server-side policy set generously as a backstop.
func (p *PubSub) DeadLetter(ctx context.Context, msg *Message, reason DeadLetterReason) error {
	if p.client == nil {
		return fmt.Errorf("pubsub bus is not started")
	}
	p.dlqOnce.Do(func() { p.dlq = p.client.Topic(p.topicID + "-dlq") })

	if reason.FailedAt.IsZero() {
		reason.FailedAt = time.Now().UTC()
	}
	result := p.dlq.Publish(ctx, &pubsub.Message{
		Data: msg.Payload,
		Attributes: map[string]string{
			"dedup_key":      msg.DedupKey,
			"cell":           msg.Cell,
			"org_id":         msg.OrgID,
			"project_id":     msg.ProjectID,
			"environment_id": msg.EnvironmentID,
			"environment":    msg.Environment,
			"event_count":    strconv.Itoa(msg.EventCount),
			// Why it is here, so an operator does not have to reproduce the failure to
			// find out.
			"dlq_group":    reason.Group,
			"dlq_reason":   reason.Reason,
			"dlq_attempts": strconv.Itoa(reason.Attempts),
			"dlq_at":       reason.FailedAt.UTC().Format(time.RFC3339Nano),
		},
	})
	// Waited on, because the caller acknowledges the original immediately afterwards: a
	// dead letter that was not durable is a batch that was accepted and then forgotten.
	if _, err := result.Get(ctx); err != nil {
		return fmt.Errorf("publish to %s-dlq: %w", p.topicID, err)
	}
	return nil
}

// Subscribe consumes the subscription, acknowledging only after the handler returns.
//
// Messages are grouped before they reach the handler. Receive hands over one message per
// callback, and handing those straight on would mean one ClickHouse insert — and therefore
// one part — per SDK batch, which is what stops a node at §4's ceiling. So a callback parks
// its message in a group and returns; the group is handed over whole when it is full or
// when it has waited long enough.
//
// A message is acknowledged only after the group's handler returns nil, which is Appendix
// C.2 unchanged: the effect is durable before the queue forgets the message. What does
// change is the unit — a whole group is acknowledged or nacked together, so one message
// failing replays the others with it. That is deliberate, and it is the exchange ADR 0023
// records: the handler is idempotent, so a replay costs work rather than correctness.
//
// Holding messages unacknowledged is bounded on both sides. maxMessages must stay under
// the subscription's MaxOutstandingMessages or Receive stops pulling, and the client keeps
// extending the acknowledgement deadline of anything outstanding, so parking a message for
// maxWait does not make it redeliver.
func (p *PubSub) Subscribe(ctx context.Context, group string, handle Handler) error {
	if p.client == nil {
		return fmt.Errorf("pubsub bus is not started")
	}
	if group == "" {
		return fmt.Errorf("a subscription needs a group")
	}
	maxRows, maxMessages, maxWait := p.delivery()

	g := &grouper{handle: handle, maxRows: maxRows, maxMessages: maxMessages}

	// The timer flushes a partly-filled group, which is what keeps a quiet cell from
	// holding a handful of messages until enough arrive to fill one.
	ticker := time.NewTicker(maxWait)
	defer ticker.Stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.flush(ctx)
			}
		}
	}()

	sub := p.client.Subscription(p.subscriptionID(group))
	err := sub.Receive(ctx, func(ctx context.Context, m *pubsub.Message) {
		msg := &Message{
			Payload:  m.Data,
			DedupKey: m.Attributes["dedup_key"],
			Cell:     m.Attributes["cell"],
		}
		applyAttributes(msg, m.Attributes)
		g.add(ctx, msg, m)
	})

	// Receive has returned, so nothing new arrives. Anything still parked was never
	// acknowledged; nacking it returns it to the subscription rather than letting it sit
	// until its deadline expires.
	g.drain()
	return err
}

// grouper parks messages until there are enough of them to be worth one insert.
type grouper struct {
	handle      Handler
	maxRows     int
	maxMessages int

	mu   sync.Mutex
	msgs []*Message
	acks []*pubsub.Message
	rows int

	// flushing serializes handler calls, so two groups are never inserted concurrently.
	// Concurrency here would buy throughput the writer does not need — at a billion events
	// a day and a hundred thousand rows a block this is on the order of one insert a
	// second — and would cost the thing it is here to protect, which is block size.
	flushing sync.Mutex
}

func (g *grouper) add(ctx context.Context, msg *Message, ack *pubsub.Message) {
	g.mu.Lock()
	g.msgs = append(g.msgs, msg)
	g.acks = append(g.acks, ack)
	g.rows += msg.EventCount
	// Rows first: that is the unit the insert is sized in. The message cap is the backstop
	// for a producer that did not report a count, which would otherwise let the group grow
	// without bound.
	full := g.rows >= g.maxRows || len(g.msgs) >= g.maxMessages
	g.mu.Unlock()

	if full {
		g.flush(ctx)
	}
}

// flush hands the parked group to the handler and settles it.
func (g *grouper) flush(ctx context.Context) {
	g.flushing.Lock()
	defer g.flushing.Unlock()

	g.mu.Lock()
	msgs, acks := g.msgs, g.acks
	g.msgs, g.acks, g.rows = nil, nil, 0
	g.mu.Unlock()

	if len(msgs) == 0 {
		return
	}
	if err := g.handle(ctx, msgs); err != nil {
		// Nack rather than ack: the effect did not land, and §21's at-least-once delivery
		// is the mechanism that gets it another chance.
		settle(acks, (*pubsub.Message).Nack)
		return
	}
	settle(acks, (*pubsub.Message).Ack)
}

// settle acknowledges or returns a group.
//
// The nil check is what lets the grouper be exercised without a live subscription: the
// flush triggers decide the insert rate, and a unit that can only be tested against real
// Pub/Sub is a unit whose triggers are tested by nobody.
func settle(acks []*pubsub.Message, f func(*pubsub.Message)) {
	for _, a := range acks {
		if a != nil {
			f(a)
		}
	}
}

// drain returns anything still parked when the subscription stops.
func (g *grouper) drain() {
	g.mu.Lock()
	acks := g.acks
	g.msgs, g.acks, g.rows = nil, nil, 0
	g.mu.Unlock()
	settle(acks, (*pubsub.Message).Nack)
}

// subscriptionID names one consumer's subscription on the topic.
//
// One per group per topic per cell (§20): the storage writer and the archive writer read
// the same stream independently, and a cell's consumers read only their own cell's
// messages.
func (p *PubSub) subscriptionID(group string) string { return p.topicID + "-" + group }

func applyAttributes(msg *Message, attrs map[string]string) {
	if t, err := time.Parse(time.RFC3339Nano, attrs["published_at"]); err == nil {
		msg.PublishedAt = t
	}
	msg.OrgID = attrs["org_id"]
	msg.ProjectID = attrs["project_id"]
	msg.EnvironmentID = attrs["environment_id"]
	msg.Environment = attrs["environment"]
	msg.PolicyTier = attrs["policy_tier"]
	msg.OrgSeq = uint32(parseUint(attrs["org_seq"], 32))
	msg.ProjectSeq = uint32(parseUint(attrs["project_seq"], 32))
	msg.EnvironmentSeq = uint16(parseUint(attrs["environment_seq"], 16))
	msg.RetentionTier = uint16(parseUint(attrs["retention_tier"], 16))
	msg.EventCount = int(parseUint(attrs["event_count"], 32))
}

func parseUint(s string, bits int) uint64 {
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0
	}
	return n
}
