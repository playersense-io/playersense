# 0004. A reinstall is a new player, and the bias is documented rather than fixed

**Status:** Accepted
**Date:** 2026-09-05
**Relates to:** project.md §16, §100

## Context

Every cohort number in the product depends on recognising the same installation across
days. The identifier that does the recognising is the most consequential privacy decision
in the system, because everything downstream inherits whatever it is.

The industry's answer is a persistent device identifier: an advertising ID, a vendor ID, a
keychain entry that survives deletion, or a fingerprint assembled from device attributes.
Each survives a reinstall. Each is also, precisely, a mechanism for recognising a person
who has taken an action intended to stop being recognised.

It is worth being honest about the ceiling here rather than implying one exists. Attribution
vendors whose entire business is this problem — Adjust among them — have not solved
reinstall identity either. What they have are probabilistic methods that are less accurate
than they sound and are exactly the methods Apple's rules and GDPR both take a dim view of.

## Decision

**`install_id` is a random UUID generated on first launch and stored in ordinary app
storage. It is deleted when the app is deleted, and a reinstall produces a new one.**

No advertising identifier on any platform. No fingerprinting or probabilistic matching. No
identifier shared across apps or vendors. No keychain persistence designed to outlive an
uninstall.

## Consequences

The honest cost, stated where a customer will read it rather than discovered in a support
thread:

- **A reinstalling player is counted as a new install.** Retention is therefore biased
  *downward* for games with meaningful reinstall rates, and install counts are biased
  *upward*.
- The bias is systematic rather than random, so it is stable over time: week-over-week
  comparisons remain meaningful even though the absolute number is not exact.
- Cross-device play is not linked unless the studio supplies its own account identifier,
  which it may (§16 covers the coalesced actor key).

The return is worth naming too, because it is a product property and not merely a
compliance one. PlayerSense **does not track** as Apple defines the word. The SDK declares
`NSPrivacyTracking: false`, and a studio embedding it does not have to show an ATT prompt
on PlayerSense's behalf, or explain PlayerSense to an App Review team.

## Alternatives considered

**Keychain persistence (iOS).** Survives deletion, which is the entire objection. It also
produces a support case the first time a player asks why deleting the app did not delete
their data, and the honest answer is "it did not".

**IDFA / GAID.** Requires ATT on iOS, which means the studio shows a prompt for
PlayerSense's benefit and eats the opt-out rate. It also makes PlayerSense an advertising
SDK in the eyes of every app store questionnaire.

**Fingerprinting.** Explicitly prohibited by Apple, treated as an identifier by GDPR, and
the technique most likely to be reclassified as hostile in the next platform release.

**Probabilistic matching.** Accepting an identifier that is sometimes wrong, in the layer
everything else is computed from. A retention number that is quietly incorrect for some
fraction of players is worse than one that is correct about a slightly different question.

## Enforcement

- `apps/backend/internal/schemas/ingest.go` — the batch envelope accepts `install_id` and
  nothing resembling an advertising identifier; there is no field for one.
- `apps/backend/internal/ingest/normalize.go` — property scanning refuses batches carrying
  direct personal data, so a studio cannot smuggle a stable identifier in as a property.
- `cmd/ingest-api/main_test.go:TestDirectPersonalDataIsRefused`.
