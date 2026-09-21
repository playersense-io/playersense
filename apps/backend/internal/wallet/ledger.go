package wallet

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/internal/billing"
)

// The three things that move credit, each a lot change and a ledger entry in one transaction.
//
// The ledger is append-only and carries `balance_after_micros`, so a statement renders without
// summing history. That only holds if nothing ever changes a lot without writing beside it, which is
// why none of these takes a client: a caller who cannot start a transaction cannot move money.

// Grant adds a lot and records it.
//
// # The expiry
//
// A promo defaults to [PromoTerm] from `grantedAt` and accepts any later date instead — "ninety days
// unless we agreed otherwise", which is the whole of what an operator needs. A purchase cannot be
// given one at all: money a customer paid for is not ours to put a clock on, and a grant that
// accepted a date would be a wallet that could quietly delete somebody's money.
//
// # Idempotency
//
// `key` is derived from what happened — a payment intent, or the operator's own request id — never
// from the clock. A grant retried after a timeout must find the lot it already made rather than
// making a second one, and a second $100 on an account is not a bug anybody notices before the
// customer does.
//
// A repeat returns the existing lot and reports `false`, so a caller can tell "granted" from
// "already granted" and answer 200 rather than 201 without guessing.
func Grant(ctx context.Context, tx *ent.Tx, in GrantRequest) (*ent.CreditLot, bool, error) {
	if err := in.validate(); err != nil {
		return nil, false, err
	}

	existing, err := tx.CreditLot.Query().
		Where(creditlot.IdempotencyKey(in.Key)).Only(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, fmt.Errorf("look up an existing grant: %w", err)
	}

	account, err := tx.BillingAccount.Get(ctx, in.AccountID)
	if err != nil {
		return nil, false, fmt.Errorf("look up the billing account: %w", err)
	}
	if in.Currency != "" && in.Currency != account.Currency {
		return nil, false, fmt.Errorf("%w: the account holds %s, not %s",
			ErrCurrencyMismatch, account.Currency, in.Currency)
	}

	expires, err := Term(in.Kind, in.GrantedAt, in.ExpiresAt)
	if err != nil {
		return nil, false, err
	}

	create := tx.CreditLot.Create().
		SetAccountID(in.AccountID).
		SetKind(creditlot.Kind(in.Kind)).
		SetAmountMicros(in.Amount).
		SetRemainingMicros(in.Amount).
		SetCurrency(account.Currency).
		SetGrantedAt(in.GrantedAt).
		SetDescription(in.Description).
		SetIdempotencyKey(in.Key)
	if expires != nil {
		create = create.SetExpiresAt(*expires)
	}
	if in.StripeReference != "" {
		create = create.SetStripeReference(in.StripeReference)
	}
	if in.GrantedByUserID != 0 {
		create = create.SetGrantedByUserID(in.GrantedByUserID)
	}

	lot, err := create.Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("create the credit lot: %w", err)
	}

	// The ledger entry carries the account's running total, which a promo moves exactly as a
	// topup does: the balance is what the customer can spend, and credit they were given spends
	// the same as credit they bought. What differs is the kind, so revenue can be counted
	// without it.
	if _, err := record(ctx, tx, account, movement{
		Kind:            ledgerKind(in.Kind),
		Amount:          in.Amount,
		Description:     in.Description,
		StripeReference: in.StripeReference,
		Key:             "grant:" + in.Key,
	}); err != nil {
		return nil, false, err
	}
	return lot, true, nil
}

// GrantRequest is one grant, as a value so a signature cannot be called with two of three amounts.
type GrantRequest struct {
	AccountID int
	Kind      string
	Amount    int64
	Currency  string

	// ExpiresAt overrides [PromoTerm] for a promo, and is refused for a purchase.
	ExpiresAt *time.Time

	// GrantedAt is when the credit became available, which is the order purchased credit is
	// spent in. Backdating is deliberate and supported: a lot for an invoice paid last week
	// belongs in the queue where the payment was, not where the data entry was.
	GrantedAt time.Time

	Description     string
	StripeReference string
	GrantedByUserID int
	Key             string
}

func (in GrantRequest) validate() error {
	switch {
	case in.Amount <= 0:
		return fmt.Errorf("%w: %s", ErrNotPositive, billing.USD(in.Amount))
	case in.Kind != Promo && in.Kind != Purchase:
		return fmt.Errorf("a credit lot is a purchase or a promo, not %q", in.Kind)
	case in.Key == "":
		return fmt.Errorf("a grant needs an idempotency key derived from what happened")
	case in.GrantedAt.IsZero():
		return fmt.Errorf("a grant needs the moment the credit became available")
	}
	return nil
}

// Spend consumes credit in [Order] and records one ledger entry for the whole charge.
//
// One entry rather than one per lot, and the distinction is what a statement is for: a customer was
// charged $60 once, not three times. Which lots paid for it is the wallet's business — visible in
// the lots themselves and in the entry's description — and a statement that listed three lines for
// one charge would be a statement nobody could reconcile against an invoice.
//
// Refused whole if there is not enough. A partial spend would leave a charge that did not happen, a
// balance of zero and no record of the difference.
func Spend(ctx context.Context, tx *ent.Tx, in SpendRequest) (*SpendResult, error) {
	if in.Key == "" {
		return nil, fmt.Errorf("a spend needs an idempotency key derived from what happened")
	}

	// Already applied. Returned rather than repeated, because every caller of this is retried:
	// a month close can run twice after a crash, and the second run must not charge again.
	existing, err := tx.LedgerEntry.Query().
		Where(ledgerentry.IdempotencyKey(in.Key)).Only(ctx)
	if err == nil {
		return &SpendResult{Entry: existing, Applied: false}, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("look up an existing charge: %w", err)
	}

	account, err := tx.BillingAccount.Get(ctx, in.AccountID)
	if err != nil {
		return nil, fmt.Errorf("look up the billing account: %w", err)
	}

	lots, err := LotsOf(ctx, tx.CreditLot, in.AccountID)
	if err != nil {
		return nil, err
	}
	draws, err := Plan(lots, in.Amount, in.At)
	if err != nil {
		return nil, err
	}

	for _, draw := range draws {
		// Guarded on the remaining amount, so two concurrent spends cannot both take the
		// last of a lot. The update matches nothing if another transaction got there first,
		// and a draw that moved no row fails the charge rather than silently taking less.
		moved, err := tx.CreditLot.Update().
			Where(creditlot.ID(draw.LotID), creditlot.RemainingMicrosGTE(draw.Amount)).
			AddRemainingMicros(-draw.Amount).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("spend from lot %d: %w", draw.LotID, err)
		}
		if moved != 1 {
			return nil, fmt.Errorf("%w: lot %d changed while this charge was being applied",
				ErrInsufficient, draw.LotID)
		}
	}

	entry, err := record(ctx, tx, account, movement{
		Kind:        in.Kind,
		Amount:      -in.Amount,
		Description: in.Description,
		PeriodID:    in.PeriodID,
		Key:         in.Key,
	})
	if err != nil {
		return nil, err
	}
	return &SpendResult{Entry: entry, Draws: draws, Applied: true}, nil
}

// SpendRequest is one charge against the wallet.
type SpendRequest struct {
	AccountID int
	Amount    int64

	// Kind is the ledger kind: `usage` for a closed period, `subscription` for the plan fee,
	// `adjustment` for a person's decision. Not a wallet concept — the wallet only knows credit
	// left — but it is what a statement reads.
	Kind        string
	Description string
	PeriodID    int
	At          time.Time
	Key         string
}

// SpendResult is what a charge consumed.
type SpendResult struct {
	Entry *ent.LedgerEntry
	Draws []Draw

	// Applied is false when the idempotency key had already been used, so a caller can tell a
	// charge from the replay of one.
	Applied bool
}

// ExpireLapsed writes off credit whose date has passed, one ledger entry per lot.
//
// One entry per lot rather than one per account, because this is the opposite case from a spend: a
// customer asking "where did my $100 go" is asking about a *specific grant*, and an aggregate would
// answer with a number that matches nothing they were ever told.
//
// Idempotent by construction: the key is `expiry:<lot id>` and the lot's remaining goes to zero, so
// a second sweep finds nothing to do. The sweep is what reconciles `balance_micros` with what the
// wallet can actually spend — and until it runs, a spend already refuses an expired lot, so the
// balance is briefly overstated and never overspent.
func ExpireLapsed(ctx context.Context, tx *ent.Tx, at time.Time, limit int) ([]*ent.LedgerEntry, error) {
	lapsed, err := tx.CreditLot.Query().
		Where(
			creditlot.RemainingMicrosGT(0),
			creditlot.ExpiresAtNotNil(),
			creditlot.ExpiresAtLTE(at),
		).
		Order(ent.Asc(creditlot.FieldExpiresAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("find lapsed credit: %w", err)
	}

	var written []*ent.LedgerEntry
	for _, lot := range lapsed {
		account, err := tx.BillingAccount.Get(ctx, lot.AccountID)
		if err != nil {
			return nil, fmt.Errorf("look up the account behind lot %d: %w", lot.ID, err)
		}

		amount := lot.RemainingMicros
		moved, err := tx.CreditLot.Update().
			Where(creditlot.ID(lot.ID), creditlot.RemainingMicros(amount)).
			SetRemainingMicros(0).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("write off lot %d: %w", lot.ID, err)
		}
		if moved != 1 {
			// Somebody spent from it between the read and the write. Not an error: the
			// spend was legitimate, and the next sweep writes off whatever is left.
			continue
		}

		entry, err := record(ctx, tx, account, movement{
			Kind:   "expiry",
			Amount: -amount,
			Description: "Promotional credit expired: " +
				describeLot(lot),
			Key: "expiry:" + strconv.Itoa(lot.ID),
		})
		if err != nil {
			return nil, err
		}
		written = append(written, entry)
	}
	return written, nil
}

// describeLot names a lapsed grant the way its statement line should read.
func describeLot(lot *ent.CreditLot) string {
	if lot.Description != "" {
		return lot.Description
	}
	return billing.USD(lot.AmountMicros) + " granted " + lot.GrantedAt.Format("2 January 2006")
}

// movement is one ledger entry before it knows the balance it produced.
type movement struct {
	Kind            string
	Amount          int64
	Description     string
	StripeReference string
	PeriodID        int
	Key             string
}

// record appends to the ledger and moves the account's running total, in that order.
//
// The two are one operation: `balance_after_micros` is written rather than recomputed, so an entry
// whose account was not moved is an entry claiming a balance the account does not have. Both happen
// inside the caller's transaction.
func record(ctx context.Context, tx *ent.Tx, account *ent.BillingAccount, in movement) (*ent.LedgerEntry, error) {
	after := account.BalanceMicros + in.Amount

	create := tx.LedgerEntry.Create().
		SetAccountID(account.ID).
		SetKind(ledgerentry.Kind(in.Kind)).
		SetAmountMicros(in.Amount).
		SetBalanceAfterMicros(after).
		SetCurrency(account.Currency).
		SetDescription(in.Description).
		SetIdempotencyKey(in.Key)
	if in.StripeReference != "" {
		create = create.SetStripeReference(in.StripeReference)
	}
	if in.PeriodID != 0 {
		create = create.SetPeriodID(in.PeriodID)
	}

	entry, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("append to the ledger: %w", err)
	}
	if _, err := tx.BillingAccount.UpdateOneID(account.ID).
		SetBalanceMicros(after).Save(ctx); err != nil {
		return nil, fmt.Errorf("move the account's balance: %w", err)
	}
	// Kept in step in memory too, so a caller writing two movements in one transaction does not
	// compute the second one's balance from a stale row.
	account.BalanceMicros = after
	return entry, nil
}

// ledgerKind is what a grant of this sort is called on a statement.
//
// A promo is not a `topup`: a topup is money that arrived, it appears in revenue and it is
// refundable. Adding the two together is how a month's revenue figure stops being one.
func ledgerKind(kind string) string {
	if kind == Promo {
		return "promo"
	}
	return "topup"
}
