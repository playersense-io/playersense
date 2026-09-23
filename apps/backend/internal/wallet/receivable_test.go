package wallet_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// An invoiced account's receivable (ADR 0045): owed and not yet due, so never arrears — billed on an
// invoice, and settled only by something that names the invoice.

// invoiced makes the fixture's account a contracted one.
func (a *account) invoiced(t *testing.T) {
	t.Helper()
	if err := a.db.Client.BillingAccount.UpdateOneID(a.id).
		SetCollection(billingaccount.CollectionInvoiced).Exec(context.Background()); err != nil {
		t.Fatalf("make the account invoiced: %v", err)
	}
}

// invoice is a row for settlements to name; what it bills does not matter to the wallet.
func (a *account) invoice(t *testing.T) *ent.Invoice {
	t.Helper()
	start := time.Now().UTC().Truncate(time.Hour)
	row, err := a.db.Client.Invoice.Create().
		SetAccountID(a.id).SetPeriodStart(start).SetPeriodEnd(start.Add(time.Hour)).
		SetLedgerFromID(0).SetLedgerToID(0).SetTotalMicros(0).
		SetIssueKey("test-invoice-" + uuid.NewString()).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create an invoice: %v", err)
	}
	return row
}

// in runs one wallet function in its own transaction.
func in[T any](t *testing.T, a *account, fn func(context.Context, *ent.Tx) (T, error)) T {
	t.Helper()
	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	out, err := fn(context.Background(), tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("%v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return out
}

func (a *account) settle(t *testing.T,
	fn func(context.Context, *ent.Tx, wallet.SettleRequest) (*wallet.SettleResult, error),
	invoice *ent.Invoice, amount int64, key string,
) *wallet.SettleResult {
	t.Helper()
	return in(t, a, func(ctx context.Context, tx *ent.Tx) (*wallet.SettleResult, error) {
		return fn(ctx, tx, wallet.SettleRequest{
			AccountID: a.id, InvoiceID: invoice.ID, Amount: amount, Key: a.key(key),
		})
	})
}

// Credit is still spent first, in the usual order; what it cannot cover is owed on the next invoice,
// not overdue — so the account stays active.
func TestInvoicedUsageCreditCannotCoverIsAReceivable(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.grant(t, wallet.GrantRequest{Kind: wallet.Promo, Amount: dollars(5), Key: a.key("promo")})
	may := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)

	result := in(t, a, func(ctx context.Context, tx *ent.Tx) (*wallet.UsageResult, error) {
		return wallet.ChargeUsage(ctx, tx, wallet.UsageRequest{AccountID: a.id, Amount: dollars(12),
			At: time.Now(), PeriodStart: may, Key: a.key("u1")})
	})

	row := a.row(t)
	if result.Owed != dollars(7) || row.ReceivableMicros != dollars(7) || row.ArrearsMicros != 0 {
		t.Errorf("owed %s, receivable %s, arrears %s; want $7 receivable and no arrears",
			billing.USD(result.Owed), billing.USD(row.ReceivableMicros), billing.USD(row.ArrearsMicros))
	}
	if row.State != billingaccount.StateActive {
		t.Errorf("state %s; a receivable not yet due is not a debt", row.State)
	}
	if result.Entry.ReceivableMicros != dollars(7) || result.Entry.PeriodStart == nil ||
		!result.Entry.PeriodStart.Equal(may) {
		t.Errorf("entry receivable %s, period %v; want $7 for May",
			billing.USD(result.Entry.ReceivableMicros), result.Entry.PeriodStart)
	}
	a.assertInvariant(t)
}

// A payment settles what it can; a payment beyond the receivable is the customer's money, and becomes
// purchased credit rather than disappearing. Replayed, it pays once.
func TestPayingAnInvoiceSettlesTheReceivableAndKeepsTheRest(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.usage(t, dollars(70), "u1")
	invoice := a.invoice(t)

	paid := a.settle(t, wallet.PayInvoice, invoice, dollars(100), "paid")
	again := a.settle(t, wallet.PayInvoice, invoice, dollars(100), "paid")

	if paid.Settled != dollars(70) || paid.Excess != dollars(30) || !paid.Applied || again.Applied {
		t.Errorf("settled %s, excess %s, applied %v then %v", billing.USD(paid.Settled),
			billing.USD(paid.Excess), paid.Applied, again.Applied)
	}
	row := a.row(t)
	if row.ReceivableMicros != 0 {
		t.Errorf("receivable %s after paying it", billing.USD(row.ReceivableMicros))
	}
	lots, _ := a.db.Client.CreditLot.Query().Where(creditlot.AccountID(a.id)).All(context.Background())
	if len(lots) != 1 || lots[0].Kind != creditlot.KindPurchase || lots[0].RemainingMicros != dollars(30) {
		t.Errorf("lots %+v; want the $30 overpayment as purchased credit", lots)
	}
	if paid.Entry.InvoiceID == nil || *paid.Entry.InvoiceID != invoice.ID {
		t.Error("the payment does not name the invoice it settles")
	}
	a.assertInvariant(t)
}

// The platform fee and the commitment go straight to the receivable: a promotional credit is a
// discount on usage, not on the contract.
func TestAContractChargeIsNeverPaidFromCredit(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.grant(t, wallet.GrantRequest{Kind: wallet.Promo, Amount: dollars(100), Key: a.key("promo")})

	_, charged := in2(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.LedgerEntry, bool, error) {
		return wallet.Accrue(ctx, tx, wallet.AccrueRequest{AccountID: a.id, Kind: "subscription",
			Amount: dollars(50), Description: "Platform fee", Key: a.key("fee")})
	})
	_, replayed := in2(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.LedgerEntry, bool, error) {
		return wallet.Accrue(ctx, tx, wallet.AccrueRequest{AccountID: a.id, Kind: "subscription",
			Amount: dollars(50), Description: "Platform fee", Key: a.key("fee")})
	})

	if !charged || replayed {
		t.Errorf("charged %v, replayed %v; want once", charged, replayed)
	}
	if got := a.available(t, time.Now()); got != dollars(100) {
		t.Errorf("available %s; the fee must not touch credit", billing.USD(got))
	}
	if row := a.row(t); row.ReceivableMicros != dollars(50) {
		t.Errorf("receivable %s, want $50", billing.USD(row.ReceivableMicros))
	}
	a.assertInvariant(t)
}

// A write-off lowers what is owed and never becomes credit — nothing was paid to give back.
func TestAWriteOffNeverBecomesCredit(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.usage(t, dollars(5), "u1")

	result := a.settle(t, wallet.WriteOff, a.invoice(t), dollars(8), "note")

	if result.Settled != dollars(5) || a.row(t).ReceivableMicros != 0 {
		t.Errorf("settled %s, receivable %s", billing.USD(result.Settled), billing.USD(a.row(t).ReceivableMicros))
	}
	if n, _ := a.db.Client.CreditLot.Query().Where(creditlot.AccountID(a.id)).Count(context.Background()); n != 0 {
		t.Errorf("%d lots; a write-off made credit", n)
	}
	a.assertInvariant(t)
}

// A dispute takes a payment back, so what it paid is owed again.
func TestADisputedPaymentIsOwedAgain(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.usage(t, dollars(40), "u1")
	invoice := a.invoice(t)
	a.settle(t, wallet.PayInvoice, invoice, dollars(40), "paid")

	a.settle(t, wallet.Reopen, invoice, dollars(40), "dispute")

	if row := a.row(t); row.ReceivableMicros != dollars(40) {
		t.Errorf("receivable %s, want $40 owed again", billing.USD(row.ReceivableMicros))
	}
	a.assertInvariant(t)
}

// Arrears carried into invoicing are the same debt, billed rather than overdue: the balance does not
// move, and the grace period ends.
func TestCarryingArrearsMovesTheDebtNotTheBalance(t *testing.T) {
	a := newAccount(t)
	a.usage(t, dollars(9), "u1")
	before := a.row(t)
	if before.State != billingaccount.StateGracePeriod {
		t.Fatalf("state %s, want grace_period while it owes", before.State)
	}
	a.invoiced(t)

	in(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.LedgerEntry, error) {
		return wallet.CarryArrears(ctx, tx, a.id, a.key("carry"))
	})

	after := a.row(t)
	if after.ArrearsMicros != 0 || after.ReceivableMicros != dollars(9) || after.BalanceMicros != before.BalanceMicros {
		t.Errorf("arrears %s, receivable %s, balance %s → %s", billing.USD(after.ArrearsMicros),
			billing.USD(after.ReceivableMicros), billing.USD(before.BalanceMicros), billing.USD(after.BalanceMicros))
	}
	if after.State != billingaccount.StateActive {
		t.Errorf("state %s; carried debt is not overdue", after.State)
	}
	a.assertInvariant(t)
}

// Overdue, not owing, starts the grace period; and a suspended invoiced account is let back in by the
// suspension job, which checks its limit too — not by the invoice being paid.
func TestOverdueStartsTheGracePeriodAndPayingEndsIt(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	since := time.Now().Add(-time.Hour)

	account := in(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.BillingAccount, error) {
		return wallet.SetOverdue(ctx, tx, a.id, &since)
	})
	if account.State != billingaccount.StateGracePeriod || a.row(t).OverdueSince == nil {
		t.Fatalf("state %s; want grace_period once an invoice is overdue", account.State)
	}
	account = in(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.BillingAccount, error) {
		return wallet.SetOverdue(ctx, tx, a.id, nil)
	})
	if account.State != billingaccount.StateActive {
		t.Errorf("state %s; want active once nothing is overdue", account.State)
	}

	if err := a.db.Client.BillingAccount.UpdateOneID(a.id).
		SetState(billingaccount.StateSuspended).SetOverdueSince(since).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	account = in(t, a, func(ctx context.Context, tx *ent.Tx) (*ent.BillingAccount, error) {
		return wallet.SetOverdue(ctx, tx, a.id, nil)
	})
	if account.State != billingaccount.StateSuspended {
		t.Errorf("state %s; a suspended invoiced account is let back in by the job", account.State)
	}
}

// A volume credit takes usage off the next invoice rather than making credit, while there is any to
// take off.
func TestAnInvoicedUsageCreditLowersTheReceivable(t *testing.T) {
	a := newAccount(t)
	a.invoiced(t)
	a.usage(t, dollars(10), "u1")

	a.usageMovement(t, wallet.CreditUsage, dollars(4), "credit")

	if row := a.row(t); row.ReceivableMicros != dollars(6) {
		t.Errorf("receivable %s, want $6", billing.USD(row.ReceivableMicros))
	}
	if n, _ := a.db.Client.CreditLot.Query().Where(creditlot.AccountID(a.id)).Count(context.Background()); n != 0 {
		t.Errorf("%d lots; the credit should have come off the receivable", n)
	}
	a.assertInvariant(t)
}

// An invoiced account never asks for an automatic top-up, however far below zero its balance is.
func TestAnInvoicedAccountNeverWantsATopUp(t *testing.T) {
	customer := "cus_x"
	account := &ent.BillingAccount{
		AutoTopupEnabled: true, StripeCustomerID: &customer, AutoTopupThresholdMicros: dollars(10),
		Collection: billingaccount.CollectionInvoiced, State: billingaccount.StateActive,
	}
	if wallet.WantsTopUp(account, -dollars(5000)) {
		t.Error("an invoiced account asked for a top-up")
	}
	account.Collection = billingaccount.CollectionPrepaid
	if !wallet.WantsTopUp(account, -dollars(5000)) {
		t.Error("the same prepaid account did not")
	}
}

func in2[A, B any](t *testing.T, a *account, fn func(context.Context, *ent.Tx) (A, B, error)) (A, B) {
	t.Helper()
	var second B
	first := in(t, a, func(ctx context.Context, tx *ent.Tx) (A, error) {
		x, y, err := fn(ctx, tx)
		second = y
		return x, err
	})
	return first, second
}
