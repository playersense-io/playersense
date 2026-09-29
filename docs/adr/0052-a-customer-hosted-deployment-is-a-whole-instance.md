# 0052. A customer-hosted deployment is a whole instance, not a split

**Status:** Proposed
**Date:** 2026-09-29
**Relates to:** project.md §6, §83, §99, §105; [ADR 0038](0038-one-api-one-database.md), [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0045](0045-contracted-accounts-are-invoiced-monthly.md); [compliance 03](../compliance/03-residency-and-transfers.md)

## Context

Large studios have said, more than once, that they will not send their game data to a third
party, PlayerSense included. They are open to running it in their own AWS, GCP or Azure
account and paying PlayerSense for the software. This is the deal [ADR 0038](0038-one-api-one-database.md)
said to revisit first: "the thing to revisit first when a deal turns on it".

What the code looks like today, from a read of the repository on 2026-09-29 (not a run):

- One API binary, one PostgreSQL, one ClickHouse, one Valkey. The cell and region split
  ([0008](0008-two-tier-control-plane.md), [0009](0009-cells-region-and-residency.md),
  [0010](0010-us-control-plane-eu-cell.md)) was removed on 2026-09-21.
- The runtime data path is already well factored: ingestion never queries PostgreSQL per
  request, `analytics-writer` never touches PostgreSQL, and `internal/query` has no ORM in it.
- The code is not separable. `handlers` imports about 60 ent packages, and the ClickHouse
  tenant keys (`org_seq`, `project_seq`, `environment_seq`) are PostgreSQL autoincrement ids.
- Charging, the wallet, Stripe, keys, roles and store verification depend only on PostgreSQL,
  Valkey and the event bus. `billing-writer` needs the bus message shape and the digest key,
  not ClickHouse.
- Nothing is deployable outside a developer's machine yet: no Dockerfiles, no Terraform, no
  Kubernetes. The bus (Pub/Sub), the archive (GCS), backlog monitoring (Cloud Monitoring) and
  identity (Firebase) assume GCP.
- The company is one person building with AI agents. There is no team to hire and no budget
  for audits.

## Decision

**A customer-hosted deployment is the whole existing stack, run as its own single-tenant
instance inside the customer's cloud account.** It is not a control plane in our account
talking to a data plane in theirs.

The customer's instance holds its own PostgreSQL, ClickHouse, Valkey, bus and archive bucket.
Their game data never leaves their account. Surrogate ids are allocated by their own
PostgreSQL, so nothing about tenant keys changes. The dashboard works unchanged, because the
dashboard is part of the instance.

Only two things cross to PlayerSense:

1. **A license.** A signed file the instance checks. It carries the customer, the term and
   the plan's features.
2. **A usage count.** Tracked players per month, from the instance's own `billable_player`
   rows, as a signed report. No event, no player identifier and no content.

Billing in such an instance is the contracted-account path ([ADR 0045](0045-contracted-accounts-are-invoiced-monthly.md)),
invoiced by PlayerSense from the count. The wallet, top-ups and Stripe are switched off by
one setting in the instance. The meter stays the one [ADR 0039](0039-players-are-charged-as-they-arrive.md)
chose: tracked players.

### Order of work

1. **Package.** Dockerfiles for `api`, `worker`, the three writers, `migrate` and `client`.
   An S3 `blobstore`, and ClickHouse's cold tier on an S3-compatible disk. The local bus on a
   single node. Terraform for one AWS VM running the compose stack. A BYOC setting that turns
   off Stripe, the wallet and suspension.
2. **Make it sellable.** OIDC/SSO to replace Firebase, real email (SMTP or SES), license
   checks with server-side feature gating, the signed usage report, backup and restore, an
   upgrade and rollback runbook, and customer-granted support access.
3. **Scale, only when a customer asks.** High availability on Kubernetes, an SQS or Kafka
   bus, Azure and GCP variants.

Estimated at 6 to 8 weeks of calendar time to a first customer-ready AWS instance, most of it
review and testing rather than writing. This is an estimate, not a measurement.

### What is not done, on purpose

No SOC 2, no ISO 27001, no paid penetration test and no paid legal review are budgeted. When a
customer asks for one, the answer is that PlayerSense cannot yet cover its cost. Instead:

- Free standard contract templates (Common Paper, Bonterms) adapted for the MSA and DPA. In a
  customer-hosted instance PlayerSense does not receive game data, so the DPA is short.
- Scanners in CI: `govulncheck`, `gosec`, `trivy`, `semgrep`, and OWASP ZAP against a staging
  instance. An SBOM from `syft` and signed images from `cosign`.
- A short security overview written from `docs/compliance`, and answers to questionnaires.
- The customer can scan and test their own instance, because it runs in their account.

Nothing is spent before a studio has signed. If a studio makes an audit a condition, it is a
funded milestone in its contract, paid from the platform fee.

### Price

Kept to the existing meter, so the code needs no new one. Suggested, and untested with any
studio: about 20% to 30% below the hosted per-player rate (the customer pays for the
infrastructure), plus an annual platform fee floor of $24k to $48k that includes priority
support. A customer-hosted studio never pays less than it pays hosted today.

## Consequences

- **The residency answer becomes "wherever you run it".** This is the strongest answer
  available, and it costs no rebuild of the registry, routing or a second database.
- **Every customer is a separate instance to upgrade.** Ent auto-migrates forward and
  `migrate` is the only upgrade step, so a bad release is hard to roll back on a database
  PlayerSense cannot see. The upgrade and rollback runbook is the real work of phase 2, not
  a nicety.
- **PlayerSense loses live visibility.** The operator console cannot see a customer's
  instance. Support needs an access route the customer grants and can revoke.
- **Metering is trust plus audit.** A signed report from software running in the customer's
  account can be withheld or altered. The contract carries an audit right, and an offline
  mode (an offline license, usage reported by hand each quarter) is a separate, higher price.
- **Plan gating must exist first.** `billing.HasFeature` has no callers today, so priority
  support and the report builder are not gated on the server. A license that names features
  is meaningless until something checks them.
- **Email has to be real.** The only sender is `log`. Invitations and billing notices go
  nowhere, and a customer instance cannot use a shared mailer.
- **The sub-processor list does not grow.** A customer's instance is their infrastructure,
  not ours ([§105](../../project.md)).
- **The hosted product is unchanged.** The BYOC setting is off by default.

## Alternatives considered

**A control plane in PlayerSense's account and a data plane in the customer's.** The classic
BYOC shape, and the one this ADR first looked at. It needs the cell registry rebuilt, an id
issuing authority so the customer's ClickHouse keys do not collide, and `handlers` untangled
from 60 ent packages. That is the work [ADR 0038](0038-one-api-one-database.md) removed and
costed as large. It is the right shape once there are many customers on the split, and the
wrong first move for one.

**Customer-owned storage only, with PlayerSense still running compute.** The bucket is
theirs, but the events pass through PlayerSense's servers, which is exactly what they have
said they will not accept.

**Regional hosting run by PlayerSense (EU, US).** Cheap for the customer and no better on the
objection: the data is still in a third party's account.

**Full SOC 2 first.** Costs $15k to $30k in year one, and buys nothing until a studio
demands it. A customer-hosted instance also shrinks what an auditor would scope.

## Enforcement

None yet, and saying so is the point of the Proposed status. When phase 1 lands this should
name the BYOC setting in `internal/config`, the test that fails when the instance makes an
outbound call to anything except the license and usage endpoints, and the CI job that builds
the images and the Terraform module.
