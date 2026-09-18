package wallet_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/ledgerentry"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The storage half: every movement of credit is a lot change and a ledger entry, in one transaction.
//
// Against the real database rather than a fake, because what is being tested is the pairing — a lot
// that moved without an entry beside it is a balance nobody can reconcile, and that failure only
// exists at the transaction boundary.

// newClaims invents a distinct verified identity, so every fixture gets its own organization.
func newClaims() identity.Claims {
	id := uuid.NewString()[:12]
	return identity.Claims{
		UID:           "firebase-" + id,
		Email:         "billing-" + id + "@studio.example",
		EmailVerified: true,
		Name:          "Ada Lovelace",
		Provider:      "google.com",
	}
}

// account is one organization with a billing account, and the database behind it.
type account struct {
	db *pgdb.Global
	id int

	// run distinguishes this fixture's idempotency keys from every other run's.
	run string
}

// key is an idempotency key that is unique to this run.
//
// It has to be. A key derived from the test's name is stable across runs, and an idempotency key is
// *meant* to make a repeat a no-op — so the second `go test` found the first run's lot, returned it
// unchanged, and every assertion about this account's balance failed while the code was correct. The
// first run passed, which is the worst version of that mistake.
//
// The same reasoning as `unique()` in the control-plane suite, and the same lesson its comment
// records about a clock-derived identifier.
func (a *account) key(name string) string { return name + ":" + a.run }

func newAccount(t *testing.T) *account {
	t.Helper()
	settings := testsupport.Settings(t)
	db := testsupport.Global(t, settings)

	person, err := identity.EnsureUser(context.Background(), db.Client, newClaims(), time.Now())
	if err != nil {
		t.Fatalf("seed a person: %v", err)
	}
	orgs, err := identity.Organizations(context.Background(), db.Client, person.ID)
	if err != nil || len(orgs) == 0 {
		t.Fatalf("the personal organization is missing: %v", err)
	}
	return &account{
		db:  db,
		id:  testsupport.SeedBillingAccount(t, db, orgs[0].ID).ID,
		run: uuid.NewString()[:12],
	}
}

// grant runs one grant in its own transaction, the way a handler does.
func (a *account) grant(t *testing.T, in wallet.GrantRequest) *global.CreditLot {
	t.Helper()
	in.AccountID = a.id
	if in.GrantedAt.IsZero() {
		in.GrantedAt = time.Now()
	}

	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	lot, _, err := wallet.Grant(context.Background(), tx, in)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Grant: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return lot
}

func (a *account) spend(t *testing.T, in wallet.SpendRequest) (*wallet.SpendResult, error) {
	t.Helper()
	in.AccountID = a.id
	if in.At.IsZero() {
		in.At = time.Now()
	}
	if in.Kind == "" {
		in.Kind = "usage"
	}

	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	result, err := wallet.Spend(context.Background(), tx, in)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return result, nil
}

// sweep runs the global expiry pass and returns only the entries for this account.
//
// The pass is deliberately one walk over every account's lots rather than one pass per customer, so
// in a shared test database it also writes off other runs' lapsed credit. Filtering here keeps the
// assertions about this fixture — the alternative is a test that fails because a different test left
// something behind.
func (a *account) sweep(t *testing.T, at time.Time) []*global.LedgerEntry {
	t.Helper()
	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	written, err := wallet.ExpireLapsed(context.Background(), tx, at, 100)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("ExpireLapsed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	mine := make([]*global.LedgerEntry, 0, len(written))
	for _, entry := range written {
		if entry.AccountID == a.id {
			mine = append(mine, entry)
		}
	}
	return mine
}

func (a *account) balance(t *testing.T) int64 {
	t.Helper()
	row, err := a.db.Client.BillingAccount.Get(context.Background(), a.id)
	if err != nil {
		t.Fatalf("read the account: %v", err)
	}
	return row.BalanceMicros
}

func (a *account) available(t *testing.T, at time.Time) int64 {
	t.Helper()
	lots, err := wallet.LotsOf(context.Background(), a.db.Client.CreditLot, a.id)
	if err != nil {
		t.Fatalf("read lots: %v", err)
	}
	return wallet.Available(lots, at)
}

func (a *account) statement(t *testing.T) []*global.LedgerEntry {
	t.Helper()
	rows, err := a.db.Client.LedgerEntry.Query().
		Where(ledgerentry.AccountID(a.id)).
		Order(global.Asc(ledgerentry.FieldID)).
		All(context.Background())
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	return rows
}

// TestAGrantIsALotAndALedgerEntry is the pairing everything else depends on.
//
// The ledger is the only answer to "why is my balance this", so a lot that appeared without an entry
// beside it is credit with no explanation — and the balance it produced cannot be checked against
// anything.
func TestAGrantIsALotAndALedgerEntry(t *testing.T) {
	a := newAccount(t)
	now := time.Now()

	lot := a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(100), GrantedAt: now,
		Description: "Launch credit", Key: a.key("promo"),
	})

	if lot.RemainingMicros != dollars(100) {
		t.Errorf("a fresh lot has %s left, want the whole $100", billing.USD(lot.RemainingMicros))
	}
	// Ninety days, because nobody named a date.
	if lot.ExpiresAt == nil || !lot.ExpiresAt.Equal(now.Add(wallet.PromoTerm)) {
		t.Errorf("expires_at = %v, want %v", lot.ExpiresAt, now.Add(wallet.PromoTerm))
	}

	entries := a.statement(t)
	if len(entries) != 1 {
		t.Fatalf("the statement has %d entries, want one", len(entries))
	}
	entry := entries[0]
	// `promo`, not `topup`: a topup is money that arrived, and a month that adds the two
	// together is a month whose revenue figure is wrong.
	if string(entry.Kind) != "promo" {
		t.Errorf("kind = %q, want promo", entry.Kind)
	}
	if entry.AmountMicros != dollars(100) || entry.BalanceAfterMicros != dollars(100) {
		t.Errorf("entry = %+v, want +$100 and a balance of $100", entry)
	}
	if a.balance(t) != dollars(100) {
		t.Errorf("the account's balance is %s", billing.USD(a.balance(t)))
	}
}

// TestGrantingTwiceWithOneKeyGrantsOnce is the protection a retry needs.
//
// Every path that grants credit is retried: a payment webhook is delivered at least once, and an
// operator whose request timed out presses the button again. A second $100 on an account is not a
// bug anybody notices before the customer does.
func TestGrantingTwiceWithOneKeyGrantsOnce(t *testing.T) {
	a := newAccount(t)
	in := wallet.GrantRequest{
		Kind: wallet.Purchase, Amount: dollars(50), GrantedAt: time.Now(),
		Description: "Top-up", Key: a.key("topup"),
	}

	first := a.grant(t, in)
	second := a.grant(t, in)

	if first.ID != second.ID {
		t.Errorf("two lots were created: %d and %d", first.ID, second.ID)
	}
	if got := a.balance(t); got != dollars(50) {
		t.Errorf("balance = %s, want $50 once", billing.USD(got))
	}
	if entries := a.statement(t); len(entries) != 1 {
		t.Errorf("the statement has %d entries, want one", len(entries))
	}
}

// TestASpendIsOneEntryAcrossManyLots is what a statement is for.
//
// A customer was charged $130 once, not twice. Which lots paid for it is the wallet's business; a
// statement listing one line per lot is a statement nobody can reconcile against an invoice.
func TestASpendIsOneEntryAcrossManyLots(t *testing.T) {
	a := newAccount(t)
	now := time.Now()

	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Purchase, Amount: dollars(40), GrantedAt: now.AddDate(0, 0, -30),
		Key: a.key("purchase"),
	})
	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(100), GrantedAt: now,
		Key: a.key("promo"),
	})

	result, err := a.spend(t, wallet.SpendRequest{
		Amount: dollars(130), Description: "March usage", Key: a.key("usage"), At: now,
	})
	if err != nil {
		t.Fatalf("Spend: %v", err)
	}
	if !result.Applied {
		t.Error("the first charge reported itself as a replay")
	}

	// Two draws, promo first — the ordering rules, now through the database.
	if len(result.Draws) != 2 ||
		result.Draws[0].Kind != wallet.Promo || result.Draws[0].Amount != dollars(100) ||
		result.Draws[1].Kind != wallet.Purchase || result.Draws[1].Amount != dollars(30) {
		t.Fatalf("draws = %+v", result.Draws)
	}

	// One entry for the charge, and the balance is what is left.
	entries := a.statement(t)
	if len(entries) != 3 {
		t.Fatalf("the statement has %d entries, want two grants and one charge", len(entries))
	}
	charge := entries[2]
	if charge.AmountMicros != -dollars(130) || charge.BalanceAfterMicros != dollars(10) {
		t.Errorf("charge = %+v, want -$130 leaving $10", charge)
	}
	if got := a.available(t, now); got != dollars(10) {
		t.Errorf("available = %s, want $10", billing.USD(got))
	}
	// The promo is exhausted and the purchase is partly spent, which is what the lots say.
	if got := a.balance(t); got != dollars(10) {
		t.Errorf("balance = %s, want $10", billing.USD(got))
	}
}

// TestChargingTwiceWithOneKeyChargesOnce is the same protection on the way out.
//
// A month close can run again after a crash, and the second run must not charge again.
func TestChargingTwiceWithOneKeyChargesOnce(t *testing.T) {
	a := newAccount(t)
	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Purchase, Amount: dollars(100), GrantedAt: time.Now(),
		Key: a.key("purchase"),
	})

	in := wallet.SpendRequest{Amount: dollars(60), Description: "Usage", Key: a.key("usage")}
	first, err := a.spend(t, in)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	second, err := a.spend(t, in)
	if err != nil {
		t.Fatalf("replayed charge: %v", err)
	}

	if !first.Applied || second.Applied {
		t.Errorf("applied = %v then %v; want true then false", first.Applied, second.Applied)
	}
	if got := a.balance(t); got != dollars(40) {
		t.Errorf("balance = %s, want $40 — charged once", billing.USD(got))
	}
}

// TestASweepWritesOffLapsedCreditOneLotAtATime is the opposite case from a spend.
//
// A customer asking "where did my $100 go" is asking about a specific grant, so an aggregate entry
// would answer with a number matching nothing they were ever told. And the kind is `expiry` rather
// than `adjustment`: an adjustment is a decision somebody can be asked about, and a customer reading
// "adjustment -$100" would reasonably ask who did that to them.
func TestASweepWritesOffLapsedCreditOneLotAtATime(t *testing.T) {
	a := newAccount(t)
	now := time.Now()
	past := now.Add(-time.Hour)

	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(60), GrantedAt: now.AddDate(0, 0, -100),
		ExpiresAt: &past, Description: "Evaluation credit", Key: a.key("a"),
	})
	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(25), GrantedAt: now.AddDate(0, 0, -100),
		ExpiresAt: &past, Key: a.key("b"),
	})
	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Purchase, Amount: dollars(15), GrantedAt: now, Key: a.key("c"),
	})

	// Before the sweep the balance column is overstated and what can be spent is not: the two
	// numbers disagree by exactly the credit that has lapsed, which is the design rather than a
	// bug.
	if got := a.balance(t); got != dollars(100) {
		t.Errorf("balance before the sweep = %s, want $100", billing.USD(got))
	}
	if got := a.available(t, now); got != dollars(15) {
		t.Errorf("available before the sweep = %s, want $15", billing.USD(got))
	}

	written := a.sweep(t, now)
	if len(written) != 2 {
		t.Fatalf("the sweep wrote %d entries, want one per lapsed lot", len(written))
	}
	for _, entry := range written {
		if string(entry.Kind) != "expiry" {
			t.Errorf("kind = %q, want expiry", entry.Kind)
		}
	}
	// The named grant is named on its own line, because that is the line somebody is looking for.
	if written[0].Description == "" || !contains(written[0].Description, "Evaluation credit") {
		t.Errorf("the write-off does not name the grant: %q", written[0].Description)
	}

	// And now the two numbers agree, which is what the sweep is for.
	if got := a.balance(t); got != dollars(15) {
		t.Errorf("balance after the sweep = %s, want $15", billing.USD(got))
	}
	if a.balance(t) != a.available(t, now) {
		t.Errorf("balance %s and available %s still disagree",
			billing.USD(a.balance(t)), billing.USD(a.available(t, now)))
	}

	// Sweeping again does nothing: the lots are empty and the keys are taken.
	if again := a.sweep(t, now); len(again) != 0 {
		t.Errorf("a second sweep wrote %d entries", len(again))
	}
}

// TestASweepLeavesCreditThatWasSpentFirstAlone is the race between a charge and the calendar.
//
// A lot spent to zero before the sweep reaches it has nothing to write off, and an entry claiming it
// expired would be a second negative movement for money already gone — the balance would end up
// lower than the customer ever spent.
func TestASweepLeavesCreditThatWasSpentFirstAlone(t *testing.T) {
	a := newAccount(t)
	now := time.Now()
	soon := now.Add(time.Hour)

	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(50), GrantedAt: now, ExpiresAt: &soon,
		Key: a.key("promo"),
	})
	if _, err := a.spend(t, wallet.SpendRequest{
		Amount: dollars(50), Description: "Usage", Key: a.key("usage"), At: now,
	}); err != nil {
		t.Fatalf("Spend: %v", err)
	}

	// An hour later the lot has lapsed, and it is empty.
	if written := a.sweep(t, soon.Add(time.Minute)); len(written) != 0 {
		t.Errorf("the sweep wrote off %d spent lots", len(written))
	}
	if got := a.balance(t); got != 0 {
		t.Errorf("balance = %s, want nothing", billing.USD(got))
	}
}

// TestAChargeAgainstLapsedCreditIsRefused is the property that makes the overstated balance safe.
//
// Between midnight and the sweep the column says $100 and the wallet can spend nothing. The spend
// path has to refuse on its own rather than trusting the number.
func TestAChargeAgainstLapsedCreditIsRefused(t *testing.T) {
	a := newAccount(t)
	now := time.Now()
	past := now.Add(-time.Minute)

	a.grant(t, wallet.GrantRequest{
		Kind: wallet.Promo, Amount: dollars(100), GrantedAt: now.AddDate(0, 0, -91),
		ExpiresAt: &past, Key: a.key("promo"),
	})

	_, err := a.spend(t, wallet.SpendRequest{
		Amount: dollars(10), Description: "Usage", Key: a.key("usage"), At: now,
	})
	if !errors.Is(err, wallet.ErrInsufficient) {
		t.Fatalf("Spend against lapsed credit = %v, want ErrInsufficient", err)
	}
	// Nothing was written, so the refusal did not leave a charge behind.
	if entries := a.statement(t); len(entries) != 1 {
		t.Errorf("the statement has %d entries, want only the grant", len(entries))
	}
}

// TestPurchasedCreditCannotBeGivenAnExpiry is the refusal that protects a customer's own money.
func TestPurchasedCreditCannotBeGivenAnExpiry(t *testing.T) {
	a := newAccount(t)
	now := time.Now()
	date := now.AddDate(0, 1, 0)

	tx, err := a.db.Client.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, _, err := wallet.Grant(context.Background(), tx, wallet.GrantRequest{
		AccountID: a.id, Kind: wallet.Purchase, Amount: dollars(10),
		GrantedAt: now, ExpiresAt: &date, Key: a.key("bad"),
	}); err == nil {
		t.Error("purchased credit was given an expiry date")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}
