package query

import (
	"testing"

	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

func row(size, players, payers uint64) protocol.CohortRow {
	return protocol.CohortRow{
		CohortSize: size, Players: players, Payers: payers,
		RevenueMicros: 1_000_000, Purchases: 3,
	}
}

// TestAThinCellIsSuppressedNotZeroed is §32.2 and ADR 0017's central claim.
//
// The distinction is the whole point: a zero says nobody came back, and a dashboard drawing
// it will say so out loud. "Suppressed" says the query will not answer, which is true.
func TestAThinCellIsSuppressedNotZeroed(t *testing.T) {
	kept, suppressed := Suppression{K: 5}.Apply([]protocol.CohortRow{
		row(100, 40, 10),
		row(100, 3, 0), // three players is three people
		row(100, 60, 20),
	})
	if suppressed != 1 {
		t.Fatalf("suppressed %d cells, want 1", suppressed)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d rows, want 2", len(kept))
	}
	for _, r := range kept {
		if r.Players == 0 {
			t.Fatal("a suppressed cell was returned as a zero rather than withheld")
		}
		if r.Players < 5 {
			t.Fatalf("a cell of %d survived a threshold of 5", r.Players)
		}
	}
}

// TestAZeroIsNotSuppressed keeps the curve honest.
//
// A count of nobody identifies nobody. Withholding it would put a gap in the chart where a
// real, meaningful zero belongs — and a reader cannot tell a withheld cell from a missing
// one.
func TestAZeroIsNotSuppressed(t *testing.T) {
	kept, suppressed := Suppression{K: 5}.Apply([]protocol.CohortRow{row(100, 0, 0)})
	if suppressed != 0 || len(kept) != 1 {
		t.Fatalf("a genuine zero was withheld: kept %d, suppressed %d", len(kept), suppressed)
	}
}

// TestAThinCohortWithholdsEveryOffset is the denominator half of the rule.
//
// A cohort of three is three identifiable people whatever the numerator says, so no cell
// resting on it can be shown — not even one where many of them returned.
func TestAThinCohortWithholdsEveryOffset(t *testing.T) {
	var rows []protocol.CohortRow
	for offset := range 10 {
		r := row(3, 3, 0)
		r.DayOffset = offset
		rows = append(rows, r)
	}
	kept, suppressed := Suppression{K: 5}.Apply(rows)
	if len(kept) != 0 {
		t.Fatalf("%d cells of a three-player cohort were disclosed", len(kept))
	}
	if suppressed != 10 {
		t.Fatalf("suppressed %d, want all 10", suppressed)
	}
}

// TestAThinPayerCountDropsOnlyTheMoney covers the case a population test alone would miss.
//
// Two payers inside a cohort of two hundred is a thick cell holding a thin subgroup. The
// retention number describes the two hundred and is safe; the revenue describes the two and
// is not. Dropping the row would withhold a legitimate number, so the money goes and the
// row stays.
func TestAThinPayerCountDropsOnlyTheMoney(t *testing.T) {
	kept, suppressed := Suppression{K: 5}.Apply([]protocol.CohortRow{row(200, 150, 2)})
	if len(kept) != 1 {
		t.Fatalf("the row was withheld entirely; only the money should have gone")
	}
	if suppressed != 1 {
		t.Fatalf("the withheld money was not counted: %d", suppressed)
	}
	got := kept[0]
	if got.Players != 150 {
		t.Fatalf("the population number was lost: %d", got.Players)
	}
	if got.Payers != 0 || got.RevenueMicros != 0 || got.Purchases != 0 {
		t.Fatalf("money describing two people survived: %+v", got)
	}
}

// TestPayerConversionSuppressesOnItsOwnNumerator.
//
// When payers *is* the measure, a thin payer count is the thin cell — dropping the money
// and keeping the row would leave a conversion rate computed from a numerator that was
// withheld.
func TestPayerConversionSuppressesOnItsOwnNumerator(t *testing.T) {
	kept, suppressed := Suppression{K: 5, Metric: schemas.MetricPayerConversion}.
		Apply([]protocol.CohortRow{row(200, 150, 2)})
	if len(kept) != 0 || suppressed != 1 {
		t.Fatalf("a two-payer conversion cell was disclosed: kept %d", len(kept))
	}
}
