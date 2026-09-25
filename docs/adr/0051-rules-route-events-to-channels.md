# 0051. Rules route events to channels, and the account's owners always hear about its money

**Status:** Accepted
**Date:** 2026-09-25
**Builds on:** [ADR 0043](0043-a-bounded-tolerance-for-accounts-that-owe.md), [ADR 0049](0049-webhooks-hold-only-what-is-owed.md), [ADR 0050](0050-logs-are-a-stream-of-their-own-billed-as-they-cost.md)

## Context

A project could set up notification channels (an email list, a Slack or Discord incoming webhook,
Teams, PagerDuty, a signed webhook of its own), and only a report's Notify step could send to them.
Nothing said "when this happens, tell those". Studios asked for the Sentry shape: a failed top-up to
#finance, a publish to #live-ops, a crash to the one on call. And the first question was how a
studio with several Slack channels would say which event goes where.

The events themselves already existed. The webhook outbox (ADR 0049) carries every project event, in
the transaction that caused it. The organization's own events, its account and money, had nowhere
to go at all: a card that declined or an account suspended was a log line and a row nobody watched.

## Decision

**Three layers, kept apart.**

| Layer | What it is | Where |
|---|---|---|
| Channels | Where: one destination each. Two Slack channels are two channels | `notification_channels`, per project |
| Events | What can happen: the webhook catalog, plus the organization's own | `internal/alerts/catalog.go` |
| Rules | The routing: these events, from these environments, to these channels, at most this often | `notification_rules` |

A rule points at channels rather than holding a destination, so a channel changed once is changed for
every rule, and one event can reach many channels while one channel serves many rules.

**Two scopes.** A project's rule hears that project's events and may notify that project's channels
(`reports.read`/`reports.write`, the channels' own permissions). The organization's rule hears its
account's events (`billing.topup_failed`, `billing.payment_overdue`, `billing.account_suspended`,
`billing.account_restored`) and may notify any of its projects' channels. It is the billing
people's to change (`billing.read`/`billing.manage`).

**The money is never left to a rule.** Those four events always email the organization's owners,
admins and billing members, at most once a day per event type, whatever the rules say. A rule adds
Slack or PagerDuty on top; nothing can take the email away. A studio that never opens this page
still hears that its ingestion was suspended.

**One queue, filled where the event is.**

- An organization event is written to `notification_outboxes` in the transaction that made it: the
  ledger's state change, the suspension job's, an automatic top-up's release. It exists exactly when
  the change does. Each announcement fires only when its update changed a row, so a Stripe webhook
  and the worker releasing the same claim announce it once.
- A project event is queued by the webhook dispatcher, which already takes every one. It hands each
  event to the rules before delivering any, so a failure to queue retries the batch whole and no
  endpoint hears an event twice. Rules are cached per project for five seconds: a project with no
  rules, most of them, costs its events a map lookup, whatever their number.
- Rows are keyed by event, rule and channel, so queuing twice is one row.
- A cooldown and the owners' daily email are one table, `notification_marks`: a key per thing
  remembered, rewritten in one `INSERT … ON CONFLICT … WHERE` so two workers racing for the same key
  cannot both be told yes.

**Delivery.** The worker claims due rows `FOR UPDATE SKIP LOCKED` with a lease, sends through the
existing `notify.Sender`, and logs every send in the channels' delivery log with its source ("Rule:
Billing to #finance"). A failure is retried at 30 seconds, 2, 10 and 30 minutes, 1 and 2 hours, then
given up on: yesterday's alert delivered tomorrow helps nobody.

**The dashboard** has a Notifications section of its own (Rules, Channels, Activity) rather than a
settings tab, because rules change as the game does. The Rules page shows what is always on above
the rules, so nobody writes a rule for an email they already get.

## Consequences

- Crash and issue alerts (a new issue, a regression, a spike) plug in as project events with their
  own detector. They must be about issues, not records, with a cooldown per issue, or a crash storm
  floods Slack.
- Slack incoming webhooks need one URL per channel. That is fine for a few and tedious for twenty; a
  "Connect Slack" app would let a rule pick a channel by name. Incoming webhooks stay the default
  because they grant PlayerSense nothing else in the customer's workspace.
- Channels still belong to a project. An organization rule borrowing a project's channel works, but
  channels owned by the organization would say it better.
- The owners' email is not a channel, so it is not in the Activity log; the mail sender's own log
  is where it shows.
