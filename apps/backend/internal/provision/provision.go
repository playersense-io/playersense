// Package provision gives an organization somewhere to be charged.
//
// Its own package because two unrelated callers need it — identity, when an organization is created,
// and the charger, for one created before billing accounts were provisioned — and neither should
// import the other.
package provision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/plan"
)

// sqlNoRows is what an upsert that did nothing may report, and is not a failure.
var sqlNoRows = sql.ErrNoRows

// ErrNoOrganization is a batch for an organization that no longer exists.
var ErrNoOrganization = errors.New("the organization no longer exists")

// Account gives an organization a billing account on the default plan.
//
// Called where an organization is created, and again by the charger for an organization created
// before this existed — so every organization that sends players has somewhere to charge them. A
// no-op when the account already exists (the organization id is unique on the table, and a race
// between the two callers is a conflict that does nothing), and when no plan is marked default:
// then there is no plan to put it on, and inventing one would be choosing a price.
//
// Returns the account, or nil when there is no default plan.
func Account(ctx context.Context, tx *ent.Tx, orgID int) (*ent.BillingAccount, error) {
	existing, err := tx.BillingAccount.Query().Where(billingaccount.OrganizationID(orgID)).Only(ctx)
	if err == nil {
		return existing, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("find the billing account: %w", err)
	}

	defaultPlan, err := tx.Plan.Query().
		Where(plan.IsDefault(true), plan.Active(true)).
		Order(ent.Asc(plan.FieldID)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find the default plan: %w", err)
	}

	if err := tx.BillingAccount.Create().
		SetOrganizationID(orgID).
		SetPlanID(defaultPlan.ID).
		SetCurrency(defaultPlan.Currency).
		OnConflictColumns(billingaccount.FieldOrganizationID).
		DoNothing().
		Exec(ctx); err != nil && !errors.Is(err, sqlNoRows) {
		// The organization is gone — deleted while its last batches were still queued. There
		// is nobody to charge, and retrying would stall every account behind it on the bus.
		if ent.IsConstraintError(err) {
			return nil, ErrNoOrganization
		}
		return nil, fmt.Errorf("open the billing account: %w", err)
	}
	return tx.BillingAccount.Query().Where(billingaccount.OrganizationID(orgID)).Only(ctx)
}
