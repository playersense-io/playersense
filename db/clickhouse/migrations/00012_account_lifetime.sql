-- What one *account* has done, as an aggregate rather than a join (§41, §45).
--
-- `player_lifetime` is keyed on the device (§29.2, ADR 0004): a reinstall is a new install,
-- and a player with a phone and a tablet is two rows. That is the right key for cohort
-- reporting and the wrong one for a targeting rule that says "whales". Somebody who spent
-- $60 on each of two devices is not an install whale and is an account whale, and a studio
-- writing the rule has to be able to say which it meant.
--
-- So this is the same construction with a different key. No join anywhere: the materialized
-- view aggregates at insert time exactly as `mv_player_lifetime` does, and §29's no-join
-- doctrine is untouched.
--
-- ## Why `account_*` and not `actor_*`
--
-- `events_raw.actor_key` is `if(player_id != '', player_id, toString(install_id))` — it is
-- never empty, and for an anonymous player it *is* the install. A table named after it would
-- duplicate `player_lifetime` for everyone who never signed in, and the name would invite
-- exactly the assumption 00001's own comment warns about. This table is about accounts, so
-- it is keyed on `player_id` and filtered to rows that have one.
--
-- ## What is deliberately not here
--
-- `first_attrs`. The sticky arrival dimensions are a cohort concern and cohorts are
-- device-keyed (§29.3), so duplicating them per account would be a second answer to a
-- question that already has one.
--
-- ## Erasure
--
-- This table holds `account_key`, which is the studio's own player identifier. It is
-- therefore part of §61's purge — see internal/erasure/clickhouse.go. A per-player table
-- outside the erasure path is a person who was told they were deleted and was not.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS account_lifetime
(
    project_seq     UInt32                                             COMMENT 'class=none',
    environment_seq UInt16                                             COMMENT 'class=none',
    -- The surrogate the sort key uses, derived from player_id and nothing else.
    account_id      UInt64                                             COMMENT 'class=identifier',
    account_key     SimpleAggregateFunction(any, String)               COMMENT 'class=identifier',
    first_seen      SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=derived',
    last_seen       SimpleAggregateFunction(max, DateTime64(3, 'UTC')) COMMENT 'class=derived',
    playtime_ms     SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=derived',
    revenue_micros  SimpleAggregateFunction(sum, Int64)                COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=financial',
    active_days     AggregateFunction(uniq, Date)                      COMMENT 'class=derived',
    sessions        SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=derived',
    first_purchase_at SimpleAggregateFunction(min, Nullable(DateTime64(3, 'UTC'))) COMMENT 'class=financial',
    last_purchase_at  SimpleAggregateFunction(max, Nullable(DateTime64(3, 'UTC'))) COMMENT 'class=financial'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, account_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_account_lifetime TO account_lifetime AS
SELECT
    project_seq,
    environment_seq,
    -- assumeNotNull, and it is not decoration. player_id is Nullable, so sipHash64 over it
    -- returns Nullable(UInt64) and any() returns Nullable(String) — neither of which is the
    -- column's type. The WHERE below already guarantees there is a value; this says so to the
    -- type system instead of relying on a coercion that is silent when it works and silent
    -- when it does not.
    sipHash64(project_seq, environment_seq, assumeNotNull(player_id)) AS account_id,
    any(assumeNotNull(player_id))                        AS account_key,
    min(ingest_time)                                     AS first_seen,
    max(ingest_time)                                     AS last_seen,
    sum(playtime_ms)                                     AS playtime_ms,
    sum(revenue_micros)                                  AS revenue_micros,
    countIf(event_type = 'purchase')                     AS purchases,
    uniqState(event_date)                                AS active_days,
    countIf(event_name = 'session_start')                AS sessions,
    min(if(event_type = 'purchase', ingest_time, NULL))  AS first_purchase_at,
    max(if(event_type = 'purchase', ingest_time, NULL))  AS last_purchase_at
FROM events_raw
-- Not `actor_key IS NOT NULL`, which is never false: actor_key falls back to the install.
-- Only rows from a player who has identified themselves belong in an account table.
WHERE player_id IS NOT NULL
GROUP BY project_seq, environment_seq, account_id;
-- +goose StatementEnd

-- +goose StatementBegin
-- The only legal read path, as player_lifetime_v is: reading the table directly can see
-- parts that have not merged, and a half-merged row is a smaller lifetime spend than the
-- truth. A targeting rule reading one would quietly drop a whale out of their audience.
CREATE VIEW IF NOT EXISTS account_lifetime_v AS
SELECT
    al.project_seq              AS project_seq,
    al.environment_seq          AS environment_seq,
    al.account_id               AS account_id,
    any(al.account_key)         AS account_key,
    min(al.first_seen)          AS first_seen,
    max(al.last_seen)           AS last_seen,
    sum(al.playtime_ms)         AS playtime_ms,
    sum(al.revenue_micros)      AS revenue_micros,
    sum(al.purchases)           AS purchases,
    uniqMerge(al.active_days)   AS active_days,
    sum(al.sessions)            AS sessions,
    min(al.first_purchase_at)   AS first_purchase_at,
    max(al.last_purchase_at)    AS last_purchase_at
FROM account_lifetime AS al
GROUP BY al.project_seq, al.environment_seq, al.account_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS account_lifetime_v;
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_account_lifetime;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS account_lifetime;
-- +goose StatementEnd
