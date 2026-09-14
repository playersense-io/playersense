# 0017. A thin aggregate is suppressed at query time, not at write time

**Status:** Accepted
**Date:** 2026-09-06
**Relates to:** project.md §32.2, §102

## Context

An aggregate is anonymous only while it is thick enough to be. A retention cell holding one
player, in one country, on one app version, identifies that person exactly as well as their
identifier would — and it does so in a table nominally containing no identifiers.

Breakdowns multiply, so thin cells are not an edge case: they are the natural consequence
of letting a customer slice by country and version and platform at once.

## Decision

A minimum cell size is enforced **at the query layer**. A cell below the threshold is
**suppressed and reported as suppressed**, never rendered as a zero.

Cohort aggregates below the threshold are treated as personal data, not assumed anonymous.

## Consequences

- The stored cells stay complete, so a suppressed cell still contributes to the larger cell
  it rolls up into. Totals remain correct even where a breakdown is not shown.
- The threshold can be raised later without recomputing history — which matters, because
  the right number is a judgement that may change.
- Reporting suppression rather than zero is deliberate. A zero is a claim about the world;
  "suppressed" is a claim about the query, and only one of them is true.

## Alternatives considered

**Suppress at write time by not storing thin cells.** Destroys the rollup: the larger cell
would be missing its contribution, so totals would silently stop matching.

**Round or add noise.** Differential privacy is a real answer to a different question, and
applied casually it produces numbers that are wrong in ways a customer cannot reason about.
For a product whose value is that its numbers are trustworthy, that is the wrong trade.

**Leave it to the customer.** The customer is a studio, not a privacy team, and the thin
cell is created by the product offering the breakdown.

## Enforcement

- Query layer, per §32.2. The reporting API is not yet built; this record exists so it is
  built with the threshold rather than retrofitted.
