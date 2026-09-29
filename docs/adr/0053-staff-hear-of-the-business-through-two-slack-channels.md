# 0053. Staff hear of the business through two Slack channels

**Status:** Accepted
**Date:** 2026-09-29
**Relates to:** [ADR 0050](0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md), [ADR 0051](0051-rules-route-events-to-channels.md), [ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md); project.md §56

## Context

Nothing told PlayerSense's own staff how the business was doing, or that something had broken,
unless somebody opened the operations console. A studio has channels, rules and rate caps
([ADR 0051](0051-rules-route-events-to-channels.md)), all stored per project. The company had none
of that for itself, and the two things it wants are different in kind: a number to read each
morning, and a fault to look at now.

## Decision

**Two Slack incoming webhooks in the staff workspace, set in the environment and holding no rows.**
They are not studio channels and do not use the notification outbox.

- **Growth** (`STAFF_SLACK_GROWTH_URL`). The worker posts one digest a day at `STAFF_GROWTH_AT`, in
  UTC, for the day that ended: money in (`topup` and `invoice_payment`), refunds, usage charged,
  new and total organizations, users and projects, and accounts owing. The figures are read off
  the ledger and the tables and never cached, so the digest cannot disagree with the console. Only
  entries that are money are counted. `promo` and `expiry` are credit PlayerSense gave and took
  back, and are not revenue ([ADR 0036](0036-a-wallet-is-lots-because-credit-expires.md)).
- **Alerts** (`STAFF_SLACK_ALERTS_URL`). Every record at ERROR or above that the API, the worker or
  a writer logs, which includes each panic the framework recovers. One message per kind per
  fifteen minutes, twenty an hour across all kinds; what is held back is counted on the next
  message of that kind.

Each is off while its URL is empty, and a process with neither runs exactly as before. A bad URL
fails the process at start-up, by the same validation a studio's Slack channel gets.

## Consequences

- **The digest is once a day, best effort.** "Once" is a Valkey `SETNX` on the day, released if the
  post fails so the next tick retries, and dropped after six hours so a worker starting at supper
  does not post the morning's numbers. Valkey keeps nothing across a restart (ADR 0001), so a Valkey
  restart inside that window can send a second digest. A duplicate was chosen over a missing one.
- **UTC, not the reader's morning.** `STAFF_GROWTH_AT=09:00` is 09:00 UTC. A zone with daylight
  saving runs twice or not at all on two days a year, which is why `WORKER_DERIVE_AT` is UTC too.
- **ERROR is the definition of critical.** There is no severity ladder. A line logged at ERROR is a
  line somebody decided a person should see, and a bug is raised by logging it there. The cost is
  that an ERROR someone wrote casually is a Slack message. The dedupe and the hourly cap bound
  that, and the fix is to log it lower.
- **An alert repeats only an allowlist of attributes** (`error`, `panic`, `method`, `path`,
  `request_id`, `job`, `reason` and a few ids), because the hook sees a record before the
  framework's logger redacts it. A stack trace is never sent. An error message can still carry
  whatever the error carried, so it is clipped, and the channel is PlayerSense's own workspace.
- **Alerting replaces `slog.Default()`.** Most packages here are handed the default logger rather
  than the application's, so the hook has to be the default to hear them. It is installed before
  anything captures the default, and only when a URL is set.
- **Sending is never on the logging path.** An alert is queued without blocking, and a full queue
  drops the message and counts it. A failed post is logged at WARN, below what raises an alert, so
  Slack being down cannot make more alerts.
- **A hard crash loses what is queued.** A process that is killed rather than stopped does not drain.
  The panics the framework recovers are raised as they happen, and a crash loop is seen by the
  next process starting.
- **What it does not watch.** A process that has stopped logging is silent, and so is a whole
  machine that is down. That needs a probe from outside, and this is not one.

## Alternatives considered

**Use a studio-style channel and rule for the company's own organization.** It reuses the outbox and
the rules, at the cost of an organization to own them, a way to tell staff from customers in every
query, and rate caps chosen per rule for a purpose they were not designed for.

**A severity ladder and explicit `staffnotify.Critical(...)` calls.** More precise, and nothing would
be raised until somebody remembered to call it. The failure that matters is the one nobody
anticipated, and that is logged at ERROR by whatever code met it.

**Send from Cloud Logging or an error tracker.** It moves the problem to a service the company does
not run yet, and this is a few hundred lines with no new sub-processor beyond Slack.

## Enforcement

- `apps/backend/internal/staffnotify/staffnotify_test.go`
  - `TestAnErrorIsRaisedAndNothingBelowIt` raises ERROR only, and asserts a password, a token and a
    stack trace never appear in the message.
  - `TestARepeatIsCountedNotSentAndTheCountRidesOnTheNext` and
    `TestNoMoreThanAnHourlyAllowanceIsSentWhateverTheKind` hold the dedupe and the cap.
  - `TestCollectReadsMoneyAndCountsAndLeavesPromoOut` fails if promo credit is added to revenue.
  - `TestTheDigestIsPostedOnceInsideItsWindowAndRetriedAfterAFailure` holds the lock, the retry and
    the window.
  - `TestAnErrorReachesAWebhookAsSlackText` runs the real sender against a local receiver.
- `apps/backend/internal/config/config.go`: `StaffSlackGrowthURL`, `StaffSlackAlertsURL`,
  `StaffGrowthAt`.
- `apps/backend/cmd/{api,worker,analytics-writer,archive-writer,billing-writer}/main.go`: each
  builds its logger through `staffnotify.ForProcess` first, and registers `staffnotify.Running`.
