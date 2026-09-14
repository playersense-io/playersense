-- One row per device, for the life of the device (project.md §29.6).
--
-- Serves cohort assignment, rolling retention, cohort sizes, lifetime LTV and every
-- behavioural bucket. Nothing reads this table directly — see player_lifetime_v below.
--
-- Two corrections against the conceptual DDL in §29.6, both verified against the server:
--
--   1. Aggregate state types carry plain `String`, not `LowCardinality(String)`.
--      ClickHouse strips LowCardinality when it builds a state, so
--      argMinState over LowCardinality(String) columns yields
--      AggregateFunction(argMin, Tuple(String, String, String), ...). Declaring the
--      column with LowCardinality parses but does not match what the view produces.
--
--   2. Tenant columns are named project_seq / environment_seq, matching events_raw.
--      §29 called them project_id while typing them UInt32, which collides with the
--      UUID column of the same name on events_raw.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS player_lifetime
(
    project_seq     UInt32                                      COMMENT 'class=none',
    environment_seq UInt16                                      COMMENT 'class=none',
    device_id        UInt64                                      COMMENT 'class=identifier',

    device_key      SimpleAggregateFunction(any, String)        COMMENT 'class=identifier',

    first_seen      SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=derived',
    last_seen       SimpleAggregateFunction(max, DateTime64(3, 'UTC')) COMMENT 'class=derived',

    -- One state over a tuple, so the attributes always come from the same event.
    first_attrs     AggregateFunction(argMin,
                        Tuple(String, String, String),
                        Tuple(DateTime64(3, 'UTC'), UUID))      COMMENT 'class=derived',

    -- Conditional: attribution does not arrive on the first event (§29.3).
    first_source    AggregateFunction(argMinIf,
                        String,
                        Tuple(DateTime64(3, 'UTC'), UUID),
                        UInt8)                                  COMMENT 'class=derived',

    playtime_ms     SimpleAggregateFunction(sum, UInt64)        COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)         COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)        COMMENT 'class=financial',
    active_days     AggregateFunction(uniq, Date)               COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, device_id)
-- +goose StatementEnd

-- +goose StatementBegin
-- The GROUP BY inside the view is not optional. Without it this writes one row per
-- event — a billion a day at the §4 ceiling — and the resulting merge pressure is the
-- most likely way to lose the Stage 1 node (§29.6).
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_player_lifetime TO player_lifetime AS
SELECT
    project_seq,
    environment_seq,
    device_id,
    any(device_key)                                                 AS device_key,
    min(ingest_time)                                                AS first_seen,
    max(ingest_time)                                                AS last_seen,
    argMinState(tuple(platform, country_code, app_version),
                (ingest_time, event_id))                            AS first_attrs,
    argMinIfState(acquisition_source, (ingest_time, event_id),
                  acquisition_source != '')                         AS first_source,
    sum(playtime_ms)                                                AS playtime_ms,
    sum(revenue_micros)                                             AS revenue_micros,
    countIf(event_type = 'purchase')                                AS purchases,
    uniqState(event_date)                                           AS active_days
FROM events_raw
GROUP BY project_seq, environment_seq, device_id
-- +goose StatementEnd

-- +goose StatementBegin
-- The only read path. Selecting from player_lifetime directly can observe unmerged
-- parts, which means duplicate keys and a first_seen that is not the earliest (§29.6).
--
-- Every source column is qualified through the `pl` alias. Without it an output alias
-- shadows the column of the same name, so the second reference inside
-- toDate(min(first_seen)) resolves to the alias and ClickHouse rejects the view with
-- "aggregate function is found inside another aggregate function".
CREATE VIEW IF NOT EXISTS player_lifetime_v AS
SELECT
    pl.project_seq                      AS project_seq,
    pl.environment_seq                  AS environment_seq,
    pl.device_id                         AS device_id,
    any(pl.device_key)                  AS device_key,
    min(pl.first_seen)                  AS first_seen,
    max(pl.last_seen)                   AS last_seen,
    toDate(min(pl.first_seen))          AS cohort_date,
    argMinMerge(pl.first_attrs)         AS first_attrs,
    argMinIfMerge(pl.first_source)      AS acquisition_source,
    sum(pl.playtime_ms)                 AS playtime_ms,
    sum(pl.revenue_micros)              AS revenue_micros,
    sum(pl.purchases)                   AS purchases,
    uniqMerge(pl.active_days)           AS active_days
FROM player_lifetime AS pl
GROUP BY pl.project_seq, pl.environment_seq, pl.device_id
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS player_lifetime_v
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_player_lifetime
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS player_lifetime
-- +goose StatementEnd
