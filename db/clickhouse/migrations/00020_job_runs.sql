-- One row per pass of each of the worker's jobs (project.md §56, §67, §79): the nightly
-- derivation, and the janitor's erasures, exports, archive expiry, usage meter and credit expiry.
--
-- In ClickHouse rather than PostgreSQL because the derivation holds no PostgreSQL handle, and
-- that is a claim §64 relies on: a database outage does not stop tonight's facts being built. A
-- record of the derivation that needed the database would be the first thing to break it.
--
-- Small by construction: six jobs, a pass every fifteen minutes, about six hundred rows a day.
-- Kept for ninety days, which is longer than anybody reads a job's history for and short enough
-- that the table never needs a thought. Nothing in it names a player or a customer.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_runs
(
    job         LowCardinality(String)             COMMENT 'class=none',
    started_at  DateTime64(3, 'UTC')               COMMENT 'class=none',
    finished_at DateTime64(3, 'UTC')               COMMENT 'class=none',
    -- Which worker ran it, so two workers racing is visible rather than inferred.
    host        LowCardinality(String)             COMMENT 'class=none',
    ok          Bool                               COMMENT 'class=none',
    error       String                             COMMENT 'class=none',
    -- What the pass did, by the job's own names: completed, failed, rows, bytes...
    counts      Map(LowCardinality(String), Int64) COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(started_at)
ORDER BY (job, started_at)
TTL toDateTime(started_at) + INTERVAL 90 DAY
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS job_runs SYNC;
-- +goose StatementEnd
