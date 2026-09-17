package targeting

import (
	"crypto/sha256"
	"encoding/binary"
)

// Buckets is the resolution of a percentage rollout: 10,000, so a rollout can be expressed
// to one hundredth of a percent (§43).
const Buckets = 10000

// Bucket places a player in a stable slot between 0 and 9999.
//
//	bucket = hash(project_id + flag_key + actor_key + salt) % 10000
//
// Three properties §43 asks for, and each is a real failure when it is missing:
//
//   - **The same player stays in the same variant.** An experiment whose assignment moves
//     measures nothing: a player who saw A on Monday and B on Tuesday is in both arms, and
//     the result is noise that looks like a finding.
//   - **It does not reshuffle on app launch.** Nothing here reads a clock or a random
//     source, so the same inputs give the same answer forever, on any machine, in any
//     process.
//   - **A salt change reshuffles deliberately.** Reusing an audience across two experiments
//     correlates them; changing the salt is how a studio says "draw a fresh sample".
//
// SHA-256 rather than a fast non-cryptographic hash, and the choice is about stability
// rather than secrecy: maphash is seeded per process, FNV's distribution over short similar
// keys is poor, and xxhash would be a dependency whose version could change the answer. A
// player's bucket must survive a Go upgrade, so the algorithm is pinned to something that
// cannot quietly change. It costs about a microsecond, once per session.
//
// The separator matters. Without it, project "ab" + flag "c" and project "a" + flag "bc"
// hash identically, and two flags in two projects would share an audience for no visible
// reason.
func Bucket(projectID, flagKey, actorKey, salt string) int {
	sum := sha256.Sum256([]byte(projectID + "\x00" + flagKey + "\x00" + actorKey + "\x00" + salt))
	return int(binary.BigEndian.Uint64(sum[:8]) % Buckets)
}

// InRollout reports whether a player is inside a percentage rollout.
//
// `percent` is hundredths: 2500 is 25%. Integer hundredths rather than a float, because a
// float percentage makes "is this player in the first 25%" depend on rounding, and a
// boundary that moves by one bucket moves real players between arms.
//
// A rollout of 0 reaches nobody and 10000 reaches everybody, both without hashing — which
// also means a fully rolled-out flag costs nothing per evaluation, and that is the state
// most flags live in for most of their life.
func InRollout(percent int, projectID, flagKey, actorKey, salt string) bool {
	switch {
	case percent <= 0:
		return false
	case percent >= Buckets:
		return true
	}

	// An anonymous player has no stable identity to hash, so there is nothing to keep them
	// in one arm. Excluded rather than assigned at random: a player who flips arms every
	// launch pollutes both, and a partial rollout to players who cannot be measured is not
	// a rollout.
	if actorKey == "" {
		return false
	}
	return Bucket(projectID, flagKey, actorKey, salt) < percent
}
