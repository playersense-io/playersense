-- The served cohort facts (project.md §29.8, §29.9, §29.11).
--
-- Every one of these is partitioned on the ACTIVITY month, never the cohort month.
-- One activity day contributes cells (cohort_date = C, day_offset = A - C) for every C
-- in [A-365, A] — thirteen cohort months, a one-to-three-offset slice of each. Rebuilding
-- a cohort-month partition from a trailing activity window therefore produces only those
-- slices, and REPLACE PARTITION swaps the partition wholesale, deleting every other
-- offset in it. Partitioning on cohort_date + day_offset gives every row exactly one
-- owning partition, fully reconstructible from one month of player_activity_daily.
--
-- Correction against §29.8 as written: the cumulative columns playtime_ms_cum and
-- revenue_micros_cum are gone. A cumulative value cannot live in a SummingMergeTree —
-- inside the sum list it gets summed, outside it an arbitrary value is chosen at merge.
-- LTV is computed at read time with a window function over day_offset instead.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cohort_metrics_daily
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
-- One dimension at a time (§29.13). Growth is additive in the sum of the dimension
-- sizes rather than multiplicative in their product, which is the difference between
-- ~2.4M rows per project and a number that rivals raw-event cardinality.
CREATE TABLE IF NOT EXISTS cohort_metrics_breakdown_daily
(
    project_seq      UInt32   COMMENT 'class=none',
    environment_seq  UInt16   COMMENT 'class=none',

    dim_name         Enum8('install_platform'   = 1,
                           'install_country'    = 2,
                           'first_app_version'  = 3) COMMENT 'class=none',
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
-- Denominators, partitioned on the COHORT month — their own cadence, which is what lets
-- the metrics tables partition on the activity month without the two fighting (§29.9).
--
-- Not a SummingMergeTree: cohort_size is a value, not an accrual, and summing it on
-- merge would silently double it. The whole table is rebuilt by partition.
CREATE TABLE IF NOT EXISTS cohort_sizes
(
    project_seq          UInt32   COMMENT 'class=none',
    environment_seq      UInt16   COMMENT 'class=none',
    cohort_date          Date     COMMENT 'class=derived',

    dim_name             Enum8('__total__'          = 0,
                               'install_platform'   = 1,
                               'install_country'    = 2,
                               'first_app_version'  = 3) COMMENT 'class=none',
    dim_value            LowCardinality(String)          COMMENT 'class=derived',

    cohort_size          UInt64   COMMENT 'class=derived',
    cohort_maturity_days UInt16   COMMENT 'class=none',
    computed_at          DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(cohort_date)
ORDER BY (project_seq, environment_seq, cohort_date, dim_name, dim_value)
-- +goose StatementEnd

-- +goose StatementBegin
-- Behavioural segments (§29.11). bucket_scheme_version travels on the row so that
-- changing a boundary cannot silently reinterpret history.
CREATE TABLE IF NOT EXISTS cohort_bucket_daily
(
    project_seq           UInt32 COMMENT 'class=none',
    environment_seq       UInt16 COMMENT 'class=none',

    bucket_kind           Enum8('playtime' = 1, 'spend' = 2, 'progression' = 3) COMMENT 'class=none',
    bucket_scheme_version UInt16 COMMENT 'class=none',
    bucket_code           UInt8  COMMENT 'class=derived',

    cohort_date           Date   COMMENT 'class=derived',
    day_offset            UInt16 COMMENT 'class=derived',

    players               UInt64 COMMENT 'class=derived',
    playtime_ms           UInt64 COMMENT 'class=derived',
    revenue_micros        Int64  COMMENT 'class=financial',
    purchases             UInt64 COMMENT 'class=financial'
)
ENGINE = ReplicatedSummingMergeTree((players, playtime_ms, revenue_micros, purchases))
PARTITION BY toYYYYMM(cohort_date + toIntervalDay(day_offset))
ORDER BY (project_seq, environment_seq, bucket_kind, bucket_scheme_version,
          cohort_date, bucket_code, day_offset)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_bucket_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_sizes
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_metrics_breakdown_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_metrics_daily
-- +goose StatementEnd
