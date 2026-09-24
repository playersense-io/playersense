package handlers

import (
	"context"
	"errors"
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/predicate"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/schemas"
)

// The billing team's queue: accounts that need a person, and what Stripe told us.

// stuckAfter is how long an automatic top-up may wait on Stripe before it is somebody's problem.
// Stripe answers a charge in seconds; an hour is a lost webhook.
const stuckAfter = time.Hour

// TriageAccounts lists the billing accounts that need a person, each with every reason it is on
// the list: not active, an invoice past due, an automatic top-up that failed, or one that Stripe
// has not answered.
func TriageAccounts(ctx *muzak.Context, in schemas.AccountTriageIn) (schemas.AccountTriageOut, error) {
	db := muzak.From[*core.DB](ctx)
	now := nowUTC()

	troubled := billingaccount.StateIn(billingaccount.StateGracePeriod, billingaccount.StateRestricted,
		billingaccount.StateSuspended)
	overdue := billingaccount.OverdueSinceNotNil()
	declined := billingaccount.AutoTopupLastErrorNEQ("")
	stuck := billingaccount.AutoTopupPendingAtLT(now.Add(-stuckAfter))

	which := needsAttention(now)
	switch in.Reason {
	case "state":
		which = troubled
	case "overdue":
		which = overdue
	case "auto_topup":
		which = declined
	case "stuck":
		which = stuck
	}
	q := db.Client.BillingAccount.Query().Where(which)
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.AccountTriageOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(billingaccount.IDLT(id))
	}
	rows, err := q.WithOrganization().WithPlan().Order(ent.Desc(billingaccount.FieldID)).
		Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.AccountTriageOut{}, core.AsHTTPError(err)
	}

	out := schemas.AccountTriageOut{Accounts: make([]schemas.AccountTriageRowOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	for _, account := range rows {
		row := schemas.AccountTriageRowOut{
			State: string(account.State), StateChangedAt: account.StateChangedAt,
			Collection: string(account.Collection), ArrearsMicros: account.ArrearsMicros,
			ReceivableMicros: account.ReceivableMicros, OverdueSince: account.OverdueSince,
			AutoTopUpLastError: account.AutoTopupLastError, AutoTopUpRetryAfter: account.AutoTopupRetryAfter,
			AutoTopUpPendingAt: account.AutoTopupPendingAt, Reasons: []string{},
		}
		if org := account.Edges.Organization; org != nil {
			row.Organization = schemas.OrganizationRefOut{Slug: org.Slug, Name: org.Name}
		}
		if account.Edges.Plan != nil {
			row.Plan = account.Edges.Plan.Key
		}
		switch account.State {
		case billingaccount.StateGracePeriod, billingaccount.StateRestricted, billingaccount.StateSuspended:
			row.Reasons = append(row.Reasons, "state")
		}
		if account.OverdueSince != nil {
			row.Reasons = append(row.Reasons, "overdue")
		}
		if account.AutoTopupLastError != "" {
			row.Reasons = append(row.Reasons, "auto_topup")
		}
		if account.AutoTopupPendingAt != nil && account.AutoTopupPendingAt.Before(now.Add(-stuckAfter)) {
			row.Reasons = append(row.Reasons, "stuck")
		}
		out.Accounts = append(out.Accounts, row)
	}
	return out, nil
}

// needsAttention is every reason an account is on the triage list.
func needsAttention(now time.Time) predicate.BillingAccount {
	return billingaccount.Or(
		billingaccount.StateIn(billingaccount.StateGracePeriod, billingaccount.StateRestricted,
			billingaccount.StateSuspended),
		billingaccount.OverdueSinceNotNil(),
		billingaccount.AutoTopupLastErrorNEQ(""),
		billingaccount.AutoTopupPendingAtLT(now.Add(-stuckAfter)))
}

// ListStripeEvents is every webhook Stripe sent, newest first, narrowed by what became of it, its
// type or the organization it concerned.
func ListStripeEvents(ctx *muzak.Context, in schemas.StripeEventsIn) (schemas.StripeEventListOut, error) {
	db := muzak.From[*core.DB](ctx)
	q := db.Client.StripeEvent.Query()
	if in.Status != "" {
		q = q.Where(stripeevent.StatusEQ(stripeevent.Status(in.Status)))
	}
	if in.Type != "" {
		q = q.Where(stripeevent.Type(in.Type))
	}
	if in.Org != "" {
		q = q.Where(stripeevent.HasAccountWith(
			billingaccount.HasOrganizationWith(organization.Slug(in.Org))))
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.StripeEventListOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(stripeevent.IDLT(id))
	}
	// Selected without the payload: a page of bodies is megabytes nobody reads in a list.
	rows, err := q.Select(stripeevent.FieldID, stripeevent.FieldUID, stripeevent.FieldStripeEventID,
		stripeevent.FieldType, stripeevent.FieldAPIVersion, stripeevent.FieldStripeCreatedAt,
		stripeevent.FieldCreatedAt, stripeevent.FieldStatus, stripeevent.FieldError,
		stripeevent.FieldAttempts, stripeevent.FieldProcessedAt, stripeevent.FieldAccountID).
		Order(ent.Desc(stripeevent.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.StripeEventListOut{}, core.AsHTTPError(err)
	}
	out := schemas.StripeEventListOut{Events: make([]schemas.StripeEventOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	slugs, err := slugsOfAccounts(ctx.Context(), db, rows)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, row := range rows {
		out.Events = append(out.Events, stripeEventOut(row, slugs, false))
	}
	return out, nil
}

// ReadStripeEvent is one webhook with the body it arrived with.
func ReadStripeEvent(ctx *muzak.Context, in schemas.StripeEventParams) (schemas.StripeEventOut, error) {
	db := muzak.From[*core.DB](ctx)
	row, err := db.Client.StripeEvent.Query().Where(stripeevent.UID(in.EventID)).Only(ctx.Context())
	if err != nil {
		return schemas.StripeEventOut{}, core.AsHTTPError(err)
	}
	slugs, err := slugsOfAccounts(ctx.Context(), db, []*ent.StripeEvent{row})
	if err != nil {
		return schemas.StripeEventOut{}, core.AsHTTPError(err)
	}
	return stripeEventOut(row, slugs, true), nil
}

// ReplayStripeEvent handles a recorded event again, from the body it arrived with. For after a
// handler is fixed: an event that failed, or that was ignored and should not have been. A processed
// event is refused, since running it again could only move money twice.
func ReplayStripeEvent(ctx *muzak.Context, in schemas.StripeEventParams) (schemas.StripeReplayOut, error) {
	db := muzak.From[*core.DB](ctx)
	hooks := muzak.From[*payments.Webhooks](ctx)
	row, err := db.Client.StripeEvent.Query().Where(stripeevent.UID(in.EventID)).Only(ctx.Context())
	if err != nil {
		return schemas.StripeReplayOut{}, core.AsHTTPError(err)
	}
	outcome, err := hooks.Replay(ctx.Context(), row.ID)
	switch {
	case errors.Is(err, payments.ErrAlreadyHandled):
		return schemas.StripeReplayOut{}, muzak.Conflict(err.Error()).WithCode("already_processed")
	case err != nil:
		// The failure is on the row (Webhooks.fail), so it is read back below as well as answered.
		ctx.Logger().Warn("a replayed stripe event failed again", "event", row.StripeEventID, "error", err)
	}
	row, readErr := db.Client.StripeEvent.Get(ctx.Context(), row.ID)
	if readErr != nil {
		return schemas.StripeReplayOut{}, core.AsHTTPError(readErr)
	}
	slugs, readErr := slugsOfAccounts(ctx.Context(), db, []*ent.StripeEvent{row})
	if readErr != nil {
		return schemas.StripeReplayOut{}, core.AsHTTPError(readErr)
	}
	out := schemas.StripeReplayOut{
		Status: outcome.Status, Credited: outcome.Credited, Reason: outcome.Reason,
		Event: stripeEventOut(row, slugs, false),
	}
	if err != nil {
		out.Status = string(stripeevent.StatusFailed)
		out.Reason = row.Error
	}
	return out, nil
}

// slugsOfAccounts maps the accounts a page of events concerned to their organizations' slugs, in
// one query.
func slugsOfAccounts(ctx context.Context, db *core.DB, rows []*ent.StripeEvent) (map[int]string, error) {
	ids := map[int]bool{}
	for _, row := range rows {
		if row.AccountID != nil {
			ids[*row.AccountID] = true
		}
	}
	out := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]int, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	accounts, err := db.Client.BillingAccount.Query().Where(billingaccount.IDIn(list...)).
		WithOrganization().All(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if account.Edges.Organization != nil {
			out[account.ID] = account.Edges.Organization.Slug
		}
	}
	return out, nil
}

func stripeEventOut(row *ent.StripeEvent, slugs map[int]string, withPayload bool) schemas.StripeEventOut {
	out := schemas.StripeEventOut{
		ID: row.UID, StripeEventID: row.StripeEventID, Type: row.Type, APIVersion: row.APIVersion,
		StripeCreatedAt: row.StripeCreatedAt, ReceivedAt: row.CreatedAt, Status: string(row.Status),
		Error: row.Error, Attempts: row.Attempts, ProcessedAt: row.ProcessedAt,
	}
	if row.AccountID != nil {
		out.Organization = slugs[*row.AccountID]
	}
	if withPayload {
		out.Payload = row.Payload
	}
	return out
}
