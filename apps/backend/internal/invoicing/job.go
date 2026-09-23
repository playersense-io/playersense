package invoicing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/invoiceline"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/pgdb"
)

// Job closes invoiced accounts' months and issues the invoices, from billing-writer.
//
// Two passes, in that order: close every month that is due, then issue every draft. A draft is also
// what a close that could not reach Stripe leaves behind, so the second pass is the retry too.
type Job struct {
	DB      *pgdb.DB
	Gateway payments.Gateway
	Log     *slog.Logger

	// CloseAfter is how long after a month ends it is closed: time for the last of its usage to be
	// counted and settled, and a margin before anything is sent to a customer.
	CloseAfter time.Duration

	// StaleAfter is how long an issue waits for an answer before it is retried with its key.
	StaleAfter time.Duration

	// MaxAccounts bounds one pass.
	MaxAccounts int

	Now func() time.Time
}

// Result is one pass.
type Result struct {
	Closed int // invoices written
	Issued int // invoices issued at Stripe
}

// Run makes one pass.
func (j *Job) Run(ctx context.Context) (Result, error) {
	var out Result
	closed, closeErr := j.closeDue(ctx)
	out.Closed = closed
	issued, issueErr := j.issueDrafts(ctx)
	out.Issued = issued
	return out, errors.Join(closeErr, issueErr)
}

// closeDue closes every invoiced account whose next month has ended and settled.
func (j *Job) closeDue(ctx context.Context) (int, error) {
	// The first instant whose month is due: a month that ended CloseAfter ago or more.
	due := MonthOf(j.now().Add(-j.closeAfter()))
	ids, err := j.DB.Client.BillingAccount.Query().
		Where(billingaccount.CollectionEQ(billingaccount.CollectionInvoiced),
			billingaccount.InvoicedThroughLT(due)).
		Order(ent.Asc(billingaccount.FieldInvoicedThrough)).
		Limit(j.maxAccounts()).
		IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("find invoiced accounts to close: %w", err)
	}
	var (
		done int
		errs []error
	)
	for _, id := range ids {
		n, err := j.CloseAccount(ctx, id)
		done += n
		if err != nil {
			errs = append(errs, fmt.Errorf("account %d: %w", id, err))
		}
	}
	return done, errors.Join(errs...)
}

// CloseAccount closes every month an account has due, oldest first, each in its own transaction.
//
// A month waits while any usage for it — or before it — is still queued: it would settle after the
// close and be billed next month as prior-period usage, which is correct but reads worse than billing
// it on time. The drain empties a queue within minutes (ADR 0044), so CloseAfter is plenty.
func (j *Job) CloseAccount(ctx context.Context, accountID int) (int, error) {
	done := 0
	for range 24 { // a two-year backlog at most in one pass; the next pass continues
		account, err := j.DB.Client.BillingAccount.Get(ctx, accountID)
		if err != nil {
			return done, err
		}
		if account.Collection != billingaccount.CollectionInvoiced || account.InvoicedThrough == nil {
			return done, nil
		}
		end := MonthOf(*account.InvoicedThrough).AddDate(0, 1, 0)
		if j.now().Before(end.Add(j.closeAfter())) {
			return done, nil
		}
		queued, err := j.DB.Client.UsageCharge.Query().
			Where(usagecharge.AccountID(accountID), usagecharge.SettledAtIsNil(),
				usagecharge.PeriodStartLT(end)).
			Exist(ctx)
		if err != nil {
			return done, fmt.Errorf("look for queued usage: %w", err)
		}
		if queued {
			j.log().Info("an invoice waits for its month's usage to settle",
				"account_id", accountID, "month", MonthOf(*account.InvoicedThrough).Format("2006-01"))
			return done, nil
		}

		tx, err := j.DB.Client.Tx(ctx)
		if err != nil {
			return done, err
		}
		row, err := Close(ctx, tx, accountID, end, false)
		if err != nil {
			_ = tx.Rollback()
			return done, err
		}
		if err := tx.Commit(); err != nil {
			return done, err
		}
		if row == nil {
			return done, nil
		}
		done++
		if row.DiscrepancyMicros != 0 {
			j.log().Error("an invoice's usage does not match the dashboard's month",
				"account_id", accountID, "invoice", row.UID, "difference_micros", row.DiscrepancyMicros)
		}
	}
	return done, nil
}

// issueDrafts issues every invoice written and not yet at Stripe.
func (j *Job) issueDrafts(ctx context.Context) (int, error) {
	now := j.now()
	ids, err := j.DB.Client.Invoice.Query().
		Where(invoice.StatusEQ(invoice.StatusDraft),
			invoice.Or(invoice.IssueAttemptedAtIsNil(), invoice.IssueAttemptedAtLTE(now.Add(-j.staleAfter())))).
		Order(ent.Asc(invoice.FieldID)).
		Limit(j.maxAccounts()).
		IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("find invoices to issue: %w", err)
	}
	var (
		done int
		errs []error
	)
	for _, id := range ids {
		issued, err := j.Issue(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("invoice %d: %w", id, err))
			continue
		}
		if issued {
			done++
		}
	}
	return done, errors.Join(errs...)
}

// Issue sends one draft to Stripe, and reports whether this call issued it.
//
// The claim is the attempt's timestamp, written and committed before Stripe is asked, so two passes
// do not ask at once; the key was committed with the invoice. No answer keeps the draft, and the next
// pass after StaleAfter asks again with the same key — which Stripe answers with the invoice it already
// made, or IssueInvoice finds by its uid.
func (j *Job) Issue(ctx context.Context, invoiceID int) (bool, error) {
	row, account, err := j.claim(ctx, invoiceID)
	if err != nil || row == nil {
		return false, err
	}
	if account.StripeCustomerID == nil {
		return false, j.fail(ctx, row, "the account has no Stripe customer to invoice")
	}
	// Through the application's client: the row was read in the claim's transaction, which is over.
	lines, err := j.DB.Client.InvoiceLine.Query().
		Where(invoiceline.InvoiceID(row.ID)).
		Order(ent.Asc(invoiceline.FieldPosition)).
		All(ctx)
	if err != nil {
		return false, err
	}
	request := payments.InvoiceRequest{
		AccountUID:   account.UID.String(),
		InvoiceUID:   row.UID.String(),
		Customer:     *account.StripeCustomerID,
		Currency:     row.Currency,
		DaysUntilDue: int64(row.TermsDays),
		Description:  "PlayerSense, " + describePeriod(row.PeriodStart, row.PeriodEnd),
		PONumber:     row.PoNumber,
		Memo:         row.Memo,
		Since:        row.CreatedAt,
		Key:          row.IssueKey,
	}
	for _, l := range lines {
		if l.AmountCents != 0 {
			request.Lines = append(request.Lines, payments.InvoiceLine{
				Position: l.Position, Description: l.Description, AmountCents: l.AmountCents,
			})
		}
	}

	issued, err := j.Gateway.IssueInvoice(ctx, request)
	if err != nil {
		_ = j.fail(ctx, row, err.Error())
		j.log().Warn("an invoice got no answer from Stripe; it will be retried with the same key",
			"invoice", row.UID, "error", err)
		return false, err
	}

	update := j.DB.Client.Invoice.Update().
		Where(invoice.ID(row.ID), invoice.StatusEQ(invoice.StatusDraft)).
		SetStatus(invoice.StatusOpen).
		SetStripeInvoiceID(issued.ID).
		SetNumber(issued.Number).
		SetHostedURL(issued.HostedURL).
		SetPdfURL(issued.PDFURL).
		SetIssuedAt(j.now()).
		SetLastError("")
	if !issued.DueAt.IsZero() {
		update = update.SetDueAt(issued.DueAt)
	}
	if _, err := update.Save(ctx); err != nil {
		return false, fmt.Errorf("record the issued invoice: %w", err)
	}
	return true, nil
}

// claim marks a draft as being issued, unless another pass is already at it.
func (j *Job) claim(ctx context.Context, invoiceID int) (*ent.Invoice, *ent.BillingAccount, error) {
	tx, err := j.DB.Client.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := tx.Invoice.Query().
		Where(invoice.ID(invoiceID)).
		ForUpdate(entsql.WithLockAction(entsql.SkipLocked)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	now := j.now()
	if row.Status != invoice.StatusDraft ||
		(row.IssueAttemptedAt != nil && now.Sub(*row.IssueAttemptedAt) < j.staleAfter()) {
		return nil, nil, nil
	}
	if err := tx.Invoice.UpdateOneID(row.ID).SetIssueAttemptedAt(now).Exec(ctx); err != nil {
		return nil, nil, err
	}
	account, err := tx.BillingAccount.Get(ctx, row.AccountID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return row, account, nil
}

// fail records why the last attempt did not issue, for the operator's list.
func (j *Job) fail(ctx context.Context, row *ent.Invoice, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return j.DB.Client.Invoice.UpdateOneID(row.ID).SetLastError(reason).Exec(ctx)
}

func (j *Job) closeAfter() time.Duration {
	if j.CloseAfter > 0 {
		return j.CloseAfter
	}
	return 2 * time.Hour
}

func (j *Job) staleAfter() time.Duration {
	if j.StaleAfter > 0 {
		return j.StaleAfter
	}
	return 10 * time.Minute
}

func (j *Job) maxAccounts() int {
	if j.MaxAccounts > 0 {
		return j.MaxAccounts
	}
	return 100
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
