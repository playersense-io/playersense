# 0007. Every column carries a data class, and the build checks it

**Status:** Accepted
**Date:** 2026-09-06
**Relates to:** project.md §96, §103

## Context

Three documents have to describe what PlayerSense collects: the Article 30 processing
record, the Google Play Data Safety declaration, and Apple's `PrivacyInfo.xcprivacy`
manifest. Each is written by a person, from the schema, at a different time.

Maintained by hand they drift apart within a release or two, and each becomes a statement
nobody can verify. The drift is not carelessness — it is that a column added on a Tuesday
does not notify three documents.

## Decision

**Every column in both PostgreSQL databases and in ClickHouse carries a classification in
its column comment**, from a closed set:

```text
identifier    identifies a person or device
behavioural   what a person did
derived       computed from behaviour, still attributable
financial     revenue, purchases, entitlements
none          organization, project, infrastructure
```

`migrate verify` reads them back out of `pg_description` and `system.columns.comment`. A
column with no classification, or with one outside the set, is a failure.

The classification is then the **single source** the three documents are generated from.
Generated, they cannot describe a field that does not exist or omit one that does.

## Consequences

- Adding a column means classifying it. This is a deliberate friction of about ten seconds,
  placed where the decision is being made rather than in a review three weeks later.
- The check reads the **catalog**, not the schema files. The question is what the database
  actually holds, and a column added by hand or left behind by a half-applied migration is
  exactly the one a schema-file check would miss.
- Views are excluded in ClickHouse: a view column inherits whatever expression produced it
  and has no comment of its own, so requiring one would report a violation nothing can fix.
- The classification is what makes 0008's residency rule checkable at all — without a
  machine-readable `identifier` class there is nothing to search for.

## Alternatives considered

**A separate registry file mapping tables to classifications.** A second thing to update,
which is the original problem with an extra step.

**Naming conventions (`pii_`, `id_`).** Unenforceable, and wrong the first time a column is
renamed for readability.

**A linter over the Ent schema definitions.** Would not have caught the marker tables,
which are created in raw SQL — and those are exactly the kind of table that gets forgotten.
They now carry comments too.

## Enforcement

- `apps/backend/internal/dataclass/` — the closed set and `Valid`.
- `apps/backend/ent/schema/*/**.go` — every field carries `.Comment(dataclass.…)`.
- `db/clickhouse/migrations/*.sql` — every column carries `COMMENT 'class=…'`.
- `apps/backend/cmd/migrate/main.go` — `verify`, across all three databases.

Proven negatively: adding `player_device_id text` with `class=identifier` to the global
tier makes `migrate verify` exit non-zero and name the column.
