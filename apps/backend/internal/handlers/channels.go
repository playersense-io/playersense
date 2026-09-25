package handlers

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/channeldelivery"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/signing"
)

// A project's notification channels, and the log of what was sent to them.

func channelOut(row *ent.NotificationChannel) schemas.ChannelOut {
	ch := notify.ChannelOf(row)
	out := schemas.ChannelOut{
		ID: row.UID, Type: string(row.Type), Name: row.Name, Enabled: row.Enabled,
		Config: schemas.ChannelConfigOut{Addresses: ch.Addresses, URL: notify.Mask(ch.URL), Severity: ch.Severity,
			HeaderName: ch.HeaderName, HasKey: ch.RoutingKey != ""},
		CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy,
	}
	switch {
	case !row.Enabled:
		out.Status = "off"
	case row.LastTestAt == nil:
		out.Status = "untested"
	case row.LastTestOk != nil && *row.LastTestOk:
		out.Status = "working"
	default:
		out.Status = "failing"
	}
	if row.LastTestAt != nil {
		out.LastTest = &schemas.ChannelTestOut{At: *row.LastTestAt, OK: row.LastTestOk != nil && *row.LastTestOk,
			Detail: row.LastTestDetail}
	}
	return out
}

// written is a channel's visible settings and its secrets, from what was sent, over what it had.
func written(kind string, in schemas.ChannelConfigIn, keep map[string]string) (map[string]any, map[string]string) {
	config := map[string]any{}
	if len(in.Addresses) > 0 {
		addresses := make([]string, 0, len(in.Addresses))
		for _, a := range in.Addresses {
			addresses = append(addresses, strings.ToLower(strings.TrimSpace(a)))
		}
		config["addresses"] = addresses
	}
	if in.Severity != "" {
		config["severity"] = in.Severity
	}
	if in.HeaderName != "" {
		config["header_name"] = in.HeaderName
	}
	secret := map[string]string{}
	for k, v := range keep {
		secret[k] = v
	}
	if in.URL != "" {
		secret["url"] = in.URL
	}
	if in.RoutingKey != "" {
		secret["routing_key"] = in.RoutingKey
	}
	if in.HeaderValue != "" {
		secret["header_value"] = in.HeaderValue
	}
	if in.HeaderName == "" {
		delete(secret, "header_value")
	}
	return config, secret
}

func checked(sender *notify.Sender, kind string, config map[string]any, secret map[string]string) error {
	row := &ent.NotificationChannel{Type: notificationchannel.Type(kind), Config: config, Secret: secret}
	if err := sender.Validate(notify.ChannelOf(row)); err != nil {
		return muzak.UnprocessableEntity(strings.TrimPrefix(err.Error(), notify.ErrInvalid.Error()+": ")).
			WithCode("invalid_channel")
	}
	return nil
}

// ListChannels is a project's channels.
func ListChannels(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ChannelListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.NotificationChannel.Query().Where(notificationchannel.ProjectID(proj.ID)).
		Order(ent.Asc(notificationchannel.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.ChannelListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ChannelListOut{Channels: make([]schemas.ChannelOut, 0, len(rows))}
	for _, row := range rows {
		out.Channels = append(out.Channels, channelOut(row))
	}
	return out, nil
}

func findChannel(ctx *muzak.Context, id uuid.UUID) (*ent.NotificationChannel, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := db.Client.NotificationChannel.Query().
		Where(notificationchannel.UID(id), notificationchannel.ProjectID(proj.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no such channel")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// ReadChannel is one channel.
func ReadChannel(ctx *muzak.Context, in schemas.ChannelParams) (schemas.ChannelOut, error) {
	row, err := findChannel(ctx, in.ChannelID)
	if err != nil {
		return schemas.ChannelOut{}, err
	}
	return channelOut(row), nil
}

// CreateChannel sets a channel up. A webhook channel is given a signing secret, returned this
// once.
func CreateChannel(ctx *muzak.Context, in schemas.ChannelIn) (schemas.ChannelOut, error) {
	sender := muzak.From[*notify.Sender](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	config, secret := written(in.Type, in.Config, nil)
	if in.Type == string(notify.Webhook) {
		signingSecret, err := signing.NewSecret()
		if err != nil {
			return schemas.ChannelOut{}, core.AsHTTPError(err)
		}
		secret["signing_secret"] = signingSecret
	}
	if err := checked(sender, in.Type, config, secret); err != nil {
		return schemas.ChannelOut{}, err
	}
	who := actorOf(ctx).Label
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.NotificationChannel, error) {
		row, err := tx.NotificationChannel.Create().SetProjectID(proj.ID).
			SetType(notificationchannel.Type(in.Type)).SetName(in.Name).SetConfig(config).SetSecret(secret).
			SetCreatedBy(who).SetUpdatedBy(who).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.ChannelWritten, "channel", row.UID.String(),
			map[string]any{"project": proj.Slug, "type": in.Type, "name": in.Name, "created": true})
	})
	if err != nil {
		return schemas.ChannelOut{}, err
	}
	ctx.SetStatus(201)
	out := channelOut(row)
	out.SigningSecret = secret["signing_secret"]
	return out, nil
}

// UpdateChannel changes a channel's name, whether it is on, and where it sends. A secret not sent
// is kept; a new destination is not the one the last test reached, so the test is forgotten.
func UpdateChannel(ctx *muzak.Context, in schemas.UpdateChannelIn) (schemas.ChannelOut, error) {
	sender := muzak.From[*notify.Sender](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := findChannel(ctx, in.ChannelID)
	if err != nil {
		return schemas.ChannelOut{}, err
	}
	config, secret := written(string(row.Type), in.Config, row.Secret)
	if err := checked(sender, string(row.Type), config, secret); err != nil {
		return schemas.ChannelOut{}, err
	}
	moved := notify.ChannelOf(&ent.NotificationChannel{Type: row.Type, Config: config, Secret: secret}).URL != notify.ChannelOf(row).URL ||
		secret["routing_key"] != row.Secret["routing_key"]
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.NotificationChannel, error) {
		update := tx.NotificationChannel.UpdateOne(row).SetName(in.Name).SetConfig(config).SetSecret(secret).
			SetUpdatedBy(actorOf(ctx).Label)
		if in.Enabled != nil {
			update = update.SetEnabled(*in.Enabled)
		}
		if moved {
			update = update.ClearLastTestAt().ClearLastTestOk().SetLastTestDetail("")
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.ChannelWritten, "channel", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": in.Name, "enabled": updated.Enabled, "created": false,
				"destination_changed": moved})
	})
	if err != nil {
		return schemas.ChannelOut{}, err
	}
	return channelOut(updated), nil
}

// DeleteChannel removes a channel. Its log stays.
func DeleteChannel(ctx *muzak.Context, in schemas.ChannelParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := findChannel(ctx, in.ChannelID)
	if err != nil {
		return muzak.Empty{}, err
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		if err := tx.NotificationChannel.DeleteOne(row).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.ChannelDeleted, "channel", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": row.Name})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}

func testMessage(ctx *muzak.Context) notify.Message {
	proj := muzak.From[*core.Principal](ctx).Project
	return notify.Message{Title: "PlayerSense test", Test: true, Source: "Test from " + proj.Name,
		Summary: "If you can read this, " + proj.Name + " can notify this channel."}
}

// TestChannel sends a test to a saved channel, kept as its last test and in its log.
func TestChannel(ctx *muzak.Context, in schemas.ChannelParams) (schemas.ChannelTestOut, error) {
	db := muzak.From[*core.DB](ctx)
	sender := muzak.From[*notify.Sender](ctx)
	row, err := findChannel(ctx, in.ChannelID)
	if err != nil {
		return schemas.ChannelTestOut{}, err
	}
	r := sender.Deliver(ctx.Context(), db.Client, row, testMessage(ctx), "Test by "+actorOf(ctx).Label)
	return schemas.ChannelTestOut{At: nowUTC(), OK: r.OK, Detail: r.Detail}, nil
}

// TestDraftChannel sends a test to a channel not yet saved. Nothing is kept: the dialog shows the
// answer, and passes it as the new channel's last test when it is saved.
func TestDraftChannel(ctx *muzak.Context, in schemas.TestDraftChannelIn) (schemas.ChannelTestOut, error) {
	sender := muzak.From[*notify.Sender](ctx)
	config, secret := written(in.Type, in.Config, nil)
	if err := checked(sender, in.Type, config, secret); err != nil {
		return schemas.ChannelTestOut{}, err
	}
	row := &ent.NotificationChannel{Type: notificationchannel.Type(in.Type), Config: config, Secret: secret}
	r := sender.Send(ctx.Context(), notify.ChannelOf(row), testMessage(ctx))
	return schemas.ChannelTestOut{At: nowUTC(), OK: r.OK, Detail: r.Detail}, nil
}

// ListChannelDeliveries is what was sent to the project's channels, newest first.
func ListChannelDeliveries(ctx *muzak.Context, in schemas.ChannelDeliveriesParams) (schemas.ChannelDeliveriesOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	q := db.Client.ChannelDelivery.Query().Where(channeldelivery.ProjectID(proj.ID))
	if in.Channel != "" {
		id, err := uuid.Parse(in.Channel)
		if err != nil {
			return schemas.ChannelDeliveriesOut{}, muzak.UnprocessableEntity("channel is not a channel id")
		}
		row, err := findChannel(ctx, id)
		if err != nil {
			return schemas.ChannelDeliveriesOut{}, err
		}
		q = q.Where(channeldelivery.ChannelID(row.ID))
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.ChannelDeliveriesOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(channeldelivery.IDLT(id))
	}
	rows, err := q.Order(ent.Desc(channeldelivery.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.ChannelDeliveriesOut{}, core.AsHTTPError(err)
	}
	out := schemas.ChannelDeliveriesOut{Deliveries: make([]schemas.ChannelDeliveryOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	uids := map[int]uuid.UUID{}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ChannelID)
	}
	if channels, err := db.Client.NotificationChannel.Query().Where(notificationchannel.IDIn(ids...)).
		Select(notificationchannel.FieldID, notificationchannel.FieldUID).All(ctx.Context()); err == nil {
		for _, c := range channels {
			uids[c.ID] = c.UID
		}
	}
	for _, r := range rows {
		one := schemas.ChannelDeliveryOut{At: r.CreatedAt, ChannelName: r.ChannelName, Source: r.Source, Title: r.Title,
			OK: r.Ok, Detail: r.Detail, Test: r.Test, DurationMs: r.DurationMs}
		if uid, found := uids[r.ChannelID]; found {
			one.ChannelID = &uid
		}
		out.Deliveries = append(out.Deliveries, one)
	}
	return out, nil
}
