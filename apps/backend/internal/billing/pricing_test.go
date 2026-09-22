package billing_test

import (
	"errors"
	"testing"

	"playersense.dev/backend/internal/billing"
)

func upTo(n int64) *int64 { return &n }

// launchTiers is the card PlayerSense launched with, in micros: $0.0005 to 100k, $0.0004 to 500k,
// $0.00035 to 1M, $0.0003 above.
func launchTiers() billing.Tiers {
	return billing.Tiers{
		{UpTo: upTo(100_000), UnitPriceMicros: 500},
		{UpTo: upTo(500_000), UnitPriceMicros: 400},
		{UpTo: upTo(1_000_000), UnitPriceMicros: 350},
		{UnitPriceMicros: 300},
	}
}

// Graduated: every unit is priced at the band its position falls in.
func TestAGraduatedCardPricesEachBandAtItsOwnRate(t *testing.T) {
	card := billing.Card{Mode: billing.Graduated, Tiers: launchTiers()}

	cases := []struct {
		players int64
		want    int64
	}{
		{0, 0},
		{1, 500},
		{100_000, 50 * billing.Micros},     // 100k × $0.0005
		{100_001, 50*billing.Micros + 400}, // the first unit of band two
		{500_000, 50*billing.Micros + 160*billing.Micros}, // + 400k × $0.0004
		{1_000_000, 210*billing.Micros + 175*billing.Micros},
		{1_500_000, 385*billing.Micros + 150*billing.Micros},
	}
	for _, c := range cases {
		if got := card.Total(c.players, 0); got != c.want {
			t.Errorf("Total(%d) = %s, want %s", c.players, billing.USD(got), billing.USD(c.want))
		}
	}
}

// Volume: every unit is priced at the band the total has reached — including the cliff, which is
// the reason graduated is the default.
func TestAVolumeCardPricesTheWholeMonthAtTheBandItReached(t *testing.T) {
	card := billing.Card{Mode: billing.Volume, Tiers: launchTiers()}

	if got := card.Total(100_000, 0); got != 50*billing.Micros {
		t.Errorf("100,000 players = %s, want $50", billing.USD(got))
	}
	if got := card.Total(100_001, 0); got != 100_001*400 {
		t.Errorf("100,001 players = %s, want 100,001 × $0.0004", billing.USD(got))
	}
	// Crossing the boundary is a credit: the whole month was repriced downward.
	if got := card.Charge(100_000, 100_001, 0); got >= 0 {
		t.Errorf("crossing into a cheaper band charged %s; a volume card credits there", billing.USD(got))
	}
}

// Real-time charging splits a month into arbitrary pieces. However it is split, the pieces must sum
// to the month — otherwise a busy studio and a quiet one with the same count pay different amounts.
func TestChargesInAnyPiecesSumToTheMonth(t *testing.T) {
	for _, mode := range []billing.Mode{billing.Graduated, billing.Volume} {
		card := billing.Card{Mode: mode, Tiers: launchTiers()}
		const included = 10_000
		steps := []int64{1, 9_999, 1, 89_999, 3, 400_000, 7, 499_990, 250_000}

		var n, sum int64
		for _, k := range steps {
			sum += card.Charge(n, n+k, included)
			n += k
		}
		if want := card.Total(n, included); sum != want {
			t.Errorf("%s: pieces summed to %s, the month is %s", mode, billing.USD(sum), billing.USD(want))
		}
	}
}

// Included units are free and still occupy positions: the 100,000 boundary does not move because
// the plan gave the first 10,000 away.
func TestIncludedUnitsAreFreeAndDoNotMoveTheBoundaries(t *testing.T) {
	card := billing.Card{Mode: billing.Graduated, Tiers: launchTiers()}

	if got := card.Total(10_000, 10_000); got != 0 {
		t.Errorf("the included players cost %s", billing.USD(got))
	}
	if got := card.Charge(10_000, 10_001, 10_000); got != 500 {
		t.Errorf("the first paid player cost %s, want $0.0005", billing.UnitPrice(got))
	}
	// 90,000 paid in band one, then band two starts at 100,001 exactly as the page says.
	if got := card.Charge(100_000, 100_001, 10_000); got != 400 {
		t.Errorf("the 100,001st player cost %s, want $0.0004", billing.UnitPrice(got))
	}
}

func TestATierSetThatCannotPriceEveryCountIsRefused(t *testing.T) {
	cases := map[string]billing.Tiers{
		"empty":                   {},
		"bounded last":            {{UpTo: upTo(100), UnitPriceMicros: 1}},
		"unbounded in the middle": {{UnitPriceMicros: 2}, {UnitPriceMicros: 1}},
		"not ascending": {
			{UpTo: upTo(500), UnitPriceMicros: 2}, {UpTo: upTo(100), UnitPriceMicros: 1},
			{UnitPriceMicros: 1},
		},
		"negative price": {{UnitPriceMicros: -1}},
		"absurd price":   {{UnitPriceMicros: billing.MaxUnitPrice + 1}},
	}
	for name, tiers := range cases {
		var tierErr *billing.TierError
		if err := tiers.Validate(); !errors.As(err, &tierErr) {
			t.Errorf("%s: validated (err=%v)", name, err)
		}
	}
	if err := launchTiers().Validate(); err != nil {
		t.Errorf("the launch card was refused: %v", err)
	}
}

func TestAUnitPriceRendersBelowACent(t *testing.T) {
	cases := map[int64]string{500: "$0.0005", 350: "$0.00035", 1_000_000: "$1.00", 1_500_000: "$1.50", 0: "$0.00"}
	for micros, want := range cases {
		if got := billing.UnitPrice(micros); got != want {
			t.Errorf("UnitPrice(%d) = %q, want %q", micros, got, want)
		}
	}
}
