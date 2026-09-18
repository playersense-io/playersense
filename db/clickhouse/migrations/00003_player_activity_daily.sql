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

    -- The cohort key, carried through from events_raw where analytics-writer stamped it
    -- (§29.5). This is what makes the derivation a single-table GROUP BY: without these
    -- columns the job would have to resolve device -> cohort against player_lifetime,
    -- which is the actor-cardinality join §29.1 forbids.
    --
    -- They cost almost nothing. Every value is constant per device, so a day's rows
    -- compress to a handful of runs.
    cohort_date        Date                   COMMENT 'class=derived',
    install_platform   LowCardinality(String) COMMENT 'class=derived',
    install_country    LowCardinality(String) COMMENT 'class=derived',
    first_app_version  LowCardinality(String) COMMENT 'class=derived',

    sessions        UInt64  COMMENT 'class=behavioural',
    playtime_ms     UInt64  COMMENT 'class=behavioural',
    events          UInt64  COMMENT 'class=behavioural',
    revenue_micros  Int64   COMMENT 'class=financial',
    purchases       UInt64  COMMENT 'class=financial'
)
-- The summed list is explicit: the sticky dimensions are values, not accruals, and a
-- SummingMergeTree with no list sums every numeric column it finds.
ENGINE = ReplicatedSummingMergeTree((sessions, playtime_ms, events, revenue_micros, purchases))
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
    -- min/any over values that are constant per device; the writer stamped them.
    -- A null cohort_date means Valkey could not answer, and the nightly job backfills
    -- those rows rather than losing them (§65).
    min(assumeNotNull(cohort_date))         AS cohort_date,
    any(install_platform)                   AS install_platform,
    any(install_country)                    AS install_country,
    any(first_app_version)                  AS first_app_version,
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
