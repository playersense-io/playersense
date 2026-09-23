package suspension

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/valkey"
)

// Job suspends accounts past their bounds, and publishes which accounts are suspended.
type Job struct {
	DB    *pgdb.DB
	Cache *valkey.Client
	Log   *slog.Logger

	// Grace is how long an account may owe before it is suspended.
	Grace time.Duration

	// Policy is the credit limit's floor and share of spend (limit.go).
	Policy Policy

	// OverdueGrace is how long an invoice may be overdue before ingestion is suspended, for an
	// account whose contract does not say (ADR 0045).
	OverdueGrace time.Duration

	Now func() time.Time

	mu     sync.Mutex
	marked map[uint32]bool // what this process marked last pass, to unmark promptly
}

// Result is one pass.
type Result struct {
	Suspended   int // newly suspended this pass
	Reactivated int // invoiced accounts let back in this pass
	Marked      int // suspended accounts published to Valkey
}

// Run makes one pass: suspend, then publish.
func (j *Job) Run(ctx context.Context) (Result, error) {
	var out Result
	suspended, err := j.suspend(ctx)
	out.Suspended = suspended
	invoiced, reactivated, invoicedErr := j.invoiced(ctx)
	out.Suspended += invoiced
	out.Reactivated = reactivated
	marked, markErr := j.publish(ctx)
	out.Marked = marked
	return out, errors.Join(err, invoicedErr, markErr)
}

// suspend moves accounts in their grace period past either bound to suspended.
//
// Debt includes usage queued and not yet on the ledger (ADR 0044): charges settle every few minutes,
// and an account must not slip past its limit in between.
func (j *Job) suspend(ctx context.Context) (int, error) {
	candidates, err := j.DB.Client.BillingAccount.Query().
		Where(billingaccount.StateEQ(billingaccount.StateGracePeriod), billingaccount.ArrearsMicrosGT(0)).
		WithPlan().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("read accounts in grace: %w", err)
	}
	now := j.now()
	done := 0
	for _, account := range candidates {
		reason, due, err := j.due(ctx, account, now)
		if err != nil {
			return done, err
		}
		if !due {
			continue
		}
		// Under the row lock, and only if it still owes and is still in grace: a top-up that
		// landed between the read and here has already made it active.
		n, err := j.DB.Client.BillingAccount.Update().
			Where(
				billingaccount.ID(account.ID),
				billingaccount.StateEQ(billingaccount.StateGracePeriod),
				billingaccount.ArrearsMicrosGT(0),
			).
			SetState(billingaccount.StateSuspended).
			SetStateChangedAt(now).
			Save(ctx)
		if err != nil {
			return done, fmt.Errorf("suspend account %d: %w", account.ID, err)
		}
		if n == 1 {
			done++
			j.log().Warn("suspended ingestion for an account", "account_id", account.ID,
				"org", account.OrganizationID, "arrears", billing.USD(account.ArrearsMicros), "reason", reason)
		}
	}
	return done, nil
}

// due reports whether an account in grace has passed either bound, and which.
func (j *Job) due(ctx context.Context, account *ent.BillingAccount, now time.Time) (string, bool, error) {
	limit, source, err := Limit(ctx, j.DB.Client, account, j.Policy, now)
	if err != nil {
		return "", false, err
	}
	owed, err := Owed(ctx, j.DB.Client, account)
	if err != nil {
		return "", false, err
	}
	if owed >= limit {
		return fmt.Sprintf("owes %s, at its %s credit limit (%s)",
			billing.USD(owed), billing.USD(limit), source), true, nil
	}
	if !account.StateChangedAt.IsZero() && now.Sub(account.StateChangedAt) >= j.Grace {
		return fmt.Sprintf("has owed for longer than the %s grace period", j.Grace), true, nil
	}
	return "", false, nil
}

// Owed is what an account owes now: its arrears, and the part of its queued usage its credit will not
// cover — max(0, pending − balance), where the balance is already credit minus arrears.
func Owed(ctx context.Context, client *ent.Client, account *ent.BillingAccount) (int64, error) {
	pending, err := Pending(ctx, client, account.ID)
	if err != nil {
		return 0, err
	}
	return max(0, pending-account.BalanceMicros), nil
}

// Pending is an account's usage queued and not yet written to its ledger.
func Pending(ctx context.Context, client *ent.Client, accountID int) (int64, error) {
	var sums []struct {
		Sum int64 `json:"sum"`
	}
	if err := client.UsageCharge.Query().
		Where(usagecharge.AccountID(accountID), usagecharge.SettledAtIsNil()).
		Aggregate(ent.Sum(usagecharge.FieldAmountMicros)).
		Scan(ctx, &sums); err != nil {
		return 0, fmt.Errorf("sum pending usage: %w", err)
	}
	if len(sums) == 0 {
		return 0, nil
	}
	return sums[0].Sum, nil
}

// SuspendsAt is when an account in its grace period will be suspended if it does not pay, by time.
func SuspendsAt(account *ent.BillingAccount, grace time.Duration) *time.Time {
	if account.State != billingaccount.StateGracePeriod || account.StateChangedAt.IsZero() {
		return nil
	}
	at := account.StateChangedAt.Add(grace)
	return &at
}

// publish marks every suspended organization in Valkey, and unmarks every organization that paid —
// so a studio is let back in at once rather than when its mark lapses.
func (j *Job) publish(ctx context.Context) (int, error) {
	if j.Cache == nil {
		return 0, nil
	}
	var orgs []int
	if err := j.DB.Client.BillingAccount.Query().
		Where(billingaccount.StateEQ(billingaccount.StateSuspended)).
		Select(billingaccount.FieldOrganizationID).
		Scan(ctx, &orgs); err != nil {
		return 0, fmt.Errorf("read suspended accounts: %w", err)
	}
	now := make(map[uint32]bool, len(orgs))
	keys := make([]string, 0, len(orgs))
	for _, org := range orgs {
		now[uint32(org)] = true
		keys = append(keys, Key(j.Cache, uint32(org)))
	}
	if err := j.Cache.Mark(ctx, keys, MarkTTL); err != nil {
		return 0, fmt.Errorf("mark suspended organizations: %w", err)
	}

	// Unmark accounts that paid, decided from the database rather than from this process's memory:
	// any account that left `suspended` recently. A mark set by another billing writer, or by this
	// one before it restarted, is still found — memory alone left a paying studio refused until the
	// mark lapsed. The window is longer than a mark lives, so nothing it misses is still marked.
	var reactivated []int
	if err := j.DB.Client.BillingAccount.Query().
		Where(
			billingaccount.StateNEQ(billingaccount.StateSuspended),
			billingaccount.StateChangedAtGTE(j.now().Add(-5*MarkTTL)),
		).
		Select(billingaccount.FieldOrganizationID).
		Scan(ctx, &reactivated); err != nil {
		return len(keys), fmt.Errorf("read recently reactivated accounts: %w", err)
	}

	j.mu.Lock()
	var released []string
	for _, org := range reactivated {
		if !now[uint32(org)] {
			released = append(released, Key(j.Cache, uint32(org)))
		}
	}
	for org := range j.marked {
		if !now[org] {
			released = append(released, Key(j.Cache, org))
		}
	}
	j.marked = now
	j.mu.Unlock()
	if len(released) > 0 {
		if err := j.Cache.Del(ctx, released...); err != nil {
			return len(keys), fmt.Errorf("unmark organizations that paid: %w", err)
		}
	}
	return len(keys), nil
}

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func (j *Job) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}
