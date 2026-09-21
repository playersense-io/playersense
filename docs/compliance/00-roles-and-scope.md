# 00. Roles and scope

## Who PlayerSense is, to whom

Two relationships, and conflating them is the most common mistake in an analytics vendor's
privacy documentation.

### Processor — for player data

A studio decides to measure its players, decides what events to send, and decides how long
to keep them. PlayerSense processes that data on the studio's instructions, under a data
processing agreement. **The studio is the controller; PlayerSense is the processor.**

The practical consequences run through the whole design:

- PlayerSense does not decide what a player consents to. The studio obtains consent and the
  SDK transmits the result, which is stamped onto every row ([02](02-lawful-basis-and-consent.md)).
- PlayerSense does not use player data for its own purposes. No cross-customer modelling, no
  benchmarking that identifies a customer, no onward sharing.
- A data subject request reaches PlayerSense **through the studio**, scoped to that studio's
  project. A studio may ask about its own players and not anyone else's
  ([05](05-data-subject-rights.md)).
- Sub-processors are PlayerSense's own, disclosed and short
  ([08](08-security-and-sub-processors.md)).

### Controller — for its own customer accounts

The people who sign up, log into the dashboard, and are billed are PlayerSense's own
customers. For their account data — name, work email, organisation, subscription, invoices
— **PlayerSense is the controller**.

This is business contact data about a few thousand people who chose the relationship. It
sits in the same database as the player-linked records, which used to be forbidden and no
longer is ([03](03-residency-and-transfers.md)).

## Who the data is about

| Category | Who they are | Relationship |
|---|---|---|
| **Players** | People playing a customer's game | Never contracted with PlayerSense; usually unaware it exists |
| **Customer staff** | Developers and analysts at a studio | Signed up, agreed to terms |
| **PlayerSense staff** | Internal support and engineering | Employment relationship |

The first row is why this matters. Players never chose PlayerSense, which is precisely why
the burden sits on the architecture rather than on a consent dialog they will never see.

## Territorial scope

GDPR applies because a customer's players are in the EU and services are offered to EU
studios. It applies regardless of where any server sits — see
[03](03-residency-and-transfers.md), which states plainly what the US/EU arrangement does
and does not buy.

CCPA/CPRA applies on the same logic for California residents. PlayerSense does not **sell**
or **share** personal information as those terms are defined, and that is an architectural
constraint rather than a current fact: ADR 0004 prohibits advertising identifiers, onward
sharing and data-broker relationships permanently.

## The entity

PlayerSense is operated by a United States company (Delaware, via Stripe Atlas). That is a
fact about incorporation, not about hosting, and the two are decided separately —
[03](03-residency-and-transfers.md) explains where the deployment runs and what that does
and does not buy.

## Out of scope

- **Card data.** Stripe holds it; it never reaches PlayerSense.
- **Health, biometric, precise-location and other special-category data.** Not collected.
  The ingestion path actively refuses batches carrying direct personal data, so a studio
  cannot make it in scope by accident ([01](01-data-inventory.md)).
- **Advertising identifiers.** Not collected, on any platform, permanently (ADR 0004).
