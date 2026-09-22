// Package wallet is prepaid credit that can expire.
//
// The account's balance is not a number somebody keeps up to date; it is the sum of what is left in
// each grant. That shape exists for one reason: **promotional credit expires and purchased credit
// does not.** A scalar balance of $140 cannot tell you that $100 of it lapses on Friday, and
// therefore cannot take the right half away when Friday comes.
//
// # The order credit is spent in
//
//  1. promotional credit, soonest expiry first
//  2. purchased credit, oldest grant first
//
// **Promos before purchases**, because a promo is the only credit that can be lost. Spending a
// customer's own money while a trial credit ticks down to nothing is charging them for something
// they had already been given, and the way they find out is by reading a statement.
//
// **Soonest expiry first within promos**, because the alternative wastes credit by construction:
// spend a year-long promo while a week-long one is open and the week-long one lapses having paid
// for nothing. Nobody asked would choose that, which is why it is not left to insertion order.
//
// **Oldest first within purchases**, because that is what a customer assumes, and because it keeps
// the most recent top-up refundable for longest.
//
// # Every movement is two writes in one transaction
//
// A lot's `remaining_micros` never changes without a ledger entry beside it, and the entry carries
// the balance after. That is what makes "why is my balance this" answerable from the table rather
// than from a reconstruction, and it is why every function here takes a transaction rather than a
// client.
package wallet

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/internal/billing"
)

// PromoTerm is how long granted credit lasts when nobody says otherwise.
//
// Ninety days. Long enough that a studio evaluating PlayerSense through a quarter has the whole
// quarter, and short enough that credit granted for a conversation two years ago is not still
// sitting on an account nobody remembers. An operator may name any date instead — the default is a
// default, not a policy.
const PromoTerm = 90 * 24 * time.Hour

// Errors a wallet operation can fail with.
var (
	// ErrInsufficient is a spend larger than what is available. The error names both numbers,
	// because "insufficient funds" without them is a support ticket.
	ErrInsufficient = errors.New("not enough credit")

	// ErrNotPositive is a grant or a spend of zero or less. Refused rather than treated as a
	// no-op: a zero-amount grant is a form somebody submitted wrongly, and a negative one is
	// an attempt to spell a debit as a credit.
	ErrNotPositive = errors.New("an amount has to be more than zero")

	// ErrCurrencyMismatch is a grant in a currency the account does not hold. There is no
	// conversion here and there should not be: a rate applied at grant time is a rate somebody
	// disputes at spend time.
	ErrCurrencyMismatch = errors.New("that is not this account's currency")

	// ErrBadExpiry is a date that cannot be what somebody meant: one on purchased credit, or one
	// already past at the moment of the grant.
	//
	// A sentinel rather than a bare error because the HTTP layer has to tell it from a storage
	// failure — the caller can fix a date and cannot fix a database, so one is a 422 and the
	// other a 500. Without this the refusal for "purchases do not expire" was an internal error.
	ErrBadExpiry = errors.New("that expiry cannot be right")
)

// Lot is one grant as the wallet reasons about it.
//
// A value rather than the ent row, so the ordering rules can be tested without a database. What
// spends credit is a sequence of these; what stores it is the table.
type Lot struct {
	ID        int
	Kind      string
	Remaining int64
	ExpiresAt *time.Time
	GrantedAt time.Time
}

// Kinds of credit. Purchased credit is money; promotional credit is a discount.
const (
	Purchase = "purchase"
	Promo    = "promo"
)

// Spendable reports whether a lot can pay for something at a given moment.
//
// Expiry is `at or after`, so a lot expiring at midnight is not spendable at midnight. The
// boundary has to fall somewhere and this is the side that never spends credit the customer was
// told had gone.
func (l Lot) Spendable(at time.Time) bool {
	if l.Remaining <= 0 {
		return false
	}
	return l.ExpiresAt == nil || at.Before(*l.ExpiresAt)
}

// Order sorts lots into the sequence credit is spent in.
//
// Total and deterministic, which matters more than it looks: two lots granted in the same second
// with the same expiry must be consumed in a fixed order, or the same spend produces different
// statements on two runs and neither can be reproduced. The id breaks every remaining tie.
func Order(lots []Lot) []Lot {
	out := slices.Clone(lots)
	slices.SortStableFunc(out, func(a, b Lot) int {
		// Promos first, whatever their dates: a promo is the only credit that can be lost.
		if byKind := rank(a.Kind) - rank(b.Kind); byKind != 0 {
			return byKind
		}
		if a.Kind == Promo {
			// Soonest expiry first. A promo with no expiry — unusual, and possible if an
			// operator grants one open-ended — sorts last among promos, because it is the
			// one that cannot be wasted by waiting.
			if byExpiry := compareExpiry(a.ExpiresAt, b.ExpiresAt); byExpiry != 0 {
				return byExpiry
			}
		}
		// Oldest grant first. For purchases this is the rule; for two promos sharing an
		// expiry it is the tiebreak, and it is the same sentence either way.
		if byGrant := a.GrantedAt.Compare(b.GrantedAt); byGrant != 0 {
			return byGrant
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// rank puts promotional credit ahead of purchased credit.
func rank(kind string) int {
	if kind == Promo {
		return 0
	}
	return 1
}

// compareExpiry orders two expiries with "never" last.
//
// Never is last rather than first because a lot that cannot lapse loses nothing by being spent
// later, and every lot that can should be spent while it still counts.
func compareExpiry(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return a.Compare(*b)
	}
}

// Available is what can be spent at a given moment.
//
// Computed rather than read from a column, and that is the whole point of the design: expiry
// happens with the passage of time rather than with a write, so a stored total is correct only
// until midnight. `BillingAccount.balance_micros` is the ledger's running total — it agrees with
// this number once the expiry sweep has caught up, and the difference between them is precisely
// credit that has lapsed and not yet been written off.
func Available(lots []Lot, at time.Time) int64 {
	var total int64
	for _, lot := range lots {
		if lot.Spendable(at) {
			total += lot.Remaining
		}
	}
	return total
}

// Draw is one lot's contribution to a spend.
type Draw struct {
	LotID  int
	Kind   string
	Amount int64
}

// Plan decides which lots pay for an amount, without writing anything.
//
// Separated from the write so the arithmetic is testable on its own and so a caller can show
// somebody what a charge would consume before making it. Returns the draws in the order they were
// taken, which is the order a statement reads.
//
// A spend larger than what is available is refused rather than partially applied. Taking $90 of a
// $100 charge would leave a balance of zero, a charge that did not happen, and no record of the
// $10 — three states that each look like a different bug.
func Plan(lots []Lot, amount int64, at time.Time) ([]Draw, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotPositive, billing.USD(amount))
	}
	if available := Available(lots, at); available < amount {
		return nil, fmt.Errorf("%w: %s available, %s needed",
			ErrInsufficient, billing.USD(available), billing.USD(amount))
	}

	draws, covered := Cover(lots, amount, at)
	if covered != amount {
		// Unreachable: Available already agreed there was enough. Reported rather than
		// ignored, because the alternative is a spend that silently took less than it said.
		return nil, fmt.Errorf("%w: %s short after consuming every lot",
			ErrInsufficient, billing.USD(amount-covered))
	}
	return draws, nil
}

// Cover takes as much of an amount as the lots can pay, in [Order], and says how much that was.
//
// Plan's partial sibling, for the one charge that cannot be refused: usage (ADR 0039). The players
// were already served when the charge was priced, so an account that cannot cover it pays what it
// can and owes the rest as arrears — whereas an operator's charge is a decision that can wait for
// credit, and Plan refuses it whole. Same order, same arithmetic, one function under both.
func Cover(lots []Lot, amount int64, at time.Time) ([]Draw, int64) {
	var (
		draws     []Draw
		remaining = max(amount, 0)
	)
	for _, lot := range Order(lots) {
		if remaining == 0 {
			break
		}
		if !lot.Spendable(at) {
			continue
		}
		take := min(lot.Remaining, remaining)
		draws = append(draws, Draw{LotID: lot.ID, Kind: lot.Kind, Amount: take})
		remaining -= take
	}
	return draws, max(amount, 0) - remaining
}

// LotsOf reads an account's lots in the shape the rules above reason about.
//
// Takes the generated lot client rather than a whole database handle, because both `*ent.Client`
// and `*ent.Tx` expose one — so the same function serves a read for a dashboard and a read
// inside the transaction that is about to spend.
//
// Every lot with something left in it, **expired ones included**: whether a lot has lapsed is a
// question about `at`, and a query that filtered on the clock would answer differently depending on
// when it ran relative to the transaction it is inside.
func LotsOf(ctx context.Context, lots *ent.CreditLotClient, accountID int) ([]Lot, error) {
	rows, err := lots.Query().
		Where(creditlot.AccountID(accountID), creditlot.RemainingMicrosGT(0)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read credit lots: %w", err)
	}
	out := make([]Lot, 0, len(rows))
	for _, row := range rows {
		out = append(out, lotOf(row))
	}
	return out, nil
}

// lotOf is the one place a row becomes a Lot, so the two cannot drift.
func lotOf(row *ent.CreditLot) Lot {
	return Lot{
		ID:        row.ID,
		Kind:      string(row.Kind),
		Remaining: row.RemainingMicros,
		ExpiresAt: row.ExpiresAt,
		GrantedAt: row.GrantedAt,
	}
}

// Expiring reports the lots that lapse within a window, soonest first.
//
// For the one number a dashboard has to show beside a balance: "$100 expires in 6 days". A balance
// without it is a balance a studio plans against and then loses.
func Expiring(lots []Lot, from, until time.Time) []Lot {
	var out []Lot
	for _, lot := range lots {
		if !lot.Spendable(from) || lot.ExpiresAt == nil {
			continue
		}
		if lot.ExpiresAt.Before(until) {
			out = append(out, lot)
		}
	}
	slices.SortStableFunc(out, func(a, b Lot) int { return a.ExpiresAt.Compare(*b.ExpiresAt) })
	return out
}

// Term returns when a grant of the given kind expires, honouring an explicit date.
//
// Purchased credit does not expire, and naming a date for it is refused rather than accepted:
// money a customer paid for is not ours to put a clock on, and a grant that accepted one would be
// a wallet that could quietly delete somebody's money.
func Term(kind string, grantedAt time.Time, chosen *time.Time) (*time.Time, error) {
	if kind == Purchase {
		if chosen != nil {
			return nil, fmt.Errorf(
				"%w: purchased credit does not expire; only a promo can be given a date",
				ErrBadExpiry)
		}
		return nil, nil
	}
	if chosen != nil {
		if !chosen.After(grantedAt) {
			return nil, fmt.Errorf("%w: %s is not after the grant at %s", ErrBadExpiry,
				chosen.Format(time.RFC3339), grantedAt.Format(time.RFC3339))
		}
		return chosen, nil
	}
	term := grantedAt.Add(PromoTerm)
	return &term, nil
}
