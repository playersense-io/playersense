-- Which players saw which flag value, by day (project.md §13.1 `feature_exposures`, §44).
--
-- The SDK records a `feature_exposure` event the first time a session reads a flag under a
-- config version (sdk/core/src/exposure.rs), with four properties:
--
--   flag            which flag was read
--   value           what the player got; absent when nothing was published under the key
--   config_version  the published version the answer came from, 0 before the first fetch
--   published       false when the game fell back to its own default
--
-- ingest classifies the event as event_type 'exposure' (normalize.go), which is what this view
-- filters on, so it reads almost nothing of each block.
--
-- The value is compared as text whatever type it arrived as, since a flag's value can be a
-- boolean, a number or a string and each lands in a different property map.

-- +goose Up
-- +goose StatementBegin
-- Exposures by day, flag, config version, value and build.
--
-- A cube, and bounded for the same reason revenue_daily is: a row exists only where an exposure
-- did, and each dimension is small (a flag has a handful of values, a project a handful of live
-- versions and builds). The build is on the key because "which build is falling back to the
-- default" is the question a rollout gets wrong most often.
CREATE TABLE IF NOT EXISTS feature_exposures_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    flag            LowCardinality(String)                  COMMENT 'class=behavioural',
    day             Date                                    COMMENT 'class=behavioural',
    config_version  UInt32                                  COMMENT 'class=behavioural',
    value           String                                  COMMENT 'class=behavioural',
    published       UInt8                                   COMMENT 'class=behavioural',
    app_version     LowCardinality(String)                  COMMENT 'class=behavioural',

    exposures       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    players         AggregateFunction(uniqCombined(12), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (project_seq, environment_seq, flag, day, config_version, value, published, app_version)
TTL day + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_feature_exposures_daily TO feature_exposures_daily AS
SELECT
    e.project_seq                                               AS project_seq,
    e.environment_seq                                           AS environment_seq,
    e.props_string['flag']                                      AS flag,
    e.event_date                                                AS day,
    toUInt32(greatest(e.props_int['config_version'], 0))        AS config_version,
    multiIf(mapContains(e.props_string, 'value'), e.props_string['value'],
            mapContains(e.props_int, 'value'),    toString(e.props_int['value']),
            mapContains(e.props_float, 'value'),  toString(e.props_float['value']),
            mapContains(e.props_bool, 'value'),   if(e.props_bool['value'] = 0, 'false', 'true'),
            '')                                                 AS value,
    -- The SDK always sends it; an exposure without it came from somewhere else and is read as
    -- published, the case that puts a player in an arm.
    if(mapContains(e.props_bool, 'published'), e.props_bool['published'], 1) AS published,
    e.app_version                                               AS app_version,
    count()                                                     AS exposures,
    uniqCombinedState(12)(e.device_id)                          AS players
FROM events_raw AS e
WHERE e.event_type = 'exposure' AND e.props_string['flag'] != ''
GROUP BY project_seq, environment_seq, flag, day, config_version, value, published, app_version
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_feature_exposures_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS feature_exposures_daily SYNC
-- +goose StatementEnd
