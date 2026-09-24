-- Experiments: which arm each player was put in, and what the arms did afterwards (project.md
-- §44, §91).
--
-- An arm is a flag's value. A player joins one the first time they are exposed to the flag
-- (feature_exposure, 00017), and an experiment compares what each arm's players did in the days
-- after joining: whether they came back on day 1 and day 7, whether they paid, how much, and how
-- much they played.
--
-- That is a question across two facts, the exposure and the activity after it, and it is exactly
-- the per-player join §29.1 forbids at query time and that takes a node's memory at scale. It is
-- answered without one:
--
--   * flag_assignments keeps each device's first exposure to each flag, built as events arrive;
--   * the nightly derivation reads it and player_activity_daily together as one UNION ALL grouped
--     by device, so each device is one aggregation row holding both its assignments and its days,
--     and nothing is ever built on one side and probed from the other;
--   * experiment_daily keeps, per arm and exposure day, counts and sums and sums of squares, which
--     add across days, so a mean and a standard deviation over any range are arithmetic on a few
--     rows and the query is a small read of one fact.

-- +goose Up
-- +goose StatementBegin
-- Each device's first exposure to each flag.
--
-- The arm is taken from the first exposure and never changes: a player who was moved between arms
-- by a later publish is counted in the arm they met first, which is what an intention-to-treat
-- comparison needs.
CREATE TABLE IF NOT EXISTS flag_assignments
(
    project_seq     UInt32                                             COMMENT 'class=none',
    environment_seq UInt16                                             COMMENT 'class=none',
    flag            LowCardinality(String)                             COMMENT 'class=behavioural',
    device_id       UInt64                                             COMMENT 'class=identifier',

    first_exposed   SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=behavioural',
    -- (value, published, config_version) of the first exposure, as one state so all three come
    -- from the same event.
    arm             AggregateFunction(argMin, Tuple(String, UInt8, UInt32), DateTime64(3, 'UTC')) COMMENT 'class=behavioural'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, flag, device_id)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_flag_assignments TO flag_assignments AS
SELECT
    e.project_seq                                           AS project_seq,
    e.environment_seq                                       AS environment_seq,
    e.props_string['flag']                                  AS flag,
    e.device_id                                             AS device_id,
    min(e.event_time)                                       AS first_exposed,
    argMinState(tuple(
        multiIf(mapContains(e.props_string, 'value'), e.props_string['value'],
                mapContains(e.props_int, 'value'),    toString(e.props_int['value']),
                mapContains(e.props_float, 'value'),  toString(e.props_float['value']),
                mapContains(e.props_bool, 'value'),   if(e.props_bool['value'] = 0, 'false', 'true'),
                ''),
        toUInt8(if(mapContains(e.props_bool, 'published'), e.props_bool['published'], 1)),
        toUInt32(greatest(e.props_int['config_version'], 0))
    ), e.event_time)                                        AS arm
FROM events_raw AS e
WHERE e.event_type = 'exposure' AND e.props_string['flag'] != ''
GROUP BY project_seq, environment_seq, flag, device_id
-- +goose StatementEnd

-- +goose StatementBegin
-- Each arm's players by the day they were exposed, and what they did in the seven days from it.
--
-- players, retained_d1, retained_d7 and payers are counts; each measure is a sum over the arm's
-- players of each player's seven-day total, beside the sum of its square, which is what a standard
-- deviation needs and what adds across exposure days. Squares are Float64: a square of a player's
-- revenue in micros overflows 64 bits long before anybody is suspicious of it.
--
-- mature_days is how many of the seven days had happened when the row was built, so a comparison
-- can leave out arms still filling rather than read them as worse.
CREATE TABLE IF NOT EXISTS experiment_daily
(
    project_seq      UInt32                 COMMENT 'class=none',
    environment_seq  UInt16                 COMMENT 'class=none',
    flag             LowCardinality(String) COMMENT 'class=derived',
    exposure_date    Date                   COMMENT 'class=derived',
    variant          String                 COMMENT 'class=derived',
    published        UInt8                  COMMENT 'class=derived',

    players          UInt64                 COMMENT 'class=derived',
    retained_d1      UInt64                 COMMENT 'class=derived',
    retained_d7      UInt64                 COMMENT 'class=derived',
    payers           UInt64                 COMMENT 'class=financial',

    revenue_micros   Int64                  COMMENT 'class=financial',
    revenue_sq       Float64                COMMENT 'class=financial',
    sessions         UInt64                 COMMENT 'class=derived',
    sessions_sq      Float64                COMMENT 'class=derived',
    playtime_ms      UInt64                 COMMENT 'class=derived',
    playtime_sq      Float64                COMMENT 'class=derived',

    mature_days      UInt8                  COMMENT 'class=none',
    computed_at      DateTime               COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(exposure_date)
ORDER BY (project_seq, environment_seq, flag, exposure_date, variant, published)
TTL exposure_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS experiment_daily_staging
(
    project_seq      UInt32                 COMMENT 'class=none',
    environment_seq  UInt16                 COMMENT 'class=none',
    flag             LowCardinality(String) COMMENT 'class=derived',
    exposure_date    Date                   COMMENT 'class=derived',
    variant          String                 COMMENT 'class=derived',
    published        UInt8                  COMMENT 'class=derived',
    players          UInt64                 COMMENT 'class=derived',
    retained_d1      UInt64                 COMMENT 'class=derived',
    retained_d7      UInt64                 COMMENT 'class=derived',
    payers           UInt64                 COMMENT 'class=financial',
    revenue_micros   Int64                  COMMENT 'class=financial',
    revenue_sq       Float64                COMMENT 'class=financial',
    sessions         UInt64                 COMMENT 'class=derived',
    sessions_sq      Float64                COMMENT 'class=derived',
    playtime_ms      UInt64                 COMMENT 'class=derived',
    playtime_sq      Float64                COMMENT 'class=derived',
    mature_days      UInt8                  COMMENT 'class=none',
    computed_at      DateTime               COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(exposure_date)
ORDER BY (project_seq, environment_seq, flag, exposure_date, variant, published)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS experiment_daily_staging SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS experiment_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_flag_assignments
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS flag_assignments SYNC
-- +goose StatementEnd
