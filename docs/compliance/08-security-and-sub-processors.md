# 08. Security, audit and sub-processors

## Encryption

**In transit** everywhere, including between services on a private network. "It is on the
internal network" is an assumption about a network's topology that outlives the topology.

**At rest** for PostgreSQL, ClickHouse and GCS. Customer-managed keys are available for
enterprise agreements that require them.

## Credentials

No key is stored in a form that could be presented as a credential (Appendix C.10).

A key is `public:private`. The public half is stored and indexed; the private half exists
only as an Argon2id digest with its parameters encoded alongside it. A database disclosure
yields public identifiers and digests, neither of which is a working key. See ADR 0011.

Key classes are enforced per endpoint: a `reporting` or `server` credential presented to the
ingestion endpoint is refused, so a leaked analytics key cannot widen into a reporting one.

Revocation writes a tombstone under the **public** half, because the control plane cannot
reproduce the secret it minted — revocation by somebody who cannot reproduce the credential
is the normal case, not the exception. It takes effect immediately rather than when a cache
entry happens to expire, which is exactly the minutes during which somebody needs it to stop
working.

## Rate limiting

Counters live in Valkey so every instance shares one budget, and the limiter **fails
closed**: when the storage cannot answer, the request is refused rather than counted as
allowed. An attacker who can reach the storage should not also get to choose the moment
limits stop applying. See ADR 0015.

Counting happens before authentication, so an unauthenticated flood costs the sender its
budget rather than costing a key lookup each time.

## The highest-risk surface

**The admin console**, when it exists. Internal staff able to look at any organisation's data
is exactly the capability a support team needs and exactly the one an attacker or a curious
employee would want.

The rules it will be built under, recorded now so it is built with them:

- every staff read of customer data is logged with the actor, the customer, and a **reason** —
  a ticket reference, not free text;
- those records are held separately from the data they describe;
- **audit records are append-only.** A log the operator can edit proves nothing.

## Breach readiness

Notification deadlines are short enough that the scoping question — whose data, and how
much — has to be answerable before it is asked.

The archive manifests are what make it a query rather than an investigation: they record
which objects held which project, environment and time range. Combined with the cell model,
"which customers' data was in the affected system" is bounded by construction — a cell is a
blast radius as well as a jurisdiction.

## Sub-processors

Deliberately short:

```text
Google Cloud Platform    infrastructure, storage, queueing
Stripe                   billing
```

Short because every addition is a customer notification, a DPA amendment, and one more party
a studio's own compliance review has to accept. Keeping it short is an architectural
constraint rather than an accident: a dependency that would process customer data is weighed
against the cost of adding a name to this list.

Notably **not** on it, and not planned: any analytics or session-replay vendor inside
PlayerSense's own dashboard that would see customer data, any error-reporting service
receiving payloads rather than stack traces, and any model provider processing customer data
for training.

## Remote access to a cell

US engineers can reach an EU cell in an incident. That is a transfer
([03](03-residency-and-transfers.md)), it is covered by the same mechanism as the rest, and
it is logged under the admin-console rules above.

Stating it is the point. A residency claim that quietly excludes operator access is a
residency claim that will not survive its first customer questionnaire.

## Status

Encryption at rest and in transit are provider defaults in the deployment; the audit log,
the admin console and the archive manifests are designed and not built —
[10-open-items.md](10-open-items.md).
