-- The event explorer's facts (project.md §3.5, §27).
--
-- Three questions the Events page asks and nothing answered without reading events_raw:
--
--   * which events does this game send, when did each last arrive, and of what kind;
--   * how often, by how many players, and split by where they are and what they run;
--   * which properties each event carries, how often each is filled, and with what values.
--
-- All three are incremental views that aggregate inside the insert block, so each block of a
-- hundred thousand events leaves a few hundred rows behind and nothing ever re-reads history.

-- +goose Up
-- +goose StatementBegin
-- One row per event name per project: the catalogue itself.
--
-- last_seen is when the gateway last accepted one, not the device's clock. "Is this event still
-- arriving" is a question about the pipeline, and a clamped or spooled event_time would answer it
-- with when the player did something days ago.
CREATE TABLE IF NOT EXISTS event_names
(
    project_seq     UInt32                                             COMMENT 'class=none',
    environment_seq UInt16                                             COMMENT 'class=none',
    event_name      LowCardinality(String)                             COMMENT 'class=behavioural',

    -- The classification ingest gave it (normalize.go eventType). A name can arrive both with
    -- and without revenue, so the latest wins and purchases below says how many carried money.
    event_type      SimpleAggregateFunction(anyLast, String)           COMMENT 'class=behavioural',
    first_seen      SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=derived',
    last_seen       SimpleAggregateFunction(max, DateTime64(3, 'UTC')) COMMENT 'class=derived',
    events          SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=derived',
    purchases       SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=financial'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, event_name)
-- +goose StatementEnd

-- +goose StatementBegin
-- Qualified through `e`, because the event_type alias would otherwise shadow the column inside
-- countIf and ClickHouse would read it as an aggregate inside an aggregate.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_event_names TO event_names AS
SELECT
    e.project_seq                       AS project_seq,
    e.environment_seq                   AS environment_seq,
    e.event_name                        AS event_name,
    anyLast(toString(e.event_type))     AS event_type,
    min(e.ingest_time)                  AS first_seen,
    max(e.ingest_time)                  AS last_seen,
    count()                             AS events,
    countIf(e.event_type = 'purchase')  AS purchases
FROM events_raw AS e
GROUP BY e.project_seq, e.environment_seq, e.event_name
-- +goose StatementEnd

-- +goose StatementBegin
-- Each event by day, whole and split by one attribute at a time.
--
-- The attributes are the ones the event arrived with (platform, country_code, app_version), not
-- the sticky install attributes metrics_breakdown_daily carries: "which build is sending
-- level_fail" is a question about the build that sent it, and answering with the build the player
-- installed would be wrong in exactly the case that matters, right after a release.
--
-- One dimension per row rather than their product (§27, §29.13): four arms, so a day costs the
-- number of events times the sum of the dimension sizes. The '__total__' arm is the event
-- unsplit, so the catalogue reads one arm and never sums the others back together, which would
-- count a player once per platform they played on.
CREATE TABLE IF NOT EXISTS events_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    dim_name        Enum8('__total__'   = 0,
                          'platform'    = 1,
                          'country'     = 2,
                          'app_version' = 3)                COMMENT 'class=none',
    event_name      LowCardinality(String)                  COMMENT 'class=behavioural',
    day             Date                                    COMMENT 'class=behavioural',
    dim_value       LowCardinality(String)                  COMMENT 'class=behavioural',

    events          SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',

    -- Approximate above a few thousand players, exact below: uniqCombined keeps small sets as a
    -- list and only switches to a sketch once the list would outgrow it. A ceiling of ~12 KiB.
    players         AggregateFunction(uniqCombined(14), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (project_seq, environment_seq, dim_name, event_name, day, dim_value)
-- Aggregates outlive raw (§59), as metrics_daily does.
TTL day + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_events_daily TO events_daily AS
SELECT
    project_seq,
    environment_seq,
    dim.1                               AS dim_name,
    event_name,
    event_date                          AS day,
    dim.2                               AS dim_value,
    count()                             AS events,
    sum(playtime_ms)                    AS playtime_ms,
    sum(revenue_micros)                 AS revenue_micros,
    countIf(event_type = 'purchase')    AS purchases,
    uniqCombinedState(14)(device_id)    AS players
FROM events_raw
ARRAY JOIN [('__total__', ''),
            ('platform', toString(platform)),
            ('country', toString(country_code)),
            ('app_version', toString(app_version))] AS dim
GROUP BY project_seq, environment_seq, dim_name, event_name, day, dim_value
-- +goose StatementEnd

-- +goose StatementBegin
-- What each event's properties hold, by day.
--
-- Every value is compared as text, so a level of 12 and a level of "12" are one value. The four
-- maps stay distinguishable by value_type, which is what the explorer shows as the type.
--
-- Bounded twice over, because a property can be anything a studio chose to send, including a
-- transaction id that is different on every row: distinct values is a uniqCombined(12) state
-- (about 3 KiB at most) and the top values a topK(10) state, so a high-cardinality property costs
-- the same as a low one.
--
-- Context properties (`ctx.` keys, §8.1 of the SDK protocol) are left out. They describe the
-- player rather than the occurrence, they are copied onto every event of a batch, and cataloguing
-- them per event would multiply this view's work by every context key for no new fact.
CREATE TABLE IF NOT EXISTS event_properties_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    event_name      LowCardinality(String)                  COMMENT 'class=behavioural',
    property        LowCardinality(String)                  COMMENT 'class=behavioural',
    value_type      Enum8('string' = 1, 'int' = 2, 'float' = 3, 'bool' = 4) COMMENT 'class=none',
    day             Date                                    COMMENT 'class=behavioural',

    occurrences     SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    distinct_values AggregateFunction(uniqCombined(12), String) COMMENT 'class=derived',
    top_values      AggregateFunction(topK(10), String)     COMMENT 'class=behavioural'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (project_seq, environment_seq, event_name, property, value_type, day)
TTL day + INTERVAL 400 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_event_properties_daily TO event_properties_daily AS
SELECT
    project_seq,
    environment_seq,
    event_name,
    prop.1                              AS property,
    prop.2                              AS value_type,
    event_date                          AS day,
    count()                             AS occurrences,
    uniqCombinedState(12)(prop.3)       AS distinct_values,
    topKState(10)(prop.3)               AS top_values
FROM events_raw
ARRAY JOIN arrayFilter(p -> NOT startsWith(p.1, 'ctx.'), arrayConcat(
    arrayMap((k, v) -> (k, 'string', v),           mapKeys(props_string), mapValues(props_string)),
    arrayMap((k, v) -> (k, 'int',    toString(v)), mapKeys(props_int),    mapValues(props_int)),
    arrayMap((k, v) -> (k, 'float',  toString(v)), mapKeys(props_float),  mapValues(props_float)),
    arrayMap((k, v) -> (k, 'bool',   if(v = 0, 'false', 'true')), mapKeys(props_bool), mapValues(props_bool))
)) AS prop
GROUP BY project_seq, environment_seq, event_name, property, value_type, day
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_event_properties_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS event_properties_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_events_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS events_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_event_names
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS event_names
-- +goose StatementEnd
