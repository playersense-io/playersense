package billing

import "fmt"

// Features are what a plan unlocks, as opposed to how much of something it includes (ADR 0042).
//
// The same split as entitlements: PlayerSense owns a closed set of names, and which plan has which is
// data on the plan's row — so putting MCP access on a new plan is an update, not a deploy, and a typo
// ("mcp_acess") is refused when the plan is saved rather than quietly unlocking nothing.
type Feature string

const (
	// PremiumSupport is a shared Slack channel with the PlayerSense team.
	PremiumSupport Feature = "premium_support"

	// SlackReports is the daily report builder that posts to a studio's own Slack.
	SlackReports Feature = "slack_reports"

	// MCPAccess is PlayerSense's MCP server, for a studio's own AI tools.
	MCPAccess Feature = "mcp_access"
)

// features is the closed set, in the order a pricing page lists them.
var features = []Feature{PremiumSupport, SlackReports, MCPAccess}

// AllFeatures returns the vocabulary.
func AllFeatures() []Feature {
	out := make([]Feature, len(features))
	copy(out, features)
	return out
}

// KnownFeature reports whether a name is one PlayerSense understands.
func KnownFeature(name string) bool {
	for _, f := range features {
		if string(f) == name {
			return true
		}
	}
	return false
}

// UnknownFeatureError names what was written and is not in the vocabulary.
type UnknownFeatureError struct{ Name string }

func (e *UnknownFeatureError) Error() string {
	return fmt.Sprintf("%q is not a feature PlayerSense understands", e.Name)
}

// ValidateFeatures refuses a name outside the vocabulary.
func ValidateFeatures(names []string) error {
	for _, name := range names {
		if !KnownFeature(name) {
			return &UnknownFeatureError{Name: name}
		}
	}
	return nil
}

// HasFeature is the check a feature makes before it serves anyone: whether the organization's plan
// unlocks it. The plan is the subscription's while the subscription is live and the default plan once
// it ends (ADR 0042), so a plan's features are always the ones the organization has paid for.
func HasFeature(planFeatures []string, feature Feature) bool {
	for _, name := range planFeatures {
		if name == string(feature) {
			return true
		}
	}
	return false
}
