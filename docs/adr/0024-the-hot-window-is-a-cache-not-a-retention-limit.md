# 0024. The hot window is a cache, not a retention limit

**Status:** Accepted
**Date:** 2026-09-16
**Relates to:** project.md §3.4, §14, §24, §33, §34, §35, §59, Appendix A, Appendix C.6

## Context

§4.2 does the arithmetic and then the document lives with it: a billion events a day is
roughly a terabyte before compression, and a 500 GB VPS disk is not a retention plan. §33's
answer was a "3-14 day hot window" in ClickHouse with everything older living only in the
Parquet archive.

That answer has a cost nobody had to pay yet, because it makes the hot window a **retention
limit in practice**. A studio on a 90-day tier has 90 days of data and can query about
seven of them; days 8 to 90 exist in GCS as Parquet and are reachable only through §34's
asynchronous cold-query path, which is not built. The dashboard cannot see them at all.

Appendix A gestures at the real answer and then talks itself out of it — it explains that
managed systems use disaggregated storage, observes that "PlayerSense Stage 1's closest
low-cost approximation" is a bounded local disk plus GCS, and files the rest under §35's
"Stage 2 hot/cold simplification". The premise is that object-backed ClickHouse is a
managed-product feature.

It is not. It is a storage policy, and open-source ClickHouse has had one for years. The
local build already reports `s3`, `gcs` and `azureBlobStorage` as available disk types.

## Decision

**A table names a storage policy, and the deployment decides what is behind it.**

`events_raw` first (00009) and `player_activity_daily` after it (00010). The second matters
more than it looks: §33.1 singles that table out as the one aggregate that is *not* small —
about 120 GB a year at ten million daily actives — so tiering the raw events and leaving it
on local disk would have moved the ceiling without removing it. `player_lifetime` stays hot,
because `player_lifetime_v` is on the query path.

```text
volume `default`   local NVMe        the hot window
volume `cold`      object storage    everything older, still queryable
```

The TTL now says two things rather than one:

```sql
TTL toDateTime(event_time) + toIntervalDay(7) TO VOLUME 'cold',
    expire_at DELETE
```

`TO VOLUME` is an operational constant — how much fits on the disk. `DELETE` stays keyed on
`expire_at`, which is MATERIALIZED from the row's own retention tier (§23), so retention
stays a per-project product decision. A project on a 365-day tier keeps its data for a year
either way; only the first week of it is on NVMe.

The policy lives in infra configuration, not in the schema, so the same migration runs
everywhere and `cold` is a second local directory in development and a GCS bucket in
production. That is §3.6's rule applied to storage: the schema is coupled to a policy name,
not to a product.

**The Parquet archive stays and is not replaced by this.** The cold volume holds
ClickHouse's own format, which only ClickHouse can read; §14's archive is portable, is what
§34's exports and §73's reprocessing read, and is the copy that survives losing the cluster.
Appendix C.6 is unchanged.

## Consequences

**§33's hot window stops being a retention limit and becomes cache residency.** A 365-day
retention curve is answerable from the raw table rather than only from the aggregates, and
§34's cold-query path becomes an optimisation rather than the only way to see last month.

**Storage becomes a bill rather than a ceiling**, which is the property the whole question
was about. Growing past the disk is a line item, not a migration.

**Nothing on the reporting path is cold**, and that is worth stating precisely rather than
worrying about in the abstract. The tiered tables are `events_raw` and, since 00010,
`player_activity_daily`. The reporting API reads neither: §26's rollup selection means it
answers from `cohort_metrics_daily`, `cohort_sizes`, `metrics_daily`, `events_1m`,
`events_1h` and `player_lifetime_v`, every one of which is on local disk. So §32's limits did
not need re-tuning — a dashboard query cannot reach object storage today.

What *does* read cold data is `archivecheck`, a rebuild of an activity month older than
ninety days, and — when they are built — §34's cold queries and §3.5's player timeline.
Those are batch or explicitly-asynchronous paths, which is the right place for a slow read.
§32's ceilings should be revisited when §34 lands, under its own budget rather than the
dashboard's.

**A `<cache>` disk sits in front of the object store**, so a granule read once is served from
local NVMe the next time, bounded at 10 GiB. `cache_on_write_operations` is off deliberately:
a part being moved *to* cold is moving because nobody is reading it, and caching it on the
way out would evict granules somebody is actually querying.

**Object storage charges per operation as well as per byte.** A badly-shaped cold query is
now a cost as well as a latency, which is another reason §32's limits matter more than they
did.

**`prefer_not_to_merge` is set on the cold volume.** Merging parts that already live there
is a download, a merge and an upload for no query benefit. The cost is that cold parts stay
as they were when they moved, so the compaction §10.7 owns matters more.

**A down-migration only works while everything is still hot.** Moving back to the single-disk
policy fails once parts are on the cold volume, and that is the honest behaviour: the
alternative is a migration that silently moves terabytes.

## Alternatives considered

**Leave §33 as it is and build §34's cold-query path.** This is the document's existing
plan. It is strictly more work — an asynchronous job, a result store, a UI for a query that
returns later — and it delivers a worse product, because "last month is available in about a
minute, as a download" is not a dashboard.

**A bigger disk.** Appendix A already rejects this and gives the reasons: cheap VPS products
cap out, resizing is provider-specific and often offline, a larger disk does not bring CPU
or IOPS, and continuously growing one node builds an enormous failure domain.

**Wait for Stage 2 and managed ClickHouse.** §35's position, and the reason it is wrong is
that the feature is not a managed-product feature. Waiting means either paying for managed
ClickHouse before there is revenue, or shipping a product whose retention setting does not
mean what a customer reads it to mean.

**Put the cold volume on the same bucket as the Parquet archive.** Tempting, and wrong: two
different formats and two different lifecycles in one prefix, where §60's retention worker
sweeps manifests and would have to learn to leave ClickHouse's parts alone. Separate
prefixes at least; a separate bucket is clearer.

## Changing what is behind `cold` is a data move, not a config change

This cost an outage on the development stack and is the kind of thing that is much more
expensive to learn in production, so it is written down rather than remembered.

ClickHouse validates **every part on disk** when it attaches a table — including *outdated*
parts, which linger after a move until the cleanup thread reclaims them (`old_parts_lifetime`,
eight minutes by default). A part on a disk the policy does not name is a fatal
`UNKNOWN_DISK`, and the table does not attach. The server starts, the port answers, and the
table is simply gone.

So renaming the cold disk from `cold_local` to something else, with parts already on it, is
enough to take the table offline. The procedure that works:

1. Add the new disk to the cold volume **alongside** the old one, and restart. Both are named
   by the policy, so every existing part is legal.
2. `ALTER TABLE ... MOVE PARTITION ID '…' TO DISK '<new>'` for every partition still on the
   old disk.
3. Wait until `system.parts` reports **nothing** on the old disk — active *or* outdated. This
   is the step that is easy to skip, because the active parts have already moved and the
   table looks finished.
4. Remove the old disk from the policy and restart.

`MOVE PARTITION` takes a partition expression; reading one back out of `system.parts` and
feeding it to an `ALTER` is awkward because the tuple comes back quoted. `MOVE PARTITION ID`
with `partition_id` is the form that composes.

## Enforcement

- `infra/stage1/compose/clickhouse/config.d/storage.xml` — the policy, with the production
  GCS disk documented in place and its credentials read from the environment rather than
  committed.
- `db/clickhouse/migrations/00009_tiered_storage.sql` — `events_raw` moves onto the policy
  and gains the `TO VOLUME` arm, at seven days.
- `db/clickhouse/migrations/00010_tier_player_activity.sql` — `player_activity_daily` too, at
  ninety days, which keeps the nightly derivation's two-month window three times clear of
  the cold volume.
- Verified on the local stack against a **real GCS bucket**, not a local stand-in:
  `gs://playersense-clickhouse-eu-1-dev` in `europe-west3`, reached through the
  S3-compatible endpoint with an HMAC credential. Rows dated thirty days ago land on `cold`
  and rows dated today on `default`; one `SELECT` reads both without knowing the difference;
  the full test suite and `cohortcheck` pass with object storage underneath.

## Residency

The cold volume holds raw events, so it is player data and §99's rule applies to it exactly
as it applies to the archive: **one bucket per cell, in that cell's region.** `eu-1`'s is in
`europe-west3` — Frankfurt, which [03](../compliance/03-residency-and-transfers.md) names as
the answer a procurement question actually wants.

It is a separate bucket from the Parquet archive rather than a prefix inside it. §60's
retention worker sweeps archive manifests and deletes the objects they name; pointing it at a
bucket that also holds ClickHouse's parts would give it the opportunity to delete something
it does not understand.

The service account behind the HMAC key has `objectAdmin` on the cold bucket and **no
binding at all** on the archive bucket, so a leaked ClickHouse credential does not reach the
canonical copy.

## Open

- **`player_lifetime` has no TTL at all and is not tiered.** One row per device, kept
  forever, on local disk. It grows with the player base rather than with time (§33.1), so it
  is slower than the others — but "slower" is not "bounded", and nothing currently removes a
  device that has not been seen in three years. It stays hot because the query path reads it;
  bounding it is a separate decision about what a dormant device is worth keeping.
- **Cold read performance is unmeasured.** Nothing on the dashboard path touches it, so this
  is not urgent, but §34 should not be designed against a guess.
- **No lifecycle rule on either bucket, deliberately.** ClickHouse's TTL owns expiry on the
  cold volume and §60's retention worker owns it on the archive; a bucket rule would be a
  second, uncoordinated opinion about when data dies. §74 permits one as a "global safety
  net" and that stays available — but a safety net under a worker that is not built yet is
  just an undocumented delete. The decision is recorded as an `expiry-owner` label on each
  bucket, so it is on the resource rather than only in this file.
