package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
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
	if err != nil || lot == nil {
		return ignored("not a PlayerSense top-up"), err
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
