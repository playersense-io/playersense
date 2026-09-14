-- Staging tables for the whole-month rebuild (project.md §79.1).
--
-- The derivation never writes into a served fact directly. It rebuilds a whole activity
-- month into staging and then swaps the partition:
--
--   ALTER TABLE cohort_metrics_daily REPLACE PARTITION 'YYYYMM' FROM cohort_metrics_daily_staging
--
-- REPLACE PARTITION requires identical structure and the same engine family on both
-- sides, so these mirror their targets exactly.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cohort_metrics_daily_staging
(
    project_seq      UInt32   COMMENT 'class=none',
    environment_seq  UInt16   COMMENT 'class=none',
    cohort_date      Date     COMMENT 'class=derived',
    day_offset       UInt16   COMMENT 'class=derived',
    returned_players UInt64   COMMENT 'class=derived',
    sessions         UInt64   COMMENT 'class=derived',
    playtime_ms      UInt64   COMMENT 'class=derived',
    revenue_micros   Int64    COMMENT 'class=financial',
    purchases        UInt64   COMMENT 'class=financial',
    payers           UInt64   COMMENT 'class=financial',
    computed_at      DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedSummingMergeTree(
    (returned_players, sessions, playtime_ms, revenue_micros, purchases, payers))
PARTITION BY toYYYYMM(cohort_date + toIntervalDay(day_offset))
ORDER BY (project_seq, environment_seq, cohort_date, day_offset)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cohort_metrics_breakdown_daily_staging
(
    project_seq      UInt32   COMMENT 'class=none',
    environment_seq  UInt16   COMMENT 'class=none',
    dim_name         Enum8('install_platform'   = 1,
                           'install_country'    = 2,
                           'first_app_version'  = 3,
                           'acquisition_source' = 4) COMMENT 'class=none',
    dim_value        LowCardinality(String)          COMMENT 'class=derived',
    cohort_date      Date     COMMENT 'class=derived',
    day_offset       UInt16   COMMENT 'class=derived',
    returned_players UInt64   COMMENT 'class=derived',
    playtime_ms      UInt64   COMMENT 'class=derived',
    revenue_micros   Int64    COMMENT 'class=financial',
    purchases        UInt64   COMMENT 'class=financial',
    payers           UInt64   COMMENT 'class=financial'
)
ENGINE = ReplicatedSummingMergeTree(
    (returned_players, playtime_ms, revenue_micros, purchases, payers))
PARTITION BY toYYYYMM(cohort_date + toIntervalDay(day_offset))
ORDER BY (project_seq, environment_seq, dim_name, cohort_date, dim_value, day_offset)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cohort_sizes_staging
(
    project_seq          UInt32   COMMENT 'class=none',
    environment_seq      UInt16   COMMENT 'class=none',
    cohort_date          Date     COMMENT 'class=derived',
    dim_name             Enum8('__total__'          = 0,
                               'install_platform'   = 1,
                               'install_country'    = 2,
                               'first_app_version'  = 3,
                               'acquisition_source' = 4) COMMENT 'class=none',
    dim_value            LowCardinality(String)          COMMENT 'class=derived',
    cohort_size          UInt64   COMMENT 'class=derived',
    cohort_maturity_days UInt16   COMMENT 'class=none',
    computed_at          DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(cohort_date)
ORDER BY (project_seq, environment_seq, cohort_date, dim_name, dim_value)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_sizes_staging
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_metrics_breakdown_daily_staging
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_metrics_daily_staging
-- +goose StatementEnd
