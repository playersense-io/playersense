package alerts

import (
	"context"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/internal/billing"
)

// AccountMoved announces a billing account's change of state, in the transaction that made it:
// into its grace period, suspended, or back from a suspension. Other moves say nothing: leaving the
// grace period by paying is the good news nobody needs paging for.
func AccountMoved(ctx context.Context, client *ent.Client, account *ent.BillingAccount,
	from, to billingaccount.State, balanceMicros, arrearsMicros int64) error {
	var kind string
	switch {
	case to == billingaccount.StateSuspended && from != billingaccount.StateSuspended:
		kind = AccountSuspended
	case from == billingaccount.StateSuspended && to != billingaccount.StateSuspended:
		kind = AccountRestored
	case from == billingaccount.StateActive && to == billingaccount.StateGracePeriod:
		kind = PaymentOverdue
	default:
		return nil
	}
	data := map[string]any{"balance": billing.USD(balanceMicros)}
	if arrearsMicros > 0 {
		data["owed"] = billing.USD(arrearsMicros)
	}
	return Publish(ctx, client, Event{OrganizationID: account.OrganizationID, Type: kind, Data: data})
}

// TopUpDeclined announces an automatic top-up that did not charge, and why.
func TopUpDeclined(ctx context.Context, client *ent.Client, account *ent.BillingAccount, amountMicros int64, reason string) error {
	data := map[string]any{"reason": reason, "balance": billing.USD(account.BalanceMicros)}
	if amountMicros > 0 {
		data["amount"] = billing.USD(amountMicros)
	}
	return Publish(ctx, client, Event{OrganizationID: account.OrganizationID, Type: TopUpFailed, Data: data})
}
