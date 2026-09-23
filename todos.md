# PlayerSense — Frontend build todos (Landing, Client, Admin)

## Current state (as of this writing)

The backend is the mature part of this repo: four Go binaries (`api`, `analytics-writer`,
`archive-writer`, `worker`) built on the Muzak framework, one PostgreSQL database, ClickHouse
for analytics,
Valkey for ephemeral state, GCS for archives, and a Rust SDK core shipping today as a Unity
package. 37 ADRs and an 11-file compliance dossier record *why* it's built this way, and
`docs/compliance/10-open-items.md` is an accurate, current list of what is designed but not
yet built.

**Frontend scaffolding now exists.** `apps/landing`, `apps/client` and `apps/admin` are
scaffolded Nuxt 4 + Nuxt UI v4 apps (client/admin from the official `ui/dashboard` template,
landing from `ui`), wired into a pnpm workspace with a shared `packages/ui` Nuxt layer (the
`Logo`/`LogoMark`/`PagePlaceholder` components and the `/favicon.svg` + app-icon set). Every
nav item from `project.md` §55/§56 has a real route; each renders an honest placeholder — a
neutral "not wired up yet" alert, or an amber "blocked on backend" one with the specific
gap cited — rather than fake data. All three typecheck and lint clean and were verified
booting in a real browser (light + dark mode, org/team page with the six real seeded roles).
No API wiring, auth, or real page content beyond that exists yet — that is what the rest of
this file tracks. Section numbers below (`§NN`) refer to `project.md`.

Read before starting: `README.md` (how to run the local stack and every service), `project.md`
Part XIII (§54–56, the app specs), §77 (suggested monorepo layout), §93 (tech stack),
Appendix B (hostnames), and `docs/compliance/10-open-items.md` (ground truth on backend gaps —
check it before assuming an endpoint exists).

---

## 0. Shared foundation (do first — blocks all three apps)

- [x] Add root `package.json` + `pnpm-workspace.yaml` per §77's suggested layout; keep
      `go.work` untouched, it only concerns `apps/backend`.
- [ ] `packages/eslint-config` — shared lint/format config for the three apps. Each app
      currently has its own `eslint.config.mjs` (identical, from the Nuxt UI template);
      not yet deduplicated into a shared package.
- [x] `packages/contracts` — TypeScript types generated from the API's own OpenAPI
      document, with `openapi.json` and `src/schema.d.ts` both committed so the apps need no
      build step and a reviewer sees a contract change in the diff that caused it.

      The document comes from `go run ./apps/backend/cmd/api -openapi`, a flag added for
      this, rather than from a running server — the application builds and prints its
      description without opening a connection, so the drift check needs no stack and runs
      on every push. `pnpm contracts:check` regenerates in memory and fails on any
      difference; it was verified to fail by adding a response to a route and passing again
      once reverted.

      Helpers are keyed on path and method (`ResponseOf<'/v1/batches', 'post'>`), not on
      operation ids, because Muzak derives ids from the route
      (`post_v1_organizations_by_org_projects`) and naming one in a page means a route
      rename fails with a message about a missing id. `MethodsOf` narrows the method to the
      verbs a path serves; without it `ResponseOf<'/v1/batches', 'get'>` resolved to a
      quietly useless type instead of an error. `src/assertions.test-d.ts` pins all of this
      at `tsc --noEmit`.

      Added as a `workspace:*` dependency of `client` and `admin`, and verified to resolve
      and typecheck from a page. **No client is generated** — auth, retries and error
      handling are one decision the apps should make together in a fetch wrapper, which is
      the next item below.
- [ ] `packages/analytics-query-dsl` — typed builders/validators for the §57 query DSL
      (metric queries and the distinct §57.1 cohort-query shape), shared by `client` and any
      future internal BI tooling.
- [x] `packages/ui` — built as a Nuxt layer (`extends: ['../../packages/ui']`), not an npm-style
      built package: its `components/` (`Logo`, `LogoMark`, `PagePlaceholder`) and `public/`
      (favicon set) merge into all three apps automatically, zero build step. Holds the brand
      source assets too (`assets/brand/mark.svg`, `favicon.svg`, `tile.svg`).
- [x] Install and configure Nuxt UI + Tailwind across the three apps (decided in §6 — `project.md`
      mandates Nuxt only, not a component kit, so this was ours to make). `primary`/`neutral`
      both set to `neutral` in each app's `app.config.ts` for the monochrome brand.
- [ ] Firebase Web SDK config (public config only — project ID, API key — never the service
      account already in `.env`'s `FIREBASE_PROJECT_ID`/`FIREBASE_CERTIFICATE_URL`) for the
      three environments, shared across `landing`/`client`/`admin`.
- [x] **CORS** — `cmd/api` now carries a policy, origins from `CORS_ALLOWED_ORIGINS`
      (comma-separated, exact match including scheme and port), defaulted to the three local
      dev servers and overridden per deployment. `apps/backend/cmd/api/cors_test.go` proves
      the allow, the refusal, and both preflight directions.

      Two things came out differently from how this item assumed:
      **`AllowCredentials` is off.** It governs cookies and TLS client certs, not the
      `Authorization` header — that one is an ordinary request header already in Muzak's
      default `AllowedHeaders`. Both apps hold the Firebase token in client state and attach
      it themselves, so turning it on would buy nothing and widen what an origin-list mistake
      costs. **`ExposedHeaders` was the missing piece nobody had written down:** browsers hide
      every non-safelisted response header, so without it a 429's `Retry-After` is invisible
      to the dashboard and a rate limit becomes a retry storm. `Retry-After`, `X-Request-Id`,
      `ETag` and `Content-Disposition` are exposed.
- [ ] Settle hostnames against Appendix B (`playersense.io`, `app.playersense.io`,
      `admin.playersense.io`, `api.playersense.io`, etc.) — none are registered/deployed yet.
      Deployment *model* is decided in §6 (independent Nitro servers on Cloud Run, not
      Muzak's `Router.Frontend`).
- [x] Extend `.github/workflows/ci.yml` with a Node/pnpm job — added as `node`, running
      the contract drift check first (a stale contract is the likeliest reason the apps stop
      typechecking, and the clearer message to fail with), then lint, typecheck and build.
      It starts no services: the only Go it runs is `cmd/api -openapi`, which dials nothing.
      There is no `test` step yet because the apps have no tests.
- [ ] Decide token plumbing: Firebase ID token as `Authorization: Bearer` on every
      `api` calls from `client`/`admin`; refresh-on-expiry. `client` and
      `admin` are already set `ssr: false` (both `nuxt.config.ts`) so the token lives in
      client-only auth state, not something SSR has to handle — that half is settled; the
      actual Firebase wiring and request interceptor are not.

---

## 1. `landing` (§54)

Public app: marketing, pricing, product pages, public docs entry points, SDK install guides,
status/legal links, signup/login entry. **Must not contain privileged admin logic.**

- [x] Scaffold Nuxt app at `apps/landing` (Nuxt UI's `ui` template).
- [x] Home / marketing page — hero, feature grid grounded in real capabilities (analytics,
      flags & rollouts, LiveOps, privacy-by-schema, scale, SDK), CTA. No invented pricing,
      testimonials or customer logos.
- [ ] Pricing page reflecting §36–38 (MAU as primary meter, free tier, promo credit). The
      numbers are no longer missing: render `GET /v1/pricing` (public, no credential) — every plan
      on sale, its included players, the tiers in force (`from`/`up_to` positions and a
      `unit_price` already rendered below a cent, e.g. `$0.0005`) and any scheduled change. Never
      hardcode a price in the page; staff change them as rate cards (ADR 0039).
- [ ] Product pages: analytics, feature flags & LiveOps. Hold off on purchase-verification
      and webhooks marketing copy until those ship server-side (§90, not built — see §4
      below), or caveat them as "coming soon."
- [x] SDK install guide (`/docs/sdk`) — Unity is the only shipping binding today (`sdk/unity/README.md`,
      `CHANGELOG.md`, package at `0.1.0` **unreleased**). Publish the platform matrix honestly
      per `docs/sdk/platforms.md`: shipping today are iOS, iOS simulator, Android (arm64
      device-tested, arm32, x86_64) and Apple-silicon macOS; **not yet shipping** are Windows
      x64, Linux x64 and Intel macOS (blocked on CI runners, not code — the one that matters
      most, since Unity+Steam is Windows-first); WebGL and consoles are not attempted. Leave
      clearly marked placeholders for Unreal/Godot/native-iOS/native-Android/JS per the `sdk/`
      layout §77 plans — none of those bindings exist yet.
- [ ] Public docs entry point — nothing public-facing is generated today; the only docs are
      internal markdown (`docs/sdk/protocol.md`, etc.). Decide whether `docs.playersense.io`
      (Appendix B) is a generated site from these sources or a separate effort; out of scope
      for `landing` itself beyond the entry link.
- [ ] Status page link (`status.playersense.io`, Appendix B) — no status infra exists; link a
      placeholder or third-party tool for v1.
- [ ] Legal pages (privacy policy, terms). Flagged explicitly in
      `docs/compliance/10-open-items.md` under "Contractual" as blocking launch: "Privacy
      policy and public documentation of what the SDK collects."
- [ ] Signup entry — Firebase Auth creates the user; the backend already provisions the row +
      personal organization automatically. `identity.EnsureUser`
      (`apps/backend/internal/identity/identity.go:94`) is called from the auth-resolution path
      in `internal/core/principal.go:179` that every authenticated dashboard request goes
      through, so opening the dashboard for the first time is enough — no dedicated
      signup-completion route is needed. `core.ResolveKnownUser` is the read-only counterpart
      used by the analytics routes and deliberately does not create anything (§55.2).
- [ ] Login entry — Firebase sign-in, redirect into `client`.
- [ ] Recovery/password-reset flow must not disclose whether an account exists (§55,
      `docs/rate-limits.md` §6), and auth-email buttons need the 60-second cooldown described
      in §5 below.

---

## 2. `client` (§55) — studio-facing dashboard

### General
- [x] Scaffold Nuxt app at `apps/client` (Nuxt UI's `ui/dashboard` template — ships with
      Unovis for charts, TanStack Table, Zod; see the corrected charting decision in §6).
- [ ] Auth shell: Firebase sign-in, organization/project switcher, environment switcher
      (development/staging/production, §75–76).
- [ ] Wire `packages/contracts`-typed clients to `api` (`:21080`), carrying the Firebase ID
      token.
- [ ] Respect Appendix C #3: a project outside the caller's access list comes back as
      "no such project," never a 403 — don't let the UI infer or display anything about
      projects a person wasn't granted.
- [x] ~~Handle the 421 (wrong-cell) response by following the returned address~~ — removed
      with the cell architecture (ADR 0038). Nothing answers 421 any more (§6,
      Appendix B) rather than treating it as a generic error.
- [ ] The dashboard is deliberately **not** rate-limited except invitations
      (`docs/rate-limits.md` §1) — don't build client-side throttling anywhere else.

### Ready to build now — backend exists
- [ ] **Organization / Team** (§55.1) — members, roles, invitations, the two-axis role +
      project-access model (`all_projects` as its own explicit grant, never implied by a
      missing field). Enforce in the UI: last owner can't be removed/demoted; removing a
      member is a hard delete, not a disable; role/project-access changes require owner/admin
      and are separate requests.
- [ ] Invitation flow — surface the exact budgets from `docs/rate-limits.md` §1: **3/hour per
      address, 30/hour per organization**, one outstanding invitation per address per
      organization, and this route fails **closed** (a stuck rate-limit check refuses rather
      than allows). Show `delivered: false` honestly when `MAIL_SENDER=log` — see §5.
- [ ] **API Keys** — create/list/revoke per project across the three key classes (`project`,
      `reporting`, `server`, §52). Make unmistakable that `reporting`/`server` keys are secrets
      shown once, never to be embedded in a browser.
- [ ] **Feature Flags** — CRUD against `/segments`, `/flags`, `/liveops-events`, plus the
      draft/publish flow (`config/{environment}/draft`, `.../publish`). Surface write-time
      validation errors inline (unknown segment reference, cycle, type mismatch — ADR 0034
      means these are caught at write time, not just publish time). Gate the publish action in
      the UI on `liveops.publish`, not just on the write permission.
- [ ] Rule-builder UI for segment/flag targeting must visibly label each field's trust tier
      (`docs/sdk/context-and-targeting.md`): server-derived (`geo.*`, `network.*` — not
      forgeable), SDK-generated (`app.*`, `device.*`, `sdk.*`, `install.*` — forgeable only
      about itself), studio context (`context.*` — forgeable by a modified client), and account
      context (`account.*`, `commerce.*` — not forgeable). Don't let a studio build a
      value-granting rule (e.g. unlocking paid content) on a client-forgeable field without a
      visible warning.
- [ ] **Segments** — same CRUD + validation path.
- [ ] **LiveOps** (scheduled events) — same CRUD + publish-gating path.
- [ ] **Data Retention** — raw vs. aggregate retention explainer; §59 explicitly requires
      "product UI must explain the difference clearly."
- [ ] **Billing** (§55.3) — usage page over `usage_daily` (tracked players reported as
      *busiest day*, not a sum — don't sum it client-side either); wallet showing both
      `available_micros` and `ledger_balance_micros` with an explanation of why they can
      briefly diverge (lapse vs. sweep); ledger/lot list distinguishing promotional (expiring)
      from purchased (never-expiring) credit; auto-topup toggle. No path here for a customer to
      grant themselves credit — that's operator-only (see Admin). Charging is real time now
      (ADR 0039): `GET …/billing` carries `month` — the exact tracked players this month (not a
      peak), what they cost, what is still queued (seconds old), the rate card and the band the
      next player lands in — and `arrears_micros`, usage the wallet could not cover. Show arrears
      prominently and say plainly that ingestion is unaffected.
      Paying (ADR 0040): "Add credit" is `POST …/billing/top-ups` → redirect the browser to the
      returned `url`; "Manage cards" is `POST …/billing/portal` → redirect likewise. On return
      (`?checkout=success`) do **not** show the credit as arrived — poll `GET …/billing` for a few
      seconds until the webhook's lot appears, and say "payment received, crediting…" meanwhile.
      Plan (ADR 0042): show the plan, its features and `subscription` (status, interval,
      period_end, cancel_at_period_end); "Upgrade to Enterprise" is `POST …/billing/subscription`
      with `month` or `year` → redirect; "Manage subscription" is the portal. Read Enterprise's
      prices and features from `GET /v1/pricing`, never hardcoded.
      Auto top-up form: no card field; a 422 saying there is no saved card means "pay once or add a
      card in the portal first". Show `auto_topup.pending`, `queued`, `last_error`, and
      `used_today` of `daily_limit` (3 by default, ADR 0041).
- [ ] **Audit log** (§55.4) — read-only, gated on `audit.read` (only Owner/Admin among the six
      seeded roles). Render before/after state on role and rollout changes; label
      operator-initiated entries as self-asserted, per the backend's own labeling.
- [ ] **Project Settings** — metadata, environments; show `policy_tier` as read-only
      (fixed at creation, §75 — never editable in place).

### Ready to build now — analytics via the person-auth routes (§55.2)
- [ ] **Overview** — headline metrics from `/analytics/{env}/metrics/query`.
- [ ] **Cohorts** — retention (classic + rolling), playtime, revenue/LTV, payer conversion,
      one-dimension breakdowns (never two — the compiler refuses it, §57.1). Every chart must
      render `cohort_maturity_days` and `suppressed_cells`/`suppression_threshold` — a bare
      number here can misread as a true zero.
- [ ] **Revenue** — same query surface, revenue-focused views.
- [ ] Loading/latency states appropriate to §80's targets (~1s for pre-aggregated charts, ~3s
      for heavier queries) — caching itself is server-side (Valkey, §58), the client just needs
      to handle real latency, not implement its own cache.
- [ ] `kids_strict` project handling: cross-day retention is **not computable** under that tier
      (§98) — show an explicit explanation, not an empty chart, when a project is in that tier.
- [ ] **Players** — scope this page carefully; there is no dedicated player-detail/timeline
      endpoint yet (see §4). Confirm what's actually queryable before committing UI to it. When
      it exists, explain the `install_id` vs. studio-supplied `actor_key` distinction in the UI:
      `install_id` is a random UUID reset on reinstall (ADR 0004), so a reinstalled player can't
      be linked to their prior history without the studio's own `actor_key` — surface this as an
      honest limit anywhere player lookup is offered (support tooling, data-subject requests),
      not as a bug.
- [ ] **Sessions** — DAU/session counts are available from `metrics_daily`-backed queries;
      session **duration percentiles are not** (needs `sessions_final`/§30 sessionization,
      unbuilt per `docs/compliance/10-open-items.md`). Ship counts/totals only for v1.
- [ ] **Realtime** — confirm whether `/metrics/query` supports a genuinely near-real-time
      interval before committing to this page; it may be blocked on something not yet exposed.

### Blocked on backend work not yet built
Tracked as ground truth in `docs/compliance/10-open-items.md` and cross-checked against
`apps/backend` (no matching internal package/handler/router exists for any of these):
- [ ] **Events** (event explorer) — no raw-read query path exposed yet (§3.5, §31).
- [ ] **Funnels** (§31) — not built server-side.
- [ ] **Player timeline** — not built.
- [ ] **Experiments** — no experiment/exposure concept in the backend at all: `grep -rli
      experiment apps/backend` only turns up comments in `internal/targeting/bucket.go` and
      `internal/liveconfig/evaluate.go` explaining the mechanism a future experiment would use.
      Deterministic percentage rollout and audience bucketing exist (`internal/targeting`,
      `internal/liveconfig`) and are the primitive an experiment would sit on; variant-assignment
      tracking and exposure analytics (§44, §91) do not exist.
- [ ] **Purchases** — the `verifiedpurchase` ent entity exists (schema only,
      `apps/backend/ent/schema/regional.go`); confirmed by grep that zero handler or
      router code touches `VerifiedPurchase`/`VerifyPurchase` anywhere. No receipt-verification
      logic against Apple/Google, no normalized-purchase-event ingestion path. Nothing for this
      page to call yet.
- [ ] **Webhooks** — no outbound customer-webhook package, handler, router, or transactional
      outbox table exists (`grep -rli outbox apps/backend` is empty). The only webhook-adjacent
      code is (a) comments confirming the ingestion routes deliberately call none, (b) `StripeEvent`
      (`ent/schema/billing.go`) — an *inbound* record of Stripe's webhooks for
      billing, unrelated to §49's outbound customer webhooks, and (c) `MaxWebhookEndpoints`, an
      entitlement constant (`internal/billing/billing.go:63`) with no CRUD or delivery logic
      behind it. Phase 2, §90.
- [ ] **Exports** — `internal/export` (bundle assembly) and `/v1/privacy/exports`
      exist, but only for a **server key** (a studio's own backend), not a dashboard session.
      There is no dashboard route for a signed-in member to request/download their own
      project's export. Confirm intended scope (see the open decision in §6) before building.
- [ ] Data-subject-rights UI (§101) in general — same gap as Exports: the existing
      `/v1/privacy/*` routes are designed for server-to-server use, not a browser session.

---

## 3. `admin` (§56) — internal PlayerSense ops console

This is explicitly the least-built surface in the whole system —
`docs/compliance/10-open-items.md`: *"Admin console with per-read reason capture ... nothing
writes it yet."* Treat almost every item here as backend-and-frontend work together, not
frontend-only, and do the staff-identity item first — everything else depends on it.

- [x] Scaffold Nuxt app at `apps/admin` (also `ui/dashboard` — same shell, its own nav).
- [ ] **Staff authentication + the `staff_access` model (prerequisite for everything below).**
      Nothing exists yet: no way to distinguish PlayerSense staff from customer accounts, no
      `staff_access` table recording actor + customer + reason per read
      (§104), no routes that accept staff credentials. §56 requires "a separate authorization
      scope," and §104 calls the admin console "the highest-risk surface in the system."
- [ ] Per-read reason-capture UI — every staff lookup of customer data must require a ticket
      reference (not free text) before the read is granted (§104, `docs/compliance/08`).
- [ ] Organization lookup.
- [ ] Project lookup.
- [ ] Entitlement overrides.
- [ ] Billing inspection (can likely reuse the billing routes under staff scope
      once that exists, rather than the member scope `client` uses).
- [ ] **Plan features and subscription prices** (ADR 0042): a features checklist on the plan form
      (`PUT /v1/operator/plans/{plan}`) and monthly/yearly price fields
      (`PUT /v1/operator/plans/{plan}/subscription`), with a note that a changed price applies to new
      subscribers only.
- [ ] **Plans & prices** over the operator routes (ADR 0039): list/create/edit plans
      (`/v1/operator/plans`), publish rate cards (`/v1/operator/plans/{plan}/rate-cards`) with a
      tier editor that shows `from`–`up_to` positions and prices below a cent, the price history
      marked current/scheduled/superseded, and moving an organization onto a plan
      (`PUT /v1/operator/organizations/{org}/plan`). There is deliberately no "edit price" —
      publishing a card *is* the edit, and it cannot start in the past. Same shared-token caveat
      as credit grants below.
- [ ] Automatic top-up daily limit per organization (`auto_topup_daily_limit`, default 3, ADR 0041)
      — editable only in the database today; needs an operator route and a field here.
- [ ] Credit grants/charges UI over the existing operator routes
      (`POST /v1/operator/organizations/{org}/credits`, `.../charges`). Today these sit behind
      a **shared operator token**, not a verified staff identity — `granted_by_user_id` is
      null and audit entries are self-asserted (`docs/compliance/10-open-items.md`). Decide:
      ship against the shared token as an interim step, or block on verified staff identity
      first (see §6).
- [ ] Ingestion volume dashboard.
- [ ] Queue backlog view (Pub/Sub in production, the local NDJSON spool in dev).
- [ ] ClickHouse health view.
- [ ] Storage usage view.
- [ ] Retention jobs view — the worker's janitor loop (archive expiry, erasures, exports;
      README "worker" section) — surfaced per-job with failures visible (the janitor
      deliberately reports every failure rather than stopping at the first).
- [ ] Webhook failure inspection — blocked entirely until the webhook system itself is built
      (§90, tracked in §4).
- [ ] Purchase verification debugging — blocked entirely until purchase verification is built
      (§90, tracked in §4).
- [ ] Abuse / rate-limit controls — inspection (and override, if wanted) of the existing
      Valkey-backed limits in `docs/rate-limits.md`.
- [ ] Support tooling — a "view as" mode for debugging a customer's dashboard state, itself
      going through `staff_access` and reason capture like every other read here.
- [ ] Audit access — staff's view into customer audit logs, plus (a distinct, separately
      flagged gap) a **staff access review**: who on our side can reach the deployment, and when —
      not the same thing as a customer's own member list.
- [ ] Verify the separate authorization scope end-to-end at the **routing layer** — a guard on
      every route, not just hidden nav links — since §56 is explicit that this must never be
      reachable merely by knowing the URL.

---

## 4. Backend work this frontend effort will surface

Track alongside the frontend work above; none of it blocks `landing`, but most of `client`'s
"blocked" section and nearly all of `admin` depend on these.

- [x] ~~CORS configuration on `api`~~ (§0) — done; see §0 for what differed from the plan.
- [ ] `staff_access` table + staff authentication — blocks all of `admin`.
- [ ] Verified staff identity for the operator credit/charge routes (currently shared-token
      only).
- [ ] Webhooks system (§90) — blocks `client`'s Webhooks page and `admin`'s failure inspection.
- [ ] Purchase verification (§90) — blocks `client`'s Purchases page and `admin`'s debugging
      view.
- [ ] Experiments + exposure analytics (§91) — blocks `client`'s Experiments page.
- [ ] Event explorer / raw-read query path, funnels, player timeline (§3.5, §31) — blocks
      `client`'s Events, Funnels, and Players pages.
- [ ] Session duration percentiles / `sessions_final` sessionization (§30) — blocks the full
      Sessions page.
- [x] ~~Stripe, as the wallet's funding source~~ — built (ADR 0040): Checkout top-ups, the
      customer portal for cards, signed webhooks, automatic top-up off-session, refunds and disputes
      taken back (never promo credit) and restored when a refund fails or a dispute is won. Still
      open: tax (Stripe Tax, once registrations exist), credit notes on refunds, failure emails,
      and the live-mode portal configuration in Stripe's dashboard. Every top-up is invoiced.
- [ ] Decide what happens after the grace period (§40.1): how long `grace_period` lasts, what
      `restricted` restricts, and what cancellation does to retained data. Nothing escalates past
      `grace_period` automatically until this is decided.
- [ ] Enterprise features — premium Slack support, the daily Slack report builder, the MCP server —
      each gated on `billing.HasFeature(plan.Features, …)` (ADR 0042).
- [ ] Realtime metrics endpoint — confirm scope before `client`'s Realtime page is built.
- [ ] A person-authenticated (not server-key) data-subject-rights route, if `client` is meant
      to expose export/erasure requests directly rather than delegating to a studio's own
      backend integration.

---

### Stripe — still open (ADR 0040, 0041)

Built: Checkout top-ups and the customer portal, signed webhooks, automatic top-up (queued by the
charge, 3 a day), refunds and disputes taken back, and an invoice for every top-up. What is left:

- [ ] **Tax.** Stripe Tax is off until tax registrations exist. Turning it on does not change the
      top-up flow: Checkout and the automatic-top-up invoices both take `automatic_tax`.
- [ ] **Credit notes.** A refunded top-up's invoice stays "paid"; no credit note is issued against it.
- [ ] **Failure emails.** A declined automatic top-up and the daily limit only show on the billing
      page (`auto_topup.last_error`). Nobody is emailed, so an account can sit in arrears unnoticed;
      and a card that needs 3-D Secure has no flow that sends the customer to confirm it.
- [ ] **Stripe customer sync.** Renaming an organization or changing its billing email does not update
      its Stripe customer; a customer deleted in Stripe's Dashboard leaves a stale pointer.
- [ ] **Replaying failed webhooks.** Stripe retries a failed delivery for about three days; after that
      the event stays `failed` in `stripe_events` with its reason, and nothing reprocesses it — a job,
      or an admin screen with a replay button.
- [ ] **Subscription events on the endpoint.** Add `customer.subscription.created`,
      `customer.subscription.updated` and `customer.subscription.deleted` to the Dashboard webhook —
      without them a paid Enterprise subscription never moves the organization onto Enterprise.
- [ ] **The portal for subscriptions.** Configure Stripe's customer portal to allow canceling and
      switching between Enterprise monthly and yearly.
- [ ] **Two subscriptions.** Two Checkout tabs completed can leave a studio billed twice; the newer is
      its plan. Support cancels the older in Stripe — or a webhook rule could cancel it automatically.
- [ ] **Production setup.** Live keys in production secrets; a Dashboard webhook endpoint for
      `/v1/stripe/webhook` with its signing secret as `STRIPE_WEBHOOK_SECRET`, its API version pinned
      to `2026-04-22.dahlia`; the live-mode customer portal configuration saved once.
- [ ] **Minor.** USD only. No refund of unused purchased credit when a studio leaves. The Stripe SDK
      prints its own error lines to stderr; they should go through the service's logger.

---

## 5. Compliance/UX details that apply across all three apps

- [ ] Never imply an email was sent when `MAIL_SENDER=log` is active — show `delivered: false`
      plainly (README, §55.1).
- [ ] State the `kids_strict` retention trade-off (no cross-day retention, ever) at the point a
      studio *chooses* the tier during project creation, not after they notice an empty chart
      (§98's explicit requirement).
- [ ] Every cohort/retention chart renders suppression + maturity provenance; never show a
      number that could be misread as a genuine zero (§57.1, §32.2).
- [ ] Password-reset/recovery responses must not disclose whether an account exists (§55,
      `docs/rate-limits.md` §6).
- [ ] 60-second cooldown on auth-email buttons (verification resend, password reset) as an
      interface default layered on top of Firebase's own server-side protections — not a
      replacement for them (§55, `docs/rate-limits.md` §6).
- [ ] Disable duplicate submission on every auth-email action while a send is pending, and
      refresh the Firebase user/ID token after confirmation so the backend sees the updated
      verified-email claim (§55).

---

## 6. Decisions made, so they don't get relitigated per-PR

- [x] **Component library: Nuxt UI + Tailwind** (§0). Two of the three apps are data-dense
      authenticated dashboards that need to ship fast; Nuxt UI's components are built on
      Tailwind + Reka UI and cover the data-grid/form/modal vocabulary `client` and `admin`
      both need without a second design system.
- [x] **Charting library: Unovis** (`@unovis/ts` + `@unovis/vue`), superseding an earlier draft
      of this decision that picked ECharts before `client` was scaffolded. Nuxt UI's own
      `ui/dashboard` template — which `client`/`admin` are built from — ships Unovis as its
      chart layer already (see `apps/client/package.json`), so it's the better-supported,
      zero-extra-dependency choice: no competing charting library to wire theme tokens into.
      Handles time series and heatmaps; still needs its own verification for the cohort/
      retention pages' specific annotation needs (suppression thresholds, maturity boundaries)
      when those are actually built.
- [x] **Data fetching: Nuxt's built-in `useFetch`/`useAsyncData`** over the generated
      `packages/contracts` client, not a separate query library. Muzak's own backend philosophy
      is zero third-party dependencies where avoidable; matching that on the frontend keeps one
      fewer moving part. Revisit only if cache invalidation across the org/project/environment
      switcher becomes genuinely painful with the built-in composables.
- [x] **Deployment model: independent Nitro servers per app on Cloud Run**, not static builds
      embedded in a Go binary via Muzak's `Router.Frontend`. `project.md` line 7 calls these
      "three independent applications" and §93/§87 Step 4 both assume separate deployables; this
      codebase has been consistently disciplined about service boundaries everywhere else —
      the writers and the worker are still their own binaries even after the four HTTP
      services merged (ADR 0038) — and folding frontend assets into a Go service binary would
      be the one place that discipline breaks.
- [x] **Brand mark, wordmark and favicon.** The mark (`packages/ui/assets/brand/mark.svg`) is
      the Solar icon-set radar mark by 480 Design, used as-is under CC BY 4.0 — an open ring
      with a hand sweeping to the centre and an inner ring, as a single filled path in
      `currentColor`, so `Logo`/`LogoMark` track `text-neutral-950 dark:text-neutral-50`
      automatically. The attribution comment stays in every source file that carries the path
      (`mark.svg`, `favicon.svg`, `tile.svg`, `LogoMark.vue`); if the licence ever needs a
      visible credit, the landing footer is the place. Rasters are rendered from
      `favicon.svg` (32/48, rounded badge, transparent corners, mark at ~60% of the badge) and
      `tile.svg` (180/192/512, square) with resvg. Wordmark set in lowercase
      ("playersense") in Space Grotesk, loaded via `@nuxt/fonts`' zero-config Google Fonts
      detection — note it only detects `font-family` in real CSS/`<style>` blocks, not inline
      `style=""` attributes, which is why `Logo.vue` uses a scoped `<style>` block rather than
      an inline style. Favicon is a fixed dark rounded badge (`#0a0a0a` bg, `#fafafa` mark) —
      deliberately not adaptive to OS theme, unlike the in-app logo, matching how most product
      favicons stay one consistent brand color regardless of dark mode. Shipped as
      `favicon.svg` + PNG fallbacks + `apple-touch-icon.png` + `site.webmanifest`, no legacy
      `.ico` (full SVG/PNG support across evergreen browsers as of 2026 makes it low-value).
- [x] **`client` does not expose data-subject rights (export/erasure) directly in v1.** The
      existing `/v1/privacy/*` routes are server-key-only by design (§99's residency reasoning),
      and building a new person-authenticated proxy route is real backend work with deletion/
      compliance stakes — not something to rush alongside three new frontend builds. Studios
      needing this today call the server-key API from their own backend, per current docs.
      Revisit once `admin`'s staff tooling and the compliance backlog in §4 have more headroom.
