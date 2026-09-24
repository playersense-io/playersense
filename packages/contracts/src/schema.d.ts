/**
 * Generated from the PlayerSense API's own OpenAPI document. Do not edit.
 *
 * Regenerate with:  pnpm --filter @playersense/contracts generate
 * Verified in CI by: pnpm --filter @playersense/contracts check
 */

export interface paths {
    readonly "/v1/batches": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Accept a batch of analytics events
         * @description Answers 202 once the batch has reached durable queueing. The response says how many events were accepted and how many were dropped, so an SDK can stop retrying a batch that will never be accepted. The body may be sent with Content-Encoding: gzip or zstd; the size limit below applies to the decoded batch either way.
         */
        readonly post: operations["post_v1_batches"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/cohorts/query": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Answer a cohort question
         * @description Resolves to exactly one pre-aggregated cohort fact and never joins on a player key. Cells describing too few players are suppressed rather than returned as zero, and the response says how many were withheld.
         */
        readonly post: operations["post_v1_cohorts_query"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/config": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * What this player gets
         * @description Resolves every feature flag and every LiveOps event for one player: values and windows, never the rules behind them. Geography and network are derived from the request's own connection and are never read from the body, because a value a client sends is one a modified build can choose.
         */
        readonly post: operations["post_v1_config"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/invitations": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List invitations waiting for you
         * @description Matched on the address your sign-in verified, so it shows only what was sent to your own inbox. It carries no token and does not need to: accepting one of these names the organization instead.
         */
        readonly get: operations["get_v1_invitations"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/invitations/accept": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Accept an invitation
         * @description Name either the token from the link or the organization, when one is already waiting for your verified address. Accepting twice succeeds and says so, because the intent is satisfied either way.
         */
        readonly post: operations["post_v1_invitations_accept"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/metrics/query": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Answer a time-series question
         * @description Active players, sessions, events and revenue over time. The planner chooses the summary table from the requested grain (§26); a grain finer than the range can carry is refused rather than returned as tens of thousands of points.
         */
        readonly post: operations["post_v1_metrics_query"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/charges": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Spend from an organization's wallet
         * @description Consumes promotional credit first, soonest to expire first, then purchased credit oldest first. Refused whole rather than partly when there is not enough. One ledger entry for the charge, however many grants paid for it — the response says which.
         */
        readonly post: operations["post_v1_operator_organizations_by_org_charges"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/contract": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Put an organization on a contract
         * @description From now (ADR 0045). `invoiced` bills it monthly: usage its credit does not cover, the platform fee (prorated in a part month) and any top-up to the minimum commitment go on one Stripe invoice, due `terms_days` after it is sent; ingestion is suspended `overdue_grace_days` after a missed due date, or at once when what it owes, billed or not, reaches `credit_limit_micros`. It needs a credit limit and a billing email, and no live subscription. `prepaid` moves it back, closing what is unbilled on a final invoice. Terms are never edited: each call is a new contract. Recorded in the organization's audit log.
         */
        readonly put: operations["put_v1_operator_organizations_by_org_contract"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/credit-limit": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Set an organization's credit limit
         * @description How much it may owe before its ingestion is suspended. Without an override the limit is 20% of its paid usage in the last 30 days, or the plan's floor if that is higher; an override sets it exactly. Omit credit_limit_micros to clear the override. Recorded in the organization's audit log.
         */
        readonly put: operations["put_v1_operator_organizations_by_org_credit_limit"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/credits": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Add credit to an organization's wallet
         * @description A `promo` expires — ninety days unless `expires_at` names another date — and is spent before purchased credit. A `purchase` does not expire and cannot be given a date. `granted_at` backdates the grant, which decides where purchased credit sits in the spend queue. `reference` is the idempotency key: the same one twice grants once.
         */
        readonly post: operations["post_v1_operator_organizations_by_org_credits"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/invoices": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * An organization's invoices, as staff see them
         * @description Drafts and failed issues included, with why the last attempt did not issue, any difference between the month's usage and the dashboard's, and the ledger range each one bills.
         */
        readonly get: operations["get_v1_operator_organizations_by_org_invoices"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/invoices/{invoice_id}/credit-notes": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Credit part of an open invoice
         * @description Issues a Stripe credit note lowering what the invoice asks for, and what the organization owes with it. Whole cents, at most what is still owed.
         */
        readonly post: operations["post_v1_operator_organizations_by_org_invoices_by_invoice_id_credit_notes"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/invoices/{invoice_id}/mark-paid": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Record a wire Stripe did not see
         * @description Marks an open invoice paid out of band at Stripe, and settles it here as Stripe's webhook would. `reference` is the transfer's; the same one twice marks it once.
         */
        readonly post: operations["post_v1_operator_organizations_by_org_invoices_by_invoice_id_mark_paid"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/organizations/{org}/plan": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Move an organization onto a plan
         * @description From the next player on. The month's count carries over, so the organization keeps its place in the bands and players already charged stay charged. Recorded in the organization's audit log.
         */
        readonly put: operations["put_v1_operator_organizations_by_org_plan"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/plans": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * The whole catalogue
         * @description Every plan, withdrawn ones included, with the card in force now and the next one scheduled.
         */
        readonly get: operations["get_v1_operator_plans"];
        readonly put?: never;
        /**
         * Add a plan
         * @description A plan charges nothing for players until a rate card is published for it. `is_default` makes it the plan new organizations are put on, and unsets the previous default in the same transaction.
         */
        readonly post: operations["post_v1_operator_plans"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/plans/{plan}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Rename a plan, change what it includes, or withdraw it
         * @description Absent fields are left alone. Not its prices — publish a rate card for that. Withdrawing a plan stops it being sold; accounts on it stay.
         */
        readonly put: operations["put_v1_operator_plans_by_plan"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/plans/{plan}/rate-cards": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * A plan's price history
         * @description Newest first, each marked current, scheduled or superseded.
         */
        readonly get: operations["get_v1_operator_plans_by_plan_rate_cards"];
        readonly put?: never;
        /**
         * Set a plan's prices from a moment on
         * @description Tiers are player positions within a calendar month, lowest first, the last one unbounded; `unit_price_micros` is per player ($0.0005 is 500). `effective_from` defaults to now and cannot be in the past. Cards are never edited: publish another to change a price.
         */
        readonly post: operations["post_v1_operator_plans_by_plan_rate_cards"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/operator/plans/{plan}/subscription": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Set what a plan's subscription costs
         * @description Monthly and yearly prices in micros ($99 is 99000000). Creates the plan's Stripe Product the first time, and a Stripe Price per interval — Prices cannot be edited, so a changed amount is a new Price: new subscribers pay it, existing ones keep theirs until they change plan.
         */
        readonly put: operations["put_v1_operator_plans_by_plan_subscription"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** List the organizations you belong to */
        readonly get: operations["get_v1_organizations"];
        readonly put?: never;
        /**
         * Create an organization
         * @description The caller becomes its owner, and its six roles are seeded.
         */
        readonly post: operations["post_v1_organizations"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read an organization */
        readonly get: operations["get_v1_organizations_by_org"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/audit": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Who did what
         * @description The organization's own log, newest first: memberships, roles, keys, publishes, credit and configuration. Append-only — every field on the row is immutable, so there is no code path that could change what an entry says. Filterable to one action, because "who published" and "who changed a role" are different questions.
         */
        readonly get: operations["get_v1_organizations_by_org_audit"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * The plan, the state and the wallet
         * @description `available_micros` is what can be spent now: the sum of every grant that has not expired, in the order it will be spent. `ledger_balance_micros` is the ledger's running total, which is briefly higher between credit lapsing and the sweep writing it off.
         */
        readonly get: operations["get_v1_organizations_by_org_billing"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/auto-topup": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Configure automatic charging
         * @description A threshold at or above the amount is refused: it would top up into a balance still under the threshold, so the next check charges again, and the next. Enabling it without a saved card is refused too — it would do nothing, and the account would find out by being suspended.
         */
        readonly put: operations["put_v1_organizations_by_org_billing_auto_topup"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/invoices": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Monthly invoices
         * @description For an organization billed by invoice (ADR 0045): every invoice it was sent, newest first, with its lines — the platform fee, each month's players, the credit that paid for some of them, and any top-up to the contract's minimum — its due date, and Stripe's page to read and pay it. Empty for a prepaid organization.
         */
        readonly get: operations["get_v1_organizations_by_org_billing_invoices"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/invoices/{invoice_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** One invoice */
        readonly get: operations["get_v1_organizations_by_org_billing_invoices_by_invoice_id"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/ledger": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Why the balance is what it is
         * @description Every movement, newest first, append-only. A promo grant is a `promo` rather than a `topup` and lapsed credit is an `expiry` rather than an `adjustment` — an adjustment is a decision somebody can be asked about.
         */
        readonly get: operations["get_v1_organizations_by_org_billing_ledger"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/portal": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Manage cards and receipts
         * @description Opens Stripe's customer portal and returns its URL: add, remove or choose the default card, and read receipts. The default card is the one automatic top-up charges.
         */
        readonly post: operations["post_v1_organizations_by_org_billing_portal"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/subscription": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Subscribe to a plan
         * @description Opens Stripe Checkout for the plan's monthly or yearly price and returns its URL. The organization moves to the plan when Stripe reports the subscription, by webhook. An organization that already has a subscription changes or cancels it in the billing portal instead.
         */
        readonly post: operations["post_v1_organizations_by_org_billing_subscription"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/billing/top-ups": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Pay for credit
         * @description Opens a Stripe Checkout page for this amount and returns its URL; send the browser there. Nothing is credited by this call or by the browser coming back — the wallet moves when Stripe reports the payment, usually within seconds. The card used is saved at Stripe for automatic top-up; PlayerSense stores none of it.
         */
        readonly post: operations["post_v1_organizations_by_org_billing_top_ups"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/invitations": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List an organization's invitations
         * @description Every status, because an accepted invitation is the record of how somebody got in. No token is ever disclosed — only a digest is stored.
         */
        readonly get: operations["get_v1_organizations_by_org_invitations"];
        readonly put?: never;
        /**
         * Invite somebody
         * @description Sends a link and stores only its digest. The invitation can be accepted only by a Firebase account that has verified the address it was sent to, so forwarding it does not let a colleague join in somebody else's place.
         */
        readonly post: operations["post_v1_organizations_by_org_invitations"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/invitations/{invitation_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        readonly post?: never;
        /**
         * Revoke an invitation
         * @description The row is kept and marked revoked, so the token keeps matching something that refuses it rather than matching nothing by luck.
         */
        readonly delete: operations["delete_v1_organizations_by_org_invitations_by_invitation_id"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/members": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List an organization's members
         * @description With the member ceiling and how much of it is used, because an invitation that breaches it fails at accept time — in front of the invited colleague rather than whoever sent it.
         */
        readonly get: operations["get_v1_organizations_by_org_members"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/members/{member_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read one membership */
        readonly get: operations["get_v1_organizations_by_org_members_by_member_id"];
        readonly put?: never;
        readonly post?: never;
        /**
         * Remove a member
         * @description The row is deleted, unlike a revoked key or an archived flag: a membership is not a record of what happened, it is a live grant of access to player data. The organization's last owner cannot be removed.
         */
        readonly delete: operations["delete_v1_organizations_by_org_members_by_member_id"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/members/{member_id}/access": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Change which projects a member reaches
         * @description The second axis: the role says what, this says where. `all_projects` is required rather than defaulted — it is the widest grant in the product and must not be given by omitting a field.
         */
        readonly put: operations["put_v1_organizations_by_org_members_by_member_id_access"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/members/{member_id}/role": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        /**
         * Change what a member may do
         * @description Reserved to an owner or an admin: a grantable permission to assign roles is equivalent to owner. The organization's last owner cannot be demoted.
         */
        readonly put: operations["put_v1_organizations_by_org_members_by_member_id_role"];
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List the projects you can see
         * @description An organization's projects, filtered to the ones this member has been given access to.
         */
        readonly get: operations["get_v1_organizations_by_org_projects"];
        readonly put?: never;
        /**
         * Create a project
         * @description Creates a project and its development, staging and production environments. `home_region` and `policy_tier` are fixed at creation: a tier change applies forward and never retroactively, so changing one is a new project.
         */
        readonly post: operations["post_v1_organizations_by_org_projects"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read a project */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/cohorts/query": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Answer a cohort question for a signed-in person
         * @description The same planner, facts and suppression threshold the reporting-key endpoint uses. The scope is the project in the path, checked against your access list, and the environment named beside it.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_analytics_by_environment_cohorts_query"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/events": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List every event the game sends
         * @description Each event name the project has sent, with its count and players over the range and the range before it, when it was first and last accepted, and how many property keys it carried. Read from event_names, events_daily and event_properties_daily; nothing reads events_raw.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/events/{name}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Describe one event
         * @description One event's series against the previous range, its split by the platform, country and build it arrived with, and what each of its properties held: how often it was filled, how many distinct values it had, and its most frequent ones.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events_by_name"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/events/{name}/occurrences": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List an event's latest occurrences
         * @description The newest occurrences from the last seven days, whole: who sent them, on what, and every property. Names players, so it needs player.read.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events_by_name_occurrences"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/flags/exposures": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List how often every flag was read
         * @description Each flag the game recorded an exposure for, with its exposures over the range and the range before, its players, and each day. An exposure is the first time a session read a flag under a config version (SDK protocol §14).
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_flags_exposures"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/flags/{flag}/exposures": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Describe who saw which value of a flag
         * @description Exposures and players by value and day, by config version and by build, and the share that fell back to the game's own default because nothing was published.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_flags_by_flag_exposures"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/metrics/query": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Answer a time-series question for a signed-in person
         * @description Active players, sessions, events and revenue over time, for one environment of one project.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_analytics_by_environment_metrics_query"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/players": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List the game's players
         * @description One page of installs, filtered by status, arrival platform or country, and whether they have paid, sorted by when they were last seen or by what they did. Each is one install (§29.2): a reinstall is a new one. Needs player.read, and is not served for a kids_strict project.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_players"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/players/{id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Describe one player
         * @description An install by its id, or a signed-in player by the id the game gave them, across every install they played on in the last ninety days: their life so far, each day of the last ninety, and their newest events.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_players_by_id"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/purchases": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List purchases
         * @description The purchases ledger, newest first, a page at a time: what was bought, where, for how much, by whom, and whether it was their first. Names players, so it needs player.read.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_purchases"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/revenue": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Describe the game's revenue
         * @description Revenue, purchases and buyers against the previous range, by day and store, by product, store and country, by price, the day's first-time payers, spender tiers by lifetime spend, and purchases by weekday and hour. Revenue the game reported, in the currencies it reported, not receipts verified with a store.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_revenue"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/sessions": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Describe the game's sessions
         * @description Sessions by day against the previous range, how long they lasted on a fixed ladder with the median and 90th percentile read off it, how they ended, by platform, and when they start by weekday and hour in the timezone asked for. A day is counted once it is over; complete_through says which is the last.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_sessions"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/analytics/{environment}/sessions/recent": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List the newest sessions
         * @description Today's sessions, newest first, and yesterday's when today has fewer than asked for: who played, on what, for how long, and whether the session is still going. Names players, so it needs player.read.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_analytics_by_environment_sessions_recent"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/config/{environment}/draft": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Preview what the next publish would produce
         * @description Compiles the same input a publish would and throws the document away. Says whether it compiles, what it would carry, whether serving it costs a history lookup per request, and what is being left out and why.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_config_by_environment_draft"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/config/{environment}/publish": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Publish this project's configuration
         * @description Compiles every active flag and LiveOps event into one immutable version for the named environment, with each segment reference replaced by the segment's own rule. A version that compiles is a version that evaluates.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_config_by_environment_publish"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/flags": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** List a project's flags */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_flags"];
        readonly put?: never;
        /**
         * Declare a flag
         * @description `kind` and `key` are fixed at creation: a game calls a typed reader on the key, so a kind that changed would be a document the installed build cannot read. Every value is checked against the kind here.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_flags"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/flags/{key}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read a flag */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_flags_by_key"];
        /**
         * Replace a flag
         * @description `kind` is not a member of this body. A field a caller may send that has no effect is worse than one that does not exist.
         */
        readonly put: operations["put_v1_organizations_by_org_projects_by_project_flags_by_key"];
        readonly post?: never;
        /**
         * Archive a flag
         * @description The row is kept. Until somebody publishes, the game keeps getting the answer the last published document gave it.
         */
        readonly delete: operations["delete_v1_organizations_by_org_projects_by_project_flags_by_key"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/keys": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** List a project's API keys */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_keys"];
        readonly put?: never;
        /**
         * Mint an API key
         * @description Returns the key once. Only a SHA-256 digest is stored, so it cannot be shown again.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_keys"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/keys/{key_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        readonly post?: never;
        /**
         * Revoke an API key
         * @description The row is kept and marked revoked rather than deleted.
         */
        readonly delete: operations["delete_v1_organizations_by_org_projects_by_project_keys_by_key_id"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/liveops-events": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List a project's LiveOps events
         * @description Highest priority first. Each carries the next few concrete windows its schedule produces, computed by the same code the config path runs.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_liveops_events"];
        readonly put?: never;
        /**
         * Schedule a LiveOps event
         * @description Created as a draft unless told otherwise: publishing reads only the active ones, so an event cannot reach a player by being typed.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_liveops_events"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/liveops-events/{key}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read a LiveOps event */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_liveops_events_by_key"];
        /**
         * Replace a LiveOps event
         * @description `status` moves freely between draft, active, paused and archived. Every pair is something a studio legitimately does, and a state machine here would forbid the one somebody needs at two in the morning.
         */
        readonly put: operations["put_v1_organizations_by_org_projects_by_project_liveops_events_by_key"];
        readonly post?: never;
        /** Archive a LiveOps event */
        readonly delete: operations["delete_v1_organizations_by_org_projects_by_project_liveops_events_by_key"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/segments": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List a project's segments
         * @description Named audiences, archived ones included: an archived segment is still inlined in every published version that used it, so a dashboard explaining one has to be able to name it.
         */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_segments"];
        readonly put?: never;
        /**
         * Name an audience
         * @description The rule is parsed and every segment it refers to is resolved here, not at the publish: a rule that cannot compile is refused while the person who wrote it is still looking at it.
         */
        readonly post: operations["post_v1_organizations_by_org_projects_by_project_segments"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/projects/{project}/segments/{key}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Read a segment */
        readonly get: operations["get_v1_organizations_by_org_projects_by_project_segments_by_key"];
        /**
         * Replace a segment
         * @description A whole-object write. A partial update of a rule tree would mean two people merging into an audience neither of them wrote.
         */
        readonly put: operations["put_v1_organizations_by_org_projects_by_project_segments_by_key"];
        readonly post?: never;
        /**
         * Archive a segment
         * @description The row is kept and marked archived; the next publish leaves it out. A rule still naming it will then refuse to compile, which is the intended way to find out.
         */
        readonly delete: operations["delete_v1_organizations_by_org_projects_by_project_segments_by_key"];
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/roles": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * List the roles a member can hold
         * @description The six seeded roles, and any the organization has added. A system role's permissions are computed from the vocabulary rather than read from its row, so this list is the authority on what it grants.
         */
        readonly get: operations["get_v1_organizations_by_org_roles"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/organizations/{org}/usage": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * What this organization used
         * @description Tracked players, events and retained bytes over a range, broken down by project and by day, with the plan's allowances beside them. Defaults to the current calendar month. Tracked players is the busiest day rather than a sum — adding daily figures counts one player once per day they played.
         */
        readonly get: operations["get_v1_organizations_by_org_usage"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/pricing": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * What PlayerSense costs
         * @description Every plan on sale, its allowances, the price per tracked player in force now, and any price change already scheduled. Tiers are player positions within a calendar month, inclusive; a graduated card prices each player at the band their position falls in.
         */
        readonly get: operations["get_v1_pricing"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/privacy/erasures": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Erase everything held about one player
         * @description Records the request, stops the actor's events being accepted or served, and removes them asynchronously from the events, the two device-keyed summaries, the verified purchases and the archive. Returns immediately: removal is a rewrite of whole parts and whole objects and is measured in hours, which is why a tombstone goes up first.
         */
        readonly post: operations["post_v1_privacy_erasures"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/privacy/erasures/{request_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Check an erasure
         * @description Reports the state, the timestamps a deletion SLA is measured against, and what was removed.
         */
        readonly get: operations["get_v1_privacy_erasures_by_request_id"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/privacy/exports": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Export everything held about one player
         * @description Assembles the events, the device records, the daily activity and the verified purchases into one newline-delimited JSON bundle in this cell's own region. The bundle expires, so collect it before then.
         */
        readonly post: operations["post_v1_privacy_exports"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/privacy/exports/{request_id}": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /** Check an export */
        readonly get: operations["get_v1_privacy_exports_by_request_id"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/privacy/exports/{request_id}/download": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Download an export bundle
         * @description Streams the newline-delimited JSON bundle. The first line says what the bundle is and what it does not cover; the last line is a summary, so a truncated file is recognisable as one.
         */
        readonly get: operations["get_v1_privacy_exports_by_request_id_download"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/stripe/webhook": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly get?: never;
        readonly put?: never;
        /**
         * Stripe's events
         * @description Verified by the Stripe-Signature header. A payment that succeeded puts credit in its wallet, once, however many times and by however many events it is reported; a failed automatic top-up is released and retried later; a usage invoice paid, credited or voided settles what its organization owes (ADR 0045). Everything else is recorded and ignored.
         */
        readonly post: operations["post_v1_stripe_webhook"];
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
    readonly "/v1/users/me": {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        /**
         * Who you are, and where you belong
         * @description The signed-in person and every organization they belong to, with their role in each. `balance` is the organization's live balance — `available_micros`, or `owed_micros` when it owes, is the headline — and is null where the role does not hold `billing.read`, exactly where GET …/billing would refuse.
         */
        readonly get: operations["get_v1_users_me"];
        readonly put?: never;
        readonly post?: never;
        readonly delete?: never;
        readonly options?: never;
        readonly head?: never;
        readonly patch?: never;
        readonly trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        readonly APIKeyCreatedOut: {
            /** Format: date-time */
            readonly created_at: string;
            /** @description Which environment the key is scoped to */
            readonly environment: string;
            readonly id: string;
            /** @description project, reporting or server */
            readonly kind: string;
            /** Format: date-time */
            readonly last_used_at?: string | null;
            readonly name?: string;
            /** @description The public half of the key; not a secret */
            readonly public_id: string;
            /** Format: date-time */
            readonly revoked_at?: string | null;
            /** @description The full key. Shown once and never again */
            readonly secret: string;
        };
        readonly APIKeyListOut: {
            readonly keys: readonly components["schemas"]["APIKeyOut"][];
        };
        readonly APIKeyOut: {
            /** Format: date-time */
            readonly created_at: string;
            /** @description Which environment the key is scoped to */
            readonly environment: string;
            readonly id: string;
            /** @description project, reporting or server */
            readonly kind: string;
            /** Format: date-time */
            readonly last_used_at?: string | null;
            readonly name?: string;
            /** @description The public half of the key; not a secret */
            readonly public_id: string;
            /** Format: date-time */
            readonly revoked_at?: string | null;
        };
        readonly AcceptInvitationIn: {
            /** @description The organization to join, for an invitation already waiting for your verified address */
            readonly organization?: string;
            /** @description The token from the invitation link */
            readonly token?: string;
        };
        readonly Accepted: {
            /**
             * Format: int64
             * @description Events durably queued
             */
            readonly accepted: number;
            readonly batch_id: string;
            /**
             * Format: int64
             * @description Events refused; see reasons
             */
            readonly dropped?: number;
            readonly reasons?: readonly string[];
        };
        readonly AcceptedOut: {
            readonly all_projects: boolean;
            readonly already_member?: boolean;
            readonly organization: components["schemas"]["OrganizationOut"];
            readonly projects?: readonly components["schemas"]["ProjectRefOut"][];
            readonly role: components["schemas"]["RoleOut"];
        };
        readonly App: {
            readonly build?: string;
            readonly version: string;
        };
        readonly AuditEventOut: {
            /** @description A dotted name from the same family as the permissions: member.invite, liveops.publish */
            readonly action: string;
            readonly actor: string;
            /** @description Present only when a signed-in person did it */
            readonly actor_email?: string;
            /** Format: date-time */
            readonly at: string;
            readonly id: string;
            readonly ip?: string;
            readonly metadata?: {
                readonly [key: string]: unknown;
            };
            readonly target_id?: string;
            readonly target_type?: string;
        };
        readonly AuditListOut: {
            readonly events: readonly components["schemas"]["AuditEventOut"][];
        };
        readonly AutoTopUpOut: {
            /** Format: int64 */
            readonly amount_micros: number;
            /**
             * Format: int64
             * @description Automatic charges allowed in any 24 hours
             */
            readonly daily_limit: number;
            readonly enabled: boolean;
            /** @description Why the last automatic top-up did not happen */
            readonly last_error?: string;
            /** @description A charge has been sent to Stripe and its outcome is not yet known */
            readonly pending: boolean;
            /** @description The balance is below the threshold and a top-up is due within seconds */
            readonly queued: boolean;
            /**
             * Format: date-time
             * @description After a decline, not tried again before this
             */
            readonly retry_after?: string | null;
            /** Format: int64 */
            readonly threshold_micros: number;
            /**
             * Format: int64
             * @description Automatic charges sent in the current 24-hour window
             */
            readonly used_today: number;
        };
        readonly BalanceSummaryOut: {
            readonly available: string;
            /**
             * Format: int64
             * @description The headline: credit left to spend, after usage not yet settled
             */
            readonly available_micros: number;
            /** @description prepaid or invoiced */
            readonly collection: string;
            readonly currency: string;
            readonly owed: string;
            /**
             * Format: int64
             * @description What it owes, including usage not yet settled; the headline when not zero
             */
            readonly owed_micros: number;
            /**
             * Format: int64
             * @description Usage counted and not yet on the statement — minutes old at most
             */
            readonly pending_micros: number;
            /**
             * Format: int64
             * @description For an invoiced organization, what it owes on its invoices, billed or not; not late
             */
            readonly receivable_micros: number;
            /** @description active, grace_period, restricted, suspended or canceled */
            readonly state: string;
        };
        readonly BillingOut: {
            readonly arrears: string;
            /** Format: int64 */
            readonly arrears_micros: number;
            readonly auto_topup: components["schemas"]["AutoTopUpOut"];
            /** @description The same number, rendered */
            readonly available: string;
            /** Format: int64 */
            readonly available_micros: number;
            /** @description prepaid or invoiced */
            readonly collection: string;
            /** @description The terms in force; absent if the organization has never had a contract */
            readonly contract?: components["schemas"]["ContractOut"] | null;
            /**
             * Format: int64
             * @description Owing this much suspends ingestion at once
             */
            readonly credit_limit_micros: number;
            /** @description usage (20% of the last 30 days' paid usage), floor (the minimum), or override (set by PlayerSense) */
            readonly credit_limit_source: string;
            readonly currency: string;
            readonly expiring_soon?: readonly components["schemas"]["CreditLotOut"][];
            readonly features: readonly string[];
            /** Format: int64 */
            readonly ledger_balance_micros: number;
            readonly live: components["schemas"]["LiveBalanceOut"];
            /** @description Every grant with credit left, in the order it will be spent */
            readonly lots?: readonly components["schemas"]["CreditLotOut"][];
            readonly month?: components["schemas"]["MonthUsageOut"] | null;
            readonly plan: components["schemas"]["PlanOut"];
            /** Format: int64 */
            readonly promo_micros: number;
            /** Format: int64 */
            readonly purchased_micros: number;
            /** @description What an invoiced organization owes and is not yet late on */
            readonly receivable?: components["schemas"]["ReceivableOut"] | null;
            /** @description active, grace_period (owes; everything works), suspended (ingestion refused until paid), restricted or canceled */
            readonly state: string;
            readonly subscription?: components["schemas"]["SubscriptionOut"] | null;
            /**
             * Format: date-time
             * @description If still owing then, ingestion is suspended
             */
            readonly suspends_at?: string | null;
        };
        readonly Breakdown: {
            /** @description install_platform, install_country or first_app_version */
            readonly dimension: string;
        };
        readonly CheckoutOut: {
            /** Format: date-time */
            readonly expires_at: string;
            /** @description Stripe's Checkout Session id */
            readonly id: string;
            /** @description Stripe's hosted payment page; redirect the browser here */
            readonly url: string;
        };
        readonly CohortQueryIn: {
            /**
             * @description install (default) or player
             * @enum {string}
             */
            readonly basis?: "install" | "player";
            readonly breakdown?: components["schemas"]["Breakdown"] | null;
            /**
             * @description playtime or spend; required for metric=bucket
             * @enum {string}
             */
            readonly bucket?: "playtime" | "spend";
            readonly cohort: components["schemas"]["CohortRange"];
            /** @description Not accepted; scope comes from the key */
            readonly environment_id?: string;
            /** @description The event, for metric=event */
            readonly event?: string;
            readonly filters?: readonly components["schemas"]["Filter"][];
            /**
             * @description retention, playtime, sessions, revenue, ltv, payer_conversion, bucket or event
             * @enum {string}
             */
            readonly metric: "retention" | "playtime" | "sessions" | "revenue" | "ltv" | "payer_conversion" | "bucket" | "event";
            /** @description Which day-offsets to return */
            readonly offsets?: readonly number[];
            /** @description Not accepted; scope comes from the key */
            readonly org_id?: string;
            /** @description Not accepted; scope comes from the key */
            readonly project_id?: string;
            /**
             * @description classic or rolling; required for retention
             * @enum {string}
             */
            readonly retention_basis?: "classic" | "rolling";
        };
        readonly CohortRange: {
            /** @description First cohort day, inclusive */
            readonly from: string;
            /** @description day (default), week or month */
            readonly grain?: string;
            /** @description Last cohort day, inclusive */
            readonly to: string;
        };
        readonly CohortResult: {
            /** Format: int64 */
            readonly cohort_maturity_days: number;
            /** Format: date-time */
            readonly computed_at: string;
            readonly rows: readonly components["schemas"]["CohortRow"][];
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
        };
        readonly CohortRow: {
            readonly cohort_date: string;
            /** Format: int64 */
            readonly cohort_size: number;
            /** Format: int64 */
            readonly day_offset: number;
            readonly dim_value?: string;
            /** Format: int64 */
            readonly events?: number;
            /** Format: int64 */
            readonly payers?: number;
            /** Format: int64 */
            readonly players: number;
            /** Format: int64 */
            readonly players_cumulative?: number;
            /** Format: int64 */
            readonly playtime_ms?: number;
            /** Format: int64 */
            readonly purchases?: number;
            /** Format: double */
            readonly rate: number;
            /** Format: int64 */
            readonly revenue_micros?: number;
            /** Format: int64 */
            readonly revenue_micros_cumulative?: number;
            /** Format: int64 */
            readonly sessions?: number;
        };
        readonly ConfigEvent: {
            /** Format: int64 */
            readonly ends_at_ms: number;
            readonly key: string;
            readonly kind: string;
            /** Format: int64 */
            readonly next_starts_at_ms?: number;
            readonly payload: unknown;
            /** Format: int64 */
            readonly priority: number;
            /** Format: int64 */
            readonly starts_at_ms: number;
        };
        readonly ConfigIn: {
            readonly app: components["schemas"]["App"];
            readonly context?: {
                readonly [key: string]: unknown;
            };
            readonly device: components["schemas"]["Device"];
            /** @enum {string} */
            readonly environment?: "development" | "staging" | "production";
            /** Format: uuid */
            readonly install_id: string;
            readonly player_id?: string;
            /** Format: int64 */
            readonly schema_version: number;
            readonly sdk: components["schemas"]["SDK"];
        };
        readonly ConfigResponse: {
            readonly flags: {
                readonly [key: string]: unknown;
            };
            readonly liveops_events: readonly components["schemas"]["ConfigEvent"][];
            /** Format: int64 */
            readonly server_time_ms: number;
            readonly upcoming?: readonly components["schemas"]["ConfigEvent"][];
            /** Format: int64 */
            readonly version: number;
        };
        readonly ConfigVersionOut: {
            readonly environment: string;
            /** Format: int64 */
            readonly flags: number;
            /** Format: int64 */
            readonly liveops_events: number;
            readonly needs_player_facts: boolean;
            readonly note?: string;
            /** Format: date-time */
            readonly published_at: string;
            readonly published_by: string;
            /** Format: int64 */
            readonly version: number;
        };
        readonly Consent: {
            /** @description Whether the player agreed to analytics collection */
            readonly analytics: boolean;
            /** @description authorized, denied, not_determined or restricted */
            readonly att_status?: string;
            /** Format: date-time */
            readonly collected_at?: string;
            readonly gdpr_applies?: boolean;
            /** @description Which version of the terms they agreed to */
            readonly policy_version: string;
        };
        readonly ContextFrame: {
            /** Format: int64 */
            readonly at: number;
            readonly set?: {
                readonly [key: string]: unknown;
            };
            readonly unset?: readonly string[];
            readonly values?: {
                readonly [key: string]: unknown;
            };
        };
        readonly ContractOut: {
            /** @description invoiced or prepaid */
            readonly collection: string;
            /** Format: int64 */
            readonly credit_limit_micros?: number | null;
            /** Format: date-time */
            readonly effective_from: string;
            readonly memo?: string;
            /** Format: int64 */
            readonly minimum_commitment_micros: number;
            /** Format: int64 */
            readonly overdue_grace_days: number;
            /** Format: int64 */
            readonly platform_fee_micros: number;
            readonly po_number?: string;
            /** Format: int64 */
            readonly terms_days: number;
        };
        readonly CreateOrganizationIn: {
            /** @description Display name */
            readonly name: string;
            /** @description URL-safe short name; lower case letters, digits and hyphens */
            readonly slug: string;
        };
        readonly CreatePlanIn: {
            readonly description?: string;
            /** @description included_mau, included_events, … — the closed vocabulary */
            readonly entitlements?: {
                readonly [key: string]: number;
            };
            /** @description premium_support, slack_reports, mcp_access — the closed vocabulary */
            readonly features?: readonly string[];
            /** @description The plan new organizations are put on; at most one */
            readonly is_default?: boolean;
            /** @description Lowercase, permanent: accounts and snapshots refer to it */
            readonly key: string;
            /**
             * Format: int64
             * @description Stored and shown; not yet charged
             */
            readonly monthly_price_micros?: number;
            readonly name: string;
            /** Format: email */
            readonly operator: string;
        };
        readonly CreditChargedOut: {
            /** Format: int64 */
            readonly amount_micros: number;
            readonly available: string;
            /** Format: int64 */
            readonly available_micros: number;
            readonly charged: boolean;
            readonly draws: readonly components["schemas"]["DrawOut"][];
            readonly entry: components["schemas"]["LedgerEntryOut"];
        };
        readonly CreditGrantedOut: {
            readonly available: string;
            /** Format: int64 */
            readonly available_micros: number;
            readonly granted: boolean;
            readonly lot: components["schemas"]["CreditLotOut"];
        };
        readonly CreditLotOut: {
            /**
             * Format: int64
             * @description What was granted
             */
            readonly amount_micros: number;
            readonly description?: string;
            readonly expired?: boolean;
            /** Format: date-time */
            readonly expires_at?: string | null;
            /** Format: date-time */
            readonly granted_at: string;
            readonly id: string;
            /** @description purchase or promo */
            readonly kind: string;
            readonly remaining: string;
            /**
             * Format: int64
             * @description What is left of it
             */
            readonly remaining_micros: number;
        };
        readonly Device: {
            readonly locale?: string;
            readonly os_version?: string;
            /** @description ios, android, windows, macos, linux, web, console */
            readonly platform: string;
        };
        readonly DraftOut: {
            /** @description Whether a publish would succeed */
            readonly compiles: boolean;
            readonly environment: string;
            readonly excluded?: readonly components["schemas"]["ExcludedOut"][];
            /**
             * Format: int64
             * @description Flags the next version would carry
             */
            readonly flags: number;
            /**
             * Format: int64
             * @description Active LiveOps events it would carry
             */
            readonly liveops_events: number;
            /** @description Whether serving it costs a history lookup per request */
            readonly needs_player_facts: boolean;
            /** Format: int64 */
            readonly next_version: number;
            /** @description Why it would not, naming the flag or event */
            readonly problem?: string;
            /** Format: int64 */
            readonly published_version: number;
        };
        readonly DrawOut: {
            /** Format: int64 */
            readonly amount_micros: number;
            readonly kind: string;
            readonly lot: string;
        };
        readonly EnvironmentOut: {
            readonly id: string;
            /** @description development, staging or production */
            readonly kind: string;
        };
        readonly ErasureIn: {
            /** @description The actor key or install_id the player is known by in this project */
            readonly actor_key: string;
        };
        readonly ErasureOut: {
            readonly actor_key: string;
            /** Format: int32 */
            readonly archive_objects_redacted: number;
            /**
             * Format: date-time
             * @description When nothing further could arrive and removal was finished
             */
            readonly completed_at?: string | null;
            /** Format: int32 */
            readonly devices_deleted: number;
            /** @description Why the last attempt did not finish; the request is retried */
            readonly failure_reason?: string;
            /** Format: int32 */
            readonly purchases_deleted: number;
            /** Format: date-time */
            readonly received_at: string;
            readonly request_id: string;
            /** @description received, tombstoned, purging, completed or failed */
            readonly status: string;
            /**
             * Format: date-time
             * @description When the data stopped being accepted or served
             */
            readonly tombstoned_at?: string | null;
        };
        readonly ErrorBody: {
            readonly code: string;
            readonly details?: readonly components["schemas"]["ErrorDetail"][];
            readonly message: string;
            /** Format: int64 */
            readonly status: number;
        };
        readonly ErrorDetail: {
            readonly field: string;
            readonly issue: string;
            readonly location: string;
        };
        readonly ErrorResponse: {
            readonly error: components["schemas"]["ErrorBody"];
            readonly request_id?: string;
        };
        readonly Event: {
            readonly currency?: string;
            /** @description Optional; derived from the batch and position when omitted */
            readonly event_id?: string;
            /** @description level_completed, session_start, purchase… */
            readonly event_name: string;
            /** Format: date-time */
            readonly event_time: string;
            /** @description Present once the player has an account */
            readonly player_id?: string;
            readonly properties?: {
                readonly [key: string]: unknown;
            };
            /** Format: int64 */
            readonly revenue_micros?: number;
            /**
             * Format: int64
             * @description Monotonic within a session, for ordering
             */
            readonly sequence?: number;
            readonly session_id?: string;
        };
        readonly EventBucketOut: {
            /** Format: date-time */
            readonly at: string;
            /** Format: int64 */
            readonly events: number;
            /** Format: int64 */
            readonly players?: number | null;
            /** Format: int64 */
            readonly previous_events: number;
        };
        readonly EventCatalogueOut: {
            readonly approximate: boolean;
            readonly events: readonly components["schemas"]["EventSummaryOut"][];
            /** Format: date-time */
            readonly from: string;
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
            /** Format: date-time */
            readonly to: string;
        };
        readonly EventDetailOut: {
            readonly approximate: boolean;
            readonly breakdowns: {
                readonly [key: string]: readonly components["schemas"]["EventSliceOut"][];
            };
            /** Format: date-time */
            readonly first_seen: string;
            /** Format: date-time */
            readonly from: string;
            readonly interval: string;
            /** Format: date-time */
            readonly last_seen: string;
            readonly name: string;
            readonly properties: readonly components["schemas"]["EventPropertyOut"][];
            /** @description Every bucket of the range, with the same bucket of the range before */
            readonly series: readonly components["schemas"]["EventBucketOut"][];
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
            /** Format: date-time */
            readonly to: string;
            readonly totals: components["schemas"]["EventTotalsOut"];
            readonly type: string;
        };
        readonly EventOccurrenceOut: {
            readonly app_version: string;
            /** Format: date-time */
            readonly at: string;
            readonly country?: string;
            readonly currency?: string;
            readonly event_id: string;
            readonly install_id: string;
            readonly name: string;
            readonly platform: string;
            readonly player_id?: string;
            readonly properties?: {
                readonly [key: string]: unknown;
            };
            /** Format: date-time */
            readonly received_at: string;
            /** Format: int64 */
            readonly revenue_micros?: number;
            readonly session_id?: string;
        };
        readonly EventOccurrencesOut: {
            readonly occurrences: readonly components["schemas"]["EventOccurrenceOut"][];
        };
        readonly EventPropertyOut: {
            /**
             * Format: int64
             * @description Distinct values, from a sketch
             */
            readonly distinct: number;
            /**
             * Format: double
             * @description The share of the event's occurrences that carried it
             */
            readonly fill: number;
            readonly key: string;
            /** Format: int64 */
            readonly occurrences: number;
            readonly top: readonly components["schemas"]["TopValueOut"][];
            /** @description string, int, float or bool */
            readonly type: string;
        };
        readonly EventSliceOut: {
            /** Format: int64 */
            readonly events: number;
            /** Format: int64 */
            readonly players?: number | null;
            readonly value: string;
        };
        readonly EventSummaryOut: {
            /** Format: int64 */
            readonly events: number;
            /** Format: date-time */
            readonly first_seen: string;
            /** Format: date-time */
            readonly last_seen: string;
            readonly name: string;
            /** Format: int64 */
            readonly players?: number | null;
            /** Format: int64 */
            readonly previous_events: number;
            /** Format: int64 */
            readonly previous_players?: number | null;
            /** Format: int64 */
            readonly properties: number;
            /** @description custom, session, purchase or exposure, as ingest classified it */
            readonly type: string;
        };
        readonly EventTotalsOut: {
            /** Format: int64 */
            readonly events: number;
            /** Format: int64 */
            readonly players?: number | null;
            /** Format: int64 */
            readonly previous_events: number;
            /** Format: int64 */
            readonly previous_players?: number | null;
        };
        readonly ExcludedOut: {
            readonly key: string;
            /** @description segment, flag or liveops_event */
            readonly kind: string;
            readonly reason: string;
        };
        readonly ExportIn: {
            /** @description The actor key or install_id the player is known by in this project */
            readonly actor_key: string;
        };
        readonly ExportOut: {
            readonly actor_key: string;
            /** Format: date-time */
            readonly completed_at?: string | null;
            /** @description Where to fetch the bundle while it exists */
            readonly download_url?: string;
            /**
             * Format: int64
             * @description How many events the bundle holds; zero is a real answer
             */
            readonly events: number;
            /**
             * Format: date-time
             * @description When the bundle is deleted; collect it before this
             */
            readonly expires_at?: string | null;
            readonly failure_reason?: string;
            /** Format: date-time */
            readonly received_at: string;
            readonly request_id: string;
            /** Format: int64 */
            readonly size_bytes: number;
            /** @description received, running, completed, failed or expired */
            readonly status: string;
        };
        readonly Filter: {
            /** @description A sticky cohort dimension */
            readonly field: string;
            /** @description The values, for in */
            readonly in?: readonly string[];
            /** @description eq, ne or in */
            readonly op: string;
            /** @description The value, for eq and ne */
            readonly value?: string;
        };
        readonly FlagAppVersionOut: {
            readonly app_version: string;
            /** Format: int64 */
            readonly default_exposures: number;
            /** Format: int64 */
            readonly exposures: number;
            /** Format: int64 */
            readonly players?: number | null;
        };
        readonly FlagExposureListOut: {
            readonly flags: readonly components["schemas"]["FlagExposureSumOut"][];
            /** Format: date-time */
            readonly from: string;
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
            /** Format: date-time */
            readonly to: string;
        };
        readonly FlagExposureOut: {
            readonly app_versions: readonly components["schemas"]["FlagAppVersionOut"][];
            /** @description Players are distinct counts from sketches */
            readonly approximate: boolean;
            readonly days: readonly components["schemas"]["FlagValueDayOut"][];
            /** Format: double */
            readonly default_share: number;
            /** Format: int64 */
            readonly exposures: number;
            readonly flag: string;
            /** Format: date-time */
            readonly from: string;
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
            /** Format: date-time */
            readonly to: string;
            readonly values: readonly components["schemas"]["FlagValueOut"][];
            readonly versions: readonly components["schemas"]["FlagVersionOut"][];
        };
        readonly FlagExposureSumOut: {
            /** @description Exposures on each day of the range, in order */
            readonly daily: readonly number[];
            /** Format: int64 */
            readonly exposures: number;
            readonly flag: string;
            /** Format: int64 */
            readonly players?: number | null;
            /** Format: int64 */
            readonly previous_exposures: number;
        };
        readonly FlagListOut: {
            readonly flags: readonly components["schemas"]["FlagOut"][];
        };
        readonly FlagOut: {
            readonly archived: boolean;
            /** Format: date-time */
            readonly created_at: string;
            readonly description?: string;
            /**
             * Format: byte
             * @description What a player gets when no rule matches, and what the SDK uses offline
             */
            readonly fallback: string;
            readonly id: string;
            /** @description What the game reads. Immutable */
            readonly key: string;
            /** @description bool, string, number or json. Immutable */
            readonly kind: string;
            readonly name: string;
            /**
             * Format: int64
             * @description Hundredths of a percent: 2500 is 25%
             */
            readonly rollout: number;
            /** @description Ordered; the first whose audience matches wins */
            readonly rules: readonly components["schemas"]["VariantOut"][];
            /** @description Changing it draws a fresh sample */
            readonly salt?: string;
            /** Format: date-time */
            readonly updated_at: string;
        };
        readonly FlagValueDayOut: {
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly exposures: number;
            /** Format: int64 */
            readonly players?: number | null;
            readonly published: boolean;
            readonly value: string;
        };
        readonly FlagValueOut: {
            /** Format: int64 */
            readonly exposures: number;
            /** Format: int64 */
            readonly players?: number | null;
            /** @description False for the game's own default */
            readonly published: boolean;
            /** @description As text, whatever type it was */
            readonly value: string;
        };
        readonly FlagVersionOut: {
            /** Format: int32 */
            readonly config_version: number;
            /** Format: int64 */
            readonly exposures: number;
            /** Format: int64 */
            readonly players?: number | null;
        };
        readonly IngestBatchIn: {
            readonly app: components["schemas"]["App"];
            /**
             * Format: uuid
             * @description Stable across retries, so a redelivery is recognisable
             */
            readonly batch_id: string;
            readonly consent: components["schemas"]["Consent"];
            readonly context?: readonly components["schemas"]["ContextFrame"][];
            readonly device: components["schemas"]["Device"];
            /**
             * @description development, staging or production
             * @enum {string}
             */
            readonly environment?: "development" | "staging" | "production";
            readonly events: readonly components["schemas"]["Event"][];
            /**
             * Format: uuid
             * @description The installation this batch came from
             */
            readonly install_id: string;
            /**
             * Format: int64
             * @description The envelope version; currently 1
             */
            readonly schema_version: number;
            readonly sdk: components["schemas"]["SDK"];
        };
        readonly InvitationCreatedOut: {
            /** Format: date-time */
            readonly accepted_at?: string | null;
            readonly all_projects: boolean;
            /** Format: date-time */
            readonly created_at: string;
            readonly delivered: boolean;
            /** @description Who it was sent to. Compared with the verified address at accept time */
            readonly email: string;
            /** Format: date-time */
            readonly expires_at: string;
            readonly id: string;
            /** @description Who sent it; absent if that account is gone */
            readonly invited_by?: string;
            readonly projects?: readonly components["schemas"]["ProjectRefOut"][];
            /** @description What accepting grants. Fixed when it was sent */
            readonly role: components["schemas"]["RoleOut"];
            /** @description pending, accepted, revoked or expired */
            readonly status: string;
        };
        readonly InvitationListOut: {
            readonly invitations: readonly components["schemas"]["InvitationOut"][];
        };
        readonly InvitationOut: {
            /** Format: date-time */
            readonly accepted_at?: string | null;
            readonly all_projects: boolean;
            /** Format: date-time */
            readonly created_at: string;
            /** @description Who it was sent to. Compared with the verified address at accept time */
            readonly email: string;
            /** Format: date-time */
            readonly expires_at: string;
            readonly id: string;
            /** @description Who sent it; absent if that account is gone */
            readonly invited_by?: string;
            readonly projects?: readonly components["schemas"]["ProjectRefOut"][];
            /** @description What accepting grants. Fixed when it was sent */
            readonly role: components["schemas"]["RoleOut"];
            /** @description pending, accepted, revoked or expired */
            readonly status: string;
        };
        readonly InvoiceLineOut: {
            /**
             * Format: int64
             * @description What the invoice bills for it
             */
            readonly amount_cents: number;
            /**
             * Format: int64
             * @description The ledger's exact amount
             */
            readonly amount_micros: number;
            readonly description: string;
            /** @description platform_fee, usage, prior_usage, credits_applied, commitment or adjustment */
            readonly kind: string;
            /** Format: date-time */
            readonly period_start?: string | null;
        };
        readonly InvoiceOut: {
            /** Format: int64 */
            readonly amount_paid_micros: number;
            /** Format: int64 */
            readonly amount_remaining_micros: number;
            readonly currency: string;
            /** Format: date-time */
            readonly due_at?: string | null;
            /** @description Issued when the organization left invoicing */
            readonly final?: boolean;
            /** @description Stripe's page to read and pay it */
            readonly hosted_url?: string;
            readonly id: string;
            /** Format: date-time */
            readonly issued_at?: string | null;
            readonly lines: readonly components["schemas"]["InvoiceLineOut"][];
            /** @description Stripe's invoice number, once issued */
            readonly number?: string;
            /** @description Open and past its due date */
            readonly overdue: boolean;
            /** Format: date-time */
            readonly paid_at?: string | null;
            readonly pdf_url?: string;
            /**
             * Format: date-time
             * @description Exclusive
             */
            readonly period_end: string;
            /** Format: date-time */
            readonly period_start: string;
            readonly po_number?: string;
            /** @description draft (being issued), open, paid, void, uncollectible, or empty (nothing to bill; never sent) */
            readonly status: string;
            readonly total: string;
            /** Format: int64 */
            readonly total_micros: number;
        };
        readonly InvoicesOut: {
            readonly invoices: readonly components["schemas"]["InvoiceOut"][];
        };
        readonly LedgerEntryOut: {
            readonly amount: string;
            /**
             * Format: int64
             * @description Positive adds, negative spends
             */
            readonly amount_micros: number;
            /** Format: date-time */
            readonly at: string;
            /** Format: int64 */
            readonly balance_after_micros: number;
            readonly description?: string;
            readonly id: string;
            /** @description topup, promo, usage, subscription, adjustment, refund or expiry */
            readonly kind: string;
        };
        readonly LedgerOut: {
            readonly entries: readonly components["schemas"]["LedgerEntryOut"][];
        };
        readonly LiveBalanceOut: {
            /**
             * Format: int64
             * @description The headline: credit left to spend, after pending usage
             */
            readonly available_micros: number;
            readonly balance: string;
            /**
             * Format: int64
             * @description The ledger total minus pending usage; counts lapsed promo credit until it is written off, so not the headline
             */
            readonly balance_micros: number;
            /**
             * Format: int64
             * @description Arrears plus pending usage the credit does not cover; the headline when not zero
             */
            readonly owed_micros: number;
            readonly pending: string;
            /**
             * Format: int64
             * @description Usage counted and not yet on the statement — minutes old at most
             */
            readonly pending_micros: number;
            /** Format: int64 */
            readonly promo_micros: number;
            /** Format: int64 */
            readonly purchased_micros: number;
            /**
             * Format: int64
             * @description For an invoiced organization, what it owes on its invoices, billed or not, including pending usage the credit does not cover — owed, not late. Zero for a prepaid one
             */
            readonly receivable_micros: number;
        };
        readonly LiveOpsEventListOut: {
            readonly liveops_events: readonly components["schemas"]["LiveOpsEventOut"][];
        };
        readonly LiveOpsEventOut: {
            /** Format: byte */
            readonly audience?: string;
            /** Format: date-time */
            readonly created_at: string;
            readonly id: string;
            /** @description Immutable */
            readonly key: string;
            /** @description The studio's own type: seasonal_event, offer, announcement */
            readonly kind: string;
            readonly name: string;
            /** @description The next few concrete windows this schedule produces */
            readonly occurrences?: readonly components["schemas"]["WindowOut"][];
            /**
             * Format: byte
             * @description Opaque to PlayerSense; the studio's own shape
             */
            readonly payload: string;
            /**
             * Format: int64
             * @description Higher wins when two events of one kind are both on
             */
            readonly priority: number;
            /** Format: int64 */
            readonly rollout: number;
            readonly salt?: string;
            readonly schedule: components["schemas"]["ScheduleOut"];
            /** @description draft, active, paused or archived */
            readonly status: string;
            /** Format: date-time */
            readonly updated_at: string;
        };
        readonly MeOrganizationOut: {
            /** @description Null when your role may not read billing, or there is no billing account */
            readonly balance?: components["schemas"]["BalanceSummaryOut"] | null;
            readonly id: string;
            readonly name: string;
            readonly role: components["schemas"]["MeRoleOut"];
            readonly slug: string;
        };
        readonly MeOut: {
            /** @description Every organization you belong to, by name */
            readonly organizations: readonly components["schemas"]["MeOrganizationOut"][];
            readonly person: components["schemas"]["PersonOut"];
        };
        readonly MeRoleOut: {
            /** @description Stable; owner, admin, liveops_manager, developer, growth_manager or billing for the seeded six */
            readonly key: string;
            readonly name: string;
        };
        readonly MemberListOut: {
            /** Format: int64 */
            readonly member_limit: number;
            readonly members: readonly components["schemas"]["MemberOut"][];
            /** Format: int64 */
            readonly members_used: number;
        };
        readonly MemberOut: {
            /** @description Every project, including ones created tomorrow */
            readonly all_projects: boolean;
            readonly email: string;
            readonly id: string;
            /** Format: date-time */
            readonly joined_at: string;
            readonly name?: string;
            readonly projects?: readonly components["schemas"]["ProjectRefOut"][];
            readonly role: components["schemas"]["RoleOut"];
            readonly you?: boolean;
        };
        readonly MetricQueryIn: {
            /** @description Not accepted; scope comes from the key */
            readonly environment_id?: string;
            /** @description Only on minute and hour intervals */
            readonly event_name?: string;
            readonly filters?: readonly components["schemas"]["Filter"][];
            readonly from: string;
            /** @description At most one dimension; event_name on minute and hour intervals */
            readonly group_by?: readonly string[];
            /**
             * @description minute, hour, day (default) or month
             * @enum {string}
             */
            readonly interval?: "minute" | "hour" | "day" | "month";
            /**
             * @description active_players, new_players, sessions, revenue…
             * @enum {string}
             */
            readonly metric: "active_players" | "new_players" | "returning_players" | "sessions" | "playtime" | "events" | "revenue" | "purchases" | "payers" | "continuing_players" | "reactivated_players";
            /** @description Not accepted; scope comes from the key */
            readonly org_id?: string;
            /** @description Not accepted; scope comes from the key */
            readonly project_id?: string;
            readonly to: string;
        };
        readonly MetricResult: {
            readonly approximate: boolean;
            /** Format: date-time */
            readonly computed_at?: string;
            readonly interval: string;
            readonly rows: readonly components["schemas"]["MetricRow"][];
            readonly source: string;
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
        };
        readonly MetricRow: {
            /** Format: int64 */
            readonly active_players?: number;
            /**
             * Format: date-time
             * @description Start of the interval, UTC
             */
            readonly bucket: string;
            /** Format: int64 */
            readonly continuing_players?: number;
            readonly dim_value?: string;
            /** Format: int64 */
            readonly events?: number;
            /** Format: int64 */
            readonly new_players?: number;
            /** Format: int64 */
            readonly payers?: number;
            /** Format: int64 */
            readonly playtime_ms?: number;
            /** Format: int64 */
            readonly purchases?: number;
            /** Format: int64 */
            readonly reactivated_players?: number;
            /** Format: int64 */
            readonly returning_players?: number;
            /** Format: int64 */
            readonly revenue_micros?: number;
            /** Format: int64 */
            readonly sessions?: number;
            /** Format: double */
            readonly value: number;
        };
        readonly MonthUsageOut: {
            readonly card?: components["schemas"]["RateCardOut"] | null;
            readonly charged: string;
            /**
             * Format: int64
             * @description Every charge this month, drained or not
             */
            readonly charged_micros: number;
            /**
             * Format: int64
             * @description Free this month under the plan
             */
            readonly included_players: number;
            /**
             * Format: int64
             * @description Queued and not yet taken from the wallet; minutes old at most
             */
            readonly pending_micros: number;
            /** Format: date-time */
            readonly period_start: string;
            /** Format: int64 */
            readonly tier: number;
            /** Format: int64 */
            readonly tracked_players: number;
        };
        readonly NewPayersOut: {
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly payers?: number | null;
        };
        readonly OperatorInvoiceOut: {
            /** Format: int64 */
            readonly amount_paid_micros: number;
            /** Format: int64 */
            readonly amount_remaining_micros: number;
            /** Format: int64 */
            readonly carry_in_micros: number;
            /** Format: int64 */
            readonly carry_out_micros: number;
            readonly currency: string;
            /**
             * Format: int64
             * @description Usage billed for the month minus what the dashboard said it cost; zero when they agree
             */
            readonly discrepancy_micros: number;
            /** Format: date-time */
            readonly due_at?: string | null;
            /** @description Issued when the organization left invoicing */
            readonly final?: boolean;
            /** @description Stripe's page to read and pay it */
            readonly hosted_url?: string;
            readonly id: string;
            /** Format: date-time */
            readonly issued_at?: string | null;
            /** @description Why the last attempt to issue it did not */
            readonly last_error?: string;
            /** Format: int64 */
            readonly ledger_from_id: number;
            /** Format: int64 */
            readonly ledger_to_id: number;
            readonly lines: readonly components["schemas"]["InvoiceLineOut"][];
            /** @description Stripe's invoice number, once issued */
            readonly number?: string;
            /** @description Open and past its due date */
            readonly overdue: boolean;
            /** Format: date-time */
            readonly paid_at?: string | null;
            readonly pdf_url?: string;
            /**
             * Format: date-time
             * @description Exclusive
             */
            readonly period_end: string;
            /** Format: date-time */
            readonly period_start: string;
            readonly po_number?: string;
            /** @description draft (being issued), open, paid, void, uncollectible, or empty (nothing to bill; never sent) */
            readonly status: string;
            readonly stripe_invoice_id?: string;
            readonly total: string;
            /** Format: int64 */
            readonly total_micros: number;
        };
        readonly OperatorInvoicesOut: {
            readonly invoices: readonly components["schemas"]["OperatorInvoiceOut"][];
        };
        readonly OrganizationListOut: {
            readonly organizations: readonly components["schemas"]["OrganizationOut"][];
        };
        readonly OrganizationOut: {
            /** Format: date-time */
            readonly created_at: string;
            /** @description The organization's identifier */
            readonly id: string;
            /** @description Display name */
            readonly name: string;
            /** @description URL-safe short name, unique across PlayerSense */
            readonly slug: string;
        };
        readonly PendingInvitationListOut: {
            readonly invitations: readonly components["schemas"]["PendingInvitationOut"][];
        };
        readonly PendingInvitationOut: {
            /** Format: date-time */
            readonly expires_at: string;
            readonly invited_by?: string;
            /** @description The organization's display name */
            readonly organization: string;
            readonly organization_slug: string;
            /** @description The role's display name */
            readonly role: string;
        };
        readonly PersonOut: {
            readonly avatar_url?: string;
            readonly email: string;
            /** @description False means read-only until the address is confirmed */
            readonly email_verified: boolean;
            readonly id: string;
            readonly name?: string;
        };
        readonly PlanDetailOut: {
            readonly active: boolean;
            readonly currency: string;
            readonly current?: components["schemas"]["RateCardOut"] | null;
            readonly description?: string;
            readonly entitlements?: {
                readonly [key: string]: number;
            };
            readonly features: readonly string[];
            /** @description month, year — the ones this plan can be paid by */
            readonly intervals?: readonly string[];
            readonly is_default: boolean;
            readonly key: string;
            /** Format: int64 */
            readonly monthly_price_micros: number;
            readonly name: string;
            readonly scheduled?: components["schemas"]["RateCardOut"] | null;
            readonly subscribable: boolean;
            /** Format: int64 */
            readonly yearly_price_micros: number;
        };
        readonly PlanOut: {
            /** @description The plan's allowances, with any per-account override already applied */
            readonly entitlements?: {
                readonly [key: string]: number;
            };
            readonly key: string;
            /** Format: int64 */
            readonly monthly_price_micros: number;
            readonly name: string;
        };
        readonly PlansOut: {
            readonly plans: readonly components["schemas"]["PlanDetailOut"][];
        };
        readonly PlayerDayOut: {
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly events: number;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
            /** Format: int64 */
            readonly sessions: number;
        };
        readonly PlayerListOut: {
            /**
             * Format: int64
             * @description Absent on the last page
             */
            readonly next_offset?: number;
            readonly players: readonly components["schemas"]["PlayerSummaryOut"][];
        };
        readonly PlayerProfileOut: {
            readonly days: readonly components["schemas"]["PlayerDayOut"][];
            readonly events: readonly components["schemas"]["EventOccurrenceOut"][];
            readonly id: string;
            /** @description The installs this profile covers */
            readonly installs: readonly string[];
            /** @description install or account */
            readonly kind: string;
            readonly summary: components["schemas"]["PlayerSummaryOut"];
        };
        readonly PlayerSummaryOut: {
            /** Format: int64 */
            readonly active_days: number;
            readonly app_version: string;
            readonly country?: string;
            /** Format: date-time */
            readonly first_purchase_at?: string | null;
            /** Format: date-time */
            readonly first_seen: string;
            readonly install_id: string;
            /** Format: date-time */
            readonly last_purchase_at?: string | null;
            /** Format: date-time */
            readonly last_seen: string;
            readonly platform: string;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
            /** Format: int64 */
            readonly sessions: number;
            /** @description active, lapsed or churned */
            readonly status: string;
        };
        readonly PortalOut: {
            /** @description Stripe's customer portal; redirect the browser here */
            readonly url: string;
        };
        readonly PricePointOut: {
            readonly currency: string;
            /** Format: int64 */
            readonly price_micros: number;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
        };
        readonly ProjectListOut: {
            readonly projects: readonly components["schemas"]["ProjectOut"][];
        };
        readonly ProjectOut: {
            /** Format: date-time */
            readonly created_at: string;
            readonly environments?: readonly components["schemas"]["EnvironmentOut"][];
            readonly id: string;
            readonly name: string;
            readonly notes?: readonly string[];
            /** @description standard, child_directed or kids_strict */
            readonly policy_tier: string;
            /**
             * Format: int32
             * @description Raw event retention, in days
             */
            readonly retention_days: number;
            readonly slug: string;
        };
        readonly ProjectRefOut: {
            readonly id: string;
            readonly name: string;
            readonly slug: string;
        };
        readonly PurchaseOut: {
            readonly app_version: string;
            /** Format: date-time */
            readonly at: string;
            readonly country?: string;
            readonly currency: string;
            readonly event_id: string;
            readonly first_purchase: boolean;
            readonly install_id: string;
            readonly platform: string;
            readonly player_id?: string;
            readonly product_id: string;
            /** Format: int64 */
            readonly revenue_micros: number;
            readonly session_id?: string;
            readonly store: string;
        };
        readonly PurchasesOut: {
            /** @description Pass as before for the next page; absent on the last */
            readonly next?: string;
            readonly purchases: readonly components["schemas"]["PurchaseOut"][];
            /** @description Always false: these are purchases the game reported */
            readonly verified: boolean;
        };
        readonly RateCardOut: {
            /** Format: date-time */
            readonly created_at: string;
            /** @description Self-asserted; operator views only */
            readonly created_by?: string;
            readonly currency: string;
            /** Format: date-time */
            readonly effective_from: string;
            readonly id: string;
            readonly meter: string;
            readonly mode: string;
            readonly note?: string;
            /** @description current, scheduled or superseded */
            readonly status: string;
            readonly tiers: readonly components["schemas"]["TierOut"][];
        };
        readonly RateCardsOut: {
            readonly cards: readonly components["schemas"]["RateCardOut"][];
        };
        readonly ReceivableOut: {
            /**
             * Format: date-time
             * @description When the current period closes into an invoice
             */
            readonly next_invoice_at?: string | null;
            /**
             * Format: int64
             * @description Invoiced and not yet paid
             */
            readonly open_micros: number;
            /**
             * Format: int64
             * @description Of that, past its due date
             */
            readonly overdue_micros: number;
            /**
             * Format: date-time
             * @description When the first unpaid invoice passed its due date
             */
            readonly overdue_since?: string | null;
            /**
             * Format: int64
             * @description Used and not yet invoiced, including usage not yet settled
             */
            readonly unbilled_micros: number;
        };
        readonly RecentSessionsOut: {
            readonly sessions: readonly components["schemas"]["SessionOut"][];
        };
        readonly RevenueDayOut: {
            readonly by_store?: {
                readonly [key: string]: number;
            };
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
            readonly withheld?: boolean;
        };
        readonly RevenueOut: {
            /** @description Buyers are distinct counts from sketches */
            readonly approximate: boolean;
            readonly countries: readonly components["schemas"]["RevenueSliceOut"][];
            /** @description Every currency summed into these figures */
            readonly currencies: readonly string[];
            readonly days: readonly components["schemas"]["RevenueDayOut"][];
            /** Format: date-time */
            readonly from: string;
            readonly heatmap: readonly (readonly number[])[];
            readonly new_payers: readonly components["schemas"]["NewPayersOut"][];
            readonly price_points: readonly components["schemas"]["PricePointOut"][];
            readonly products: readonly components["schemas"]["RevenueSliceOut"][];
            readonly spender_tiers: readonly components["schemas"]["SpenderTierOut"][];
            readonly stores: readonly components["schemas"]["RevenueSliceOut"][];
            /** Format: int64 */
            readonly suppressed_cells: number;
            /** Format: int64 */
            readonly suppression_threshold: number;
            readonly timezone: string;
            /** Format: date-time */
            readonly to: string;
            readonly totals: components["schemas"]["RevenueTotalsOut"];
            /** @description Always false: these are purchases the game reported, not store receipts */
            readonly verified: boolean;
        };
        readonly RevenueSliceOut: {
            /** Format: int64 */
            readonly buyers: number;
            readonly key: string;
            /** Format: int64 */
            readonly previous_revenue_micros: number;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
        };
        readonly RevenueTotalsOut: {
            /** Format: int64 */
            readonly buyers?: number | null;
            /** Format: int64 */
            readonly previous_buyers?: number | null;
            /** Format: int64 */
            readonly previous_purchases: number;
            /** Format: int64 */
            readonly previous_revenue_micros: number;
            /** Format: int64 */
            readonly purchases: number;
            /** Format: int64 */
            readonly revenue_micros: number;
        };
        readonly RoleListOut: {
            readonly roles: readonly components["schemas"]["RoleOut"][];
        };
        readonly RoleOut: {
            readonly description?: string;
            readonly id: string;
            /** @description Stable; owner, admin, liveops_manager, developer, growth_manager or billing for the seeded six */
            readonly key: string;
            /** @description What the dashboard shows. Editable, even for a system role */
            readonly name: string;
            readonly permissions: readonly string[];
            /** @description An owner or admin role. Its permissions are computed, and it cannot be created */
            readonly system: boolean;
        };
        readonly SDK: {
            /** @description unity, unreal, godot, ios, android, javascript… */
            readonly name: string;
            readonly version: string;
        };
        readonly ScheduleIn: {
            /** Format: date-time */
            readonly anchor: string;
            /** @description A Go duration: 2h, 30m, 120h */
            readonly duration: string;
            /** Format: int64 */
            readonly interval?: number;
            readonly mode: string;
            readonly timezone?: string;
            /** Format: date-time */
            readonly until?: string | null;
            readonly weekdays?: readonly number[];
        };
        readonly ScheduleOut: {
            /**
             * Format: date-time
             * @description The first instant it is on; its date parts are what recurs
             */
            readonly anchor: string;
            /** @description How long one occurrence lasts, as a Go duration: 2h, 120h */
            readonly duration: string;
            /**
             * Format: int64
             * @description Periods to skip: 3 with daily is every third day
             */
            readonly interval?: number;
            /** @description once, hourly, daily, weekly, monthly or yearly */
            readonly mode: string;
            /** @description An IANA name. Empty means UTC */
            readonly timezone?: string;
            /**
             * Format: date-time
             * @description When repetition stops. Absent means evergreen
             */
            readonly until?: string | null;
            /** @description 0 is Sunday. Empty means the anchor's own weekday */
            readonly weekdays?: readonly number[];
        };
        readonly SegmentListOut: {
            readonly segments: readonly components["schemas"]["SegmentOut"][];
        };
        readonly SegmentOut: {
            /** @description An archived segment is left out of the next publish */
            readonly archived: boolean;
            /** Format: date-time */
            readonly created_at: string;
            readonly description?: string;
            readonly id: string;
            /** @description The stable name rules refer to. Immutable */
            readonly key: string;
            readonly name: string;
            /**
             * Format: byte
             * @description The audience, as internal/targeting parses it
             */
            readonly rule: string;
            /** Format: date-time */
            readonly updated_at: string;
        };
        readonly SessionDayOut: {
            /** Format: int64 */
            readonly active_players: number;
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly duration_ms: number;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly previous_duration_ms: number;
            /** Format: int64 */
            readonly previous_sessions: number;
            /** Format: int64 */
            readonly sessions: number;
        };
        readonly SessionLengthOut: {
            /** Format: int64 */
            readonly from_ms: number;
            /** Format: int64 */
            readonly sessions: number;
            /**
             * Format: int64
             * @description Absent on the last rung, which has no end
             */
            readonly to_ms?: number;
        };
        readonly SessionOut: {
            readonly app_version: string;
            readonly country?: string;
            /** Format: int64 */
            readonly duration_ms: number;
            /** Format: int64 */
            readonly events: number;
            readonly install_id: string;
            /** Format: date-time */
            readonly last_event_at: string;
            /** @description ended, timed_out, or open (an event in the last thirty minutes) */
            readonly outcome: string;
            readonly platform: string;
            readonly player_id?: string;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly revenue_micros?: number;
            readonly session_id: string;
            /** Format: date-time */
            readonly started_at: string;
        };
        readonly SessionOutcomeOut: {
            /** @description ended (a session_end arrived) or timed_out (none did; the SDK closed it) */
            readonly outcome: string;
            /** Format: int64 */
            readonly sessions: number;
        };
        readonly SessionPlatformOut: {
            /** Format: int64 */
            readonly duration_ms: number;
            readonly platform: string;
            /**
             * Format: int64
             * @description By the platform players installed on
             */
            readonly player_days: number;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly sessions: number;
        };
        readonly SessionTotalsOut: {
            /** Format: int64 */
            readonly duration_ms: number;
            /** Format: int64 */
            readonly events: number;
            /** Format: int64 */
            readonly median_ms: number;
            /** Format: int64 */
            readonly p90_ms: number;
            /** Format: int64 */
            readonly player_days: number;
            /** Format: int64 */
            readonly playtime_ms: number;
            /** Format: int64 */
            readonly previous_duration_ms: number;
            /** Format: int64 */
            readonly previous_sessions: number;
            /** Format: int64 */
            readonly sessions: number;
        };
        readonly SessionsOut: {
            /**
             * Format: date-time
             * @description The last whole day the session facts hold
             */
            readonly complete_through?: string;
            /** Format: date-time */
            readonly computed_at?: string;
            readonly days: readonly components["schemas"]["SessionDayOut"][];
            /** Format: date-time */
            readonly from: string;
            readonly heatmap: readonly (readonly number[])[];
            readonly lengths: readonly components["schemas"]["SessionLengthOut"][];
            readonly outcomes: readonly components["schemas"]["SessionOutcomeOut"][];
            readonly platforms: readonly components["schemas"]["SessionPlatformOut"][];
            readonly timezone: string;
            /** Format: date-time */
            readonly to: string;
            readonly totals: components["schemas"]["SessionTotalsOut"];
        };
        readonly SpenderTierOut: {
            /** Format: int64 */
            readonly from_micros: number;
            /** Format: int64 */
            readonly payers?: number | null;
            /** Format: int64 */
            readonly revenue_micros?: number | null;
            /**
             * Format: int64
             * @description Absent on the last band, which has no end
             */
            readonly to_micros?: number;
        };
        readonly SubscriptionOut: {
            readonly cancel_at_period_end: boolean;
            /** @description month or year */
            readonly interval?: string;
            /**
             * Format: date-time
             * @description When the paid period ends — and, if canceling, when the plan does
             */
            readonly period_end?: string | null;
            /** @description Stripe's: active, trialing, past_due, canceled, unpaid, … */
            readonly status: string;
        };
        readonly TierIn: {
            /**
             * Format: int64
             * @description Per player, in micros: $0.0005 is 500
             */
            readonly unit_price_micros: number;
            /**
             * Format: int64
             * @description Inclusive. Absent on the last band only
             */
            readonly up_to?: number | null;
        };
        readonly TierOut: {
            /**
             * Format: int64
             * @description The first player position in the band
             */
            readonly from: number;
            /** @description $0.0005, not $0.00 */
            readonly unit_price: string;
            /** Format: int64 */
            readonly unit_price_micros: number;
            /**
             * Format: int64
             * @description The last, inclusive; absent on the last band
             */
            readonly up_to?: number | null;
        };
        readonly TopValueOut: {
            /** Format: int64 */
            readonly count: number;
            /**
             * Format: double
             * @description Of the property's occurrences
             */
            readonly share: number;
            readonly value: string;
        };
        readonly UsageByProjectOut: {
            readonly project: components["schemas"]["ProjectRefOut"];
            readonly totals: components["schemas"]["UsageTotalsOut"];
        };
        readonly UsageDayOut: {
            /** Format: date-time */
            readonly day: string;
            /** Format: int64 */
            readonly events: number;
            readonly final: boolean;
            /** Format: int64 */
            readonly retained_bytes: number;
            /** Format: int64 */
            readonly tracked_players: number;
        };
        readonly UsageOut: {
            readonly days: readonly components["schemas"]["UsageDayOut"][];
            readonly final: boolean;
            /** Format: date-time */
            readonly from: string;
            readonly included?: {
                readonly [key: string]: number;
            };
            readonly projects: readonly components["schemas"]["UsageByProjectOut"][];
            /** Format: date-time */
            readonly to: string;
            readonly totals: components["schemas"]["UsageTotalsOut"];
        };
        readonly UsageTotalsOut: {
            /** Format: int64 */
            readonly events: number;
            /**
             * Format: int64
             * @description The busiest day in the range. Not a sum: adding DAU counts one player once per day
             */
            readonly peak_daily_players: number;
            /**
             * Format: int64
             * @description Archive bytes still inside the retention window on the last day of the range
             */
            readonly retained_bytes: number;
        };
        readonly VariantIn: {
            /** Format: byte */
            readonly audience?: string;
            /**
             * Format: int64
             * @description Hundredths of a percent; defaults to the whole audience
             */
            readonly rollout?: number | null;
            /** Format: byte */
            readonly value: string;
        };
        readonly VariantOut: {
            /**
             * Format: byte
             * @description Who this value is for. Absent means everybody the flag reaches
             */
            readonly audience?: string;
            /**
             * Format: int64
             * @description This variant's own share, within the audience
             */
            readonly rollout: number;
            /**
             * Format: byte
             * @description What they get. Must match the flag's kind
             */
            readonly value: string;
        };
        readonly WebhookOut: {
            readonly received: boolean;
            readonly status?: string;
        };
        readonly WindowOut: {
            /**
             * Format: date-time
             * @description Exclusive, so two back-to-back occurrences never claim one instant
             */
            readonly end: string;
            /** Format: date-time */
            readonly start: string;
        };
    };
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    readonly post_v1_batches: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["IngestBatchIn"];
            };
        };
        readonly responses: {
            /** @description Accepted */
            readonly 202: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["Accepted"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The key is scoped to another environment */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The batch exceeded the body limit, compressed or decoded */
            readonly 413: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The Content-Encoding is not gzip or zstd */
            readonly 415: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The batch could not be queued; retry with backoff */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_cohorts_query: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["CohortQueryIn"];
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CohortResult"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This endpoint needs a reporting key */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The cohort facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The cohort facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_config: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["ConfigIn"];
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ConfigResponse"];
                };
            };
            /** @description Nothing this player would see has changed since the ETag they sent */
            readonly 304: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This endpoint needs a project key, and one belonging to the environment asked for */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The published configuration could not be read; the SDK should keep using its last known good copy */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_invitations: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PendingInvitationListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_invitations_accept: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["AcceptInvitationIn"];
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["AcceptedOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That invitation was sent to a different address, or this account has not verified its own */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That invitation is unknown, already used, revoked or expired */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That organization has as many members as its plan allows */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_metrics_query: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["MetricQueryIn"];
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MetricResult"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This endpoint needs a reporting key */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The summaries cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The summaries could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_organizations_by_org_charges: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** Format: int64 */
                    readonly amount_micros?: number;
                    readonly description: string;
                    /**
                     * @description usage, subscription or adjustment
                     * @default adjustment
                     * @enum {string}
                     */
                    readonly kind?: "usage" | "subscription" | "adjustment";
                    /**
                     * Format: email
                     * @description Your own address. Recorded in the customer's audit log as self-asserted
                     */
                    readonly operator: string;
                    /** @description An idempotency key derived from what happened */
                    readonly reference: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CreditChargedOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description There is not enough credit, and the message names both numbers */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The amount is not positive */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_operator_organizations_by_org_contract: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: email
                     * @description Where invoices are sent
                     */
                    readonly billing_email?: string;
                    /**
                     * @description invoiced or prepaid
                     * @enum {string}
                     */
                    readonly collection: "invoiced" | "prepaid";
                    /**
                     * Format: int64
                     * @description Required for invoiced. Owing this much, billed or not, suspends ingestion
                     */
                    readonly credit_limit_micros?: number | null;
                    /** @description The invoice footer */
                    readonly memo?: string;
                    /**
                     * Format: int64
                     * @description The least a month bills for usage; the difference is a commitment line
                     */
                    readonly minimum_commitment_micros?: number;
                    /**
                     * Format: email
                     * @description Your own address. Recorded in the customer's audit log as self-asserted
                     */
                    readonly operator: string;
                    /**
                     * Format: int64
                     * @description Days after a missed due date before ingestion is suspended. Defaults to 14
                     */
                    readonly overdue_grace_days?: number | null;
                    /**
                     * Format: int64
                     * @description Billed every month, prorated in a part month. $500 is 500000000
                     */
                    readonly platform_fee_micros?: number;
                    /** @description Printed on every invoice */
                    readonly po_number?: string;
                    /**
                     * Format: int64
                     * @description Days an invoice is due after it is issued. Defaults to 30
                     */
                    readonly terms_days?: number | null;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["BillingOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization has a live subscription */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No credit limit, or no billing email, for an invoiced contract */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached, or payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_operator_organizations_by_org_credit_limit: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description Exact limit in micros; omit to return to 20% of paid usage with the floor
                     */
                    readonly credit_limit_micros?: number | null;
                    /** Format: email */
                    readonly operator: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["BillingOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_organizations_by_org_credits: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description Micros of the account's currency. $100 is 100000000
                     */
                    readonly amount_micros?: number;
                    /** @description What a customer reads on their statement */
                    readonly description?: string;
                    /**
                     * Format: date-time
                     * @description Defaults to 90 days for a promo. Not accepted for a purchase
                     */
                    readonly expires_at?: string | null;
                    /**
                     * Format: date-time
                     * @description Defaults to now. Backdate to place an invoice payment where it belongs in the queue
                     */
                    readonly granted_at?: string | null;
                    /**
                     * @description purchase or promo
                     * @enum {string}
                     */
                    readonly kind: "purchase" | "promo";
                    /**
                     * Format: email
                     * @description Your own address. Recorded in the customer's audit log as self-asserted
                     */
                    readonly operator: string;
                    /** @description An idempotency key derived from what happened: a payment intent, a ticket id */
                    readonly reference: string;
                    readonly stripe_reference?: string;
                };
            };
        };
        readonly responses: {
            /** @description That reference had already been used; the existing lot is returned */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CreditGrantedOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The amount is not positive, or an expiry was named for purchased credit */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_operator_organizations_by_org_invoices: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OperatorInvoicesOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_organizations_by_org_invoices_by_invoice_id_credit_notes: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly invoice_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description Whole cents, at most what is still owed on it
                     */
                    readonly amount_micros?: number;
                    /** Format: email */
                    readonly operator: string;
                    /** @description Printed on the credit note */
                    readonly reason: string;
                    /** @description An idempotency key: the same one twice credits once */
                    readonly reference: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OperatorInvoiceOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The invoice is not open */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Not whole cents, or more than is owed */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_organizations_by_org_invoices_by_invoice_id_mark_paid: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly invoice_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** Format: email */
                    readonly operator: string;
                    /** @description The transfer's reference; the same one twice marks it paid once */
                    readonly reference: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OperatorInvoiceOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The invoice is not open */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_operator_organizations_by_org_plan: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** Format: email */
                    readonly operator: string;
                    /** @description The plan's key */
                    readonly plan: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["BillingOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization or plan by that name */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_operator_plans: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlansOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_plans: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["CreatePlanIn"];
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlanDetailOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A plan with that key exists */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An entitlement is misspelled or negative */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_operator_plans_by_plan: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly plan: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** @description Withdrawn plans are not sold; accounts on them stay */
                    readonly active?: boolean | null;
                    readonly description?: string | null;
                    /** @description Replaces the whole map when present */
                    readonly entitlements?: {
                        readonly [key: string]: number;
                    };
                    /** @description Replaces the whole list when present */
                    readonly features?: readonly string[];
                    /** @description true makes this the default and unsets the previous one */
                    readonly is_default?: boolean | null;
                    /** Format: int64 */
                    readonly monthly_price_micros?: number | null;
                    readonly name?: string | null;
                    /** Format: email */
                    readonly operator: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlanDetailOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No plan has that key */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An entitlement is misspelled or negative */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_operator_plans_by_plan_rate_cards: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly plan: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["RateCardsOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No plan has that key */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_operator_plans_by_plan_rate_cards: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly plan: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: date-time
                     * @description Defaults to now; refused if in the past
                     */
                    readonly effective_from?: string | null;
                    /**
                     * @description Only tracked_players today
                     * @default tracked_players
                     * @enum {string}
                     */
                    readonly meter?: "tracked_players";
                    /**
                     * @description graduated (each band at its own price) or volume (the whole month at the band reached)
                     * @default graduated
                     * @enum {string}
                     */
                    readonly mode?: "graduated" | "volume";
                    /** @description Why the price changed, for whoever asks next */
                    readonly note?: string;
                    /**
                     * Format: email
                     * @description Your own address. Stored on the card as self-asserted
                     */
                    readonly operator: string;
                    /** @description Lowest band first */
                    readonly tiers?: readonly components["schemas"]["TierIn"][];
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["RateCardOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No plan has that key */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The plan already has a card starting at that moment */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The tiers leave a gap, overlap, or bound the last band; or the card starts in the past */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_operator_plans_by_plan_subscription: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly plan: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description $99 is 99000000; zero for no monthly price
                     */
                    readonly monthly_price_micros?: number;
                    /** Format: email */
                    readonly operator: string;
                    /**
                     * Format: int64
                     * @description $999 is 999000000; zero for no yearly price
                     */
                    readonly yearly_price_micros?: number;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlanDetailOut"];
                };
            };
            /** @description The operator token is missing or wrong */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No plan has that key */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Neither price named, or not whole cents */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached, or payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OrganizationListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["CreateOrganizationIn"];
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OrganizationOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An organization with that slug already exists */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["OrganizationOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No organization has that slug, or you are not a member of it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_audit: {
        readonly parameters: {
            readonly query?: {
                /** @description One action name, exactly */
                readonly action?: string;
                /** @description Newest first, up to 200 */
                readonly limit?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["AuditListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_billing: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["BillingOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This organization has no billing account yet */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_billing_auto_topup: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description Absent leaves the stored value alone
                     */
                    readonly amount_micros?: number | null;
                    readonly enabled: boolean;
                    /**
                     * Format: int64
                     * @description Absent leaves the stored value alone
                     */
                    readonly threshold_micros?: number | null;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["BillingOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The settings cannot work together, or there is no saved card to charge; the message says why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be asked whether a card is saved */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_billing_invoices: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["InvoicesOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_billing_invoices_by_invoice_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly invoice_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["InvoiceOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No such invoice */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_billing_ledger: {
        readonly parameters: {
            readonly query?: {
                /** @description Newest first, up to 200 */
                readonly limit?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LedgerOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_billing_portal: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PortalOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached, or payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_billing_subscription: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * @description month or year
                     * @enum {string}
                     */
                    readonly interval: "month" | "year";
                    /** @description The plan's key, e.g. enterprise */
                    readonly plan: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CheckoutOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization already has a subscription */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The plan cannot be paid at that interval */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached, or payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_billing_top_ups: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: int64
                     * @description Whole cents, $5 to $50,000. $100 is 100000000
                     */
                    readonly amount_micros?: number;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CheckoutOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Below $5, above $50,000, or not whole cents */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Stripe could not be reached, or payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_invitations: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["InvitationListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_invitations: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * @description Defaults to true
                     * @default true
                     */
                    readonly all_projects?: boolean | null;
                    /**
                     * Format: email
                     * @description The address to invite. The invitation can only be accepted by a Firebase account that has verified it
                     */
                    readonly email: string;
                    /** @description Project slugs, when all_projects is false */
                    readonly projects?: readonly string[];
                    /** @description The role's key, resolved within this organization */
                    readonly role: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["InvitationCreatedOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That address is already a member, or already has an outstanding invitation */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Too many invitations to that address or from that organization in the last hour */
            readonly 429: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The email could not be sent; no invitation was created */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_invitations_by_invitation_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly invitation_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["InvitationOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That invitation is not outstanding */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_members: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MemberListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_members_by_member_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                /** @description The membership's identifier, not the user's */
                readonly member_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MemberOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_members_by_member_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                /** @description The membership's identifier, not the user's */
                readonly member_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description No Content */
            readonly 204: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content?: never;
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That member is the organization's only owner */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_members_by_member_id_access: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly member_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** @description Required. true grants every project, now and later */
                    readonly all_projects: boolean | null;
                    /** @description Project slugs. Ignored when all_projects is true */
                    readonly projects?: readonly string[];
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MemberOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description One of those projects does not exist in this organization */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_members_by_member_id_role: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly member_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** @description The role's key, resolved within this organization */
                    readonly role: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MemberOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That member is the organization's only owner */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This organization has no role with that key */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ProjectListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization to create the project under */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly name: string;
                    /**
                     * @description standard, child_directed or kids_strict
                     * @default standard
                     * @enum {string}
                     */
                    readonly policy_tier?: "standard" | "child_directed" | "kids_strict";
                    /**
                     * Format: int32
                     * @description Raw retention; one of 7, 30, 90, 180 or 365. Defaults to 30
                     * @enum {integer|null}
                     */
                    readonly retention_days?: 7 | 30 | 90 | 180 | 365 | null;
                    /** @description URL-safe short name, unique within the organization */
                    readonly slug: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ProjectOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A project with that slug already exists in the organization */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ProjectOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_analytics_by_environment_cohorts_query: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly CohortQueryIn: components["schemas"]["CohortQueryIn"];
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["CohortResult"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The cohort facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The cohort facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["EventCatalogueOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events_by_name: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
                /** @description hour or day (default) */
                readonly interval?: "hour" | "day";
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
                readonly name: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["EventDetailOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The project has never sent this event */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_events_by_name_occurrences: {
        readonly parameters: {
            readonly query?: {
                /** @description Newest first, up to 100 */
                readonly limit?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
                readonly name: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["EventOccurrencesOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_flags_exposures: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagExposureListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_flags_by_flag_exposures: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
                /** @description One config version; every version when absent */
                readonly version?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
                readonly flag: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagExposureOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No exposure of this flag in the range */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_analytics_by_environment_metrics_query: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly MetricQueryIn: components["schemas"]["MetricQueryIn"];
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MetricResult"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The summaries cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The summaries could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_players: {
        readonly parameters: {
            readonly query?: {
                /** @description active (seen within 7 days), lapsed (7 to 30) or churned (over 30) */
                readonly status?: "active" | "lapsed" | "churned";
                /** @description The platform the player installed on */
                readonly platform?: string;
                /** @description The country the player installed in */
                readonly country?: string;
                /** @description paying or never */
                readonly spend?: "paying" | "never";
                /** @description last_seen (default), first_seen, revenue, playtime, sessions or active_days; newest or largest first */
                readonly sort?: "last_seen" | "first_seen" | "revenue" | "playtime" | "sessions" | "active_days";
                /** @description Up to 200 */
                readonly limit?: number;
                /** @description Up to 10,000; narrow the filters to reach further */
                readonly offset?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlayerListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_players_by_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
                /** @description An install id (a UUID) or the player id the game signed the player in with */
                readonly id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlayerProfileOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description No such install or player in this project */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_purchases: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
                /** @description Comma-separated stores: app_store, google_play, or what the game reported */
                readonly store?: string;
                /** @description Comma-separated ISO country codes */
                readonly country?: string;
                /** @description Comma-separated product ids */
                readonly product?: string;
                /** @description Newest first, up to 200 */
                readonly limit?: number;
                /** @description The next cursor of the previous page */
                readonly before?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PurchasesOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_revenue: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
                /** @description Comma-separated stores: app_store, google_play, or what the game reported */
                readonly store?: string;
                /** @description Comma-separated ISO country codes */
                readonly country?: string;
                /** @description Comma-separated product ids */
                readonly product?: string;
                /** @description IANA timezone for the weekday-by-hour heatmap; defaults to UTC */
                readonly tz?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["RevenueOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_sessions: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD, inclusive. Defaults to six days before to */
                readonly from?: string;
                /** @description YYYY-MM-DD, inclusive. Defaults to today (UTC) */
                readonly to?: string;
                /** @description IANA timezone for the weekday-by-hour heatmap; defaults to UTC */
                readonly tz?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SessionsOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_analytics_by_environment_sessions_recent: {
        readonly parameters: {
            readonly query?: {
                /** @description Newest first, up to 200 */
                readonly limit?: number;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["RecentSessionsOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not read this project's analytics */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or environment does not exist, or you cannot see it */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts cannot answer this question, and the body explains why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The facts could not be read */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_config_by_environment_draft: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description development, staging or production */
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["DraftOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This project has no such environment */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_config_by_environment_publish: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly environment: "development" | "staging" | "production";
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly note?: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ConfigVersionOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The configuration does not compile; the message names the flag or event and why */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_flags: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_flags: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly description?: string;
                    /**
                     * Format: byte
                     * @description Required: a flag with no answer makes the game depend on the network
                     */
                    readonly fallback: string;
                    /** @description What the game reads. Cannot be changed afterwards */
                    readonly key: string;
                    /**
                     * @description bool, string, number or json. Cannot be changed afterwards
                     * @enum {string}
                     */
                    readonly kind: "bool" | "string" | "number" | "json";
                    readonly name: string;
                    /**
                     * Format: int64
                     * @description Hundredths of a percent; defaults to 10000, the whole game
                     */
                    readonly rollout?: number | null;
                    readonly rules?: readonly components["schemas"]["VariantIn"][];
                    readonly salt?: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A flag with that key already exists */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A value does not match the flag's kind, or a rule cannot be resolved */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_flags_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_projects_by_project_flags_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** @description Absent leaves it as it is; false un-archives */
                    readonly archived?: boolean | null;
                    readonly description?: string;
                    /** Format: byte */
                    readonly fallback: string;
                    readonly name: string;
                    /** Format: int64 */
                    readonly rollout?: number | null;
                    readonly rules?: readonly components["schemas"]["VariantIn"][];
                    readonly salt?: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A value does not match the flag's kind, or a rule cannot be resolved */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_projects_by_project_flags_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["FlagOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That flag is already archived */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_keys: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["APIKeyListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or key does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_keys: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * @description development, staging or production
                     * @enum {string}
                     */
                    readonly environment: "development" | "staging" | "production";
                    /**
                     * @description project for a game binary, reporting for analytics reads, server for server-to-server
                     * @enum {string}
                     */
                    readonly kind: "project" | "reporting" | "server";
                    /** @description A label, so a dashboard row is identifiable */
                    readonly name?: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["APIKeyCreatedOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or key does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_projects_by_project_keys_by_key_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                /** @description The key to revoke */
                readonly key_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["APIKeyOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization, project or key does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That key is already revoked */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_liveops_events: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LiveOpsEventListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_liveops_events: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /**
                     * Format: byte
                     * @description Absent means every player
                     */
                    readonly audience?: string;
                    /** @description Immutable */
                    readonly key: string;
                    /** @description The studio's own type. Not an enum: PlayerSense does not know what kinds of thing a game has */
                    readonly kind: string;
                    readonly name: string;
                    /**
                     * Format: byte
                     * @description Whatever the game needs. PlayerSense does not read it
                     */
                    readonly payload: string;
                    /** Format: int64 */
                    readonly priority?: number;
                    /** Format: int64 */
                    readonly rollout?: number | null;
                    readonly salt?: string;
                    readonly schedule: components["schemas"]["ScheduleIn"];
                    /**
                     * @description draft, active, paused or archived. Defaults to draft
                     * @default draft
                     * @enum {string}
                     */
                    readonly status?: "draft" | "active" | "paused" | "archived";
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LiveOpsEventOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A LiveOps event with that key already exists */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The schedule or the audience is not valid, and the message says which */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_liveops_events_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LiveOpsEventOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_projects_by_project_liveops_events_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** Format: byte */
                    readonly audience?: string;
                    readonly kind: string;
                    readonly name: string;
                    /** Format: byte */
                    readonly payload: string;
                    /** Format: int64 */
                    readonly priority?: number;
                    /** Format: int64 */
                    readonly rollout?: number | null;
                    readonly salt?: string;
                    readonly schedule: components["schemas"]["ScheduleIn"];
                    /**
                     * @default draft
                     * @enum {string}
                     */
                    readonly status?: "draft" | "active" | "paused" | "archived";
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LiveOpsEventOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The schedule or the audience is not valid, and the message says which */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_projects_by_project_liveops_events_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["LiveOpsEventOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That LiveOps event is already archived */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_segments: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SegmentListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_organizations_by_org_projects_by_project_segments: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    readonly description?: string;
                    /** @description The stable name rules will refer to. Cannot be changed afterwards */
                    readonly key: string;
                    /** @description Display name */
                    readonly name: string;
                    /**
                     * Format: byte
                     * @description The audience. An empty rule matches every player
                     */
                    readonly rule: string;
                };
            };
        };
        readonly responses: {
            /** @description Created */
            readonly 201: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SegmentOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A segment with that key already exists */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The rule is malformed, names a segment that does not exist, or refers to itself */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_projects_by_project_segments_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SegmentOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly put_v1_organizations_by_org_projects_by_project_segments_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                readonly org: string;
                readonly project: string;
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": {
                    /** @description Absent leaves it as it is; false un-archives */
                    readonly archived?: boolean | null;
                    readonly description?: string;
                    readonly name: string;
                    /** Format: byte */
                    readonly rule: string;
                };
            };
        };
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SegmentOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The rule is malformed, names a segment that does not exist, or refers to itself */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly delete_v1_organizations_by_org_projects_by_project_segments_by_key: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
                /** @description The project's slug */
                readonly project: string;
                /** @description The object's key, unique within the project */
                readonly key: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["SegmentOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization or project does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description That segment is already archived */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_roles: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The organization's slug */
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["RoleListOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_organizations_by_org_usage: {
        readonly parameters: {
            readonly query?: {
                /** @description YYYY-MM-DD. Defaults to the first of the current month */
                readonly from?: string;
                /** @description YYYY-MM-DD. Defaults to today */
                readonly to?: string;
            };
            readonly header?: never;
            readonly path: {
                readonly org: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["UsageOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The organization does not exist */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The range is not two dates, ends before it starts, or is longer than 400 days */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_pricing: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["PlansOut"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_privacy_erasures: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["ErasureIn"];
            };
        };
        readonly responses: {
            /** @description Accepted */
            readonly 202: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErasureOut"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This endpoint needs a server key */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be recorded, so nothing was started; send it again */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_privacy_erasures_by_request_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The identifier returned when the request was submitted */
                readonly request_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErasureOut"];
                };
            };
            /** @description No such erasure request for this project */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_privacy_exports: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody: {
            readonly content: {
                readonly "application/json": components["schemas"]["ExportIn"];
            };
        };
        readonly responses: {
            /** @description Accepted */
            readonly 202: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ExportOut"];
                };
            };
            /** @description The key is missing, unknown or revoked */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This endpoint needs a server key */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This actor is being erased, so there is nothing left to export */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_privacy_exports_by_request_id: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The identifier returned when the request was submitted */
                readonly request_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ExportOut"];
                };
            };
            /** @description No such export request for this project */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_privacy_exports_by_request_id_download: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path: {
                /** @description The identifier returned when the request was submitted */
                readonly request_id: string;
            };
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content?: never;
            };
            /** @description The bundle has expired or was removed by an erasure */
            readonly 404: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The bundle is not ready yet */
            readonly 409: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description The request could not be validated. */
            readonly 422: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly post_v1_stripe_webhook: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["WebhookOut"];
                };
            };
            /** @description The signature does not verify */
            readonly 400: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description Payments are not configured */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
    readonly get_v1_users_me: {
        readonly parameters: {
            readonly query?: never;
            readonly header?: never;
            readonly path?: never;
            readonly cookie?: never;
        };
        readonly requestBody?: never;
        readonly responses: {
            /** @description OK */
            readonly 200: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["MeOut"];
                };
            };
            /** @description No usable sign-in was presented */
            readonly 401: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description This account may not do that */
            readonly 403: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description A sign-in could not be verified right now; the credential is not necessarily bad */
            readonly 503: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
            /** @description An unexpected error occurred. */
            readonly default: {
                headers: {
                    readonly [name: string]: unknown;
                };
                content: {
                    readonly "application/json": components["schemas"]["ErrorResponse"];
                };
            };
        };
    };
}
