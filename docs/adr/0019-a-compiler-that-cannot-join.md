# 0019. The cohort compiler has no join to give

**Status:** Accepted
**Date:** 2026-09-14
**Relates to:** project.md §32.1, §32.2, §57.1, §69, Appendix C #3, #16

## Context

Every cohort query in §29 reads a pre-aggregated fact. The facts exist so that the question
"who arrived on day C, and what did they do N days later" never becomes a join between one
row per player and one row per event — the join that makes retention expensive and, at the
document's scale, impossible on the Stage 1 node.

§32.1 does not ask for a planner that avoids joins. It asks for a compiler that cannot emit
one, and gives the reason: *a limit that can be exceeded under load is a limit that will
be.* The pressure is real and predictable — the first customer question the facts cannot
answer will arrive with a plausible argument for one exception.

There is a second rule pulling the same way. Appendix C #3 requires that no customer query
execute without tenant scope, and §69 says the compiler injects the predicate rather than
trusting the caller. Both are the same shape of problem: a property that must hold for every
query ever compiled, enforced by something other than remembering.

## Decision

**The builder has no join.** No method adds a second table; the `FROM` clause can only hold
a value of the closed `Fact` type, which has five members. A planner that wanted a join
could not express one.

**The tenant is a parameter of compilation.** `Compile` takes a `Scope`, and every statement
it produces carries `project_seq` and `environment_seq` bound to that scope's values before
any other predicate. There is no code path that compiles a statement without them.

**Denominators are a second single-table query, divided in Go.** §29.9 would permit joining
`cohort_sizes` — it is a bounded lookup, a few hundred thousand rows, not one per player —
and `cmd/cohortcheck` does exactly that. The read path does not, because the cost is one
round trip and the benefit is that "no cohort query joins" needs no exception attached to
it. Appendix C #16 draws precisely this line when it adds *"and no resident dictionary
stands in for such a join"*.

**A question the facts cannot answer is refused with the reason.** Not approximated, not
served empty.

## Consequences

- One combination is unavailable and its absence is now explicit: a breakdown by one
  dimension filtered by another. `cohort_metrics_breakdown_daily` carries one dimension at a
  time (§29.13), so "iOS players broken down by country" is a cartesian cell. It is refused
  with a message naming both dimensions.
- Every rate costs two round trips instead of one. At the facts' size — §29.12 puts
  `cohort_metrics_daily` at about 34k rows per project per environment over three years —
  this is not measurable. If it ever is, §29.9's dictionary is still available and this
  record should be superseded rather than quietly bent.
- A hole remains, and naming it is worth more than pretending otherwise: a column expression
  is a string, and a string can contain the word `JOIN`. The type system cannot close that;
  a test reading the emitted SQL can.

## Alternatives considered

**A planner that checks for joins before executing.** This is what §32.1 rejects by name. A
check is a branch, a branch has an else, and the else is where the exception goes.

**Allow the bounded join for denominators only.** Defensible — §29.9 permits it and
cohortcheck relies on it. Rejected because the rule then becomes "no joins except the
allowed ones", and every future exception argues from the first one. A rule with no
exceptions is enforceable by a grep; a rule with one is enforceable by review.

**Trust the tenant from the request body, validated against the key.** Equivalent when
correct and catastrophic when not, and the failure is silent: a query scoped to another
project returns rows rather than an error. The body's tenant fields are refused instead.

## Enforcement

- `apps/backend/internal/query/sql.go` — the builder. `Fact` is closed; there is no join
  method.
- `apps/backend/internal/query/plan.go` — `scopeAnd` is applied to every statement.
- `TestACohortQueryNeverJoins` compiles all 67 shapes the planner can produce, both
  statements of each, and reads the output for a join in any form. Verified to have teeth by
  smuggling one into a column expression: all 67 fail.
- `TestTenantScopeIsAlwaysInjected` asserts both predicates, bound to the scope's values,
  on every shape.
- `TestTwoDimensionsAtOnceIsRefused` asserts the refusal names both dimensions.
- `TestScopeInTheBodyIsRefused` asserts a body naming a project is refused rather than
  ignored.
