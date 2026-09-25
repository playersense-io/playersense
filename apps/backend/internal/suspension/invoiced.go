package suspension

import (
	"context"
	"errors"
	"fmt"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/payments"
)

// Invoiced accounts (ADR 0045): overdue, not owing, starts the clock.
//
// An invoiced account owes by design — everything it has used since its last invoice, and every invoice
// not yet due — so neither of ADR 0043's bounds can be read the way a prepaid account's are. Its two:
//
//   - **Overdue.** An open invoice past its due date makes it overdue (`overdue_since`) and puts it in
//     its grace period. Its contract's `overdue_grace_days` later, ingestion is suspended.
//   - **Exposure.** What it owes, billed or not — unbilled usage, open invoices and queued usage, less
//     any credit — reaching its credit limit suspends it at once. That is [Owed], unchanged, because the
//     balance nets the receivable.
//
// And this job alone lets a suspended invoiced account back in, once neither holds: a payment clears
// what is overdue, but the account may have been suspended for its exposure, which only this pass
// checks.

// invoiced makes one pass over invoiced accounts, and accounts with overdue invoices.
func (j *Job) invoiced(ctx context.Context) (suspended, reactivated int, err error) {
	now := j.now()
	if err := j.markOverdue(ctx, now); err != nil {
		return 0, 0, err
	}

	candidates, err := j.DB.Client.BillingAccount.Query().
		Where(billingaccount.Or(
			billingaccount.CollectionEQ(billingaccount.CollectionInvoiced),
			billingaccount.OverdueSinceNotNil(),
		), billingaccount.StateIn(billingaccount.StateActive, billingaccount.StateGracePeriod,
			billingaccount.StateSuspended)).
		WithPlan().
		All(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read invoiced accounts: %w", err)
	}
	var errs []error
	for _, account := range candidates {
		reason, due, err := j.invoicedDue(ctx, account, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch {
		case due && account.State != billingaccount.StateSuspended:
			moved, err := j.move(ctx, account, billingaccount.StateSuspended, now,
				billingaccount.StateNEQ(billingaccount.StateSuspended))
			if err != nil {
				errs = append(errs, fmt.Errorf("suspend account %d: %w", account.ID, err))
				continue
			}
			if moved {
				suspended++
				j.log().Warn("suspended ingestion for an invoiced account", "account_id", account.ID,
					"org", account.OrganizationID, "reason", reason)
			}
		case !due && account.State == billingaccount.StateSuspended &&
			account.Collection == billingaccount.CollectionInvoiced && account.ArrearsMicros == 0:
			// Back to grace while something is still overdue, otherwise active.
			state := billingaccount.StateActive
			if account.OverdueSince != nil {
				state = billingaccount.StateGracePeriod
			}
			moved, err := j.move(ctx, account, state, now,
				billingaccount.StateEQ(billingaccount.StateSuspended))
			if err != nil {
				errs = append(errs, fmt.Errorf("reactivate account %d: %w", account.ID, err))
				continue
			}
			if moved {
				reactivated++
				j.log().Info("let an invoiced account back in", "account_id", account.ID,
					"org", account.OrganizationID)
			}
		}
	}
	return suspended, reactivated, errors.Join(errs...)
}

// invoicedDue reports whether an invoiced — or overdue — account is past either of its bounds.
func (j *Job) invoicedDue(ctx context.Context, account *ent.BillingAccount, now time.Time) (string, bool, error) {
	if at := j.overdueSuspendsAt(ctx, account); at != nil && !now.Before(*at) {
		return fmt.Sprintf("an invoice has been overdue since %s, past its grace",
			account.OverdueSince.Format(time.DateOnly)), true, nil
	}
	if account.Collection != billingaccount.CollectionInvoiced {
		return "", false, nil
	}
	limit, source, err := Limit(ctx, j.DB.Client, account, j.Policy, now)
	if err != nil {
		return "", false, err
	}
	owed, err := Owed(ctx, j.DB.Client, account)
	if err != nil {
		return "", false, err
	}
	if owed >= limit {
		return fmt.Sprintf("owes %s billed and unbilled, at its %s credit limit (%s)",
			billing.USD(owed), billing.USD(limit), source), true, nil
	}
	return "", false, nil
}

// markOverdue records every account whose open invoice has passed its due date and is not yet marked,
// each in its own transaction (payments.ReassessOverdue). Stripe's invoice.overdue is not waited for.
func (j *Job) markOverdue(ctx context.Context, now time.Time) error {
	ids, err := j.DB.Client.Invoice.Query().
		Where(invoice.StatusEQ(invoice.StatusOpen), invoice.AmountRemainingMicrosGT(0), invoice.DueAtLT(now)).
		QueryAccount().
		Where(billingaccount.OverdueSinceIsNil()).
		IDs(ctx)
	if err != nil {
		return fmt.Errorf("find overdue invoices: %w", err)
	}
	var errs []error
	for _, id := range ids {
		tx, err := j.DB.Client.Tx(ctx)
		if err != nil {
			return err
		}
		if err := payments.ReassessOverdue(ctx, tx, id, now); err != nil {
			_ = tx.Rollback()
			errs = append(errs, err)
			continue
		}
		if err := tx.Commit(); err != nil {
			errs = append(errs, err)
			continue
		}
		j.log().Warn("an invoice is overdue", "account_id", id)
	}
	return errors.Join(errs...)
}

// overdueSuspendsAt is when an overdue account is suspended if it does not pay.
func (j *Job) overdueSuspendsAt(ctx context.Context, account *ent.BillingAccount) *time.Time {
	return OverdueSuspendsAt(ctx, j.DB.Client, account, j.OverdueGrace)
}

// OverdueSuspendsAt is when an account with an overdue invoice is suspended if it does not pay: its
// contract's grace after the first missed due date, or `fallback` when no contract says. Nil when
// nothing is overdue.
func OverdueSuspendsAt(ctx context.Context, client *ent.Client, account *ent.BillingAccount, fallback time.Duration) *time.Time {
	if account.OverdueSince == nil {
		return nil
	}
	grace := fallback
	if contract, err := client.BillingContract.Query().
		Where(billingcontract.AccountID(account.ID)).
		Order(ent.Desc(billingcontract.FieldEffectiveFrom), ent.Desc(billingcontract.FieldID)).
		First(ctx); err == nil {
		grace = time.Duration(contract.OverdueGraceDays) * 24 * time.Hour
	}
	at := account.OverdueSince.Add(grace)
	return &at
}
