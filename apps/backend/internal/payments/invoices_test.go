package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/payments"
)

// PlayerSense's own usage invoices (ADR 0045): settled by Stripe's word, once, and only these.

// usageInvoice makes the fixture's account invoiced, owing `amount` on one open invoice due at `due`.
func (f *fixture) usageInvoice(amount int64, due time.Time) *ent.Invoice {
	f.t.Helper()
	ctx := context.Background()
	if err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetCollection(billingaccount.CollectionInvoiced).Exec(ctx); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		_ = f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
			SetCollection(billingaccount.CollectionPrepaid).Exec(context.Background())
	})
	f.owe(amount)
	start := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	row, err := f.db.Client.Invoice.Create().
		SetAccountID(f.account.ID).SetPeriodStart(start).SetPeriodEnd(start.AddDate(0, 1, 0)).
		SetLedgerFromID(0).SetLedgerToID(0).SetTotalMicros(amount).SetAmountRemainingMicros(amount).
		SetStatus(invoice.StatusOpen).SetDueAt(due).SetNumber("PS-0001").
		SetStripeInvoiceID("in_" + uuid.NewString()[:12]).
		SetIssueKey("playersense-invoice-" + uuid.NewString()).
		Save(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

// invoiceEvent is a usage invoice as Stripe sends it.
func (f *fixture) invoiceEvent(kind string, row *ent.Invoice, status string, paidCents int64) payments.Outcome {
	f.t.Helper()
	out, err := f.deliver("evt_"+uuid.NewString(), kind, map[string]any{
		"id": *row.StripeInvoiceID, "object": "invoice", "status": status, "currency": "usd",
		"customer": f.customer, "number": "PS-0001", "amount_paid": paidCents,
		"hosted_invoice_url": "https://invoice.stripe.test/" + *row.StripeInvoiceID,
		"metadata": map[string]string{
			payments.MetaAccount: f.account.UID.String(), payments.MetaKind: payments.KindUsageInvoice,
			payments.MetaInvoice: row.UID.String(),
		},
	})
	if err != nil {
		f.t.Fatalf("%s: %v", kind, err)
	}
	return out
}

func (f *fixture) invoice(row *ent.Invoice) *ent.Invoice {
	f.t.Helper()
	fresh, err := f.db.Client.Invoice.Get(context.Background(), row.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return fresh
}

func (f *fixture) payments() int {
	f.t.Helper()
	n, err := f.db.Client.LedgerEntry.Query().Where(ledgerentry.AccountID(f.account.ID),
		ledgerentry.KindEQ(ledgerentry.KindInvoicePayment)).Count(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// Paid is reported by invoice.paid and invoice.payment_succeeded, each delivered more than once: the
// receivable falls once.
func TestAPaidUsageInvoiceSettlesItsReceivableOnce(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(120*billing.Micros, time.Now().Add(30*24*time.Hour))

	first := f.invoiceEvent("invoice.paid", row, "paid", 12_000)
	f.invoiceEvent("invoice.paid", row, "paid", 12_000)
	f.invoiceEvent("invoice.payment_succeeded", row, "paid", 12_000)

	if first.Status != "processed" || !first.Credited {
		t.Errorf("first delivery %+v; want processed and credited", first)
	}
	if got := f.row().ReceivableMicros; got != 0 || f.payments() != 1 {
		t.Errorf("receivable %s after %d payments; want settled once", billing.USD(got), f.payments())
	}
	if fresh := f.invoice(row); fresh.Status != invoice.StatusPaid || fresh.AmountRemainingMicros != 0 ||
		fresh.AmountPaidMicros != 120*billing.Micros || fresh.PaidAt == nil {
		t.Errorf("invoice after payment: %+v", fresh)
	}
	f.assertInvariant()
}

// An event delivered late cannot undo a newer one: a `finalized` after `paid` leaves it paid.
func TestALateEventCannotReopenAPaidInvoice(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(50*billing.Micros, time.Now().Add(30*24*time.Hour))
	f.invoiceEvent("invoice.paid", row, "paid", 5_000)
	f.invoiceEvent("invoice.finalized", row, "open", 0)
	if fresh := f.invoice(row); fresh.Status != invoice.StatusPaid {
		t.Errorf("status %s after a late finalized; want paid", fresh.Status)
	}
}

// A credit note on an open invoice lowers what is owed, once; a voided invoice writes off the rest.
func TestACreditNoteAndAVoidLowerWhatIsOwed(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(100*billing.Micros, time.Now().Add(30*24*time.Hour))
	note := map[string]any{
		"id": "cn_" + uuid.NewString()[:12], "object": "credit_note", "invoice": *row.StripeInvoiceID,
		"amount": 2_000, "pre_payment_amount": 2_000, "post_payment_amount": 0, "currency": "usd",
	}
	for range 2 {
		if _, err := f.deliver("evt_"+uuid.NewString(), "credit_note.created", note); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.row().ReceivableMicros; got != 80*billing.Micros {
		t.Errorf("receivable %s after a $20 credit note, want $80", billing.USD(got))
	}
	if fresh := f.invoice(row); fresh.AmountRemainingMicros != 80*billing.Micros {
		t.Errorf("remaining %s, want $80", billing.USD(fresh.AmountRemainingMicros))
	}

	f.invoiceEvent("invoice.voided", row, "void", 0)
	if got := f.row().ReceivableMicros; got != 0 {
		t.Errorf("receivable %s after the void, want nothing", billing.USD(got))
	}
	if fresh := f.invoice(row); fresh.Status != invoice.StatusVoid {
		t.Errorf("status %s, want void", fresh.Status)
	}
	f.assertInvariant()
}

// An invoice naming this account but paid by another customer moves nothing.
func TestAUsageInvoiceOfAnotherCustomerIsIgnored(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(30*billing.Micros, time.Now().Add(24*time.Hour))
	f.customer = "cus_somebody_else"
	out := f.invoiceEvent("invoice.paid", row, "paid", 3_000)
	if out.Status != "ignored" || f.row().ReceivableMicros != 30*billing.Micros {
		t.Errorf("%+v, receivable %s; want ignored and nothing settled", out, billing.USD(f.row().ReceivableMicros))
	}
}

// Past its due date an invoice makes the account overdue; paying it ends that. A dispute of the
// payment puts it back on the receivable and the account overdue again; winning settles it again.
func TestADisputedInvoicePaymentIsOwedAgainUntilItIsWon(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(75*billing.Micros, time.Now().Add(-48*time.Hour))
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := payments.ReassessOverdue(context.Background(), tx, f.account.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if account := f.row(); account.OverdueSince == nil || account.State != billingaccount.StateGracePeriod {
		t.Fatalf("overdue %v, state %s; want overdue and in grace", account.OverdueSince, account.State)
	}

	pi := "pi_" + uuid.NewString()[:12]
	if _, err := f.deliver("evt_"+uuid.NewString(), "invoice_payment.paid", map[string]any{
		"id": "inpay_" + uuid.NewString()[:8], "object": "invoice_payment", "invoice": *row.StripeInvoiceID,
		"amount_paid": 7_500, "currency": "usd", "status": "paid",
		"payment": map[string]any{"type": "payment_intent", "payment_intent": pi},
	}); err != nil {
		t.Fatal(err)
	}
	f.invoiceEvent("invoice.paid", row, "paid", 7_500)
	if account := f.row(); account.OverdueSince != nil || account.State != billingaccount.StateActive {
		t.Fatalf("after paying: overdue %v, state %s; want neither", account.OverdueSince, account.State)
	}

	id := "dp_" + uuid.NewString()[:12]
	f.dispute("charge.dispute.created", pi, id, "needs_response", 7_500)
	if account := f.row(); account.ReceivableMicros != 75*billing.Micros || account.OverdueSince == nil {
		t.Errorf("after the dispute: receivable %s, overdue %v; want owed and overdue again",
			billing.USD(account.ReceivableMicros), account.OverdueSince)
	}
	if fresh := f.invoice(row); fresh.Status != invoice.StatusOpen || fresh.AmountRemainingMicros != 75*billing.Micros {
		t.Errorf("invoice after the dispute: %s, remaining %s", fresh.Status, billing.USD(fresh.AmountRemainingMicros))
	}

	f.dispute("charge.dispute.closed", pi, id, "won", 7_500)
	if account := f.row(); account.ReceivableMicros != 0 || account.OverdueSince != nil ||
		account.State != billingaccount.StateActive {
		t.Errorf("after winning: receivable %s, overdue %v, state %s",
			billing.USD(account.ReceivableMicros), account.OverdueSince, account.State)
	}
	if fresh := f.invoice(row); fresh.Status != invoice.StatusPaid {
		t.Errorf("invoice after winning: %s, want paid", fresh.Status)
	}
	f.assertInvariant()
}

// A refund of an invoice payment is flagged for a person, not applied.
func TestARefundOfAnInvoicePaymentIsFlaggedNotApplied(t *testing.T) {
	f := newFixture(t)
	row := f.usageInvoice(40*billing.Micros, time.Now().Add(24*time.Hour))
	pi := "pi_" + uuid.NewString()[:12]
	if err := f.db.Client.Invoice.UpdateOneID(row.ID).SetPaymentIntent(pi).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.invoiceEvent("invoice.paid", row, "paid", 4_000)

	out := f.refund(pi, "re_"+uuid.NewString()[:12], "succeeded", 4_000)

	if out.Reason == "" || f.row().ReceivableMicros != 0 {
		t.Errorf("%+v, receivable %s; want recorded with a reason and nothing moved", out,
			billing.USD(f.row().ReceivableMicros))
	}
}
