package billing

import "fmt"

// AutoTopUp is an account's automatic-charge configuration, as a person set it.
//
// A value rather than three loose arguments, because the rule is about how the three relate
// and a signature that takes them separately is a signature somebody calls with two of them.
type AutoTopUp struct {
	Enabled bool

	// Threshold is the balance below which a charge is made, and Amount is what is
	// charged.
	Threshold int64
	Amount    int64

	// HasCard is whether Stripe holds a card it can charge without the customer present. Asked
	// of Stripe when the setting is saved, never stored (ADR 0040).
	HasCard bool
}

// Validate checks the rule the schema cannot state.
//
// Four columns whose legality depends on each other is not something a CHECK constraint can
// say without becoming unreadable, and a trigger would put the rule somewhere nobody reading
// this package would look. It lives here for the same reason the member cap lives in
// internal/identity.
func (a AutoTopUp) Validate() error {
	if !a.Enabled {
		// The amounts are still stored while it is off, so that turning it back on
		// restores what was configured rather than presenting an empty form. They are
		// deliberately not checked here — a half-filled form somebody abandoned is not an
		// error until they switch it on.
		return nil
	}

	if !a.HasCard {
		// The failure this prevents is silent rather than loud: automatic top-up enabled
		// against no card does nothing at all, and the account discovers it by being
		// suspended on a day when somebody thought it was handled.
		return fmt.Errorf("automatic top-up needs a saved payment method to charge; " +
			"add one with a top-up or in the billing portal")
	}
	if a.Amount < MinTopUp {
		return fmt.Errorf(
			"an automatic charge of %s is below the %s minimum; most of it would be the "+
				"processing fee", USD(a.Amount), USD(MinTopUp))
	}
	if a.Amount > MaxTopUp {
		return fmt.Errorf(
			"an automatic charge of %s is above the %s maximum", USD(a.Amount), USD(MaxTopUp))
	}
	if a.Amount%CentMicros != 0 || a.Threshold%CentMicros != 0 {
		// A card is charged in cents. Rounding a fraction of one would charge a different
		// amount from the one the customer set.
		return fmt.Errorf("automatic top-up amounts are whole cents")
	}
	if a.Threshold < 0 {
		return fmt.Errorf("a threshold of %s is below zero", USD(a.Threshold))
	}

	// A threshold at or above the amount tops up into a balance that is still under the
	// threshold, so the next check charges again, and the next. Refused rather than
	// rate-limited: the configuration is the bug, and a limiter would hide it while the
	// customer's card was charged all night.
	if a.Threshold >= a.Amount {
		return fmt.Errorf(
			"topping up %s whenever the balance falls below %s would charge again "+
				"immediately; the amount has to exceed the threshold",
			USD(a.Amount), USD(a.Threshold))
	}
	return nil
}

// USD renders micros the way a person reads money.
//
// For error messages and statements only. Never for arithmetic: the whole reason amounts are
// int64 micros is that money that has been through a float is money somebody reconciles by
// hand.
func USD(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	return fmt.Sprintf("%s$%d.%02d", sign, micros/Micros, (micros%Micros)/10_000)
}
