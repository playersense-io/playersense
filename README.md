# PlayerSense

Game analytics platform. `project.md` is the architecture source of truth; `docs/adr/`
records why it is that way, and `docs/compliance/` maps obligations onto the code that
meets them.

## Local stack

Ports are deliberately **not** the service defaults — a `21xxx` block, below the macOS
ephemeral range so nothing collides with an OS-assigned port. Change them in `.env` and
nowhere else.

| Service | Port |
|---|---|
| PostgreSQL | 21432 |
| ClickHouse HTTP | 21123 |
| ClickHouse native | 21900 |
| Valkey | 21379 |

```bash
cp .env.example .env
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml
docker compose up -d
```

The cold volume of the hot/cold policy is a second local directory by default, which
exercises the policy and the part movement for real. To point it at a GCS bucket instead, set
`CH_COLD_ENDPOINT` and the HMAC pair in `.env` and add the overlay:

```bash
export COMPOSE_FILE=infra/stage1/compose/docker-compose.yml:infra/stage1/compose/docker-compose.cold-object.yml
```

It is an overlay rather than a flag because ClickHouse constructs every declared disk eagerly:
an S3 disk with an empty endpoint is not an absent disk, it is a server that will not start.

## Migrations

Two databases: **PostgreSQL** for accounts, projects, keys, billing and the records that
name a player, and **ClickHouse** for the events.

```bash
go run ./apps/backend/cmd/migrate up          # postgres, then clickhouse
go run ./apps/backend/cmd/migrate postgres    # postgres only
go run ./apps/backend/cmd/migrate up ch       # clickhouse only
go run ./apps/backend/cmd/migrate status      # which ClickHouse migrations are applied
go run ./apps/backend/cmd/migrate verify      # §96 classification, across both
go run ./apps/backend/cmd/migrate reset       # drop everything, Keeper metadata included
go run ./apps/backend/cmd/migrate backfill events_daily sessions   # fill new rollups from history
```

A materialized view only sees inserts made after it exists, so a rollup added by a migration starts
empty. `backfill` runs each named view's own SELECT over the history already in `events_raw`, once,
and fills anything chained from it the same way; it refuses a table that already holds rows, since
running over those would count them twice. Stop analytics-writer while it runs.

`verify` reads the live catalog rather than the schema files, which is what makes it proof
rather than documentation. It used to also enforce §99's residency rule — no column
classified `identifier` in the global tier — and that rule is gone with the tier
([ADR 0038](docs/adr/0038-one-api-one-database.md)); the classification it was written in
terms of is not.

## Layout

```
apps/backend/     Go services and commands (module playersense.dev/backend)
sdk/core/         The SDK protocol in Rust, once, with no I/O of its own
sdk/ffi/          The C ABI over it, for every binding
sdk/unity/        The Unity package: a host, a P/Invoke layer and the API a game calls
db/clickhouse/    Goose migrations for the analytics schema
db/postgres/      Ent owns the PostgreSQL schema
docs/adr/         Architecture decision records
docs/compliance/  Obligation -> mechanism -> code -> test
docs/sdk/         The language-neutral SDK protocol every client implements
infra/stage1/     Local compose stack
```

Requires Go 1.27 (Muzak uses generic methods and `encoding/json/v2`).

## Services

### api — `:21080`

```bash
go run ./apps/backend/cmd/api
```

One binary and one address: the dashboard, the SDK's ingestion and configuration endpoints,
and the reporting and data-subject surfaces. They were four deployables so a cell could run
three of them and so ingestion could fail independently
([ADR 0038](docs/adr/0038-one-api-one-database.md)).

#### The dashboard

`GET /openapi.json` describes the whole surface. Everything under `/v1` needs a Firebase ID token as
`Authorization: Bearer <token>`; `/healthz` does not. A shared admin secret used to work and
deliberately no longer does — a secret cannot say who is asking, and every answer here depends
on who that is. Each route then names the permission it needs, and the refusal names the one
that was missing.

Organizations, projects, API keys, **the people in an organization** — and **the three tables a
studio edits**:

```
GET                         /v1/organizations/{org}/roles
GET                         /v1/organizations/{org}/members
GET                         /v1/organizations/{org}/members/{member_id}
       PUT                  /v1/organizations/{org}/members/{member_id}/role
       PUT                  /v1/organizations/{org}/members/{member_id}/access
              DELETE        /v1/organizations/{org}/members/{member_id}
GET    POST                 /v1/organizations/{org}/invitations
              DELETE        /v1/organizations/{org}/invitations/{invitation_id}
GET                         /v1/invitations              (waiting for you)
       POST                 /v1/invitations/accept
```

Reading needs `member.read`, inviting and revoking `member.invite`, removing `member.remove`.
The two `PUT`s need **an owner or an admin** and no permission at all: a grantable "manage roles"
would be silently equivalent to owner, because whoever held it could write themselves a role
containing everything.

**A role says what, project access says where** — two routes, because `all_projects` is the widest
grant in the product and must not be given by omitting a field from a combined write.

**An invitation is a token in an inbox.** Only its SHA-256 digest is stored, it is shown once in
the link, and accepting it needs a Firebase account that has *verified* the address it was sent to
— so forwarding it does not let a colleague join in somebody else's place. Somebody who lost the
mail accepts by naming the organization instead; the verified address is the authorisation and the
token is only how a link works.

**Nothing is actually emailed yet.** `MAIL_SENDER=log` composes the message and says it delivered
nothing, and the response's `delivered` member says the same — set `MAIL_LOG_BODY=true` to get the
link out of a local stack. Invitations are the one rate-limited route on the dashboard, three per
address per hour, failing closed ([docs/rate-limits.md](docs/rate-limits.md)).

The last owner of an organization cannot be removed or demoted, by either door.

Removing a member **deletes** the row, unlike a revoked key or an archived flag: a membership is
not a record of what happened, it is a live grant of access to player data.

**A project's settings**:

```
GET    PATCH                /v1/organizations/{org}/projects/{project}             the name; project.update
       PUT                  /v1/organizations/{org}/projects/{project}/retention   project.update
       POST   DELETE        /v1/organizations/{org}/projects/{project}/deletion    project.delete
GET                         /v1/organizations/{org}/projects/{project}/apps        platforms and SDKs seen
```

Retention is one of 7, 30, 90, 180 or 365 days, at most the policy tier's cap (365, child-directed
90, kids strict 30) and the plan's `max_raw_retention_days`. New rows take a change within the key
cache's five minutes. A raised tier brings nothing back; a lowered one is enforced on rows already
written by the worker, which deletes raw rows older than it once a day until no row stamped with the
old tier can remain. A deletion waits thirty days and can be cancelled until then; when due, the
worker revokes the project's keys and hides it, and an hour later deletes its analytics from every
ClickHouse table and hands its archive to the expiry sweep. Its usage and invoices stay.

**The audit log** — `audit.read`, which only OWNER and ADMIN hold among the seeded six:

```
GET                         /v1/organizations/{org}/audit    ?action&target_type&target_id&actor&from&to&before
```

Every route that changes something writes an entry: memberships, roles, project
access, keys, invitations, segments, flags, LiveOps events, publishes, credit and billing settings.
**Append-only by construction** — every field on the row is immutable and the mixin omits
`updated_at`, so ent generates an update builder with no setter at all, and §08's "a log the operator
can edit proves nothing" is a compile error rather than a policy.

Entries are written **by hand, at the decision**, not by middleware. Middleware records requests; an
audit log has to record what something *was*: "Ada's role changed from developer to owner" and "the
rollout moved from 10% to 100%" were never in the request. Forgetting one fails the suite —
`TestEveryMutatingRouteDecidesAboutAudit` reads the OpenAPI document and demands that every mutating
route is either recorded or exempt with a reason
([ADR 0037](docs/adr/0037-an-audit-log-records-decisions-not-requests.md)).

Actions PlayerSense staff take on an account are entered as **PlayerSense staff** with the address
their verified sign-in carried (see *The operations console* below).

**Usage, the wallet and the plan** — `billing.read`, except the last, which needs `billing.manage`:

```
GET                         /v1/organizations/{org}/usage
GET                         /v1/organizations/{org}/billing
GET                         /v1/organizations/{org}/billing/ledger
       PUT                  /v1/organizations/{org}/billing/auto-topup
```

**The balance is not a column.** Credit is held as *lots* — one per grant, each with its own
remaining amount and its own expiry — because promotional credit expires and a single number cannot
expire part of itself. `available_micros` is the sum of what is left in every lot that has not
lapsed; `ledger_balance_micros` is the ledger's running total, which is briefly higher between credit
lapsing and the sweep writing it off. Both are returned, because a number that looks wrong should be
explainable ([ADR 0036](docs/adr/0036-a-wallet-is-lots-because-credit-expires.md)).

**Credit is spent promos first — soonest to expire first — then purchased credit oldest first.** A
promo is the only credit that can be lost, so spending a customer's own money while a trial credit
ticks down is charging them for something they were already given. A promo lasts ninety days unless
somebody names another date; purchased credit never expires and cannot be given a date.

**Putting credit in a wallet is a staff action**, behind the operations console's sign-in rather than
any customer permission, so an owner holding everything still cannot grant themselves credit:

```
       POST                 /v1/operator/organizations/{org}/credits
       POST                 /v1/operator/organizations/{org}/charges
```

`reference` on both is the idempotency key: the same one twice grants or charges once. The staff
member who acted is on the lot, in the customer's audit log and in `staff_access` with the ticket.

The **usage meter** runs on the worker, deriving each day from `metrics_daily` — the same fact the
dashboard's DAU comes from, so an invoice and a chart are two readings of one number. A day is
rewritten until it settles and final at eight days. Tracked players is reported as the busiest day
rather than a sum: adding daily figures counts one player once per day they played.

**Players are charged as they arrive** ([ADR 0039](docs/adr/0039-players-are-charged-as-they-arrive.md)).
`billing-writer` counts each organization's new tracked players this month as batches arrive, prices
each at its *position* in the month on the plan's rate card, and queues the charge; every five seconds
the queue is drained into the wallet. Usage the wallet cannot cover becomes arrears — never a refused
charge, and never a refused batch — and the next top-up pays it first. The billing page's `month`
shows the exact count, what it cost, and the band the next player lands in.

**Prices are rate cards, and staff publish them rather than edit them:**

```
GET    POST                 /v1/operator/plans
       PUT                  /v1/operator/plans/{plan}
GET    POST                 /v1/operator/plans/{plan}/rate-cards
       PUT                  /v1/operator/organizations/{org}/plan
GET                         /v1/pricing                 public: what the landing page reads
```

A card is a plan's tiers from `effective_from` on. It cannot be changed, and it starts no sooner than
a minute after it is published — chargers keep prices in memory for less than that, so every one of
them has the card before it applies. A new price is a new card, and each player is priced at the card in force when they arrived. Tiers are
player positions within a calendar month, inclusive, and the last is unbounded. The launch prices —
$2.50 per 1,000 players to 100,000, $2.00 to 500,000, $1.75 to 1,000,000, $1.50 above, so a studio
with 200,000 players a month and 10,000 included pays $425, and one with 500,000 pays $1,025 — are data, loaded like this (`$TOKEN` is a staff
member's Firebase ID token and `$TICKET` the change's ticket reference):

```bash
curl -X POST localhost:21080/v1/operator/plans -H "Authorization: Bearer $TOKEN" -H "X-Staff-Ticket: $TICKET" -d '{
  "key": "payg", "name": "Pay as you go", "is_default": true,
  "entitlements": {"included_mau": 10000}}'

curl -X POST localhost:21080/v1/operator/plans/payg/rate-cards -H "Authorization: Bearer $TOKEN" -H "X-Staff-Ticket: $TICKET" -d '{
  "mode": "graduated", "note": "launch prices: $2.50 per 1,000 players",
  "tiers": [
    {"up_to": 100000,  "unit_price_micros": 2500},
    {"up_to": 500000,  "unit_price_micros": 2000},
    {"up_to": 1000000, "unit_price_micros": 1750},
    {"unit_price_micros": 1500}]}'
```

**Money reaches the wallet through Stripe**, and PlayerSense stores no card or billing details
([ADR 0040](docs/adr/0040-stripe-funds-the-wallet.md)):

```
       POST                 /v1/organizations/{org}/billing/top-ups    → a Stripe Checkout URL
       POST                 /v1/organizations/{org}/billing/portal     → Stripe's customer portal URL
       POST                 /v1/stripe/webhook                         Stripe only; signature-verified
```

Both `billing.manage` routes return a URL to send the browser to. Nothing is credited by opening
Checkout or by the browser coming back — only by Stripe's signed webhook, keyed on the PaymentIntent so
a payment reported several ways is credited once. The card paid with is saved at Stripe, and automatic
top-up (`PUT …/billing/auto-topup`) charges it from `billing-writer` when the balance falls below the
threshold, with an idempotency key committed before the request so a lost response never becomes a
second charge. Every top-up is invoiced by Stripe — Checkout top-ups and automatic ones alike — with the
invoice in the customer portal. A refund or a dispute takes the credit back — never promotional credit — and turns
automatic top-up off; a failed refund or a won dispute puts it back. The charge that leaves a balance
low queues the top-up in its own transaction, and the
queue is worked every two seconds; at most 3 automatic charges per organization in any 24 hours
([ADR 0041](docs/adr/0041-top-ups-are-queued-by-the-charge-pricing-is-cached.md)). Without `STRIPE_SECRET_KEY` these routes answer 503.

To receive webhooks locally, forward them with the Stripe CLI and give the API the secret it prints:

```bash
stripe listen --events customer.subscription.created,customer.subscription.updated,customer.subscription.deleted,payment_intent.succeeded,payment_intent.payment_failed,payment_intent.canceled,checkout.session.completed,checkout.session.async_payment_succeeded,refund.created,refund.updated,refund.failed,charge.dispute.created,charge.dispute.funds_withdrawn,charge.dispute.funds_reinstated,charge.dispute.closed,invoice.finalized,invoice.sent,invoice.paid,invoice.voided,invoice.marked_uncollectible,invoice.overdue,invoice_payment.paid,credit_note.created \
  --forward-to localhost:21080/v1/stripe/webhook
STRIPE_WEBHOOK_SECRET=whsec_… go run ./apps/backend/cmd/api    # the secret `stripe listen` printed
```

Against Stripe's test mode, `STRIPE_LIVE=1 go test ./apps/backend/internal/payments -run Live` checks
the SDK wiring, and with `stripe listen` and the API running, `STRIPE_E2E=1 … -run EndToEnd` makes a
real automatic top-up and waits for Stripe's own webhook for it.

**Plans are Stripe subscriptions** ([ADR 0042](docs/adr/0042-plans-are-stripe-subscriptions.md)).
Enterprise, $199 a month or $1,990 a year (ten months' price), unlocks `premium_support`, `slack_reports` and `mcp_access`;
Free unlocks nothing. Staff set a plan's features with `PUT /v1/operator/plans/{plan}` and its prices
with `PUT /v1/operator/plans/{plan}/subscription`, which creates the Stripe Product and Prices. A studio
subscribes with `POST /v1/organizations/{org}/billing/subscription` (a Checkout URL) and manages or
cancels in the portal; Stripe's `customer.subscription.*` webhooks move the organization onto the plan
and, when it ends, back to the default. The subscription is billed to the card; players are still charged
from the wallet on every plan.

```bash
curl -X POST localhost:21080/v1/operator/plans -H "Authorization: Bearer $TOKEN" -H "X-Staff-Ticket: $TICKET" -d '{
  "key": "enterprise", "name": "Enterprise",
  "entitlements": {"included_mau": 10000},
  "features": ["premium_support", "slack_reports", "mcp_access"]}'

curl -X PUT localhost:21080/v1/operator/plans/enterprise/subscription -H "Authorization: Bearer $TOKEN" -H "X-Staff-Ticket: $TICKET" -d '{
  "monthly_price_micros": 199000000, "yearly_price_micros": 1990000000}'
```

Enterprise charges players only once it has a rate card of its own — publish one, the same as Free's or
different.

**Owing is tolerated, within bounds** ([ADR 0043](docs/adr/0043-a-bounded-tolerance-for-accounts-that-owe.md)).
An account that owes keeps ingesting for `BILLING_GRACE_PERIOD` (7 days), or until it owes its credit
limit — 20% of its last 30 days' paid usage, never below a $50 floor or its plan's `credit_limit_micros` —
whichever comes first. Then its SDK batches are answered 429 with `Retry-After`, which the SDK keeps
them for, until it pays; paying lets it back in within about half a minute. Staff can set an exact limit
for one organization with `PUT /v1/operator/organizations/{org}/credit-limit`.

**Contracted organizations are invoiced monthly** ([ADR 0045](docs/adr/0045-contracted-accounts-are-invoiced-monthly.md)).
Staff put an organization on a contract — net terms, the grace after a missed due date, a platform
fee, a minimum commitment, a credit limit, a PO number — and from then on usage its credit does not
cover is a *receivable*, owed on the next invoice rather than overdue. `billing-writer` closes each
month `BILLING_INVOICE_CLOSE_AFTER` (2 hours) after it ends and issues one Stripe invoice
(`send_invoice`, due `terms_days` later): the fee, the month's players, credits applied, and any top-up
to the minimum. An invoice is a stretch of the ledger, so it cannot disagree with it; usage that
settles after its month closed is on the next invoice as prior-period usage. Stripe collects it — by
bank transfer or card on its hosted page — and its webhook settles the receivable. The account is
suspended only `overdue_grace_days` after a missed due date, or at once when what it owes, billed or
not, reaches its credit limit. Customers of an invoiced organization cannot top up, turn on automatic
top-up or subscribe; its plan fee is on the invoice, and staff assign its plan.

```
PUT    /v1/operator/organizations/{org}/contract                            invoiced or prepaid, from now
GET    /v1/operator/organizations/{org}/invoices                            drafts, failures, discrepancies
POST   /v1/operator/organizations/{org}/invoices/{invoice_id}/mark-paid     a wire Stripe did not see
POST   /v1/operator/organizations/{org}/invoices/{invoice_id}/credit-notes  lower an open invoice
GET    /v1/organizations/{org}/billing/invoices                             billing.read
GET    /v1/organizations/{org}/billing/invoices/{invoice_id}                billing.read
```

```bash
curl -X PUT localhost:21080/v1/operator/organizations/$ORG/contract -H "Authorization: Bearer $TOKEN" -H "X-Staff-Ticket: $TICKET" -d '{
  "collection": "invoiced", "terms_days": 30, "overdue_grace_days": 14,
  "platform_fee_micros": 2000000000, "minimum_commitment_micros": 5000000000,
  "credit_limit_micros": 20000000000, "billing_email": "ap@publisher.example",
  "po_number": "PO-4471"}'
```

Moving an organization back to `prepaid` issues a final invoice for everything unbilled; its open
invoices stay collectable. Invoice reminder emails are Stripe's Dashboard setting.

`is_default` is the plan every new organization is put on. With no default plan, organizations get no
billing account and `billing-writer` warns that their players are not being billed. The included
players are free and still hold positions: with 10,000 included, player 100,001 is still in the
$0.002 band.

And **the three tables a studio edits**:

```
GET    POST                 /v1/organizations/{org}/projects/{project}/segments
GET    PUT    DELETE        /v1/organizations/{org}/projects/{project}/segments/{key}
GET    POST                 /v1/organizations/{org}/projects/{project}/flags
GET    PUT    DELETE        /v1/organizations/{org}/projects/{project}/flags/{key}
GET    POST                 /v1/organizations/{org}/projects/{project}/liveops-events
GET    PUT    DELETE        /v1/organizations/{org}/projects/{project}/liveops-events/{key}
GET                         /v1/organizations/{org}/projects/{project}/config/{environment}/draft
       POST                 /v1/organizations/{org}/projects/{project}/config/{environment}/publish
GET                         /v1/organizations/{org}/projects/{project}/config/{environment}/versions
GET                         /v1/organizations/{org}/projects/{project}/config/{environment}/versions/{v}
       POST                 /v1/organizations/{org}/projects/{project}/config/{environment}/versions/{v}/rollback
GET    POST                 /v1/organizations/{org}/projects/{project}/experiments
GET    PUT    DELETE        /v1/organizations/{org}/projects/{project}/experiments/{id}
       POST                 …/experiments/{id}/start, /stop, /conclude, /ship
       PUT                  …/experiments/{id}/traffic
```

Reads need `liveops.read`, writes `liveops.write`, and publishing and rolling back alone need
`liveops.publish` — which is what lets a DEVELOPER draft a rollout and hold no power to ship it.

**An experiment is its own record, compiled into its flag.** It names a flag, two to eight arms
(each a value of the flag's kind, different from the others, weights adding to 100), the share
of the audience let in and the metric it is judged on. While it runs, every publish compiles it
into the flag as a split: one hash decides who is in the test and another which arm, so raising
traffic only adds players. config-api tells the SDK the experiment and arm beside the value, and
the SDK puts them on the flag's `feature_exposure` as `experiment` and `arm`, which is what
`flag_assignments` counts; an SDK that predates this is counted by value. Stopping or concluding
takes the split out of the next publish, and shipping makes the winner the flag's fallback.
A rollback publishes an earlier version's document again as the next version, with the reason.

**A write is checked as thoroughly as a publish.** The rule is parsed, every segment reference is
resolved against the project, a cycle is refused, and every flag value is checked against the
flag's declared kind — so "segment `whales` does not exist" reaches the person who typed it
rather than whoever presses publish three days later
([ADR 0034](docs/adr/0034-a-rule-is-validated-when-it-is-written.md)).

**DELETE archives; it never deletes.** A published version holds each rule inlined, so the row is
what still explains version 7. The next publish stops carrying it.

**The draft is the read the publish needs.** It compiles what the next version would be and
throws it away, reporting whether it compiles, what it would carry, whether serving it costs a
history lookup per request, and what is being left out and why — because an archived flag and a
paused event are both absent on purpose.

#### Ingestion

`POST /v1/batches` with `Authorization: Bearer ps_project_live_...`. Answers 202 once the batch
is durably queued. `EVENT_BUS=local` writes an fsynced NDJSON spool under `EVENT_BUS_DIR`;
`EVENT_BUS=pubsub` publishes to Google Pub/Sub.

The body may be sent `Content-Encoding: gzip` or `zstd`, and an SDK should leave `event_id`
out and let the gateway derive it. Together those take a 500-event batch from 290 bytes an
event to 8.1 — see [ADR 0026](docs/adr/0026-compressed-json-and-derived-event-ids.md), and
`go run ./apps/backend/cmd/wiresize` to reproduce the numbers.

What a client must actually do — sessions, consent, the disk spool, retries, and what happens
when the player quits after eight seconds with no network — is
[docs/sdk/protocol.md](docs/sdk/protocol.md), implemented once in `sdk/core` and shared by
every binding. How a studio describes its game for targeting, and why geography is resolved
from the connection rather than sent by the client, is
[docs/sdk/context-and-targeting.md](docs/sdk/context-and-targeting.md).

#### Reporting

Two questions, and **two credentials that can ask them**.

For a machine, with `Authorization: Bearer ps_reporting_live_...`:

- `POST /v1/cohorts/query` — retention, playtime, revenue, LTV, payer conversion and
  behavioural segments, keyed on when a player arrived (§29).
- `POST /v1/metrics/query` — active players, sessions, events and revenue over time, keyed
  on when something happened (§26, §27).

For a person, with a Firebase ID token and `analytics.read`:

```
POST /v1/organizations/{org}/projects/{project}/analytics/{environment}/cohorts/query
POST /v1/organizations/{org}/projects/{project}/analytics/{environment}/metrics/query
```

The dashboard needs these because §52 makes a reporting key a secret and a browser cannot hold
one — shipping it to the page would publish every studio's analytics to anyone who opened the
network tab.

**The scope is still not the caller's to choose** (Appendix C #3). The project comes from the
path and is checked against that person's project access list *before* any permission — one they
were not given is "no such project", never a refusal — and the environment is looked up inside
it. The body's `project_id`, `environment_id` and `org_id` remain decoys that refuse the request.
The environment is in the path rather than pinned to the credential because a person switches
between development and production, where a key is minted for one.

**Reading a chart cannot create an account.** `core.ResolveKnownUser` withholds the account
creation `core.ResolveUser` performs on a first sign-in, so a valid token from a stranger
cannot bring a user row or a personal organization into existence as a side effect.

Both credentials read pre-aggregated facts and never join on a player key. Cells describing too
few players are suppressed rather than returned as zero, and every answer says how many were
withheld. A time series also says which summary tier answered it and whether its player count is
exact or a sketch. A person and a key asking the same question get the same numbers and the same
provenance — there is one planner, and a test compares the two answers row by row.

#### The dashboard's pages

Beside the two queries, one read per page, all under the same analytics path and all answered from
facts built for them ([ADR 0046](docs/adr/0046-every-page-reads-a-fact-built-for-it.md)):

```
GET   …/analytics/{environment}/events                       every event, its counts, players, properties
GET   …/analytics/{environment}/events/{name}                one event: series, splits, property values
GET   …/analytics/{environment}/events/{name}/occurrences    its newest occurrences            player.read
GET   …/analytics/{environment}/sessions                     lengths, endings, platforms, heatmap
GET   …/analytics/{environment}/sessions/recent              the newest sessions               player.read
GET   …/analytics/{environment}/players                      the directory of installs         player.read
GET   …/analytics/{environment}/players/{id}                 an install, or an account by id   player.read
GET   …/analytics/{environment}/revenue                      by day, product, store, country, price
GET   …/analytics/{environment}/purchases                    the ledger, a page at a time      player.read
GET   …/analytics/{environment}/flags/exposures              every flag read in a range
GET   …/analytics/{environment}/flags/{flag}/exposures       one flag by value, version and build
GET   …/analytics/{environment}/experiments/{flag}           each arm's outcomes over seven days
GET   …/analytics/{environment}/experiment-results/{id}      an experiment's arms over the days it ran
POST  …/analytics/{environment}/funnels/query                a steps or progression funnel
GET POST PUT DELETE …/funnels[/{id}]                          funnels saved by name, without a range
GET   /v1/organizations/{org}/analytics/{environment}/projects    every readable project, side by side
```

`…` is `/v1/organizations/{org}/projects/{project}`. The pages that count need `analytics.read` and
withhold any count of people under the suppression threshold, as the two queries do. The ones that
name a player need **`player.read`** as well, so a studio can hand somebody the numbers without its
players; Developer and Growth Manager are seeded with it. None of those is served for a
`kids_strict` project.

`metrics/query` also reads the whole-project minute and hour tiers (`activity_1m`, `activity_1h`),
splits a minute or hour series by `event_name`, and answers `continuing_players` and
`reactivated_players` by day; `cohorts/query` answers `metric: "event"`, carries sessions on every
row, and groups by the month.

Nothing here joins. Each fact is built by a view that aggregates inside the insert block, or by the
nightly derivation, and the questions that look like joins are answered by stamping, by a union
grouped by device, or by two bounded statements. The few reads of `events_raw` (occurrences, a
timeline, a funnel) run under tighter limits (`QUERY_RAW_*`), bounded by the sort key and a day
range; a funnel reads only its step events and decides each player with `windowFunnel`. Every
statement has a memory ceiling (`QUERY_MAX_MEMORY_USAGE`) and spills to disk before it; one that
reaches any limit is answered 422 with what to narrow.

#### The operations console

Everything under `/v1/operator` is for PlayerSense staff (§56, §104). Staff sign in with the same
Firebase project as customers, and are staff because an active row in `staff_members` says so; a
customer's valid sign-in reaches none of it. Each member holds some of four roles: **ops** (the
platform: ingestion, the queue, ClickHouse, storage), **support** (one customer's records),
**billing** (one customer's money, and the catalogue) and **admin** (all of it, and who is staff).

A route that reaches one customer's records (the directory included), or changes the catalogue, needs a ticket reference in
`X-Staff-Ticket` and writes a `staff_access` row (who, which customer, the ticket, when) before it
runs; `TestEveryCustomerRouteAsksForATicket` fails for one that forgets. Changes to an account are also
in the customer's audit log as **PlayerSense staff** with the verified address.

```
GET              /v1/operator/me              who you are to the console
GET   POST       /v1/operator/staff           admin: who is staff, and adding somebody
      PUT        /v1/operator/staff/{id}      admin: their roles, or deactivating them
GET              /v1/operator/staff-access    admin: every staff read of customer data
GET              /v1/operator/organizations   find one: slug, name, id, sequence or a member's address
GET              /v1/operator/organizations/{org}          its projects, members and account
GET              /v1/operator/projects        find one: slug, name, id or the ingestion page's sequence
GET              /v1/operator/projects/{id}   its environments, every key, 30 days of its meter
GET   PUT        /v1/operator/organizations/{org}/entitlements   overrides, merged; null clears one
GET              /v1/operator/organizations/{org}/billing        the wallet plus Stripe ids
GET              /v1/operator/organizations/{org}/ledger         the statement, paged with before
GET              /v1/operator/organizations/{org}/credit-lots    grants; all=true for spent ones
GET              /v1/operator/organizations/{org}/usage          the customer's usage page
GET              /v1/operator/organizations/{org}/audit          the customer's own log, filtered
GET              /v1/operator/accounts        billing: accounts suspended, overdue, declined or stuck
GET              /v1/operator/stripe-events   billing: every webhook and what became of it
GET              /v1/operator/stripe-events/{id}          one, with its body
POST             /v1/operator/stripe-events/{id}/replay   handle a failed or ignored one again
GET              /v1/operator/overview        the front page: pipeline, node, queue, worker, and accounts
GET              /v1/operator/ingestion       ops: every tenant's events, bytes, batches, lag
GET              /v1/operator/clickhouse      ops: replication, merges, mutations, parts, memory
GET              /v1/operator/storage         ops: bytes on disk by table and project
GET              /v1/operator/rate-limits     ops: every budget, who was refused by hour, a project's counters
GET              /v1/operator/queue           ops: each writer's unconsumed batches, their age, dead letters
GET              /v1/operator/jobs            ops: every worker pass, its failures, and the erasure,
                                              export and archive queues waiting on them
POST             /v1/operator/projects/{id}/archive-manifests/{m}/retry   hand a failed object back
```

The first admin is added from the command line, since nobody exists yet to add them:

```bash
go run ./apps/backend/cmd/staff add you@playersense.dev --roles admin
go run ./apps/backend/cmd/staff list
```

#### Data-subject requests

§101's surface, with
`Authorization: Bearer ps_server_live_...` — a server key, not a reporting one, because
erasing a studio's player is not the same privilege as reading its aggregates:

- `POST /v1/privacy/erasures` — records the request and tombstones the actor in the same
  round trip; removal runs in the worker over the following hours.
- `POST /v1/privacy/exports` — everything held about one actor, collected from
  `GET /v1/privacy/exports/{id}/download` as newline-delimited JSON.

A server key rather than a reporting one, because erasing a studio's player is not the same
privilege as reading its aggregates.

### analytics-writer — `:21082` (health probe only)

```bash
go run ./apps/backend/cmd/analytics-writer
```

Consumes the queue and stores batches in ClickHouse, stamping each one's cohort key so no
cohort query ever needs a join. It never touches PostgreSQL.

### archive-writer — `:21084` (health probe only)

```bash
go run ./apps/backend/cmd/archive-writer
```

Consumes the event stream on its own subscription and writes the canonical Parquet
archive, with a manifest per object in PostgreSQL. `ARCHIVE_STORE=local` writes under
`ARCHIVE_DIR`; `ARCHIVE_STORE=gcs` writes to the bucket.

### billing-writer — `:21088` (health probe only)

```bash
go run ./apps/backend/cmd/billing-writer
```

Charges tracked players as they arrive, on its own subscription: counts who is new this month,
queues what they cost, and settles the queue into wallets — within `BILLING_SETTLE_AFTER` (5 minutes), or
within seconds when a balance would cross zero or its top-up threshold
([ADR 0044](docs/adr/0044-usage-settles-when-it-matters.md)); the billing page's `live` balance already
takes off what is queued. It deletes player sets older than last month, and queue rows a year after they
settled; with `STRIPE_SECRET_KEY` set, it also charges saved cards for balances
below their automatic top-up threshold, and closes invoiced organizations' months into Stripe invoices
every `BILLING_INVOICE_EVERY` ([ADR 0045](docs/adr/0045-contracted-accounts-are-invoiced-monthly.md)). Needs PostgreSQL; Valkey only makes it faster — it answers
"already counted this month" and holds each organization's pricing, so a busy organization costs no
database reads to price. `BILLING_LOAD=1 go test ./apps/backend/internal/charging -run TestLoad -v`
pushes a million batches through four writers and checks every organization's books. Refuses to start without a
`BILLING_DIGEST_KEY` of 32 bytes or more — and that key must never change mid-month, or every player
of the month is new again and charged twice. Ingestion never waits for it
([ADR 0039](docs/adr/0039-players-are-charged-as-they-arrive.md)).

### worker — `:21086` (health probe only)

```bash
go run ./apps/backend/cmd/worker        # long-running: both loops, on their schedules
go run ./apps/backend/cmd/worker once   # one pass of each, then exit
```

Two loops on two cadences, because §79's list is two kinds of work.

**The derivation**, nightly at `WORKER_DERIVE_AT`. Rebuilds the cohort facts, their
denominators, the behavioural buckets, the daily/monthly metrics, complete sessions by day, the
lifecycle split of returning players and each experiment arm's outcomes: everything the
reporting routes serve that a view cannot build. Until it runs, every chart reads zero while ingestion is perfectly healthy.

Whole months, never a trailing window: each row of a cohort fact belongs to exactly one
activity-month partition, and `REPLACE PARTITION` swaps a partition wholesale. Denominators
are rebuilt before numerators, so a returner can never be divided by a cohort size computed
before they arrived. Running it twice produces the same tables.

**The lifecycle pass**, every `WORKER_JANITOR_EVERY`. Three jobs that are each waiting on
something, which is why they do not wait for the night:

- **Archive expiry** (§60) sweeps `delete_after` on the manifests, deletes the object and
  keeps the row — an object deleted on the twentieth is still billable for the twenty days it
  existed.
- **Erasures** (§61) purge one actor from `events_raw`, both device-keyed summaries, their
  purchases and the archive objects holding them. An archive object is *rewritten*
  without the person rather than deleted, because it holds an hour of everyone else too
  ([ADR 0025](docs/adr/0025-an-erasure-rewrites-the-archive.md)).
- **Exports** (§101) assemble one actor's data into a bundle in the archive bucket, and
  remove it when its term is up.

None of the three can stop the others: a bucket outage that blocks expiry must not block an
erasure with a legal deadline on it, so a pass reports every failure rather than the first.

`once` is what a scheduled Stage 2 deployment uses — a Cloud Run Job beats a process
holding a timer.

## SDK

The protocol is implemented **once**, in Rust, with no I/O of its own: sessions, consent,
batching, the disk spool, retry and backoff, deduplication and the response table. A binding
supplies a host — a clock, a file, an HTTP call — and a facade in its own idiom, so the same
core runs on every platform rather than being reimplemented five times and drifting.

```bash
cd sdk && cargo test                  # the protocol, and the ABI over it
cd sdk && ./build-native.sh           # the native libraries, and what they export
cd sdk/unity && ./install-plugins.sh  # put them in the Unity package
cd sdk/unity && ./verify.sh           # compile the package and run its tests, in real Unity
```

`sdk/unity` is the first binding. A game pastes its project key into Project Settings and
writes one line:

```csharp
PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12 });
PlayerSense.Context.Player.Set("rank", "diamond");
```

No `await`, no callback, no result to check, nothing to wrap in `try`/`catch`
([sdk/unity/README.md](sdk/unity/README.md)). The native libraries are not committed — an
unstripped iOS archive is 21 MB against ~550 KB of linked code — so a checkout builds them
with the two scripts above.

## Storage tiers

`events_raw` and `player_activity_daily` sit on a **hot/cold storage policy**, so the §33
hot window is cache residency rather than a retention limit: old parts move off local NVMe
and stay queryable in place. Retention is unchanged and still per project — `expire_at` is
derived from the row's own retention tier.

| Table | Moves to cold after | Why |
|---|---|---|
| `events_raw` | 7 days | The largest table; nothing on the dashboard path reads it |
| `player_activity_daily` | 90 days | §33.1's other big one. Three times clear of the derivation's two-month rebuild window |
| `player_lifetime` | never | `player_lifetime_v` is on the query path (§29.10) |

```bash
# which volume each partition is on
docker exec playersense-clickhouse clickhouse-client --port 9000 \
  -u playersense --password playersense_local -d playersense \
  -q "SELECT partition, disk_name, sum(rows) FROM system.parts
      WHERE table='events_raw' AND active GROUP BY partition, disk_name ORDER BY partition"
```

Locally `cold` is a second directory. In production it is a GCS bucket — see
`infra/stage1/compose/clickhouse/config.d/storage.xml`, which carries the disk definition
and reads its credentials from the environment. See [ADR 0024](docs/adr/0024-the-hot-window-is-a-cache-not-a-retention-limit.md).

## Tests

```bash
go test ./apps/backend/... -race
cd sdk && cargo test                      # the SDK protocol and its C ABI
cd sdk && cargo clippy --all-targets      # --all-targets, or the test fixtures go unlinted
cd sdk && ./build-native.sh               # native libraries, and what they export
go run ./apps/backend/cmd/cohortcheck     # 15 correctness checks against real derived data
go run ./apps/backend/cmd/archivecheck   # Appendix C.6: is anything about to expire unarchived?
go run ./apps/backend/cmd/wiresize        # what a batch weighs, four ways
```

Miri covers the C ABI's pointer and handle code:

```bash
cd sdk && cargo +nightly miri test -p playersense-ffi --test abi -- \
  --skip a_host_that_calls_back --skip a_spooled_batch_survives --skip tracking_survives \
  --skip a_dead_endpoint --skip a_202_reaches --skip context_set_through --skip a_401_stops \
  --skip a_batch_is_spooled --skip track_reports_consent_outcomes
```

The skips are the nine tests that compress a batch — gzip under an interpreter takes longer
than the run is worth, and the unsafe code they add over the rest is one struct construction.

`cargo test` runs the protocol conformance suite — every scenario in
[docs/sdk/protocol.md](docs/sdk/protocol.md) §13 against a fake host, so "the player quits
after eight seconds with no network" and "the process is killed between spooling and the 202"
are deterministic tests rather than a device farm. The ABI suite drives the same core through
the real `extern "C"` symbols, which is the half a binding can get wrong.

A binding's own tests cover only what is left: the seam. `sdk/unity/verify.sh` builds a
throwaway project around the package, compiles it and runs four play-mode tests — that the C#
`[StructLayout]` declarations still match the native library, that the host's delegates survive
a garbage collection, that the installation identifier lives where the platform will not back
it up, and that every call is safe before the SDK has started. Each of those fails on a device
and nowhere else, and none of them is reachable from Rust.

The layout one is the one to keep honest: add a field to `Native.PsConfig` and it fails with
*"PsConfig: the binding says 272 and this binary says 264"*, and the SDK refuses to start
rather than writing past the end of a struct.

## Continuous integration

`.github/workflows/ci.yml`, on every push. Four jobs, because they fail for four different
reasons: the Rust protocol and its ABI (fmt, clippy `--all-targets`, tests, and the
header-drift check), Miri over the boundary code, Go against the real stack in Docker, and a
native matrix across macOS, Linux and Windows runners.

That last job is the one that changes what can ship. Windows, Linux and Intel macOS cannot be
built on an Apple-silicon developer machine — no MSVC toolchain, no Linux linker — so until
now those three libraries did not exist and the Unity package could not ship to PC at all.
See [docs/sdk/platforms.md](docs/sdk/platforms.md).

**What CI does not cover: Unity.** `verify.sh` needs a Unity editor and a licence, and the
play-mode tests need the native plugin for whichever platform the runner is. It is run by hand
before a release, and a workflow that had never executed would be worse than admitting that.

`cohortcheck` seeds synthetic events with a known answer and asserts the pipeline reproduces
it. It is the independent second opinion on every number the reporting API serves.

`archivecheck` compares what ClickHouse still holds against what the archive has, and fails
when a day would reach its TTL with no archive. Nothing can *enforce* that ordering — a
ClickHouse TTL is a declarative instruction to a background merge, with no hook to wait on —
so the invariant is held by margin, and this is what measures it.
