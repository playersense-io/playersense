# 02. Lawful basis and consent

## Where the basis comes from

PlayerSense is a processor for player data ([00](00-roles-and-scope.md)). The studio is the
controller and obtains the basis; the SDK transmits the result; PlayerSense records it and
acts on it.

That division is why the architecture's job here is narrow and absolute: **make the basis
provable for every row, forever.**

## Consent is carried on the row

Consent reaches the gateway in the batch envelope and is stamped onto **every event** as
columns: whether analytics consent was granted, the policy version in force, and the ATT
status as reported.

It is not held only at the batch level, and it is not looked up at query time.

The reason is a property that is easy to miss until it matters: **consent cannot be
reconstructed after the fact.** Nothing in a stored event reveals what the player was shown
or agreed to. A row written without a basis can never acquire one later, and a mutable
per-player flag says only what is true *now* — which is the wrong question when the question
is "was this row lawfully collected in March".

See ADR 0006.

### What carrying it per row buys

**Lawful basis is provable for any row**, including rows recovered from cold archive years
later and rows reprocessed into a new schema.

**Withdrawal is scopeable.** "Erase everything collected for this actor under policy
version N or earlier" is a bounded, executable statement. Without the column, the only
honest reading of a withdrawal is "erase everything" — more destructive than the player
asked for, and harder to prove was done.

**A policy change is visible in the data.** When terms are revised, the version on the row
says which text applied, and rows collected under the old one can be found.

## A batch without consent

Accepted, and nothing is stored.

```json
{ "batch_id": "…", "accepted": 0, "dropped": 14,
  "reasons": ["analytics consent was not granted, so nothing was stored"] }
```

`202` rather than a rejection, because an SDK that retries a rejected batch forever is worse
for everyone than one told plainly that nothing was stored. Nothing reaches the queue, so
nothing reaches storage.

A log batch (protocol §15) has a second basis: `consent.diagnostics`, the player's answer to a
separate "share error reports" question when a game asks one. Either answer is enough for that
batch, and neither says anything about events.

## `att_status`

Recorded, not acted on.

PlayerSense does not **track** as Apple defines the term — no advertising identifier, no
cross-app or cross-vendor identifier, no data-broker sharing (ADR 0004). The SDK therefore
declares `NSPrivacyTracking: false`, and a studio embedding it is not obliged to show an ATT
prompt on PlayerSense's behalf.

The field is stored because a studio may need to answer a question about it, and because a
studio running its own ATT flow for other reasons may want the status alongside its
analytics.

## Legitimate interests, and why it is not relied on

A studio may in some jurisdictions rely on legitimate interests for first-party analytics
rather than consent. That is the controller's assessment to make, and PlayerSense's schema
supports it: the consent columns record what was transmitted, and a studio operating on
another basis records that basis in its own documentation.

What the architecture does **not** do is assume a basis when none was transmitted. A batch
whose consent flag is false stores nothing, regardless of what basis the studio might have
argued for. Failing closed is the only safe default for a processor.

## Restriction and objection

Handled through these columns rather than by deletion. A restricted actor's rows stop being
read while remaining available if the restriction is lifted —
see [05](05-data-subject-rights.md).
