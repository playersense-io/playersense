# 06. Retention and deletion

## Retention is a property of the project

Each project carries a retention tier in days, set at creation and adjustable within the
plan's limits. It travels **on the message** to the writer and is stamped onto every row, so
a row's expiry is a property of the row rather than of a lookup that may have changed since.

```sql
expire_at MATERIALIZED toDateTime(event_time) + toIntervalDay(retention_tier)
TTL expire_at DELETE
SETTINGS ttl_only_drop_parts = 1
```

`ttl_only_drop_parts` matters: without it, expiry is a rewrite of every part containing one
expired row, which on a table of this size is continuous background work for no benefit.
With it, whole parts are dropped once every row in them has expired — which is why
`events_raw` is partitioned by day.

### The bug worth recording

`expire_at` was originally declared with no default. It therefore defaulted to the epoch,
and `TTL expire_at DELETE` expired **every row** on the first merge.

It was found by running it and noticing the table was empty. The fix is the `MATERIALIZED`
expression above, and the lesson is that a TTL column without a computed default is a
delete-everything statement with a delay on it.

## Policy tiers cap retention

`child_directed` and `kids_strict` cap retention below the plan's normal maximum
([04](04-children-and-platform-rules.md)). The cap is applied at project creation, so a row
cannot be written with a retention longer than its tier permits.

## Hot, cold, and the ordering rule

```text
ClickHouse   hot, queryable, bounded by the retention tier
GCS          cold, durable, the retained copy
```

One ordering rule, and it is an operational invariant (Appendix C.6): **local data is not
expired until archive durability is confirmed**, where archiving is required. A disk-pressure
response that deletes ahead of the archive is a data-loss incident wearing a cost-saving
costume.

## Deletion, and what actually removes a row

Three different things remove data, and they are not interchangeable:

| Mechanism | Removes | Timescale | Triggered by |
|---|---|---|---|
| TTL expiry | Whole partitions past their retention | Daily, automatic | The retention tier |
| Actor purge | One actor's rows across every table | Hours | An erasure request ([05](05-data-subject-rights.md)) |
| Project deletion | Everything for a project | Hours | A customer leaving |

### Project deletion

A queue can hold messages for hours, so a batch arriving for a project that has since been
deleted is ordinary rather than exceptional. Writing it would resurrect data somebody asked
to have erased.

The writer therefore consults a project tombstone in the cell's Valkey, written by the
control plane on deletion, and drops the batch with a counter rather than a log line per
event.

If Valkey cannot answer, the batch is **written** and the asynchronous purge removes it. The
purge is the guarantee; the tombstone is a fast path. Making the fast path the guarantee
would mean a cache outage could silently resurrect deleted data, which is the wrong failure
to choose.

## What is not deleted

**Aggregates that identify nobody.** A daily count of sessions for a project survives the
deletion of an individual actor, because it is not about them any more —
[07](07-aggregate-disclosure.md) covers where that stops being true.

**Billing records.** Invoices and usage counts are retained for the period tax law requires.
They are aggregate counts in the global tier and name no player.

**Audit records of the deletion itself.** Proving an erasure happened requires a record that
it happened, which necessarily outlives the data. It holds the actor key, the request, and
the timestamps — and it lives in the cell tier, because it names a player.

## Status

The TTL is live in the schema. The archive writer, the purge job and the project tombstone
are designed and not built — see [10-open-items.md](10-open-items.md).
