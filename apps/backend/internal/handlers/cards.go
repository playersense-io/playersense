package handlers

import (
	"encoding/json"
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/paymentcard"
	"playersense.dev/backend/ent/receipt"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/schemas"
)

// The organization's cards, invoice details and receipts: copies of what Stripe holds, kept by the
// webhooks (payments/cards.go) and read from Stripe itself the first time and on a refresh.

// syncedAccount is the account, having asked Stripe for its cards and details if nobody has yet
// or the caller wants them fresh. A Stripe that cannot answer leaves the copy as it was.
func syncedAccount(ctx *muzak.Context, refresh bool) (*ent.BillingAccount, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return nil, err
	}
	if account.StripeCustomerID == nil || (account.CardsSyncedAt != nil && !refresh) {
		return account, nil
	}
	gateway := muzak.From[payments.Gateway](ctx)
	details, err := gateway.DescribeCustomer(ctx.Context(), *account.StripeCustomerID)
	if err != nil {
		if refresh {
			return nil, paymentsError(err)
		}
		ctx.Logger().Warn("could not read the customer from stripe; showing what the webhooks kept", "error", err)
		return account, nil
	}
	if _, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		return struct{}{}, payments.SyncCustomer(ctx.Context(), tx, account.ID, details, nowUTC())
	}); err != nil {
		return nil, err
	}
	return accountOf(ctx.Context(), db, org.ID)
}

// ListCards is the organization's saved cards: brand, last four, expiry and which is the default.
func ListCards(ctx *muzak.Context, in schemas.CardsParams) (schemas.CardsOut, error) {
	db := muzak.From[*core.DB](ctx)
	account, err := syncedAccount(ctx, in.Refresh)
	if err != nil {
		return schemas.CardsOut{}, err
	}
	rows, err := db.Client.PaymentCard.Query().
		Where(paymentcard.AccountID(account.ID), paymentcard.DetachedAtIsNil()).
		Order(ent.Desc(paymentcard.FieldIsDefault), ent.Desc(paymentcard.FieldID)).All(ctx.Context())
	if err != nil {
		return schemas.CardsOut{}, core.AsHTTPError(err)
	}
	now := nowUTC()
	out := schemas.CardsOut{Cards: make([]schemas.CardOut, 0, len(rows)), SyncedAt: account.CardsSyncedAt}
	for _, c := range rows {
		// A card works to the end of its expiry month.
		expires := time.Date(c.ExpYear, time.Month(c.ExpMonth)+1, 1, 0, 0, 0, 0, time.UTC)
		out.Cards = append(out.Cards, schemas.CardOut{
			ID: c.StripePaymentMethodID, Brand: c.Brand, Last4: c.Last4, ExpMonth: c.ExpMonth, ExpYear: c.ExpYear,
			Funding: c.Funding, IsDefault: c.IsDefault, Expired: c.ExpYear > 0 && !now.Before(expires), AddedAt: c.CreatedAt,
		})
	}
	return out, nil
}

// AddCard opens Stripe Checkout to save a card without paying. It is listed when Stripe reports it.
func AddCard(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.CheckoutOut, error) {
	db := muzak.From[*core.DB](ctx)
	settings := muzak.From[config.Settings](ctx)
	gateway := muzak.From[payments.Gateway](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.CheckoutOut{}, err
	}
	customer, err := ensureCustomer(ctx, account)
	if err != nil {
		return schemas.CheckoutOut{}, err
	}
	checkout, err := gateway.CreateSetupCheckout(ctx.Context(), payments.SetupCheckoutRequest{
		AccountUID: account.UID.String(), Customer: customer, Currency: account.Currency,
		SuccessURL: settings.StripeCheckoutSuccessURL, CancelURL: settings.StripeCheckoutCancelURL,
	})
	if err != nil {
		return schemas.CheckoutOut{}, paymentsError(err)
	}
	return schemas.CheckoutOut{URL: checkout.URL, ID: checkout.ID, ExpiresAt: checkout.ExpiresAt}, nil
}

// cardOf is one of the account's saved cards, still attached.
func cardOf(ctx *muzak.Context, id string) (*ent.BillingAccount, *ent.PaymentCard, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return nil, nil, err
	}
	card, err := db.Client.PaymentCard.Query().Where(paymentcard.AccountID(account.ID),
		paymentcard.StripePaymentMethodID(id), paymentcard.DetachedAtIsNil()).Only(ctx.Context())
	if ent.IsNotFound(err) || account.StripeCustomerID == nil {
		return nil, nil, muzak.NotFound("this organization has no such card")
	}
	if err != nil {
		return nil, nil, core.AsHTTPError(err)
	}
	return account, card, nil
}

// SetDefaultCard makes a saved card the one Stripe charges: automatic top-up and invoices.
func SetDefaultCard(ctx *muzak.Context, in schemas.CardParams) (schemas.CardsOut, error) {
	gateway := muzak.From[payments.Gateway](ctx)
	account, card, err := cardOf(ctx, in.CardID)
	if err != nil {
		return schemas.CardsOut{}, err
	}
	if err := gateway.SetDefaultCard(ctx.Context(), *account.StripeCustomerID, card.StripePaymentMethodID); err != nil {
		return schemas.CardsOut{}, paymentsError(err)
	}
	if _, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		if err := payments.MarkDefault(ctx.Context(), tx, account.ID, card.StripePaymentMethodID); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.CardDefaultSet, "payment_card", card.UID.String(),
			map[string]any{"brand": card.Brand, "last4": card.Last4})
	}); err != nil {
		return schemas.CardsOut{}, err
	}
	return ListCards(ctx, schemas.CardsParams{OrgSlug: in.OrgSlug})
}

// RemoveCard removes a saved card at Stripe. The newest card left becomes the default; with none
// left, automatic top-up is turned off, since there is nothing to charge.
func RemoveCard(ctx *muzak.Context, in schemas.CardParams) (schemas.CardsOut, error) {
	gateway := muzak.From[payments.Gateway](ctx)
	account, card, err := cardOf(ctx, in.CardID)
	if err != nil {
		return schemas.CardsOut{}, err
	}
	if err := gateway.DetachCard(ctx.Context(), card.StripePaymentMethodID); err != nil {
		return schemas.CardsOut{}, paymentsError(err)
	}
	var next string
	if _, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		next, err = payments.MarkDetached(ctx.Context(), tx, account.ID, card.StripePaymentMethodID, nowUTC())
		if err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		topUpOff := false
		if next == "" && account.AutoTopupEnabled {
			if err := tx.BillingAccount.UpdateOne(account).SetAutoTopupEnabled(false).Exec(ctx.Context()); err != nil {
				return struct{}{}, core.AsHTTPError(err)
			}
			topUpOff = true
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.CardRemoved, "payment_card", card.UID.String(),
			map[string]any{"brand": card.Brand, "last4": card.Last4, "auto_topup_turned_off": topUpOff})
	}); err != nil {
		return schemas.CardsOut{}, err
	}
	if next != "" && card.IsDefault {
		// The removed card was the default: Stripe is told which charges now, as the list says.
		if err := gateway.SetDefaultCard(ctx.Context(), *account.StripeCustomerID, next); err == nil {
			_, _ = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
				return struct{}{}, payments.MarkDefault(ctx.Context(), tx, account.ID, next)
			})
		}
	}
	return ListCards(ctx, schemas.CardsParams{OrgSlug: in.OrgSlug})
}

// ReadInvoiceDetails is what Stripe prints on the organization's invoices.
func ReadInvoiceDetails(ctx *muzak.Context, in schemas.CardsParams) (schemas.InvoiceDetailsOut, error) {
	account, err := syncedAccount(ctx, in.Refresh)
	if err != nil {
		return schemas.InvoiceDetailsOut{}, err
	}
	out := schemas.InvoiceDetailsOut{Address: []string{}, TaxIDs: []string{}, SyncedAt: account.CardsSyncedAt}
	if raw, err := json.Marshal(account.InvoiceDetails); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	if out.Address == nil {
		out.Address = []string{}
	}
	if out.TaxIDs == nil {
		out.TaxIDs = []string{}
	}
	if account.StripeCustomerID != nil {
		out.Customer = *account.StripeCustomerID
	}
	return out, nil
}

// ListReceipts is the organization's receipts for top-ups and its subscription, newest first.
func ListReceipts(ctx *muzak.Context, in schemas.ReceiptsParams) (schemas.ReceiptsOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.ReceiptsOut{}, err
	}
	q := db.Client.Receipt.Query().Where(receipt.AccountID(account.ID))
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.ReceiptsOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(receipt.IDLT(id))
	}
	rows, err := q.Order(ent.Desc(receipt.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.ReceiptsOut{}, core.AsHTTPError(err)
	}
	out := schemas.ReceiptsOut{Receipts: make([]schemas.ReceiptOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	for _, r := range rows {
		out.Receipts = append(out.Receipts, schemas.ReceiptOut{
			ID: r.StripeInvoiceID, Number: r.Number, Kind: string(r.Kind), Status: string(r.Status),
			Description: r.Description, TotalMicros: r.TotalMicros, Currency: r.Currency, IssuedAt: r.IssuedAt,
			PaidAt: r.PaidAt, HostedURL: r.HostedURL, PDFURL: r.PdfURL,
		})
	}
	return out, nil
}
