package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/memberproject"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/schema"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/signing"
	"playersense.dev/backend/internal/webhooks"
)

// A project's webhook endpoints (§49-51): made and changed here, delivered to by the worker, and
// read back from ClickHouse's log.

func dayOut(f webhooks.Figures) schemas.WebhookDayOut {
	return schemas.WebhookDayOut{Deliveries: f.Deliveries, FirstTry: f.FirstTry(), TrySuccess: f.TrySuccess(),
		Succeeded: f.Succeeded, Failed: f.Failed, P50Ms: f.P50, P95Ms: f.P95}
}

func endpointOut(e *ent.WebhookEndpoint, retrying int, day webhooks.Figures) schemas.EndpointOut {
	out := schemas.EndpointOut{
		ID: e.UID, Name: e.Name, Description: e.Description, URL: e.URL, Events: e.Events, Environments: e.Environments,
		Status: "enabled", Health: "healthy", DisabledReason: e.DisabledReason, DisabledAt: e.DisabledAt,
		FailingSince: e.FailingSince, APIVersion: e.APIVersion, Headers: []schemas.WebhookHeaderOut{},
		Secret:   schemas.WebhookSecretOut{CreatedAt: e.SecretCreatedAt.UTC(), PreviousExpiresAt: e.PreviousExpiresAt},
		Retrying: retrying, LastDay: dayOut(day),
		CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(), CreatedBy: e.CreatedBy, UpdatedBy: e.UpdatedBy,
	}
	if out.Secret.PreviousExpiresAt != nil && time.Now().After(*out.Secret.PreviousExpiresAt) {
		out.Secret.PreviousExpiresAt = nil
	}
	for _, h := range e.Headers {
		ho := schemas.WebhookHeaderOut{Name: h.Name, Secret: h.Secret}
		if !h.Secret {
			ho.Value = h.Value
		}
		out.Headers = append(out.Headers, ho)
	}
	switch {
	case !e.Enabled:
		out.Status, out.Health = "disabled", "disabled"
	case e.FailingSince != nil:
		out.Health = "failing"
		at := e.FailingSince.Add(webhooks.DisableAfter).UTC()
		out.AutoDisableAt = &at
	}
	return out
}

func findEndpoint(ctx *muzak.Context, id uuid.UUID) (*ent.WebhookEndpoint, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	e, err := db.Client.WebhookEndpoint.Query().Where(webhookendpoint.UID(id), webhookendpoint.ProjectID(proj.ID)).Only(ctx.Context())
	if ent.IsNotFound(err) {
		return nil, muzak.NotFound("this project has no such webhook endpoint")
	}
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return e, nil
}

func invalidWebhook(message string) error {
	return muzak.UnprocessableEntity(message).WithCode("invalid_webhook")
}

// endpointWritten checks what was sent and reads it as an endpoint's settings, over what it had:
// a secret header sent without a value keeps the one saved.
func endpointWritten(ctx *muzak.Context, in schemas.WebhookBody, had *ent.WebhookEndpoint) ([]schema.WebhookHeader, map[string]string, error) {
	sender := muzak.From[*webhooks.Sender](ctx)
	if err := webhooks.CheckURL(in.URL, sender.AllowPrivate); err != nil {
		return nil, nil, invalidWebhook(err.Error())
	}
	for _, e := range in.Events {
		if e != webhooks.EveryEvent && !webhooks.Known(e) {
			return nil, nil, invalidWebhook("there is no event type " + e)
		}
	}
	for _, env := range in.Environments {
		if env != "production" && env != "staging" && env != "development" {
			return nil, nil, invalidWebhook("environments are production, staging and development, not " + env)
		}
	}
	headers := []schema.WebhookHeader{}
	secrets := map[string]string{}
	seen := map[string]bool{}
	for _, h := range in.Headers {
		name := strings.TrimSpace(h.Name)
		lower := strings.ToLower(name)
		if name == "" || strings.ContainsAny(name, " :\r\n\t") || len(name) > 64 {
			return nil, nil, invalidWebhook("a header name is a single token, like X-Api-Key")
		}
		for _, r := range webhooks.Reserved {
			if lower == r {
				return nil, nil, invalidWebhook(name + " is set by PlayerSense on every delivery")
			}
		}
		if seen[lower] {
			return nil, nil, invalidWebhook(name + " is given twice")
		}
		seen[lower] = true
		if strings.ContainsAny(h.Value, "\r\n") || len(h.Value) > 1024 {
			return nil, nil, invalidWebhook("the value of " + name + " is not a header value")
		}
		if h.Secret {
			value := h.Value
			if value == "" && had != nil {
				value = had.HeaderSecrets[name]
			}
			if value == "" {
				return nil, nil, invalidWebhook("the secret header " + name + " needs a value")
			}
			secrets[name] = value
			headers = append(headers, schema.WebhookHeader{Name: name, Secret: true})
			continue
		}
		headers = append(headers, schema.WebhookHeader{Name: name, Value: h.Value})
	}
	return headers, secrets, nil
}

// retryingCounts is how many deliveries each of a project's endpoints owes, in one grouped read.
func retryingCounts(ctx *muzak.Context, projectID int) (map[int]int, error) {
	db := muzak.From[*core.DB](ctx)
	var rows []struct {
		EndpointID int `json:"endpoint_id"`
		Count      int `json:"count"`
	}
	if err := db.Client.WebhookRetry.Query().Where(webhookretry.ProjectID(projectID)).
		GroupBy(webhookretry.FieldEndpointID).Aggregate(ent.Count()).Scan(ctx.Context(), &rows); err != nil {
		return nil, err
	}
	out := map[int]int{}
	for _, r := range rows {
		out[r.EndpointID] = r.Count
	}
	return out, nil
}

// ListWebhooks is the project's endpoints, each with its last day.
func ListWebhooks(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.WebhookListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.WebhookEndpoint.Query().Where(webhookendpoint.ProjectID(proj.ID)).
		Order(ent.Asc(webhookendpoint.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.WebhookListOut{}, core.AsHTTPError(err)
	}
	retrying, err := retryingCounts(ctx, proj.ID)
	if err != nil {
		return schemas.WebhookListOut{}, core.AsHTTPError(err)
	}
	health := map[uuid.UUID]webhooks.Figures{}
	if len(rows) > 0 {
		if conn, err := muzak.From[*chdb.Conn](ctx).DB(); err == nil {
			// The figures are the log's; the list is still worth showing when ClickHouse is not.
			health, _ = webhooks.ProjectHealth(ctx.Context(), conn, proj.ID, time.Now())
		}
	}
	out := schemas.WebhookListOut{Webhooks: make([]schemas.EndpointOut, 0, len(rows))}
	for _, e := range rows {
		out.Webhooks = append(out.Webhooks, endpointOut(e, retrying[e.ID], health[e.UID]))
	}
	return out, nil
}

// ReadWebhook is one endpoint, with who changed it.
func ReadWebhook(ctx *muzak.Context, in schemas.WebhookParams) (schemas.EndpointOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	retrying, err := db.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(e.ID)).Count(ctx.Context())
	if err != nil {
		return schemas.EndpointOut{}, core.AsHTTPError(err)
	}
	var day webhooks.Figures
	if conn, err := muzak.From[*chdb.Conn](ctx).DB(); err == nil {
		day, _, _, _ = webhooks.Health(ctx.Context(), conn, e.ProjectID, e.UID, time.Now())
	}
	out := endpointOut(e, retrying, day)
	log, err := auditOf(ctx.Context(), db, principal.Organization.ID, schemas.AuditFilter{TargetType: "webhook",
		TargetID: e.UID.String(), Limit: 50})
	if err == nil {
		for _, entry := range log.Events {
			out.History = append(out.History, schemas.WebhookEntry{At: entry.At, Actor: entry.Actor, Action: entry.Action})
		}
	}
	return out, nil
}

// CreateWebhook makes an endpoint, and returns its signing secret this once.
func CreateWebhook(ctx *muzak.Context, in schemas.WebhookIn) (schemas.EndpointOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	headers, secrets, err := endpointWritten(ctx, in.WebhookBody, nil)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	secret, err := signing.NewSecret()
	if err != nil {
		return schemas.EndpointOut{}, core.AsHTTPError(err)
	}
	who := actorOf(ctx).Label
	e, err := inTx(ctx, func(tx *ent.Tx) (*ent.WebhookEndpoint, error) {
		// The plan's limit is a project's, as the webhooks page counts it.
		if err := withinPlan(ctx, tx, proj.OrganizationID, billing.MaxWebhookEndpoints, func() (int, error) {
			return tx.WebhookEndpoint.Query().Where(webhookendpoint.ProjectID(proj.ID)).Count(ctx.Context())
		}, "this project has as many webhook destinations as its plan allows"); err != nil {
			return nil, err
		}
		create := tx.WebhookEndpoint.Create().SetProjectID(proj.ID).SetName(in.Name).SetDescription(in.Description).
			SetURL(strings.TrimSpace(in.URL)).SetEvents(in.Events).SetEnvironments(in.Environments).
			SetAPIVersion(webhooks.APIVersion).SetHeaders(headers).SetHeaderSecrets(secrets).
			SetSecret(secret).SetSecretCreatedAt(time.Now().UTC()).SetCreatedBy(who).SetUpdatedBy(who)
		if in.Enabled != nil && !*in.Enabled {
			create = create.SetEnabled(false).SetDisabledReason("manual").SetDisabledAt(time.Now().UTC())
		}
		e, err := create.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return e, auditFor(ctx).record(ctx.Context(), tx, audit.WebhookWritten, "webhook", e.UID.String(),
			map[string]any{"project": proj.Slug, "name": e.Name, "url": e.URL, "events": e.Events, "created": true})
	})
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	ctx.SetStatus(http.StatusCreated)
	out := endpointOut(e, 0, webhooks.Figures{})
	out.SigningSecret = secret
	return out, nil
}

// UpdateWebhook changes an endpoint. Turning one back on clears its failing streak, so it has
// three days again.
func UpdateWebhook(ctx *muzak.Context, in schemas.UpdateWebhookIn) (schemas.EndpointOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	headers, secrets, err := endpointWritten(ctx, in.WebhookBody, e)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.WebhookEndpoint, error) {
		update := tx.WebhookEndpoint.UpdateOne(e).SetName(in.Name).SetDescription(in.Description).
			SetURL(strings.TrimSpace(in.URL)).SetEvents(in.Events).SetEnvironments(in.Environments).
			SetHeaders(headers).SetHeaderSecrets(secrets).SetUpdatedBy(actorOf(ctx).Label)
		if in.Enabled != nil && *in.Enabled != e.Enabled {
			if *in.Enabled {
				update = update.SetEnabled(true).SetDisabledReason("").ClearDisabledAt().ClearFailingSince()
			} else {
				update = update.SetEnabled(false).SetDisabledReason("manual").SetDisabledAt(time.Now().UTC())
			}
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.WebhookWritten, "webhook", e.UID.String(),
			map[string]any{"project": proj.Slug, "name": updated.Name, "url": updated.URL, "events": updated.Events,
				"enabled": updated.Enabled, "created": false})
	})
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	if !updated.Enabled {
		// Off cancels what it was owed.
		if err := muzak.From[*webhooks.Sender](ctx).CancelOwed(ctx.Context(), updated, "Canceled: the endpoint was turned off"); err != nil {
			return schemas.EndpointOut{}, core.AsHTTPError(err)
		}
	}
	return endpointOut(updated, 0, webhooks.Figures{}), nil
}

// DeleteWebhook removes an endpoint and what it was owed. Its log ages out with the rest.
func DeleteWebhook(ctx *muzak.Context, in schemas.WebhookParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return muzak.Empty{}, err
	}
	if err := muzak.From[*webhooks.Sender](ctx).CancelOwed(ctx.Context(), e, "Canceled: the endpoint was deleted"); err != nil {
		return muzak.Empty{}, core.AsHTTPError(err)
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		if _, err := tx.WebhookRetry.Delete().Where(webhookretry.EndpointID(e.ID)).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		if err := tx.WebhookEndpoint.DeleteOne(e).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.WebhookDeleted, "webhook", e.UID.String(),
			map[string]any{"project": proj.Slug, "name": e.Name, "url": e.URL})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(http.StatusNoContent)
	return muzak.Empty{}, nil
}

// RollWebhookSecret gives an endpoint a new signing secret, returned this once. The old one keeps
// signing beside it for the hours asked, a day by default, so the receiver can move over.
func RollWebhookSecret(ctx *muzak.Context, in schemas.RollWebhookSecretIn) (schemas.EndpointOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	secret, err := signing.NewSecret()
	if err != nil {
		return schemas.EndpointOut{}, core.AsHTTPError(err)
	}
	hours := 24
	if in.ExpireAfterHours != nil {
		hours = *in.ExpireAfterHours
	}
	now := time.Now().UTC()
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.WebhookEndpoint, error) {
		update := tx.WebhookEndpoint.UpdateOne(e).SetSecret(secret).SetSecretCreatedAt(now).SetUpdatedBy(actorOf(ctx).Label)
		if hours > 0 {
			update = update.SetPreviousSecret(e.Secret).SetPreviousExpiresAt(now.Add(time.Duration(hours) * time.Hour))
		} else {
			update = update.SetPreviousSecret("").ClearPreviousExpiresAt()
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.WebhookSecretRolled, "webhook", e.UID.String(),
			map[string]any{"project": proj.Slug, "name": e.Name, "previous_valid_hours": hours})
	})
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	out := endpointOut(updated, 0, webhooks.Figures{})
	out.SigningSecret = secret
	return out, nil
}

// RevealWebhookSecret shows an endpoint's signing secret, and records who asked.
func RevealWebhookSecret(ctx *muzak.Context, in schemas.WebhookParams) (schemas.EndpointOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.EndpointOut{}, err
	}
	if _, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.WebhookSecretShown, "webhook", e.UID.String(),
			map[string]any{"project": proj.Slug, "name": e.Name})
	}); err != nil {
		return schemas.EndpointOut{}, err
	}
	out := endpointOut(e, 0, webhooks.Figures{})
	out.SigningSecret = e.Secret
	return out, nil
}

// WebhookCatalog is every event type an endpoint can hear.
func WebhookCatalog(*muzak.Context, muzak.Empty) (schemas.WebhookCatalogOut, error) {
	out := schemas.WebhookCatalogOut{APIVersion: webhooks.APIVersion}
	for _, e := range webhooks.Catalog {
		out.Events = append(out.Events, schemas.WebhookEventTypeOut{Type: e.Type, Group: e.Group, Summary: e.Summary,
			Environments: e.Environments, Emitted: e.Emitted})
	}
	return out, nil
}

// ---- sends -----------------------------------------------------------------------------------

func tryOut(t webhooks.Try) schemas.WebhookTryOut {
	out := schemas.WebhookTryOut{Number: t.Number, At: t.At.UTC(), Error: t.Error, Ms: t.Ms, Response: t.Response,
		Outcome: t.Outcome, NextAt: t.NextAt, Manual: t.Manual}
	if t.Code > 0 {
		code := t.Code
		out.Code = &code
	}
	return out
}

func logUnavailable(err error) error {
	return muzak.ServiceUnavailable("the delivery log could not be read; retry shortly").Wrap(err)
}

// TestWebhook sends a made-up event of a type now, and answers how the endpoint took it. It is kept
// in the delivery log, marked a test.
func TestWebhook(ctx *muzak.Context, in schemas.TestWebhookIn) (schemas.WebhookSentOut, error) {
	sender := muzak.From[*webhooks.Sender](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	if !webhooks.Known(in.Type) {
		return schemas.WebhookSentOut{}, invalidWebhook("there is no event type " + in.Type)
	}
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.WebhookSentOut{}, err
	}
	env := in.Environment
	if env == "" && len(e.Environments) > 0 {
		env = e.Environments[0]
	}
	sent, err := sender.Test(ctx.Context(), e, proj.UID, in.Type, env)
	if err != nil {
		return schemas.WebhookSentOut{}, logUnavailable(err)
	}
	return schemas.WebhookSentOut{DeliveryID: sent.DeliveryID, EventID: sent.EventID, Try: tryOut(sent.Try)}, nil
}

// ResendWebhookDelivery sends a delivery's event again now, as one try outside its schedule.
func ResendWebhookDelivery(ctx *muzak.Context, in schemas.WebhookDeliveryParams) (schemas.WebhookSentOut, error) {
	sender := muzak.From[*webhooks.Sender](ctx)
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.WebhookSentOut{}, err
	}
	sent, err := sender.Resend(ctx.Context(), e, in.DeliveryID)
	if errors.Is(err, webhooks.ErrGone) {
		return schemas.WebhookSentOut{}, muzak.NotFound("no such delivery in the last thirty days")
	}
	if err != nil {
		return schemas.WebhookSentOut{}, logUnavailable(err)
	}
	return schemas.WebhookSentOut{DeliveryID: sent.DeliveryID, EventID: sent.EventID, Try: tryOut(sent.Try)}, nil
}

// ResendFailedWebhooks queues every delivery to an endpoint that gave up since a moment, for the
// worker to send again: what the endpoint missed while it was down.
func ResendFailedWebhooks(ctx *muzak.Context, in schemas.ResendFailedIn) (schemas.ResendFailedOut, error) {
	sender := muzak.From[*webhooks.Sender](ctx)
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.ResendFailedOut{}, err
	}
	if !e.Enabled {
		return schemas.ResendFailedOut{}, muzak.Conflict("the endpoint is off: turn it on first").WithCode("webhook_disabled")
	}
	since := time.Now().Add(-72 * time.Hour)
	if in.Since != nil {
		since = *in.Since
	}
	n, err := sender.ResendFailed(ctx.Context(), e, since, 10_000)
	if err != nil {
		return schemas.ResendFailedOut{}, logUnavailable(err)
	}
	return schemas.ResendFailedOut{Queued: n}, nil
}

// ---- the log ---------------------------------------------------------------------------------

func deliveryOut(d webhooks.Delivery) schemas.WebhookDeliveryOut {
	out := schemas.WebhookDeliveryOut{ID: d.ID, EventID: d.EventID, Type: d.Type, Environment: d.Environment,
		CreatedAt: d.Created.UTC(), Status: d.Outcome, Attempts: d.Attempts, LastAt: d.LastAt.UTC(), LastError: d.LastError,
		NextAt: d.NextAt, Test: d.Test}
	if d.LastCode > 0 {
		code := d.LastCode
		out.LastCode = &code
	}
	return out
}

func deliveriesOf(ctx *muzak.Context, projectID int, endpoint uuid.UUID, statuses []string, kind, before string, limit int) (schemas.WebhookDeliveriesOut, error) {
	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return schemas.WebhookDeliveriesOut{}, logUnavailable(err)
	}
	f := webhooks.DeliveryFilter{Outcomes: statuses, Type: kind, Limit: limit}
	if before != "" {
		cursor, err := webhooks.ParseCursor(before)
		if err != nil {
			return schemas.WebhookDeliveriesOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		f.Before = cursor
	}
	rows, next, err := webhooks.Deliveries(ctx.Context(), conn, projectID, endpoint, f)
	if err != nil {
		return schemas.WebhookDeliveriesOut{}, logUnavailable(err)
	}
	out := schemas.WebhookDeliveriesOut{Deliveries: make([]schemas.WebhookDeliveryOut, 0, len(rows))}
	for _, d := range rows {
		out.Deliveries = append(out.Deliveries, deliveryOut(d))
	}
	if next != nil {
		out.Next = next.String()
	}
	return out, nil
}

func deliveryDetailOf(ctx *muzak.Context, projectID int, endpoint, delivery uuid.UUID) (schemas.WebhookDeliveryDetailOut, error) {
	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return schemas.WebhookDeliveryDetailOut{}, logUnavailable(err)
	}
	d, err := webhooks.ReadDelivery(ctx.Context(), conn, projectID, endpoint, delivery)
	if err != nil {
		return schemas.WebhookDeliveryDetailOut{}, logUnavailable(err)
	}
	if d == nil {
		return schemas.WebhookDeliveryDetailOut{}, muzak.NotFound("no such delivery in the last thirty days")
	}
	out := schemas.WebhookDeliveryDetailOut{WebhookDeliveryOut: deliveryOut(d.Delivery), Tries: []schemas.WebhookTryOut{}}
	for _, t := range d.Tries {
		out.Tries = append(out.Tries, tryOut(t))
	}
	if d.Payload != "" && json.Valid([]byte(d.Payload)) {
		out.Payload = json.RawMessage(d.Payload)
	}
	return out, nil
}

// ListWebhookDeliveries is an endpoint's deliveries, newest first.
func ListWebhookDeliveries(ctx *muzak.Context, in schemas.WebhookDeliveriesParams) (schemas.WebhookDeliveriesOut, error) {
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.WebhookDeliveriesOut{}, err
	}
	return deliveriesOf(ctx, e.ProjectID, e.UID, in.Statuses(), in.Type, in.Before, in.Limit)
}

// ReadWebhookDelivery is one delivery, every try, and the event it carried.
func ReadWebhookDelivery(ctx *muzak.Context, in schemas.WebhookDeliveryParams) (schemas.WebhookDeliveryDetailOut, error) {
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.WebhookDeliveryDetailOut{}, err
	}
	return deliveryDetailOf(ctx, e.ProjectID, e.UID, in.DeliveryID)
}

// WebhookHealth is an endpoint's last day against the day before, and its tries by hour.
func WebhookHealth(ctx *muzak.Context, in schemas.WebhookParams) (schemas.WebhookHealthOut, error) {
	db := muzak.From[*core.DB](ctx)
	e, err := findEndpoint(ctx, in.WebhookID)
	if err != nil {
		return schemas.WebhookHealthOut{}, err
	}
	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return schemas.WebhookHealthOut{}, logUnavailable(err)
	}
	cur, prev, hours, err := webhooks.Health(ctx.Context(), conn, e.ProjectID, e.UID, time.Now())
	if err != nil {
		return schemas.WebhookHealthOut{}, logUnavailable(err)
	}
	retrying, err := db.Client.WebhookRetry.Query().Where(webhookretry.EndpointID(e.ID)).Count(ctx.Context())
	if err != nil {
		return schemas.WebhookHealthOut{}, core.AsHTTPError(err)
	}
	out := schemas.WebhookHealthOut{Current: dayOut(cur), Previous: dayOut(prev), Retrying: retrying,
		Hours: make([]schemas.WebhookHourOut, 0, len(hours))}
	for _, h := range hours {
		out.Hours = append(out.Hours, schemas.WebhookHourOut{At: h.At, OK: h.OK, Failed: h.Failed})
	}
	return out, nil
}

// emit writes a webhook event for the route's project, in the transaction of the change it
// describes: the event exists exactly when the change does.
func emit(ctx *muzak.Context, tx *ent.Tx, environment, kind string, data any) error {
	proj := muzak.From[*core.Principal](ctx).Project
	return webhooks.Emit(ctx.Context(), tx.Client(), webhooks.Event{ProjectID: proj.ID, ProjectUID: proj.UID,
		Environment: environment, Type: kind, Data: data})
}

// emitForMember writes a project-level event to every project a member reaches: all of the
// organization's when they see every project, else those they were given.
func emitForMember(ctx context.Context, tx *ent.Tx, orgID int, member *ent.OrganizationMember, kind string, data any) error {
	q := tx.Project.Query().Where(project.OrganizationID(orgID), project.DeletedAtIsNil())
	if !member.AllProjects {
		ids, err := tx.MemberProject.Query().Where(memberproject.MemberID(member.ID)).
			Select(memberproject.FieldProjectID).Ints(ctx)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		q = q.Where(project.IDIn(ids...))
	}
	projects, err := q.All(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if err := webhooks.Emit(ctx, tx.Client(), webhooks.Event{ProjectID: p.ID, ProjectUID: p.UID, Type: kind, Data: data}); err != nil {
			return err
		}
	}
	return nil
}
