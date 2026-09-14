-- Staging for the behavioural-bucket rebuild (project.md §29.11, §79.1).
--
-- 00005 created staging mirrors for the three facts that existed then. cohort_bucket_daily
-- was declared in 00004 but never derived, so it never got one. This is that mirror.
--
-- REPLACE PARTITION requires identical structure and the same engine family on both sides,
-- so this matches 00004's cohort_bucket_daily exactly.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cohort_bucket_daily_staging
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
DROP TABLE IF EXISTS cohort_bucket_daily_staging
-- +goose StatementEnd
