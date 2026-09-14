package eventbus

import (
	"context"
	"fmt"
	"strconv"

	"cloud.google.com/go/pubsub"
)

// PubSub publishes to Google Pub/Sub, which §7.2 makes the default from day one.
//
// The publisher batches at the RPC level, so many SDK batches share a publish request —
// §20's point that the message count is not the hard part.
type PubSub struct {
	projectID string
	topicID   string

	client *pubsub.Client
	topic  *pubsub.Topic
}

// NewPubSub returns an unconnected bus.
func NewPubSub(projectID, topicID string) *PubSub {
	return &PubSub{projectID: projectID, topicID: topicID}
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
