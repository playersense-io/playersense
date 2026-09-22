package payments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/wallet"
)

var sqlNoRows = sql.ErrNoRows

// AutoTopUp charges saved cards when balances run low.
//
// # One charge per claim, whatever fails
//
// A pass finds accounts below their threshold and, for each, **claims** it: a fresh key is written
// to `auto_topup_pending_key` and committed. Only then is Stripe asked to charge, with that key as
// the Idempotency-Key. What happens next decides what becomes of the claim:
//
//	succeeded                      credited and released, in one transaction
//	declined / needs the customer  released, with a time before which it is not tried again
//	processing                     kept; the webhook finishes it
//	no answer (timeout, crash)     kept; after StaleAfter, retried with the SAME key
//
// The last row is the one that matters. A request whose response was lost may or may not have
// charged the card, and nothing on this side can tell which. Retrying with the same key makes it not
// matter: Stripe returns the payment it already made instead of making another. And an account with
// a claim outstanding is never claimed again, so no second key exists to charge twice with.
//
// Stripe remembers a key for at least a day. A claim older than that is first looked for among the
// customer's payments, so even a claim left behind by a long outage is resolved rather than repeated.
type AutoTopUp struct {
	DB      *pgdb.DB
	Gateway Gateway
	Log     *slog.Logger

	// StaleAfter is how long a claim waits for an answer before it is retried with its key.
	StaleAfter time.Duration

	// RetryAfter is how long an account waits after a decline or a missing card.
	RetryAfter time.Duration

	// MaxAccounts bounds one pass.
	MaxAccounts int

	Now func() time.Time

	Stats AutoTopUpStats
}

// AutoTopUpStats is what automatic top-up has done.
type AutoTopUpStats struct {
	Attempts atomic.Int64
	Credited atomic.Int64
	Declined atomic.Int64
	Unknown  atomic.Int64 // no answer; the claim is kept and retried with its key
	Limited  atomic.Int64 // not charged: the daily limit was reached
}

// Day is the daily limit's window: any 24 hours, not a calendar day, so three charges at 23:59 and
// three more at 00:01 is not something the limit allows.
const Day = 24 * time.Hour

// keyLifetime is how long Stripe is relied on to remember an Idempotency-Key. Its documented
// minimum is twenty-four hours; this leaves margin.
const keyLifetime = 20 * time.Hour

// Run works the queue: accounts whose balance movement asked for a top-up (ADR 0041).
//
// Read through a partial index that holds only asking accounts, oldest ask first, so it is cheap
// enough to run every couple of seconds at any number of accounts — which is what turns "a charge
// left the balance low" into "the card was charged" in seconds rather than at the next sweep.
func (a *AutoTopUp) Run(ctx context.Context) (int, error) {
	return a.pass(ctx, true)
}

// Sweep is the safety net: every account below its threshold, asked for or not.
//
// The queue is written in the same transaction as the balance, so nothing should reach here that the
// queue missed — but a threshold raised directly in the database, or an account restored from a
// backup, would. It is a full scan of billing accounts and runs every few minutes, not seconds.
func (a *AutoTopUp) Sweep(ctx context.Context) (int, error) {
	return a.pass(ctx, false)
}

func (a *AutoTopUp) pass(ctx context.Context, queued bool) (int, error) {
	now := a.now()
	limit := a.MaxAccounts
	if limit <= 0 {
		limit = 100
	}

	query := a.DB.Client.BillingAccount.Query().
		Where(
			billingaccount.Or(
				billingaccount.AutoTopupRetryAfterIsNil(),
				billingaccount.AutoTopupRetryAfterLTE(now),
			),
			billingaccount.Or(
				billingaccount.AutoTopupPendingKeyIsNil(),
				billingaccount.AutoTopupPendingAtLTE(now.Add(-a.staleAfter())),
			),
		)
	if queued {
		query = query.
			Where(billingaccount.AutoTopupRequestedAtNotNil()).
			Order(ent.Asc(billingaccount.FieldAutoTopupRequestedAt))
	} else {
		// Below threshold, able to be charged. The balance is the ledger's running total,
		// which already nets arrears.
		query = query.Where(
			billingaccount.AutoTopupEnabled(true),
			billingaccount.StripeCustomerIDNotNil(),
			billingaccount.StateNEQ(billingaccount.StateCanceled),
			func(s *entsql.Selector) {
				s.Where(entsql.ColumnsLT(s.C(billingaccount.FieldBalanceMicros),
					s.C(billingaccount.FieldAutoTopupThresholdMicros)))
			},
		)
	}
	candidates, err := query.Limit(limit).IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("find accounts to top up: %w", err)
	}

	var (
		done int
		errs []error
	)
	for _, id := range candidates {
		if err := a.topUp(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("account %d: %w", id, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// claim is an account's outstanding automatic top-up.
type claim struct {
	account *ent.BillingAccount
	key     string
	since   time.Time
	resumed bool // an earlier claim whose answer never came
	amount  int64
}

// topUp takes one account from claim to outcome.
func (a *AutoTopUp) topUp(ctx context.Context, accountID int) error {
	c, err := a.claim(ctx, accountID)
	if err != nil || c == nil {
		return err
	}
	a.Stats.Attempts.Add(1)
	customer := *c.account.StripeCustomerID

	// A claim unanswered for longer than Stripe keeps keys is looked for before anything is
	// sent: the charge may have happened, and a new request with a forgotten key would charge
	// again.
	if c.resumed && a.now().Sub(c.since) > keyLifetime {
		found, err := a.Gateway.FindAutoTopUp(ctx, customer, c.key, c.since.Add(-time.Hour))
		if err != nil {
			a.Stats.Unknown.Add(1)
			return err
		}
		if found != nil {
			return a.settle(ctx, c, *found)
		}
	}

	card, err := a.Gateway.ChargeableCard(ctx, customer)
	if err != nil {
		a.Stats.Unknown.Add(1)
		return err // nothing was sent; the claim goes stale and is retried
	}
	if card == "" {
		return a.release(ctx, c, "there is no saved card to charge; add one with a top-up or in the billing portal")
	}

	payment, err := a.Gateway.ChargeOffSession(ctx, OffSessionRequest{
		AccountUID:    c.account.UID.String(),
		Customer:      customer,
		PaymentMethod: card,
		AmountCents:   c.amount / billing.CentMicros,
		Currency:      c.account.Currency,
		Key:           c.key,
	})
	if errors.Is(err, ErrKeyReused) {
		// The key was already used with different parameters — the customer changed cards
		// between the first attempt and this one. Whatever that first attempt did, it did
		// under this key, so it is found rather than repeated.
		found, findErr := a.Gateway.FindAutoTopUp(ctx, customer, c.key, c.since.Add(-time.Hour))
		if findErr != nil {
			a.Stats.Unknown.Add(1)
			return findErr
		}
		if found != nil {
			return a.settle(ctx, c, *found)
		}
	}
	if err != nil {
		// No answer. The claim stays, and the retry reuses its key.
		a.Stats.Unknown.Add(1)
		a.log().Warn("an automatic top-up got no answer; it will be retried with the same key",
			"account_id", c.account.ID, "key", c.key, "error", err)
		return err
	}
	return a.settle(ctx, c, payment)
}

// claim writes a key to an account that still needs a top-up, or picks up the key it already has.
func (a *AutoTopUp) claim(ctx context.Context, accountID int) (*claim, error) {
	tx, err := a.DB.Client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// SKIP LOCKED: an account being drained or topped up by somebody else is left to them this
	// pass. Its balance is moving and the check below would be reading it mid-flight.
	account, err := tx.BillingAccount.Query().
		Where(billingaccount.ID(accountID)).
		ForUpdate(entsql.WithLockAction(entsql.SkipLocked)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := a.now()
	wants := wallet.WantsTopUp(account, account.BalanceMicros)

	// An ask that no longer holds — the balance recovered, the setting was switched off — leaves
	// the queue in whatever write this claim makes, under the lock, so the queue never keeps a row
	// it has no reason to.
	update := tx.BillingAccount.UpdateOneID(account.ID)
	dequeue := !wants && account.AutoTopupRequestedAt != nil
	if dequeue {
		update = update.ClearAutoTopupRequestedAt()
	}

	// An outstanding claim is resolved whether or not a top-up is still wanted: a charge may
	// already have happened under it, and only its own key can find out.
	if account.AutoTopupPendingKey != nil {
		if account.AutoTopupPendingAt != nil && now.Sub(*account.AutoTopupPendingAt) < a.staleAfter() {
			return nil, nil // answered by somebody else while this pass was starting
		}
		// Resumed with its own key and its own amount — a different amount under the same key
		// is a request Stripe refuses, and rightly.
		c := &claim{account: account, key: *account.AutoTopupPendingKey, resumed: true}
		if account.AutoTopupPendingAt != nil {
			c.since = *account.AutoTopupPendingAt
		}
		if account.AutoTopupPendingAmountMicros != nil {
			c.amount = *account.AutoTopupPendingAmountMicros
		} else {
			c.amount, _ = Amount(account)
		}
		if err := update.SetAutoTopupPendingAt(now).Exec(ctx); err != nil {
			return nil, err
		}
		return c, tx.Commit()
	}

	amount, needed := Amount(account)
	if !wants || !needed {
		if dequeue {
			if err := update.Exec(ctx); err != nil {
				return nil, err
			}
			return nil, tx.Commit()
		}
		return nil, nil
	}

	// The daily limit, counted under the lock so two workers cannot both take the last slot.
	windowStart, count := now, 0
	if account.AutoTopupWindowStartedAt != nil && now.Sub(*account.AutoTopupWindowStartedAt) < Day {
		windowStart, count = *account.AutoTopupWindowStartedAt, account.AutoTopupWindowCount
	}
	if count >= account.AutoTopupDailyLimit {
		// Not charged, and not asked about again until the window reopens. The request stays
		// queued, so the top-up happens then if the balance is still low.
		reopens := windowStart.Add(Day)
		if err := update.
			SetAutoTopupRetryAfter(reopens).
			SetAutoTopupLastError(fmt.Sprintf(
				"the limit of %d automatic top-ups in 24 hours was reached; the next can be made after %s",
				account.AutoTopupDailyLimit, reopens.Format(time.RFC3339))).
			Exec(ctx); err != nil {
			return nil, err
		}
		a.Stats.Limited.Add(1)
		return nil, tx.Commit()
	}

	key := "playersense-autotopup-" + account.UID.String() + "-" + uuid.NewString()
	if err := update.
		SetAutoTopupWindowStartedAt(windowStart).
		SetAutoTopupWindowCount(count + 1).
		SetAutoTopupPendingKey(key).
		SetAutoTopupPendingAt(now).
		SetAutoTopupPendingAmountMicros(amount).
		Exec(ctx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &claim{account: account, key: key, since: now, amount: amount}, nil
}

// settle acts on an answer.
func (a *AutoTopUp) settle(ctx context.Context, c *claim, p Payment) error {
	tx, err := a.DB.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	switch {
	case p.Succeeded():
		if _, err := Credit(ctx, tx, p, a.now()); err != nil {
			return err
		}
		a.Stats.Credited.Add(1)
	case p.Status == "processing":
		return nil // the webhook finishes it; the claim stays
	default:
		reason := p.FailureMessage
		if reason == "" {
			reason = "the automatic top-up was " + p.Status
		}
		if p.Metadata == nil {
			p.Metadata = map[string]string{}
		}
		p.Metadata[MetaKey] = c.key
		p.Metadata[MetaAccount] = c.account.UID.String()
		p.Customer = *c.account.StripeCustomerID
		if err := Release(ctx, tx, p, a.now().Add(a.retryAfter()), reason); err != nil {
			return err
		}
		a.Stats.Declined.Add(1)
	}
	return tx.Commit()
}

// release ends a claim that was never sent.
func (a *AutoTopUp) release(ctx context.Context, c *claim, reason string) error {
	return a.DB.Client.BillingAccount.Update().
		Where(billingaccount.ID(c.account.ID), billingaccount.AutoTopupPendingKey(c.key)).
		ClearAutoTopupPendingKey().ClearAutoTopupPendingAt().ClearAutoTopupPendingAmountMicros().
		SetAutoTopupRetryAfter(a.now().Add(a.retryAfter())).
		SetAutoTopupLastError(reason).
		Exec(ctx)
}

// Amount is what an automatic top-up charges, and whether one is needed at all.
//
// The configured amount, or the smallest multiple of it that brings the balance back above the
// threshold — an account that owes $200 with "$50 when below $10" is charged $250 once, not $50 five
// times in five passes. Capped at billing.MaxTopUp, because an automatic charge is one nobody is
// watching; an account whose debt exceeds that is topped up by the cap and stays in its grace period.
func Amount(account *ent.BillingAccount) (int64, bool) {
	step := account.AutoTopupAmountMicros
	if step <= 0 || account.BalanceMicros >= account.AutoTopupThresholdMicros {
		return 0, false
	}
	short := account.AutoTopupThresholdMicros - account.BalanceMicros
	steps := short/step + 1 // strictly above the threshold, so the next pass does not charge again
	amount := min(steps*step, billing.MaxTopUp)
	amount -= amount % billing.CentMicros
	return amount, amount > 0
}

func (a *AutoTopUp) staleAfter() time.Duration {
	if a.StaleAfter > 0 {
		return a.StaleAfter
	}
	return 10 * time.Minute
}

func (a *AutoTopUp) retryAfter() time.Duration {
	if a.RetryAfter > 0 {
		return a.RetryAfter
	}
	return 6 * time.Hour
}

func (a *AutoTopUp) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *AutoTopUp) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}
