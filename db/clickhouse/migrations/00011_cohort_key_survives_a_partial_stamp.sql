-- One unstamped event must not poison a whole device-day (project.md §29.5, §65).
--
-- §65 promises that losing Valkey costs speed rather than data: the writer stamps a null
-- `cohort_date` and "the nightly derivation resolves them from player_lifetime". The
-- materialized view then collapsed a device-day with:
--
--     min(assumeNotNull(cohort_date))
--
-- `assumeNotNull` turns a NULL into the zero date, and 1970 is smaller than every real
-- cohort date, so `min` prefers it. **One** unstamped event in a device-day therefore
-- discarded the correct value that every other event in it carried.
--
-- The blast radius is bigger than a Valkey outage, because the writer's lookup is per batch:
-- a device sending several batches during a brief blip has some stamped and some not, and
-- the whole day goes to 1970. From there the cohort derivation drops the row entirely
-- (`WHERE cohort_date != toDate(0)`) while `cohort_sizes` — derived from `player_lifetime`,
-- which never needed Valkey — still counts the player. The numerator loses them and the
-- denominator keeps them, so retention reads **low**, and nothing reports an error.
--
-- Swapping the two functions is the whole fix:
--
--     assumeNotNull(min(cohort_date))
--
-- ClickHouse's aggregate functions skip NULLs, so `min` now returns the earliest value that
-- was actually stamped and only yields NULL when *every* event in the device-day was
-- unstamped. `assumeNotNull` then maps that one genuinely-unknown case to the 1970 sentinel
-- the derivation already recognises, so nothing downstream changes shape.
--
-- Verified in place before the change:
--
--     min(assumeNotNull(c)) -> 1970-01-01   -- one stamped, one unstamped
--     assumeNotNull(min(c)) -> 2026-09-01
--
-- Rows already written with a poisoned key are not repaired by this; that is the
-- scheduler's backfill, which is the other half of §65's promise.

-- +goose Up
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_player_activity_daily
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW mv_player_activity_daily TO player_activity_daily AS
SELECT
    project_seq,
    environment_seq,
    event_date                              AS activity_date,
    device_id,
    -- Null-aware: the minimum of the values that were stamped, and NULL only when none
    -- were. assumeNotNull maps that to the 1970 sentinel the derivation recognises.
    assumeNotNull(min(cohort_date))         AS cohort_date,
    any(install_platform)                   AS install_platform,
    any(install_country)                    AS install_country,
    any(first_app_version)                  AS first_app_version,
    any(acquisition_source)                 AS acquisition_source,
    countIf(event_name = 'session_start')   AS sessions,
    sum(playtime_ms)                        AS playtime_ms,
    count()                                 AS events,
    sum(revenue_micros)                     AS revenue_micros,
    countIf(event_type = 'purchase')        AS purchases
FROM events_raw
GROUP BY project_seq, environment_seq, activity_date, device_id
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_player_activity_daily
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW mv_player_activity_daily TO player_activity_daily AS
SELECT
    project_seq,
    environment_seq,
    event_date                              AS activity_date,
    device_id,
    min(assumeNotNull(cohort_date))         AS cohort_date,
    any(install_platform)                   AS install_platform,
    any(install_country)                    AS install_country,
    any(first_app_version)                  AS first_app_version,
    any(acquisition_source)                 AS acquisition_source,
    countIf(event_name = 'session_start')   AS sessions,
    sum(playtime_ms)                        AS playtime_ms,
    count()                                 AS events,
    sum(revenue_micros)                     AS revenue_micros,
    countIf(event_type = 'purchase')        AS purchases
FROM events_raw
GROUP BY project_seq, environment_seq, activity_date, device_id
-- +goose StatementEnd
