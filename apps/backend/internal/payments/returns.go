package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// Money going back: refunds and disputes.
//
// A top-up that is refunded, or disputed by the customer's bank, is money PlayerSense no longer has, so
// the credit it bought is taken back (wallet.Reverse): what is left of that top-up first, then other
// purchased credit, then arrears. It is found by its PaymentIntent — the same id its credit was keyed
// on — so no metadata is needed and a refund of anything PlayerSense did not sell is ignored.
//
// Each refund and each dispute is one reversal, keyed on Stripe's own id, however many of the several
// events that report it arrive. A refund that fails after succeeding, or a dispute that is won, puts
// back exactly what was taken — and only if something was.
//
// Automatic top-up is switched off on the account either way. The alternative is charging, seconds
// later, the card whose money was just returned — which is how a refunded customer becomes a
// disputing one, and how a disputed card becomes a second dispute.
//
// A usage invoice's payment (ADR 0045) is found the same way, by the PaymentIntent its invoice
// recorded. A dispute puts what it took back on the receivable and reopens the invoice — overdue at
// once, since its due date has passed — and a won dispute settles it again. A refund of one is
// recorded and flagged: whether money returned means the customer owed less or was overpaid is a
// person's decision, not the ledger's.

func (w *Webhooks) returns(ctx context.Context, tx *ent.Tx, event stripe.Event) (handled, bool, error) {
	switch event.Type {
	case "refund.created", "refund.updated", "refund.failed":
		var r stripe.Refund
		if err := json.Unmarshal(event.Data.Raw, &r); err != nil {
			return handled{}, true, fmt.Errorf("decode the refund: %w", err)
		}
		pi := ""
		if r.PaymentIntent != nil {
			pi = r.PaymentIntent.ID
		}
		switch r.Status {
		case stripe.RefundStatusSucceeded:
			h, err := w.takeBack(ctx, tx, pi, r.Amount, string(r.Currency), "refund:"+r.ID,
				"Refund", "a top-up was refunded, so automatic top-up was turned off; turn it back on to resume")
			return h, true, err
		case stripe.RefundStatusFailed, stripe.RefundStatusCanceled:
			h, err := w.restore(ctx, tx, pi, "refund:"+r.ID, "refund-restored:"+r.ID,
				"Refund "+string(r.Status)+": credit restored")
			return h, true, err
		default:
			return ignored("the refund is " + string(r.Status) + "; nothing is taken back until it succeeds"), true, nil
		}

	case "charge.refunded", "charge.refund.updated":
		// Reported again, per refund, by refund.* — which carry the refund's own id and status. A
		// charge does not reliably list its refunds at this API version, so it is not the source.
		return ignored("handled through refund events"), true, nil

	case "charge.dispute.created", "charge.dispute.funds_withdrawn":
		var d stripe.Dispute
		if err := json.Unmarshal(event.Data.Raw, &d); err != nil {
			return handled{}, true, fmt.Errorf("decode the dispute: %w", err)
		}
		if strings.HasPrefix(string(d.Status), "warning_") {
			// An inquiry: the bank is asking, and no money has moved.
			return ignored("an inquiry; no funds were withdrawn"), true, nil
		}
		h, err := w.takeBack(ctx, tx, piOf(d.PaymentIntent), d.Amount, string(d.Currency), "dispute:"+d.ID,
			"Disputed payment", "a top-up was disputed, so automatic top-up was turned off")
		return h, true, err

	case "charge.dispute.funds_reinstated", "charge.dispute.closed":
		var d stripe.Dispute
		if err := json.Unmarshal(event.Data.Raw, &d); err != nil {
			return handled{}, true, fmt.Errorf("decode the dispute: %w", err)
		}
		if event.Type == "charge.dispute.closed" && d.Status != stripe.DisputeStatusWon {
			return ignored("closed " + string(d.Status) + "; the credit stays taken back"), true, nil
		}
		h, err := w.restore(ctx, tx, piOf(d.PaymentIntent), "dispute:"+d.ID, "dispute-restored:"+d.ID,
			"Dispute won: credit restored")
		return h, true, err
	}
	return handled{}, false, nil
}

// takeBack reverses a top-up's credit and turns automatic top-up off.
func (w *Webhooks) takeBack(ctx context.Context, tx *ent.Tx, pi string, cents int64, currency, key, description, reason string) (handled, error) {
	lot, err := topUpLot(ctx, tx, pi)
	if err != nil {
		return handled{}, err
	}
	if lot == nil {
		return w.takeBackInvoice(ctx, tx, pi, cents, key)
	}
	if !strings.EqualFold(currency, lot.Currency) {
		w.log().Error("a refund or dispute is in another currency than its top-up", "payment_intent", pi, "currency", currency)
		return ignored(fmt.Sprintf("in %s, the top-up was in %s", currency, lot.Currency)), nil
	}
	if cents <= 0 {
		return ignored("nothing to take back"), nil
	}
	result, err := wallet.Reverse(ctx, tx, wallet.ReverseRequest{
		AccountID: lot.AccountID, Lot: lot.ID, Amount: cents * billing.CentMicros,
		Description: description, StripeReference: pi, At: w.now(), Key: key,
	})
	if err != nil {
		return handled{}, err
	}
	if result.Applied {
		if err := tx.BillingAccount.Update().
			Where(billingaccount.ID(lot.AccountID)).
			SetAutoTopupEnabled(false).
			ClearAutoTopupRequestedAt().
			SetAutoTopupLastError(reason).
			Exec(ctx); err != nil {
			return handled{}, fmt.Errorf("turn automatic top-up off: %w", err)
		}
	}
	return handled{status: stripeevent.StatusProcessed, accountID: lot.AccountID}, nil
}

// restore puts back what a reversal took, when the refund failed or the dispute was won.
func (w *Webhooks) restore(ctx context.Context, tx *ent.Tx, pi, reversedKey, key, description string) (handled, error) {
	taken, err := wallet.Reversed(ctx, tx, reversedKey)
	if err != nil {
		return handled{}, err
	}
	if taken == nil {
		return ignored("nothing was taken back, so nothing is restored"), nil
	}
	if taken.InvoiceID != nil {
		return w.restoreInvoice(ctx, tx, taken, key, description)
	}
	// Purchased credit, because it is the customer's money returning — and a purchase lot pays any
	// arrears the reversal created before it becomes spendable.
	if _, _, err := wallet.Grant(ctx, tx, wallet.GrantRequest{
		AccountID: taken.AccountID, Kind: wallet.Purchase, Amount: -taken.AmountMicros,
		GrantedAt: w.now(), Description: description, StripeReference: pi, Key: key,
	}); err != nil {
		return handled{}, err
	}
	return handled{status: stripeevent.StatusProcessed, accountID: taken.AccountID}, nil
}

// topUpLot is the lot a PaymentIntent credited, or nil if it credited none.
func topUpLot(ctx context.Context, tx *ent.Tx, pi string) (*ent.CreditLot, error) {
	if pi == "" {
		return nil, nil
	}
	lot, err := tx.CreditLot.Query().Where(creditlot.IdempotencyKey("stripe:" + pi)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return lot, err
}

func piOf(pi *stripe.PaymentIntent) string {
	if pi == nil {
		return ""
	}
	return pi.ID
}

// takeBackInvoice reopens a usage invoice whose payment a bank took back.
func (w *Webhooks) takeBackInvoice(ctx context.Context, tx *ent.Tx, pi string, cents int64, key string) (handled, error) {
	if pi == "" {
		return ignored("not a PlayerSense payment"), nil
	}
	row, err := w.invoiceOfPayment(ctx, tx, pi)
	if err != nil {
		return handled{}, err
	}
	if row == nil {
		return ignored("not a PlayerSense payment"), nil
	}
	if !strings.HasPrefix(key, "dispute:") {
		w.log().Error("a usage invoice's payment was refunded; adjust the ledger by hand",
			"invoice", row.UID, "payment_intent", pi, "cents", cents)
		return handled{status: stripeevent.StatusProcessed, accountID: row.AccountID,
			reason: "a refund of an invoice payment is not applied; adjust the ledger by hand"}, nil
	}
	// Stripe reports a dispute more than once — charge.dispute.created and funds_withdrawn arrive in
	// the same second — and each report after the first is the same dispute, already applied.
	if done, err := wallet.Reversed(ctx, tx, key); err != nil || done != nil {
		return handled{status: stripeevent.StatusProcessed, accountID: row.AccountID,
			reason: "already taken back"}, err
	}
	// A dispute proves the payment it disputes was made, and it can arrive before invoice.paid — in
	// the same second, against Stripe's test mode. So an invoice not yet recorded as paid is settled
	// here first, under the key invoice.paid will use: that event then finds it done, and leaves the
	// reopening below standing. Either order ends in the same ledger.
	if row.Status == invoice.StatusOpen && row.AmountPaidMicros == 0 && row.AmountRemainingMicros > 0 &&
		row.StripeInvoiceID != nil {
		result, err := wallet.PayInvoice(ctx, tx, wallet.SettleRequest{
			AccountID: row.AccountID, InvoiceID: row.ID, Amount: row.AmountRemainingMicros, At: w.now(),
			Description: "Invoice " + orUID(row.Number, row) + " paid", StripeReference: *row.StripeInvoiceID,
			Key: PaidKey(*row.StripeInvoiceID),
		})
		if err != nil {
			return handled{}, err
		}
		if result.Applied {
			row.AmountPaidMicros, row.AmountRemainingMicros = row.AmountRemainingMicros, 0
		}
	}
	amount := min(cents*billing.CentMicros, row.AmountPaidMicros)
	if amount <= 0 {
		return ignored("nothing was paid on the invoice to take back"), nil
	}
	result, err := wallet.Reopen(ctx, tx, wallet.SettleRequest{
		AccountID: row.AccountID, InvoiceID: row.ID, Amount: amount, At: w.now(),
		Description: "Disputed payment of invoice " + orUID(row.Number, row), StripeReference: pi, Key: key,
	})
	if err != nil {
		return handled{}, err
	}
	if result.Applied {
		if err := tx.Invoice.UpdateOneID(row.ID).
			SetStatus(invoice.StatusOpen).
			SetAmountPaidMicros(row.AmountPaidMicros - amount).
			SetAmountRemainingMicros(row.AmountRemainingMicros + amount).
			Exec(ctx); err != nil {
			return handled{}, fmt.Errorf("reopen the invoice: %w", err)
		}
	}
	return handled{status: stripeevent.StatusProcessed, accountID: row.AccountID},
		ReassessOverdue(ctx, tx, row.AccountID, w.now())
}

// invoiceOfPayment is the usage invoice a PaymentIntent paid, locked, or nil.
//
// Recorded from invoice_payment.paid when that has arrived. Stripe sends it up to a minute after the
// payment, and a dispute of the payment can arrive first — so when nothing is recorded, Stripe is
// asked. An error asking is returned rather than ignored: the event is answered 500 and Stripe
// delivers it again, instead of a dispute being dropped because Stripe was slow to answer once.
func (w *Webhooks) invoiceOfPayment(ctx context.Context, tx *ent.Tx, pi string) (*ent.Invoice, error) {
	row, err := tx.Invoice.Query().Where(invoice.PaymentIntent(pi)).ForUpdate().Only(ctx)
	switch {
	case err == nil:
		return row, nil
	case !ent.IsNotFound(err):
		return nil, err
	case w.Gateway == nil:
		return nil, nil
	}
	id, err := w.Gateway.InvoiceOfPayment(ctx, pi)
	switch {
	case errors.Is(err, ErrNotConfigured):
		return nil, nil
	case err != nil:
		return nil, err
	case id == "":
		return nil, nil
	}
	row, err = tx.Invoice.Query().Where(invoice.StripeInvoiceID(id)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil // a subscription's invoice, or one PlayerSense did not issue
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Invoice.UpdateOneID(row.ID).SetPaymentIntent(pi).Exec(ctx); err != nil {
		return nil, fmt.Errorf("record the invoice's payment: %w", err)
	}
	row.PaymentIntent = &pi
	return row, nil
}

// restoreInvoice settles an invoice again when the dispute that reopened it is won.
func (w *Webhooks) restoreInvoice(ctx context.Context, tx *ent.Tx, taken *ent.LedgerEntry, key, description string) (handled, error) {
	row, err := tx.Invoice.Query().Where(invoice.ID(*taken.InvoiceID)).ForUpdate().Only(ctx)
	if err != nil {
		return handled{}, err
	}
	amount := -taken.AmountMicros
	result, err := wallet.PayInvoice(ctx, tx, wallet.SettleRequest{
		AccountID: taken.AccountID, InvoiceID: row.ID, Amount: amount, At: w.now(),
		Description: description, StripeReference: orEmpty(taken.StripeReference), Key: key,
	})
	if err != nil {
		return handled{}, err
	}
	if result.Applied {
		update := tx.Invoice.UpdateOneID(row.ID).
			SetAmountPaidMicros(row.AmountPaidMicros + amount).
			SetAmountRemainingMicros(max(0, row.AmountRemainingMicros-amount))
		if row.AmountRemainingMicros <= amount {
			update = update.SetStatus(invoice.StatusPaid)
		}
		if err := update.Exec(ctx); err != nil {
			return handled{}, fmt.Errorf("settle the invoice again: %w", err)
		}
	}
	return handled{status: stripeevent.StatusProcessed, accountID: taken.AccountID},
		ReassessOverdue(ctx, tx, taken.AccountID, w.now())
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
