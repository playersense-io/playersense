package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"muzak.dev/framework/testclient"
	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/auditevent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments/paymentstest"
	"playersense.dev/backend/internal/wallet"
)

// Contracted accounts (ADR 0045), end to end: staff put an organization on a contract, its month
// closes into an invoice, the customer reads it, and staff credit part of it and record the wire that
// paid the rest.

type invoiceOut struct {
	ID                    uuid.UUID `json:"id"`
	Status                string    `json:"status"`
	TotalMicros           int64     `json:"total_micros"`
	AmountRemainingMicros int64     `json:"amount_remaining_micros"`
	AmountPaidMicros      int64     `json:"amount_paid_micros"`
	HostedURL             string    `json:"hosted_url"`
	PONumber              string    `json:"po_number"`
	Lines                 []struct {
		Kind        string `json:"kind"`
		AmountCents int64  `json:"amount_cents"`
	} `json:"lines"`
}

func (w *money) contract(t *testing.T, body map[string]any) *testclient.Response {
	t.Helper()
	body["operator"] = "ada@playersense.dev"
	return w.operator.Put(w.admin()+"/contract", testclient.JSON(body))
}

func (w *money) billingAccount(t *testing.T) *ent.BillingAccount {
	t.Helper()
	org := w.organization(t, w.slug)
	row, err := w.db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(org.ID)).Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (w *money) audited(t *testing.T, action audit.Action) int {
	t.Helper()
	org := w.organization(t, w.slug)
	n, err := w.db.Client.AuditEvent.Query().
		Where(auditevent.OrganizationID(org.ID), auditevent.Action(string(action))).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAnInvoicedContractFromContractToPaidInvoice(t *testing.T) {
	stripe := paymentstest.New()
	w := newWalletWith(t, stripe)
	ctx := context.Background()

	// A contract needs a limit and somewhere to send the invoices.
	w.contract(t, map[string]any{"collection": "invoiced", "billing_email": "finance@studio.example"}).
		AssertStatus(http.StatusUnprocessableEntity)

	w.contract(t, map[string]any{
		"collection": "invoiced", "platform_fee_micros": dollars(300), "credit_limit_micros": dollars(10_000),
		"billing_email": "finance@studio.example", "po_number": "PO-4471",
	}).AssertStatus(http.StatusOK)
	account := w.billingAccount(t)
	t.Cleanup(func() {
		_ = w.db.Client.BillingAccount.UpdateOneID(account.ID).
			SetCollection(billingaccount.CollectionPrepaid).Exec(context.Background())
	})
	if account.Collection != billingaccount.CollectionInvoiced || account.InvoicedThrough == nil ||
		account.StripeCustomerID == nil || stripe.BillingEmails[*account.StripeCustomerID] != "finance@studio.example" {
		t.Fatalf("after the contract: %+v", account)
	}

	// Paying by card is refused: the organization pays its invoices.
	w.client(t).Post(w.base()+"/billing/top-ups", testclient.JSON(map[string]any{"amount_micros": dollars(50)})).
		AssertStatus(http.StatusConflict)
	w.client(t).Put(w.base()+"/billing/auto-topup", testclient.JSON(map[string]any{
		"enabled": true, "threshold_micros": dollars(10), "amount_micros": dollars(50),
	})).AssertStatus(http.StatusConflict)
	w.client(t).Post(w.base()+"/billing/subscription", testclient.JSON(map[string]any{
		"plan": "enterprise", "interval": "month",
	})).AssertStatus(http.StatusConflict)

	// A month of players, then its close — as the billing writer would, the morning after.
	month := invoicing.MonthOf(*account.InvoicedThrough)
	tx, err := w.db.Client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wallet.ChargeUsage(ctx, tx, wallet.UsageRequest{AccountID: account.ID, Amount: dollars(120),
		At: time.Now(), PeriodStart: month, Key: "usage:" + uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	job := &invoicing.Job{DB: w.db, Gateway: stripe,
		Now: func() time.Time { return month.AddDate(0, 1, 0).Add(3 * time.Hour) }}
	if n, err := job.CloseAccount(ctx, account.ID); err != nil || n != 1 {
		t.Fatalf("closed %d, %v", n, err)
	}
	if _, err := job.Run(ctx); err != nil {
		t.Logf("pass: %v", err) // other tests' drafts in the shared database are not this test's
	}

	var listed struct {
		Invoices []invoiceOut `json:"invoices"`
	}
	w.client(t).Get(w.base() + "/billing/invoices").AssertStatus(http.StatusOK).JSON(&listed)
	if len(listed.Invoices) != 1 {
		t.Fatalf("%d invoices, want the month's", len(listed.Invoices))
	}
	inv := listed.Invoices[0]
	if inv.Status != "open" || inv.HostedURL == "" || inv.PONumber != "PO-4471" || len(inv.Lines) < 2 {
		t.Fatalf("invoice %+v", inv)
	}
	w.client(t).Get(w.base() + "/billing/invoices/" + inv.ID.String()).AssertStatus(http.StatusOK)

	// Staff credit $20 of it, then record the wire for the rest.
	path := w.admin() + "/invoices/" + inv.ID.String()
	var credited invoiceOut
	w.operator.Post(path+"/credit-notes", testclient.JSON(map[string]any{
		"amount_micros": dollars(20), "reason": "Outage on the 12th", "reference": unique("cn"),
		"operator": "ada@playersense.dev",
	})).AssertStatus(http.StatusOK).JSON(&credited)
	if credited.AmountRemainingMicros != inv.TotalMicros-dollars(20) {
		t.Errorf("remaining %d after a $20 credit, want %d", credited.AmountRemainingMicros, inv.TotalMicros-dollars(20))
	}

	reference := unique("wire")
	var paid invoiceOut
	w.operator.Post(path+"/mark-paid", testclient.JSON(map[string]any{
		"reference": reference, "operator": "ada@playersense.dev",
	})).AssertStatus(http.StatusOK).JSON(&paid)
	w.operator.Post(path+"/mark-paid", testclient.JSON(map[string]any{
		"reference": reference, "operator": "ada@playersense.dev",
	})).AssertStatus(http.StatusOK)
	if paid.Status != "paid" || paid.AmountRemainingMicros != 0 {
		t.Errorf("after marking paid: %+v", paid)
	}
	// All that is still owed is what the invoice carried below a cent, for the next one.
	row, err := w.db.Client.Invoice.Query().Where(invoice.UID(inv.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.billingAccount(t).ReceivableMicros; got != row.CarryOutMicros {
		t.Errorf("receivable %d after paying the only invoice, want its %d-micro carry", got, row.CarryOutMicros)
	}
	if n := len(stripe.PaidOutOfBand); n != 1 {
		t.Errorf("marked paid at Stripe %d times, want once", n)
	}

	// Back to prepaid: the contract changes, the invoices stay.
	w.contract(t, map[string]any{"collection": "prepaid"}).AssertStatus(http.StatusOK)
	if got := w.billingAccount(t).Collection; got != billingaccount.CollectionPrepaid {
		t.Errorf("collection %s after moving back", got)
	}

	for action, want := range map[audit.Action]int{
		audit.ContractSet: 2, audit.InvoiceCredited: 1, audit.InvoicePaid: 1,
	} {
		if got := w.audited(t, action); got != want {
			t.Errorf("%s logged %d times, want %d", action, got, want)
		}
	}
	// Nobody but staff.
	w.client(t).Put(w.admin()+"/contract", testclient.JSON(map[string]any{
		"collection": "prepaid", "operator": "x@y.z",
	})).AssertStatus(http.StatusUnauthorized)
}

// A live subscription and a contract would be two bills for one plan.
func TestASubscribedOrganizationCannotBeInvoiced(t *testing.T) {
	w := newWallet(t)
	status := "active"
	if err := w.db.Client.BillingAccount.UpdateOneID(w.billingAccount(t).ID).
		SetSubscriptionStatus(status).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.contract(t, map[string]any{
		"collection": "invoiced", "credit_limit_micros": dollars(1_000), "billing_email": "f@studio.example",
	}).AssertStatus(http.StatusConflict)
}
