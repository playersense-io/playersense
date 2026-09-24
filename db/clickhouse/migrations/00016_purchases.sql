-- Purchases, the players who make them, and revenue by what was bought (project.md §13.1
-- `purchase_events`, §27 `revenue_1d`).
--
-- These are the purchases the game *reported*, TrackRevenue events: analytics about a purchase,
-- useful for funnels and trends, not the verified money fact §46 describes, which is a receipt
-- checked against the store server to server and is not built. The pages that read these say so.
--
-- A purchase is any event carrying revenue (normalize.go's event_type 'purchase'). What was bought
-- and where are the studio's properties, read by two names:
--
--   * `product_id`, falling back to the event name, which is what most games call a purchase;
--   * `store`, falling back to the platform's own store: ios to app_store, android to google_play,
--     and the platform name itself otherwise.
--
-- Revenue is in the currency the game reported. There is no FX normalisation (§27 asks for it
-- only "if FX normalization exists"), so every table here carries the currency and a sum across
-- currencies is the reader's decision, not this schema's.
--
-- Purchases are a few percent of events, so every view here reads a block that the WHERE clause
-- has already emptied of almost everything, and the two chained from `purchases` read only what it
-- just wrote.

-- +goose Up
-- +goose StatementBegin
-- One row per purchase.
--
-- Replacing on the sort key, which ends in event_id: a redelivered batch carries the same event
-- ids (§21, ADR 0026), and although the insert is deduplicated by its token, a purchase counted
-- twice is the one duplicate a studio will certainly notice. event_time sits before event_id so
-- the newest purchases are read first and a ledger page stops as soon as it has enough.
--
-- It names the device and the player, so it keeps the project's raw retention exactly as
-- events_raw does (§59), partitioned by the tier so a month of one tier drops whole.
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
    -- The day the device arrived, as analytics-writer stamped it (§29.5), so "bought on day N"
    -- is a subtraction rather than a lookup. Null where the stamp failed, as on events_raw.
    cohort_date     Nullable(Date)                  COMMENT 'class=derived'
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_tier, toYYYYMM(purchase_date))
ORDER BY (project_seq, environment_seq, purchase_date, event_time, event_id)
TTL toDateTime(event_time) + toIntervalDay(retention_tier)
SETTINGS ttl_only_drop_parts = 1
-- +goose StatementEnd

-- +goose StatementBegin
-- Qualified through `e` throughout: several outputs share a name with the column they are made
-- from, and an unqualified reference would resolve to the output instead.
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
    e.cohort_date                                                       AS cohort_date
FROM events_raw AS e
WHERE e.revenue_micros > 0
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per paying device: when it first and last bought, how often and for how much.
--
-- Every paying-player question is a question about this set, which is a few percent of
-- player_lifetime: new payers on a day are the devices whose first purchase fell on it, and a
-- spender tier is a lifetime total. Reading it costs the number of payers, not the number of
-- players.
--
-- No TTL, as player_lifetime has none: its rows are aggregate states, and expiring a partial row
-- would silently shrink a lifetime total. It is purged by device on erasure like the rest.
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
SELECT
    p.project_seq                AS project_seq,
    p.environment_seq            AS environment_seq,
    p.device_id                  AS device_id,
    any(p.install_id)            AS install_id,
    anyLast(p.player_id)         AS player_id,
    min(p.event_time)            AS first_purchase_at,
    max(p.event_time)            AS last_purchase_at,
    count()                      AS purchases,
    sum(p.revenue_micros)        AS revenue_micros
FROM purchases AS p
GROUP BY project_seq, environment_seq, device_id
-- +goose StatementEnd

-- +goose StatementBegin
-- Revenue by day, store, country, product and price.
--
-- A cube rather than one dimension at a time, which §27 warns against for events and which is
-- safe here for a reason that does not hold for events: a row exists only where a purchase did,
-- so the table can never hold more rows than there were purchases, and on any real day it holds
-- far fewer. The Revenue page filters by store, country and product at once, which one-at-a-time
-- rows cannot answer.
--
-- Price is on the key so the price-point chart is a GROUP BY rather than a scan of purchases.
-- A product has one price per currency almost always, so it costs next to nothing.
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
SELECT
    p.project_seq                       AS project_seq,
    p.environment_seq                   AS environment_seq,
    p.purchase_date                     AS day,
    p.store                             AS store,
    p.country_code                      AS country_code,
    p.product_id                        AS product_id,
    p.currency                          AS currency,
    p.revenue_micros                    AS price_micros,
    count()                             AS purchases,
    sum(p.revenue_micros)               AS revenue_micros,
    uniqCombinedState(12)(p.device_id)  AS buyers
FROM purchases AS p
GROUP BY project_seq, environment_seq, day, store, country_code, product_id, currency, price_micros
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
