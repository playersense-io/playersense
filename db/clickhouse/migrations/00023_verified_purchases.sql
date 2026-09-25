-- Verified purchases in the purchases ledger, next to the ones the game reported (project.md
-- §46-48, ADR 0048).
--
-- 00016 held only what the game reported: TrackRevenue events, analytics about a purchase. A
-- purchase checked with its store server to server is the money fact, and it now lands in the same
-- three tables, told apart by `source`:
--
--   * reported  what mv_purchases copies out of events_raw, as before;
--   * verified  what the worker ships from PostgreSQL's purchase_changes outbox, one row per
--               change: the purchase, its refund, a refund reversed, or a receipt turned away.
--
-- Which one a page counts is a predicate, never a join: for a store the project verifies with,
-- from the day it began, the verified rows; for everything else, the reported ones ("verified
-- wins", internal/query/revenue.go).
--
-- A verified purchase's row changes as the store tells us more (verified, then refunded), so the
-- ledger replaces on `version`, the outbox id of the change that wrote it, and a FINAL read sees
-- its latest state. The rollups chained from it cannot replace; they count each change once as it
-- is inserted, which is why a row says what *changed* (`change`) as well as what the purchase now
-- *is* (`status`): a refund adds to refunds and nothing to purchases.
--
-- The tables are rebuilt rather than altered: the engine gains its version column, and payers and
-- revenue_daily gain keys. There is no live data; `migrate backfill purchases` refills all three
-- from events_raw.

-- +goose Up
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_revenue_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS revenue_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_payers
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS payers SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_purchases
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS purchases SYNC
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per purchase, reported or verified; a verified purchase's row is replaced as it changes.
--
-- device_id defaults to the same surrogate events_raw computes, so a verified purchase whose
-- install is known lands on the device the game's own events describe, and one verified for a
-- player alone lands on a stand-in install the worker derives from the player id.
CREATE TABLE IF NOT EXISTS purchases
(
    project_seq          UInt32                          COMMENT 'class=none',
    environment_seq      UInt16                          COMMENT 'class=none',
    retention_tier       UInt16                          COMMENT 'class=none',
    purchase_date        Date                            COMMENT 'class=behavioural',
    event_time           DateTime64(3, 'UTC')            COMMENT 'class=behavioural',
    event_id             UUID                            COMMENT 'class=behavioural',

    device_id            UInt64 DEFAULT sipHash64(project_seq, environment_seq, toString(install_id)) COMMENT 'class=identifier',
    install_id           UUID                            COMMENT 'class=identifier',
    player_id            Nullable(String)                COMMENT 'class=identifier',
    session_id           String                          COMMENT 'class=identifier',

    event_name           LowCardinality(String)          COMMENT 'class=behavioural',
    product_id           LowCardinality(String)          COMMENT 'class=financial',
    store                LowCardinality(String)          COMMENT 'class=financial',
    revenue_micros       Int64                           COMMENT 'class=financial',
    currency             LowCardinality(String)          COMMENT 'class=financial',

    platform             LowCardinality(String)          COMMENT 'class=behavioural',
    country_code         LowCardinality(String)          COMMENT 'class=behavioural',
    app_version          LowCardinality(String)          COMMENT 'class=behavioural',
    cohort_date          Nullable(Date)                  COMMENT 'class=derived',

    -- reported or verified.
    source               LowCardinality(String) DEFAULT 'reported' COMMENT 'class=none',
    -- What the purchase is now: '' for a reported one; verified, refunded, sandbox or rejected.
    status               LowCardinality(String) DEFAULT ''  COMMENT 'class=financial',
    -- What this row changed, which is what the rollups count.
    change               Enum8('purchase' = 1, 'refund' = 2, 'reinstate' = 3, 'rejection' = 4) DEFAULT 'purchase' COMMENT 'class=none',
    -- The outbox id of the change that wrote it; 0 for a reported purchase, which never changes.
    version              UInt64 DEFAULT 0                COMMENT 'class=none',
    store_transaction_id String DEFAULT ''               COMMENT 'class=identifier',
    refunded_at          Nullable(DateTime64(3, 'UTC'))  COMMENT 'class=financial',
    refund_reason        LowCardinality(String) DEFAULT '' COMMENT 'class=financial',
    rejection            LowCardinality(String) DEFAULT '' COMMENT 'class=none',
    verify_ms            UInt32 DEFAULT 0                COMMENT 'class=none'
)
ENGINE = ReplicatedReplacingMergeTree(version)
PARTITION BY (retention_tier, toYYYYMM(purchase_date))
ORDER BY (project_seq, environment_seq, purchase_date, event_time, event_id)
TTL toDateTime(event_time) + toIntervalDay(retention_tier)
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_purchases TO purchases AS
SELECT
    e.project_seq                                                       AS project_seq,
    e.environment_seq                                                   AS environment_seq,
    e.retention_tier                                                    AS retention_tier,
    e.event_date                                                        AS purchase_date,
    e.event_time                                                        AS event_time,
    e.event_id                                                          AS event_id,
    e.device_id                                                         AS device_id,
    e.install_id                                                        AS install_id,
    e.player_id                                                         AS player_id,
    e.session_id                                                        AS session_id,
    e.event_name                                                        AS event_name,
    if(e.props_string['product_id'] != '', e.props_string['product_id'],
       toString(e.event_name))                                          AS product_id,
    multiIf(e.props_string['store'] != '', e.props_string['store'],
            e.platform = 'ios', 'app_store',
            e.platform = 'android', 'google_play',
            toString(e.platform))                                       AS store,
    e.revenue_micros                                                    AS revenue_micros,
    ifNull(e.currency, '')                                              AS currency,
    e.platform                                                          AS platform,
    e.country_code                                                      AS country_code,
    e.app_version                                                       AS app_version,
    e.cohort_date                                                       AS cohort_date,
    'reported'                                                          AS source
FROM events_raw AS e
WHERE e.revenue_micros > 0
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per paying device, per store and source: when it first and last bought, how often and
-- for how much. Keyed by store and source as well as the device so that "verified wins" can pick
-- the rows to finish a device from; a device almost always buys in one store, so it costs next to
-- nothing. Only purchases count: a refund does not undo having paid, a test purchase was never
-- paid, and a rejection was never a purchase.
CREATE TABLE IF NOT EXISTS payers
(
    project_seq       UInt32                                             COMMENT 'class=none',
    environment_seq   UInt16                                             COMMENT 'class=none',
    device_id         UInt64                                             COMMENT 'class=identifier',
    store             LowCardinality(String)                             COMMENT 'class=financial',
    source            LowCardinality(String)                             COMMENT 'class=none',

    install_id        SimpleAggregateFunction(any, UUID)                 COMMENT 'class=identifier',
    player_id         SimpleAggregateFunction(anyLast, Nullable(String)) COMMENT 'class=identifier',
    first_purchase_at SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=financial',
    last_purchase_at  SimpleAggregateFunction(max, DateTime64(3, 'UTC')) COMMENT 'class=financial',
    purchases         SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=financial',
    revenue_micros    SimpleAggregateFunction(sum, Int64)                COMMENT 'class=financial'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, device_id, store, source)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_payers TO payers AS
SELECT
    p.project_seq                AS project_seq,
    p.environment_seq            AS environment_seq,
    p.device_id                  AS device_id,
    p.store                      AS store,
    p.source                     AS source,
    any(p.install_id)            AS install_id,
    anyLast(p.player_id)         AS player_id,
    min(p.event_time)            AS first_purchase_at,
    max(p.event_time)            AS last_purchase_at,
    count()                      AS purchases,
    sum(p.revenue_micros)        AS revenue_micros
FROM purchases AS p
WHERE p.change = 'purchase' AND p.status != 'sandbox'
GROUP BY project_seq, environment_seq, device_id, store, source
-- +goose StatementEnd

-- +goose StatementBegin
-- Revenue by day, store, source, country, product and price, as 00016 had it, with what else a
-- change can say: refunds (booked on the day of the refund, net of reversals), test purchases and
-- rejected receipts. purchases, revenue_micros and buyers count paid purchases only, so every
-- reader that summed them before reads the same thing now.
CREATE TABLE IF NOT EXISTS revenue_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    day             Date                                    COMMENT 'class=behavioural',
    store           LowCardinality(String)                  COMMENT 'class=financial',
    source          LowCardinality(String)                  COMMENT 'class=none',
    country_code    LowCardinality(String)                  COMMENT 'class=behavioural',
    product_id      LowCardinality(String)                  COMMENT 'class=financial',
    currency        LowCardinality(String)                  COMMENT 'class=financial',
    price_micros    Int64                                   COMMENT 'class=financial',

    purchases       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',
    revenue_micros  SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    refunds         SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    refunded_micros SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    sandbox         SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',
    rejected        SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',
    buyers          AggregateFunction(uniqCombined(12), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (project_seq, environment_seq, day, store, source, country_code, product_id, currency, price_micros)
TTL day + INTERVAL 1095 DAY
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_revenue_daily TO revenue_daily AS
SELECT
    p.project_seq                                                           AS project_seq,
    p.environment_seq                                                       AS environment_seq,
    if(p.change IN ('refund', 'reinstate') AND p.refunded_at IS NOT NULL,
       toDate(assumeNotNull(p.refunded_at)), p.purchase_date)               AS day,
    p.store                                                                 AS store,
    p.source                                                                AS source,
    p.country_code                                                          AS country_code,
    p.product_id                                                            AS product_id,
    p.currency                                                              AS currency,
    p.revenue_micros                                                        AS price_micros,
    countIf(p.change = 'purchase' AND p.status != 'sandbox')                AS purchases,
    sumIf(p.revenue_micros, p.change = 'purchase' AND p.status != 'sandbox') AS revenue_micros,
    toInt64(countIf(p.change = 'refund')) - toInt64(countIf(p.change = 'reinstate')) AS refunds,
    sumIf(p.revenue_micros, p.change = 'refund') - sumIf(p.revenue_micros, p.change = 'reinstate') AS refunded_micros,
    countIf(p.change = 'purchase' AND p.status = 'sandbox')                 AS sandbox,
    countIf(p.change = 'rejection')                                         AS rejected,
    uniqCombinedStateIf(12)(p.device_id, p.change = 'purchase' AND p.status != 'sandbox') AS buyers
FROM purchases AS p
GROUP BY project_seq, environment_seq, day, store, source, country_code, product_id, currency, price_micros
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_revenue_daily
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS revenue_daily SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_payers
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS payers SYNC
-- +goose StatementEnd
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_purchases
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS purchases SYNC
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS purchases
(
    project_seq     UInt32                          COMMENT 'class=none',
    environment_seq UInt16                          COMMENT 'class=none',
    retention_tier  UInt16                          COMMENT 'class=none',
    purchase_date   Date                            COMMENT 'class=behavioural',
    event_time      DateTime64(3, 'UTC')            COMMENT 'class=behavioural',
    event_id        UUID                            COMMENT 'class=behavioural',
    device_id       UInt64                          COMMENT 'class=identifier',
    install_id      UUID                            COMMENT 'class=identifier',
    player_id       Nullable(String)                COMMENT 'class=identifier',
    session_id      String                          COMMENT 'class=identifier',
    event_name      LowCardinality(String)          COMMENT 'class=behavioural',
    product_id      LowCardinality(String)          COMMENT 'class=financial',
    store           LowCardinality(String)          COMMENT 'class=financial',
    revenue_micros  Int64                           COMMENT 'class=financial',
    currency        LowCardinality(String)          COMMENT 'class=financial',
    platform        LowCardinality(String)          COMMENT 'class=behavioural',
    country_code    LowCardinality(String)          COMMENT 'class=behavioural',
    app_version     LowCardinality(String)          COMMENT 'class=behavioural',
    cohort_date     Nullable(Date)                  COMMENT 'class=derived'
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_tier, toYYYYMM(purchase_date))
ORDER BY (project_seq, environment_seq, purchase_date, event_time, event_id)
TTL toDateTime(event_time) + toIntervalDay(retention_tier)
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd
-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_purchases TO purchases AS
SELECT
    e.project_seq AS project_seq, e.environment_seq AS environment_seq, e.retention_tier AS retention_tier,
    e.event_date AS purchase_date, e.event_time AS event_time, e.event_id AS event_id,
    e.device_id AS device_id, e.install_id AS install_id, e.player_id AS player_id, e.session_id AS session_id,
    e.event_name AS event_name,
    if(e.props_string['product_id'] != '', e.props_string['product_id'], toString(e.event_name)) AS product_id,
    multiIf(e.props_string['store'] != '', e.props_string['store'], e.platform = 'ios', 'app_store',
            e.platform = 'android', 'google_play', toString(e.platform)) AS store,
    e.revenue_micros AS revenue_micros, ifNull(e.currency, '') AS currency, e.platform AS platform,
    e.country_code AS country_code, e.app_version AS app_version, e.cohort_date AS cohort_date
FROM events_raw AS e
WHERE e.revenue_micros > 0
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS payers
(
    project_seq       UInt32                                             COMMENT 'class=none',
    environment_seq   UInt16                                             COMMENT 'class=none',
    device_id         UInt64                                             COMMENT 'class=identifier',
    install_id        SimpleAggregateFunction(any, UUID)                 COMMENT 'class=identifier',
    player_id         SimpleAggregateFunction(anyLast, Nullable(String)) COMMENT 'class=identifier',
    first_purchase_at SimpleAggregateFunction(min, DateTime64(3, 'UTC')) COMMENT 'class=financial',
    last_purchase_at  SimpleAggregateFunction(max, DateTime64(3, 'UTC')) COMMENT 'class=financial',
    purchases         SimpleAggregateFunction(sum, UInt64)               COMMENT 'class=financial',
    revenue_micros    SimpleAggregateFunction(sum, Int64)                COMMENT 'class=financial'
)
ENGINE = ReplicatedAggregatingMergeTree
ORDER BY (project_seq, environment_seq, device_id)
-- +goose StatementEnd
-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_payers TO payers AS
SELECT p.project_seq AS project_seq, p.environment_seq AS environment_seq, p.device_id AS device_id,
    any(p.install_id) AS install_id, anyLast(p.player_id) AS player_id,
    min(p.event_time) AS first_purchase_at, max(p.event_time) AS last_purchase_at,
    count() AS purchases, sum(p.revenue_micros) AS revenue_micros
FROM purchases AS p
GROUP BY project_seq, environment_seq, device_id
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS revenue_daily
(
    project_seq     UInt32                                  COMMENT 'class=none',
    environment_seq UInt16                                  COMMENT 'class=none',
    day             Date                                    COMMENT 'class=behavioural',
    store           LowCardinality(String)                  COMMENT 'class=financial',
    country_code    LowCardinality(String)                  COMMENT 'class=behavioural',
    product_id      LowCardinality(String)                  COMMENT 'class=financial',
    currency        LowCardinality(String)                  COMMENT 'class=financial',
    price_micros    Int64                                   COMMENT 'class=financial',
    purchases       SimpleAggregateFunction(sum, UInt64)    COMMENT 'class=financial',
    revenue_micros  SimpleAggregateFunction(sum, Int64)     COMMENT 'class=financial',
    buyers          AggregateFunction(uniqCombined(12), UInt64) COMMENT 'class=derived'
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (project_seq, environment_seq, day, store, country_code, product_id, currency, price_micros)
TTL day + INTERVAL 1095 DAY
-- +goose StatementEnd
-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_revenue_daily TO revenue_daily AS
SELECT p.project_seq AS project_seq, p.environment_seq AS environment_seq, p.purchase_date AS day,
    p.store AS store, p.country_code AS country_code, p.product_id AS product_id, p.currency AS currency,
    p.revenue_micros AS price_micros, count() AS purchases, sum(p.revenue_micros) AS revenue_micros,
    uniqCombinedState(12)(p.device_id) AS buyers
FROM purchases AS p
GROUP BY project_seq, environment_seq, day, store, country_code, product_id, currency, price_micros
-- +goose StatementEnd
