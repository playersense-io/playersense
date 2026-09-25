# PlayerSense SDK protocol

The language-neutral contract §15 asks for. Every SDK — Unity, Unreal, Godot, Swift, Kotlin,
TypeScript, and the server-side ones — implements this, and the conformance suite at the end
is what makes "implements" checkable rather than asserted.

It exists because §15's other instruction is the important one: **do not independently
reinvent protocol behaviour in every SDK.** Eleven implementations of retry, eleven opinions
about when a session starts and eleven identifier bugs is not a platform.

Keywords are RFC 2119. Section marks like §17 point at `project.md`.

---

## 1. The model

Five nouns, and the whole protocol is how they relate.

| | what it is | lifetime |
|---|---|---|
| **install** | one installation of one game on one device | from first launch to uninstall |
| **session** | one continuous period of play | minutes |
| **actor** | who the studio knows the player as, if it knows | from sign-in |
| **batch** | one flush: the transport unit | one request |
| **event** | one thing that happened | immutable |

A batch is **not** a category of event. It is everything queued since the last flush,
whatever kind — a session start, three gameplay events, a flag exposure and a purchase travel
in one request. That is the point of §4.1: batching is what turns eleven thousand events a
second into twenty-three requests a second.

What sits on the batch is what cannot change inside one flush — the install, the SDK, the
build, the device, the consent. What sits on each event is what can: the name, the time, the
session, the actor, the ordering, the properties, the revenue. `player_id` is per event and
`install_id` is per batch for exactly one reason: somebody can sign in halfway through a
flush, and nobody can change device halfway through one.

---

## 2. The surface

One verb, not one per kind of event.

What a game calls:

```
Initialize(config)                       // local only; never blocks, never touches the network
SetPlayer(actorKey) / ClearPlayer()      // sign-in and sign-out
GrantConsent(policyVersion, attStatus?, gdprApplies?)   // see §5
DenyConsent()                            // a refusal, or a withdrawal
SetContext(scope, key, value)            // the studio's own vocabulary; see §8.1
RemoveContext(scope, key)
Track(name, properties?)                 // the whole analytics API
TrackRevenue(name, amount, currency, properties?)
   -> queued | held for consent | declined, or a rejection (see below)
Flush()                                  // returns immediately; the send is asynchronous
Shutdown()                               // one last attempt, then the client is retired
```

What a **binding** must additionally wire, because a game cannot be asked to:

```
Foreground() / Background()              // from the platform's own lifecycle; drives §4
Tick()                                   // periodically: expiry, backoff, the flush timer
OnResponse(requestId, status, retryAfter, body)   // exactly once per request, §11
InstallId() / Stats() / ContextRevision()         // for a diagnostics screen
```

Two of those are worth stating plainly. `OnResponse` MUST be delivered exactly once for every
request the SDK asked for, **including one that never reached a server** — a request the core
never hears about again is a batch that never retries. And `Shutdown` takes no timeout: the
core makes one attempt and returns, because the real bound is the few hundred milliseconds the
platform gives a process on its way out, which no parameter can extend. Nothing is lost
either way; the batch was spooled before the attempt and goes on the next launch (§7.3).

`Track` MUST distinguish three outcomes from a rejection, and a binding MUST NOT treat any of
the three as a failure:

| | |
|---|---|
| **queued** | Collected under a granted consent, and on its way |
| **held for consent** | In memory only, pending the player's answer (§5) |
| **declined** | Discarded, because the player said no |

A *rejection* — a malformed name, too many properties — is a different thing: it means the
call was wrong and a developer has something to fix. Returning success for a declined event,
as an earlier draft of this document allowed, made "my events are not arriving" unanswerable
on the device: a diagnostics screen could not tell a delivered event from a discarded one.

Rejection MUST be reported **regardless of consent state**. A developer testing with analytics
declined otherwise gets silence for a misspelled event name and discovers it in production.
Validating a name reads the argument and stores nothing, so no consent state forbids it.

Grant and deny are separate verbs because a policy version is meaningless for a refusal, and
one call taking both would make it optional for the case that needs it. A binding MAY present
them as a single `SetConsent(state, …)` over a platform enum — the Unity package does — as
long as granting without a policy version is refused.

There is deliberately **no** `StartSession`. §30 says to do as much session definition as
possible in the SDK, and a session boundary a developer has to remember is a session boundary
that will be wrong in one scene and missing in another. The SDK owns it (§4).

There is deliberately **no** `TrackPurchase` that records money. `TrackRevenue` records an
*analytics* event about a purchase — useful for funnels, not trusted for revenue. The money
fact comes from the studio's backend verifying the receipt against the store (§46–§48), which
is a server-to-server call with a server key, not something a modified client can assert.

`Track` MUST NOT block, MUST NOT allocate unboundedly, and MUST NOT throw. §18's last line is
the one that outranks everything else in this document: **analytics must never crash or
freeze the game.** An SDK that cannot record an event drops it and carries on.

---

## 3. Identity

### install_id

A UUID, generated on first launch, persisted, and sent on every batch.

It MUST survive an app update. It MUST NOT survive an uninstall. It MUST be excluded from
platform backup — which means a **file**, not `NSUserDefaults` and not `SharedPreferences`,
because both of those are copied by iCloud and Android Auto Backup. Restore one onto a second
phone and one installation emits from two devices at once, forever, and every retention curve
built on it is wrong in a way nothing reports.

How a binding excludes it: on iOS, the no-backup flag on the directory. On Android, by putting
the directory *inside* `getNoBackupFilesDir()`, which the platform excludes from Auto Backup
and from device-to-device transfer, rather than by shipping a backup rule the game's manifest
has to merge (ADR 0030). A binding that cannot reach either MUST say so in a log line: the
failure is otherwise invisible in the data, which looks entirely healthy while being wrong.

A reinstall therefore produces a new install and a new player. That is ADR 0004's decision,
not an accident: it slightly overstates installs and it is honest, whereas the alternatives
are device fingerprints §100 forbids.

### actor key

Whatever the studio calls the player — `SetPlayer("player-91827364")`. Optional; many games
never have one.

`SetPlayer` MUST NOT flush. `player_id` travels per event, so a batch that straddles a
sign-in is correct: the events before carry no actor and the events after do. This is the
mirror of the consent rule in §5, and the contrast is worth internalising — the envelope's
split between batch-level and event-level fields is exactly the split between "must flush on
change" and "need not".

`ClearPlayer` on sign-out. Do not reuse one actor key for two people.

### What the SDK never collects

No advertising identifier, ever. No IDFA, no GAID, no vendor identifier, no MAC address, no
serial number, and nothing that composes into a fingerprint (§100). There is no field for any
of it in the envelope, which is the only durable way to enforce it.

---

## 4. Sessions

The SDK owns the boundary.

1. On the first `Track` after `Initialize`, or on returning to the foreground after the
   session has expired, the SDK generates a new `session_id` and emits `session_start`.
2. Every subsequent event carries that `session_id` and an incrementing `sequence`.
3. A session expires after **30 seconds** in the background (default, configurable).
   Returning before that resumes it; returning after starts a new one.
4. `session_end` MAY be emitted on a clean exit. Nothing may depend on it: mobile operating
   systems terminate processes without warning, and §30 says so explicitly. A background
   finalizer closes stale sessions server-side.

`session_start` is a **critical** event (§7), so it is sent immediately rather than waiting
for a batching interval. It carries the install's cohort assignment, and a cohort is assigned
from the time the server *received* the event (§29.4) — so a session start that sits in a
spool for three days is a player counted in the wrong cohort.

---

## 5. Consent

Three states, and the transitions are where SDKs get this wrong.

| state | collect? | spool? | send? |
|---|---|---|---|
| unknown | yes, in memory only | **no** | no |
| granted | yes | yes | yes |
| denied | no | no | no |

**Unknown** is the state at first launch before the player has answered. Events go into a
bounded in-memory buffer — they MUST NOT reach the disk spool, because a spool is a copy on
disk of data collected under no basis. Bound the buffer (200 events is generous) and drop the
oldest beyond it.

**On granted:** stamp the buffer with the granted consent block and promote it to the spool.
The events are now lawful and are sent normally.

**On denied:** discard the buffer. Collect nothing further.

**On withdrawal** (granted → denied): **flush what is already spooled, under the old consent
block, before recording the withdrawal.** Withdrawal is not retroactive — those events were
collected lawfully, and the consent version they were collected under travels on every row
precisely so this stays provable (§97, ADR 0006). Dropping them would be discarding data the
studio is entitled to, and uploading them *after* the state flips would label them with a
basis that was not theirs.

**A batch carries exactly one consent block, which applies to every event in it.** So an SDK
MUST flush at every consent transition. This is the rule that `SetPlayer` deliberately does
not have, and the reason is structural: consent is on the batch, the actor is on the event.

A batch whose `consent.analytics` is false is accepted and discarded by the gateway — it
answers 202 with `dropped` equal to the event count. Do not retry it.

---

## 6. Durability: the queue and the spool

This is the heart of the protocol, and the answer to "what happens when…".

```
Track()  ──▶  ring buffer (memory)
                   │  flush trigger (§7)
                   ▼
              immutable batch  ──▶  spool (disk)  ──▶  POST /v1/batches  ──▶  202  ──▶  delete
```

### The rules

1. **An event is durable when it is in the spool, not when it is tracked.** The loss window
   on an unclean kill is the ring since the last batch was created — bounded by §7's
   triggers, and **zero for critical events**, which create a batch of their own immediately.
2. **A batch is immutable once created.** Same `batch_id`, same events, same order, on every
   attempt, forever. This is §18's rule and it is now load-bearing in a way it was not: the
   gateway derives each event's identity from the batch id and the event's position in it
   (ADR 0026), so an SDK that re-packs a batch between attempts produces different identities
   for the same events and defeats deduplication.
3. **A `batch_id` is never reused for different content.** It is a v4 UUID generated when the
   batch is created. Reusing one is a protocol violation with a consequence: two different
   events acquire one identity and deduplicate into one.
4. **The batch is written to the spool before the first send attempt**, not after. A crash
   between the two must leave the batch on disk, not in flight and gone.
5. **Delete only on 202**, or on a refusal that says retrying is pointless (§9).

### Spool bounds

| bound | default | why this number |
|---|---|---|
| max age | **7 days** | §19.2 clamps `event_time` to seven days past. A batch older than that uploads events whose timestamps the server silently rewrites, so the honest act is to drop it rather than upload provable fiction. |
| max size | 5 MB | Roughly a week of a chatty game, compressed. Beyond it the device is storing more than it will ever send. |
| max batches | 500 | A bound on the drain, so a week offline does not become a thundering herd on reconnect. |

### Eviction

When a bound is reached, drop **oldest first within the lowest priority class present**.

| class | what | rule |
|---|---|---|
| **critical** | `session_start`, anything carrying revenue | never dropped while any normal event remains |
| **normal** | everything else | dropped oldest-first |

§18's rule 8 in a table: drop low-priority analytics before causing the game to fail, and
before losing the events that carry cohort assignment and money.

### Spool integrity

Entries carry a format version and a length prefix. An entry that does not parse truncates
the spool at that point and is discarded — never retried forever, never fatal. A disk that is
full is a failed spool write, which means the batch stays in memory and is subject to
eviction; it is not an exception the game sees.

If a second process holds the spool lock, the SDK runs memory-only and says so in the debug
log. It MUST NOT corrupt a spool another process owns.

---

## 7. When a batch is sent

| trigger | default | note |
|---|---|---|
| a **critical** event | immediately | `session_start` and revenue do not wait |
| event count | 250 | below §19.1's hard cap of 1000, so a burst has headroom |
| serialized size | 128 KB | keeps a batch under the wire limit with room to spare |
| time since last flush | 30 s, foreground only | |
| app backgrounded or quitting | immediately | **spool first, then attempt the network** |
| `Flush()` | immediately | |
| network became reachable | immediately, if the spool is non-empty | |

Background timers MUST stop in the background. A game that is not being played should not be
waking a radio.

**Bigger batches are cheaper per event**, and measurably so (`cmd/wiresize`, zstd, derived
event ids):

| events per batch | bytes per event |
|---|---|
| 50 | 16.5 |
| 500 | 8.1 |
| 1000 | 7.0 |

A low-volume game will sit near the top row whatever the interval, because it has nothing to
batch. That is fine — the table is a reason not to flush every event, not a reason to delay a
player's last session.

---

## 8. Transport

```http
POST /v1/batches HTTP/1.1
Authorization: Bearer ps_project_live_…
Content-Type: application/json
Content-Encoding: gzip
```

- **The key goes in the header, never in the body** (§17.1). It is a publishable key and is
  assumed discoverable (§71) — which is why it can only ingest, and why the environment comes
  from the key rather than from the body.
- **Compression is `gzip` or `zstd`, and nothing else.** A 415 names the encoding it refused.
  In practice: gzip for game clients, because it is in every engine's standard library, and
  zstd for server-side SDKs, where a native dependency is free. That is what §17's "zstd
  where supported" means once you try to ship it.
- **Do not send `event_id`.** The gateway derives it. A v4 UUID is sixteen incompressible
  bytes and it is three quarters of a compressed event; omitting it takes a 500-event batch
  from 31.6 bytes an event to 8.1 (ADR 0026). Send one only where a natural identifier
  already exists — a server SDK replaying its own log — and it MUST then be a UUID.
- `batch_id` and `install_id` MUST be UUIDs. The gateway refuses anything else with a 422.
- **Timestamps.** Stamp `event_time` when the event happens, never at flush and never again
  on retry. Derive it as `sessionStartWallClock + (monotonicNow − sessionStartMonotonic)`, so
  a player who changes their device clock mid-session does not produce events that interleave
  backwards. The server clamps anything outside [−7 days, +1 hour] and preserves the original
  in `client_event_time`, so a wrong clock is corrected and auditable rather than hidden.
- **Truncate locally** to §12's limits. The server truncates too, but bytes not sent cost
  nothing.

### 8.1 The context timeline

A batch carries the studio's context **once**, as a timeline, rather than restating it on
every event. The gateway expands it onto each event before storage, so ClickHouse receives
fully denormalized rows and no query has to join to reconstruct them
([ADR 0028](../adr/0028-context-travels-as-a-timeline.md)).

```json
{ "context": [ { "at": 0, "values": { "rank": "gold", "chapter": 4 } },
               { "at": 2, "set":    { "rank": "diamond" } } ],
  "events":  [ …, …, …, … ] }
```

Four rules, and the first two are the ones an SDK gets wrong:

1. **`at` is the event's index in this batch**, counting from zero. A change applies from that
   event onward.
2. **A change applies to future events, never to queued ones.** An event tracked while the
   player was gold stays gold, even when the change lands in the same batch. This is the same
   principle that lets one batch straddle a sign-in (§3).
3. **Frame 0 always carries a full `values` snapshot.** A batch must be self-contained: the
   server never remembers where the previous batch left the context, so a redelivery
   reconstructs exactly what the first delivery did.
4. **Frames ascend by `at`.** The server resolves the timeline in one pass and refuses a batch
   whose frames are out of order, because a frame out of order would silently never apply.

Within one frame the order is fixed: `values` replaces, then `set` applies, then `unset`
removes. Omit `context` entirely when the game sets none.

Context is **bounded scalars** — string, integer, float, boolean — never arbitrary JSON, and
never a namespace the studio does not own. See
[context-and-targeting.md](context-and-targeting.md).

---

## 9. What the server answers, and what to do about it

| status | meaning | action |
|---|---|---|
| **202** | durably queued | delete the batch. Read the body: `accepted`, `dropped`, `reasons`. |
| **400** | malformed body, or an unsupported `schema_version` | **drop the batch.** Retrying cannot help. Log loudly — this is an SDK bug. |
| **401** | key missing, unknown or revoked | **stop sending.** Keep the spool. Surface to the developer. Retry no sooner than the next `Initialize`. |
| **403** | wrong key class, or a key scoped to another environment | as 401. A configuration error, not a transient one. |
| **413** | too large, compressed or decoded | **split the batch in half and retry each.** A single event that still fails is dropped and logged. |
| **415** | `Content-Encoding` not supported | disable compression for this process, retry uncompressed, log. |
| **422** | envelope failed validation | **drop the batch.** An SDK bug; the details name the field. |
| **429** | rate limited | honour `Retry-After`. Do not count this toward the failure streak. |
| **5xx**, timeout, DNS, no route | transient | keep the batch, back off (§10). |

### The 202 body is not a formality

```json
{"batch_id": "…", "accepted": 498, "dropped": 2,
 "reasons": ["event 17: event_name \"Level Completed!\" is not an accepted name", "…"]}
```

`dropped` is events the server will never accept. Retrying them is pointless; **surfacing
them is not.** An SDK MUST expose them through a callback and MUST log them in debug builds.
Silently discarding them is how a studio ships a release where a third of their events have a
space in the name and nobody notices for a month.

`accepted: 0` with a reason about consent means the gateway discarded the batch on purpose.
Delete it; do not retry.

---

## 10. Backoff, and not draining the battery

- Exponential with **full jitter**: `delay = random(0, min(cap, base × 2^attempts))`, base 1 s,
  cap 5 min. Jitter is not decoration — without it every client that lost the same server
  reconnects in the same millisecond.
- After **8** consecutive transient failures, go **cold**: stop attempting entirely until the
  app next comes to the foreground or the platform reports the network has changed. A dead
  endpoint must not cost a player their battery.
- Never retry in a tight loop, never retry on a background thread the game is waiting on,
  never hold a wake lock for analytics.
- `Shutdown()` makes **one** attempt and returns; the deadline is the host's HTTP timeout and,
  under it, whatever the platform gives a process on its way out. Whatever does not go stays in
  the spool for the next launch. An SDK that blocks a quit is an SDK that gets an ANR report
  filed against the game.

---

## 11. Scenarios

The cases worth walking through, because each one has bitten a real SDK.

### The player who installs, plays for eight seconds, and quits

```
t=0.0  Initialize        install_id created, spool opened and drained (empty)
t=0.2  consent granted   in-memory buffer promoted (empty)
t=0.3  first Track       session_id generated, session_start emitted
t=0.3  ── critical ──▶   batch #1 created, spooled, POSTed
t=0.6  202               deleted from spool. The install is counted.
t=8.0  quit              ring spooled as batch #2, one bounded attempt
```

The install is recorded within a second of the first event because `session_start` is
critical and does not wait for a batching interval. This is the case that justifies that
rule: a player who never returns is recorded exactly once, in their first second, or not at
all.

If the network was unavailable at t=0.3, batch #1 stays spooled and is sent on the **next
launch**. If there is never a next launch, the install is never counted — and no design can
change that, because the bytes never left the device. What the protocol can do is make the
window as small as possible, which is what it does.

### No network at all, for days

Batches accumulate in the spool. Nothing is lost until a bound is hit; then normal events are
dropped oldest-first and `session_start`s and revenue survive. At seven days, entries begin
ageing out — not to save space, but because the server would clamp their timestamps and
report a fiction (§6).

On reconnect the SDK drains **oldest first**, one batch per request, with normal backoff. The
drain is bounded so a week offline does not arrive as a burst.

One consequence to be honest about: those events are assigned to the cohort of the day they
**arrive**, not the day they happened (§29.4). A player who played offline on Monday and
reconnected on Thursday is a Thursday cohort. This is a known, accepted cost of assigning
cohorts server-side, and it is the reason the seven-day spool bound is not longer.

### Initialize, then the network is turned off, then the game is quit

Identical to the above, and specifically: `Initialize` performs **no network operation at
all**. It reads a file, opens a spool and returns. An SDK whose initialization can fail
because a server is unreachable is an SDK that can delay a game's launch, and §3.2's whole
posture is that the analytics path never becomes a dependency of the game.

### The server is down

5xx or a connection error → the batch stays, backoff climbs, and after eight attempts the SDK
goes cold until foreground or a network change. The game is unaffected; nothing is lost
inside the spool bounds. When the server returns, the drain begins with the oldest batch.

If the server is down for longer than the spool's age bound, the oldest batches are dropped.
That is a deliberate choice of losing old data over reporting wrong data.

### The process is killed with no callback

Force-quit from the app switcher, an out-of-memory kill, a crash. Everything spooled survives
and is drained next launch. The ring is lost — at most one flush interval of normal events,
and no critical ones.

This is why backgrounding **spools before it sends**: the background callback is the last
moment the SDK is guaranteed to run, and using it to start a network request that may not
finish, without first writing to disk, is the single most common way an SDK loses a session.

### The batch is sent twice

A 202 that never arrives because the connection dropped. The SDK retries the same immutable
batch; the server sees the same `batch_id` and the same derived event ids, and the writer's
deduplication token makes the insert land once (§21, ADR 0018). Delivery is at-least-once and
the observable result is idempotent — which holds **only** because the batch was immutable
(§6, rule 2).

### The player signs in mid-session

Nothing flushes. Events before the sign-in carry no `player_id` and events after carry one, in
the same batch if that is how it falls. Cohorts follow the device regardless (§29.2), so the
player's history is continuous either way.

### The player withdraws consent mid-session

Flush the spool under the old consent block, then stop collecting. See §5.

### The player reinstalls

A new `install_id`, a new player, a new cohort. Intended (ADR 0004). An SDK MUST NOT attempt
to recover the old identifier from a backup, a keychain that survives uninstall, or anything
else — that would be building the cross-install identifier the design refuses to have.

### The device clock is wrong

Events are stamped from a monotonic clock anchored at session start, so they stay ordered
within a session. The server clamps anything outside its skew window and preserves the
original. An SDK MUST NOT try to correct the clock itself, and MUST NOT refuse to send events
because a clock looks wrong — the correction is the server's, and it is recorded.

### The game ships with the wrong key

401 or 403, permanently. Stop sending, keep the spool, surface it loudly in debug builds. Do
not retry on a timer: a shipped build with a bad key would otherwise spend the rest of its
life retrying.

---

## 12. Limits

Server-enforced. An SDK SHOULD enforce them locally too, because bytes not sent cost nothing
and a locally-dropped event can be logged with its name.

| limit | value |
|---|---|
| events per batch | 1000 |
| decoded body | 4 MiB |
| compressed body on the wire | 1 MiB |
| properties per event | 64 |
| property key | 64 characters |
| string property value | 1024 characters |
| event name | 96 characters, `[a-zA-Z0-9_.:-]` |
| clock skew accepted | −7 days to +1 hour |
| per-IP burst | 200 requests/second |
| per-IP sustained | 6000 requests/minute |

Direct personal data in properties — anything matching an email, a phone number or a card
number — is refused and the batch is rejected (§19.3). SDKs SHOULD warn about it in debug
builds rather than letting a studio discover it in production.

---

## 13. Conformance

An SDK is conformant when these pass. Three of them are the ones §15 singles out, because
each fails silently and corrupts data rather than losing it.

**Identity**
1. `install_id` survives an app update.
2. `install_id` does **not** survive an uninstall/reinstall.
3. `install_id` is absent from a platform backup and does not restore onto a second device.
4. `SetPlayer` does not flush; a batch may contain events on both sides of a sign-in.

**Durability**
5. A batch is written to the spool before the first send attempt.
6. Kill the process between spooling and the 202: the batch is sent on the next launch.
7. A retried batch is byte-identical to the first attempt — same `batch_id`, same order.
8. A 202 that arrives twice results in one set of rows (verified against the server).
9. Spool entries older than the age bound are dropped, not sent.
10. A full spool drops normal events before critical ones.
11. A corrupt spool entry is discarded without crashing and without blocking the rest.

**Session**
12. The first `Track` after `Initialize` emits `session_start` before it.
13. `session_start` is sent without waiting for a batching interval.
14. Backgrounding for longer than the session timeout starts a new `session_id`.
15. A session survives a brief backgrounding.

**Consent**
16. Nothing reaches disk while consent is unknown.
17. Granting consent promotes the in-memory buffer; denying discards it.
18. Withdrawal flushes the spool under the previous consent block before it stops collection.
19. No batch ever contains two consent states.

**Context**
20. A batch that carries context opens with a full `values` snapshot at `at: 0`.
21. A change made while events are queued becomes a delta at the index of the *next* event,
    and the events before it keep the old values.
22. Several changes before the next event produce one frame, not several.
23. A cleared session scope travels as `unset`, so the server can tell "gone" from
    "unchanged".
24. A game that sets no context sends no `context` field at all.

**Transport**
25. A gzip-compressed batch is accepted and decodes to the same events.
26. `event_id` is not sent, and the events come back with distinct ids.
27. A 413 causes a split-and-retry, not a drop.
28. A 401 stops sending without discarding the spool.
29. `dropped` and `reasons` from a 202 reach the developer.

**Behaviour under stress**
31. `Track` on the main thread never blocks measurably and never throws, including with the
    spool unwritable, the network down and the buffer full.
32. `Shutdown` returns promptly with the network black-holed.
33. Eight consecutive failures put the SDK in a cold state rather than a retry loop.

**What the caller is told**
34. `Track` reports which of queued, held-for-consent and declined happened, and none of the
    three is reported as a failure.
35. A malformed event name is rejected even when the player has declined, and is counted as a
    rejection rather than as a consent statistic.

Most of these are proved once, in `sdk/core`, against a fake host — which is the point of
having one core. Four cannot be, and belong to each binding because they are properties of the
platform rather than of the protocol: **3** (the identifier is outside platform backup), the
memory layout of every struct crossing the ABI, the survival of the host's callbacks under the
platform's garbage collector, and **31**'s "never throws" at the language boundary a game
actually touches. Unity's are `sdk/unity/Tests/Runtime/BoundaryTests.cs`.

---

## 14. What is not in this document

**Feature flags and LiveOps config** (§41–§45) are a separate, read-only surface, and
`config-api` is not built. When it is: fetching config is not an event, and what comes *back*
into this protocol is an exposure event — emitted only where the value mattered, and
deduplicated per session and config version (§44). An SDK that emits one per evaluation emits
one per frame.

**Purchase verification** (§46–§48) is server-to-server with a server key. A client
`TrackRevenue` event is funnel telemetry, not the money fact.

**Data-subject requests** (§61, §101) are also server-to-server, on the privacy surface.
A game client never erases anybody.

**Logs, exceptions and crashes** are §15, a stream of their own beside this one.

## 15. Logs, exceptions and crashes

Cloud Logging (ADR 0050) is a second, separate stream. It shares the spool, the transport and the
backoff of §5 to §10, and nothing else: its own endpoint, its own queue on disk, its own flush.
A game that logs heavily must never delay an analytics batch, and a storm of one must never
starve the other.

```http
POST {endpoint}/v1/logs
Authorization: Bearer ps_client_live_...
Content-Type: application/json
Content-Encoding: gzip
```

```json
{
  "schema_version": 1,
  "batch_id": "7d1c1d1e-1111-4111-8111-111111111111",
  "install_id": "7d1c1d1e-2222-4222-8222-222222222222",
  "sdk": { "name": "unity", "version": "1.0.0" },
  "app": { "version": "1.4.2", "build": "412" },
  "device": { "platform": "android", "os_version": "14", "model": "SM-S918B" },
  "consent": { "analytics": true, "diagnostics": true, "policy_version": "2026-01" },
  "records": [
    { "time": "2026-09-25T14:05:11.204Z", "level": "warn", "logger": "Net",
      "message": "Retrying request to api.example.com (attempt 2)",
      "session_id": "…", "attributes": { "scene": "Shop" } },
    { "time": "2026-09-25T14:05:12.010Z", "level": "error",
      "exception": { "type": "NullReferenceException", "value": "Object reference not set",
        "handled": true,
        "frames": [
          { "module": "UnityEngine.EventSystems.ExecuteEvents", "function": "Execute" },
          { "module": "Game.UI.ShopPanel", "function": "OnPurchaseClicked", "file": "ShopPanel.cs", "line": 118, "in_app": true }
        ] },
      "breadcrumbs": [ { "at": "2026-09-25T14:05:09.000Z", "category": "ui", "message": "Opened Shop" } ] }
  ]
}
```

The header is §3's, field for field, plus `device.model`. What a record is follows from what it
carries: `crash: true` is a crash, an `exception` is an exception, anything else is a log line.

**Levels.** `trace`, `debug`, `info`, `warn`, `error`, `fatal`. The engine's own names map onto
them in the binding (Unity's `Log` is `info`, `Assert` and `Exception` are `error`).

**Frames** are outermost first, innermost (where it was thrown) last, and `in_app` marks the game's
own code. Grouping into issues reads only in-app frames and never line numbers, so an SDK that
cannot tell what is in-app should mark nothing rather than everything.

**Crashes** come from three places, and an SDK MUST NOT install a signal handler to get them.
A handler competes with the engine's own (Mono turns a null dereference into a
NullReferenceException through SIGSEGV), with IL2CPP's and with any other crash reporter the game
ships, and a crash reporter that crashes a game is the one thing worse than none.

1. An exception that is about to end the process is recorded synchronously on the thread it
   killed, written to the spool before the network is tried, and sent on the next launch if it
   does not go now.
2. What the operating system recorded about the last run, read on the next launch: Android's
   `getHistoricalProcessExitReasons` (Android 11+: Java and native crashes, ANRs, low-memory kills
   in the foreground) and iOS MetricKit crash diagnostics (iOS 14+).
3. Where neither exists, a marker (`ps.run`) written when the app comes to the foreground and
   cleared when it leaves or quits. Found at launch, it means the last run ended in front of the
   player without either, and is reported as an `AbnormalExit` crash. Where (2) exists the
   marker is off, so one crash is one record.

The record's `time` is when it happened; the server accepts a record up to 30 days old for
exactly this reason.

**Breadcrumbs.** An SDK keeps, in memory only, a ring of the last things that happened (scene
loads, a low-memory warning, log lines below the capture level; 50 by default) and attaches it to
each exception, crash and record at `error` or above. It never sends breadcrumbs with a lesser
line, and the server keeps at most the last 100.

**Record ids** are optional. Leave them out: the server derives one from `batch_id` and the
record's position, so a retried batch lands on the same ids (§4).

**Limits.** At most 1,000 records a batch and 4 MB decoded. The server cuts rather than refuses:
a message at 8 KB, an exception's value at 4 KB, a stack to its innermost 128 frames, breadcrumbs to
the last 100, attributes to 32 keys of up to 1 KB each. A record with neither a message nor an
exception is dropped, and says so in the answer.

**Consent** is §6's, with one addition: `consent.diagnostics` (§15.1). A batch carrying neither
`analytics` nor `diagnostics` is answered 202 and nothing is stored.

**What the server answers** is §9's table. A 429 (the device, address or project log budget, or a
suspended account) keeps the batch for later, as it does for events.

**What is billed** is each record's content in bytes, uncompressed: its text, frames, breadcrumbs and
attributes, plus 64 bytes for the fields every record has. Compression on the wire is free to the
studio and cheaper for everyone, so an SDK SHOULD always send zstd.

### 15.1 Privacy, and what an SDK does on the device

Error reports are personal data under GDPR when they carry an installation id, and neither the
ePrivacy Directive nor any regulator's guidance exempts crash reporting from consent in general.
So the SDK treats them as it treats events, and every rule below is the SDK's job, not the
game's.

**Off until the studio turns it on.** Capture changes what a game declares on the App Store (App
Privacy: Crash Data, Other Diagnostic Data) and Google Play (Data safety: Crash logs,
Diagnostics). An SDK update must never change those answers behind a studio's back.

**Before the player answers**, records wait in memory (50 at most) and are sent if the answer is
yes. A crash cannot wait in memory, so it waits on the device under `ps.lh.` and is deleted if the
answer is no. Games commonly crash before their consent screen is shown, and those are the
crashes that matter most.

**A second question, when a game asks one.** Error reports follow the analytics answer by
default: one screen, one answer. A game with its own "Share error reports" switch, the way
desktop apps ask, records that answer separately and it wins for logs and crashes in either
direction. A batch sent under it carries `"diagnostics": true`, with `analytics` as it stands. A
game can also ask after the fact: the SDK reports how many crash reports are waiting, so a game
can say "The game closed unexpectedly last time. Send a report?"

**Scrubbed twice.** Emails, bearer tokens and JWTs, `password=`-style pairs, Luhn-valid card
numbers, IPv4 addresses and the account name in a file path (`/Users/emma/`, `C:\Users\Emma\`)
are replaced with a marker (`[email]`, `[token]`, `[redacted]`, `[card]`, `[ip]`, `[user]`) on
the device, and again on the server for any SDK that did not. Messages, exception values,
breadcrumbs, attributes and frame paths are all scrubbed. What only the game knows is personal
(a display name in a message) is the studio's to remove, in the binding's `BeforeSend`.

**What is never read.** No device name (`SystemInfo.deviceName` is what the player called their
phone), no hardware or advertising identifier, no IDFV. `device.model` is the hardware model,
shared by millions of devices. The installation id is random and lives only in the app's own
storage, outside platform backup.

**Children.** A game that says it is child-directed sends no `player_id` and no attributes on a
record: what broke, where, and on what kind of device. That is COPPA's support for internal
operations, and what the Families policy and the Kids category expect of an SDK.

**Cost on the device.** The capture callback copies two references into a fixed ring under a
lock and returns; parsing, filtering and the native call happen four times a second on the main
thread. An error repeated within 10 seconds is one record with a `ps.repeat` count, a device sends
at most 120 records a minute (crashes excepted), and a batch is gzip, at most 200 records or
64 KB, sent every 15 seconds, or at once for a crash.
