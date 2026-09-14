-- The daily and monthly product metrics (project.md §26, §27, §38).
--
-- §27 names players_1d, sessions_1d and revenue_1d as three tables. They are one here, for
-- the reason §13.1 already gives when it collapses retention_cohort_daily into
-- cohort_metrics_daily: they share one grain and one key, and "three tables of the same
-- shape cost three scans to answer one question". A dashboard asking for DAU, sessions and
-- revenue over September is asking one question.
--
-- Derived from player_activity_daily rather than from events_raw. §38 requires exactly this
-- of the billing meter — "the billing meter and the cohort tier are reading the same fact.
-- Deriving the two independently would eventually produce a month where the invoice and the
-- dashboard disagree, and no amount of investigation would establish which was right" — and
-- a dashboard that disagrees with the invoice is the same failure wearing a different hat.
--
-- **A job, not a materialized view, and the reason is not performance.** §29.14 spells it
-- out: player_activity_daily is a SummingMergeTree, so until parts merge a device-day
-- exists as several partial rows, and a chained view counting count() sees the same player
-- once per block. DAU would drift upward and nothing would report an error. The job
-- collapses to one row per device-day first, which is what makes these counts exact — and
-- exact is the point, because 00007's realtime tier is already the approximate one.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS metrics_daily
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    activity_date     Date     COMMENT 'class=derived',

    -- Exact distinct devices. Not an uniq state: this is the number a studio quotes in a
    -- board deck and compares against another vendor's.
    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',
    returning_players UInt64   COMMENT 'class=derived',

    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',

    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial',

    computed_at       DateTime COMMENT 'class=none'
)
-- Not a SummingMergeTree. active_players is a value, not an accrual, and summing it on
-- merge would silently double it — the same trap cohort_sizes avoids for the same reason.
-- The job rebuilds whole partitions, so there is exactly one row per key.
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, activity_date)
-- Aggregates outlive raw (§59): a customer on a 30-day raw tier still sees a year of DAU.
TTL activity_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
-- One dimension at a time (§29.13), for the same reason the cohort breakdown fact is:
-- growth is additive in the sum of the dimension sizes rather than multiplicative in their
-- product.
--
-- The dimensions are the *arrival* attributes player_activity_daily carries, and the names
-- say so. install_country is where a player installed, not where they are today, and
-- first_app_version is what they installed, not what they are running. A version-adoption
-- chart is a different question against a different source; answering it with these columns
-- would be wrong in a way nobody would notice.
CREATE TABLE IF NOT EXISTS metrics_breakdown_daily
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    activity_date     Date     COMMENT 'class=derived',

    dim_name          Enum8('install_platform'   = 1,
                            'install_country'    = 2,
                            'first_app_version'  = 3,
                            'acquisition_source' = 4) COMMENT 'class=none',
    dim_value         LowCardinality(String)          COMMENT 'class=derived',

    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',
    returning_players UInt64   COMMENT 'class=derived',

    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',

    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, dim_name, activity_date, dim_value)
TTL activity_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
-- MAU, exact, and the shape §38's billing meter reads.
--
-- A month's active players is not the sum of its days: a player active on the 3rd and the
-- 9th is one monthly active player and two daily ones. It needs its own distinct count over
-- the whole month, which is why this is a table rather than something summed at read time.
CREATE TABLE IF NOT EXISTS metrics_monthly
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    month             Date     COMMENT 'class=derived',

    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',

    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',

    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial',

    computed_at       DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(month)
ORDER BY (project_seq, environment_seq, month)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS metrics_daily_staging
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    activity_date     Date     COMMENT 'class=derived',
    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',
    returning_players UInt64   COMMENT 'class=derived',
    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',
    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial',
    computed_at       DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, activity_date)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS metrics_breakdown_daily_staging
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    activity_date     Date     COMMENT 'class=derived',
    dim_name          Enum8('install_platform'   = 1,
                            'install_country'    = 2,
                            'first_app_version'  = 3,
                            'acquisition_source' = 4) COMMENT 'class=none',
    dim_value         LowCardinality(String)          COMMENT 'class=derived',
    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',
    returning_players UInt64   COMMENT 'class=derived',
    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',
    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, dim_name, activity_date, dim_value)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS metrics_monthly_staging
(
    project_seq       UInt32   COMMENT 'class=none',
    environment_seq   UInt16   COMMENT 'class=none',
    month             Date     COMMENT 'class=derived',
    active_players    UInt64   COMMENT 'class=derived',
    new_players       UInt64   COMMENT 'class=derived',
    sessions          UInt64   COMMENT 'class=derived',
    playtime_ms       UInt64   COMMENT 'class=derived',
    events            UInt64   COMMENT 'class=derived',
    revenue_micros    Int64    COMMENT 'class=financial',
    purchases         UInt64   COMMENT 'class=financial',
    payers            UInt64   COMMENT 'class=financial',
    computed_at       DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(month)
ORDER BY (project_seq, environment_seq, month)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_monthly_staging
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_breakdown_daily_staging
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_daily_staging
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_monthly
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_breakdown_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_daily
-- +goose StatementEnd
