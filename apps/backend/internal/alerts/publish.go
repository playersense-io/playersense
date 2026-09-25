package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/ent/notificationrule"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/webhooks"
)

// OwnersEvery is how often the owners are emailed about one event type: once a day. A card that
// keeps declining is retried every few hours, and one email a day says so without becoming noise.
const OwnersEvery = 24 * time.Hour

// Event is something that happened, for the rules that hear it.
type Event struct {
	OrganizationID int
	// ProjectID is empty for an event of the organization.
	ProjectID   int
	Environment string
	Type        string
	// Data is what the message is made from: amounts, reasons, names. Never a player.
	Data map[string]any
	// At is when it happened; now when zero.
	At time.Time
	// UID is the event's id; a new one when zero.
	UID uuid.UUID
}

// Publish queues an event of the organization for its rules and, when its kind says so, for the
// owners' email. Pass the transaction's client: the notifications exist exactly when the change
// that caused them does. It costs a read of the organization's rules, and only a write when
// something hears it.
func Publish(ctx context.Context, client *ent.Client, e Event) error {
	kind, ok := Lookup(e.Type)
	if !ok || kind.Scope != ScopeOrganization || e.ProjectID != 0 {
		return fmt.Errorf("alerts: %q is not an event of the organization", e.Type)
	}
	e = e.normalized()

	var rows []*ent.NotificationOutboxCreate
	if kind.Owners {
		due, err := markDue(ctx, client, fmt.Sprintf("owners:%d:%s", e.OrganizationID, e.Type), OwnersEvery, e.At)
		if err != nil {
			return err
		}
		if due {
			rows = append(rows, outboxRow(client, e, nil, nil))
		}
	}

	rules, err := client.NotificationRule.Query().Where(
		notificationrule.OrganizationID(e.OrganizationID),
		notificationrule.ProjectIDIsNil(),
		notificationrule.Enabled(true),
	).All(ctx)
	if err != nil {
		return fmt.Errorf("alerts: read the organization's rules: %w", err)
	}
	for _, rule := range rules {
		more, err := forRule(ctx, client, rule, e)
		if err != nil {
			return err
		}
		rows = append(rows, more...)
	}
	if len(rows) == 0 {
		return nil
	}
	return client.NotificationOutbox.CreateBulk(rows...).Exec(ctx)
}

// forRule is the rows one rule adds for an event, after its filters and its cooldown.
func forRule(ctx context.Context, client *ent.Client, rule *ent.NotificationRule, e Event) ([]*ent.NotificationOutboxCreate, error) {
	if !Hears(rule.Events, e.Type) || !HearsFrom(rule.Environments, e.Environment) || len(rule.Channels) == 0 {
		return nil, nil
	}
	if rule.MutedUntil != nil && e.At.Before(*rule.MutedUntil) {
		return nil, nil
	}
	if rule.CooldownMinutes > 0 {
		due, err := markDue(ctx, client, fmt.Sprintf("rule:%d:%s", rule.ID, e.Type),
			time.Duration(rule.CooldownMinutes)*time.Minute, e.At)
		if err != nil || !due {
			return nil, err
		}
	}
	channels, err := channelsOf(ctx, client, rule)
	if err != nil {
		return nil, err
	}
	rows := make([]*ent.NotificationOutboxCreate, 0, len(channels))
	for _, ch := range channels {
		ruleID, channelID := rule.ID, ch.ID
		rows = append(rows, outboxRow(client, e, &ruleID, &channelID))
	}
	if len(rows) > 0 {
		if err := client.NotificationRule.UpdateOneID(rule.ID).SetLastFiredAt(e.At).Exec(ctx); err != nil {
			return nil, fmt.Errorf("alerts: mark rule %d fired: %w", rule.ID, err)
		}
	}
	return rows, nil
}

// channelsOf is a rule's channels that still exist and are on. A rule of a project reaches only that
// project's; a rule of the organization, those of any of its projects.
func channelsOf(ctx context.Context, client *ent.Client, rule *ent.NotificationRule) ([]*ent.NotificationChannel, error) {
	q := client.NotificationChannel.Query().Where(
		notificationchannel.UIDIn(rule.Channels...), notificationchannel.Enabled(true))
	if rule.ProjectID != nil {
		q = q.Where(notificationchannel.ProjectID(*rule.ProjectID))
	} else {
		q = q.Where(notificationchannel.HasProjectWith(project.OrganizationID(rule.OrganizationID)))
	}
	channels, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("alerts: read rule %d's channels: %w", rule.ID, err)
	}
	return channels, nil
}

func outboxRow(client *ent.Client, e Event, ruleID, channelID *int) *ent.NotificationOutboxCreate {
	c := client.NotificationOutbox.Create().SetOrganizationID(e.OrganizationID).SetEventUID(e.UID).
		SetType(e.Type).SetEnvironment(e.Environment).SetData(e.Data).SetOccurredAt(e.At).
		SetNextAt(time.Now().UTC()).SetNillableRuleID(ruleID).SetNillableChannelID(channelID)
	if e.ProjectID != 0 {
		c = c.SetProjectID(e.ProjectID)
	}
	return c
}

func (e Event) normalized() Event {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.At = e.At.UTC()
	if e.UID == uuid.Nil {
		e.UID = uuid.New()
	}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	return e
}

// markDue reports whether something last noted under key is at least `every` old, and notes it now
// if so. One statement, so two workers racing for the same key cannot both be told yes.
func markDue(ctx context.Context, client *ent.Client, key string, every time.Duration, at time.Time) (bool, error) {
	rows, err := client.QueryContext(ctx, `
		INSERT INTO notification_marks (key, fired_at) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET fired_at = EXCLUDED.fired_at
		WHERE notification_marks.fired_at <= EXCLUDED.fired_at - $3::interval
		RETURNING id`, key, at, fmt.Sprintf("%d seconds", int(every.Seconds())))
	if err != nil {
		return false, fmt.Errorf("alerts: note %s: %w", key, err)
	}
	defer rows.Close()
	due := rows.Next()
	return due, rows.Err()
}

// Sink queues a project's events for its rules. The webhook dispatcher hands it every event it
// takes, before it delivers any, so an event is queued for people exactly when it is for endpoints.
//
// Rules are read once per project every RulesTTL rather than per event: a project with no rules,
// which is most of them, costs its events a map lookup, whatever their number.
type Sink struct {
	Client   *ent.Client
	RulesTTL time.Duration

	mu    sync.Mutex
	cache map[int]cachedRules
}

type cachedRules struct {
	at    time.Time
	orgID int
	rules []*ent.NotificationRule
}

var _ webhooks.AlertSink = (*Sink)(nil)

// Enqueue queues one event for the project's rules that hear it. Safe to call twice for one event:
// its rows are keyed by the event, the rule and the channel.
func (s *Sink) Enqueue(ctx context.Context, e webhooks.ClaimedEvent) error {
	orgID, rules, err := s.rulesOf(ctx, e.ProjectID)
	if err != nil || len(rules) == 0 {
		return err
	}
	ev := Event{OrganizationID: orgID, ProjectID: e.ProjectID, Environment: e.Environment, Type: e.Type,
		Data: dataOf(e.Body), At: e.CreatedAt, UID: e.EventUID}.normalized()
	var rows []*ent.NotificationOutboxCreate
	for _, rule := range rules {
		more, err := forRule(ctx, s.Client, rule, ev)
		if err != nil {
			return err
		}
		rows = append(rows, more...)
	}
	if len(rows) == 0 {
		return nil
	}
	return s.Client.NotificationOutbox.CreateBulk(rows...).
		OnConflict(sql.ConflictColumns("event_uid", "rule_id", "channel_id")).DoNothing().Exec(ctx)
}

func (s *Sink) rulesOf(ctx context.Context, projectID int) (int, []*ent.NotificationRule, error) {
	ttl := s.RulesTTL
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	s.mu.Lock()
	if c, ok := s.cache[projectID]; ok && time.Since(c.at) < ttl {
		s.mu.Unlock()
		return c.orgID, c.rules, nil
	}
	s.mu.Unlock()

	p, err := s.Client.Project.Get(ctx, projectID)
	if ent.IsNotFound(err) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("alerts: read project %d: %w", projectID, err)
	}
	rules, err := s.Client.NotificationRule.Query().Where(
		notificationrule.ProjectID(projectID), notificationrule.Enabled(true)).All(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("alerts: read project %d's rules: %w", projectID, err)
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[int]cachedRules{}
	}
	s.cache[projectID] = cachedRules{at: time.Now(), orgID: p.OrganizationID, rules: rules}
	s.mu.Unlock()
	return p.OrganizationID, rules, nil
}

// dataOf is a webhook envelope's data, for the message. An envelope that does not read is a
// message without detail, not a lost notification.
func dataOf(body []byte) map[string]any {
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return map[string]any{}
	}
	return envelope.Data
}
