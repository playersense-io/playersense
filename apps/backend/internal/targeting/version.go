package targeting

import (
	"strconv"
	"strings"
)

// compareVersions orders two app versions the way a person means them.
//
// This exists because the obvious thing is wrong in a way that looks right. As strings,
// "1.9.0" sorts *after* "1.12.0", so a campaign targeting "1.12.0 and later" silently
// excludes everyone who upgraded to 1.12 — and reaches the players still on 1.9. The
// symptom is a campaign with a strange audience, not an error, so nobody looks for a bug.
//
// Deliberately not a full semver implementation. A game's version is whatever the studio
// typed into the build settings: "1.12", "1.12.0", "2.0.1-rc3", "1.12.0.4881". So this
// compares dot-separated parts numerically where both sides are numeric and
// lexicographically otherwise, treats a missing part as zero — 1.12 equals 1.12.0 — and
// ignores anything after the first '-' or '+', because a pre-release suffix orders before
// its release in semver and that subtlety has no place in a targeting rule a studio writes
// once.
func compareVersions(left, right string) int {
	leftParts := versionParts(left)
	rightParts := versionParts(right)

	longest := len(leftParts)
	if len(rightParts) > longest {
		longest = len(rightParts)
	}

	for i := 0; i < longest; i++ {
		leftPart := partAt(leftParts, i)
		rightPart := partAt(rightParts, i)

		leftNum, leftNumeric := strconv.ParseInt(leftPart, 10, 64)
		rightNum, rightNumeric := strconv.ParseInt(rightPart, 10, 64)

		switch {
		case leftNumeric == nil && rightNumeric == nil:
			if leftNum != rightNum {
				if leftNum < rightNum {
					return -1
				}
				return 1
			}
		default:
			// At least one part is not a number — "1.0.beta" against "1.0.2". Compared
			// as text, which is arbitrary but stable, and the alternative is calling them
			// equal and letting a rule match both.
			if order := strings.Compare(leftPart, rightPart); order != 0 {
				return order
			}
		}
	}
	return 0
}

// versionParts splits on dots, after dropping any pre-release or build suffix.
func versionParts(version string) []string {
	version = strings.TrimSpace(version)
	if at := strings.IndexAny(version, "-+"); at >= 0 {
		version = version[:at]
	}
	if version == "" {
		return nil
	}
	return strings.Split(version, ".")
}

// partAt treats a missing part as "0", so 1.12 and 1.12.0 are the same version. A studio
// writing "1.12" in a rule means the release, not a version below every 1.12.x.
func partAt(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return "0"
}
