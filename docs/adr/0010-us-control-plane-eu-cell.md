# 0010. The control plane is US-hosted; the first cell is European

**Status:** Accepted
**Date:** 2026-09-14
**Relates to:** project.md §95, §99

## Context

The company is being incorporated in the United States, via Stripe Atlas. The first
customers are European studios. Those two facts pull in opposite directions, and the
resolution needs to be deliberate rather than a side effect of where the first server was
provisioned.

Incorporation and hosting are separate questions — a US entity may host anywhere — so
"the company is American, therefore the servers are" is not an argument. The argument has
to be made per tier.

## Decision

**The global control plane is hosted in the United States.** It sits beside the entity and
beside Stripe, and — decisively — it holds no player data (0008). Its rows describe direct
customers who signed the terms themselves, and there are thousands of them rather than
millions.

**The first cell, `eu-1`, is hosted in Europe**, and holds the player event data and the
player-linked PostgreSQL records. That is the part a customer's procurement asks about, and
"Frankfurt" is a better answer than "the United States, under a framework". It is also the
part that is painful to move later.

## Consequences

One thing has to be stated plainly, because it is the assumption this decision most invites:

> **Hosting the control plane in the United States does not reduce GDPR exposure.**

GDPR attaches because EU players' data is processed and services are offered to EU studios,
not because of where a server sits. A US entity needs a transfer mechanism — Data Privacy
Framework self-certification, or standard contractual clauses — for EU personal data it
controls *wherever* that data is hosted, because US staff can reach it.

What actually keeps that surface small is 0008's rule: no player identifier ever reaches
the global tier, so the US-hosted database holds only customer account records — business
contact data, which is a far narrower transfer story than player behavioural data would be.

Other consequences:

- Ingest latency for European players is unaffected: batches are fire-and-forget and
  answered `202`, so an extra hundred milliseconds is invisible.
- A control-plane query from a European dashboard crosses the Atlantic. It is a
  low-frequency, low-volume surface; if it becomes a problem the answer is a read replica,
  not moving the tier.
- A US cell (`us-1`) is a later operational move, not a redesign.

## Alternatives considered

**Everything in the EU, including the control plane.** Defensible, and it makes the Stripe
and entity relationship awkward without removing the transfer question — US staff would
still access it.

**Everything in the US.** Simplest, and it makes the residency answer "no" for exactly the
customers being sold to first.

**Two full control planes, one per region.** Solves nothing that 0008's split does not, and
introduces the surrogate-allocation problem described there.

## Enforcement

- `migrate verify` is the mechanical proof that the US-hosted tier holds no player
  identifier, and it is the reason this arrangement is defensible rather than merely
  convenient (0007, 0008).
- `docs/compliance/03-residency-and-transfers.md` records the transfer basis.
