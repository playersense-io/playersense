# 0031. Absence is NULL, not the empty string

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §20, §23, §96; [ADR 0004](0004-installation-identity.md)

## Context

`events_raw` stored `player_id` as a plain `String` and used `''` to mean "this event has no
player". Every derived expression then had to know the convention:

```sql
actor_key = if(player_id != '', player_id, toString(install_id))
```

The schema had exactly one `Nullable` column — `cohort_date` — so the convention was neither
consistent nor written down. It was noticed while adding `account_lifetime`, where the natural
filter reads `WHERE actor_key != ''` and is **always true**, because `actor_key` is itself a
coalesce that falls back to the install. That filter would have put every anonymous
installation into an account table.

The usual objections to `Nullable` in ClickHouse are storage and speed. Both were measured on
this machine rather than assumed, over 10M rows with 30% of them identified:

| | `String` + `''` | `Nullable` | |
|---|---|---|---|
| `player_id` compressed | 22,458,493 B | 22,504,224 B | **+0.2%** |
| `LowCardinality` column compressed | 62.04 KiB | 60.95 KiB | **Nullable is smaller** |
| Filter (`!= ''` vs `IS NOT NULL`) | 0.008 s | **0.003 s** | ~2.5× faster |
| `uniq` over the filtered set | 0.022 s | 0.027 s | ~20% slower |

The null mask is a run of zeroes, so it compresses to almost nothing, and a filter reads a
bitmask instead of comparing strings. For a `LowCardinality` column NULL is cheaper than
carrying `''` as a dictionary entry. The storage argument does not hold and the speed argument
points both ways.

## Decision

**A column is `Nullable` when absence is a distinct state. It keeps a sentinel when "unknown"
is a value the product shows.**

That line matters, because the two look alike and are not:

- `player_id`, `currency`, `att_status`, `consent_policy_version` → **Nullable**. An anonymous
  event has no player; a non-revenue event has no currency. There is no such thing as a player
  whose identifier is the empty string, so `''` was encoding "absent" in a slot that should not
  have had one.
- `install_country`, `install_platform`, `first_app_version`, `country_code` →
  **unchanged**. These are reporting *dimensions*, and "unknown" is a bucket a
  chart displays beside "TR" and "DE". Making them Nullable would change the GROUP BY key of
  five tables to say something the product does not mean.

Derived columns that coalesce — `actor_key`, `device_key`, `device_id` — stay non-null by
definition, and `actor_key`'s expression becomes `ifNull(player_id, toString(install_id))`.

**Go converts at the boundary.** Go has one empty string and no concept of absence, so
`internal/writer`'s `nullable()` turns `""` into a SQL NULL at the single point a value crosses
into the database. Reads coalesce at the edge instead: the export bundle selects
`ifNull(player_id, '')` because an omitted JSON field is how a document says "nothing here".

## Consequences

The conversion is a place to get it wrong, and getting it wrong is silent. An anonymous event
stored as `''` rather than NULL still satisfies `IS NOT NULL`, so it would flow into
`account_lifetime` as a single account whose key is the empty string — **one enormous fake
player that every account-level targeting rule would match**, growing with every anonymous
install, reported by nothing.

That is not hypothetical. Deliberately reverting `nullable(ev.PlayerID)` to `ev.PlayerID`
makes `TestAnAnonymousEventStoresNoPlayerAtAll` fail with exactly that:

```
0 rows have a NULL player, want 1
1 rows store the empty string instead of NULL
account_lifetime holds 2 accounts, want exactly the identified one
```

A materialized view over a Nullable column needs `assumeNotNull` where its target is not
nullable — `sipHash64` of a `Nullable(String)` is a `Nullable(UInt64)`, and relying on the
coercion is silent when it works and silent when it does not.

Migration 00001 was edited in place and the cell re-migrated from empty, rather than adding an
`ALTER`. Pre-launch, with no data, a clean schema is worth more than a faithful history of a
decision that was wrong.

## Alternatives considered

**Leave it as `''` and document the convention.** Cheaper, and the measurement says it costs
nothing at rest. Rejected because the convention has to be re-derived at every call site: the
`actor_key != ''` trap was found by reading the schema closely, and the next person will not.

**Make everything Nullable.** Consistent, and wrong. A dimension's "unknown" bucket is a value
the product renders; turning it into NULL would push the sentinel from the schema into every
chart's presentation layer, which is further from where the meaning is.

## Enforcement

`cmd/analytics-writer`'s `TestAnAnonymousEventStoresNoPlayerAtAll` asserts that an anonymous
event stores NULL rather than `''`, that it produces no account row, and that `actor_key` still
coalesces so a player timeline is unaffected. Confirmed to fail when the conversion is removed.

`migrate verify` continues to classify every column (§96), so a Nullable column added without a
`class=` comment fails the build rather than the audit.
