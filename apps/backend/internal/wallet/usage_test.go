package wallet_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// Usage cannot be refused (ADR 0039): the players were served before the charge was priced. These
// tests are the rules that follow — take what credit there is in the usual order, owe the rest, pay
// the debt before anything else, and never let two movements erase each other.

func (a *account) usage(t *testing.T, amount int64, key string) *wallet.UsageResult {
	t.Helper()
	return a.usageMovement(t, wallet.ChargeUsage, amount, key)
}

func (a *account) usageMovement(t *testing.T,
	move func(context.Context, *ent.Tx, wallet.UsageRequest) (*wallet.UsageResult, error),
	amount int64, key string,
) *wallet.UsageResult {
	t.Helper()
	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	result, err := move(context.Background(), tx, wallet.UsageRequest{
		AccountID: a.id, Amount: amount, Description: "usage", At: time.Now(), Key: a.key(key),
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("usage movement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return result
}

func (a *account) row(t *testing.T) *ent.BillingAccount {
	t.Helper()
	row, err := a.db.Client.BillingAccount.Get(context.Background(), a.id)
	if err != nil {
		t.Fatalf("read the account: %v", err)
	}
	return row
}

// assertInvariant is the one equation the wallet rests on: the ledger's running total is what the
// lots hold minus what is owed — arrears, and an invoiced account's receivable. Checked after every
// scenario, because every bug in this area shows up as this equation failing.
func (a *account) assertInvariant(t *testing.T) {
	t.Helper()
	row := a.row(t)
	lots, err := a.db.Client.CreditLot.Query().Where(creditlot.AccountID(a.id)).All(context.Background())
	if err != nil {
		t.Fatalf("read lots: %v", err)
	}
	var held int64
	for _, lot := range lots {
		held += lot.RemainingMicros
	}
	if row.BalanceMicros != held-row.ArrearsMicros-row.ReceivableMicros {
		t.Errorf("balance %s ≠ lots %s − arrears %s − receivable %s", billing.USD(row.BalanceMicros),
			billing.USD(held), billing.USD(row.ArrearsMicros), billing.USD(row.ReceivableMicros))
	}
}

// Promo first, then purchases oldest first — the order every spend uses, now for usage too.
func TestUsageSpendsPromoFirstThenTopUpsOldestFirst(t *testing.T) {
	a := newAccount(t)
	now := time.Now()
	older := a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(10),
		GrantedAt: now.Add(-48 * time.Hour), Key: a.key("old")})
	newer := a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(10),
		GrantedAt: now.Add(-time.Hour), Key: a.key("new")})
	promo := a.grant(t, wallet.GrantRequest{Kind: wallet.Promo, Amount: dollars(5), Key: a.key("promo")})

	result := a.usage(t, dollars(12), "u1")

	want := []wallet.Draw{
		{LotID: promo.ID, Kind: wallet.Promo, Amount: dollars(5)},
		{LotID: older.ID, Kind: wallet.Purchase, Amount: dollars(7)},
	}
	if len(result.Draws) != len(want) {
		t.Fatalf("draws = %+v, want %+v", result.Draws, want)
	}
	for i := range want {
		if result.Draws[i] != want[i] {
			t.Errorf("draw %d = %+v, want %+v", i, result.Draws[i], want[i])
		}
	}
	if result.Owed != 0 {
		t.Errorf("owed %s with enough credit", billing.USD(result.Owed))
	}
	_ = newer
	a.assertInvariant(t)
}

// An empty wallet does not refuse usage and does not lose it: the charge is recorded whole and the
// shortfall becomes arrears, which moves the account into its grace period.
func TestUsageTheWalletCannotCoverBecomesArrears(t *testing.T) {
	a := newAccount(t)
	a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(3), Key: a.key("topup")})

	result := a.usage(t, dollars(10), "u1")

	if result.Owed != dollars(7) {
		t.Errorf("owed %s, want $7", billing.USD(result.Owed))
	}
	if result.Entry.AmountMicros != -dollars(10) {
		t.Errorf("the ledger recorded %s; usage is recorded whole", billing.USD(result.Entry.AmountMicros))
	}
	row := a.row(t)
	if row.ArrearsMicros != dollars(7) || row.BalanceMicros != -dollars(7) {
		t.Errorf("arrears %s, balance %s; want $7 and -$7",
			billing.USD(row.ArrearsMicros), billing.USD(row.BalanceMicros))
	}
	if row.State != billingaccount.StateGracePeriod {
		t.Errorf("state = %s, want grace_period", row.State)
	}
	a.assertInvariant(t)
}

// A top-up pays what is owed before any of it is spendable, and an account that has paid is active
// again.
func TestATopUpPaysArrearsBeforeBecomingCredit(t *testing.T) {
	a := newAccount(t)
	a.usage(t, dollars(30), "u1") // nothing in the wallet: $30 owed

	lot := a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(100), Key: a.key("topup")})

	if lot.AmountMicros != dollars(100) || lot.RemainingMicros != dollars(70) {
		t.Errorf("lot granted %s with %s left; want $100 with $70",
			billing.USD(lot.AmountMicros), billing.USD(lot.RemainingMicros))
	}
	row := a.row(t)
	if row.ArrearsMicros != 0 || row.BalanceMicros != dollars(70) {
		t.Errorf("arrears %s, balance %s; want $0 and $70",
			billing.USD(row.ArrearsMicros), billing.USD(row.BalanceMicros))
	}
	if row.State != billingaccount.StateActive {
		t.Errorf("state = %s after paying; want active", row.State)
	}
	a.assertInvariant(t)
}

// A top-up smaller than the debt pays part of it and leaves the account in grace.
func TestATopUpSmallerThanTheDebtPaysPartOfIt(t *testing.T) {
	a := newAccount(t)
	a.usage(t, dollars(30), "u1")
	lot := a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(10), Key: a.key("topup")})

	row := a.row(t)
	if lot.RemainingMicros != 0 || row.ArrearsMicros != dollars(20) {
		t.Errorf("lot left %s, arrears %s; want $0 and $20",
			billing.USD(lot.RemainingMicros), billing.USD(row.ArrearsMicros))
	}
	if row.State != billingaccount.StateGracePeriod {
		t.Errorf("state = %s while still owing; want grace_period", row.State)
	}
	a.assertInvariant(t)
}

// The same key twice is one charge. A drain that crashed after spending and before marking its
// queue rows runs again with the same key, and must find its own entry.
func TestAUsageChargeReplayedWithItsKeyChargesOnce(t *testing.T) {
	a := newAccount(t)
	a.grant(t, wallet.GrantRequest{Kind: wallet.Purchase, Amount: dollars(50), Key: a.key("topup")})

	first := a.usage(t, dollars(10), "drain-1")
	second := a.usage(t, dollars(10), "drain-1")

	if !first.Applied || second.Applied {
		t.Errorf("applied = %v then %v; want true then false", first.Applied, second.Applied)
	}
	if second.Entry == nil || second.Entry.ID != first.Entry.ID {
		t.Error("the replay did not return the original entry")
	}
	if got := a.balance(t); got != dollars(40) {
		t.Errorf("balance %s after one charge replayed; want $40", billing.USD(got))
	}
	a.assertInvariant(t)
}

// A volume credit pays arrears first and the rest becomes non-expiring credit.
func TestAUsageCreditPaysArrearsThenBecomesPurchasedCredit(t *testing.T) {
	a := newAccount(t)
	a.usage(t, dollars(4), "u1") // $4 owed
	a.usageMovement(t, wallet.CreditUsage, dollars(10), "credit")

	row := a.row(t)
	if row.ArrearsMicros != 0 || row.BalanceMicros != dollars(6) {
		t.Errorf("arrears %s, balance %s; want $0 and $6",
			billing.USD(row.ArrearsMicros), billing.USD(row.BalanceMicros))
	}
	lots, err := a.db.Client.CreditLot.Query().Where(creditlot.AccountID(a.id)).All(context.Background())
	if err != nil || len(lots) != 1 {
		t.Fatalf("lots = %d (%v), want one", len(lots), err)
	}
	if lots[0].Kind != creditlot.KindPurchase || lots[0].ExpiresAt != nil || lots[0].RemainingMicros != dollars(6) {
		t.Errorf("credit lot = %s %s left, expires %v; want a non-expiring $6 purchase",
			lots[0].Kind, billing.USD(lots[0].RemainingMicros), lots[0].ExpiresAt)
	}
	a.assertInvariant(t)
}

// The race the row lock exists for: a top-up landing while usage drains. Without the lock each
// computes balance_after from the same row and one of them is erased from the running total.
func TestConcurrentTopUpsAndUsageNeverEraseEachOther(t *testing.T) {
	a := newAccount(t)
	const rounds = 20

	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for i := range rounds {
		wg.Add(2)
		go func() {
			defer wg.Done()
			tx, err := a.db.Client.Tx(context.Background())
			if err != nil {
				errs <- err
				return
			}
			_, _, err = wallet.Grant(context.Background(), tx, wallet.GrantRequest{
				AccountID: a.id, Kind: wallet.Purchase, Amount: dollars(2),
				GrantedAt: time.Now(), Key: a.key("topup-" + itoa(i)),
			})
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			errs <- err
		}()
		go func() {
			defer wg.Done()
			tx, err := a.db.Client.Tx(context.Background())
			if err != nil {
				errs <- err
				return
			}
			_, err = wallet.ChargeUsage(context.Background(), tx, wallet.UsageRequest{
				AccountID: a.id, Amount: dollars(1), At: time.Now(), Key: a.key("usage-" + itoa(i)),
			})
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent movement failed: %v", err)
		}
	}

	// $40 in, $20 out, whatever the interleaving — including usage that ran before any credit
	// existed and was owed, then paid by a later top-up.
	if got := a.balance(t); got != dollars(20) {
		t.Errorf("balance %s after %d top-ups of $2 and %d charges of $1; want $20",
			billing.USD(got), rounds, rounds)
	}
	if row := a.row(t); row.ArrearsMicros != 0 {
		t.Errorf("arrears %s once every top-up landed", billing.USD(row.ArrearsMicros))
	}
	a.assertInvariant(t)
}

func itoa(i int) string {
	return time.Duration(i).String()
}
