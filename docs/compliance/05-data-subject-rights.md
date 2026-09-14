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

Built on the same asynchronous job machinery as every other long-running operation, with an
actor-scoped variant. The export record lives in the **cell tier** because it names a player
([03](03-residency-and-transfers.md)).

### Rectification

In practice a deletion and a re-send.

Analytics events are immutable facts about things that happened, not records that can be
corrected. "The player was on level 4, not level 3, last Tuesday" is not a correction to a
fact; it is a claim that a different event occurred. The honest handling is to delete what
was recorded and let the studio send what it believes to be true.

### Erasure

A tombstone, then an asynchronous purge, then confirmation.

1. The request is recorded in the cell tier with status `received`.
2. A tombstone is written so nothing further is accepted or served for that actor —
   `tombstoned`.
3. The purge runs — `purging` — across `events_raw`, `player_lifetime` and
   `player_activity_daily`, which are the tables holding device identifiers, plus the
   verified purchases in the cell tier and the archived objects in the cell's bucket.
4. `completed`, with a timestamp. A failure is recorded with a reason and is retryable
   (Appendix C.13).

Asynchronous because ClickHouse deletion is a rewrite of whole parts, and because a
synchronous erasure endpoint would be an availability problem and a denial-of-service vector
at once. The tombstone is what makes the asynchrony safe: the data stops being *used*
immediately even though it stops *existing* over the following hours.

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

The cell tier is fast: `data_deletion_requests` and `verified_purchases` are both indexed on
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

## Status

The schema exists — `data_deletion_requests` is in the cell tier with its status machine —
and the jobs that execute it are not yet built. See [10-open-items.md](10-open-items.md).
Claiming otherwise here would be exactly the kind of statement somebody relies on.
