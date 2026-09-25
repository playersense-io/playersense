package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/project"
)

// Event is something that happened, to be sent to whoever hears it.
type Event struct {
	ProjectID  int
	ProjectUID uuid.UUID
	// Environment is production, staging or development; empty for a project-level event.
	Environment string
	Type        string
	Data        any
	// At is when it happened; now when zero.
	At time.Time
}

// Envelope is an event as it is POSTed.
type Envelope struct {
	ID         uuid.UUID       `json:"id"`
	Type       string          `json:"type"`
	APIVersion string          `json:"api_version"`
	CreatedAt  time.Time       `json:"created_at"`
	Project    EnvelopeProject `json:"project"`
	Data       any             `json:"data"`
}

// EnvelopeProject is whose event it is.
type EnvelopeProject struct {
	ID          uuid.UUID `json:"id"`
	Environment string    `json:"environment,omitempty"`
}

// Body is an event's envelope as bytes, with its id.
func Body(e Event, id uuid.UUID) ([]byte, error) {
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	return json.Marshal(Envelope{ID: id, Type: e.Type, APIVersion: APIVersion, CreatedAt: at.UTC().Truncate(time.Millisecond),
		Project: EnvelopeProject{ID: e.ProjectUID, Environment: e.Environment}, Data: e.Data})
}

// Emit writes an event to the outbox. Pass the transaction's client when the event is part of a
// change: the event exists exactly when the change does. It costs one small insert; an event no
// endpoint hears is deleted by the worker as soon as it is taken.
func Emit(ctx context.Context, client *ent.Client, e Event) error {
	if !Known(e.Type) {
		return fmt.Errorf("webhooks: no event type %q", e.Type)
	}
	id := uuid.New()
	body, err := Body(e, id)
	if err != nil {
		return fmt.Errorf("webhooks: %s: %w", e.Type, err)
	}
	return client.WebhookOutbox.Create().SetUID(id).SetProjectID(e.ProjectID).SetEnvironment(e.Environment).
		SetType(e.Type).SetBody(body).Exec(ctx)
}

// WithEvent runs a change and writes its event in one transaction, for a change made outside a
// request (the worker's), whose project is known by its public id.
func WithEvent(ctx context.Context, client *ent.Client, projectUID uuid.UUID, kind string, data any, change func(*ent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := change(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	p, err := tx.Project.Query().Where(project.UID(projectUID)).Only(ctx)
	if err == nil {
		err = Emit(ctx, tx.Client(), Event{ProjectID: p.ID, ProjectUID: p.UID, Type: kind, Data: data})
	} else if ent.IsNotFound(err) {
		err = nil // the project is gone; nobody is listening
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
