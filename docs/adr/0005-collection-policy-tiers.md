# 0005. A project declares a collection tier at creation, stamped onto every row

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §75, §98

## Context

Not every game may be measured the same way. A game directed at children falls under COPPA
and Google Play's Families policy; a game in Apple's Kids Category falls under a stricter
rule still, which restricts sending device information to third parties — and in this
arrangement PlayerSense *is* the third party.

These constraints cannot be applied at query time. A row collected under a rule that
forbade collecting it is not fixed by declining to read it.

## Decision

A project declares a **policy tier** at creation. It is stamped onto every event, so the
constraint travels with the data rather than being re-derived from a mutable flag.

| Tier | Identifier | Attribution | Retention | Cohorts |
|---|---|---|---|---|
| `standard` | persistent `install_id` | full | per plan | full |
| `child_directed` | persistent `install_id` | **none** | capped | full |
| `kids_strict` | session-scoped only | none | short | **same-day only** |

`child_directed` keeps a persistent identifier because COPPA's support-for-internal-
operations basis permits one for analytics. What it removes is everything pointing at
advertising: acquisition source, campaign attribution, and any onward sharing.

`kids_strict` has no persistent identifier at all. The gateway replaces the installation
identifier with a value scoped to that single batch, **regardless of what the SDK sent**.

A tier change applies from that moment forward and never retroactively.

## Consequences

The consequence that must be said out loud, at signup rather than after integration:

> **Cross-day retention is not computable under `kids_strict`.**

Retention means recognising the same player on two days. A tier that forbids recognising
them on two days forbids retention. Same-day aggregates — sessions, events, revenue,
playtime — all work normally.

Selling a Kids Category studio an analytics product and letting them find an empty
retention page after they have shipped is a worse outcome than telling them during signup.

Enforcing the identifier replacement at the gateway rather than in the SDK is the
difference between a policy and a promise. The SDK may be old, misconfigured, or modified;
the gateway is not.

## Alternatives considered

**A per-project settings flag read at query time.** Rows are already collected by then.
It also makes a tier change retroactive, which is exactly the audit failure the tier exists
to prevent.

**Trusting the SDK to send a session-scoped identifier for kids projects.** A promise about
somebody else's binary.

**Refusing Kids Category customers.** Possible, and a narrower product than intended. The
tier makes it a supported case with a stated limitation instead.

## Enforcement

- `apps/backend/ent/schema/globaltier/project.go` — `policy_tier` is immutable.
- `apps/backend/internal/handlers/ingest.go` — the gateway replaces `install_id` per batch
  for `kids_strict`, before normalization, so nothing downstream can link two batches.
- `cmd/ingest-api/main_test.go:TestKidsStrictHasNoPersistentIdentifier` sends the *same*
  identifier twice and asserts the two batches are unlinkable.
