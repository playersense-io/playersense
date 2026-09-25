package handlers

import (
	"net/url"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/webhookendpoint"
	"playersense.dev/backend/ent/webhookretry"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/webhooks"
)

// Webhooks as staff see them: the platform's last day, the backlog, and the endpoints failing
// worst, with any one delivery readable on a ticket.

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}

// OperatorWebhooks is the last day of webhooks across the platform.
func OperatorWebhooks(ctx *muzak.Context, _ schemas.OperatorWebhooksParams) (schemas.OperatorWebhooksOut, error) {
	db := muzak.From[*core.DB](ctx)
	out := schemas.OperatorWebhooksOut{Failing: []schemas.OperatorWebhookOut{}, Disabled: []schemas.OperatorWebhookOut{}}

	var oldest *time.Time
	if err := db.DB().QueryRowContext(ctx.Context(), `SELECT count(*), min(created_at) FROM webhook_outboxes`).
		Scan(&out.Outbox, &oldest); err != nil {
		return out, core.AsHTTPError(err)
	}
	if oldest != nil {
		out.OutboxOldestS = time.Since(*oldest).Seconds()
	}
	var err error
	if out.Retrying, err = db.Client.WebhookRetry.Query().Count(ctx.Context()); err != nil {
		return out, core.AsHTTPError(err)
	}
	if out.Due, err = db.Client.WebhookRetry.Query().Where(webhookretry.NextAtLTE(time.Now())).Count(ctx.Context()); err != nil {
		return out, core.AsHTTPError(err)
	}

	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return out, logUnavailable(err)
	}
	total, worst, err := webhooks.PlatformHealth(ctx.Context(), conn, time.Now(), 50)
	if err != nil {
		return out, logUnavailable(err)
	}
	out.LastDay = dayOut(total)

	byUID := map[uuid.UUID]webhooks.EndpointDay{}
	var ids []int
	var uids []uuid.UUID
	for _, w := range worst {
		byUID[w.Endpoint] = w
		ids = append(ids, w.ProjectID)
		uids = append(uids, w.Endpoint)
	}
	endpoints := []*ent.WebhookEndpoint{}
	if len(uids) > 0 {
		endpoints, err = db.Client.WebhookEndpoint.Query().Where(webhookendpoint.UIDIn(uids...)).All(ctx.Context())
		if err != nil {
			return out, core.AsHTTPError(err)
		}
	}
	disabled, err := db.Client.WebhookEndpoint.Query().Where(webhookendpoint.DisabledReason("failing"),
		webhookendpoint.DisabledAtGT(time.Now().Add(-7*24*time.Hour))).Order(ent.Desc(webhookendpoint.FieldDisabledAt)).
		Limit(50).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, e := range disabled {
		ids = append(ids, e.ProjectID)
	}
	briefs, err := projectBriefs(ctx, ids)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	row := func(e *ent.WebhookEndpoint, f webhooks.Figures) schemas.OperatorWebhookOut {
		return schemas.OperatorWebhookOut{ID: e.UID, Project: briefs[e.ProjectID], Name: e.Name, Host: hostOf(e.URL),
			Enabled: e.Enabled, FailingSince: e.FailingSince, DisabledAt: e.DisabledAt, LastDay: dayOut(f)}
	}
	for _, e := range endpoints {
		out.Failing = append(out.Failing, row(e, byUID[e.UID].Figures))
	}
	for _, e := range disabled {
		out.Disabled = append(out.Disabled, row(e, webhooks.Figures{}))
	}
	return out, nil
}

// OperatorWebhookDeliveries is one project's endpoint's deliveries, read on a ticket.
func OperatorWebhookDeliveries(ctx *muzak.Context, in schemas.OperatorWebhookDeliveriesParams) (schemas.WebhookDeliveriesOut, error) {
	proj := muzak.From[core.StaffAccessRecord](ctx).Project
	return deliveriesOf(ctx, proj.ID, in.WebhookID, schemas.WebhookDeliveriesParams{Status: in.Status}.Statuses(), "", in.Before, in.Limit)
}

// OperatorWebhookDelivery is one delivery, read on a ticket.
func OperatorWebhookDelivery(ctx *muzak.Context, in schemas.OperatorWebhookDeliveryParams) (schemas.WebhookDeliveryDetailOut, error) {
	proj := muzak.From[core.StaffAccessRecord](ctx).Project
	return deliveryDetailOf(ctx, proj.ID, in.WebhookID, in.DeliveryID)
}
