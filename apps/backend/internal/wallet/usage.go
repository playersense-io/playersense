package wallet

import (
	"context"
	"fmt"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/internal/billing"
)

// Usage is the one kind of charge the wallet cannot refuse (ADR 0039).
//
// Every other spend is a decision that can wait for credit, and [Spend] refuses it whole when there
// is not enough. Usage is different in kind: by the time it is priced the players have been served —
// their batches were answered 202 and are already stored — so refusing the charge would not undo
// anything, it would only lose the money. So usage takes what credit there is, in the same order
// every spend uses (promos soonest-expiring first, then purchases oldest first), and what credit
// cannot cover becomes the account's arrears, which the next grant pays before anything else.
//
// Both movements here are a single ledger entry of kind `usage` however many lots paid, for the
// reason [Spend] gives: a customer was charged once.
//
// An invoiced account (ADR 0045) spends credit the same way, and owes the rest as a receivable
// rather than arrears: it is billed on the month's invoice and due on the invoice's terms, not
// overdue the moment it is charged.

// UsageRequest is one drain's net charge for one account.
type UsageRequest struct {
	AccountID int

	// Amount is positive. A charge and a credit are two functions rather than one signed
	// amount, so a sign error cannot turn a bill into a refund.
	Amount int64

	Description string
	At          time.Time

	// PeriodStart is the month the usage belongs to, by arrival (ADR 0039), so an invoice can
	// bill it as that month's even when it settles in the next. Zero leaves it unrecorded.
	PeriodStart time.Time

	// Key is derived from the queue rows the drain settled, so a drain that crashed after
	// spending and before marking its rows finds its own entry on the next pass.
	Key string
}

// UsageResult is what a usage movement did.
type UsageResult struct {
	Entry *ent.LedgerEntry
	Draws []Draw

	// Owed is the part credit could not cover, added to the account's arrears — or, for an
	// invoiced account, to its receivable.
	Owed int64

	// Applied is false when the key had already been used.
	Applied bool
}

// ChargeUsage takes a usage charge from the wallet, owing what it cannot cover.
func ChargeUsage(ctx context.Context, tx *ent.Tx, in UsageRequest) (*UsageResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &UsageResult{Entry: existing}, err
	}

	lots, err := LotsOf(ctx, tx.CreditLot, in.AccountID)
	if err != nil {
		return nil, err
	}
	draws, covered := Cover(lots, in.Amount, in.At)
	if err := take(ctx, tx, draws); err != nil {
		return nil, err
	}

	owed := in.Amount - covered
	move := movement{
		Kind:        "usage",
		Amount:      -in.Amount,
		PeriodStart: in.period(),
		Description: in.Description,
		Key:         in.Key,
	}
	if IsInvoiced(account) {
		move.Receivable = owed
	} else {
		move.Arrears = owed
	}
	entry, err := record(ctx, tx, account, move)
	if err != nil {
		return nil, err
	}
	return &UsageResult{Entry: entry, Draws: draws, Owed: owed, Applied: true}, nil
}

// CreditUsage gives usage money back: a volume card repricing the month downward.
//
// Arrears first, for the same reason a grant pays them first — or, for an invoiced account, the
// receivable, which is usage not yet paid for: taking it off the next invoice is the credit. What is
// left becomes a **purchase** lot — it is money the customer already paid for players, so it does not
// expire and it is not a discount — keyed off the same idempotency key so a replay cannot mint it
// twice.
func CreditUsage(ctx context.Context, tx *ent.Tx, in UsageRequest) (*UsageResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &UsageResult{Entry: existing}, err
	}

	owing := account.ArrearsMicros
	if IsInvoiced(account) {
		owing = account.ReceivableMicros
	}
	settled := min(owing, in.Amount)
	if rest := in.Amount - settled; rest > 0 {
		if _, err := tx.CreditLot.Create().
			SetAccountID(in.AccountID).
			SetKind(creditlot.KindPurchase).
			SetAmountMicros(rest).
			SetRemainingMicros(rest).
			SetCurrency(account.Currency).
			SetGrantedAt(in.At).
			SetDescription(in.Description).
			SetIdempotencyKey("usage-credit:" + in.Key).
			Save(ctx); err != nil {
			return nil, fmt.Errorf("create the usage credit: %w", err)
		}
	}

	move := movement{
		Kind:        "usage",
		Amount:      in.Amount,
		PeriodStart: in.period(),
		Description: in.Description,
		Key:         in.Key,
	}
	if IsInvoiced(account) {
		move.Receivable = -settled
	} else {
		move.Arrears = -settled
	}
	entry, err := record(ctx, tx, account, move)
	if err != nil {
		return nil, err
	}
	return &UsageResult{Entry: entry, Applied: true}, nil
}

func (in UsageRequest) period() *time.Time {
	if in.PeriodStart.IsZero() {
		return nil
	}
	start := in.PeriodStart.UTC()
	return &start
}

func (in UsageRequest) validate() error {
	switch {
	case in.Amount <= 0:
		return fmt.Errorf("%w: %s", ErrNotPositive, billing.USD(in.Amount))
	case in.Key == "":
		return fmt.Errorf("a usage movement needs an idempotency key derived from what it settles")
	case in.At.IsZero():
		return fmt.Errorf("a usage movement needs the moment it applies")
	}
	return nil
}
