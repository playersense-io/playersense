// Package invoicing closes an invoiced account's month and issues its invoice (ADR 0045).
//
// # An invoice is a stretch of the ledger
//
// The close does not recompute anything. Usage credit could not cover was already charged to the
// account's receivable as the players arrived (wallet.ChargeUsage); the close adds the contract's
// charges — the platform fee, and the commitment's top-up — and writes an invoice for every
// receivable accrual for its period or before that no invoice has billed yet. So an invoice cannot
// disagree with the ledger, a charge that settled after its month was invoiced is on the next invoice
// rather than on neither, and next month's first players are left for next month's.
//
// # Then Stripe is told, as a top-up's card is charged
//
// The invoice row is the claim. It is committed with its key before Stripe is asked, every Stripe step
// is keyed off it, and a response lost in flight is resumed as the same invoice (payments.IssueInvoice).
package invoicing

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/invoiceline"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/wallet"
)

// ErrNotInvoiced is a close asked of an account that has no invoicing period open.
var ErrNotInvoiced = fmt.Errorf("the account is not being invoiced")

// Close writes the invoice for an account's period ending at `end`, inside the caller's transaction.
//
// The period starts where the last invoice ended (`invoiced_through`). A month close passes the first
// of the next month; the route that moves an account back to prepaid passes now, with final set. Nothing
// to close — `end` not after the start — is nil and no error.
//
// The account is locked first, and every write here happens under that lock: the fee, the commitment,
// the invoice and the account's new starting point commit together or not at all, so a close that
// crashes half way is simply run again.
func Close(ctx context.Context, tx *ent.Tx, accountID int, end time.Time, final bool) (*ent.Invoice, error) {
	account, err := tx.BillingAccount.Query().Where(billingaccount.ID(accountID)).ForUpdate().Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock the account: %w", err)
	}
	if account.InvoicedThrough == nil {
		return nil, ErrNotInvoiced
	}
	start := account.InvoicedThrough.UTC()
	end = end.UTC()
	if !end.After(start) {
		return nil, nil
	}
	month := MonthOf(start)

	contract, err := tx.BillingContract.Query().
		Where(billingcontract.AccountID(account.ID), billingcontract.EffectiveFromLT(end),
			billingcontract.CollectionEQ(billingcontract.CollectionInvoiced)).
		Order(ent.Desc(billingcontract.FieldEffectiveFrom), ent.Desc(billingcontract.FieldID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("find the contract for %s: %w", month.Format("January 2006"), err)
	}

	// The platform fee, prorated when the period is less than its month.
	key := account.UID.String() + ":" + start.Format(time.RFC3339)
	if fee := prorate(contract.PlatformFeeMicros, start, end); fee > 0 {
		if _, _, err := wallet.Accrue(ctx, tx, wallet.AccrueRequest{
			AccountID: account.ID, Kind: "subscription", Amount: fee, PeriodStart: month,
			Description: "Platform fee, " + describePeriod(start, end), Key: "fee:" + key,
		}); err != nil {
			return nil, err
		}
	}

	// What this invoice bills: every accrual for this period or before that no invoice has billed.
	//
	// The last invoice billed what was on the ledger when it closed (up to `from`) for months before
	// it ended (before `start`). So what is left is anything written since, and anything written
	// before but for a month from `start` on — June's first players, settled in the two hours before
	// May closed, which May's invoice rightly left for June's. Entries for a month after this one
	// are left in turn. An entry with no month is billed as soon as it is written.
	from := account.InvoicedLedgerID
	entries, err := tx.LedgerEntry.Query().
		Where(ledgerentry.AccountID(account.ID), ledgerentry.InvoiceIDIsNil(),
			ledgerentry.Or(
				ledgerentry.And(ledgerentry.PeriodStartIsNil(), ledgerentry.IDGT(from)),
				ledgerentry.And(ledgerentry.PeriodStartLT(end),
					ledgerentry.Or(ledgerentry.IDGT(from), ledgerentry.PeriodStartGTE(start))),
			)).
		Order(ent.Asc(ledgerentry.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the ledger since the last invoice: %w", err)
	}
	bill := billOf(entries, month)

	// The commitment: what the minimum exceeds this month's usage by. Straight to the receivable,
	// like the fee — credit is a discount on usage, not on the contract.
	if short := prorate(contract.MinimumCommitmentMicros, start, end) - bill.usage[month]; short > 0 {
		entry, _, err := wallet.Accrue(ctx, tx, wallet.AccrueRequest{
			AccountID: account.ID, Kind: "commitment", Amount: short, PeriodStart: month,
			Description: "Minimum commitment, " + describePeriod(start, end), Key: "commitment:" + key,
		})
		if err != nil {
			return nil, err
		}
		bill.add(entry, month)
	}

	carryIn, err := carried(ctx, tx, account.ID)
	if err != nil {
		return nil, err
	}
	discrepancy := int64(0)
	if !final {
		if discrepancy, err = reconcile(ctx, tx, account.ID, month); err != nil {
			return nil, err
		}
	}

	total := bill.receivable + carryIn
	cents := int64(0)
	if total > 0 {
		cents = total / billing.CentMicros
	}
	lines := bill.lines(month, start, end, cents)

	// The ledger as it stands, under the lock: everything to here is either billed now or left for a
	// later month by the rule above.
	cursor, err := tx.LedgerEntry.Query().
		Where(ledgerentry.AccountID(account.ID)).
		Order(ent.Desc(ledgerentry.FieldID)).
		FirstID(ctx)
	switch {
	case ent.IsNotFound(err):
		cursor = from
	case err != nil:
		return nil, fmt.Errorf("read the end of the ledger: %w", err)
	}
	uid := uuid.New()
	status := invoice.StatusDraft
	if cents == 0 {
		status = invoice.StatusEmpty // nothing to bill, so nothing is sent
	}
	row, err := tx.Invoice.Create().
		SetUID(uid).
		SetAccountID(account.ID).
		SetContractID(contract.ID).
		SetPeriodStart(start).
		SetPeriodEnd(end).
		SetLedgerFromID(account.InvoicedLedgerID).
		SetLedgerToID(cursor).
		SetCurrency(account.Currency).
		SetTotalMicros(cents * billing.CentMicros).
		SetCarryInMicros(carryIn).
		SetCarryOutMicros(total - cents*billing.CentMicros).
		SetDiscrepancyMicros(discrepancy).
		SetTermsDays(contract.TermsDays).
		SetPoNumber(contract.PoNumber).
		SetMemo(contract.Memo).
		SetFinal(final).
		SetStatus(status).
		SetAmountRemainingMicros(cents * billing.CentMicros).
		SetIssueKey("playersense-invoice-" + uid.String()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("write the invoice: %w", err)
	}
	creates := make([]*ent.InvoiceLineCreate, len(lines))
	for i, line := range lines {
		create := tx.InvoiceLine.Create().
			SetInvoiceID(row.ID).SetPosition(i).SetKind(line.kind).SetDescription(line.description).
			SetAmountMicros(line.micros).SetAmountCents(line.cents)
		if line.period != nil {
			create = create.SetPeriodStart(*line.period)
		}
		creates[i] = create
	}
	if _, err := tx.InvoiceLine.CreateBulk(creates...).Save(ctx); err != nil {
		return nil, fmt.Errorf("write the invoice's lines: %w", err)
	}

	if err := tx.BillingAccount.UpdateOneID(account.ID).
		SetInvoicedThrough(end).
		SetInvoicedLedgerID(cursor).
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("move the account's next invoice on: %w", err)
	}
	return row, nil
}

// bill is what a stretch of the ledger comes to.
type bill struct {
	receivable int64               // every accrual: what the invoice bills before the carry
	usage      map[time.Time]int64 // gross usage by month, credit-paid part included
	usageOwed  int64               // the part of that usage credit did not cover
	fee        int64
	commitment int64
	other      int64 // any other accrual: arrears carried in at the switch
}

func billOf(entries []*ent.LedgerEntry, month time.Time) *bill {
	b := &bill{usage: map[time.Time]int64{}}
	for _, entry := range entries {
		b.add(entry, month)
	}
	return b
}

func (b *bill) add(entry *ent.LedgerEntry, month time.Time) {
	switch entry.Kind {
	case ledgerentry.KindUsage:
		// A usage entry with no month predates ADR 0045; it is billed as this month's.
		period := month
		if entry.PeriodStart != nil {
			period = MonthOf(*entry.PeriodStart)
		}
		b.usage[period] += -entry.AmountMicros // charges are negative, volume credits positive
		b.usageOwed += entry.ReceivableMicros
	case ledgerentry.KindSubscription:
		b.fee += entry.ReceivableMicros
	case ledgerentry.KindCommitment:
		b.commitment += entry.ReceivableMicros
	default:
		b.other += entry.ReceivableMicros
	}
	b.receivable += entry.ReceivableMicros
}

// line is one invoice line before it is written.
type line struct {
	kind        invoiceline.Kind
	description string
	micros      int64
	cents       int64
	period      *time.Time
}

// lines are what a customer reads: the fee, each month's players, what credit paid for, the
// commitment, anything else. Each is its exact amount in micros and in cents; the cents are rounded
// toward zero and the difference — never a whole cent per line — goes to the largest line, so the
// lines add up to exactly what the invoice bills.
func (b *bill) lines(month, start, end time.Time, cents int64) []line {
	var out []line
	push := func(kind invoiceline.Kind, description string, micros int64, period *time.Time) {
		if micros != 0 {
			out = append(out, line{kind: kind, description: description, micros: micros,
				cents: micros / billing.CentMicros, period: period})
		}
	}
	push(invoiceline.KindPlatformFee, "Platform fee, "+describePeriod(start, end), b.fee, &month)
	months := make([]time.Time, 0, len(b.usage))
	for m := range b.usage {
		months = append(months, m)
	}
	slices.SortFunc(months, func(a, b time.Time) int { return a.Compare(b) })
	var gross int64
	for _, m := range months {
		period := m
		gross += b.usage[m]
		if m.Equal(month) {
			push(invoiceline.KindUsage, "Tracked players, "+describePeriod(start, end), b.usage[m], &period)
		} else {
			push(invoiceline.KindPriorUsage, "Tracked players, "+m.Format("January 2006")+
				" (settled after it was invoiced)", b.usage[m], &period)
		}
	}
	push(invoiceline.KindCreditsApplied, "Credits applied", -(gross - b.usageOwed), nil)
	push(invoiceline.KindCommitment, "Minimum commitment, "+describePeriod(start, end), b.commitment, &month)
	push(invoiceline.KindAdjustment, "Adjustments", b.other, nil)

	var sum int64
	largest := -1
	for i, l := range out {
		sum += l.cents
		if l.cents > 0 && (largest < 0 || l.cents > out[largest].cents) {
			largest = i
		}
	}
	if diff := cents - sum; diff != 0 {
		if largest >= 0 {
			out[largest].cents += diff
		} else {
			out = append(out, line{kind: invoiceline.KindAdjustment, description: "Rounding", cents: diff})
		}
	}
	return out
}

// carried is what the account's last invoice left below a cent, for this one to bill.
func carried(ctx context.Context, tx *ent.Tx, accountID int) (int64, error) {
	last, err := tx.Invoice.Query().
		Where(invoice.AccountID(accountID)).
		Order(ent.Desc(invoice.FieldPeriodStart)).
		First(ctx)
	switch {
	case ent.IsNotFound(err):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("read the last invoice: %w", err)
	}
	return last.CarryOutMicros, nil
}

// reconcile is how far the ledger's usage for a month is from what the dashboard said it cost.
//
// Both are the drain's numbers — monthly_usage.charged_micros as players are counted, the ledger as
// they settle — so once the month's queue is empty they agree. A difference is recorded on the invoice
// rather than refused: the ledger is what is owed, and staff need to see where it disagrees.
func reconcile(ctx context.Context, tx *ent.Tx, accountID int, month time.Time) (int64, error) {
	var sums []struct {
		Sum int64 `json:"sum"`
	}
	if err := tx.LedgerEntry.Query().
		Where(ledgerentry.AccountID(accountID), ledgerentry.KindEQ(ledgerentry.KindUsage),
			ledgerentry.PeriodStart(month)).
		Aggregate(ent.Sum(ledgerentry.FieldAmountMicros)).
		Scan(ctx, &sums); err != nil {
		return 0, fmt.Errorf("sum the month's usage: %w", err)
	}
	var ledger int64
	if len(sums) == 1 {
		ledger = -sums[0].Sum
	}
	var dashboard int64
	usage, err := tx.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(accountID), monthlyusage.PeriodStart(month)).Only(ctx)
	switch {
	case err == nil:
		dashboard = usage.ChargedMicros
	case !ent.IsNotFound(err):
		return 0, fmt.Errorf("read the month's usage: %w", err)
	}
	return ledger - dashboard, nil
}

// prorate is a monthly amount for the part of its month a period covers.
func prorate(monthly int64, start, end time.Time) int64 {
	if monthly <= 0 {
		return 0
	}
	from, to := MonthOf(start), MonthOf(start).AddDate(0, 1, 0)
	if !start.After(from) && !end.Before(to) {
		return monthly
	}
	if end.After(to) {
		end = to
	}
	covered := int64(end.Sub(start) / time.Second)
	return monthly * covered / int64(to.Sub(from)/time.Second)
}

// describePeriod is "May 2026", or "12–31 May 2026" for part of one.
func describePeriod(start, end time.Time) string {
	from, to := MonthOf(start), MonthOf(start).AddDate(0, 1, 0)
	if !start.After(from) && !end.Before(to) {
		return from.Format("January 2006")
	}
	last := end.Add(-time.Nanosecond)
	return fmt.Sprintf("%d–%d %s", start.Day(), last.Day(), from.Format("January 2006"))
}

// MonthOf is the first instant of a moment's calendar month, in UTC — the same month the charger
// keys usage on.
func MonthOf(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
}
