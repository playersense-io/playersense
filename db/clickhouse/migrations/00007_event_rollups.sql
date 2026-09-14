-- Realtime and hourly event summaries (project.md §26, §27, §28).
--
-- §26's rollup policy wants three resolutions: minute for the last day, hour for the last
-- ninety, day beyond that. These are the first two. The daily tier is 00008, and it is a
-- job rather than a view for a reason spelled out there.
--
-- Both are incremental materialized views off events_raw, which is what §28 asks for: the
-- summary is calculated on newly inserted blocks and nothing ever re-aggregates history.
-- That works here because every measure is either additive or an aggregate state, so the
-- merge is associative and a block arriving late is simply another block.
--
-- Unique players are an `uniq` state and therefore **approximate**, which §27 sanctions
-- ("approximate unique players where appropriate"). Appropriate means here and not in the
-- daily tier: a realtime chart tolerates a fraction of a percent, and a number a customer
-- is billed on or compares against their own does not. The exact count lives in 00008.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS events_1m
(
    project_seq     UInt32                 COMMENT 'class=none',
    environment_seq UInt16                 COMMENT 'class=none',

    minute          DateTime               COMMENT 'class=behavioural',
    event_name      LowCardinality(String) COMMENT 'class=behavioural',

    events          SimpleAggregateFunction(sum, UInt64) COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64) COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)  COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64) COMMENT 'class=financial',

    -- Approximate, and never read by anything that has to be exact.
    players         AggregateFunction(uniq, UInt64)      COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (project_seq, environment_seq, minute, event_name)
-- Seven days rather than the raw retention tier. This exists to answer "what is happening
-- right now" (§26: the last 24 hours), and a minute-grain table kept for a year is a large
-- table nobody queries. Partitioned by day so the TTL is a directory unlink rather than a
-- rewrite, for the same reason events_raw is (§23.1).
TTL minute + INTERVAL 7 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_events_1m TO events_1m AS
SELECT
    project_seq,
    environment_seq,
    toStartOfMinute(event_time)      AS minute,
    event_name,
    count()                          AS events,
    sum(playtime_ms)                 AS playtime_ms,
    sum(revenue_micros)              AS revenue_micros,
    countIf(event_type = 'purchase') AS purchases,
    uniqState(device_id)             AS players
FROM events_raw
GROUP BY project_seq, environment_seq, minute, event_name
-- +goose StatementEnd

-- +goose StatementBegin
-- The hourly tier, which §26 uses for anything up to ninety days.
--
-- Keyed on event_name and nothing else. §27 warns against a rollup carrying every
-- dimension because the cartesian cardinality approaches raw, and §29.13 already learned
-- that lesson the expensive way on the cohort side. Dimensional breakdowns live on the
-- daily tier (00008), one dimension at a time, where the grain is coarse enough to afford
-- them.
CREATE TABLE IF NOT EXISTS events_1h
(
    project_seq     UInt32                 COMMENT 'class=none',
    environment_seq UInt16                 COMMENT 'class=none',

    hour            DateTime               COMMENT 'class=behavioural',
    event_name      LowCardinality(String) COMMENT 'class=behavioural',

    events          SimpleAggregateFunction(sum, UInt64) COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64) COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)  COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64) COMMENT 'class=financial',

    players         AggregateFunction(uniq, UInt64)      COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (project_seq, environment_seq, hour, event_name)
TTL hour + INTERVAL 400 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_events_1h TO events_1h AS
SELECT
    project_seq,
    environment_seq,
    toStartOfHour(event_time)        AS hour,
    event_name,
    count()                          AS events,
    sum(playtime_ms)                 AS playtime_ms,
    sum(revenue_micros)              AS revenue_micros,
    countIf(event_type = 'purchase') AS purchases,
    uniqState(device_id)             AS players
FROM events_raw
GROUP BY project_seq, environment_seq, hour, event_name
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_events_1h
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS events_1h
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_events_1m
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS events_1m
-- +goose StatementEnd
