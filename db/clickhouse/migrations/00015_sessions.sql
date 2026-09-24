-- Sessions as facts of their own (project.md §13.1 `sessions_final`, §27 `sessions_1d`, §30).
--
-- metrics_daily counts sessions as session_start events, which is enough for "how many" and
-- says nothing about how long a session was, how it ended, or which ones are happening now.
-- Those need the session as a unit: its first and last event, whether a session_end arrived.
--
-- Two tables, for the reason metrics_daily and player_activity_daily are two (§29.14):
--
--   * `sessions` is an incremental view, one row per session per day, built as blocks arrive.
--     A session's events arrive over many blocks, so until parts merge it exists as several
--     partial rows, and every read groups by the session.
--   * `sessions_daily` is derived nightly from it, one whole month at a time, once each session
--     is complete. A length is only a length once the session is over, and a view cannot know
--     when that is.

-- +goose Up
-- +goose StatementBegin
-- One row per session per day it had events on.
--
-- Keyed by the day the events happened rather than the day the session started, because the
-- start is not known to the block carrying a session's later events. A session played across
-- midnight therefore has a row on each side, and every read groups by (device_id, session_key),
-- which puts it back together. session_key is the session id hashed, so that grouping is on two
-- integers rather than a string.
--
-- Kept for the project's raw retention or ninety days, whichever is shorter. A session row names
-- the device, the session and the player, so it is raw data in all but shape and must not
-- outlive the events it came from (§59); past ninety days every question is answered by
-- sessions_daily. Partitioned by the tier as events_raw is, so a month of one tier drops whole
-- (ttl_only_drop_parts): a month is either complete or gone, and rebuilding it can never publish
-- half of one.
CREATE TABLE IF NOT EXISTS sessions
(
    project_seq     UInt32                                              COMMENT 'class=none',
    environment_seq UInt16                                              COMMENT 'class=none',
    retention_tier  UInt16                                              COMMENT 'class=none',
    activity_date   Date                                                COMMENT 'class=behavioural',
    device_id       UInt64                                              COMMENT 'class=identifier',
    session_key     UInt64                                              COMMENT 'class=identifier',

    session_id      SimpleAggregateFunction(any, String)                COMMENT 'class=identifier',
    -- The account the session was played under, if the player had signed in. anyLast() skips NULL,
    -- so one signed-in event is enough to name it.
    player_id       SimpleAggregateFunction(anyLast, Nullable(String))  COMMENT 'class=identifier',

    started_at      SimpleAggregateFunction(min, DateTime64(3, 'UTC'))  COMMENT 'class=behavioural',
    ended_at        SimpleAggregateFunction(max, DateTime64(3, 'UTC'))  COMMENT 'class=behavioural',

    events          SimpleAggregateFunction(sum, UInt64)                COMMENT 'class=behavioural',
    playtime_ms     SimpleAggregateFunction(sum, UInt64)                COMMENT 'class=behavioural',
    revenue_micros  SimpleAggregateFunction(sum, Int64)                 COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)                COMMENT 'class=financial',

    -- 1 once a session_end has arrived. Nothing may depend on one arriving (SDK protocol §4), so a
    -- session without it is not a crash; it is a session the operating system ended for us.
    ended           SimpleAggregateFunction(max, UInt8)                 COMMENT 'class=behavioural',

    platform        SimpleAggregateFunction(any, String)                COMMENT 'class=behavioural',
    app_version     SimpleAggregateFunction(any, String)                COMMENT 'class=behavioural',
    country_code    SimpleAggregateFunction(any, String)                COMMENT 'class=behavioural'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY (retention_tier, toYYYYMM(activity_date))
ORDER BY (project_seq, environment_seq, activity_date, device_id, session_key)
TTL activity_date + toIntervalDay(least(retention_tier, 90))
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
-- An event with no session id belongs to no session: the SDK assigns one to everything it
-- tracks, so these are server-side imports, and counting them as one enormous session per day
-- would be the worst possible answer.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_sessions TO sessions AS
SELECT
    e.project_seq                                   AS project_seq,
    e.environment_seq                               AS environment_seq,
    e.retention_tier                                AS retention_tier,
    e.event_date                                    AS activity_date,
    e.device_id                                     AS device_id,
    sipHash64(e.session_id)                         AS session_key,
    any(e.session_id)                               AS session_id,
    anyLast(e.player_id)                            AS player_id,
    min(e.event_time)                               AS started_at,
    max(e.event_time)                               AS ended_at,
    count()                                         AS events,
    sum(e.playtime_ms)                              AS playtime_ms,
    sum(e.revenue_micros)                           AS revenue_micros,
    countIf(e.event_type = 'purchase')              AS purchases,
    max(toUInt8(e.event_name = 'session_end'))      AS ended,
    any(toString(e.platform))                       AS platform,
    any(toString(e.app_version))                    AS app_version,
    any(toString(e.country_code))                   AS country_code
FROM events_raw AS e
WHERE e.session_id != ''
GROUP BY project_seq, environment_seq, retention_tier, activity_date, device_id, session_key
-- +goose StatementEnd

-- +goose StatementBegin
-- Complete sessions by the day they started, their length and how they ended.
--
-- length_bucket indexes a fixed ladder (rollup.SessionLengthBounds): under 30 seconds, then 1,
-- 2, 5, 10, 15, 20, 30 and 45 minutes, 1, 2 and 4 hours, and longer. Fine enough that any coarser
-- chart and any percentile a chart wants can be read off it, and additive, so a range of days
-- is a sum. A percentile stored per day could not be combined into a percentile for the month.
--
-- The length is from the first event to the last. It is what the player experienced as one
-- sitting, including time the SDK reported no playtime for; playtime_ms is carried beside it
-- for the other reading.
--
-- Not a SummingMergeTree, for the reason metrics_daily is not one: the job rebuilds whole
-- partitions, so there is exactly one row per key and nothing to sum.
CREATE TABLE IF NOT EXISTS sessions_daily
(
    project_seq     UInt32                         COMMENT 'class=none',
    environment_seq UInt16                         COMMENT 'class=none',
    session_date    Date                           COMMENT 'class=derived',
    platform        LowCardinality(String)         COMMENT 'class=derived',
    length_bucket   UInt8                          COMMENT 'class=derived',
    outcome         Enum8('ended' = 1, 'timed_out' = 2) COMMENT 'class=derived',

    sessions        UInt64                         COMMENT 'class=derived',
    duration_ms     UInt64                         COMMENT 'class=derived',
    playtime_ms     UInt64                         COMMENT 'class=derived',
    events          UInt64                         COMMENT 'class=derived',
    revenue_micros  Int64                          COMMENT 'class=financial',

    computed_at     DateTime                       COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(session_date)
ORDER BY (project_seq, environment_seq, session_date, platform, length_bucket, outcome)
TTL session_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS sessions_daily_staging
(
    project_seq     UInt32                         COMMENT 'class=none',
    environment_seq UInt16                         COMMENT 'class=none',
    session_date    Date                           COMMENT 'class=derived',
    platform        LowCardinality(String)         COMMENT 'class=derived',
    length_bucket   UInt8                          COMMENT 'class=derived',
    outcome         Enum8('ended' = 1, 'timed_out' = 2) COMMENT 'class=derived',
    sessions        UInt64                         COMMENT 'class=derived',
    duration_ms     UInt64                         COMMENT 'class=derived',
    playtime_ms     UInt64                         COMMENT 'class=derived',
    events          UInt64                         COMMENT 'class=derived',
    revenue_micros  Int64                          COMMENT 'class=financial',
    computed_at     DateTime                       COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(session_date)
ORDER BY (project_seq, environment_seq, session_date, platform, length_bucket, outcome)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sessions_daily_staging SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sessions_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_sessions
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS sessions SYNC
-- +goose StatementEnd
