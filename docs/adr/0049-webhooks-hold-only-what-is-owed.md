# 0049. Webhooks keep in PostgreSQL only what is still owed

**Status:** Accepted
**Date:** 2026-09-25
**Builds on:** project.md §49-51, [ADR 0048](0048-verified-purchases-win.md)

## Context

A studio's server hears the business's events by webhook: a verified purchase to grant, a config
published, a key revoked. §50 asks for a transactional outbox so an event exists exactly when its
change does. The volume is set by purchases and by how many endpoints a project has, and it has to
hold at millions of deliveries a day. The usual shape, a deliveries table in PostgreSQL with a row
per delivery updated on every try, writes and vacuums that table at the rate of the whole product,
and keeps thirty days of it where a list page reads it.

## Decision

**Only what is in flight lives in PostgreSQL.** `webhook_outboxes` holds events not yet taken,
written in the transaction of the change (`webhooks.Emit`, `webhooks.WithEvent`) and deleted once
every endpoint has had its try. `webhook_retries` holds deliveries still owed: one that failed and
is due again, or one held back from an endpoint that is failing or stayed busy. A delivery that
lands at its first try, which is nearly every delivery, is never a PostgreSQL row at all.

**Everything that happened lives in ClickHouse.** Every try is a row of `webhook_attempts`, written
by the worker a second at a time; `webhook_events` keeps each event's body once for reading and
resending; `webhook_deliveries` (replacing on the latest try) answers the deliveries list, and
`webhook_health_1h` answers the health figures and chart. Thirty days of tries, ninety of health. No
page reads the tries to answer a list, and nothing joins.

**The worker.** Claims are `FOR UPDATE SKIP LOCKED` with a lease, so any number of workers share the
outbox and the retries without taking a row twice, and a row whose worker died is taken again. A
pool of workers is shared by every endpoint, each endpoint allowed sixteen tries at once: a slow
endpoint fills its own slots, never the pool. A delivery waits up to two seconds for a busy
endpoint's slot and is then held back as a retry. An endpoint that fails five times running is left
alone for thirty seconds, doubling to ten minutes, and then tried with one delivery before the rest
follow; the deliveries it holds back do not spend their tries. Connections to each host are kept
open between tries.

**The schedule** is the dashboard's: after 1 and 5 minutes, half an hour, 2, 6 and 12 hours, then a
day twice, each stretched by up to a tenth. An endpoint that has answered nothing but failures for
three days is turned off, what it was owed is closed as failed in the log, and the organization's
owners and admins are emailed. The failing streak is written when it starts and when it ends, never
per try.

**Signing** is `v1=` and the HMAC-SHA256 of `timestamp.body`, comma-separated while a rolled secret
still signs beside the new one, exactly as the dashboard's verification code in four languages
expects. `X-PlayerSense-Id` is the event's id, the same on every try. Report channels sign the same
way.

## Consequences

- One worker delivered 40,000 deliveries (10,000 events to four endpoints) in 1.4 seconds with every
  try logged, about 28,000 a second (`TestLoadWebhookThroughput`, opt-in). Workers add linearly;
  PostgreSQL sees one insert and one delete per event, and nothing per successful delivery.
- The log is written in batches, so a worker killed outright can lose up to a second of tries from
  the log. The deliveries themselves are not lost: an event is deleted from the outbox only after
  every try was made or kept as a retry, so a crash repeats deliveries (at least once, as documented,
  keyed by `X-PlayerSense-Id`) rather than dropping them.
- An endpoint's settings are cached in each worker for five seconds, so a change reaches deliveries
  within that.
- Events not yet emitted (subscriptions, Live Ops windows, alerts, usage thresholds) are in the
  catalog marked `emitted: false`; an endpoint may listen to them and will hear them once they are.
- One payload version, 2026-09-01. There is nothing live to keep an older one for.

## Enforcement

- `TestWebhooksAreDeliveredRetriedAndTurnedOff` (cmd/api): a delivery is signed, carries its headers
  and lands in the log; a failure becomes a retry and a failing streak; a resend lands and ends the
  retry; a rolled secret signs beside the old one; three days of failures turn the endpoint off and
  email its owner; turned back on, what gave up is sent again.
- `internal/webhooks` tests: the schedule, the breaker and the busy wait, URL checks, signatures.
- `control_audit_test.go` holds every mutating webhook route to an audit action or a stated
  exemption.
