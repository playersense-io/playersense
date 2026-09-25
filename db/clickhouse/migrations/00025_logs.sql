-- Cloud Logging: the game's own logs, exceptions and crashes (ADR 0050).
--
-- One wide table holds every record, and three small tables are built from it as it is written,
-- so that no page reads the records to answer a count:
--
--   logs               every record, searchable, kept for the project's log retention
--   logs_volume_1m     records and bytes a minute, by severity: the chart, and the storage meter
--   log_issues         one row per issue (a fingerprint): what it is, when it was first and last seen
--   log_issue_hours    an issue's events and affected installs an hour: its list, trend and sparkline
--   log_usage_hourly   bytes an hour by organization and project, for the two meters; outlives the logs
--
-- Why ClickHouse and not a search engine. Logs are written once, never updated, read newest first
-- inside a time range, and almost never read at all after a day or two. A column store compresses
-- them ten to twenty times (the same message template repeats across millions of rows), sorts them
-- by time so "the newest 200 in the last hour" reads one granule, and its text index answers a
-- word search without an inverted index the size of the data. An Elasticsearch cluster would hold
-- the same bytes several times over on SSD; this holds them once, compressed, and moves them to
-- object storage after two days (the hot window, ADR 0024).
--
-- Retention is the project's own choice (projects.log_retention_days), stamped on every row as
-- retention_days, and is part of the partition key for the reason events_raw gives: a part only
-- expires whole when every row in it shares the TTL.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS logs
(
    org_seq          UInt32                      COMMENT 'class=none',
    project_seq      UInt32                      COMMENT 'class=none',
    environment_seq  UInt16                      COMMENT 'class=none',
    retention_days   UInt16                      COMMENT 'class=none',

    -- When the game says it happened, bounded by the gateway (a device clock is not trusted
    -- past the skew limits), and when the gateway took it.
    timestamp        DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)) COMMENT 'class=behavioural',
    received_at      DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)) COMMENT 'class=behavioural',

    record_id        UUID                        COMMENT 'class=none',
    severity         Enum8('trace' = 1, 'debug' = 2, 'info' = 3, 'warn' = 4, 'error' = 5, 'fatal' = 6) COMMENT 'class=none',
    kind             Enum8('log' = 1, 'exception' = 2, 'crash' = 3) COMMENT 'class=none',
    logger           LowCardinality(String)      COMMENT 'class=none',

    -- Free text the game wrote. Classified identifier because nothing stops a game from logging
    -- a name or an address, and a column that may hold one is treated as one (§96).
    message          String CODEC(ZSTD(3))       COMMENT 'class=identifier',
    exception_type   LowCardinality(String)      COMMENT 'class=none',
    exception_value  String CODEC(ZSTD(3))       COMMENT 'class=identifier',
    -- The frames as JSON, innermost last: [{"function","module","file","line","in_app"}].
    stacktrace       String CODEC(ZSTD(3))       COMMENT 'class=none',
    -- What happened just before a crash, as JSON: [{"at","category","message","level"}].
    breadcrumbs      String CODEC(ZSTD(3))       COMMENT 'class=identifier',
    handled          UInt8                       COMMENT 'class=none',
    -- Where it happened: the innermost frame of the game's own code, "Module.Function".
    culprit          LowCardinality(String)      COMMENT 'class=none',

    -- Which issue the record belongs to; 0 for a plain log line. Computed by the writer from the
    -- exception type and its in-app frames, or from the message with its numbers taken out.
    fingerprint      UInt64                      COMMENT 'class=none',

    session_id       String CODEC(ZSTD(1))       COMMENT 'class=identifier',
    install_id       UUID                        COMMENT 'class=identifier',
    player_id        Nullable(String)            COMMENT 'class=identifier',

    app_version      LowCardinality(String)      COMMENT 'class=behavioural',
    app_build        LowCardinality(String)      COMMENT 'class=behavioural',
    platform         LowCardinality(String)      COMMENT 'class=behavioural',
    os_version       LowCardinality(String)      COMMENT 'class=behavioural',
    device_model     LowCardinality(String)      COMMENT 'class=behavioural',
    country_code     LowCardinality(String)      COMMENT 'class=behavioural',
    sdk_name         LowCardinality(String)      COMMENT 'class=none',
    sdk_version      LowCardinality(String)      COMMENT 'class=none',

    attributes       Map(LowCardinality(String), String) CODEC(ZSTD(1)) COMMENT 'class=identifier',

    -- What the record is billed as: its size as the game sent it, uncompressed. Not what it costs
    -- on disk, which depends on its neighbours and is ours to make small.
    size             UInt32                      COMMENT 'class=none',

    -- Words in the message and the exception, lower-cased, with CamelCase and letters against
    -- digits split apart first: "timeout" finds TimeoutException and "ms" finds 30000ms. A search
    -- for two words reads only the granules holding both. internal/logs/search.go spells the same
    -- expression; the two must match exactly or ClickHouse does not use the index.
    INDEX idx_text lower(replaceRegexpAll(concat(message, ' ', exception_type, ' ', exception_value), '([a-z])([A-Z])|([A-Za-z])([0-9])|([0-9])([A-Za-z])', '\\1\\3\\5 \\2\\4\\6')) TYPE text(tokenizer = splitByNonAlpha),
    INDEX idx_severity severity TYPE minmax GRANULARITY 1,
    INDEX idx_fingerprint fingerprint TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_install install_id TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_player player_id TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_session session_id TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attribute_keys mapKeys(attributes) TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = ReplicatedMergeTree
PARTITION BY (retention_days, toDate(timestamp))
-- Newest first inside one environment is every read the page makes, so time is the last key and
-- a descending read of a range is a read in order, stopping at the page it needs.
ORDER BY (project_seq, environment_seq, timestamp)
TTL toDateTime(timestamp) + INTERVAL 2 DAY TO VOLUME 'cold',
    toDateTime(timestamp) + toIntervalDay(retention_days) DELETE
SETTINGS storage_policy = 'hot_cold', ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
-- A minute of one environment's records by severity and kind: what the chart draws over any range
-- without reading a record, and what the storage meter sums.
CREATE TABLE IF NOT EXISTS logs_volume_1m
(
    project_seq      UInt32                      COMMENT 'class=none',
    environment_seq  UInt16                      COMMENT 'class=none',
    retention_days   UInt16                      COMMENT 'class=none',
    minute           DateTime('UTC')             COMMENT 'class=none',
    severity         Enum8('trace' = 1, 'debug' = 2, 'info' = 3, 'warn' = 4, 'error' = 5, 'fatal' = 6) COMMENT 'class=none',
    kind             Enum8('log' = 1, 'exception' = 2, 'crash' = 3) COMMENT 'class=none',
    records          UInt64                      COMMENT 'class=none',
    bytes            UInt64                      COMMENT 'class=none'
)
ENGINE = ReplicatedSummingMergeTree
PARTITION BY toYYYYMM(minute)
ORDER BY (project_seq, environment_seq, minute, severity, kind, retention_days)
TTL minute + toIntervalDay(retention_days)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_logs_volume_1m TO logs_volume_1m AS
SELECT
    project_seq,
    environment_seq,
    retention_days,
    toStartOfMinute(timestamp) AS minute,
    severity,
    kind,
    count()                    AS records,
    sum(size)                  AS bytes
FROM logs
GROUP BY project_seq, environment_seq, retention_days, minute, severity, kind
-- +goose StatementEnd

-- +goose StatementBegin
-- An issue: every exception and crash sharing a fingerprint. What it is, where it came from, and
-- when it was first and last seen, merged as records arrive.
CREATE TABLE IF NOT EXISTS log_issues
(
    project_seq      UInt32                                                      COMMENT 'class=none',
    environment_seq  UInt16                                                      COMMENT 'class=none',
    fingerprint      UInt64                                                      COMMENT 'class=none',
    kind             SimpleAggregateFunction(max, UInt8)                         COMMENT 'class=none',
    severity         SimpleAggregateFunction(max, UInt8)                         COMMENT 'class=none',
    first_seen       SimpleAggregateFunction(min, DateTime64(3, 'UTC'))          COMMENT 'class=none',
    last_seen        SimpleAggregateFunction(max, DateTime64(3, 'UTC'))          COMMENT 'class=none',
    events           SimpleAggregateFunction(sum, UInt64)                        COMMENT 'class=none',
    retention_days   SimpleAggregateFunction(max, UInt16)                        COMMENT 'class=none',
    exception_type   AggregateFunction(argMax, String, DateTime64(3, 'UTC'))     COMMENT 'class=none',
    title            AggregateFunction(argMax, String, DateTime64(3, 'UTC'))     COMMENT 'class=identifier',
    culprit          AggregateFunction(argMax, String, DateTime64(3, 'UTC'))     COMMENT 'class=none',
    first_release    AggregateFunction(argMin, String, DateTime64(3, 'UTC'))     COMMENT 'class=none',
    last_release     AggregateFunction(argMax, String, DateTime64(3, 'UTC'))     COMMENT 'class=none',
    installs         AggregateFunction(uniqCombined, UUID)                       COMMENT 'class=none'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, fingerprint)
-- An issue nobody has seen for a whole retention is gone with its last record.
TTL toDateTime(last_seen) + toIntervalDay(retention_days)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_log_issues TO log_issues AS
SELECT
    project_seq,
    environment_seq,
    fingerprint,
    max(toUInt8(kind))                                   AS kind,
    max(toUInt8(severity))                               AS severity,
    min(timestamp)                                       AS first_seen,
    max(timestamp)                                       AS last_seen,
    count()                                              AS events,
    max(retention_days)                                  AS retention_days,
    argMaxState(toString(exception_type), timestamp)     AS exception_type,
    argMaxState(if(exception_value != '', exception_value, message), timestamp) AS title,
    argMaxState(toString(culprit), timestamp)            AS culprit,
    argMinState(toString(app_version), timestamp)        AS first_release,
    argMaxState(toString(app_version), timestamp)        AS last_release,
    uniqCombinedState(install_id)                        AS installs
FROM logs
WHERE fingerprint != 0
GROUP BY project_seq, environment_seq, fingerprint
-- +goose StatementEnd

-- +goose StatementBegin
-- An issue's hour: how often it happened and to how many installs. The list sorts by it over the
-- chosen range, and an issue's trend and sparkline are read from it.
CREATE TABLE IF NOT EXISTS log_issue_hours
(
    project_seq      UInt32                                  COMMENT 'class=none',
    environment_seq  UInt16                                  COMMENT 'class=none',
    fingerprint      UInt64                                  COMMENT 'class=none',
    hour             DateTime('UTC')                         COMMENT 'class=none',
    events           SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    installs         AggregateFunction(uniqCombined, UUID)   COMMENT 'class=none',
    retention_days   SimpleAggregateFunction(max, UInt16)    COMMENT 'class=none'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (project_seq, environment_seq, fingerprint, hour)
TTL hour + toIntervalDay(retention_days)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_log_issue_hours TO log_issue_hours AS
SELECT
    project_seq,
    environment_seq,
    fingerprint,
    toStartOfHour(timestamp)       AS hour,
    count()                        AS events,
    uniqCombinedState(install_id)  AS installs,
    max(retention_days)            AS retention_days
FROM logs
WHERE fingerprint != 0
GROUP BY project_seq, environment_seq, fingerprint, hour
-- +goose StatementEnd

-- +goose StatementBegin
-- What the meters read (ADR 0050): bytes taken in an hour, by organization, project and the retention
-- they were stamped with. Kept 400 days whatever the logs' own retention, because a month is billed
-- after its logs may be gone: a project keeping one day of logs is still billed for what it sent on
-- the first of the month when the month closes. Kept bytes at any moment are the hours still inside
-- their retention; ingested bytes are every hour of the month. By the hour the gateway took them,
-- not the hour the game says they happened: a crash sent a week late is billed in the month it
-- arrived, never in a month that has already closed.
CREATE TABLE IF NOT EXISTS log_usage_hourly
(
    org_seq          UInt32            COMMENT 'class=none',
    project_seq      UInt32            COMMENT 'class=none',
    environment_seq  UInt16            COMMENT 'class=none',
    retention_days   UInt16            COMMENT 'class=none',
    hour             DateTime('UTC')   COMMENT 'class=none',
    records          UInt64            COMMENT 'class=none',
    bytes            UInt64            COMMENT 'class=none'
)
ENGINE = ReplicatedSummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (org_seq, project_seq, hour, environment_seq, retention_days)
TTL hour + INTERVAL 400 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_log_usage_hourly TO log_usage_hourly AS
SELECT
    org_seq,
    project_seq,
    environment_seq,
    retention_days,
    toStartOfHour(received_at) AS hour,
    count()                    AS records,
    sum(size)                  AS bytes
FROM logs
GROUP BY org_seq, project_seq, environment_seq, retention_days, hour
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_log_usage_hourly
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS log_usage_hourly SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_log_issue_hours
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS log_issue_hours SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_log_issues
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS log_issues SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_logs_volume_1m
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS logs_volume_1m SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS logs SYNC
-- +goose StatementEnd
