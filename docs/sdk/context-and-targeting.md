# Context and targeting

How a studio describes its game to PlayerSense, and how PlayerSense decides what a given
player should receive. The companion to [protocol.md](protocol.md), which covers getting
events *in*; this covers getting configuration *out*.

Keywords are RFC 2119. Section marks like §44 point at `project.md`.

---

## 1. PlayerSense never learns the shape of anybody's game

One studio has `World → Chapter → Level → Zone`. The next has `Map → Region → Dungeon`. The
next has `Season → League → Match`. The fourth has no progression at all. An SDK with
`SetLevel()` and `SetChapter()` fits the first and lies to the other three, and every attempt
to generalise it produces a worse vocabulary than any studio's own.

So there is no game vocabulary in the SDK. There is a key, a value and a scope:

```csharp
PlayerSense.Context.Player.Set("progress.chapter", 6);
PlayerSense.Context.Player.Set("rank", "diamond");
PlayerSense.Context.Session.Set("game_mode", "ranked");
PlayerSense.Context.Session.Set("map", "desert_03");
```

Another studio writes `combat_level` and `activity` and it works identically. Targeting reads
these back by the studio's own names. PlayerSense does not know what any of them mean, and
that is what lets it serve a match-three game and an MMO with the same engine.

---

## 2. Four layers, three authorities

Not every value is equally trustworthy, and conflating them is how a modded APK gets a
legendary sword.

| layer | example | who asserts it | forgeable by a modified build? |
|---|---|---|---|
| **Server-derived** | `geo.country`, `network.vpn`, `network.asn` | PlayerSense, from the request's own IP | **no** |
| **SDK-generated** | `app.version`, `device.platform`, `sdk.version`, `install.age_days` | the SDK, from its configuration | yes, but only about itself |
| **Studio context** | `context.rank`, `context.progress.level` | the game client | **yes** |
| **Account context** | `account.subscription`, `commerce.verified_lifetime_spend` | the studio's backend, over a server credential | **no** |

The namespaces are reserved and the SDK refuses to write into any but its own:

```text
geo.*         network.*      ← PlayerSense, from the connection
app.*         device.*       sdk.*      install.*   ← the SDK
account.*     commerce.*     ← the studio's backend, server-to-server
playersense.* ← reserved
context.*     ← the studio, through the SDK
```

`PlayerSense.Context.Set("geo.country", "US")` is **refused by name**, and the refusal says
who owns it. Not because filing it under `context.geo.country` would be dangerous — it would
be harmless — but because the studio is heading for a targeting rule on `geo.country` that
silently never matches, and nobody debugs that quickly.

### Why geography never comes from the client

PlayerSense resolves the request's IP against its own databases: country, region, ASN, ISP,
and whether the connection is a VPN, a proxy, Tor, a datacentre or a mobile network. The SDK
ships none of that — no MMDB, no VPN heuristics, no ASN lookup — and is smaller and more
honest for it. **A value the client never sends is a value the client cannot forge.**

It also names things accurately. `geo.country` means *this connection exits from an address
geolocated to Turkey*, which is not the same claim as *this person is in Turkey*. When both
matter, they are separate facts and a rule can use both:

```text
account.country = TR        the studio's own record
geo.country     = US        where the connection comes out
network.vpn     = true      and how
```

Collapsing those into one "country" would throw away the only interesting part.

### The rule this exists for

A dashboard must be able to say, and mean:

> ⚠ `context.progress.level` is **client supplied**. Not suitable for rules that grant value.

> ✓ `commerce.verified_lifetime_spend` is **server authoritative**.

So "give the legendary sword if lifetime spend is over $100" can be written safely, and
"show the Halloween banner if level ≥ 10" can be written cheaply, and the difference is
visible at the moment somebody writes the rule.

---

## 3. Scopes

| scope | lives until | for |
|---|---|---|
| **Player** | the actor changes | what is true of them: rank, progress, subscription tier |
| **Session** | the session ends | where they are and what they are doing: mode, map, difficulty |

Player scope is persisted and restored on the next launch **along with whose it was**. The
sign-in that follows decides its fate: the same player keeps it, anybody else clears it. A
device where a second account signs in must never inherit the first one's rank, or it is
targeted as them.

A new session clears session scope. Nothing else does.

Where both scopes hold a key, **session wins**: it is the narrower statement. A player whose
rank is `diamond` playing a `casual` match is, right now, in a casual match.

### Context is not event properties

```csharp
PlayerSense.Analytics.Track("enemy_killed", new() { ["enemy"] = "orc", ["weapon"] = "bow" });
```

Properties describe **one occurrence**. Context describes **current state**. They stay
separate on the wire and meet on the row: a batch carries context once as a timeline, and the
gateway materializes it onto every event before storage, prefixed `ctx.`
([ADR 0028](../adr/0028-context-travels-as-a-timeline.md)).

```text
event_name        ctx.rank    ctx.chapter
-----------------------------------------
match_started     gold        4
enemy_killed      gold        4
match_completed   diamond     4
```

So `GROUP BY ctx.rank` is a scan of one denormalized table. §29.1's rule that no cohort query
joins on a player key is untouched — there is nothing to join to.

### Event-time context is not cohort context

The distinction that keeps "retention by rank" from being ambiguous.

| | means | changes? |
|---|---|---|
| `ctx.rank` | the rank when the event happened | yes |
| `cohort.rank` | the rank when the player entered the cohort | never |

"Sessions by rank" wants the first. "D7 retention by rank" wants the second — a player who was
bronze on Monday and gold on Friday belongs to the bronze cohort for as long as that cohort
exists, or the denominator moves under the numerator and §29.9's whole argument collapses.

A cohort dimension is frozen at cohort creation and written into the already-denormalized
cohort fact, which is §29.3's sticky-dimension model extended to studio keys. It needs no new
query machinery and adds no join.

**Both are opt-in per key**, because each one costs cardinality:

```text
rank    event dimension ✓   targeting ✓   cohort dimension ✓
map     event dimension ✓   targeting ✓   cohort dimension ✗
weapon  event dimension ✗   targeting ✓   cohort dimension ✗
```

Retention by the map somebody happened to be standing on during their first session is not a
question anyone asks, and every dimension multiplies a rollup's rows by its cardinality. The
budget is §29.13's, and a cardinality check must be able to refuse `player_id`.

---

## 4. The configuration request

The client sends what only it knows:

```json
{
  "install_id": "…",
  "player_id": "player_123",
  "app":      { "version": "1.14.2", "build": "481", "platform": "android" },
  "context":  { "progress.level": 43, "game_mode": "ranked", "rank": "diamond" },
  "snapshot_version": "cfg_8271"
}
```

The server adds what only it knows, from the connection it is answering:

```json
{
  "geo":     { "country": "TR", "region": "75" },
  "network": { "asn": 9121, "isp": "…", "vpn": false, "datacenter": false }
}
```

None of that travels from the game, and the game cannot override it.

---

## 5. The snapshot

The response is a **snapshot**: everything this player is currently eligible for, already
decided.

```json
{
  "snapshot_version": "cfg_831928",
  "server_time":      "2026-09-17T08:30:04Z",
  "generated_at":     "2026-09-17T08:30:04Z",
  "next_refresh_at":  "2026-09-17T08:45:00Z",

  "flags": {
    "new_inventory": { "value": true, "version": 38 }
  },

  "live_ops": [
    {
      "key":     "halloween_2026",
      "kind":    "seasonal_event",
      "version": 17,
      "starts_at": "2026-10-20T00:00:00Z",
      "ends_at":   "2026-11-05T23:59:59Z",
      "payload": { "xp_multiplier": 1.5, "boss": { "id": "pumpkin_king" } }
    }
  ]
}
```

Four properties of that shape are load-bearing.

**`Get()` never makes a request.** It is a dictionary lookup against the cached snapshot.
A game calling `Flags.IsEnabled` in `Update()` must cost nothing, and a game that has to
`await` its configuration is a game with a loading screen it did not want.

**`payload` is opaque.** PlayerSense understands the envelope — key, kind, version, window,
eligibility — and nothing inside. That is the studio's domain, and the moment PlayerSense
starts validating its contents is the moment it starts constraining what a game can be.

**`kind` is a free string.** Not an enum: somebody will need `raid`, `battle_pass`,
`flash_sale`, `maintenance`, `community_goal`, `boss_rotation`, and an enum means a PlayerSense
release before they can ship.

**The snapshot contains results, never rules.** See §7.

---

## 6. Freshness, and the clock

A snapshot is persisted, so a game launched offline has its configuration before the first
frame. Three states:

| state | meaning | behaviour |
|---|---|---|
| **fresh** | before `next_refresh_at` | used |
| **stale** | past it, refresh failed | **still used**, and a refresh is retried |
| **expired** | past the publisher's hard limit | dropped; the game's own defaults apply |

Stale is the important one. A configuration that vanished because a refresh failed would
turn a network blip into a gameplay change, which is worse than being a few minutes behind.

### `IsActive` does not trust the device clock

A player who sets their phone to next October must not get the Halloween event. The snapshot
carries `server_time`; the SDK records it against the **monotonic** clock and evaluates
activation windows from `server_time + elapsed_monotonic`.

This is the same anchoring the analytics protocol already uses for event timestamps
([protocol.md §8](protocol.md)), and for the same reason: the monotonic clock is the only one
a player cannot move.

If no snapshot has ever been fetched there is no server time, and windows cannot be evaluated
at all — a schedule-bearing item MUST then be treated as inactive rather than trusted to the
device.

---

## 7. Targeting is evaluated on the server, and the client is told only the result

The dashboard rule:

```text
geo.country IN ["TR","DE","FR"]  AND  network.vpn = false
AND context.progress.level >= 20  AND commerce.verified_lifetime_spend >= 50
```

The client receives:

```json
{ "live_ops": [ { "key": "vip_weekend_offer", "payload": { "discount": 25 } } ] }
```

It does **not** receive the rule. Not a redacted rule, not the thresholds, not the fields.
An eligible player is told *you are eligible*; an ineligible one is told nothing, because the
item simply is not in their snapshot.

The SDK therefore evaluates no segments, no rollouts, no experiment allocation and no
audience trees. It looks up keys in a dictionary. Everything else is
[ADR 0027](../adr/0027-targeting-is-evaluated-on-the-server.md).

---

## 8. Re-evaluation

Context changes make a snapshot potentially wrong: a player who crosses level 10 may have
become eligible for something.

```csharp
PlayerSense.Context.Player.Set("progress.level", 10);
PlayerSense.Context.Player.Set("progress.chapter", 2);
PlayerSense.Context.Session.Set("map", "forest");
```

That is three changes and **one** refresh. The SDK tracks a context revision and debounces —
one second by default — so a game that sets context in a loop does not send a request per
key. Setting a value to what it already is does not count as a change at all, which is what
makes calling `Set` from `Update()` safe.

Two other triggers:

- **`next_refresh_at`**, as the publisher chose it.
- **A network change.** The server's view of `geo.*` and `network.*` is a function of the
  connection, so a player who enables a VPN has a snapshot that was computed for a different
  network. The SDK cannot know the new country — and must not try — but it knows connectivity
  changed, so it marks the snapshot stale and refreshes. The old one keeps being used until
  the new one arrives.

And an explicit escape hatch, for the moment a game genuinely needs the new configuration
before it proceeds:

```csharp
await CompleteTutorial();
PlayerSense.Context.Player.Set("tutorial.completed", true);
await PlayerSense.LiveOps.RefreshAsync();
```

---

## 9. Exposure

Reading a flag that affects what the player sees records an exposure event, so an experiment
can be connected to retention and revenue (§44).

```csharp
PlayerSense.Flags.IsEnabled("new_shop");   // records an exposure
PlayerSense.Flags.PeekBool("new_shop");    // does not
```

The distinction is the developer telling the SDK which one they mean: *I am using this* or
*I am inspecting this*. A tooling screen, a debug overlay and an analytics dashboard all want
the second.

Exposures are deduplicated per **session + flag + variant + snapshot version**, and that
bound is not optional: §44 is explicit that a flag checked every frame must not produce an
event every frame. An exposure is an ordinary analytics event and rides the batch path in
[protocol.md](protocol.md), so it costs nothing extra to transport.

---

## 10. One engine, four surfaces

```text
                    remote configuration
                            │
        ┌───────────────┬───┴────────┬────────────────┐
     Flags           LiveOps      Config          Experiments
  bool/variant     scheduled,     plain           controlled
  + exposure       targeted,      key/value       variants
                   + payload                      + exposure
```

Same context, targeting, snapshot, cache, versioning, server clock and refresh. Different
public surfaces, because "should this happen?", "what is running right now?", "what number
should I use?" and "which arm is this player in?" are four different questions and one API
that answered all of them would answer none of them well.

---

## 11. What is built

| | |
|---|---|
| Context model, scopes, namespace authority | **built** — `sdk/core/src/context.rs` |
| Persistence and the player-change boundary | **built** — with conformance tests |
| Context frames on the wire | **built** — `sdk/core/src/client.rs`, and the envelope |
| A binding a game can call | **built** — Unity: `PlayerSense.Context.Session` / `.Player`, over the C ABI |
| Expansion onto event rows at ingestion | **built** — `internal/ingest/normalize.go` |
| Context as a *rollup* dimension, opt-in per key | **not built** — needs the per-key opt-in and the cardinality check |
| Cohort dimensions frozen from context | **not built** |
| Snapshot cache, freshness, server-time anchoring | **not built** |
| `config-api`, segment evaluation, IP enrichment | **not built** — §10.6 |
| Exposure events | the transport is built; the flag evaluation they attach to is not |

Context now reaches analytics: a studio that sets `rank` gets it on every event row without
putting it on every `Track` call. What it does not yet reach is a *dashboard dimension* — that
needs the per-key opt-in and a real rollup column, because `props_string['ctx.rank']` is a map
lookup per row and §26 makes raw scans the fallback rather than the dashboard path.
