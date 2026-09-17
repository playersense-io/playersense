# 07. Aggregate disclosure

## An aggregate is anonymous only while it is thick

A retention cell holding one player, in one country, on one app version, identifies that
person exactly as well as their identifier would. It does so in a table that nominally
contains no identifiers, which is what makes it easy to miss.

Thin cells are not an edge case. They are the direct consequence of letting a customer slice
by country and platform and app version at once — the product creates them by offering the
breakdown.

## The rule

**Cohort aggregates below a minimum cell size are treated as personal data, not assumed
anonymous.**

A cell below the threshold is **suppressed and reported as suppressed**, never rendered as
a zero.

## Suppression happens at query time

Two properties make that the right layer, and both are lost by suppressing at write time:

- **The stored cells stay complete**, so a suppressed cell still contributes to the larger
  cell it rolls up into. Totals remain correct even where a breakdown is not shown. Dropping
  thin cells at write time silently breaks every rollup above them.
- **The threshold can be raised without recomputing history**, which matters because the
  right number is a judgement that may change — and may differ per regime.

## Suppressed, not zero

Reporting suppression rather than zero is deliberate. A zero is a claim about the world;
"suppressed" is a claim about the query. Only one of them is true, and a dashboard that
renders the false one is a dashboard producing confidently wrong analysis.

## What was rejected

**Noise or rounding.** Differential privacy is a real answer to a different question. Applied
casually it produces numbers wrong in ways a customer cannot reason about, and for a product
whose entire value is that its numbers are trustworthy, that is the wrong trade.

**Leaving it to the customer.** The customer is a studio, not a privacy team, and the thin
cell was created by the product offering the breakdown.

See ADR 0017.

## Status

**Built**, and the threshold shipped with the reporting API rather than after it — which is
what this record asked for. `apps/backend/internal/query/suppress.go` applies it to the
response in one place, `QUERY_SUPPRESSION_K` sets it, and it defaults to 5.

Both query shapes are covered: the cohort path and the ordinary time series suppress on the
same rule, because an active-player count is a count of people whatever it is keyed on. The
tests are named in [09-controls-map.md](09-controls-map.md) under *Disclosure* and *Query
safety*.
