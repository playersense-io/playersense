package wallet_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// The ordering rules, tested without a database, because they are arithmetic rather than storage.
//
// Every case below is a sentence somebody would say about their statement. That is deliberate: a
// wallet is only correct if the order it spends in is the order a customer would have predicted.

var day = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func at(days int) time.Time { return day.AddDate(0, 0, days) }

func ptr(t time.Time) *time.Time { return &t }

func dollars(n int64) int64 { return n * billing.Micros }

// TestPromoCreditIsSpentBeforeMoneyTheCustomerPaid is the rule with the most money riding on it.
//
// A promo is the only credit that can be lost. Spending a customer's own balance while a trial
// credit ticks down to nothing is charging them for something they had already been given, and the
// way they find out is by reading a statement weeks later.
func TestPromoCreditIsSpentBeforeMoneyTheCustomerPaid(t *testing.T) {
	// The purchase is older, which is the case that would go wrong if the order were simply
	// chronological.
	lots := []wallet.Lot{
		{ID: 1, Kind: wallet.Purchase, Remaining: dollars(40), GrantedAt: at(-30)},
		{ID: 2, Kind: wallet.Promo, Remaining: dollars(100), ExpiresAt: ptr(at(60)), GrantedAt: at(-1)},
	}

	draws, err := wallet.Plan(lots, dollars(60), day)
	if err != nil {
		t.Fatalf("Plan = %v", err)
	}
	if len(draws) != 1 || draws[0].LotID != 2 || draws[0].Amount != dollars(60) {
		t.Fatalf("draws = %+v, want $60 from the promo alone", draws)
	}

	// And past the promo, the purchase pays the rest — one charge, two lots, in that order.
	draws, err = wallet.Plan(lots, dollars(130), day)
	if err != nil {
		t.Fatalf("Plan = %v", err)
	}
	want := []wallet.Draw{
		{LotID: 2, Kind: wallet.Promo, Amount: dollars(100)},
		{LotID: 1, Kind: wallet.Purchase, Amount: dollars(30)},
	}
	if len(draws) != 2 || draws[0] != want[0] || draws[1] != want[1] {
		t.Errorf("draws = %+v, want %+v", draws, want)
	}
}

// TestTheSoonestPromoToExpireIsSpentFirst is the rule that stops credit being wasted.
//
// Spend a year-long promo while a week-long one is open and the week-long one lapses having paid for
// nothing. The customer is out of pocket by the amount we chose not to use, and nothing in the
// product ever said so.
func TestTheSoonestPromoToExpireIsSpentFirst(t *testing.T) {
	lots := []wallet.Lot{
		// Granted first, expires last. Insertion order and expiry order disagree, which is
		// the whole point of the case.
		{ID: 1, Kind: wallet.Promo, Remaining: dollars(50), ExpiresAt: ptr(at(300)), GrantedAt: at(-40)},
		{ID: 2, Kind: wallet.Promo, Remaining: dollars(50), ExpiresAt: ptr(at(7)), GrantedAt: at(-2)},
	}

	draws, err := wallet.Plan(lots, dollars(50), day)
	if err != nil {
		t.Fatalf("Plan = %v", err)
	}
	if draws[0].LotID != 2 {
		t.Errorf("spent lot %d first; the one expiring in a week is lot 2", draws[0].LotID)
	}

	// A promo with no expiry sorts last among promos: it is the one that cannot be wasted by
	// waiting, so everything that can be is spent ahead of it.
	open := append(lots, wallet.Lot{
		ID: 3, Kind: wallet.Promo, Remaining: dollars(50), GrantedAt: at(-90),
	})
	order := wallet.Order(open)
	if order[len(order)-1].ID != 3 {
		t.Errorf("order = %v, want the never-expiring promo last", ids(order))
	}
}

// TestPurchasedCreditIsSpentOldestFirst is what a customer assumes, and it keeps the most recent
// top-up refundable for longest.
func TestPurchasedCreditIsSpentOldestFirst(t *testing.T) {
	lots := []wallet.Lot{
		{ID: 1, Kind: wallet.Purchase, Remaining: dollars(20), GrantedAt: at(-1)},
		{ID: 2, Kind: wallet.Purchase, Remaining: dollars(20), GrantedAt: at(-60)},
		{ID: 3, Kind: wallet.Purchase, Remaining: dollars(20), GrantedAt: at(-30)},
	}

	draws, err := wallet.Plan(lots, dollars(50), day)
	if err != nil {
		t.Fatalf("Plan = %v", err)
	}
	got := []int{draws[0].LotID, draws[1].LotID, draws[2].LotID}
	if got[0] != 2 || got[1] != 3 || got[2] != 1 {
		t.Errorf("spent %v, want oldest first: 2, 3, 1", got)
	}
	if draws[2].Amount != dollars(10) {
		t.Errorf("the last draw took %s, want $10", billing.USD(draws[2].Amount))
	}
}

// TestExpiredCreditIsNotSpentEvenBeforeTheSweepRunsIt is the safety property the whole design rests
// on.
//
// Expiry happens with the passage of time, not with a write. A balance column goes stale at midnight
// and the sweep that fixes it runs minutes later — so the spend path has to refuse a lapsed lot on
// its own, without waiting to be told.
func TestExpiredCreditIsNotSpentEvenBeforeTheSweepRuns(t *testing.T) {
	lots := []wallet.Lot{
		{ID: 1, Kind: wallet.Promo, Remaining: dollars(100), ExpiresAt: ptr(at(-1)), GrantedAt: at(-91)},
		{ID: 2, Kind: wallet.Purchase, Remaining: dollars(10), GrantedAt: at(-5)},
	}

	// The lapsed promo is still a row holding $100, and it is not part of the balance.
	if got := wallet.Available(lots, day); got != dollars(10) {
		t.Errorf("Available = %s, want $10 — the promo lapsed yesterday", billing.USD(got))
	}

	// A charge it could have covered is refused, and the refusal names both numbers rather than
	// saying "insufficient funds".
	_, err := wallet.Plan(lots, dollars(50), day)
	if !errors.Is(err, wallet.ErrInsufficient) {
		t.Fatalf("Plan = %v, want ErrInsufficient", err)
	}
	for _, want := range []string{"$10.00", "$50.00"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %s: %v", want, err)
		}
	}

	// The boundary is exclusive: a lot expiring at this instant is already gone. It has to fall
	// on one side, and this is the side that never spends credit somebody was told had expired.
	edge := []wallet.Lot{
		{ID: 1, Kind: wallet.Promo, Remaining: dollars(100), ExpiresAt: ptr(day), GrantedAt: at(-90)},
	}
	if got := wallet.Available(edge, day); got != 0 {
		t.Errorf("Available at the expiry instant = %s, want nothing", billing.USD(got))
	}
	if got := wallet.Available(edge, day.Add(-time.Nanosecond)); got != dollars(100) {
		t.Errorf("Available a nanosecond earlier = %s, want $100", billing.USD(got))
	}
}

// TestAChargeTooLargeIsRefusedWholeRatherThanPartly.
//
// Taking $90 of a $100 charge leaves three states that each look like a different bug: a balance of
// zero, a charge that did not happen, and no record of the $10.
func TestAChargeTooLargeIsRefusedWholeRatherThanPartly(t *testing.T) {
	lots := []wallet.Lot{
		{ID: 1, Kind: wallet.Promo, Remaining: dollars(90), ExpiresAt: ptr(at(30)), GrantedAt: at(-1)},
	}

	if _, err := wallet.Plan(lots, dollars(100), day); !errors.Is(err, wallet.ErrInsufficient) {
		t.Fatalf("Plan = %v, want ErrInsufficient", err)
	}
	// Exactly the balance is fine. An off-by-one here would refuse a customer spending the last
	// of their credit, which is the moment they are most likely to be watching.
	draws, err := wallet.Plan(lots, dollars(90), day)
	if err != nil {
		t.Fatalf("spending the whole balance = %v", err)
	}
	if draws[0].Amount != dollars(90) {
		t.Errorf("draw = %s, want the whole $90", billing.USD(draws[0].Amount))
	}

	// Zero and negative are refused rather than treated as nothing happening: a zero-amount
	// charge is a form submitted wrongly, and a negative one is a credit spelled as a debit.
	for _, amount := range []int64{0, dollars(-5)} {
		if _, err := wallet.Plan(lots, amount, day); !errors.Is(err, wallet.ErrNotPositive) {
			t.Errorf("Plan(%s) = %v, want ErrNotPositive", billing.USD(amount), err)
		}
	}
}

// TestTheOrderIsTotal is about reproducibility rather than fairness.
//
// Two lots granted in the same second with the same expiry have to be consumed in a fixed order, or
// one charge produces two different statements on two runs and neither can be reproduced.
func TestTheOrderIsTotal(t *testing.T) {
	same := ptr(at(30))
	lots := []wallet.Lot{
		{ID: 7, Kind: wallet.Promo, Remaining: dollars(10), ExpiresAt: same, GrantedAt: day},
		{ID: 3, Kind: wallet.Promo, Remaining: dollars(10), ExpiresAt: same, GrantedAt: day},
		{ID: 5, Kind: wallet.Promo, Remaining: dollars(10), ExpiresAt: same, GrantedAt: day},
	}

	first := ids(wallet.Order(lots))
	for range 20 {
		// Shuffled by reversing, which is enough to break a sort that relied on input order.
		reversed := make([]wallet.Lot, 0, len(lots))
		for i := len(lots) - 1; i >= 0; i-- {
			reversed = append(reversed, lots[i])
		}
		if got := ids(wallet.Order(reversed)); !equal(got, first) {
			t.Fatalf("order depends on input: %v then %v", first, got)
		}
	}
	if first[0] != 3 {
		t.Errorf("order = %v; the id is the last tiebreak, so 3 comes first", first)
	}
}

// TestNinetyDaysUnlessSomebodySaysOtherwise is the expiry policy.
func TestNinetyDaysUnlessSomebodySaysOtherwise(t *testing.T) {
	// The default.
	got, err := wallet.Term(wallet.Promo, day, nil)
	if err != nil {
		t.Fatalf("Term = %v", err)
	}
	if want := day.Add(wallet.PromoTerm); !got.Equal(want) {
		t.Errorf("a promo expires %s, want %s", got, want)
	}
	if wallet.PromoTerm != 90*24*time.Hour {
		t.Errorf("PromoTerm = %v, want ninety days", wallet.PromoTerm)
	}

	// An operator's own date, which is the whole of "ninety days unless we agreed otherwise".
	chosen := day.AddDate(1, 0, 0)
	got, err = wallet.Term(wallet.Promo, day, &chosen)
	if err != nil || !got.Equal(chosen) {
		t.Errorf("Term(chosen) = %v, %v; want the date given", got, err)
	}

	// A date at or before the grant is refused. It would create a lot that is already lapsed —
	// a $100 grant that nobody can spend and that the next sweep writes off, which looks like
	// the money was taken away.
	for _, bad := range []time.Time{day, day.AddDate(0, 0, -1)} {
		if _, err := wallet.Term(wallet.Promo, day, &bad); err == nil {
			t.Errorf("Term accepted an expiry of %s for a grant at %s", bad, day)
		}
	}

	// Purchased credit never expires, and naming a date for it is refused rather than ignored.
	// Money a customer paid for is not ours to put a clock on.
	if got, err := wallet.Term(wallet.Purchase, day, nil); err != nil || got != nil {
		t.Errorf("Term(purchase) = %v, %v; want no expiry", got, err)
	}
	if _, err := wallet.Term(wallet.Purchase, day, &chosen); err == nil {
		t.Error("Term accepted an expiry on purchased credit")
	}
}

// TestWhatExpiresSoonIsAnswerable is the number a dashboard shows beside the balance.
//
// A balance without it is a balance a studio plans against and then loses.
func TestWhatExpiresSoon(t *testing.T) {
	lots := []wallet.Lot{
		{ID: 1, Kind: wallet.Promo, Remaining: dollars(30), ExpiresAt: ptr(at(20)), GrantedAt: at(-70)},
		{ID: 2, Kind: wallet.Promo, Remaining: dollars(40), ExpiresAt: ptr(at(3)), GrantedAt: at(-87)},
		{ID: 3, Kind: wallet.Purchase, Remaining: dollars(50), GrantedAt: at(-5)},
		{ID: 4, Kind: wallet.Promo, Remaining: dollars(60), ExpiresAt: ptr(at(-1)), GrantedAt: at(-91)},
	}

	within := wallet.Expiring(lots, day, at(7))
	if len(within) != 1 || within[0].ID != 2 {
		t.Fatalf("expiring within a week = %v, want just lot 2", ids(within))
	}

	// Purchased credit never appears: it does not expire, so it cannot be about to.
	// Already-lapsed credit does not either — it is gone, not going.
	for _, lot := range wallet.Expiring(lots, day, at(365)) {
		if lot.Kind == wallet.Purchase {
			t.Error("purchased credit was reported as expiring")
		}
		if lot.ID == 4 {
			t.Error("credit that already lapsed was reported as expiring")
		}
	}
	// Soonest first, so a dashboard reads the top of the list.
	soon := wallet.Expiring(lots, day, at(365))
	if len(soon) != 2 || soon[0].ID != 2 || soon[1].ID != 1 {
		t.Errorf("expiring = %v, want lot 2 then lot 1", ids(soon))
	}
}

func ids(lots []wallet.Lot) []int {
	out := make([]int, 0, len(lots))
	for _, lot := range lots {
		out = append(out, lot.ID)
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
