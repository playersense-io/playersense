# Compliance

PlayerSense holds behavioural data about millions of people who never chose it, on behalf
of studios who answer to Apple, Google and several regulators. That makes compliance an
**architectural** concern rather than a legal one, and the distinction is the reason these
documents exist.

A policy can be written the week before an audit. A schema with no column for consent takes
a migration — and a migration of live transactional data is not a thing anyone does in a
week.

## What this is and is not

**It is** a record of what the architecture makes possible, which obligation each mechanism
discharges, and — in [09-controls-map.md](09-controls-map.md) — the specific code and test
behind each claim.

**It is not** legal advice, and it does not claim certification. Where something is not yet
built, [10-open-items.md](10-open-items.md) says so by name. A compliance document that
describes unbuilt controls is worse than none, because it is the document somebody will
rely on.

## Contents

| | |
|---|---|
| [00-roles-and-scope.md](00-roles-and-scope.md) | Who PlayerSense is to whom, and what is in and out of scope |
| [01-data-inventory.md](01-data-inventory.md) | What is collected, where it is stored, and how each column is classified |
| [02-lawful-basis-and-consent.md](02-lawful-basis-and-consent.md) | Why each row may be held, and how that is provable per row |
| [03-residency-and-transfers.md](03-residency-and-transfers.md) | Where data sits, what crosses a border, and under what basis |
| [04-children-and-platform-rules.md](04-children-and-platform-rules.md) | COPPA, Play Families, Apple Kids Category, ATT, privacy manifests |
| [05-data-subject-rights.md](05-data-subject-rights.md) | Access, portability, rectification, erasure, objection, and backups |
| [06-retention-and-deletion.md](06-retention-and-deletion.md) | How long things are kept and what actually removes them |
| [07-aggregate-disclosure.md](07-aggregate-disclosure.md) | When an aggregate stops being anonymous |
| [08-security-and-sub-processors.md](08-security-and-sub-processors.md) | Encryption, access control, audit, breach readiness, sub-processors |
| [09-controls-map.md](09-controls-map.md) | **Obligation → mechanism → code → test.** The one to read |
| [10-open-items.md](10-open-items.md) | What is designed but not built, stated plainly |

## The four that cannot be retrofitted

Everything else in here can arrive later. These four cannot, and they are built:

1. **Consent provenance** — carried on every row ([02](02-lawful-basis-and-consent.md), ADR 0006).
   Nothing in a stored event reveals what the player was told, so a row written without a
   basis can never acquire one.
2. **The policy tier** — declared at project creation and stamped onto every event
   ([04](04-children-and-platform-rules.md), ADR 0005).
3. **Column classification** — the machine-readable source for the Article 30 record, the
   Play declaration and the Apple manifest ([01](01-data-inventory.md), ADR 0007). It also
   used to carry the residency rule, and can carry it again
   ([03](03-residency-and-transfers.md), [ADR 0038](../adr/0038-one-api-one-database.md)).

## Architected for

GDPR and UK GDPR; CCPA and CPRA; COPPA; Apple's App Review Guidelines, including App
Tracking Transparency and privacy manifests; Google Play's Data Safety and Families
requirements.

## Explicitly not pursued

**SOC 2 and ISO 27001** — programmes rather than architecture, and startable when a
customer requires one. **PCI DSS** beyond what using Stripe already gives, since no card
data reaches PlayerSense. **China's PIPL**, which needs a domestic entity and domestic
infrastructure and is better out of scope than half-designed.
