-- One row per device per day (project.md §29.7).
--
-- Every measure is additive, so this is a SummingMergeTree rather than an
-- AggregatingMergeTree: no states, no -Merge suffixes, and DAU is count() over rows.
--
-- This is the largest table the cohort model adds — roughly 10 GB a month compressed at
-- 10M DAU (§33.1) — which is why the TTL is aggressive and why anything that must
-- outlive it lives in player_lifetime instead.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS player_activity_daily
(
    project_seq     UInt32  COMMENT 'class=none',
    environment_seq UInt16  COMMENT 'class=none',
    activity_date   Date    COMMENT 'class=behavioural',
    device_id       UInt64  COMMENT 'class=identifier',

    sessions        UInt64  COMMENT 'class=behavioural',
    playtime_ms     UInt64  COMMENT 'class=behavioural',
    events          UInt64  COMMENT 'class=behavioural',
    revenue_micros  Int64   COMMENT 'class=financial',
    purchases       UInt64  COMMENT 'class=financial'
)
ENGINE = ReplicatedSummingMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, activity_date, device_id)
TTL activity_date + INTERVAL 400 DAY
-- +goose StatementEnd

-- +goose StatementBegin
-- `sessions` is countIf(session_start), pinned here rather than left to a caller.
-- Written as a distinct count of session_id it would need aggregate states and this
-- table would stop being additive (§29.7).
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_player_activity_daily TO player_activity_daily AS
SELECT
    project_seq,
    environment_seq,
    event_date                              AS activity_date,
    device_id,
    countIf(event_name = 'session_start')   AS sessions,
    sum(playtime_ms)                        AS playtime_ms,
    count()                                 AS events,
    sum(revenue_micros)                     AS revenue_micros,
    countIf(event_type = 'purchase')        AS purchases
FROM events_raw
GROUP BY project_seq, environment_seq, activity_date, device_id
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_player_activity_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS player_activity_daily
-- +goose StatementEnd
