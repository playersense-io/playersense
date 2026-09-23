package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v85"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/plan"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/pricingcache"
)

// Subscriptions (ADR 0042): Stripe runs them — renewals, invoices, retries, cancellation in the portal
// — and these events keep the account in step.
//
// A live subscription puts the account on the plan its Price belongs to; one that ends puts it back
// on the default plan. Live means active, trialing or past_due: past_due is Stripe still retrying a
// failed renewal, and taking a studio's features away during the retries would punish a card that is
// about to succeed. When Stripe gives up, the subscription is canceled or unpaid, and that ends it.
//
// Events arrive out of order. Each one is applied only if Stripe created it after the last one applied
// to the account, so an `updated` delivered after the `deleted` cannot bring a subscription back.
//
// The plan's rate card prices the account's next players from then on: its cached pricing is
// invalidated here, and reaches every charger within the charger's memory TTL.

// Live is whether a Stripe subscription status still grants its plan.
func Live(status string) bool { return liveSubscription(stripe.SubscriptionStatus(status)) }

// liveSubscription is whether a status still grants the plan.
func liveSubscription(status stripe.SubscriptionStatus) bool {
	switch status {
	case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing, stripe.SubscriptionStatusPastDue:
		return true
	}
	return false
}

func (w *Webhooks) subscription(ctx context.Context, tx *ent.Tx, event stripe.Event) (handled, bool, error) {
	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted",
		"customer.subscription.paused", "customer.subscription.resumed":
	default:
		return handled{}, false, nil
	}
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return handled{}, true, fmt.Errorf("decode the subscription: %w", err)
	}
	if sub.Customer == nil || sub.Items == nil || len(sub.Items.Data) == 0 || sub.Items.Data[0].Price == nil {
		return ignored("the subscription names no customer or price"), true, nil
	}

	account, err := tx.BillingAccount.Query().
		Where(billingaccount.StripeCustomerID(sub.Customer.ID)).
		ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ignored("no account has this customer"), true, nil
	}
	if err != nil {
		return handled{}, true, err
	}

	at := time.Unix(event.Created, 0).UTC()
	if account.SubscriptionSyncedAt != nil && at.Before(*account.SubscriptionSyncedAt) {
		return ignored("older than the subscription state already applied"), true, nil
	}
	// Only the account's own subscription can end its plan. One that never became the account's —
	// a checkout that expired while incomplete — ending must not move an account staff put on a plan
	// by contract. (One starting while another is live is a second subscription the dashboard
	// refuses to open; it is taken as the account's latest word, and the older one should be
	// canceled in Stripe.)
	ending := event.Type == "customer.subscription.deleted" || !liveSubscription(sub.Status)
	if ending && (account.StripeSubscriptionID == nil || *account.StripeSubscriptionID != sub.ID) {
		return ignored("not the account's subscription"), true, nil
	}

	item := sub.Items.Data[0]
	update := tx.BillingAccount.UpdateOneID(account.ID).
		SetSubscriptionStatus(string(sub.Status)).
		SetSubscriptionCancelAtPeriodEnd(sub.CancelAtPeriodEnd).
		SetSubscriptionSyncedAt(at)
	if item.CurrentPeriodEnd > 0 {
		update = update.SetSubscriptionPeriodEnd(time.Unix(item.CurrentPeriodEnd, 0).UTC())
	}
	if item.Price.Recurring != nil {
		switch item.Price.Recurring.Interval {
		case stripe.PriceRecurringIntervalMonth:
			update = update.SetSubscriptionInterval(billingaccount.SubscriptionIntervalMonth)
		case stripe.PriceRecurringIntervalYear:
			update = update.SetSubscriptionInterval(billingaccount.SubscriptionIntervalYear)
		}
	}

	if ending {
		// Back to the default plan, if there is one to go back to.
		fallback, err := tx.Plan.Query().Where(plan.IsDefault(true), plan.Active(true)).First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return handled{}, true, err
		}
		if fallback != nil {
			update = update.SetPlanID(fallback.ID)
		}
	} else {
		subscribed, err := tx.Plan.Query().Where(plan.Or(
			plan.StripePriceMonthlyID(item.Price.ID), plan.StripePriceYearlyID(item.Price.ID),
		)).Only(ctx)
		if ent.IsNotFound(err) {
			w.log().Error("a subscription's price belongs to no plan", "subscription", sub.ID, "price", item.Price.ID)
			return ignored("the price belongs to no plan"), true, nil
		}
		if err != nil {
			return handled{}, true, err
		}
		update = update.SetPlanID(subscribed.ID).SetStripeSubscriptionID(sub.ID)
	}
	if err := update.Exec(ctx); err != nil {
		return handled{}, true, fmt.Errorf("apply the subscription: %w", err)
	}

	// After the commit would be tidier; before is safe: a stale read between here and the commit
	// re-caches the old plan for at most the charger's memory TTL, and the next event invalidates again.
	if err := pricingcache.InvalidateOrganization(ctx, w.Cache, account.OrganizationID); err != nil {
		w.log().Warn("could not invalidate cached pricing after a plan change", "error", err)
	}
	return handled{status: stripeevent.StatusProcessed, accountID: account.ID}, true, nil
}
