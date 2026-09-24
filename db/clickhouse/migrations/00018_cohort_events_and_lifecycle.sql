-- What the Cohorts and Players pages still could not ask (project.md §29, §55).
--
--   1. Sessions per install split by platform, country or build. cohort_metrics_daily carries
--      sessions and its breakdown twin did not, so "sessions per install on iOS" had no answer.
--   2. How often each cohort did one particular event, and what share of it had done it at all
--      by day N. Nothing cohort-keyed knew about event names.
--   3. Whether a returning player came back from a short break or a long one. metrics_daily
--      splits actives into new and returning; the lifecycle chart needs the returners split again.

-- +goose Up
-- +goose StatementBegin
-- Sessions on the breakdown fact, placed where the unbroken fact has them.
--
-- Outside the SummingMergeTree's sum list, and that is safe here in a way it would not be in
-- general: the derivation rebuilds whole partitions with one row per key, so no merge ever meets
-- two rows to combine. Rows built before this migration read 0 until their month is derived
-- again; a pass with WORKER_ACTIVITY_MONTHS=13 rebuilds everything player_activity_daily still
-- holds.
ALTER TABLE cohort_metrics_breakdown_daily
    ADD COLUMN IF NOT EXISTS sessions UInt64 COMMENT 'class=derived' AFTER returned_players
-- +goose StatementEnd

-- +goose StatementBegin
-- The staging twin must match column for column, or REPLACE PARTITION refuses the swap.
ALTER TABLE cohort_metrics_breakdown_daily_staging
    ADD COLUMN IF NOT EXISTS sessions UInt64 COMMENT 'class=derived' AFTER returned_players
-- +goose StatementEnd

-- +goose StatementBegin
-- Each event per cohort per day since install.
--
-- An incremental view, where the other cohort facts are derived nightly, because it can be: the
-- cohort key is already on every row of events_raw (§29.5), so the offset is a subtraction and
-- no join is needed. events is additive; players is a mergeable state, so "had done it by day N"
-- is the merge of offsets 0..N, a distinct count over the whole stretch rather than a sum that
-- counts a player once per day.
--
-- uniqCombined(12): exact while a cell is small, which is where the suppression threshold looks,
-- and about 1.6% error when it is large, in a state of at most ~3 KiB.
--
-- Rows the writer could not stamp (a null cohort_date, §65) are left out rather than guessed.
-- The nightly backfill repairs player_activity_daily, which this view cannot see, so an event
-- count here can run a fraction low for the devices affected; a player count never runs high.
CREATE TABLE IF NOT EXISTS cohort_events_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    event_name      LowCardinality(String)                  COMMENT 'class=behavioural',
    cohort_date     Date                                    COMMENT 'class=derived',
    day_offset      UInt16                                  COMMENT 'class=derived',

    events          SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=derived',
    players         AggregateFunction(uniqCombined(12), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
-- The activity month, as every cohort fact is partitioned (§29.8).
PARTITION BY toYYYYMM(cohort_date + toIntervalDay(day_offset))
ORDER BY (project_seq, environment_seq, event_name, cohort_date, day_offset)
TTL cohort_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_cohort_events_daily TO cohort_events_daily AS
SELECT
    e.project_seq                                                   AS project_seq,
    e.environment_seq                                               AS environment_seq,
    e.event_name                                                    AS event_name,
    assumeNotNull(e.cohort_date)                                    AS cohort_date,
    toUInt16(dateDiff('day', assumeNotNull(e.cohort_date), e.event_date)) AS day_offset,
    count()                                                         AS events,
    uniqCombinedState(12)(e.device_id)                              AS players
FROM events_raw AS e
WHERE e.cohort_date IS NOT NULL
  AND e.event_date >= e.cohort_date
  -- A cohort query asks for offsets up to 365 (CohortQueryIn.Validate) over at most 400 cohort
  -- days, so nothing past 400 can ever be read; storing it would only cost rows.
  AND dateDiff('day', e.cohort_date, e.event_date) <= 400
GROUP BY project_seq, environment_seq, event_name, cohort_date, day_offset
-- +goose StatementEnd

-- +goose StatementBegin
-- Each day's active players split by where they came from: new, continuing (also active in the
-- previous seven days) and reactivated (back after more than seven days away, or after more than
-- thirty, when their last visit is outside the window the job reads).
--
-- The three sum to active_players by construction, the same promise metrics_daily makes about
-- new and returning. A device whose cohort is unknown is never counted new, as there.
--
-- Its own table rather than columns on metrics_daily because it is derived from a different read:
-- metrics_daily is a projection of one month of player_activity_daily, and this needs thirty days
-- before the month as well, to know when each returner was last seen.
CREATE TABLE IF NOT EXISTS lifecycle_daily
(
    project_seq         UInt32   COMMENT 'class=none',
    environment_seq     UInt16   COMMENT 'class=none',
    activity_date       Date     COMMENT 'class=derived',

    active_players      UInt64   COMMENT 'class=derived',
    new_players         UInt64   COMMENT 'class=derived',
    continuing_players  UInt64   COMMENT 'class=derived',
    reactivated_players UInt64   COMMENT 'class=derived',

    computed_at         DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, activity_date)
TTL activity_date + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS lifecycle_daily_staging
(
    project_seq         UInt32   COMMENT 'class=none',
    environment_seq     UInt16   COMMENT 'class=none',
    activity_date       Date     COMMENT 'class=derived',
    active_players      UInt64   COMMENT 'class=derived',
    new_players         UInt64   COMMENT 'class=derived',
    continuing_players  UInt64   COMMENT 'class=derived',
    reactivated_players UInt64   COMMENT 'class=derived',
    computed_at         DateTime COMMENT 'class=none'
)
ENGINE = ReplicatedMergeTree
PARTITION BY toYYYYMM(activity_date)
ORDER BY (project_seq, environment_seq, activity_date)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS lifecycle_daily_staging SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS lifecycle_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_cohort_events_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cohort_events_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE cohort_metrics_breakdown_daily_staging DROP COLUMN IF EXISTS sessions
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE cohort_metrics_breakdown_daily DROP COLUMN IF EXISTS sessions
-- +goose StatementEnd
