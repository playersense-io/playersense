package charging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/wallet"
)

// Drainer moves queued usage charges into the wallet.
type Drainer struct {
	DB  *pgdb.DB
	Log *slog.Logger

	// MaxAccounts bounds one pass. Whatever is left is the next pass's, a few seconds later,
	// so a backlog is worked through at a steady rate rather than in one transaction storm.
	MaxAccounts int

	// When an account's queue is settled (ADR 0044). The drain looks every few seconds and
	// settles an account only when it matters:
	//
	//	its open charge is SettleAfter old          — at most one ledger line per SettleAfter
	//	its pending charges reach SettleCap         — no statement line holds more than this
	//	pending would take the balance below zero   — debt appears on the ledger at once
	//	pending would cross its auto top-up threshold — the card is charged within seconds
	//
	// The last two only on the crossing, not while already below: an account already in debt
	// or already waiting on a top-up settles on the clock like any other. Zero SettleAfter
	// settles everything, every pass.
	SettleAfter time.Duration
	SettleCap   int64

	Now func() time.Time

	Stats DrainStats
}

// DrainStats is what the drain has done.
type DrainStats struct {
	Passes   atomic.Int64
	Accounts atomic.Int64
	Charges  atomic.Int64
	Micros   atomic.Int64 // net moved out of wallets, credits subtracted
	Owed     atomic.Int64 // micros that became arrears
	Failures atomic.Int64
}

// DrainResult is one pass.
type DrainResult struct {
	Accounts int
	Charges  int
}

// Run drains up to MaxAccounts accounts' queues.
//
// A failure on one account is logged and counted and the pass moves on: its charges stay queued,
// untouched, and the next pass tries again. The error returned joins every such failure, for the
// caller's log; it never means a charge was dropped.
func (d *Drainer) Run(ctx context.Context) (DrainResult, error) {
	d.Stats.Passes.Add(1)
	limit := d.MaxAccounts
	if limit <= 0 {
		limit = 500
	}

	// Accounts whose queue is due, off the partial index over unsettled rows — which, with each
	// delivery extending an account's open row, is about one row per account however large
	// usage_charges grows. Oldest first, so a backlog is worked in order.
	cap := d.SettleCap
	if cap <= 0 {
		cap = 1_000_000_000 // $1,000
	}
	rows, err := d.DB.Client.QueryContext(ctx, `
		SELECT c.account_id
		FROM usage_charges c
		JOIN billing_accounts a ON a.id = c.account_id
		WHERE c.settled_at IS NULL
		GROUP BY c.account_id, a.balance_micros, a.auto_topup_enabled, a.auto_topup_threshold_micros
		HAVING min(c.created_at) <= $1
		    OR sum(c.amount_micros) >= $2
		    OR (a.balance_micros >= 0 AND a.balance_micros - sum(c.amount_micros) < 0)
		    OR (a.auto_topup_enabled
		        AND a.balance_micros >= a.auto_topup_threshold_micros
		        AND a.balance_micros - sum(c.amount_micros) < a.auto_topup_threshold_micros)
		ORDER BY min(c.created_at)
		LIMIT $3`,
		d.now().Add(-d.SettleAfter), cap, limit)
	if err != nil {
		return DrainResult{}, fmt.Errorf("find queued charges: %w", err)
	}
	var accounts []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return DrainResult{}, err
		}
		accounts = append(accounts, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return DrainResult{}, err
	}

	var (
		out  DrainResult
		errs []error
	)
	for _, id := range accounts {
		settled, err := d.drainAccount(ctx, id)
		if err != nil {
			d.Stats.Failures.Add(1)
			d.log().Error("an account's charges could not be drained; they stay queued",
				"account_id", id, "error", err)
			errs = append(errs, fmt.Errorf("account %d: %w", id, err))
			continue
		}
		if settled > 0 {
			out.Accounts++
			out.Charges += settled
		}
	}
	d.Stats.Accounts.Add(int64(out.Accounts))
	d.Stats.Charges.Add(int64(out.Charges))
	return out, errors.Join(errs...)
}

// drainAccount settles one account's queue in one transaction.
//
// Spending and marking commit together. That is what makes a crash harmless: there is no state in
// which the wallet moved and the rows still look unpaid, so the next pass can never pay them again —
// and no state in which the rows look paid and the wallet did not move.
func (d *Drainer) drainAccount(ctx context.Context, accountID int) (int, error) {
	tx, err := d.DB.Client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	settled, err := d.settle(ctx, tx, accountID)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if settled == 0 {
		return 0, tx.Rollback()
	}
	return settled, tx.Commit()
}

func (d *Drainer) settle(ctx context.Context, tx *ent.Tx, accountID int) (int, error) {
	// SKIP LOCKED: another drain already holds this account, and waiting for it would be a
	// second drain doing nothing slowly. It is left to that one.
	_, err := tx.BillingAccount.Query().
		Where(billingaccount.ID(accountID)).
		ForUpdate(entsql.WithLockAction(entsql.SkipLocked)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("lock the account: %w", err)
	}

	// FOR UPDATE: an open row can still be extended by an accrual (ADR 0044). Locked here, an
	// extension either committed first — and is read, and paid — or waits for this commit, finds the
	// row settled and opens a new one. Read unlocked, an extension landing between this read and the
	// mark below would be marked settled without being paid.
	charges, err := tx.UsageCharge.Query().
		Where(usagecharge.AccountID(accountID), usagecharge.SettledAtIsNil()).
		Order(ent.Asc(usagecharge.FieldID)).
		ForUpdate().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the queue: %w", err)
	}
	if len(charges) == 0 {
		return 0, nil
	}

	// One ledger entry per month drained, so a statement line never spans two months.
	now := d.now()
	for _, batch := range byPeriod(charges) {
		entry, err := d.pay(ctx, tx, accountID, batch, now)
		if err != nil {
			return 0, err
		}
		ids := make([]int, len(batch))
		for i, c := range batch {
			ids[i] = c.ID
		}
		update := tx.UsageCharge.Update().
			Where(usagecharge.IDIn(ids...), usagecharge.SettledAtIsNil()).
			SetSettledAt(now)
		if entry != nil {
			update = update.SetLedgerEntryID(entry.ID)
		}
		marked, err := update.Save(ctx)
		if err != nil {
			return 0, fmt.Errorf("mark the charges settled: %w", err)
		}
		if marked != len(ids) {
			// Unreachable while the account is locked. Refused rather than committed,
			// because a charge paid and not marked is a charge paid twice next pass.
			return 0, fmt.Errorf("marked %d of %d charges; another drain moved them", marked, len(ids))
		}
	}
	return len(charges), nil
}

// pay moves one month's net charge through the wallet.
func (d *Drainer) pay(ctx context.Context, tx *ent.Tx, accountID int, batch []*ent.UsageCharge, now time.Time) (*ent.LedgerEntry, error) {
	var net int64
	for _, c := range batch {
		net += c.AmountMicros
	}
	first, last := batch[0], batch[len(batch)-1]
	request := wallet.UsageRequest{
		AccountID:   accountID,
		Description: describe(batch),
		At:          now,
		// The month, so an invoice bills it as that month's even when it settles in the next
		// (ADR 0045). byPeriod made the batch one month.
		PeriodStart: first.PeriodStart,
		// Derived from the rows it settles, which are settled exactly once — see drainAccount
		// for why a crash cannot make this pass run twice over the same rows.
		Key: "usage:" + strconv.Itoa(accountID) + ":" + strconv.Itoa(first.ID) + "-" + strconv.Itoa(last.ID),
	}

	switch {
	case net > 0:
		request.Amount = net
		result, err := wallet.ChargeUsage(ctx, tx, request)
		if err != nil {
			return nil, err
		}
		d.Stats.Micros.Add(net)
		d.Stats.Owed.Add(result.Owed)
		return result.Entry, nil
	case net < 0:
		request.Amount = -net
		result, err := wallet.CreditUsage(ctx, tx, request)
		if err != nil {
			return nil, err
		}
		d.Stats.Micros.Add(net)
		return result.Entry, nil
	default:
		// A charge and a volume credit that cancel exactly. Settled with no ledger entry,
		// because a statement line reading $0.00 is noise.
		return nil, nil
	}
}

// byPeriod splits a queue into its months, oldest first, preserving id order within each.
func byPeriod(charges []*ent.UsageCharge) [][]*ent.UsageCharge {
	var (
		out   [][]*ent.UsageCharge
		index = map[time.Time]int{}
	)
	for _, c := range charges {
		key := c.PeriodStart.UTC()
		i, found := index[key]
		if !found {
			i = len(out)
			index[key] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], c)
	}
	return out
}

// describe is the statement line: which month, and which players.
func describe(batch []*ent.UsageCharge) string {
	lowest, highest := batch[0].FirstUnit, batch[0].LastUnit
	for _, c := range batch {
		lowest = min(lowest, c.FirstUnit)
		highest = max(highest, c.LastUnit)
	}
	return fmt.Sprintf("Tracked players, %s: #%s–#%s",
		batch[0].PeriodStart.UTC().Format("January 2006"), grouped(lowest), grouped(highest))
}

// grouped writes 100001 as 100,001.
func grouped(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (d *Drainer) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d *Drainer) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
