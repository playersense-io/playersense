# 0034. A rule is validated when it is written, not only when it is published

**Status:** Accepted
**Date:** 2026-09-18
**Relates to:** project.md §41, §42, §43, §45; [ADR 0027](0027-targeting-is-evaluated-on-the-server.md), [ADR 0032](0032-a-liveops-event-carries-a-schedule.md)

## Context

§41's three authored tables — segments, feature flags and LiveOps events — shipped with a
compiler, an evaluator, a config endpoint, a Unity binding and exposure analytics on top of
them, and **no way to write a row**. `POST .../config/{environment}/publish` read tables nothing
could fill. `liveops.write` and `liveops.read` were in the permission vocabulary and on two
seeded roles, attached to no route.

That absence hid a second thing. The publish was the only code that had ever parsed a stored
rule, so it was also the only validator, and a publish is the wrong moment for a message:

- `liveops.publish` is a **separate permission** on purpose, so that a junior drafts and a
  senior ships. The person who receives a refusal is therefore usually not the person who
  caused it.
- It arrives **late**. A rule written on Tuesday fails on Friday, in front of somebody who has
  a release to get out and no idea which of forty segments changed.
- For a **cycle** it arrives too late to be actionable at all. `A -> B -> A` cannot exist until
  the write that closes the loop; afterwards the project simply does not publish, and nothing in
  the error says which of the two rules to change, because either would fix it.

A third thing surfaced on the first attempt to write a flag. `FeatureFlag.fallback` was declared
`field.JSON("fallback", map[string]any{})`, and `false` does not unmarshal into a map — so a
**bool flag, the commonest kind and the one every test in `internal/liveconfig` uses, could not
be stored at all.** `LiveOpsEvent.payload` had the same shape, which made the economy multiplier
in its own doc comment — `2.0` — the one payload it could not carry. The column was `jsonb`
either way; only Go's idea of it was wrong, and nothing had ever written a row to find out.

## Decision

**The authoring routes exist, and they validate everything a publish validates.** A write parses
the rule through `targeting.Parse`, resolves every segment reference against the project, and
refuses a cycle — `liveconfig.Segments.Check` is `Inline` run for its error, so it is the same
walk the publish performs rather than a second implementation of it.

**A segment is checked with itself in the set.** The candidate rule is inserted under its own key
before the walk, which is the only way a self-reference can be caught: the row does not exist yet
on a create, and still holds the old rule on an update.

**A flag declares a kind, and every value is checked against it at the write.** `liveconfig.Kind`
is a closed set — `bool`, `string`, `number`, `json` — and `Accepts` refuses a value of the wrong
shape, naming what arrived in the words somebody editing a flag would use rather than as a Go
type. Absence is refused for every kind, `json` included: a flag exists to answer without the
network, so a fallback of `null` is a flag with no answer, and the game reaches the state it
would have reached had PlayerSense been unreachable.

**`key` and `kind` are immutable, and `kind` is not a member of the update body.** A game calls a
typed reader on the key, so a kind that changed would be a document the installed build cannot
read. A field a caller may send that has no effect is worse than one that does not exist, because
somebody will send it, see a 200, and believe it.

**A raw JSON column holds a JSON value, not a map.** `fallback` and `payload` become
`json.RawMessage`. `Segment.rule`, `LiveOpsEvent.audience` and `FeatureFlag.rules` stay maps and
lists, because a rule and a variant genuinely are objects.

**DELETE archives.** Every table keeps its rows, as revoking an API key does. A published version
holds each rule *inlined*, so a deleted row leaves a running configuration nobody can explain —
and for a flag it is worse: a key that vanishes from the next document is a key whose SDK falls
back, and the fallback is the value a studio chose for an offline device rather than for this.

**`archived` is the one field a `PUT` does not replace.** It is a pointer, and absent means "leave
it as it is". Everything else about these bodies is a whole-object write, but archiving is a
lifecycle gesture with its own route — so a plain bool would mean a dashboard correcting a name
without echoing the member back silently returns an archived flag to the next published document,
which a shipped game then starts reading again. Un-archiving has to be said out loud.

**A draft is readable under `liveops.read`.** `GET .../config/{environment}/draft` compiles what
the next publish would and throws the document away, reporting whether it compiles, what it would
carry, whether serving it costs a history lookup per request, and **what is being left out and
why** — because an archived flag and a paused event are both absent on purpose, and "my event is
missing" must be distinguishable from a bug.

**The publish still re-validates everything.** These two checks are not redundant: a segment
archived after a flag referenced it breaks a rule nobody touched, so the same question has to be
asked again at the moment the answer ships.

## Consequences

The same grammar is now walked twice per authored rule — once on the write, once on the publish.
The cost is a `SELECT` of the project's segments per write, against a table holding tens of rows
per project, on a surface that serves people rather than programs.

**A rule cannot be saved in a state its author intends to fix later.** Writing `A -> B` before
`B` exists is refused, so a studio building two segments that refer to each other must create the
referenced one first. This is a real constraint and the trade is deliberate: referencing a segment
that does not exist yet is a typo far more often than a plan, and the alternative is a project
that has stopped being publishable with no single place to point at.

**Status transitions on a LiveOps event are unconstrained.** Draft, active, paused and archived
move freely in every direction, because every pair is something a studio legitimately does —
pausing a live event, resuming it, returning an active one to draft, un-archiving one it turns out
to want next year. A state machine would forbid exactly one of those, and it would be the one
somebody needs at two in the morning.

**A whole-object `PUT`, so the last write wins visibly.** Two people editing one flag will
overwrite each other rather than merge. A partial update of an ordered rule list means addressing
nodes inside a tree, and the merge of two such edits is an audience neither person wrote.

## Alternatives considered

**Leave validation at the publish.** Rejected on where the message lands. The compiler's errors
are already good sentences — "segment \"whales\" does not exist", "flag \"vip\" rule 2: unknown
operator" — and their only defect was the audience: they were being read by whoever holds
`liveops.publish`, about a rule somebody else wrote, days later.

**Resolve segment references lazily, treating an unknown name as "matches nobody".** Rejected
because it is the exact failure this architecture keeps guarding against. An audience that
silently became empty is an event that reaches no players, and nothing anywhere reports it.

**Store the fallback wrapped, as `{"value": false}`, to keep the map column.** Rejected: the
wrapper would reach the compiled document, and what the SDK reads as a flag's value is that
document. Inventing a container to work around a Go type would put the workaround in the public
contract.

**`PATCH` with a partial body.** Rejected for rules, and then for consistency everywhere. A patch
that can address one element of an ordered list is a merge, and a merge of two audience edits is a
rule neither editor approved.

**A state machine for the LiveOps status.** Rejected as above: every transition is legitimate, so
the machine would encode a preference as a rule.

**Validate the rule grammar in the request schema.** Rejected because it puts a second, weaker
description of the rule language in a second package, and the two drift — the first symptom being
a rule the schema accepted and the compiler refused. The wire type is raw JSON and
`internal/targeting` stays the single authority.

## Enforcement

`internal/routers/liveops.go` mounts the routes with `core.Require(authz.LiveOpsRead)` and
`core.Require(authz.LiveOpsWrite)`; `internal/handlers/liveops.go` performs the checks;
`liveconfig.Kind.Accepts` and `liveconfig.Segments.Check` are the two new mechanisms.

In `apps/backend/cmd/control-api/liveops_test.go`:

| Test | Fails when |
|---|---|
| `TestABoolFlagCanBeStored` | a `jsonb` column is typed as something narrower than a JSON value |
| `TestAFlagRefusesAValueOfTheWrongKind` | the kind check is dropped, or stops naming the rule's position |
| `TestTheKindCannotBeChanged` | `kind` becomes writable on an update |
| `TestASegmentCycleIsRefusedByTheWriteThatClosesIt` | the write-time resolution is dropped, or a refused write is allowed to overwrite the rule it refused |
| `TestAnUnknownSegmentIsRefusedAtTheWrite` | an unresolvable reference is deferred to the publish |
| `TestAScheduleWithAUnitlessDurationIsRefused` | `duration` becomes a number, or stops being parsed |
| `TestArchivingIsNotDeleting` | `DELETE` removes a row, or an ordinary edit un-archives one by omission |
| `TestTheAuthoringSurfaceSplitsReadingFromWriting` | `liveops.write` stops gating a write, or a drafter gains `liveops.publish` |
| `TestADraftSaysWhatThePublishWouldDo` | the draft stops naming what it excludes |
| `TestWhatWasAuthoredIsWhatIsPublished` | anything between the authoring tables and the compiled document breaks |

In `apps/backend/internal/liveconfig/kind_test.go`: `TestAKindAcceptsOnlyItsOwnShape`,
`TestARefusalNamesWhatArrived`, `TestAnUnknownKindIsRefusedRatherThanAssumed` and
`TestCheckRefusesWhatWouldNotCompile`, none of which needs a database.
