# 0050. Logs are a stream of their own, stored where they are counted, and billed as they cost

**Status:** Accepted
**Date:** 2026-09-25
**Builds on:** [ADR 0023](0023-blocks-are-sized-for-merges-not-for-messages.md), [ADR 0024](0024-the-hot-window-is-a-cache-not-a-retention-limit.md), [ADR 0039](0039-players-are-charged-as-they-arrive.md), [ADR 0044](0044-usage-settles-when-it-matters.md), [ADR 0046](0046-every-page-reads-a-fact-built-for-it.md)

## Context

Studios want to read their game's own logs, exceptions and crashes beside its analytics: the
Sentry and Cloud Logging job, inside the product they already integrate. ADR 0046 noted that
crashes had no fact behind them because nothing ingested them.

Logs differ from events in every way that matters to the architecture:

- **Volume.** A game logs an order of magnitude more bytes than it tracks events, and a bad build
  can log in a tight loop. The path has to take millions of requests an hour without affecting
  analytics, and without a runaway build filling a studio's bill before anybody notices.
- **Access.** Written once and never updated. Read newest first inside a time range, searched by
  word, filtered by level and release, and after a day or two barely read at all.
- **Cost.** Almost all of what a log costs is storage and the operations that move it. The
  object store bills $0.02 a GB-month, a write operation (class A) at $0.005 per thousand and a
  read (class B) at $0.0004 per thousand; the queue bills $40 a TiB. A price has to cover every one
  of those with a margin, whatever a studio's volume and retention.
- **Retention is a choice.** A studio that keeps a day of logs and one that keeps ninety cost us
  ninety times apart. The studio has to choose, and the bill has to follow the choice.

## Decision

**A stream of its own.** `POST /v1/logs` takes a `LogBatch` (docs/sdk/protocol.md §15): the analytics
header, the same key and consent block, and up to a thousand records, each a log line, a caught
exception (a type, a value, frames) or a crash (`crash: true`, sent on the next launch). The gateway
does what it does for events and nothing more: authenticate, rate-limit on budgets of its own,
bound and size each record (text is cut, never refused), and publish. It publishes to a topic of
its own (`PUBSUB_LOGS_TOPIC`), so a crash storm queues behind itself and never ahead of analytics,
and it publishes the batch **zstd-compressed**, because the queue bills raw bytes and log text
compresses five to ten times. The writer runs in the analytics-writer process on its own
subscription: one always-on process is cheaper than two, and separate positions mean neither
stream can hold the other back. Deliveries are grouped by rows (ADR 0023), so an insert is tens of
thousands of rows and a merge is cheap.

**Stored in ClickHouse, not a search engine.** One `logs` table sorted by (project, environment,
time): a newest-first page of a range is a read in order that stops at the page, a few granules
whatever the volume. Search is a **text index** over the message and the exception, lower-cased with
CamelCase and digits split apart first, so `timeout` finds `TimeoutException` and a two-word search
reads only the granules holding both; the query spells the index's expression exactly, and
`internal/logs/search.go` mirrors its tokenizer. Columnar storage compresses repeated log templates
eight to fifteen times. An Elasticsearch or OpenSearch cluster would hold the same bytes several
times over on SSD, with an inverted index the size of the data, for an access pattern that is
nearly all "the last hour, newest first".

**Every page reads a fact built for it (ADR 0046).** Four tables are filled as rows arrive:
`logs_volume_1m` (records and bytes a minute by level: the histogram over any range is a few
thousand rows), `log_issues` and `log_issue_hours` (an issue's identity, first and last seen,
events and installs an hour: the issues list, trends and sparklines), and `log_usage_hourly`
(bytes an hour by organization and project, by arrival, kept 400 days: both meters). A chart
reads the raw table only when the search has words or fields in it, and then the index narrows it.

**Issues are fingerprints, computed by the writer.** An exception is its type and the functions of
the game's own frames, innermost first, without line numbers; an error log is its logger and its
message with numbers and ids taken out; a studio may send its own fingerprint. What a studio
decides about an issue (resolved, ignored) is a PostgreSQL row; an issue seen after it was resolved
is shown as regressed, from the two timestamps.

**Hot for two days, then object storage.** Parts move to the cold volume after two days (ADR 0024)
and are dropped whole when their retention ends (`PARTITION BY (retention_days, day)`,
`ttl_only_drop_parts`). Parts are never merged on the cold volume, so object storage sees one write
per part and one delete when it expires. Rollups stay on local disk, so the chart, the figures and
the issues never read object storage at all; only a record search reaching past two days does.

**Retention is the studio's, per project:** 1, 3, 7, 14 (the default), 30, 60 or 90 days. It is
stamped on every record, so a change reaches new records as the key cache turns over. A lower
retention is enforced on older records with a **lightweight delete** (a mask beside each part, not
a rewrite, so object storage is not downloaded and uploaded again for rows nobody will read), and
the pages and the bill stop counting them at once.

**Billed as it costs, on two meters priced by rate cards (ADR 0039, 0044):**

| Meter | Unit | Covers | List price | Free each month |
|---|---|---|---|---|
| `log_ingest_mb` | a megabyte taken in, uncompressed | the queue, the CPU, the object-store writes | $0.10 a GB | 1 GB |
| `log_storage_gb_days` | a gigabyte kept for a day | disk and object storage for the retention chosen | $0.03 a GB-month | 1 GB-month |

A plan's own rate card wins; a plan without one is charged the deployment's list price
(`LOG_INGEST_PRICE_PER_GB_MICROS`, `LOG_STORAGE_PRICE_PER_GB_MONTH_MICROS`), published as a card
effective from the beginning of time so every charge still names the card that priced it. The free
tier is an entitlement (`included_log_ingest_gb`, `included_log_storage_gb_month`), defaulting to a
gigabyte of each for every organization. The worker prices ingestion every pass (a quarter of an
hour) and storage once a day, each counting forward from a position under the month's lock, so a
pass that runs twice charges nothing twice. Charges are ordinary usage charges: the drain settles
them, the suspension gate stops an account that cannot pay (ADR 0043), and the invoice names Cloud
Logging on a line of its own.

### The cost model behind the list price

Per logical (uncompressed) gigabyte, at a conservative 8x compression and two replicas:

| | Our cost | Priced by |
|---|---|---|
| Queue, publish and delivery, compressed 5x | ~$0.016 | ingestion |
| Gateway, writer and inserts | ~$0.005 | ingestion |
| Hot SSD for two days | ~$0.003 | ingestion |
| Class A writes: a part moved to object storage is ~90 objects, once, after merging | under $0.001 at any real volume (a few dollars a month for the whole cluster) | ingestion |
| **Taken in, total** | **~$0.025 a GB** | **$0.10** |
| Object storage, $0.02 a GB-month on an eighth of the bytes, twice | ~$0.005 a GB-month | storage |
| Class B reads: only a record search past the hot window reaches object storage | fractions of a cent per search | storage |
| **Kept, total** | **~$0.005 a GB-month** | **$0.03** |

A studio sending 5 GB a day and keeping 14 days pays about $15 for 150 GB taken in and about $2 for
the ~70 GB-months kept: $17 a month, against about $4 of cost. The free tier costs us about three
cents an organization a month.

## Consequences

- Delivery is at least once, and a redelivery grouped differently from the first can store a record
  twice; the insert token prevents it in the common case. For logs this is accepted, as it is not
  for events.
- The list price is deployment configuration, not a rate card staff wrote. It exists so that a plan
  nobody priced for logs still covers its costs; the moment staff publish a card, it applies.
- Storage is billed by the bytes the game sent, not by what ClickHouse stores. A customer can
  predict it; the compression margin is ours.
- A studio lowering its retention loses the older records for good, and the dialog says so before
  it saves.
- A game has to send logs before any of this has anything to show. The protocol is specified
  (protocol.md §15); the Rust core and the Unity binding do not capture logs yet.

## Alternatives considered

- **Elasticsearch or OpenSearch.** Full-text search is its strength, and it is not what log readers
  mostly do. Several copies of every byte on SSD, an index as large as the data, and a cluster to
  operate. ClickHouse is already here, and its text index answers word searches.
- **Loki-style object storage with no index.** Cheapest to store, but every search is a scan of the
  range, and a dashboard histogram over a week would read a week of logs.
- **One meter, per GB stored.** Ingestion costs (the queue, the CPU, the object-store writes) are
  paid once whatever the retention, so a one-day studio would be subsidised by a ninety-day one.
- **Metering from ClickHouse's parts.** Parts hold many organizations, are replicated, and shrink as
  they merge; none of that is the customer's, and none of it is predictable to them.
- **Attributing object-store operations to each organization.** A part holds many organizations'
  rows, so an operation has no single owner. The operations are priced into the ingestion meter,
  where they scale with what each organization sends.

## Enforcement

- `db/clickhouse/migrations/00025_logs.sql`; `migrate verify` classifies every column.
- `internal/logs` (queue, writer, fingerprint, search, reads) and its tests: grouping survives what
  changes between occurrences, the tokenizer splits as the index does, the queued payload is
  compressed.
- `internal/ingest/logs.go` and its test: bounds, truncation, derived ids, kids_strict.
- `internal/charging/logs.go`: both meters, the free tier, the list-price card.
- `internal/projects` `RunLogs`, `internal/erasure` `purgeLogs`: a lowered retention and a person's
  erasure reach the logs.
- `cmd/api/control_audit_test.go`: the two log writes are audited, ingestion is exempt.
