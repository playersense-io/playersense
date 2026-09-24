-- Where a project's events come from (project.md §56, §76): each platform and SDK, when it was
-- first and last seen, how many events it sent, and the newest build and SDK version it ran.
--
-- The settings page lists these so a studio can see which of its builds are reporting, and
-- which one stopped. Aggregated in the insert block, a row per (platform, SDK) per environment,
-- so a project with three platforms is a handful of rows however many events it sends.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_apps
(
    project_seq     UInt32                                                COMMENT 'class=none',
    environment_seq UInt16                                                COMMENT 'class=none',
    platform        LowCardinality(String)                                COMMENT 'class=none',
    sdk_name        LowCardinality(String)                                COMMENT 'class=none',

    first_seen      SimpleAggregateFunction(min, DateTime64(3, 'UTC'))    COMMENT 'class=none',
    last_seen       SimpleAggregateFunction(max, DateTime64(3, 'UTC'))    COMMENT 'class=none',
    events          SimpleAggregateFunction(sum, UInt64)                  COMMENT 'class=none',
    -- The newest by the time the gateway accepted it, so a build in the field for a month and
    -- a build released today are told apart by when, not by how the version sorts.
    app_version     AggregateFunction(argMax, String, DateTime64(3, 'UTC')) COMMENT 'class=none',
    sdk_version     AggregateFunction(argMax, String, DateTime64(3, 'UTC')) COMMENT 'class=none'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, platform, sdk_name)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_project_apps TO project_apps AS
SELECT
    e.project_seq                                    AS project_seq,
    e.environment_seq                                AS environment_seq,
    e.platform                                       AS platform,
    e.sdk_name                                       AS sdk_name,
    min(e.ingest_time)                               AS first_seen,
    max(e.ingest_time)                               AS last_seen,
    toUInt64(count())                                AS events,
    argMaxState(toString(e.app_version), e.ingest_time) AS app_version,
    argMaxState(toString(e.sdk_version), e.ingest_time) AS sdk_version
FROM events_raw AS e
GROUP BY project_seq, environment_seq, platform, sdk_name
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_project_apps SYNC;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS project_apps SYNC;
-- +goose StatementEnd
