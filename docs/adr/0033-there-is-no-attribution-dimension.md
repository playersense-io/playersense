# 0033. There is no attribution dimension

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §29.3, §29.13, §98; [ADR 0002](0002-cohorts-without-joins.md),
[ADR 0005](0005-collection-policy-tiers.md), [ADR 0032](0032-a-liveops-event-carries-a-schedule.md)

## Context

`acquisition_source` was the fourth sticky cohort dimension: a `LowCardinality(String)` column
in `events_raw` and another in `player_activity_daily`, a `first_source` aggregate state in
`player_lifetime`, a member of six `Enum8` dim_name columns, a `UNION ALL` arm in three
derivations, a value in `schemas.CohortDimensions`, and a field in the data-subject export
bundle.

**Nothing ever wrote it.** `writer.deviceState` set it to `""` unconditionally, with a comment
saying it "is not yet collected for anyone — the field exists so the cohort key's shape does
not change when it is". Every row in every table carried the empty string, and the query API
offered "acquisition_source" in its `doc:` tag as a dimension a caller could ask for.

Hasan settled what "when it is" meant: *"There will never be mmp data in this project, no ad,
campaign and so on"* and *"this is not an mmp, this is a game analytics tool where we only
analyze game metrics"*. An install's source is something a mobile measurement partner —
AppsFlyer, Adjust — tells you. PlayerSense does not integrate with one and will not, so it is
not a thing this product can know.

## Decision

The dimension is removed everywhere rather than left empty: the column, the aggregate state,
the enum members, the derivation arms, the export field, the dimension list and the API's
offer of it.

`schemas.CohortDimensions` is three: `install_platform`, `install_country`,
`first_app_version`. All three are properties of the player's arrival that the events
themselves carry, which is what made them stampable at write time (ADR 0002). Attribution
never had that property, which is why it needed an `argMinIf` over "first non-empty value"
while the others shared a single `argMin` tuple — the schema was already saying this did not
belong.

**The policy-tier refusals go with it.** §29.15 had `child_directed` refuse an acquisition
breakdown by name. A tier cannot suppress a dimension that does not exist in any tier, so the
refusal and its test are replaced by a stronger and simpler one: the planner refuses **any**
dimension outside the closed list, whatever the tier.

## Consequences

`events_raw` and `player_activity_daily` each lose a `LowCardinality(String)` column and
`player_lifetime` an `AggregateFunction(argMinIf, ...)` state. Three derivations lose a
`UNION ALL` arm apiece, and with it about a quarter of the rows in the breakdown tables they
feed: that arm was materializing a full dimension's worth of cohort-offset cells whose
`dim_value` was always `''`. Per §29.13's sizing, cells that say nothing.

Nothing is lost, because nothing was stored. There is no backfill and no migration path:
`migrate cell reset && migrate cell up` is the whole operation, and it was run.

**If PlayerSense ever does want install source**, this decision has to be reopened rather than
worked around. That is the point of removing it: a permanently-empty column is an invitation
to populate it from whatever the SDK happens to send, and a client-supplied acquisition source
is forgeable by a modified build in exactly the way ADR 0027 forbids for targeting. The honest
version needs a server-side referrer or receipt path, which is a design, not a column.

## Alternatives considered

**Leave the column, keep it empty.** The status quo. It costs a dimension in the public query
API that returns one bucket labelled "" for every cohort, a compliance control document
describing a refusal of a thing that does not exist, and a schema that implies a roadmap item
nobody intends to build.

**Keep it as a studio-supplied free string** — "organic", "our own referral tag". Defensible in
principle, but it is a client-supplied value the server cannot verify, and the product has no
place to set it. It would be an empty column with a better excuse.

**Repurpose it as a §98 advertising control.** Both the ADR 0005 tier table and the
`child_directed` refusals implicitly did this: they described removing advertising data that
never existed. Keeping a control for an absent risk makes the compliance map harder to audit,
not safer.

## Enforcement

`schemas.CohortDimensions` is the closed list, applied by the request schema
(`OneOf(CohortDimensions...)`) and again by the planner — `TestAnUnknownCohortDimensionIsRefused`
asks for `acquisition_source` by name and requires a refusal that says both what was asked and
what is available.

`migrate verify` walks every column in all three databases and fails on one it cannot classify
(§96), so a column reintroduced without a decision does not reach a release quietly. It now
reports 261 ClickHouse columns across 20 tables, down from 264.
