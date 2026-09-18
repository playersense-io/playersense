# 04. Children and platform rules

Three overlapping regimes, each stricter than the last, and none of them satisfiable by a
setting read at query time. A row collected under a rule that forbade collecting it is not
fixed by declining to read it.

## The policy tier

A project declares a tier **at creation**. It is stamped onto every event, so the constraint
travels with the data rather than being re-derived from a mutable flag. A tier change applies
from that moment forward and never retroactively — a later flag cannot make an earlier
collection lawful, and reprocessing history to pretend otherwise is precisely the audit
failure the tier exists to prevent.

| Tier | Identifier | Attribution | Retention | Cohorts |
|---|---|---|---|---|
| `standard` | persistent `install_id` | full | per plan | full |
| `child_directed` | persistent `install_id` | **none** | capped | full |
| `kids_strict` | session-scoped only | none | short | **same-day only** |

See ADR 0005.

## `standard`

The ordinary case. Everything else in this documentation describes it.

## `child_directed` — COPPA and Google Play Families

A persistent identifier is **retained**, because COPPA's support-for-internal-operations
basis permits one for analytics. Internal analytics is an enumerated permitted purpose; the
identifier exists to serve it and nothing else.

What is removed is everything pointing at advertising:

- any onward sharing;
- any use of the data outside the studio's own measurement.

There is no acquisition source or campaign attribution to remove. PlayerSense ingests no MMP
feed in any tier, so no project of any kind carries attribution data — see
[ADR 0033](../adr/0033-there-is-no-attribution-dimension.md).

Retention is capped below the plan's normal maximum.

Google Play's Families policy is satisfied by the same shape, with one addition: the SDK must
be declared, and PlayerSense publishes a Data Safety reference so a customer can complete
their own declaration without guessing ([09](09-controls-map.md)).

## `kids_strict` — Apple's Kids Category

Apple's Kids Category rule is stricter than COPPA. It restricts sending device information
to third parties, and in this arrangement **PlayerSense is the third party**.

So there is no persistent identifier at all. The gateway replaces the installation
identifier with a value scoped to that single batch, **regardless of what the SDK sent**:

```go
if tenant.PolicyTier == "kids_strict" {
    batch.InstallID = uuid.NewString()
}
```

Enforcing it at the gateway rather than in the SDK is the difference between a policy and a
promise. The SDK may be old, misconfigured, or modified; the gateway is not. The test sends
the **same** identifier in two batches and asserts they cannot be linked.

### The consequence, stated at signup

> **Cross-day retention is not computable under `kids_strict`.**

Retention means recognising the same player on two days. A tier that forbids recognising
them on two days forbids retention. This is not an implementation gap and no amount of later
engineering recovers it.

Same-day aggregates all work normally: sessions, events, revenue, playtime, funnels within a
session.

Selling a Kids Category studio an analytics product and letting them discover an empty
retention page after they have shipped is a worse outcome than telling them during signup,
which is why this sentence is in the tier's description in the product and not only here.

## App Tracking Transparency

PlayerSense **does not track** as Apple defines the term:

- no advertising identifier, on any platform;
- no device fingerprinting or probabilistic matching;
- no identifier shared across apps, vendors or sites;
- no sale or sharing with brokers or advertising networks.

These are permanent constraints, not defaults (ADR 0004). They follow from the identifier
design rather than being bolted onto it: the reason a reinstall produces a new `install_id`
is that every alternative is one of the mechanisms listed above.

Consequently the SDK declares `NSPrivacyTracking: false`, and a studio embedding it is not
obliged to show an ATT prompt on PlayerSense's behalf, or explain PlayerSense to an App
Review team.

`att_status` is recorded when the SDK reports it, and is not acted on
([02](02-lawful-basis-and-consent.md)).

## Apple privacy manifest

Apple requires a signed `PrivacyInfo.xcprivacy` from third-party SDKs, declaring collected
data types and the reason for each restricted API used — reading `UserDefaults` for the
install identifier among them.

Without it, App Store review rejects **the customer's app**, not PlayerSense. That makes it
a release blocker rather than a compliance task.

It is generated from the column classification ([01](01-data-inventory.md)) for the reason
ADR 0007 gives: a manifest maintained by hand describes a schema that has moved on.

**Status: the SDKs are not built.** See [10-open-items.md](10-open-items.md).

## Google Play Data Safety

A published reference itemising what the SDK collects, whether it is linked to identity, and
whether it is shared, so a customer can complete their own declaration without guessing.
Generated from the same classification.

**Status: not built**, for the same reason.

## Age assurance

PlayerSense does not perform age verification and does not receive age data. The studio
determines whether its game is child-directed and selects the tier. That is the correct
division — the studio knows its audience and its store listing, and a processor guessing at
a player's age would be collecting more data to collect less.
