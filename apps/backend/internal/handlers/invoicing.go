package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/invoiceline"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/predicate"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/wallet"
)

// Contracted accounts, billed by monthly invoice (ADR 0045).
//
// Staff put an organization on a contract; the billing writer closes its months and issues the
// invoices; Stripe collects them and says so by webhook. What staff do to an invoice — mark it paid for
// a wire Stripe did not see, credit part of it — is asked of Stripe, and Stripe's answer is applied the
// way its webhook would apply it, so the webhook that follows finds nothing left to do.

// ---- the operator surface ---------------------------------------------------

// SetContract puts an organization on new terms, from now.
func SetContract(ctx *muzak.Context, in schemas.SetContractIn) (schemas.BillingOut, error) {
	db := muzak.From[*core.DB](ctx)
	settings := muzak.From[config.Settings](ctx)
	gateway := muzak.From[payments.Gateway](ctx)

	org, err := organizationBySlug(ctx.Context(), db, in.OrgSlug)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	invoiced := in.Collection == string(billingcontract.CollectionInvoiced)
	email := in.BillingEmail
	if email == "" && account.BillingEmail != nil {
		email = *account.BillingEmail
	}

	if invoiced {
		switch {
		case in.CreditLimitMicros == nil:
			return schemas.BillingOut{}, muzak.UnprocessableEntity(
				"an invoiced contract needs a credit limit: the most the organization may owe, billed or not").
				WithCode("credit_limit_required")
		case email == "":
			return schemas.BillingOut{}, muzak.UnprocessableEntity(
				"an invoiced contract needs a billing email: Stripe sends the invoices there").
				WithCode("billing_email_required")
		case account.SubscriptionStatus != nil && payments.Live(*account.SubscriptionStatus):
			// Two bills for one plan: the subscription's, and the contract's platform fee.
			return schemas.BillingOut{}, muzak.Conflict(
				"the organization has a live subscription; cancel it first — an invoiced contract bills " +
					"its platform fee on the monthly invoice").WithCode("subscribed")
		}
		// At Stripe before anything here changes: an account invoiced with no customer to invoice
		// would close months nobody could be sent.
		customer, err := operatorCustomer(ctx, account, org, email)
		if err != nil {
			return schemas.BillingOut{}, err
		}
		if err := gateway.SetBillingEmail(ctx.Context(), customer, email); err != nil {
			return schemas.BillingOut{}, paymentsError(err)
		}
	}

	terms := settings.BillingInvoiceTermsDays
	if in.TermsDays != nil {
		terms = *in.TermsDays
	}
	grace := int(settings.BillingInvoiceOverdueGrace / (24 * time.Hour))
	if in.OverdueGraceDays != nil {
		grace = *in.OverdueGraceDays
	}
	now := nowUTC()

	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		var none struct{}
		locked, err := tx.BillingAccount.Query().
			Where(billingaccount.ID(account.ID)).ForUpdate().Only(ctx.Context())
		if err != nil {
			return none, err
		}
		was := locked.Collection

		switch {
		case invoiced && was == billingaccount.CollectionPrepaid:
			if err := startInvoicing(ctx.Context(), tx, locked, now); err != nil {
				return none, err
			}
		case !invoiced && was == billingaccount.CollectionInvoiced:
			if err := stopInvoicing(ctx.Context(), tx, locked.ID, now); err != nil {
				return none, err
			}
		}

		update := tx.BillingAccount.UpdateOneID(locked.ID)
		if in.CreditLimitMicros != nil {
			overrides := map[string]int64{}
			for name, amount := range locked.EntitlementOverrides {
				overrides[name] = amount
			}
			overrides[string(billing.CreditLimit)] = *in.CreditLimitMicros
			update = update.SetEntitlementOverrides(overrides)
		}
		if in.BillingEmail != "" {
			update = update.SetBillingEmail(in.BillingEmail)
		}
		if err := update.Exec(ctx.Context()); err != nil {
			return none, err
		}

		contract := tx.BillingContract.Create().
			SetAccountID(locked.ID).
			SetCollection(billingcontract.Collection(in.Collection)).
			SetEffectiveFrom(now).
			SetTermsDays(terms).
			SetOverdueGraceDays(grace).
			SetPlatformFeeMicros(in.PlatformFeeMicros).
			SetMinimumCommitmentMicros(in.MinimumCommitmentMicros).
			SetPoNumber(in.PONumber).
			SetMemo(in.Memo).
			SetOperator(staffEmail(ctx))
		if in.CreditLimitMicros != nil {
			contract = contract.SetCreditLimitMicros(*in.CreditLimitMicros)
		}
		if _, err := contract.Save(ctx.Context()); err != nil {
			return none, err
		}
		return none, auditFor(ctx).forOrganization(org.ID).byStaff(ctx).
			record(ctx.Context(), tx, audit.ContractSet, "billing_account", org.Slug, map[string]any{
				"collection":                in.Collection,
				"was_collection":            string(was),
				"terms_days":                terms,
				"overdue_grace_days":        grace,
				"platform_fee_micros":       in.PlatformFeeMicros,
				"minimum_commitment_micros": in.MinimumCommitmentMicros,
				"credit_limit_micros":       in.CreditLimitMicros,
				"po_number":                 in.PONumber,
			})
	})
	if err != nil {
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	// The credit limit is an entitlement the charger caches with the organization's pricing.
	invalidateOrg(ctx, org.ID)
	fresh, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	return billingOut(ctx.Context(), db, fresh, now, policyOf(ctx))
}

// startInvoicing moves a prepaid account to invoicing, from now.
//
// Its first period starts here, and its first invoice bills what the ledger accrues from here — so the
// ledger's end is recorded before the arrears it had are carried into its receivable, and that carried
// debt is on the first invoice. Automatic top-up goes off: the account is billed, not charged.
func startInvoicing(ctx context.Context, tx *ent.Tx, account *ent.BillingAccount, now time.Time) error {
	cursor, err := tx.LedgerEntry.Query().
		Where(ledgerentry.AccountID(account.ID)).
		Order(ent.Desc(ledgerentry.FieldID)).
		FirstID(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if _, err := wallet.CarryArrears(ctx, tx, account.ID, "carry:"+uuid.NewString()); err != nil {
		return err
	}
	return tx.BillingAccount.UpdateOneID(account.ID).
		SetCollection(billingaccount.CollectionInvoiced).
		SetInvoicedThrough(now).
		SetInvoicedLedgerID(cursor).
		SetAutoTopupEnabled(false).
		ClearAutoTopupRequestedAt().
		Exec(ctx)
}

// stopInvoicing moves an invoiced account back to prepaid, billing everything not yet billed.
//
// Every whole month not yet closed is closed as the billing writer would close it, and what is left of
// the current one is a final invoice to now. Usage still queued settles after the switch, and is charged
// to the wallet as any prepaid account's is. Open invoices stay collectable.
func stopInvoicing(ctx context.Context, tx *ent.Tx, accountID int, now time.Time) error {
	for range 24 {
		account, err := tx.BillingAccount.Get(ctx, accountID)
		if err != nil {
			return err
		}
		if account.InvoicedThrough == nil {
			break
		}
		end := invoicing.MonthOf(*account.InvoicedThrough).AddDate(0, 1, 0)
		if end.After(now) {
			break
		}
		if _, err := invoicing.Close(ctx, tx, accountID, end, false); err != nil {
			return err
		}
	}
	if _, err := invoicing.Close(ctx, tx, accountID, now, true); err != nil && !errors.Is(err, invoicing.ErrNotInvoiced) {
		return err
	}
	return tx.BillingAccount.UpdateOneID(accountID).
		SetCollection(billingaccount.CollectionPrepaid).
		Exec(ctx)
}

// operatorCustomer is the account's Stripe customer, opened the first time — the operator path's
// ensureCustomer, which has no signed-in person to name.
func operatorCustomer(ctx *muzak.Context, account *ent.BillingAccount, org *ent.Organization, email string) (string, error) {
	if account.StripeCustomerID != nil {
		return *account.StripeCustomerID, nil
	}
	db := muzak.From[*core.DB](ctx)
	customer, err := muzak.From[payments.Gateway](ctx).CreateCustomer(ctx.Context(), payments.CustomerRequest{
		AccountUID: account.UID.String(), Name: org.Name, Email: email, OrgSlug: org.Slug,
	})
	if err != nil {
		return "", paymentsError(err)
	}
	if err := db.Client.BillingAccount.Update().
		Where(billingaccount.ID(account.ID), billingaccount.StripeCustomerIDIsNil()).
		SetStripeCustomerID(customer).
		Exec(ctx.Context()); err != nil {
		return "", core.AsHTTPError(err)
	}
	return stripeCustomerOf(ctx.Context(), db, account.ID)
}

// ListOperatorInvoices is an organization's invoices as staff see them, drafts and failures included.
func ListOperatorInvoices(ctx *muzak.Context, in schemas.OperatorOrganizationParams) (schemas.OperatorInvoicesOut, error) {
	db := muzak.From[*core.DB](ctx)
	account, err := accountBySlug(ctx.Context(), db, in.Slug)
	if err != nil {
		return schemas.OperatorInvoicesOut{}, err
	}
	rows, err := invoicesOf(ctx.Context(), db, account.ID)
	if err != nil {
		return schemas.OperatorInvoicesOut{}, err
	}
	out := schemas.OperatorInvoicesOut{Invoices: make([]schemas.OperatorInvoiceOut, 0, len(rows))}
	now := nowUTC()
	for _, row := range rows {
		out.Invoices = append(out.Invoices, operatorInvoiceOut(row, now))
	}
	return out, nil
}

// MarkInvoicePaid records a payment Stripe did not see, by telling Stripe.
//
// The invoice is marked paid out of band at Stripe, and Stripe's answer settled here as its webhook
// would settle it. A second press — the same reference, or an invoice already paid — changes nothing.
func MarkInvoicePaid(ctx *muzak.Context, in schemas.MarkInvoicePaidIn) (schemas.OperatorInvoiceOut, error) {
	db := muzak.From[*core.DB](ctx)
	account, row, err := operatorInvoice(ctx.Context(), db, in.OrgSlug, in.InvoiceID)
	if err != nil {
		return schemas.OperatorInvoiceOut{}, err
	}
	if row.Status == invoice.StatusPaid {
		return operatorInvoiceOut(row, nowUTC()), nil
	}
	if row.Status != invoice.StatusOpen || row.StripeInvoiceID == nil {
		return schemas.OperatorInvoiceOut{}, muzak.Conflict(
			"only an open invoice can be marked paid; this one is " + string(row.Status)).WithCode("invoice_not_open")
	}
	issued, err := muzak.From[payments.Gateway](ctx).PayInvoiceOutOfBand(ctx.Context(), *row.StripeInvoiceID,
		"playersense-paid-"+row.UID.String()+"-"+in.Reference)
	if err != nil {
		return schemas.OperatorInvoiceOut{}, paymentsError(err)
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		var none struct{}
		if _, _, err := payments.ApplyInvoice(ctx.Context(), tx, issued, nowUTC()); err != nil {
			return none, err
		}
		return none, auditFor(ctx).forOrganization(account.OrganizationID).byStaff(ctx).
			record(ctx.Context(), tx, audit.InvoicePaid, "invoice", row.UID.String(), map[string]any{
				"number": issued.Number, "reference": in.Reference,
				"amount_micros": issued.AmountPaid * billing.CentMicros,
			})
	})
	if err != nil {
		return schemas.OperatorInvoiceOut{}, core.AsHTTPError(err)
	}
	return freshOperatorInvoice(ctx.Context(), db, row.ID)
}

// CreditInvoice issues a credit note lowering what an open invoice asks for.
func CreditInvoice(ctx *muzak.Context, in schemas.CreditInvoiceIn) (schemas.OperatorInvoiceOut, error) {
	db := muzak.From[*core.DB](ctx)
	account, row, err := operatorInvoice(ctx.Context(), db, in.OrgSlug, in.InvoiceID)
	if err != nil {
		return schemas.OperatorInvoiceOut{}, err
	}
	switch {
	case row.Status != invoice.StatusOpen || row.StripeInvoiceID == nil:
		return schemas.OperatorInvoiceOut{}, muzak.Conflict(
			"only an open invoice can be credited; this one is " + string(row.Status)).WithCode("invoice_not_open")
	case in.AmountMicros%billing.CentMicros != 0:
		return schemas.OperatorInvoiceOut{}, muzak.UnprocessableEntity(
			"a credit note is whole cents").WithCode("invalid_amount")
	case in.AmountMicros > row.AmountRemainingMicros:
		return schemas.OperatorInvoiceOut{}, muzak.UnprocessableEntity(
			billing.USD(row.AmountRemainingMicros) + " is still owed on this invoice; a credit note cannot be more").
			WithCode("invalid_amount")
	}
	note, err := muzak.From[payments.Gateway](ctx).CreditInvoice(ctx.Context(), payments.CreditNoteRequest{
		AccountUID: account.UID.String(), InvoiceUID: row.UID.String(), Invoice: *row.StripeInvoiceID,
		AmountCents: in.AmountMicros / billing.CentMicros, Memo: in.Reason,
		Key: "playersense-credit-" + row.UID.String() + "-" + in.Reference,
	})
	if err != nil {
		return schemas.OperatorInvoiceOut{}, paymentsError(err)
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		var none struct{}
		applied, err := payments.ApplyCreditNote(ctx.Context(), tx, row, note, nowUTC())
		if err != nil || !applied {
			return none, err
		}
		return none, auditFor(ctx).forOrganization(account.OrganizationID).byStaff(ctx).
			record(ctx.Context(), tx, audit.InvoiceCredited, "invoice", row.UID.String(), map[string]any{
				"credit_note": note.ID, "amount_micros": in.AmountMicros, "reason": in.Reason,
				"reference": in.Reference,
			})
	})
	if err != nil {
		return schemas.OperatorInvoiceOut{}, core.AsHTTPError(err)
	}
	return freshOperatorInvoice(ctx.Context(), db, row.ID)
}

// ---- the customer's side ----------------------------------------------------

// ListInvoices is an organization's invoices, newest first. Drafts are left out until they are issued:
// a customer reads what they were sent.
func ListInvoices(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.InvoicesOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.InvoicesOut{}, err
	}
	rows, err := invoicesOf(ctx.Context(), db, account.ID, invoice.StatusNEQ(invoice.StatusDraft))
	if err != nil {
		return schemas.InvoicesOut{}, err
	}
	out := schemas.InvoicesOut{Invoices: make([]schemas.InvoiceOut, 0, len(rows))}
	now := nowUTC()
	for _, row := range rows {
		out.Invoices = append(out.Invoices, invoiceOut(row, now))
	}
	return out, nil
}

// ReadInvoice is one invoice, with its lines.
func ReadInvoice(ctx *muzak.Context, in schemas.InvoiceParams) (schemas.InvoiceOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.InvoiceOut{}, err
	}
	rows, err := invoicesOf(ctx.Context(), db, account.ID,
		invoice.UID(in.InvoiceID), invoice.StatusNEQ(invoice.StatusDraft))
	if err != nil {
		return schemas.InvoiceOut{}, err
	}
	if len(rows) == 0 {
		return schemas.InvoiceOut{}, muzak.NotFound("no such invoice").WithCode("invoice_not_found")
	}
	return invoiceOut(rows[0], nowUTC()), nil
}

// ---- lookups and mapping ----------------------------------------------------

func invoicesOf(ctx context.Context, db *core.DB, accountID int, where ...predicate.Invoice) ([]*ent.Invoice, error) {
	query := db.Client.Invoice.Query().
		Where(invoice.AccountID(accountID)).
		WithLines(func(q *ent.InvoiceLineQuery) { q.Order(ent.Asc(invoiceline.FieldPosition)) }).
		Order(ent.Desc(invoice.FieldPeriodStart))
	query = query.Where(where...)
	rows, err := query.All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return rows, nil
}

// operatorInvoice is an invoice of the organization a staff route names, 404 when it is not its.
func operatorInvoice(ctx context.Context, db *core.DB, slug string, id uuid.UUID) (*ent.BillingAccount, *ent.Invoice, error) {
	account, err := accountBySlug(ctx, db, slug)
	if err != nil {
		return nil, nil, err
	}
	row, err := db.Client.Invoice.Query().
		Where(invoice.UID(id), invoice.AccountID(account.ID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, muzak.NotFound("no such invoice").WithCode("invoice_not_found")
	}
	if err != nil {
		return nil, nil, core.AsHTTPError(err)
	}
	return account, row, nil
}

func freshOperatorInvoice(ctx context.Context, db *core.DB, id int) (schemas.OperatorInvoiceOut, error) {
	row, err := db.Client.Invoice.Query().Where(invoice.ID(id)).
		WithLines(func(q *ent.InvoiceLineQuery) { q.Order(ent.Asc(invoiceline.FieldPosition)) }).
		Only(ctx)
	if err != nil {
		return schemas.OperatorInvoiceOut{}, core.AsHTTPError(err)
	}
	return operatorInvoiceOut(row, nowUTC()), nil
}

func invoiceOut(row *ent.Invoice, at time.Time) schemas.InvoiceOut {
	out := schemas.InvoiceOut{
		ID: row.UID, Number: row.Number, Status: string(row.Status),
		PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd, Final: row.Final,
		Currency: row.Currency, TotalMicros: row.TotalMicros, Total: billing.USD(row.TotalMicros),
		AmountPaidMicros: row.AmountPaidMicros, AmountRemainingMicros: row.AmountRemainingMicros,
		IssuedAt: row.IssuedAt, DueAt: row.DueAt, PaidAt: row.PaidAt,
		HostedURL: row.HostedURL, PDFURL: row.PdfURL, PONumber: row.PoNumber,
		Lines: make([]schemas.InvoiceLineOut, 0, len(row.Edges.Lines)),
	}
	out.Overdue = row.Status == invoice.StatusOpen && row.DueAt != nil && row.DueAt.Before(at) &&
		row.AmountRemainingMicros > 0
	for _, line := range row.Edges.Lines {
		out.Lines = append(out.Lines, schemas.InvoiceLineOut{
			Kind: string(line.Kind), Description: line.Description,
			AmountMicros: line.AmountMicros, AmountCents: line.AmountCents, PeriodStart: line.PeriodStart,
		})
	}
	return out
}

func operatorInvoiceOut(row *ent.Invoice, at time.Time) schemas.OperatorInvoiceOut {
	out := schemas.OperatorInvoiceOut{
		InvoiceOut: invoiceOut(row, at), LastError: row.LastError,
		DiscrepancyMicros: row.DiscrepancyMicros, CarryInMicros: row.CarryInMicros,
		CarryOutMicros: row.CarryOutMicros, LedgerFromID: row.LedgerFromID, LedgerToID: row.LedgerToID,
	}
	if row.StripeInvoiceID != nil {
		out.StripeInvoiceID = *row.StripeInvoiceID
	}
	return out
}

// refuseInvoiced answers a customer's attempt to pay by card for an account that is billed by invoice.
func refuseInvoiced(account *ent.BillingAccount, what string) error {
	if !wallet.IsInvoiced(account) {
		return nil
	}
	return muzak.Conflict("this organization is billed by monthly invoice, so " + what +
		"; pay the invoices from the billing page").WithCode("invoiced_account")
}
