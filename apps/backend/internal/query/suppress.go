package query

import (
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/schemas"
)

// DefaultThreshold is §32.2's k: the smallest population an aggregate may describe.
const DefaultThreshold = 5

// Suppression withholds cells too thin to be anonymous (§32.2, §102, ADR 0017).
//
// An aggregate is only anonymous while it is thick enough to be. A retention cell holding
// one player, in one country, on one app version, identifies that person exactly as well as
// their identifier would — and does it in a table that contains no identifiers, which is
// what makes the failure easy to miss.
//
// Three properties, each chosen against a plausible alternative:
//
//   - **Suppressed, not zeroed.** A zero is a claim about the world ("nobody came back");
//     "suppressed" is a claim about the query. Only one of them is true, and a dashboard
//     that cannot tell them apart will draw the wrong conclusion confidently.
//   - **Here, not in the derivation.** The stored cells stay complete, so a suppressed cell
//     still contributes to the larger cell it rolls up into. A cohort of three iOS players
//     in Portugal belongs in the iOS total even though it cannot be shown on its own.
//   - **A genuine zero is shown.** Withholding it would misreport the curve, and a count of
//     nobody discloses nothing.
type Suppression struct {
	K      int
	Metric string
}

// Apply withholds what it must and reports how much.
//
// The population test is on whichever count the row is *about*. cohort_size gates the whole
// row because a denominator that small identifies the cohort's members whatever the
// numerator says; players gates the cell; and payers gates the cell too when the metric is
// payer conversion, because there the payers are the measure.
//
// A thin payer count inside a thick cell is the remaining case, and it is not left open:
// the financial measures are dropped from that row rather than the row itself, so the
// retention number survives and "how much did those two people spend" does not. The wire
// types omit a zero, so a dropped measure is absent rather than reported as nothing.
func (s Suppression) Apply(rows []protocol.CohortRow) ([]protocol.CohortRow, int) {
	k := uint64(s.K)
	kept := make([]protocol.CohortRow, 0, len(rows))
	suppressed := 0

	for _, row := range rows {
		if thin(row.CohortSize, k) || thin(row.Players, k) || thin(row.PlayersCumulative, k) {
			suppressed++
			continue
		}
		if s.Metric == schemas.MetricPayerConversion && thin(row.Payers, k) {
			suppressed++
			continue
		}
		if thin(row.Payers, k) {
			// The population is thick enough to describe, but the paying part of it is
			// not. Drop what describes only them.
			row.Payers = 0
			row.RevenueMicros = 0
			row.Purchases = 0
			row.RevenueMicrosCumulative = 0
			suppressed++
		}
		kept = append(kept, row)
	}
	return kept, suppressed
}

// thin reports a count that describes somebody without describing enough of them.
//
// Zero is not thin. It names no one.
func thin(n, k uint64) bool { return n > 0 && n < k }
