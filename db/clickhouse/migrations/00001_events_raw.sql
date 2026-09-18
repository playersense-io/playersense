-- The raw event table (project.md §23).
--
-- Every column carries a `class=` comment. §96 requires that a column cannot be added
-- without being classified, and `migrate verify` reads system.columns.comment to enforce
-- it. The classification is the single source the Article 30 record, the Play Data Safety
-- declaration and the Apple privacy manifest are generated from.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS events_raw
(
    -- Integer surrogates carry the sort key; the UUIDs travel as attributes (§23.1).
    org_seq                 UInt32                      COMMENT 'class=none',
    project_seq             UInt32                      COMMENT 'class=none',
    environment_seq         UInt16                      COMMENT 'class=none',

    org_id                  UUID                        COMMENT 'class=none',
    project_id              UUID                        COMMENT 'class=none',
    environment_id          UUID                        COMMENT 'class=none',

    retention_tier          UInt16                      COMMENT 'class=none',
    policy_tier             LowCardinality(String)      COMMENT 'class=none',

    event_time              DateTime64(3, 'UTC')        COMMENT 'class=behavioural',
    ingest_time             DateTime64(3, 'UTC')        COMMENT 'class=behavioural',
    received_at             DateTime64(3, 'UTC')        COMMENT 'class=behavioural',
    event_date              Date MATERIALIZED toDate(event_time) COMMENT 'class=behavioural',

    event_id                UUID                        COMMENT 'class=behavioural',
    batch_id                UUID                        COMMENT 'class=behavioural',
    session_id              String                      COMMENT 'class=identifier',
    -- Nullable, not '' — an anonymous event has no player, and that is not the same
    -- thing as a player whose identifier is the empty string. Measured before choosing:
    -- the null mask costs +0.2% compressed over 10M rows, and `IS NOT NULL` filters
    -- about 2.5x faster than `!= ''` because it reads a bitmask rather than comparing
    -- strings. The sentinel bought nothing, and it hid the distinction.
    player_id               Nullable(String)            COMMENT 'class=identifier',
    install_id              UUID                        COMMENT 'class=identifier',

    -- device_key is the cohort key (§29.2) and actor_key drives player timelines.
    -- device_id is the surrogate for device_key, NOT for actor_key. The two differ the
    -- moment an anonymous player signs in, and cohorts follow the device (§29.2).
    -- Naming it device_id rather than actor_id is deliberate: a surrogate named after
    -- actor_key but derived from device_key is an invitation to assume they match.
    device_key              String  MATERIALIZED toString(install_id) COMMENT 'class=identifier',
    device_id               UInt64  MATERIALIZED sipHash64(project_seq, environment_seq, toString(install_id)) COMMENT 'class=identifier',
    actor_key               String  MATERIALIZED ifNull(player_id, toString(install_id)) COMMENT 'class=identifier',

    event_name              LowCardinality(String)      COMMENT 'class=behavioural',
    event_type              LowCardinality(String)      COMMENT 'class=behavioural',

    platform                LowCardinality(String)      COMMENT 'class=behavioural',
    country_code            LowCardinality(String)      COMMENT 'class=behavioural',
    app_version             LowCardinality(String)      COMMENT 'class=behavioural',
    sdk_name                LowCardinality(String)      COMMENT 'class=behavioural',
    sdk_version             LowCardinality(String)      COMMENT 'class=behavioural',

    -- Stamped by analytics-writer (§29.5). Null when Valkey could not answer; the
    -- nightly derivation backfills.
    cohort_date             Nullable(Date)              COMMENT 'class=derived',
    install_platform        LowCardinality(String)      COMMENT 'class=derived',
    install_country         LowCardinality(String)      COMMENT 'class=derived',
    first_app_version       LowCardinality(String)      COMMENT 'class=derived',

    -- Collection basis, per row, so it survives into the archive (§97).
    consent_analytics       UInt8                       COMMENT 'class=behavioural',
    consent_policy_version  LowCardinality(Nullable(String)) COMMENT 'class=behavioural',
    att_status              LowCardinality(Nullable(String)) COMMENT 'class=behavioural',

    props_string            Map(String, String)         COMMENT 'class=behavioural',
    props_int               Map(String, Int64)          COMMENT 'class=behavioural',
    props_float             Map(String, Float64)        COMMENT 'class=behavioural',
    props_bool              Map(String, UInt8)          COMMENT 'class=behavioural',

    revenue_micros          Int64   DEFAULT 0           COMMENT 'class=financial',
    currency                LowCardinality(Nullable(String)) COMMENT 'class=financial',
    playtime_ms             UInt64  DEFAULT 0           COMMENT 'class=behavioural',

    payload_bytes           UInt32                      COMMENT 'class=none',

    -- MATERIALIZED, never supplied by the writer. Left as a plain column it defaults to
    -- the epoch on any insert that omits it, and `TTL expire_at DELETE` then expires
    -- that row on the first merge — with ttl_only_drop_parts=1, whole parts.
    --
    -- Deriving it from event_time also makes it monotone with the partition key, which
    -- is what ttl_only_drop_parts=1 needs: a part is dropped only when every row in it
    -- has expired, so a TTL column unrelated to the partition key leaves mixed parts
    -- that never fully expire.
    expire_at DateTime MATERIALIZED toDateTime(event_time) + toIntervalDay(retention_tier) COMMENT 'class=none',

    INDEX idx_props mapKeys(props_string) TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedMergeTree
PARTITION BY (retention_tier, toDate(event_time))
ORDER BY (org_seq, project_seq, environment_seq, event_date, event_name, device_id, event_time)
TTL expire_at DELETE
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS events_raw
-- +goose StatementEnd
