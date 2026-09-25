-- Outgoing webhooks' log (project.md §49-51, ADR 0049).
--
-- PostgreSQL holds only what is in flight: the outbox of events not yet delivered and the
-- deliveries still owed a retry. Everything that happened is here, written by the worker in
-- batches: every try of every delivery, the events' bodies so one can be read or sent again, and
-- two tables built from the tries so no page reads the tries themselves to answer a list or a
-- chart.
--
-- A delivery succeeding at its first try, which is almost all of them, costs PostgreSQL nothing
-- and ClickHouse one row. That is what lets the log grow to millions of deliveries a day.
--
-- Thirty days of tries and bodies, ninety of the hourly health.

-- +goose Up
-- +goose StatementBegin
-- Each event as it was POSTed, once however many endpoints heard it.
CREATE TABLE IF NOT EXISTS webhook_events
(
    project_seq  UInt32                  COMMENT 'class=none',
    event_id     UUID                    COMMENT 'class=none',
    type         LowCardinality(String)  COMMENT 'class=none',
    environment  LowCardinality(String)  COMMENT 'class=none',
    created_at   DateTime64(3, 'UTC')    COMMENT 'class=none',
    payload      String CODEC(ZSTD(3))   COMMENT 'class=identifier'
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(created_at)
ORDER BY (project_seq, event_id)
TTL toDateTime(created_at) + INTERVAL 30 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
-- Every try. Sorted by delivery within an endpoint, so one delivery's tries are one short range.
CREATE TABLE IF NOT EXISTS webhook_attempts
(
    project_seq      UInt32                         COMMENT 'class=none',
    endpoint_id      UUID                           COMMENT 'class=none',
    delivery_id      UUID                           COMMENT 'class=none',
    event_id         UUID                           COMMENT 'class=none',
    type             LowCardinality(String)         COMMENT 'class=none',
    environment      LowCardinality(String)         COMMENT 'class=none',
    delivery_created DateTime64(3, 'UTC')           COMMENT 'class=none',
    attempt          UInt16                         COMMENT 'class=none',
    at               DateTime64(3, 'UTC')           COMMENT 'class=none',
    -- The HTTP status; 0 when no answer came at all.
    status_code      UInt16                         COMMENT 'class=none',
    error            LowCardinality(String)         COMMENT 'class=none',
    duration_ms      UInt32                         COMMENT 'class=none',
    -- The answer's body, cut at 4 KB.
    response         String CODEC(ZSTD(3))          COMMENT 'class=none',
    -- What the delivery is after this try: succeeded, retrying or failed.
    outcome          LowCardinality(String)         COMMENT 'class=none',
    next_at          Nullable(DateTime64(3, 'UTC')) COMMENT 'class=none',
    manual           UInt8                          COMMENT 'class=none',
    test             UInt8                          COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toMonday(at)
ORDER BY (project_seq, endpoint_id, delivery_id, at)
TTL toDateTime(at) + INTERVAL 30 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
-- Each delivery as it stands: its latest try replaces the one before. Sorted newest-first-able by
-- when it was made, which is how the deliveries list pages.
CREATE TABLE IF NOT EXISTS webhook_deliveries
(
    project_seq      UInt32                         COMMENT 'class=none',
    endpoint_id      UUID                           COMMENT 'class=none',
    delivery_created DateTime64(3, 'UTC')           COMMENT 'class=none',
    delivery_id      UUID                           COMMENT 'class=none',
    event_id         UUID                           COMMENT 'class=none',
    type             LowCardinality(String)         COMMENT 'class=none',
    environment      LowCardinality(String)         COMMENT 'class=none',
    attempts         UInt16                         COMMENT 'class=none',
    outcome          LowCardinality(String)         COMMENT 'class=none',
    last_at          DateTime64(3, 'UTC')           COMMENT 'class=none',
    last_code        UInt16                         COMMENT 'class=none',
    last_error       LowCardinality(String)         COMMENT 'class=none',
    next_at          Nullable(DateTime64(3, 'UTC')) COMMENT 'class=none',
    manual           UInt8                          COMMENT 'class=none',
    test             UInt8                          COMMENT 'class=none',
    version          UInt64                         COMMENT 'class=none'
)
ENGINE = ReplicatedReplacingMergeTree(version)
PARTITION BY toMonday(delivery_created)
ORDER BY (project_seq, endpoint_id, delivery_created, delivery_id)
TTL toDateTime(delivery_created) + INTERVAL 30 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_webhook_deliveries TO webhook_deliveries AS
SELECT
    a.project_seq                     AS project_seq,
    a.endpoint_id                     AS endpoint_id,
    a.delivery_created                AS delivery_created,
    a.delivery_id                     AS delivery_id,
    a.event_id                        AS event_id,
    a.type                            AS type,
    a.environment                     AS environment,
    a.attempt                         AS attempts,
    a.outcome                         AS outcome,
    a.at                              AS last_at,
    a.status_code                     AS last_code,
    a.error                           AS last_error,
    a.next_at                         AS next_at,
    a.manual                          AS manual,
    a.test                            AS test,
    toUInt64(toUnixTimestamp64Milli(a.at)) * 100 + a.attempt AS version
FROM webhook_attempts AS a
-- +goose StatementEnd

-- +goose StatementBegin
-- Each endpoint's hour: tries and how many were answered 2xx, deliveries made and how many landed at
-- their first try, how many ended delivered or given up, and the answers' latency. What the health
-- figures and the chart read, and what staff read across every endpoint. Test sends are left out.
CREATE TABLE IF NOT EXISTS webhook_health_1h
(
    project_seq UInt32                                               COMMENT 'class=none',
    endpoint_id UUID                                                 COMMENT 'class=none',
    hour        DateTime('UTC')                                      COMMENT 'class=none',
    tries       SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    ok          SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    deliveries  SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    first_ok    SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    succeeded   SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    failed      SimpleAggregateFunction(sum, UInt64)                 COMMENT 'class=none',
    latency     AggregateFunction(quantilesTDigest(0.5, 0.95), UInt32) COMMENT 'class=none'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (project_seq, endpoint_id, hour)
TTL hour + INTERVAL 90 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_webhook_health_1h TO webhook_health_1h AS
SELECT
    a.project_seq                                                        AS project_seq,
    a.endpoint_id                                                        AS endpoint_id,
    toStartOfHour(a.at)                                                  AS hour,
    count()                                                              AS tries,
    countIf(a.status_code >= 200 AND a.status_code < 300)                AS ok,
    countIf(a.attempt = 1 AND a.manual = 0)                              AS deliveries,
    countIf(a.attempt = 1 AND a.manual = 0 AND a.status_code >= 200 AND a.status_code < 300) AS first_ok,
    countIf(a.outcome = 'succeeded')                                     AS succeeded,
    countIf(a.outcome = 'failed')                                        AS failed,
    quantilesTDigestStateIf(0.5, 0.95)(a.duration_ms, a.status_code > 0) AS latency
FROM webhook_attempts AS a
WHERE a.test = 0
GROUP BY project_seq, endpoint_id, hour
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_webhook_health_1h
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_health_1h SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_webhook_deliveries
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_deliveries SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_attempts SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_events SYNC
-- +goose StatementEnd
