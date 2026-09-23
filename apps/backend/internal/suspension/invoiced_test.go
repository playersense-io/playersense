package suspension_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/wallet"
)

// Invoiced accounts (ADR 0045): suspended for an invoice overdue past its grace, or for owing its
// credit limit billed or not — never for owing what has not yet been billed.

// invoiced puts the fixture on a contract with a limit, and a grace of 14 days after a due date.
func (f *fixture) invoiced(limit int64) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
		SetCollection(billingaccount.CollectionInvoiced).
		SetInvoicedThrough(f.now).
		SetEntitlementOverrides(map[string]int64{string(billing.CreditLimit): limit}).
		Exec(ctx); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Client.BillingContract.Create().SetAccountID(f.account.ID).
		SetCollection(billingcontract.CollectionInvoiced).SetEffectiveFrom(f.now.Add(-time.Hour)).
		SetOverdueGraceDays(14).SetCreditLimitMicros(limit).Save(ctx); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() {
		_ = f.db.Client.BillingAccount.UpdateOneID(f.account.ID).
			SetCollection(billingaccount.CollectionPrepaid).ClearOverdueSince().
			SetState(billingaccount.StateActive).Exec(context.Background())
	})
}

// openInvoice is an invoice for `amount`, due at `due`.
func (f *fixture) openInvoice(amount int64, due time.Time) *ent.Invoice {
	f.t.Helper()
	row, err := f.db.Client.Invoice.Create().
		SetAccountID(f.account.ID).SetPeriodStart(due.Add(-60 * 24 * time.Hour)).SetPeriodEnd(due.Add(-30 * 24 * time.Hour)).
		SetLedgerFromID(0).SetLedgerToID(0).SetTotalMicros(amount).SetAmountRemainingMicros(amount).
		SetStatus(invoice.StatusOpen).SetDueAt(due).SetIssueKey("test-" + uuid.NewString()).
		Save(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fixture) settle(row *ent.Invoice, amount int64) {
	f.t.Helper()
	tx, err := f.db.Client.Tx(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := wallet.PayInvoice(context.Background(), tx, wallet.SettleRequest{
		AccountID: f.account.ID, InvoiceID: row.ID, Amount: amount, Key: "paid:" + uuid.NewString(),
	}); err != nil {
		_ = tx.Rollback()
		f.t.Fatal(err)
	}
	if err := tx.Invoice.UpdateOneID(row.ID).SetStatus(invoice.StatusPaid).SetAmountRemainingMicros(0).
		Exec(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) invoicedJob() *suspension.Job {
	j := f.job()
	j.OverdueGrace = 14 * 24 * time.Hour
	return j
}

func (f *fixture) runInvoiced() {
	f.t.Helper()
	if _, err := f.invoicedJob().Run(context.Background()); err != nil {
		f.t.Logf("pass: %v", err)
	}
}

// A month of usage not yet invoiced — and weeks past ADR 0043's seven days — is not a debt.
func TestAnInvoicedAccountIsNotSuspendedForWhatIsNotYetDue(t *testing.T) {
	f := newFixture(t)
	f.invoiced(10_000 * billing.Micros)
	f.owe(3_000 * billing.Micros)
	f.now = f.now.Add(40 * 24 * time.Hour)
	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateActive {
		t.Errorf("state %s; unbilled usage under the limit is not a debt", got)
	}
}

// An invoice past its due date starts the grace; past the grace, ingestion stops; paying lets it back.
func TestAnOverdueInvoiceSuspendsAfterItsGrace(t *testing.T) {
	f := newFixture(t)
	f.invoiced(10_000 * billing.Micros)
	f.owe(500 * billing.Micros)
	row := f.openInvoice(500*billing.Micros, f.now.Add(-3*24*time.Hour))

	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateGracePeriod {
		t.Fatalf("state %s three days overdue; want grace_period", got)
	}
	f.now = f.now.Add(12 * 24 * time.Hour)
	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateSuspended {
		t.Fatalf("state %s fifteen days overdue; want suspended", got)
	}
	if !f.gate().Suspended(context.Background(), uint32(f.account.OrganizationID)) {
		t.Error("the gate does not know it is suspended")
	}

	f.settle(row, 500*billing.Micros)
	tx, _ := f.db.Client.Tx(context.Background())
	if _, err := wallet.SetOverdue(context.Background(), tx, f.account.ID, nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateActive {
		t.Errorf("state %s once paid; want active", got)
	}
}

// Owing its limit, billed or not, suspends an invoiced account at once — and it is let back in once
// what it owes is under the limit again.
func TestAnInvoicedAccountOverItsLimitIsSuspendedAndLetBackIn(t *testing.T) {
	f := newFixture(t)
	f.invoiced(1_000 * billing.Micros)
	f.owe(1_200 * billing.Micros)

	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateSuspended {
		t.Fatalf("state %s owing $1,200 against $1,000; want suspended", got)
	}
	// Paying an invoice for part of it — or staff raising the limit — lets it back in.
	row := f.openInvoice(600*billing.Micros, f.now.Add(10*24*time.Hour))
	f.settle(row, 600*billing.Micros)
	f.runInvoiced()
	if got := f.state(); got != billingaccount.StateActive {
		t.Errorf("state %s owing $600 against $1,000; want active", got)
	}
}
