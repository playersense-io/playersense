# 0026. Compressed JSON with derived event ids, and not Protobuf

**Status:** Accepted
**Date:** 2026-09-17
**Relates to:** project.md §4.1, §9, §15, §17, §18, §19, §19.1, §21, §77, §85

## Context

§17 ends with one sentence — "production transport should support a compact encoding such as
Protobuf in addition to JSON" — and §77 reserves `proto/analytics.proto` for it. Neither
says what the compact encoding would buy, and the question is not rhetorical: §4's ceiling is
a billion events a day, and every byte on that path is paid for twice, once by the customer's
bandwidth bill and once by a player's mobile data plan.

Meanwhile two things §17 and §19 *do* specify were not built. §17 asks SDKs for "zstd where
supported, gzip fallback" and §19 step 3 lists `decompress` among the gateway's operations —
and nothing decompressed, so a compressed body was a JSON parse failure. §19.1 names
"maximum compressed request bytes" and "maximum decompressed bytes" as limits, and neither
existed.

So the encoding question was being asked before the two cheaper questions underneath it had
been. `cmd/wiresize` was written to answer all three with numbers instead of intuition,
against the real §17 envelope and a Unity-shaped batch of five events' properties.

## The measurement

A 500-event batch, JSON, bytes per event:

| | with a client `event_id` | without one |
|---|---|---|
| uncompressed | 289.7 | 239.7 |
| gzip | 36.4 | 11.9 |
| zstd | 31.6 | **8.1** |

And the same batch broken down by how the identifier is spelled, compressed with zstd:

| `event_id` | bytes/event |
|---|---|
| UUID, 36 characters | 31.7 |
| base64url of the same 16 bytes, 22 characters | 29.1 |
| omitted | 8.1 |

That second table is the whole argument. Re-spelling the identifier more compactly — roughly
what a binary encoding does to it — saves under three bytes. Removing its sixteen bytes of
entropy saves twenty-four. **The syntax is nearly free after compression; the entropy is
not.**

## Decision

**The gateway decompresses `gzip` and `zstd` request bodies**, and no other encoding. A
closed set: `deflate` is the raw-DEFLATE/zlib ambiguity that has produced a decade of
interoperability bugs, `br` needs a dependency for a format no game SDK ships, and an
encoding nobody sends is an attack surface nobody tests. Anything else is a 415 naming
itself.

**The two limits §19.1 asks for are enforced at the only two places they mean anything.**
The compressed body is capped on the wire before a byte is decompressed. The decompressed
body is capped by the route's own body limit, which the framework applies while binding — so
a compressed batch and an uncompressed one are held to the same size, and a bomb stops being
*read* at the limit rather than being read and then measured. This is why decompression is
middleware and not a step inside the handler: by the time a handler runs the body is already
decoded, and a limit applied after the fact is not a limit.

**`event_id` becomes optional, and the gateway derives one when it is absent**, as a v5 UUID
over the batch id and the event's position in the batch. An SDK should not send one.

**`batch_id` must be a UUID, and a supplied `event_id` must be one too.** Both are enforced
at the gateway now. They were not, and storage required it anyway — see Consequences.

**Protobuf is not adopted.** Not "not yet, in a phase": the measurement says the win it is
reached for is already collected by compression. If it is revisited it should be for parse
CPU at the gateway, measured under load, and it should arrive as content-type negotiation
beside JSON rather than instead of it — §9 requires the public contract to survive the Stage
1 to Stage 2 move, and a second wire format is additive or it is a breaking change.

## Consequences

- A 500-event batch goes from 290 bytes an event to 8.1 — **36× smaller** — or 11.9 with
  gzip, which is what a game client will actually use: gzip is in Unity's and every engine's
  standard library, while zstd needs a native plugin per platform. zstd is therefore the
  server-to-server encoding and gzip the client one, which is what §17's "where supported"
  turns out to mean in practice.
- At §4's ceiling that is 290 GB of ingress a day against 8, and the bandwidth bill is the
  smaller half of the argument.
- **Batch size is now a lever the SDK controls.** The same events cost 16.5 bytes each in a
  50-event batch and 8.1 in a 500-event one, because a batch's fixed header and its repeated
  field names amortise. §18's flush policy is where that is spent.
- **Derivation rests on §18's immutability rule**, which was already there: the same events,
  in the same order, under the same batch id, on every retry. An SDK that re-packed a batch
  between attempts would produce different ids for the same events and break §21's read-side
  deduplication. That moves a rule that was advice into something a conformance test has to
  hold, which is a cost this record is choosing to pay.
- **A client reusing one batch id for different content now collides.** It was always a
  protocol violation; it is now one with a consequence, and the conformance suite has to say
  so out loud.
- Two silent failures are closed, and finding them is what made the identifier rules
  non-negotiable. The gateway accepted any 8-to-64-character `batch_id` and any non-empty
  `event_id`, answered **202**, and the writer then found both unparseable as UUIDs — losing
  the event in one case and the entire message in the other, to a log line no SDK could ever
  see. An acknowledgement that is not true is worse than a refusal.
- The dead-letter path lost its only natural producer. `TestAPoisonedMessageIsParkedAndTheQueueMovesOn`
  now publishes its poison directly onto the bus, because the gateway can no longer make one
  — which is the right shape for that test anyway: "the gateway cannot produce one today" is
  not the same claim as "one can never arrive".

## Alternatives considered

**Protobuf now.** Covered above: measured at roughly a tenth of what compression already
gives, at the cost of a code-generation toolchain in every one of §15's eleven SDK targets
and a schema-evolution discipline in all of them.

**A compact JSON envelope — short keys, arrays instead of objects.** Saves what compression
already deletes, and costs every integration a wire format no one can read in a proxy log.
The measurement above is the argument against it: field names are not where the bytes are.

**Keep client-generated `event_id` and shorten it** to base64 or a ULID. Three bytes an event,
and it keeps a field every SDK must generate correctly. The derivation removes the field
instead of shrinking it.

**Derive `event_id` from `(batch_id, sequence)`** rather than from the position. `sequence` is
optional and is documented as monotonic *within a session*, not within a batch, so two events
in one batch can legitimately share one — and an id collision inside a batch deduplicates two
real events into one. The position is unique by construction.

**A compression ratio guard** instead of a decompressed-byte cap. Guesswork about what a
legitimate client compresses like, and it fails in both directions: a well-compressing batch
looks like an attack, and a patient attacker stays under the ratio.

**Let the reverse proxy decompress.** Moves the limits somewhere the application cannot state
them, and §19.1 wants them per project eventually. It also makes the local stack behave
differently from production, which is how a limit stops being tested.

## Enforcement

- `apps/backend/internal/transport/decompress.go` — the middleware, the closed encoding set,
  the wire cap, and the pooled decoders.
- `apps/backend/cmd/ingest-api/main.go` — installed with `app.Use` before any route binds.
- `apps/backend/internal/ingest/normalize.go` — `resolveEventID`, and the namespace the
  derivation is scoped to.
- `apps/backend/internal/schemas/ingest.go` — `batch_id` as a UUID.
- `TestACompressedBatchIsAccepted` asserts both encodings decode to the events the bus
  receives, rather than to a status code.
- `TestADecompressionBombIsRefusedBeforeItIsRead` sends 64 MB of zeroes compressed to a few
  kilobytes — deliberately far under the wire cap, so it proves the decompressed bound is
  what does the work.
- `TestADerivedEventIDSurvivesARedelivery` sends the same batch twice and asserts the ids
  come back identical, which is the property the whole derivation rests on.
- `TestASuppliedEventIDMustBeAUUID` and `TestBatchIDMustBeAUUID` cover the two silent
  failures.
- `cmd/wiresize` reproduces every number in this record.
