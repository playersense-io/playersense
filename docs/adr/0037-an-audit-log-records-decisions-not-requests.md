# 0037. An audit log records decisions, not requests

**Status:** Accepted
**Date:** 2026-09-19
**Relates to:** project.md §08, §55.1, §56; [ADR 0035](0035-an-invitation-is-addressed-not-merely-held.md), [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md)

> **Amended by [0038](0038-one-api-one-database.md).** Staff access and the customer audit
> log now live in the same database, so the separation this record relies on is between
> two tables rather than two jurisdictions. `staff_access` is still append-only and still
> requires a ticket.

## Context

Three commits shipped pointing at the same hole.

[ADR 0035](0035-an-invitation-is-addressed-not-merely-held.md) built member removal and recorded that
**the removal itself is not recorded** — a membership is the one row this product deletes rather than
archives, so without a log "who ended Ada's access, and when" has no answer anywhere.
[ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md) built the operator credit surface and
recorded that it **cannot say who acted**. `docs/compliance/08` states three requirements for audit
records and the compliance index listed the whole thing as designed and not built.

The `audit_events` table existed, complete and correct, with every field immutable. Nothing wrote it.
`audit.read` was one of the permissions defined and guarding nothing.

## Decision

### Entries are written by hand, at the decision

**Not middleware.** Middleware records *requests*; an audit log has to record *decisions*, and the
two differ in exactly the place that matters:

- "Ada's role changed **from developer** to owner" — the previous value was never in the request.
- "version 7 published, three flags, no history lookups" — computed by the compiler mid-handler.
- "$100 of promotional credit, expiring 17 December" — the expiry was derived from a default.
- "the rollout moved from 10% to 100%" — the 10% existed only in the row being replaced.

A request log can say a `PUT` happened to a URL. None of the above survives that, and every one of
them is the sentence somebody actually needs when they ask whether a change was wrong.

### Forgetting one fails the suite

The cost of writing entries by hand is that one can be forgotten, and discipline is not a mechanism.
`TestEveryMutatingRouteDecidesAboutAudit` reads the OpenAPI document, finds every `POST`, `PUT`,
`PATCH` and `DELETE`, and fails unless each is either in the recorded table or in an exempt table
**with a reason**. It also fails on a stale entry, so a removed route cannot leave the test checking
nothing.

The exempt table is currently empty: every route on the control plane that changes something writes an
entry. It stays because the next one might deserve not to, and that reason belongs written down rather
than argued in a review.

### The entry commits with the change

`audit.Record` takes a transaction, so an action that happened is an action that was logged, and a
refused request leaves nothing behind — a log where "who did this" and "who tried this" are the same
line is a log nobody can act on.

Four routes could not do this at first, and they were named rather than glossed: accepting an
invitation, changing project access, sending an invitation and creating an organization each call a
domain function that owns its own transaction. Those used `audit.RecordAfter`, which wrote separately
and could fail on its own — by then the action was committed, so the only honest choices were a
logged gap or telling a caller their change did not happen when it did.

**That gap is now closed.** Each of the three affected `internal/identity` functions —
`CreateOrganization`, `SetMemberAccess`, and the shared `accept` behind `AcceptInvitation` and
`AcceptPendingInvitationFor` — takes an optional hook, `func(tx *global.Tx, ...) error`, called inside
its own transaction immediately before commit. The caller's audit entry runs through that hook, so it
either commits with the write or rolls it back with it. `CreateInvitation`, the fourth, needed a
different fix: it wrote its row *before* attempting the email send, so the row and the entry were
never going to be atomic with each other no matter how the write was wrapped. Reordered to send first
— nothing in the message depends on the row's identity — the row and the entry now commit together,
and a failed send leaves nothing behind rather than a row to clean up.

`audit.RecordAfter` and its logging helper are deleted rather than kept as an escape hatch: there is no
remaining call site, and code whose only purpose was to describe an unclosed gap has no purpose once
the gap is closed.

Achieving this required wrapping nine handlers in transactions they did not have, which surfaced two
real bugs — see Consequences.

### An operator names themselves, and the entry says it is unverified

The shared operator token cannot say who is holding it. Two options were bad in different ways:
`granted_by_user_id` null forever, or a name treated as fact.

So the operator routes **require** an address, and the entry reads
`operator ada@playersense.dev (self-asserted)`. Required, because a field somebody has to fill in is
a field they think about, and the alternative was "operator (unidentified)" on every credit grant in
the product. Labelled, because an entry reading the bare address would be read as proof that Ada did
it when all it proves is that somebody with the deployment's token typed her address.

### `audit.read`, not `member.read`

The log names what every member of the organisation has been doing. A contractor can reasonably see
who their colleagues are without seeing what each of them changed last Tuesday, so reading it is
reserved to the two system roles among the seeded six.

### The label is written even when the edge resolves

`actor_user_id` is an edge so a dashboard can show a name; `actor_label` is written anyway. An entry
that becomes "unknown" when somebody leaves the company has lost the thing it was for, so the label
is what was true at the time and the edge refines it while it still resolves.

## Consequences

**Nine handlers gained a transaction**, and two bugs came out of that:

- `RevokeAPIKey` queried an edge off a row the transaction had produced, *after* the commit — a use
  of a closed connection, so revoking a key answered 500. Found by an existing test, which is the
  right way round.
- `SetAutoTopUp` built its update from the account row rather than from the transaction, so the
  configuration change would have committed on its own while the audit entry rolled back — precisely
  the combination this design exists to rule out. Found by reading the code after the first bug.

Both are the same mistake in two shapes: in ent, a row remembers which client produced it.

**`audit_events` is not held apart from the data it describes**, which is one of §08's three
requirements. Append-only removes the *edit* — there is no setter to call — and does not remove the
*delete*: an operator with that database can still drop rows. `staff_access` satisfies the
requirement properly, because our reads of a cell are recorded in a different database under a
different jurisdiction. A customer's own log is in the same PostgreSQL database as their
organizations and memberships, and saying so is more useful than implying otherwise.

**An operator action's actor is self-asserted**, so the log is evidence of what happened and not of
who did it. That gap closes with a verified staff identity, not with more text.

**Every audited write costs one more row and one more statement.** Tens of rows a day for an
organisation, which is what the schema comment already assumed.

## Alternatives considered

**Middleware over every mutating route.** Rejected above: it cannot see the previous value, and the
previous value is most of what an audit entry is for. It would also record refused requests as
events, which makes "who did this" unanswerable without reading a status code that is not in the row.

**A database trigger per table.** Rejected for the same reason plus a worse one: a trigger sees rows,
not intent. "This membership row disappeared" is not "Ada was removed by Grace", and the trigger
would also be somewhere nobody reading the Go would find it — the same argument this codebase makes
about the member cap and the last-owner rule living in `internal/identity`.

**An events table the application appends to asynchronously.** Rejected: a queue between the action
and its record is a window in which the record is lost, and the thing being protected is precisely
the case where somebody is motivated to lose it.

**Logging to stdout and relying on log aggregation.** Rejected because §08 wants records the customer
can read — this is *their* log, shown in *their* dashboard — and because a log line is not a row a
dashboard can filter by action or page through.

**Recording reads as well as writes.** Rejected for the customer-facing log: a studio reading their
own dashboard is not an event, and a log that grew per page view would bury the twelve entries a
month that matter. Reads *by PlayerSense staff* are a different question with a different table,
`staff_access`, which requires a ticket per read and awaits the console that will write it.

**One table for both.** Rejected: one screen where a studio's own administrator and one of our
engineers appear as the same kind of event is a screen that answers neither question.

## Enforcement

`internal/audit` holds the vocabulary and the write path; `internal/handlers/audit.go` the
per-request helpers, the `inTx` wrapper and the read; `internal/routers/members.go` mounts the read
under `core.Require(authz.AuditRead)`. The hook parameter lives on `identity.CreateOrganization`,
`identity.SetMemberAccess` and the shared `identity.accept`.

In `apps/backend/cmd/control-api/audit_test.go`:

| Test | Fails when |
|---|---|
| `TestEveryMutatingRouteDecidesAboutAudit` | a route that changes something is added and says nothing about audit, or a recorded route is removed |
| `TestRemovingAMemberIsTheEntryThatMattersMost` | the one deletion in the product stops leaving a trace |
| `TestTheLogSaysWhatSomethingWasBefore` | an entry stops carrying the previous role or rollout — the thing middleware could not have written |
| `TestAnAuditEntryCannotBeChanged` | an entry becomes mutable |
| `TestAnOperatorGrantSaysItIsSelfAsserted` | the operator name stops being required, or stops being labelled unverified |
| `TestAFailedActionIsNotLogged` | a refused request writes an entry |
| `TestTheLogNeedsAuditRead` | any seeded role other than owner and admin can read the log, or another studio's log is visible |
| `TestTheLogIsFilterableAndNewestFirst` | the order or the action filter breaks |

In `apps/backend/cmd/control-api/members_test.go`, `TestAFailedSendLeavesNothingBehind` fails if a
provider refusal leaves a row, an outstanding-invitation conflict on retry, or a `member.invite`
entry for a message that never went out.

In `apps/backend/internal/identity`, at the seam that can actually prove atomicity — a hook the
caller controls, made to fail on purpose: `TestCreateOrganizationIsAllOrNothing`,
`TestSetMemberAccessIsAllOrNothing` and `TestAcceptInvitationIsAllOrNothing` each fail if the write
they wrap survives a refusing hook. `TestAnAppendOnlyRowHasNoSetter` is what makes the immutability
a compile-time fact rather than a runtime observation.
