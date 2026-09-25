package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/notificationoutbox"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/role"
	"playersense.dev/backend/internal/mail"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/pgdb"
)

// MaxAttempts is how often a notification is tried before it is given up on: over about four hours,
// which covers a Slack or PagerDuty outage without sending yesterday's news tomorrow.
const MaxAttempts = 7

// backoff is how long after its nth failure a notification is tried again.
var backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}

const lease = 90 * time.Second

// Worker sends what is queued, in the worker process. Several may run: every claim is FOR UPDATE
// SKIP LOCKED with a lease, and a row whose worker died is taken again once its lease passes.
//
// Notifications are for people, so they are few, and a pass sends a small batch a few at a time;
// a destination that hangs holds one of those, for the sender's timeout, and nothing else.
type Worker struct {
	DB        *pgdb.DB
	Notify    *notify.Sender
	Mail      mail.Sender
	Dashboard string
	Batch     int
	Parallel  int
	Idle      time.Duration
	Logger    *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

// Name identifies the component in start-up and shutdown logs.
func (w *Worker) Name() string { return "notification-worker" }

// Start begins sending in the background.
func (w *Worker) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.done = cancel, make(chan struct{})
	go func() {
		defer close(w.done)
		for ctx.Err() == nil {
			n, err := w.Pass(ctx)
			if err != nil && ctx.Err() == nil {
				w.log().Error("a notification pass failed; tried again shortly", "error", err)
			}
			if n == 0 || err != nil {
				select {
				case <-ctx.Done():
				case <-time.After(w.idle()):
				}
			}
		}
	}()
	return nil
}

// Stop ends sending, waiting for the pass in flight.
func (w *Worker) Stop(ctx context.Context) error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
	case <-ctx.Done():
	}
	return nil
}

// Pass sends one batch of what is due, and answers how many it took.
func (w *Worker) Pass(ctx context.Context) (int, error) {
	ids, err := w.claim(ctx)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	rows, err := w.DB.Client.NotificationOutbox.Query().Where(notificationoutbox.IDIn(ids...)).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("read claimed notifications: %w", err)
	}
	parallel := w.Parallel
	if parallel <= 0 {
		parallel = 8
	}
	slots := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, row := range rows {
		slots <- struct{}{}
		wg.Add(1)
		go func(row *ent.NotificationOutbox) {
			defer func() { <-slots; wg.Done() }()
			w.settle(ctx, row, w.send(ctx, row))
		}(row)
	}
	wg.Wait()
	return len(rows), nil
}

func (w *Worker) claim(ctx context.Context) ([]int, error) {
	batch := w.Batch
	if batch <= 0 {
		batch = 64
	}
	rows, err := w.DB.DB().QueryContext(ctx, `
		UPDATE notification_outboxes SET claimed_until = now() + $2::interval
		WHERE id IN (
			SELECT id FROM notification_outboxes
			WHERE next_at <= now() AND (claimed_until IS NULL OR claimed_until < now())
			ORDER BY next_at LIMIT $1
			FOR UPDATE SKIP LOCKED)
		RETURNING id`, batch, fmt.Sprintf("%d seconds", int(lease.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("claim notifications: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// outcome is what one send came to: sent, worth another try, or not worth one.
type outcome struct {
	sent  bool
	final bool
	why   string
}

func (w *Worker) send(ctx context.Context, row *ent.NotificationOutbox) outcome {
	kind, ok := Lookup(row.Type)
	if !ok {
		return outcome{final: true, why: "no such event type any more"}
	}
	client := w.DB.Client
	where, err := w.where(ctx, row)
	if err != nil {
		return outcome{why: err.Error()}
	}
	e := Event{OrganizationID: row.OrganizationID, Environment: row.Environment, Type: row.Type,
		Data: row.Data, At: row.OccurredAt, UID: row.EventUID}
	if row.ProjectID != nil {
		e.ProjectID = *row.ProjectID
	}
	msg := Render(kind, e, where, w.Dashboard)

	if row.ChannelID == nil {
		return w.emailOwners(ctx, row.OrganizationID, where.Organization, msg)
	}
	channel, err := client.NotificationChannel.Get(ctx, *row.ChannelID)
	if ent.IsNotFound(err) {
		return outcome{final: true, why: "the channel was deleted"}
	}
	if err != nil {
		return outcome{why: err.Error()}
	}
	if !channel.Enabled {
		return outcome{final: true, why: "the channel was turned off"}
	}
	source := "Notification rule"
	if row.RuleID != nil {
		if rule, err := client.NotificationRule.Get(ctx, *row.RuleID); err == nil {
			source = "Rule: " + rule.Name
		} else if ent.IsNotFound(err) {
			return outcome{final: true, why: "the rule was deleted"}
		}
	}
	msg.Source = source
	r := w.Notify.Deliver(ctx, client, channel, msg, source)
	return outcome{sent: r.OK, why: r.Detail}
}

// emailOwners mails the organization's owners, admins and billing members. Sent when any one of
// them was reached: retrying would mail again those who already have it.
func (w *Worker) emailOwners(ctx context.Context, orgID int, orgName string, msg notify.Message) outcome {
	if w.Mail == nil {
		return outcome{final: true, why: "no mail sender"}
	}
	members, err := w.DB.Client.OrganizationMember.Query().Where(
		organizationmember.OrganizationID(orgID),
		organizationmember.HasRoleWith(role.KeyIn("owner", "admin", "billing")),
	).WithUser().All(ctx)
	if err != nil {
		return outcome{why: err.Error()}
	}
	sent, failed := 0, ""
	for _, m := range members {
		if m.Edges.User == nil || m.Edges.User.Email == "" {
			continue
		}
		if err := w.Mail.Send(ctx, mail.Message{Kind: mail.Alert, To: m.Edges.User.Email,
			Subject: msg.Title, Body: ownersBody(msg, orgName)}); err != nil {
			failed = err.Error()
			continue
		}
		sent++
	}
	switch {
	case sent > 0 || failed == "":
		return outcome{sent: true}
	default:
		return outcome{why: failed}
	}
}

func (w *Worker) where(ctx context.Context, row *ent.NotificationOutbox) (Where, error) {
	out := Where{Environment: row.Environment}
	org, err := w.DB.Client.Organization.Get(ctx, row.OrganizationID)
	if err != nil && !ent.IsNotFound(err) {
		return out, err
	}
	if org != nil {
		out.Organization = org.Name
	}
	if row.ProjectID != nil {
		p, err := w.DB.Client.Project.Get(ctx, *row.ProjectID)
		if err != nil && !ent.IsNotFound(err) {
			return out, err
		}
		if p != nil {
			out.Project = p.Name
		}
	}
	return out, nil
}

// settle deletes what was sent or given up on, and puts back what is worth another try.
func (w *Worker) settle(ctx context.Context, row *ent.NotificationOutbox, o outcome) {
	attempts := row.Attempts + 1
	if o.sent || o.final || attempts >= MaxAttempts {
		if !o.sent {
			w.log().Warn("gave up on a notification", "type", row.Type, "channel_id", row.ChannelID,
				"attempts", attempts, "reason", o.why)
		}
		if err := w.DB.Client.NotificationOutbox.DeleteOneID(row.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
			w.log().Warn("could not delete a settled notification", "id", row.ID, "error", err)
		}
		return
	}
	wait := backoff[min(attempts-1, len(backoff)-1)]
	if err := w.DB.Client.NotificationOutbox.UpdateOneID(row.ID).SetAttempts(attempts).
		SetNextAt(time.Now().UTC().Add(wait)).ClearClaimedUntil().
		SetLastError(clip(o.why, 500)).Exec(ctx); err != nil {
		w.log().Warn("could not reschedule a notification", "id", row.ID, "error", err)
	}
}

func (w *Worker) idle() time.Duration {
	if w.Idle > 0 {
		return w.Idle
	}
	return 2 * time.Second
}

func (w *Worker) log() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
