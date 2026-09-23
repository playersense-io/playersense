package wallet

import (
	"context"
	"fmt"
	"slices"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/creditlot"
)

// Taking back credit whose money went back: a refund, or a dispute the customer's bank opened.
//
// # What is taken, in what order
//
//  1. what is left of the lot the payment created;
//  2. then other *purchased* credit, oldest first;
//  3. what purchased credit cannot cover becomes arrears.
//
// Why that order: the payment bought a specific lot, so that lot is what the refund unwinds first. If
// the customer already spent some of it, the players it paid for were served — the money for them now
// has to come from the customer's other money, and failing that it is owed. **Promotional credit is
// never taken**: it was never paid for, so returning money cannot be a reason to lose it, and spending
// a promo to fund a refund would be handing the customer cash for a discount.
//
// Idempotent on the caller's key — the Stripe refund or dispute id — so every redelivery of the event
// and every other event reporting the same refund is one reversal.

// ReverseRequest is one reversal.
type ReverseRequest struct {
	AccountID int
	Lot       int   // the lot the payment created
	Amount    int64 // positive micros to take back

	Description     string
	StripeReference string
	At              time.Time
	Key             string
}

// ReverseResult is what a reversal took.
type ReverseResult struct {
	Entry   *ent.LedgerEntry
	Draws   []Draw
	Owed    int64 // the part no purchased credit covered, now arrears
	Applied bool  // false when the key had already been used
}

// Reverse takes back credit for money that was returned. One `refund` ledger entry.
func Reverse(ctx context.Context, tx *ent.Tx, in ReverseRequest) (*ReverseResult, error) {
	switch {
	case in.Amount <= 0:
		return nil, fmt.Errorf("%w: a reversal of %d", ErrNotPositive, in.Amount)
	case in.Key == "":
		return nil, fmt.Errorf("a reversal needs an idempotency key derived from what was returned")
	}
	account, err := lock(ctx, tx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if existing, err := applied(ctx, tx, in.Key); err != nil || existing != nil {
		return &ReverseResult{Entry: existing}, err
	}

	rows, err := tx.CreditLot.Query().
		Where(creditlot.AccountID(in.AccountID), creditlot.KindEQ(creditlot.KindPurchase),
			creditlot.RemainingMicrosGT(0)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read purchased credit: %w", err)
	}
	lots := make([]Lot, 0, len(rows))
	for _, row := range rows {
		lots = append(lots, lotOf(row))
	}
	// The payment's own lot first, then the rest in the usual purchase order.
	ordered := Order(lots)
	slices.SortStableFunc(ordered, func(a, b Lot) int {
		switch {
		case a.ID == in.Lot && b.ID != in.Lot:
			return -1
		case b.ID == in.Lot && a.ID != in.Lot:
			return 1
		}
		return 0
	})

	var (
		draws     []Draw
		remaining = in.Amount
	)
	for _, lot := range ordered {
		if remaining == 0 {
			break
		}
		take := min(lot.Remaining, remaining)
		draws = append(draws, Draw{LotID: lot.ID, Kind: lot.Kind, Amount: take})
		remaining -= take
	}
	if err := take(ctx, tx, draws); err != nil {
		return nil, err
	}

	entry, err := record(ctx, tx, account, movement{
		Kind:            "refund",
		Amount:          -in.Amount,
		Arrears:         remaining,
		Description:     in.Description,
		StripeReference: in.StripeReference,
		Key:             in.Key,
	})
	if err != nil {
		return nil, err
	}
	return &ReverseResult{Entry: entry, Draws: draws, Owed: remaining, Applied: true}, nil
}

// Reversed reports whether a key has already taken credit back — so a refund that later fails, or a
// dispute that is later won, is restored only if it was taken in the first place.
func Reversed(ctx context.Context, tx *ent.Tx, key string) (*ent.LedgerEntry, error) {
	return applied(ctx, tx, key)
}
