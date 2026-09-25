package webhooks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/pgdb"
)

// Sender is what the dashboard does to an endpoint at once, in the API: a test event, a delivery
// sent again. Each is one try, made while the person waits, and written to the log straight away.
type Sender struct {
	DB      *pgdb.DB
	CH      interface{ DB() (driver.Conn, error) }
	Deliver *Deliverer
	// AllowPrivate admits http and private addresses as endpoints, for a local stack and tests.
	AllowPrivate bool
}

// ErrGone is a delivery whose event has aged out of the log, or never reached it.
var ErrGone = errors.New("the delivery is no longer in the log")

func (s *Sender) conn() (driver.Conn, error) { return s.CH.DB() }

func (s *Sender) log() *AttemptLog { return &AttemptLog{CH: s.CH} }

// Sent is a try made from the dashboard.
type Sent struct {
	DeliveryID uuid.UUID
	EventID    uuid.UUID
	Try        Try
}

// Test sends a made-up event of a type to an endpoint.
func (s *Sender) Test(ctx context.Context, e *ent.WebhookEndpoint, projectUID uuid.UUID, kind, env string) (Sent, error) {
	eventID, deliveryID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	body, err := Body(Event{ProjectID: e.ProjectID, ProjectUID: projectUID, Environment: env, Type: kind,
		Data: Sample(kind, env), At: now}, eventID)
	if err != nil {
		return Sent{}, err
	}
	res := s.Deliver.Send(ctx, TargetOf(e, now), eventID, body)
	outcome := Failed
	if res.OK() {
		outcome = Succeeded
	}
	a := Attempt{ProjectID: e.ProjectID, EndpointUID: e.UID, DeliveryID: deliveryID, EventID: eventID, Type: kind,
		Environment: env, DeliveryCreated: now, Number: 1, Result: res, Outcome: outcome, Test: true}
	if err := s.log().Write(ctx, []Attempt{a}, []LoggedEvent{{ProjectID: e.ProjectID, EventID: eventID, Type: kind,
		Environment: env, CreatedAt: now, Body: body}}); err != nil {
		return Sent{}, err
	}
	return Sent{DeliveryID: deliveryID, EventID: eventID, Try: tryOf(a)}, nil
}

func tryOf(a Attempt) Try {
	return Try{Number: a.Number, At: a.Result.At, Code: a.Result.Code, Error: a.Result.Error, Ms: a.Result.Ms,
		Response: a.Result.Response, Outcome: a.Outcome, NextAt: a.NextAt, Manual: a.Manual, Test: a.Test}
}

// Resend sends a delivery's event again, now, as one try outside its schedule. A delivery still
// retrying keeps its schedule if this try fails, and is done with if it lands.
func (s *Sender) Resend(ctx context.Context, e *ent.WebhookEndpoint, delivery uuid.UUID) (Sent, error) {
	conn, err := s.conn()
	if err != nil {
		return Sent{}, err
	}
	d, err := ReadDelivery(ctx, conn, e.ProjectID, e.UID, delivery)
	if err != nil {
		return Sent{}, err
	}
	if d == nil || d.Payload == "" {
		return Sent{}, ErrGone
	}
	now := time.Now().UTC()
	res := s.Deliver.Send(ctx, TargetOf(e, now), d.EventID, []byte(d.Payload))
	a := Attempt{ProjectID: e.ProjectID, EndpointUID: e.UID, DeliveryID: delivery, EventID: d.EventID, Type: d.Type,
		Environment: d.Environment, DeliveryCreated: d.Created, Number: d.Attempts + 1, Result: res, Manual: true, Test: d.Test}
	pending, err := s.DB.Client.WebhookRetry.Query().Where(webhookretry.UID(delivery)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return Sent{}, err
	}
	switch {
	case res.OK():
		a.Outcome = Succeeded
		if pending != nil {
			_ = s.DB.Client.WebhookRetry.DeleteOne(pending).Exec(ctx)
		}
	case pending != nil:
		a.Outcome, a.NextAt = Retrying, &pending.NextAt
		_ = pending.Update().SetAttempts(a.Number).Exec(ctx)
	default:
		a.Outcome = Failed
	}
	if err := s.log().Write(ctx, []Attempt{a}, nil); err != nil {
		return Sent{}, err
	}
	return Sent{DeliveryID: delivery, EventID: d.EventID, Try: tryOf(a)}, nil
}

// ResendFailed queues every delivery to an endpoint that gave up since a moment, for the worker to
// send again: what an endpoint missed while it was down. Each is one more try. It answers how many.
func (s *Sender) ResendFailed(ctx context.Context, e *ent.WebhookEndpoint, since time.Time, limit int) (int, error) {
	conn, err := s.conn()
	if err != nil {
		return 0, err
	}
	var failed []Delivery
	var cursor *DeliveryCursor
	for len(failed) < limit {
		page, next, err := Deliveries(ctx, conn, e.ProjectID, e.UID, DeliveryFilter{Outcomes: []string{Failed},
			Before: cursor, Limit: 500})
		if err != nil {
			return 0, err
		}
		stop := false
		for _, d := range page {
			if d.Created.Before(since) {
				stop = true
				break
			}
			if !d.Test {
				failed = append(failed, d)
			}
		}
		if stop || next == nil {
			break
		}
		cursor = next
	}
	if len(failed) > limit {
		failed = failed[:limit]
	}
	if len(failed) == 0 {
		return 0, nil
	}
	payloads, err := payloadsOf(ctx, conn, e.ProjectID, failed)
	if err != nil {
		return 0, err
	}
	// Due now, by the database's clock as well as this one: a few seconds back, so a database a
	// moment behind does not hold them over to the next pass.
	now := time.Now().UTC().Add(-5 * time.Second)
	queued := 0
	for i := 0; i < len(failed); i += 500 {
		chunk := failed[i:min(i+500, len(failed))]
		creates := make([]*ent.WebhookRetryCreate, 0, len(chunk))
		for _, d := range chunk {
			body, ok := payloads[d.EventID]
			if !ok {
				continue
			}
			creates = append(creates, s.DB.Client.WebhookRetry.Create().SetUID(d.ID).SetEndpointID(e.ID).
				SetProjectID(e.ProjectID).SetEventUID(d.EventID).SetType(d.Type).SetEnvironment(d.Environment).
				SetBody([]byte(body)).SetFirstAt(d.Created).SetAttempts(d.Attempts).SetNextAt(now).SetManual(true))
		}
		if len(creates) == 0 {
			continue
		}
		// A delivery already queued is left as it is.
		if err := s.DB.Client.WebhookRetry.CreateBulk(creates...).OnConflictColumns("uid").DoNothing().Exec(ctx); err != nil {
			return queued, err
		}
		queued += len(creates)
	}
	return queued, nil
}

// payloadsOf reads the bodies of the events deliveries carried, in one statement.
func payloadsOf(ctx context.Context, conn driver.Conn, projectID int, ds []Delivery) (map[uuid.UUID]string, error) {
	seen := map[uuid.UUID]bool{}
	var ids []string
	for _, d := range ds {
		if !seen[d.EventID] {
			seen[d.EventID] = true
			ids = append(ids, "toUUID('"+d.EventID.String()+"')")
		}
	}
	sort.Strings(ids)
	rows, err := conn.Query(ctx, `SELECT event_id, payload FROM webhook_events WHERE project_seq = ? AND event_id IN (`+
		strings.Join(ids, ", ")+`)`, uint32(projectID))
	if err != nil {
		return nil, fmt.Errorf("read webhook_events: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		out[id] = payload
	}
	return out, rows.Err()
}

// CancelOwed drops what an endpoint was owed, turned off or deleted from the dashboard, and closes
// each in the log. Best effort for the log: the deliveries are dropped either way.
func (s *Sender) CancelOwed(ctx context.Context, e *ent.WebhookEndpoint, why string) error {
	owed, err := s.DB.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(e.ID)).All(ctx)
	if err != nil || len(owed) == 0 {
		return err
	}
	if _, err := s.DB.Client.WebhookRetry.Delete().Where(webhookretry.EndpointID(e.ID)).Exec(ctx); err != nil {
		return err
	}
	_ = s.log().Write(ctx, Canceled(owed, e.UID, why, time.Now()), nil)
	return nil
}
