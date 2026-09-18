package billing

import "fmt"

// UnknownEntitlementError names what was written and is not in the vocabulary.
//
// A named type rather than fmt.Errorf so a handler can tell a typo in a dashboard form from a
// database that is unreachable, and answer the first with a field error instead of a 500.
type UnknownEntitlementError struct{ Name Entitlement }

func (e *UnknownEntitlementError) Error() string {
	return fmt.Sprintf("%q is not an entitlement PlayerSense understands", e.Name)
}

// NegativeEntitlementError is an allowance below zero.
//
// Refused rather than clamped: "included_mau: -1" is somebody's finger on a minus key, and
// treating it as zero would sell them a plan that includes nothing while the form said it
// saved.
type NegativeEntitlementError struct {
	Name   Entitlement
	Amount int64
}

func (e *NegativeEntitlementError) Error() string {
	return fmt.Sprintf("%q cannot be %d; an allowance is not negative", e.Name, e.Amount)
}
