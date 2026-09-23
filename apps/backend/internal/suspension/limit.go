package suspension

import (
	"context"
	"fmt"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/internal/billing"
)

// How much an account may owe (ADR 0043).
//
// A flat limit treats a studio spending $5,000 a month like one spending $100, so the limit follows
// what the account spends:
//
//	limit = max(floor, Percent% of its usage in the last 30 days)
//
// The floor is the plan's credit_limit_micros, or the deployment's default — so a new or small
// studio is not cut off over a few dollars, and Enterprise can carry a higher floor. And staff can
// set an exact limit for one organization (its credit_limit_micros override), which wins over both:
// the account that negotiated terms, or the one being watched.

// Policy is the deployment's side of the rule.
type Policy struct {
	Floor   int64 // micros, for plans that state no credit_limit_micros
	Percent int64 // of the last 30 days' usage
}

// LimitSource says which part of the rule decided a limit, for the billing page.
type LimitSource string

const (
	FromOverride LimitSource = "override" // staff set it for this organization
	FromUsage    LimitSource = "usage"    // a share of what it spent
	FromFloor    LimitSource = "floor"    // the plan's or the deployment's minimum
)

// Window is how far back spending is counted.
const Window = 30 * 24 * time.Hour

// Limit is an account's credit limit and where it came from. The account's plan edge must be loaded.
//
// One indexed sum over the account's ledger (account_id, created_at), and only for accounts that
// owe or whose billing page is open — never on the ingestion path.
func Limit(ctx context.Context, client *ent.Client, account *ent.BillingAccount, policy Policy, at time.Time) (int64, LimitSource, error) {
	if amount, stated := account.EntitlementOverrides[string(billing.CreditLimit)]; stated {
		return amount, FromOverride, nil
	}
	floor := policy.Floor
	if p := account.Edges.Plan; p != nil {
		if amount, stated := p.Entitlements[string(billing.CreditLimit)]; stated {
			floor = amount
		}
	}

	var sums []struct {
		Sum int64 `json:"sum"`
	}
	if err := client.LedgerEntry.Query().
		Where(
			ledgerentry.AccountID(account.ID),
			ledgerentry.KindEQ(ledgerentry.KindUsage),
			ledgerentry.CreatedAtGTE(at.Add(-Window)),
		).
		Aggregate(ent.Sum(ledgerentry.FieldAmountMicros)).
		Scan(ctx, &sums); err != nil {
		return 0, "", fmt.Errorf("sum the account's usage: %w", err)
	}
	var spent int64
	if len(sums) == 1 {
		spent = -sums[0].Sum // charges are negative, volume credits positive: the net
	}
	share := spent * policy.Percent / 100
	if share > floor {
		return share, FromUsage, nil
	}
	return floor, FromFloor, nil
}
