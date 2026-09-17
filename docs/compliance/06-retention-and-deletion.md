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

Four different things remove data, and they are not interchangeable:

| Mechanism | Removes | Timescale | Triggered by |
|---|---|---|---|
| TTL expiry | Whole partitions past their retention | Daily, automatic | The retention tier |
| Archive expiry | Whole objects past their `delete_after` | Every pass of the lifecycle loop | The retention tier, as stamped when the object was written |
| Actor purge | One actor's rows across every table, and their rows inside archive objects | Hours | An erasure request ([05](05-data-subject-rights.md)) |
| Project deletion | Everything for a project | Hours | A customer leaving |

### Archive expiry

Every archive object carries a `delete_after` on its manifest, computed when the object is
written from the retention tier that arrived with the events — not read back at deletion
time. That is what makes retention a term rather than a setting: lowering a customer's
retention does not retroactively destroy an archive they already paid for, and raising it does
not resurrect what already expired.

It is **application-controlled rather than a bucket lifecycle rule**, and §60 is right to
insist. A lifecycle rule can only express an age, one number for the whole bucket. Set to the
longest tier it keeps every short-tier object far too long; set to the shortest it destroys
data a customer is paying to keep. Neither is a retention policy. A rule remains available as
a global safety net underneath this one.

The manifest row **survives the object it described**. Storage is billed from
`object_size_bytes` ([§39](../../project.md)), and an object deleted on the twentieth still has
to be billed for the twenty days it existed; a row deleted with its object takes that history
with it. The row is also the audit record of the expiry — what was removed, when, and with
what outcome is a query rather than a log search.

There is one refusal worth naming, and it exists because of the bug recorded above. A manifest
whose `delete_after` is not after the newest event inside the object cannot be right: no
retention tier of a day or more produces that, and a tier that arrived as zero produces
exactly it. The sweeper marks such a manifest `failed` and leaves the object alone, rather
than emptying the archive as fast as it is written.

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

The TTL is live in the schema, and the archive writer is built — the cold copy the ordering
rule above protects exists, and `archivecheck` measures the margin. The **archive expiry** and
the **actor purge** are built and run in the worker's lifecycle loop, every
`WORKER_JANITOR_EVERY`; [09](09-controls-map.md) maps each claim to its code and its test.

What is not built: the **project tombstone** itself. The writer already honours one and drops
the batch; nothing writes it when a project is deleted, so today a deleted project's queued
batches are removed by the purge rather than skipped. Nor is **project deletion reaching the
archive** — the per-actor path redacts objects, and the whole-project case is the same
machinery with a different predicate. See [10-open-items.md](10-open-items.md).
