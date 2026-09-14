# 0011. A key is `public:private`, and only an Argon2id digest is stored

**Status:** Accepted
**Date:** 2026-09-13
**Relates to:** project.md §52, §53, Appendix C.10

## Context

A project key ships inside a game binary. Appendix C.10 states the consequence as an
invariant: **project keys are assumed discoverable by attackers.** They will be extracted,
shared, and used in volume by people who should not have them.

Two things follow. The stored form must not be presentable as a credential — a database
disclosure must not hand anyone a working key. And verification must be cheap for wrong
keys, because wrong keys will arrive constantly.

Those two requirements fight. A slow hash (Argon2id) satisfies the first and, applied to
every presented credential, makes a guessing flood into a CPU exhaustion attack: the
attacker sends garbage, the server spends 20 milliseconds per attempt.

## Decision

A key has two halves:

```text
ps_<kind>_<env>_<public>:<private>
```

The **public** half is stored in plain text and indexed. The **private** half is never
stored — only an Argon2id digest of it, PHC-encoded with its parameters.

Verification is then: parse, look up by public half (one indexed lookup), and compare the
private half against that one digest (**exactly one** Argon2 verification). A key whose
public half matches nothing never reaches the hash.

Three kinds, and the class is enforced per endpoint: `project` for SDKs, `reporting` for
the query API, `server` for server-to-server.

A digest written under weaker parameters is upgraded on next use, so raising the cost later
does not invalidate anyone's key.

## Consequences

- A stolen database yields public identifiers and Argon2id digests. Neither can be
  presented as a credential.
- The control plane cannot reproduce a key after minting it, which is why revocation works
  on the **public** half: it writes a short-lived tombstone that the resolver refuses
  matches against. Revocation by somebody who cannot reproduce the secret is the normal
  case, not the exception.
- Every rejection returns the same error, whether the key was unknown, malformed, revoked,
  or had a wrong secret. Telling them apart tells an attacker which one to work on.

### The bug this format caused, and the fix

The public half was originally generated with **base64url**, whose alphabet contains `_` —
the same character separating the segments. Roughly one key in eight therefore contained a
separator inside a segment and failed to parse, intermittently, presenting as an invalid
credential.

The giveaway was timing: a failure took **15 microseconds** where a genuine Argon2
verification takes about **21 milliseconds**. A rejection that fast had not reached the
hash, which meant it had failed before the lookup.

Fixed by generating the random portion in **base32**, whose alphabet cannot contain the
separator, with a 300-iteration property test asserting every minted key round-trips.

## Alternatives considered

**HMAC or SHA-256 of the whole key.** Fast enough to make an offline attack on a leaked
database worthwhile, which is the thing being defended against.

**Argon2 over the whole presented credential with no public half.** Requires either hashing
against every stored key or a fast pre-index — which is the public half, arrived at by a
longer route.

**Opaque random tokens looked up by exact value.** Then the stored value *is* the
credential, and a database disclosure is a credential disclosure.

## Enforcement

- `apps/backend/internal/core/apikey.go` — minting, parsing, `HashSecret`, `VerifySecret`,
  `NeedsRehash`, and the base32 random identifier.
- `apps/backend/internal/core/apikey_test.go` — the 300-iteration round-trip property test,
  the malformed-key table, and `TestCacheDigestRevealsNothing`.
- `cmd/ingest-api/main_test.go:TestOnlyAProjectKeyMayIngest` — the class check.
- `…:TestRevocationTakesEffectImmediately` — the tombstone.
