package billing_test

import (
	"errors"
	"strings"
	"testing"

	"playersense.dev/backend/internal/billing"
)

// §37: "do not hardcode commercial values in application logic." The vocabulary is closed and
// the amounts are rows, so a name outside the vocabulary has to be refused when it is written —
// a plan holding "included_maus" would otherwise include nothing and say so to nobody.
func TestAnEntitlementOutsideTheVocabularyIsRefused(t *testing.T) {
	err := billing.Limits{"included_maus": 20_000}.Validate()
	if err == nil {
		t.Fatal("a misspelled entitlement validated")
	}
	if !strings.Contains(err.Error(), "included_maus") {
		t.Errorf("the error does not name it: %v", err)
	}

	if err := (billing.Limits{billing.IncludedMAU: 20_000}).Validate(); err != nil {
		t.Errorf("the correct spelling was refused: %v", err)
	}
}

// Zero is a real allowance — "no free events at all" is a plan somebody may sell — so a caller
// has to be able to tell it from unstated. Conflating them hands out the free tier's allowance
// to a plan that deliberately withheld it.
func TestAStatedZeroIsNotTheSameAsUnstated(t *testing.T) {
	limits := billing.Limits{billing.IncludedEvents: 0}

	if amount, stated := limits.Get(billing.IncludedEvents); !stated || amount != 0 {
		t.Errorf("a stated zero read back as %d, stated=%v", amount, stated)
	}
	if _, stated := limits.Get(billing.IncludedMAU); stated {
		t.Error("an entitlement nobody stated read back as stated")
	}
}

// One enterprise customer with ten times the included MAU is a row, not a plan nobody else is
// on. An override of zero is honoured too, because withholding an allowance the plan includes
// is a thing a negotiation produces.
func TestAnOverrideLayersOntoThePlan(t *testing.T) {
	plan := billing.Limits{
		billing.IncludedMAU:    20_000,
		billing.IncludedEvents: 5_000_000,
	}
	resolved := billing.Resolve(plan, billing.Limits{
		billing.IncludedMAU:    200_000,
		billing.MaxProjects:    50,
		billing.IncludedEvents: 0,
	})

	for _, c := range []struct {
		name billing.Entitlement
		want int64
	}{
		{billing.IncludedMAU, 200_000}, // overridden
		{billing.IncludedEvents, 0},    // overridden to nothing, deliberately
		{billing.MaxProjects, 50},      // added by the override alone
	} {
		if got, stated := resolved.Get(c.name); !stated || got != c.want {
			t.Errorf("%s resolved to %d (stated=%v), want %d", c.name, got, stated, c.want)
		}
	}

	// And the plan it was layered onto is untouched, because two accounts share it.
	if got, _ := plan.Get(billing.IncludedMAU); got != 20_000 {
		t.Errorf("resolving mutated the plan: included_mau is now %d", got)
	}
}

// The four auto-top-up columns are legal only in relation to each other, which is not something
// a CHECK constraint can say readably.
func TestAutoTopUp(t *testing.T) {
	for _, c := range []struct {
		name    string
		config  billing.AutoTopUp
		wantErr string
	}{
		{
			name:   "off, and half-filled, which is just an abandoned form",
			config: billing.AutoTopUp{Enabled: false, Amount: 1},
		},
		{
			name: "on, with a card and sane amounts",
			config: billing.AutoTopUp{
				Enabled: true, Threshold: 10 * billing.Micros, Amount: 50 * billing.Micros,
				HasCard: true,
			},
		},
		{
			name: "on with no card does nothing at all, silently",
			config: billing.AutoTopUp{
				Enabled: true, Threshold: 10 * billing.Micros, Amount: 50 * billing.Micros,
			},
			wantErr: "payment method",
		},
		{
			name: "below the floor, where most of the charge is the fee",
			config: billing.AutoTopUp{
				Enabled: true, Threshold: 1 * billing.Micros, Amount: 2 * billing.Micros,
				HasCard: true,
			},
			wantErr: "minimum",
		},
		{
			name: "above the ceiling, which is a blast radius rather than a policy",
			config: billing.AutoTopUp{
				Enabled: true, Threshold: 10 * billing.Micros, Amount: billing.MaxTopUp + 1,
				HasCard: true,
			},
			wantErr: "maximum",
		},
		{
			name: "a threshold at the amount charges all night",
			config: billing.AutoTopUp{
				Enabled: true, Threshold: 50 * billing.Micros, Amount: 50 * billing.Micros,
				HasCard: true,
			},
			wantErr: "immediately",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.config.Validate()
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("refused a legal configuration: %v", err)
			case c.wantErr != "" && err == nil:
				t.Error("accepted it")
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Errorf("the refusal does not explain itself: %v", err)
			}
		})
	}
}

// Money is rendered for people, never parsed back. The rounding matters because it is what a
// customer reads in a refusal.
func TestUSD(t *testing.T) {
	for _, c := range []struct {
		micros int64
		want   string
	}{
		{0, "$0.00"},
		{billing.MinTopUp, "$5.00"},
		{billing.MaxTopUp, "$5000.00"},
		{99 * billing.Micros, "$99.00"},
		{1_234_567, "$1.23"},   // truncated, not rounded up: we never overstate a charge
		{-2_500_000, "-$2.50"}, // a debt is a real number
	} {
		if got := billing.USD(c.micros); got != c.want {
			t.Errorf("USD(%d) = %s, want %s", c.micros, got, c.want)
		}
	}
}

func TestAFeatureIsOnlyWhatThePlanSays(t *testing.T) {
	enterprise := []string{string(billing.PremiumSupport), string(billing.MCPAccess)}
	if !billing.HasFeature(enterprise, billing.MCPAccess) || billing.HasFeature(enterprise, billing.SlackReports) {
		t.Error("a feature was granted or withheld against the plan")
	}
	if billing.HasFeature(nil, billing.MCPAccess) {
		t.Error("no plan features granted one")
	}
	var unknown *billing.UnknownFeatureError
	if err := billing.ValidateFeatures([]string{"mcp_acess"}); !errors.As(err, &unknown) {
		t.Errorf("a misspelled feature validated: %v", err)
	}
}
