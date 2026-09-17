-- Put player_activity_daily on the hot/cold policy too (project.md §29.7, §33.1).
--
-- 00009 tiered events_raw, which is the largest table, and left the one §33.1 singles out
-- by name entirely on local disk:
--
--   > Most aggregates are negligible next to raw telemetry. `player_activity_daily` is the
--   > exception, because it holds one row per active device per day rather than one row per
--   > dimension combination. At ten million daily active players it is roughly 300 million
--   > rows a month, on the order of 10 GB a month compressed, so about 120 GB a year.
--
-- With a 400-day TTL that is roughly 130 GB at steady state, sitting beside the hot window
-- of a table that is now free to be much larger than the disk. Tiering the raw events and
-- not this one moves the ceiling without removing it.
--
-- ## Why 90 days, and why this table can be tiered at all
--
-- The reads decide it. This table is read by the nightly derivation (§79.1), which rebuilds
-- WORKER_ACTIVITY_MONTHS trailing activity months — two by default. Nothing else reads it:
-- the reporting API answers from the facts derived *from* it, never from it directly.
--
-- Ninety days leaves the routine window three times clear of the cold volume, so the nightly
-- job never touches object storage. A rebuild of an older month — a repair, run by hand —
-- reads from GCS and is slower, which is the right trade for a job nobody runs on a
-- schedule.
--
-- ## What is deliberately *not* tiered
--
-- `player_lifetime` stays entirely on local disk, and that is a decision rather than an
-- omission. `player_lifetime_v` is on the query path: §29.10's rolling retention reads it
-- directly, so a cold read there lands in a customer's dashboard rather than in a batch job.
-- It is also the smaller problem — one row per device rather than one per device-day, and
-- §33.1's point is that it "grows with the player base rather than with time".

-- +goose Up
-- +goose StatementBegin
ALTER TABLE player_activity_daily MODIFY SETTING storage_policy = 'hot_cold'
-- +goose StatementEnd

-- +goose StatementBegin
-- The DELETE arm is unchanged: §29.7's 400 days, which is what keeps this table bounded at
-- all. The move is additive.
ALTER TABLE player_activity_daily MODIFY TTL
    activity_date + toIntervalDay(90) TO VOLUME 'cold',
    activity_date + toIntervalDay(400) DELETE
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE player_activity_daily MODIFY TTL activity_date + toIntervalDay(400) DELETE
-- +goose StatementEnd

-- +goose StatementBegin
-- Succeeds only while every part is still on the hot volume, for the reason 00009 gives:
-- a down-migration that silently moved a hundred gigabytes back would be worse than one
-- that refuses.
ALTER TABLE player_activity_daily MODIFY SETTING storage_policy = 'default'
-- +goose StatementEnd
