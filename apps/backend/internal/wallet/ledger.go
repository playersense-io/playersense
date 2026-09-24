package wallet

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/internal/billing"
)

// The three things that move credit, each a lot change and a ledger entry in one transaction.
//
// The ledger is append-only and carries `balance_after_micros`, so a statement renders without
// summing history. That only holds if nothing ever changes a lot without writing beside it, which is
// why none of these takes a client: a caller who cannot start a transaction cannot move money.
//
// # Every movement locks the account first
//
// `balance_after_micros` is computed from the account row, so two movements computing it from the
// same row would each write a balance that ignores the other — a top-up landing during a usage drain
// would erase the drain's charge from the running total. That was a race nobody hit while money
// moved only when an operator pressed a button; with the usage drain spending every few seconds
// (ADR 0039) it is one somebody would hit on the first busy day. So every function here starts by
// taking the account's row lock, and the idempotency check comes *after* it: a duplicate that waited
// for the lock then sees the row the first one committed.

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

	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, false, err
	}

	existing, err := tx.CreditLot.Query().
		Where(creditlot.IdempotencyKey(in.Key)).Only(ctx)
	if err == nil {
		// A key already used by another account is not a replay of this grant — returning that
		// lot would tell the caller this account was credited when it was not. Refused loudly.
		if existing.AccountID != in.AccountID {
			return nil, false, fmt.Errorf("the idempotency key %q already granted credit to another account", in.Key)
		}
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, fmt.Errorf("look up an existing grant: %w", err)
	}

	if in.Currency != "" && in.Currency != account.Currency {
		return nil, false, fmt.Errorf("%w: the account holds %s, not %s",
			ErrCurrencyMismatch, account.Currency, in.Currency)
	}

	expires, err := Term(in.Kind, in.GrantedAt, in.ExpiresAt)
	if err != nil {
		return nil, false, err
	}

	// Arrears first. Usage the wallet could not cover is owed before anything else is
	// spendable (ADR 0039), so a grant pays it down and only the rest becomes credit. The lot
	// still records the whole grant as its amount — what was granted does not change because
	// of what it paid for — and its remainder says what is left.
	settled := min(account.ArrearsMicros, in.Amount)

	create := tx.CreditLot.Create().
		SetAccountID(in.AccountID).
		SetKind(creditlot.Kind(in.Kind)).
		SetAmountMicros(in.Amount).
		SetRemainingMicros(in.Amount - settled).
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
	if in.GrantedByStaff != "" {
		create = create.SetGrantedByStaff(in.GrantedByStaff)
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
		Arrears:         -settled,
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
	// GrantedByStaff is the verified address of the staff member granting it; empty for credit
	// a payment created.
	GrantedByStaff string
	Key            string
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

	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}

	// Already applied. Returned rather than repeated, because every caller of this is retried:
	// a month close can run twice after a crash, and the second run must not charge again.
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &SpendResult{Entry: existing, Applied: false}, err
	}

	lots, err := LotsOf(ctx, tx.CreditLot, in.AccountID)
	if err != nil {
		return nil, err
	}
	draws, err := Plan(lots, in.Amount, in.At)
	if err != nil {
		return nil, err
	}
	if err := take(ctx, tx, draws); err != nil {
		return nil, err
	}

	entry, err := record(ctx, tx, account, movement{
		Kind:        in.Kind,
		Amount:      -in.Amount,
		Description: in.Description,
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
		account, err := lock(ctx, tx, lot.AccountID)
		if err != nil {
			return nil, err
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
	Kind   string
	Amount int64

	// Arrears is how the account's arrears move with it: positive when usage outran credit,
	// negative when a grant paid them down. Written in the same statement as the balance, so
	// Σ lots − arrears − receivable = balance holds at every commit rather than eventually.
	Arrears int64

	// Receivable is the same for an invoiced account's receivable (ADR 0045): positive for an
	// accrual an invoice will bill, negative for a settlement of one.
	Receivable int64

	// PeriodStart is the month a usage charge belongs to; InvoiceID the invoice a settlement
	// settles. Both absent on everything else.
	PeriodStart *time.Time
	InvoiceID   int

	Description     string
	StripeReference string
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
		SetReceivableMicros(in.Receivable).
		SetIdempotencyKey(in.Key)
	if in.StripeReference != "" {
		create = create.SetStripeReference(in.StripeReference)
	}
	if in.PeriodStart != nil {
		create = create.SetPeriodStart(in.PeriodStart.UTC())
	}
	if in.InvoiceID != 0 {
		create = create.SetInvoiceID(in.InvoiceID)
	}

	entry, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("append to the ledger: %w", err)
	}

	arrears := account.ArrearsMicros + in.Arrears
	receivable := account.ReceivableMicros + in.Receivable
	if arrears < 0 || receivable < 0 {
		// Unreachable: every caller settles at most what is owed. Refused rather than clamped,
		// because a negative debt is credit nobody granted.
		return nil, fmt.Errorf("arrears would fall to %s and the receivable to %s",
			billing.USD(arrears), billing.USD(receivable))
	}
	update := tx.BillingAccount.UpdateOneID(account.ID).
		SetBalanceMicros(after).
		SetArrearsMicros(arrears).
		SetReceivableMicros(receivable)
	if state, changed := stateFor(account, arrears); changed {
		update = update.SetState(state).SetStateChangedAt(time.Now().UTC())
		account.State = state
	}
	// The automatic top-up signal rides on the same UPDATE (ADR 0041): a movement that leaves the
	// balance below the threshold asks for a top-up, and one that lifts it back clears the ask.
	// No extra read and no extra write — the row is already locked and already being written —
	// and the signal commits with the charge that caused it, or not at all.
	switch wants := WantsTopUp(account, after); {
	case wants && account.AutoTopupRequestedAt == nil:
		now := time.Now().UTC()
		update = update.SetAutoTopupRequestedAt(now)
		account.AutoTopupRequestedAt = &now
	case !wants && account.AutoTopupRequestedAt != nil:
		update = update.ClearAutoTopupRequestedAt()
		account.AutoTopupRequestedAt = nil
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("move the account's balance: %w", err)
	}
	// Kept in step in memory too, so a caller writing two movements in one transaction does not
	// compute the second one's balance from a stale row.
	account.BalanceMicros = after
	account.ArrearsMicros = arrears
	account.ReceivableMicros = receivable
	return entry, nil
}

// WantsTopUp is whether an account with this balance should be asking for an automatic top-up.
//
// Enabled, with a Stripe customer to charge, and strictly below the threshold. The same rule the
// settings route applies when automatic top-up is switched on, so an account that is already low at
// that moment is asked for straight away rather than at the next charge.
//
// Never for an invoiced account (ADR 0045): its balance is below zero by everything it has not yet
// been invoiced for, and charging a card for that would be billing it twice.
func WantsTopUp(account *ent.BillingAccount, balance int64) bool {
	return account.AutoTopupEnabled &&
		account.Collection == billingaccount.CollectionPrepaid &&
		account.StripeCustomerID != nil &&
		account.State != billingaccount.StateCanceled &&
		balance < account.AutoTopupThresholdMicros
}

// stateFor is §40.1's first step, and deliberately only the first.
//
// An account that owes goes from active to grace_period, and one that has paid comes back — from
// grace_period or from suspended — both automatic, because both are facts about the ledger.
// Suspending is not decided here: it is the grace period running out or the credit limit being
// reached, which is the suspension job's rule (ADR 0043). `restricted` and `canceled` are left exactly
// where a person put them.
//
// "Owes" is arrears, or an invoice past its due date (ADR 0045) — never a receivable that is not yet
// due. And an invoiced account that was suspended is let back in by the suspension job alone, which
// checks both of its bounds: it may have been suspended for its exposure rather than for a debt, so
// a movement finding no arrears proves nothing about it.
func stateFor(account *ent.BillingAccount, arrears int64) (billingaccount.State, bool) {
	current := account.State
	owes := arrears > 0 || account.OverdueSince != nil
	switch {
	case owes && current == billingaccount.StateActive:
		return billingaccount.StateGracePeriod, true
	case !owes && current == billingaccount.StateGracePeriod:
		return billingaccount.StateActive, true
	case !owes && current == billingaccount.StateSuspended && account.Collection == billingaccount.CollectionPrepaid:
		// Paid: a suspended account is active again the moment its debt is cleared (ADR 0043).
		return billingaccount.StateActive, true
	default:
		return current, false
	}
}

// lock reads an account and holds its row until the transaction ends.
func lock(ctx context.Context, tx *ent.Tx, accountID int) (*ent.BillingAccount, error) {
	account, err := tx.BillingAccount.Query().
		Where(billingaccount.ID(accountID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock the billing account: %w", err)
	}
	return account, nil
}

// applied returns the ledger entry an idempotency key already produced, or nil.
func applied(ctx context.Context, tx *ent.Tx, key string) (*ent.LedgerEntry, error) {
	existing, err := tx.LedgerEntry.Query().
		Where(ledgerentry.IdempotencyKey(key)).Only(ctx)
	switch {
	case err == nil:
		return existing, nil
	case ent.IsNotFound(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("look up an existing charge: %w", err)
	}
}

// take applies draws to their lots.
//
// Guarded on the remaining amount, so a draw can never take more than a lot holds. With the account
// locked nothing else should be spending from these lots, so a draw that moved no row is a bug
// rather than a race — and it fails the whole charge rather than silently taking less.
func take(ctx context.Context, tx *ent.Tx, draws []Draw) error {
	for _, draw := range draws {
		moved, err := tx.CreditLot.Update().
			Where(creditlot.ID(draw.LotID), creditlot.RemainingMicrosGTE(draw.Amount)).
			AddRemainingMicros(-draw.Amount).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("spend from lot %d: %w", draw.LotID, err)
		}
		if moved != 1 {
			return fmt.Errorf("%w: lot %d changed while this charge was being applied",
				ErrInsufficient, draw.LotID)
		}
	}
	return nil
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
