package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// PlayerSense's own usage invoices (ADR 0045) — the one kind of invoice whose events move money.
//
// Matched by metadata this code put there (`playersense_kind=usage_invoice`), so a top-up's invoice or a
// subscription's is never mistaken for one: those stay recorded and ignored, and a top-up is credited by
// its PaymentIntent, once (ADR 0040). A usage invoice's own PaymentIntent carries none of our metadata,
// so the top-up path can never credit it a second time.
//
// What moves money, each keyed on Stripe's id so every redelivery is one movement:
//
//	invoice.paid                           the receivable falls by what was paid (wallet.PayInvoice)
//	invoice.voided / marked_uncollectible  what was still owed is written off (wallet.WriteOff)
//	credit_note.created                    what it credited before payment is written off
//
// The rest only records: the number, the links and the due date as the invoice is finalized and sent,
// and the PaymentIntent that paid it, so a dispute of that payment can find the invoice. Status only
// moves forward — draft, open, then settled — so an event delivered late cannot reopen a paid invoice.

// usageInvoice handles an event about a usage invoice, and reports whether it was one.
func (w *Webhooks) usageInvoice(ctx context.Context, tx *ent.Tx, event stripe.Event) (handled, bool, error) {
	switch event.Type {
	case "invoice.finalized", "invoice.sent", "invoice.updated", "invoice.paid", "invoice.payment_succeeded",
		"invoice.voided", "invoice.marked_uncollectible", "invoice.overdue", "invoice.payment_failed":
		var raw stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &raw); err != nil {
			return handled{}, true, fmt.Errorf("decode the invoice: %w", err)
		}
		if raw.Metadata[MetaKind] != KindUsageInvoice {
			return handled{}, false, nil
		}
		h, err := applied(ApplyInvoice(ctx, tx, issuedOf(&raw), w.now()))
		return h, true, err

	case "invoice_payment.paid":
		var payment stripe.InvoicePayment
		if err := json.Unmarshal(event.Data.Raw, &payment); err != nil {
			return handled{}, true, fmt.Errorf("decode the invoice payment: %w", err)
		}
		if payment.Invoice == nil || payment.Payment == nil || payment.Payment.PaymentIntent == nil {
			return handled{}, false, nil
		}
		row, err := tx.Invoice.Query().Where(invoice.StripeInvoiceID(payment.Invoice.ID)).Only(ctx)
		if ent.IsNotFound(err) {
			return handled{}, false, nil // a top-up's or a subscription's invoice
		}
		if err != nil {
			return handled{}, true, err
		}
		if err := tx.Invoice.UpdateOneID(row.ID).
			SetPaymentIntent(payment.Payment.PaymentIntent.ID).Exec(ctx); err != nil {
			return handled{}, true, fmt.Errorf("record the invoice's payment: %w", err)
		}
		return handled{status: stripeevent.StatusProcessed, accountID: row.AccountID}, true, nil

	case "credit_note.created":
		var note stripe.CreditNote
		if err := json.Unmarshal(event.Data.Raw, &note); err != nil {
			return handled{}, true, fmt.Errorf("decode the credit note: %w", err)
		}
		if note.Invoice == nil {
			return handled{}, false, nil
		}
		row, err := tx.Invoice.Query().Where(invoice.StripeInvoiceID(note.Invoice.ID)).Only(ctx)
		if ent.IsNotFound(err) {
			return handled{}, false, nil
		}
		if err != nil {
			return handled{}, true, err
		}
		h, err := w.creditNote(ctx, tx, row, note)
		return h, true, err
	}
	return handled{}, false, nil
}

// applied is what ApplyInvoice did, as the webhook records it: a refusal that waiting will not fix —
// not our invoice, not this customer's — is recorded and ignored rather than retried.
func applied(accountID int, credited bool, err error) (handled, error) {
	if err != nil {
		if isPermanent(err) {
			return ignored(err.Error()), nil
		}
		return handled{}, err
	}
	return handled{status: stripeevent.StatusProcessed, accountID: accountID, credited: credited}, nil
}

// ApplyInvoice brings a usage invoice up to what Stripe says of it, and moves money if it says the
// invoice was settled. Called by the webhook and by the operator routes that asked Stripe to settle
// one — the second call for the same invoice finds its key used and moves nothing. Reports the account
// and whether money arrived.
func ApplyInvoice(ctx context.Context, tx *ent.Tx, in IssuedInvoice, at time.Time) (int, bool, error) {
	row, account, err := invoiceFor(ctx, tx, in)
	if err != nil {
		return 0, false, err
	}
	credited := false

	update := tx.Invoice.UpdateOneID(row.ID).SetStripeSyncedAt(at)
	if row.StripeInvoiceID == nil && in.ID != "" {
		update = update.SetStripeInvoiceID(in.ID)
	}
	if in.Number != "" {
		update = update.SetNumber(in.Number)
	}
	if in.HostedURL != "" {
		update = update.SetHostedURL(in.HostedURL)
	}
	if in.PDFURL != "" {
		update = update.SetPdfURL(in.PDFURL)
	}
	if !in.DueAt.IsZero() {
		update = update.SetDueAt(in.DueAt)
	}

	switch in.Status {
	case "open":
		if row.Status == invoice.StatusDraft {
			update = update.SetStatus(invoice.StatusOpen).SetIssuedAt(at)
		}
	case "paid":
		if row.Status == invoice.StatusPaid {
			break
		}
		paid := in.AmountPaid * billing.CentMicros
		if paid > 0 {
			if _, err := wallet.PayInvoice(ctx, tx, wallet.SettleRequest{
				AccountID: account.ID, InvoiceID: row.ID, Amount: paid, At: at,
				Description: "Invoice " + orUID(in.Number, row) + " paid", StripeReference: in.ID,
				Key: "invoice-paid:" + in.ID,
			}); err != nil {
				return 0, false, err
			}
		}
		credited = paid > 0
		update = update.SetStatus(invoice.StatusPaid).SetPaidAt(at).
			SetAmountPaidMicros(row.AmountPaidMicros + paid).SetAmountRemainingMicros(0)
	case "void", "uncollectible":
		if row.Status == invoice.StatusVoid || row.Status == invoice.StatusUncollectible {
			break
		}
		if row.AmountRemainingMicros > 0 {
			if _, err := wallet.WriteOff(ctx, tx, wallet.SettleRequest{
				AccountID: account.ID, InvoiceID: row.ID, Amount: row.AmountRemainingMicros, At: at,
				Description:     "Invoice " + orUID(in.Number, row) + " " + in.Status + ": written off",
				StripeReference: in.ID, Key: "invoice-" + in.Status + ":" + in.ID,
			}); err != nil {
				return 0, false, err
			}
		}
		update = update.SetStatus(invoice.Status(in.Status)).SetAmountRemainingMicros(0)
	}
	if err := update.Exec(ctx); err != nil {
		return 0, false, fmt.Errorf("record the invoice: %w", err)
	}
	return account.ID, credited, ReassessOverdue(ctx, tx, account.ID, at)
}

// creditNote writes off what a credit note took off an open invoice.
//
// Only the part credited before payment. A credit note on an invoice already paid returns money — as a
// refund, as customer credit, or outside Stripe — and which of those it was, and whether it means the
// customer owed less or was simply overpaid, is a person's call: it is recorded and flagged, and the
// ledger corrected by hand (ADR 0045).
func (w *Webhooks) creditNote(ctx context.Context, tx *ent.Tx, row *ent.Invoice, note stripe.CreditNote) (handled, error) {
	if _, err := ApplyCreditNote(ctx, tx, row, CreditNote{ID: note.ID, AmountCents: note.PrePaymentAmount}, w.now()); err != nil {
		return handled{}, err
	}
	h := handled{status: stripeevent.StatusProcessed, accountID: row.AccountID}
	if note.PostPaymentAmount == 0 {
		return h, nil
	}
	w.log().Error("a credit note on a paid usage invoice needs a person",
		"credit_note", note.ID, "invoice", row.UID, "post_payment_cents", note.PostPaymentAmount)
	h.reason = fmt.Sprintf("%s credited after payment is not applied; adjust the ledger by hand",
		billing.USD(note.PostPaymentAmount*billing.CentMicros))
	return h, nil
}

// ApplyCreditNote writes off what a credit note took off an open invoice, once per note, and reports
// whether this call was the one that did.
func ApplyCreditNote(ctx context.Context, tx *ent.Tx, row *ent.Invoice, note CreditNote, at time.Time) (bool, error) {
	amount := note.AmountCents * billing.CentMicros
	if amount <= 0 {
		return false, nil
	}
	locked, err := tx.Invoice.Query().Where(invoice.ID(row.ID)).ForUpdate().Only(ctx)
	if err != nil {
		return false, err
	}
	result, err := wallet.WriteOff(ctx, tx, wallet.SettleRequest{
		AccountID: row.AccountID, InvoiceID: row.ID, Amount: min(amount, locked.AmountRemainingMicros),
		Description: "Credit note on invoice " + orUID(locked.Number, locked), StripeReference: note.ID,
		At: at, Key: "credit-note:" + note.ID,
	})
	if err != nil {
		return false, err
	}
	if result.Applied {
		if err := tx.Invoice.UpdateOneID(row.ID).
			SetAmountRemainingMicros(max(0, locked.AmountRemainingMicros-amount)).Exec(ctx); err != nil {
			return false, fmt.Errorf("record the credit note: %w", err)
		}
	}
	return result.Applied, ReassessOverdue(ctx, tx, row.AccountID, at)
}

// ReassessOverdue sets when an account's earliest unpaid invoice passed its due date, or clears it when
// none has — and the account's state with it (wallet.SetOverdue). Called whenever an invoice is paid or
// credited, and by the suspension job as due dates pass.
func ReassessOverdue(ctx context.Context, tx *ent.Tx, accountID int, at time.Time) error {
	earliest, err := tx.Invoice.Query().
		Where(invoice.AccountID(accountID), invoice.StatusEQ(invoice.StatusOpen),
			invoice.AmountRemainingMicrosGT(0), invoice.DueAtLT(at)).
		Order(ent.Asc(invoice.FieldDueAt)).
		First(ctx)
	var since *time.Time
	switch {
	case err == nil:
		since = earliest.DueAt
	case !ent.IsNotFound(err):
		return fmt.Errorf("find overdue invoices: %w", err)
	}
	_, err = wallet.SetOverdue(ctx, tx, accountID, since)
	return err
}

// invoiceFor finds the invoice an event names, locked, and checks it is really that account's: our
// metadata names it, and the customer must be the account's — the same test a payment passes
// (accountFor).
func invoiceFor(ctx context.Context, tx *ent.Tx, in IssuedInvoice) (*ent.Invoice, *ent.BillingAccount, error) {
	uid, err := uuid.Parse(in.Metadata[MetaInvoice])
	if err != nil {
		return nil, nil, ErrNotOurs
	}
	row, err := tx.Invoice.Query().Where(invoice.UID(uid)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, ErrNotOurs
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find the invoice: %w", err)
	}
	account, err := tx.BillingAccount.Query().Where(billingaccount.ID(row.AccountID)).Only(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("find the invoice's account: %w", err)
	}
	if account.UID.String() != in.Metadata[MetaAccount] || account.StripeCustomerID == nil ||
		in.Customer == "" || *account.StripeCustomerID != in.Customer {
		return nil, nil, ErrCustomerMismatch
	}
	if in.Currency != "" && !strings.EqualFold(in.Currency, row.Currency) {
		return nil, nil, fmt.Errorf("%w: %s, not %s", ErrWrongCurrency, in.Currency, row.Currency)
	}
	return row, account, nil
}

func orUID(number string, row *ent.Invoice) string {
	if number != "" {
		return number
	}
	return row.UID.String()[:8]
}
