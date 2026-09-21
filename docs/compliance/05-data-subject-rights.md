# 05. Data subject rights

## How a request arrives

Through the **studio**, not directly. PlayerSense is a processor
([00](00-roles-and-scope.md)): a player's relationship is with the game they are playing, and
the studio is the controller who must answer them.

Every request is therefore scoped to one project. A studio may ask about its own players and
not anyone else's, and that scoping is enforced rather than assumed — a reporting credential
is bound to one environment of one project (ADR 0011).

## Identifying the subject

The studio supplies the identifier it knows the player by:

- the `actor_key` the studio itself sends, if it sends one; or
- an `install_id`, if the player can produce one.

There is a real limit here and it is a consequence of ADR 0004: because a reinstall produces
a new `install_id`, a player who has reinstalled has more than one, and PlayerSense cannot
link them. A studio that wants a complete answer needs to send its own `actor_key`. Saying
this plainly is better than producing a partial export labelled as complete.

## The four rights

### Access and portability

Everything held about one actor, exported in a documented format, scoped to one project.

The bundle is newline-delimited JSON: one line per record, with a `section` naming what each
line is — the raw events, the device lifetime records, the daily activity and the verified
purchases. It opens with a line stating what the answer covers and what it does not, and
closes with a summary, so a file truncated by any accident is recognisable as one rather than
read as a shorter answer.

It is written to the archive bucket and served back through the API on the
credential the studio already holds — never as a signed bucket URL, which would be a second
credential with different lifetime rules and no revocation, issued for an object holding one
named person's entire history. The bundle itself expires: it is a second copy of everything
held about someone, so it is the last thing that should sit around.

Events come from ClickHouse rather than from the archive, which is a reversal of what §10.4
originally anticipated. It was written when the hot window was a fraction of the retention
tier; [ADR 0024](../adr/0024-the-hot-window-is-a-cache-not-a-retention-limit.md) moved
`events_raw` onto tiered storage, so the cold half of the tier is still in the table, merely
on object storage. The two copies now cover the same span and one of them answers a scoped
query. The residue is worth naming: an event that reached the archive and not ClickHouse —
the two consume the queue independently — is not in the bundle.

**A subject with more history than one bundle holds is refused**, with the number in the
reason, rather than served a shorter file. That is the same instinct as the paragraph above
about reinstalls: a partial answer presented as a complete one is the failure this right
exists to prevent.

### Rectification

In practice a deletion and a re-send.

Analytics events are immutable facts about things that happened, not records that can be
corrected. "The player was on level 4, not level 3, last Tuesday" is not a correction to a
fact; it is a claim that a different event occurred. The honest handling is to delete what
was recorded and let the studio send what it believes to be true.

### Erasure

A tombstone, then an asynchronous purge, then confirmation.

1. The request is recorded with status `received`.
2. A tombstone is written so nothing further is accepted or served for that actor —
   `tombstoned`. This happens inside the submitting request, so the data stops being used in
   the same round trip the studio submitted in.
3. The purge runs — `purging` — across `events_raw`, `player_lifetime` and
   `player_activity_daily`, which are the tables holding device identifiers, plus the
   verified purchases, any export bundle held for that actor, and the archived objects
   holding them.
4. `completed`, with a timestamp. A failure is recorded with a reason and is retryable
   (Appendix C.13).

Asynchronous because ClickHouse deletion is a rewrite of whole parts, and because a
synchronous erasure endpoint would be an availability problem and a denial-of-service vector
at once. The tombstone is what makes the asynchrony safe: the data stops being *used*
immediately even though it stops *existing* over the following hours.

**`completed` waits for a drain window.** Delivery is at-least-once and a queue can hold
messages for hours, so events for an erased actor keep arriving after the purge has run. The
tombstone drops them; if the cache could not answer, the purge is what removes them. A
request that completed on its first pass would be claiming something it cannot know yet, so
the purge keeps running — idempotently, and cheaply once there is nothing left to find — and
the request completes only when the window has closed. `completed` therefore means "and
nothing has arrived since".

**The archive is rewritten, not deleted.** An object holds one project, one environment and
one hour — hundreds of people — so deleting it to erase one of them would destroy everyone
else's events, which the studio is paying to retain. The object is read, the subject's rows
dropped, and what remains written back under a name derived from its contents; only an object
that held nobody else is deleted outright. See
[ADR 0025](../adr/0025-an-erasure-rewrites-the-archive.md).

### Two limits of an erasure, stated rather than implied

**A shared device.** `player_lifetime` and `player_activity_daily` are keyed on the device,
not the actor, and cannot be split: the events that would let the row be rebuilt are the
events being deleted, and past the raw window they are gone anyway. So the row is deleted
whole. Where two accounts signed in on one installation, the second one loses that device's
derived totals and keeps every event. §61 already says of the cohort facts that "a deleted
player simply stops being counted"; this is the same trade one table lower down.

**A device that has gone quiet.** The mapping from a studio's actor key to a device *is* the
event, so beyond the project's retention tier there is nothing left to resolve it with. A
device someone used and abandoned longer ago than that cannot be linked to the name in the
request — `player_lifetime` keeps a row keyed on an install that nothing connects to them. A
request naming the **install** reaches it directly, which is the practical reason the
paragraph above asks for every identifier rather than one.

### Objection and restriction

Handled through the consent columns ([02](02-lawful-basis-and-consent.md)) rather than by
deletion. A restricted actor's rows stop being read while remaining available if the
restriction is lifted — which is what restriction means, and what deletion would make
impossible.

## Backups

This is the part of every erasure story that is usually left vague, so state it.

**Per-actor deletion does not reach into backups.** A backup is a point-in-time image, and
rewriting one destroys the property that makes it a backup.

The position is **restore-and-redact**:

- a backup restored for any reason has pending deletions reapplied **before** it returns to
  service;
- backup retention is bounded and published, so the window during which a deleted actor
  could still exist in an offline image is a known number rather than an open question.

## Timelines

GDPR allows one month, extensible to three for complex requests.

PostgreSQL is fast: `data_deletion_requests` and `verified_purchases` are both indexed on
`(project_uid, actor_key)` and `(project_uid, player_id)`, so finding what is held about one
person there is a lookup.

ClickHouse is not, and the honest version matters more than a reassuring one. `actor_key` is
a MATERIALIZED column and appears in **no** sort key. `device_id` appears in all three
player-linked tables but never as a prefix: it is sixth of seven in `events_raw`, behind
`event_date` and `event_name`, and fourth in `player_activity_daily`. Only `player_lifetime`
places it early enough to seek. So an actor-scoped purge of `events_raw` reads that tenant's
partitions rather than seeking to a range.

That is affordable inside a month and would not be inside an hour, which is why §61 makes
deletion asynchronous and puts a tombstone in front of it. It is also why the purge job is
scoped by project and time range where the request allows it. If per-actor work ever needs
to be fast rather than merely finished, the change is a sort key and therefore a rebuild —
worth knowing before it is promised to anyone.

## How a request is made

Two endpoints, taking a **server key** rather than a reporting one: erasing a studio's
player is not the same privilege as reading its aggregates (§52).

```text
POST /v1/privacy/erasures          {"actor_key": "..."}   → 202, a request id
GET  /v1/privacy/erasures/{id}                            → status and timestamps
POST /v1/privacy/exports           {"actor_key": "..."}   → 202, a request id
GET  /v1/privacy/exports/{id}                             → status and where to collect it
GET  /v1/privacy/exports/{id}/download                    → the bundle
```

Both take a **server key** and no other class. Erasing a studio's player is not the same
privilege as reading its aggregates, and a project key ships inside a game binary.

Submitting the same erasure twice returns the first one: a studio whose request timed out
will send it again, and two records of one erasure would be two audit trails of one event.

An export is refused while an erasure is open for the same actor — assembling the bundle would
create a fresh copy of exactly what the erasure is removing. The order between the two rights
is the studio's to choose, so the refusal says so rather than silently reordering them.

## Status

Built: the request surface, the tombstone, the purge across ClickHouse, PostgreSQL and the
archive, and the export with its own expiry. [09](09-controls-map.md) maps each claim here to
the code and the test that holds it up.

Not built: the **project** tombstone — the writer honours one, and nothing writes it when a
project is deleted, so today a deleted project's queued batches are removed by the purge
rather than skipped — and the restore-and-redact procedure for backups. See
[10-open-items.md](10-open-items.md).
