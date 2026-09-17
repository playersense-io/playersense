# 0027. Targeting is evaluated on the server, and the client is told only the result

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §10.6, §41–§45, §71, §100; [ADR 0026](0026-compressed-json-and-derived-event-ids.md); docs/sdk/context-and-targeting.md

## Context

Feature flags, LiveOps and remote configuration all answer the same question — *what should
this player receive?* — and the answer depends on who the player is, where they are
connecting from, and what the studio knows about their account.

The conventional game-SDK design ships the rules to the client and evaluates them there. It
is attractive: evaluation is instant and offline, and the server does less. It also has two
properties that are disqualifying here.

The first is that **it hands the rules to the adversary.** A flag payload containing
`country = TR AND lifetime_spend >= 50 AND vpn = false` is a specification of how to become
eligible, shipped to every device including the ones running a debugger. For a cosmetic
A/B test that is uninteresting; for monetization targeting it is the whole game.

The second is that **the client cannot be the source of its own attributes.** Geography,
network class and account spend are exactly the fields worth targeting on and exactly the
fields a modified build can assert. A `Context.Set("lifetime_spend", 100000)` that reaches a
"grant the legendary sword" rule is not a bug in the rule, it is a bug in the architecture.

PlayerSense also already resolves the connection: the ingest and config paths see the request
IP, and there is an MMDB behind them for country, region, ASN, ISP, and whether the address
is a VPN, proxy, Tor exit, datacentre or mobile network. A client shipping its own copy of
that would be larger, worse and forgeable.

## Decision

**Targeting is evaluated on the server. The client receives a snapshot of results.**

The snapshot names what this player is eligible for — flags with their values, LiveOps items
with their payloads and activation windows — and carries **no rule, no threshold and no field
name from the rule**. An eligible player is told they are eligible; an ineligible player is
told nothing, because the item is absent.

**Attributes are separated by authority, and the namespace is the boundary:**

| namespace | asserted by | forgeable by a modified build |
|---|---|---|
| `geo.*`, `network.*` | PlayerSense, from the request IP | no |
| `app.*`, `device.*`, `sdk.*`, `install.*` | the SDK | only about itself |
| `account.*`, `commerce.*` | the studio's backend, over a server credential | no |
| `context.*` | the game client | **yes** |

**The SDK refuses to write into any namespace but `context.*`**, by name, with the owner in
the message. Not as a security control — the server would ignore a forged `geo.country`
anyway, since it never reads one from the body — but because a studio that sets `geo.country`
and then writes a rule against it has a rule that silently never matches.

**The dashboard must show the authority beside every attribute**, so that a rule granting
value can be written knowingly. A client-supplied attribute is not forbidden in such a rule;
it is labelled.

**The SDK evaluates nothing.** No segments, no rollouts, no experiment allocation, no
audience trees, no geolocation. `Get` is a dictionary lookup against the cached snapshot, and
must remain cheap enough to call from a frame loop.

**Activation windows are evaluated against server time anchored to the monotonic clock**, not
against the device clock, for the reason ADR 0026's sibling rule gives about event
timestamps: the device clock is the one thing in this system a player can move at will. With
no snapshot ever fetched there is no server time, and a schedule-bearing item is inactive
rather than trusted to the device.

## Consequences

- **A player with no network gets no *new* eligibility.** The snapshot is persisted and keeps
  being used while stale, so an offline launch has yesterday's configuration rather than
  none — but a player who has never fetched one gets the game's own defaults. That is the
  cost of not shipping rules, and it is the right way round: a game must work with no
  PlayerSense at all.
- **Every context change is potentially a request**, which is why the SDK debounces them and
  why setting a value to what it already is does not count as a change. Without both, a game
  setting context in `Update()` would refresh its configuration every frame.
- **A VPN switched on mid-session invalidates the network half of a snapshot**, and the
  client cannot detect the new country — correctly, since detecting it is what it must not
  do. It detects that connectivity changed, marks the snapshot stale and refreshes; the
  server recomputes from the new address.
- The server does more work per player, and it is cacheable work: the evaluation depends on
  the context and the connection, both of which change rarely.
- **Two round trips of trust are needed for account attributes.** `commerce.*` and
  `account.*` reach PlayerSense from the studio's backend over a server credential, not from
  the game. A studio that never wires that up simply cannot target on spend, which is the
  correct failure.

## Alternatives considered

**Ship the rules and evaluate on the client.** Rejected on both counts above. It is also the
design that makes every rule change a client release in practice, because a rule referencing
an attribute an older build does not send evaluates differently on every version in the wild.

**Ship the rules but obfuscate them.** Obfuscation is not a boundary, and treating it as one
means the security of a monetization system rests on how long it takes somebody to open a
decompiler.

**Let the client send `geo.country` and have the server verify it.** Then the server has to
resolve the address anyway, and the client's value is at best redundant and at worst the one
that gets used by mistake.

**One generic `Config.Get` surface for flags, LiveOps and experiments.** One engine
underneath, yes — that is the decision. One *API*, no: "should this happen?", "what is
running right now?" and "which arm is this player in?" want different return types,
different defaults and different exposure semantics, and a surface answering all three
answers none well.

**Evaluate activation windows on the device clock.** Free, and it makes the Halloween event
available to anyone who can change their phone's date.

## Enforcement

- `sdk/core/src/context.rs` — the scopes, the reserved namespaces and the refusal that names
  the owner.
- `a_client_cannot_assert_the_namespaces_the_server_resolves` asserts `geo.*`, `network.*`,
  `account.*` and `app.*` are all refused from the client.
- `one_players_context_does_not_reach_the_next` asserts a second sign-in on one device does
  not inherit the first player's attributes.
- `restating_context_does_not_look_like_a_change` asserts a thousand identical `Set` calls
  raise the revision once, which is what keeps `Update()` safe.
- `docs/sdk/context-and-targeting.md` is the contract `config-api` must implement when it is
  built; the snapshot shape in §5 is the interface.
