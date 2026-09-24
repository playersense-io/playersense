-- Move events_raw onto the hot/cold storage policy (project.md §3.4, §33, §35).
--
-- §4.2 is the fact this answers: a billion events a day is roughly a terabyte before
-- compression, so no VPS disk is a retention plan and §33's "3-14 day hot window" is really
-- a statement about which disk the bytes sit on rather than how long they are kept.
--
-- The policy is defined in infra config, not here, which is the point: the schema names
-- `hot_cold` and the deployment decides what is behind `cold` — a second local directory in
-- development, a GCS bucket in production. That is the same arrangement managed ClickHouse
-- uses to appear to have unlimited storage, and it is why old partitions stay *queryable*
-- instead of only existing in the Parquet archive that §34's unbuilt cold-query path would
-- have to go and fetch.
--
-- The TTL now says two things rather than one:
--
--   TO VOLUME 'cold'  after the hot window — the part leaves local NVMe and stays readable
--   DELETE            at the project's own retention tier — unchanged, still per row
--
-- Both are declarative instructions to a background merge, so neither is instant and
-- neither can be waited on. Appendix C.6 is unchanged by this: the archive is still the
-- copy that survives losing the cluster, because the cold volume holds ClickHouse's own
-- format and only ClickHouse can read it.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE events_raw MODIFY SETTING storage_policy = 'hot_cold'
-- +goose StatementEnd

-- +goose StatementBegin
-- Seven days of local residency, from §33's 3-14 day range.
--
-- A constant rather than the per-row retention tier, and the distinction is the whole
-- design: retention is a product decision that varies per project, while this is an
-- operational one about how much fits on the disk. A project on a 365-day tier keeps its
-- data for a year either way; only the first week of it is on NVMe.
--
-- The DELETE arm is the row's own event time plus its retention tier (00001), so it is per
-- row and unchanged. Raising the hot window later is an ALTER, and a cheap one: it
-- changes which volume future merges target rather than rewriting anything.
ALTER TABLE events_raw MODIFY TTL
    toDateTime(event_time) + toIntervalDay(7) TO VOLUME 'cold',
    toDateTime(event_time) + toIntervalDay(retention_tier) DELETE
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE events_raw MODIFY TTL toDateTime(event_time) + toIntervalDay(retention_tier) DELETE
-- +goose StatementEnd

-- +goose StatementBegin
-- Back to the single-disk policy. This only succeeds while every part is on `default`,
-- which is the honest behaviour: once parts have moved to the cold volume there is nothing
-- for a down-migration to do that would not be a data move, and a migration that silently
-- moved terabytes would be worse than one that refuses.
ALTER TABLE events_raw MODIFY SETTING storage_policy = 'default'
-- +goose StatementEnd
