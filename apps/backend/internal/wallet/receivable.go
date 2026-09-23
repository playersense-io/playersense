package wallet

import (
	"context"
	"fmt"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/internal/billing"
)

// An invoiced account's receivable (ADR 0045): what it has used and not yet paid for.
//
// Two kinds of movement, and the month close tells them apart by one column:
//
//   - **Accruals** add to it, and an invoice bills them: usage credit did not cover (usage.go), the
//     contract's platform fee and its commitment top-up ([Accrue]), and arrears carried in when an
//     account moves to invoicing ([CarryArrears]). They name no invoice.
//   - **Settlements** take from it, and each names the invoice it settles: a payment ([PayInvoice]),
//     a credit note or a write-off ([WriteOff]), and a dispute putting a payment back ([Reopen]). They
//     are never billed.
//
// Every one is a ledger entry through [record], under the account's row lock, so the invariant —
// balance = Σ lots − arrears − receivable — holds at every commit.

// AccrueRequest is a charge an invoice bills and credit never pays: the platform fee, the commitment.
type AccrueRequest struct {
	AccountID int

	// Kind is the ledger's: `subscription` for the platform fee, `commitment` for the minimum's
	// top-up.
	Kind   string
	Amount int64 // positive micros

	Description string
	PeriodStart time.Time
	Key         string
}

// Accrue adds a contract charge to the receivable.
//
// Straight to the receivable, never from credit: promotional credit is a discount on usage, and
// letting it pay a negotiated minimum would lower the minimum. Reports false for a replay.
func Accrue(ctx context.Context, tx *ent.Tx, in AccrueRequest) (*ent.LedgerEntry, bool, error) {
	switch {
	case in.Amount <= 0:
		return nil, false, fmt.Errorf("%w: %s", ErrNotPositive, billing.USD(in.Amount))
	case in.Kind != "subscription" && in.Kind != "commitment":
		return nil, false, fmt.Errorf("a contract charge is a subscription or a commitment, not %q", in.Kind)
	case in.Key == "":
		return nil, false, fmt.Errorf("a contract charge needs an idempotency key derived from its period")
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, false, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return existing, false, err
	}
	move := movement{
		Kind: in.Kind, Amount: -in.Amount, Receivable: in.Amount,
		Description: in.Description, Key: in.Key,
	}
	if !in.PeriodStart.IsZero() {
		start := in.PeriodStart.UTC()
		move.PeriodStart = &start
	}
	entry, err := record(ctx, tx, account, move)
	if err != nil {
		return nil, false, err
	}
	return entry, true, nil
}

// CarryArrears moves an account's arrears into its receivable, when it moves to invoicing.
//
// The debt is the same debt; what changes is that it is now billed on the first invoice and due on its
// terms rather than overdue already. One entry of no amount — the balance does not move — so the
// statement says where the arrears went.
func CarryArrears(ctx context.Context, tx *ent.Tx, accountID int, key string) (*ent.LedgerEntry, error) {
	account, err := lock(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, key); err != nil || existing != nil {
		return existing, err
	}
	owed := account.ArrearsMicros
	if owed == 0 {
		return nil, nil
	}
	return record(ctx, tx, account, movement{
		Kind: "adjustment", Arrears: -owed, Receivable: owed,
		Description: billing.USD(owed) + " owed, carried to the first invoice", Key: key,
	})
}

// SettleRequest is one settlement of one invoice.
type SettleRequest struct {
	AccountID int
	InvoiceID int
	Amount    int64 // positive micros

	Description     string
	StripeReference string
	At              time.Time
	Key             string
}

// SettleResult is what a settlement moved.
type SettleResult struct {
	Entry *ent.LedgerEntry

	// Settled is what came off the receivable; Excess what was paid beyond it and became
	// purchased credit.
	Settled int64
	Excess  int64

	Applied bool // false when the key had already been used
}

// PayInvoice records an invoice paid: the receivable falls by what was paid.
//
// A payment larger than the receivable — a customer who paid twice, or paid an invoice a credit had
// already reduced — is the customer's money, so the rest pays any arrears and then becomes a purchase
// lot, exactly as a top-up would. One `invoice_payment` entry, naming the invoice.
func PayInvoice(ctx context.Context, tx *ent.Tx, in SettleRequest) (*SettleResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &SettleResult{Entry: existing}, err
	}

	settled := min(account.ReceivableMicros, in.Amount)
	excess := in.Amount - settled
	arrears := min(account.ArrearsMicros, excess)
	if rest := excess - arrears; rest > 0 {
		create := tx.CreditLot.Create().
			SetAccountID(in.AccountID).
			SetKind(creditlot.KindPurchase).
			SetAmountMicros(rest).
			SetRemainingMicros(rest).
			SetCurrency(account.Currency).
			SetGrantedAt(in.at()).
			SetDescription("Paid beyond the invoice").
			SetIdempotencyKey("excess:" + in.Key)
		if in.StripeReference != "" {
			create = create.SetStripeReference(in.StripeReference)
		}
		if _, err := create.Save(ctx); err != nil {
			return nil, fmt.Errorf("keep the overpayment as credit: %w", err)
		}
	}
	entry, err := record(ctx, tx, account, movement{
		Kind: "invoice_payment", Amount: in.Amount,
		Receivable: -settled, Arrears: -arrears, InvoiceID: in.InvoiceID,
		Description: in.Description, StripeReference: in.StripeReference, Key: in.Key,
	})
	if err != nil {
		return nil, err
	}
	return &SettleResult{Entry: entry, Settled: settled, Excess: excess, Applied: true}, nil
}

// WriteOff takes an amount off the receivable that will not be paid: a credit note, an invoice voided
// or marked uncollectible. At most the receivable — never into credit, because a credit note on an
// open invoice lowers what is owed, and nothing was paid to give back. One `adjustment` entry.
func WriteOff(ctx context.Context, tx *ent.Tx, in SettleRequest) (*SettleResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &SettleResult{Entry: existing}, err
	}
	settled := min(account.ReceivableMicros, in.Amount)
	entry, err := record(ctx, tx, account, movement{
		Kind: "adjustment", Amount: settled, Receivable: -settled, InvoiceID: in.InvoiceID,
		Description: in.Description, StripeReference: in.StripeReference, Key: in.Key,
	})
	if err != nil {
		return nil, err
	}
	return &SettleResult{Entry: entry, Settled: settled, Applied: true}, nil
}

// Reopen puts a payment back on the receivable: a bank took the money back in a dispute, so it is
// owed again. One `refund` entry, naming the invoice.
func Reopen(ctx context.Context, tx *ent.Tx, in SettleRequest) (*SettleResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &SettleResult{Entry: existing}, err
	}
	entry, err := record(ctx, tx, account, movement{
		Kind: "refund", Amount: -in.Amount, Receivable: in.Amount, InvoiceID: in.InvoiceID,
		Description: in.Description, StripeReference: in.StripeReference, Key: in.Key,
	})
	if err != nil {
		return nil, err
	}
	return &SettleResult{Entry: entry, Settled: -in.Amount, Applied: true}, nil
}

// SetOverdue records when an account's first unpaid invoice passed its due date — or, with nil, that
// none is overdue any more — and moves its state as the ledger would (ADR 0045): overdue starts the
// grace period, and paying the last overdue invoice ends it. No ledger entry: no money moved.
func SetOverdue(ctx context.Context, tx *ent.Tx, accountID int, since *time.Time) (*ent.BillingAccount, error) {
	account, err := lock(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if (account.OverdueSince == nil) == (since == nil) {
		return account, nil
	}
	update := tx.BillingAccount.UpdateOneID(account.ID)
	if since == nil {
		update = update.ClearOverdueSince()
	} else {
		update = update.SetOverdueSince(since.UTC())
	}
	account.OverdueSince = since
	if state, changed := stateFor(account, account.ArrearsMicros); changed {
		update = update.SetState(state).SetStateChangedAt(time.Now().UTC())
		account.State = state
	}
	if err := update.Exec(ctx); err != nil {
		return nil, fmt.Errorf("record the account as overdue: %w", err)
	}
	return account, nil
}

// IsInvoiced is whether an account is billed by invoice.
func IsInvoiced(account *ent.BillingAccount) bool {
	return account.Collection == billingaccount.CollectionInvoiced
}

func (in SettleRequest) validate() error {
	switch {
	case in.Amount <= 0:
		return fmt.Errorf("%w: %s", ErrNotPositive, billing.USD(in.Amount))
	case in.InvoiceID == 0:
		return fmt.Errorf("a settlement names the invoice it settles")
	case in.Key == "":
		return fmt.Errorf("a settlement needs an idempotency key derived from what happened")
	}
	return nil
}

func (in SettleRequest) at() time.Time {
	if in.At.IsZero() {
		return time.Now().UTC()
	}
	return in.At
}
