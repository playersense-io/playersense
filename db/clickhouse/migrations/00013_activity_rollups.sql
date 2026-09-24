-- Whole-project activity by minute and by hour (project.md §26, §27, §67).
--
-- events_1m and events_1h are keyed by event name, which is right for "how often did
-- level_complete fire" and wrong for "how many players are on right now". Answering the second
-- from them means merging one `uniq` state per event name per bucket, and a `uniq` state holds
-- up to 65,536 hashes: a busy event's hourly state is a quarter of a megabyte. A 90-day hourly
-- chart of active players read that way merges thousands of them, which is the one read on the
-- dashboard path that grows with traffic rather than with the range.
--
-- These two carry the project total directly, one row per bucket, with `uniqCombined(14)`:
-- a fixed ceiling of about 12 KiB per state and roughly 0.8% error, exact while the set is
-- small. The planner reads them for any minute or hour question that does not name an event.
--
-- They also carry what the operations console needs and nothing else records:
--
--   * payload_bytes, the metered logical size (§39), so storage can be attributed per project;
--   * late_events, rows whose event_time is more than an hour before the gateway accepted them,
--     which is the SDK's disk spool draining (§18) rather than live traffic;
--   * lag, how long after the gateway accepted a row analytics-writer stored it. Measured by
--     the view itself with now64() at insert time, so it is the pipeline's real delay and not
--     an estimate. A retried insert is deduplicated before the view runs
--     (deduplicate_blocks_in_dependent_materialized_views), so it is not counted twice.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS activity_1m
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    minute          DateTime                                COMMENT 'class=behavioural',

    -- Constant per project. A value rather than a key column, because nothing on the dashboard
    -- path filters on it and the operations console groups by it.
    org_seq         SimpleAggregateFunction(any, UInt32)    COMMENT 'class=none',

    events          SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    -- countIf(session_start), the same definition player_activity_daily pins.
    sessions        SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',

    payload_bytes   SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    late_events     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    lag_ms_sum      SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    lag_ms_max      SimpleAggregateFunction(max, UInt32)    COMMENT 'class=none',

    batches         AggregateFunction(uniqCombined(12), UUID)   COMMENT 'class=derived',
    players         AggregateFunction(uniqCombined(14), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (project_seq, environment_seq, minute)
-- The realtime window (§26: the last 24 hours) with a week of margin, dropped a day at a time.
TTL minute + INTERVAL 7 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_activity_1m TO activity_1m AS
SELECT
    project_seq,
    environment_seq,
    toStartOfMinute(event_time)                                             AS minute,
    any(org_seq)                                                            AS org_seq,
    count()                                                                 AS events,
    countIf(event_name = 'session_start')                                   AS sessions,
    sum(playtime_ms)                                                        AS playtime_ms,
    sum(revenue_micros)                                                     AS revenue_micros,
    countIf(event_type = 'purchase')                                        AS purchases,
    sum(toUInt64(payload_bytes))                                            AS payload_bytes,
    countIf(dateDiff('second', event_time, ingest_time) > 3600)             AS late_events,
    sum(toUInt64(greatest(dateDiff('millisecond', ingest_time, now64(3)), 0))) AS lag_ms_sum,
    max(toUInt32(greatest(dateDiff('millisecond', ingest_time, now64(3)), 0))) AS lag_ms_max,
    uniqCombinedState(12)(batch_id)                                         AS batches,
    uniqCombinedState(14)(device_id)                                        AS players
FROM events_raw
GROUP BY project_seq, environment_seq, minute
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS activity_1h
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    hour            DateTime                                COMMENT 'class=behavioural',

    org_seq         SimpleAggregateFunction(any, UInt32)    COMMENT 'class=none',

    events          SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    sessions        SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',

    payload_bytes   SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    late_events     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    lag_ms_sum      SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=none',
    lag_ms_max      SimpleAggregateFunction(max, UInt32)    COMMENT 'class=none',

    batches         AggregateFunction(uniqCombined(12), UUID)   COMMENT 'class=derived',
    players         AggregateFunction(uniqCombined(14), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (project_seq, environment_seq, hour)
TTL hour + INTERVAL 400 DAY
-- +goose StatementEnd

-- +goose StatementBegin
-- Chained from activity_1m rather than read off events_raw a second time.
--
-- The block this view sees is what mv_activity_1m just wrote: already one row per project per
-- minute, so rolling it up to the hour touches dozens of rows where reading events_raw again
-- would touch the whole insert. Every column is a sum, a max or a mergeable state, so a
-- minute's rows arriving in several blocks still roll up to the same hour.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_activity_1h TO activity_1h AS
SELECT
    project_seq,
    environment_seq,
    toStartOfHour(minute)                    AS hour,
    any(org_seq)                             AS org_seq,
    sum(events)                              AS events,
    sum(sessions)                            AS sessions,
    sum(playtime_ms)                         AS playtime_ms,
    sum(revenue_micros)                      AS revenue_micros,
    sum(purchases)                           AS purchases,
    sum(payload_bytes)                       AS payload_bytes,
    sum(late_events)                         AS late_events,
    sum(lag_ms_sum)                          AS lag_ms_sum,
    max(lag_ms_max)                          AS lag_ms_max,
    uniqCombinedMergeState(12)(batches)      AS batches,
    uniqCombinedMergeState(14)(players)      AS players
FROM activity_1m
GROUP BY project_seq, environment_seq, hour
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_activity_1h
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS activity_1h SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_activity_1m
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS activity_1m SYNC
-- +goose StatementEnd
