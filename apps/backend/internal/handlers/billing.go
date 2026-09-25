package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/billingcontract"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/invoice"
	"playersense.dev/backend/ent/ledgerentry"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/ent/usagedaily"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/invoicing"
	"playersense.dev/backend/internal/payments"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/suspension"
	"playersense.dev/backend/internal/wallet"
)

// Usage, the wallet, and the operator surface that puts credit in it.
//
// # The balance is not a column
//
// `BillingAccount.balance_micros` is the ledger's running total. What can be *spent* is the sum of
// what is left in every lot that has not expired — computed, because expiry happens with the passage
// of time rather than with a write, so a stored total is correct only until midnight. The two agree
// once the janitor's sweep has caught up, and both are returned: a number that looks wrong should be
// explainable rather than merely defended.
//
// # Granting credit is not a customer's privilege at any permission
//
// A studio that could grant itself promotional credit would be a studio on an unlimited plan.
// `billing.manage` is the widest thing a customer holds and it configures automatic charging; putting
// money in the wallet is a staff action, behind the console's staff sign-in (core.ResolveStaff).

// ---- usage ------------------------------------------------------------------

// ReadUsage answers "what are we using, and what does the plan include".
//
// Both halves, because either alone is unreadable: 41,207 tracked players means nothing without the
// 10,000 the plan allows, and the allowance means nothing without the number beside it.
func ReadUsage(ctx *muzak.Context, in schemas.UsageParams) (schemas.UsageOut, error) {
	org := muzak.From[*core.Principal](ctx).Organization
	return usageOf(ctx.Context(), muzak.From[*core.DB](ctx), org.ID, in.From, in.To)
}

// usageOf is one organization's usage over a range, for its own page and for the console.
func usageOf(ctx context.Context, db *core.DB, orgID int, fromText, toText string) (schemas.UsageOut, error) {
	from, to, err := usageRange(fromText, toText, nowUTC())
	if err != nil {
		return schemas.UsageOut{}, err
	}

	rows, err := db.Client.UsageDaily.Query().
		Where(
			usagedaily.OrganizationID(orgID),
			usagedaily.DayGTE(from),
			usagedaily.DayLTE(to),
		).
		Order(ent.Asc(usagedaily.FieldDay)).
		All(ctx)
	if err != nil {
		return schemas.UsageOut{}, core.AsHTTPError(err)
	}

	out := schemas.UsageOut{From: from, To: to, Final: true}

	// The plan's allowances with any per-account override applied, so a client draws one bar
	// rather than layering two documents itself.
	if account, err := accountOf(ctx, db, orgID); err == nil {
		out.Included = resolvedEntitlements(account)
	} else if !ent.IsNotFound(err) {
		return schemas.UsageOut{}, core.AsHTTPError(err)
	}

	projects, err := db.Client.Project.Query().
		Where(project.OrganizationID(orgID)).All(ctx)
	if err != nil {
		return schemas.UsageOut{}, core.AsHTTPError(err)
	}
	byID := make(map[int]*ent.Project, len(projects))
	for _, proj := range projects {
		byID[proj.ID] = proj
	}

	var (
		perProject = map[int]*schemas.UsageByProjectOut{}
		perDay     = map[time.Time]*schemas.UsageDayOut{}
		order      []time.Time
		lastDay    time.Time
	)
	for _, row := range rows {
		if !row.Final {
			out.Final = false
		}

		// Tracked players is a peak rather than a sum, everywhere it appears. Adding DAU
		// counts one person once per day they played, which for a month is a number four
		// times the truth and is the single easiest way to publish a usage page a studio
		// disbelieves. §36's meter is monthly tracked players, and a peak is the honest
		// reading a daily table can give.
		if row.Day.After(lastDay) {
			lastDay = row.Day
		}

		day, found := perDay[row.Day]
		if !found {
			day = &schemas.UsageDayOut{Day: row.Day, Final: true}
			perDay[row.Day] = day
			order = append(order, row.Day)
		}
		day.TrackedPlayers += row.TrackedPlayers
		day.Events += row.EventsAccepted
		day.RetainedBytes += row.RetainedBytes
		if !row.Final {
			day.Final = false
		}

		share, found := perProject[row.ProjectID]
		if !found {
			share = &schemas.UsageByProjectOut{}
			if proj := byID[row.ProjectID]; proj != nil {
				share.Project = projectRefOut(proj)
			}
			perProject[row.ProjectID] = share
		}
		share.Totals.Events += row.EventsAccepted
		share.Totals.PeakDailyPlayers = max(share.Totals.PeakDailyPlayers, row.TrackedPlayers)
		// Storage is a level rather than an accrual: the answer is what was retained on the
		// last day of the range, not the sum of every day's holding.
		if row.Day.Equal(lastDay) {
			share.Totals.RetainedBytes = row.RetainedBytes
		}
	}

	for _, day := range order {
		one := perDay[day]
		out.Days = append(out.Days, *one)
		out.Totals.Events += one.Events
		out.Totals.PeakDailyPlayers = max(out.Totals.PeakDailyPlayers, one.TrackedPlayers)
		if day.Equal(lastDay) {
			out.Totals.RetainedBytes = one.RetainedBytes
		}
	}
	for _, proj := range projects {
		if share, found := perProject[proj.ID]; found {
			out.Projects = append(out.Projects, *share)
		}
	}
	return out, nil
}

// usageRange defaults to the current calendar month, which is what a billing page opens on.
func usageRange(from, to string, now time.Time) (time.Time, time.Time, error) {
	parse := func(name, value string) (time.Time, error) {
		day, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return time.Time{}, muzak.UnprocessableEntity(
				name + " is not a date; write it as YYYY-MM-DD").WithCode("invalid_date")
		}
		return day.UTC(), nil
	}

	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := now.Truncate(24 * time.Hour)

	var err error
	if from != "" {
		if start, err = parse("from", from); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if to != "" {
		if end, err = parse("to", to); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, muzak.UnprocessableEntity(
			"the range ends before it starts").WithCode("invalid_range")
	}
	// Bounded, because an unbounded range is a scan of every day this organization has ever had
	// on a page somebody reloads.
	if end.Sub(start) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, muzak.UnprocessableEntity(
			"a usage range covers at most 400 days").WithCode("range_too_long")
	}
	return start, end, nil
}

// ---- the wallet -------------------------------------------------------------

// ReadBilling answers "what are we on, and what have we got left".
func ReadBilling(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.BillingOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	return billingOut(ctx.Context(), db, account, nowUTC(), policyOf(ctx))
}

// ReadLedger answers "why is my balance this".
//
// Newest first, because the question is almost always about the most recent movement. Append-only by
// construction, so what is read today is what was read last month.
func ReadLedger(ctx *muzak.Context, in schemas.LedgerParams) (schemas.LedgerOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.LedgerOut{}, err
	}
	return ledgerOf(ctx.Context(), db, account.ID, in.Limit, in.Before)
}

// ledgerOf is one page of an account's statement, newest first. before is the next cursor of the
// previous page: the entries' ids are the ledger's order, so a page never repeats or skips a row
// written while somebody reads.
func ledgerOf(ctx context.Context, db *core.DB, accountID, limit int, before string) (schemas.LedgerOut, error) {
	if limit == 0 {
		limit = 50
	}
	q := db.Client.LedgerEntry.Query().Where(ledgerentry.AccountID(accountID))
	if before != "" {
		id, err := strconv.Atoi(before)
		if err != nil {
			return schemas.LedgerOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(ledgerentry.IDLT(id))
	}
	rows, err := q.Order(ent.Desc(ledgerentry.FieldID)).Limit(limit + 1).All(ctx)
	if err != nil {
		return schemas.LedgerOut{}, core.AsHTTPError(err)
	}

	out := schemas.LedgerOut{Entries: make([]schemas.LedgerEntryOut, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	for _, row := range rows {
		out.Entries = append(out.Entries, ledgerEntryOut(row))
	}
	return out, nil
}

// SetAutoTopUp configures automatic charging.
//
// The rule about how the fields relate is [billing.AutoTopUp.Validate], which was written before this
// route existed and had no caller. Its own comment said why: "a rule that waits for its caller is a
// rule that gets re-remembered differently." This is the caller.
func SetAutoTopUp(ctx *muzak.Context, in schemas.SetAutoTopUpIn) (schemas.BillingOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[*core.Principal](ctx).Organization

	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}

	if in.Enabled {
		if err := refuseInvoiced(account, "it is never topped up automatically"); err != nil {
			return schemas.BillingOut{}, err
		}
	}

	// Whether Stripe holds a card it can charge, asked of Stripe rather than of a column: cards are
	// managed in Stripe's portal and never stored here (ADR 0040). Asked only when turning it on,
	// because switching it off must work even while Stripe is unreachable.
	hasCard := false
	if in.Enabled {
		var err error
		if hasCard, err = chargeable(ctx, account); err != nil {
			return schemas.BillingOut{}, err
		}
	}

	wanted := billing.AutoTopUp{
		Enabled:   in.Enabled,
		Threshold: orKeep(in.ThresholdMicros, account.AutoTopupThresholdMicros),
		Amount:    orKeep(in.AmountMicros, account.AutoTopupAmountMicros),
		HasCard:   hasCard,
	}
	if err := wanted.Validate(); err != nil {
		return schemas.BillingOut{}, muzak.UnprocessableEntity(err.Error()).
			WithCode("auto_topup_not_workable")
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	// The row as it is now, locked: the top-up signal below depends on the balance, and a drain
	// moving it between the read above and this write would otherwise leave the signal computed
	// from a balance that no longer exists.
	locked, err := tx.BillingAccount.Query().
		Where(billingaccount.ID(account.ID)).ForUpdate().Only(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	account = locked

	// Built from the transaction rather than from the account row. `account.Update()` carries the
	// application's client, so it would have committed on its own — leaving a configuration change
	// that happened and an audit entry that was rolled back, which is the one combination this
	// whole design exists to rule out.
	update := tx.BillingAccount.UpdateOneID(account.ID).
		SetAutoTopupEnabled(wanted.Enabled).
		SetAutoTopupThresholdMicros(wanted.Threshold).
		SetAutoTopupAmountMicros(wanted.Amount)
	if wanted.Enabled {
		// A fresh start: a decline recorded against the old configuration says nothing about
		// the one just saved.
		update = update.ClearAutoTopupRetryAfter().SetAutoTopupLastError("")
	}
	// The signal follows the settings as it follows the balance (ADR 0041): an account already
	// below its new threshold is asked for now, not at its next charge; one switched off stops
	// asking.
	after := *account
	after.AutoTopupEnabled = wanted.Enabled
	after.AutoTopupThresholdMicros = wanted.Threshold
	if wallet.WantsTopUp(&after, account.BalanceMicros) {
		if account.AutoTopupRequestedAt == nil {
			update = update.SetAutoTopupRequestedAt(nowUTC())
		}
	} else {
		update = update.ClearAutoTopupRequestedAt()
	}
	if _, err := update.Save(ctx.Context()); err != nil {
		_ = tx.Rollback()
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	// The previous values too, because "who turned this on" is half the question and "what was it
	// before" is the other half — and nothing else in the product can answer the second.
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.AutoTopUpSet,
		"billing_account", org.Slug, map[string]any{
			"enabled":          wanted.Enabled,
			"threshold_micros": wanted.Threshold,
			"amount_micros":    wanted.Amount,
			"was_enabled":      account.AutoTopupEnabled,
			"was_threshold":    account.AutoTopupThresholdMicros,
			"was_amount":       account.AutoTopupAmountMicros,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	// Re-read through the application's client rather than returning the row the transaction
	// produced: that row belongs to a connection which is now closed, and billingOut reads its
	// plan edge.
	fresh, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	return billingOut(ctx.Context(), db, fresh, nowUTC(), policyOf(ctx))
}

// ---- the operator surface ---------------------------------------------------

// GrantCredit puts credit in an organization's wallet.
//
// The staff member who grants it is on the lot (granted_by_staff), in the customer's audit log, and in
// staff_access with the ticket: "who gave this account $100" is the first question asked when an
// account has $100 nobody expected.
func GrantCredit(ctx *muzak.Context, in schemas.GrantCreditIn) (schemas.CreditGrantedOut, error) {
	db := muzak.From[*core.DB](ctx)

	account, err := accountBySlug(ctx.Context(), db, in.OrgSlug)
	if err != nil {
		return schemas.CreditGrantedOut{}, err
	}

	grantedAt := nowUTC()
	if in.GrantedAt != nil {
		grantedAt = in.GrantedAt.UTC()
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.CreditGrantedOut{}, core.AsHTTPError(err)
	}

	lot, granted, err := wallet.Grant(ctx.Context(), tx, wallet.GrantRequest{
		AccountID:       account.ID,
		Kind:            in.Kind,
		Amount:          in.AmountMicros,
		ExpiresAt:       in.ExpiresAt,
		GrantedAt:       grantedAt,
		Description:     in.Description,
		StripeReference: in.StripeReference,
		GrantedByStaff:  staffEmail(ctx),
		Key:             "operator:" + in.Reference,
	})
	if err != nil {
		_ = tx.Rollback()
		return schemas.CreditGrantedOut{}, walletError(err)
	}

	// Only when something happened. A replayed reference wrote no lot, and an entry for it would
	// make the log say credit was granted twice — which is the opposite of what the idempotency
	// key is for.
	if granted {
		if err := auditFor(ctx).forOrganization(account.OrganizationID).
			byStaff(ctx).
			record(ctx.Context(), tx, audit.CreditGranted, "credit_lot", lot.UID.String(),
				map[string]any{
					"kind":          in.Kind,
					"amount_micros": in.AmountMicros,
					"expires_at":    expiryFor(lot),
					"description":   in.Description,
					"reference":     in.Reference,
				}); err != nil {
			_ = tx.Rollback()
			return schemas.CreditGrantedOut{}, core.AsHTTPError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return schemas.CreditGrantedOut{}, core.AsHTTPError(err)
	}

	available, err := availableOf(ctx.Context(), db, account.ID, nowUTC())
	if err != nil {
		return schemas.CreditGrantedOut{}, err
	}
	// Said explicitly in both directions. The route declares 201, so a replay that did not
	// override it would answer "created" for a grant it did not make.
	if granted {
		ctx.SetStatus(http.StatusCreated)
	} else {
		ctx.SetStatus(http.StatusOK)
	}
	return schemas.CreditGrantedOut{
		Lot:             creditLotOut(lot, nowUTC()),
		AvailableMicros: available,
		Available:       billing.USD(available),
		Granted:         granted,
	}, nil
}

// ChargeCredit spends from the wallet in the order the package documents.
//
// The operator's counterpart to a grant, and what exercises the spend order in production until §40's
// month close exists to do it on a schedule. It is here rather than on a customer's surface for the
// obvious reason: nobody charges their own account.
func ChargeCredit(ctx *muzak.Context, in schemas.ChargeCreditIn) (schemas.CreditChargedOut, error) {
	db := muzak.From[*core.DB](ctx)

	account, err := accountBySlug(ctx.Context(), db, in.OrgSlug)
	if err != nil {
		return schemas.CreditChargedOut{}, err
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.CreditChargedOut{}, core.AsHTTPError(err)
	}

	at := nowUTC()
	result, err := wallet.Spend(ctx.Context(), tx, wallet.SpendRequest{
		AccountID:   account.ID,
		Amount:      in.AmountMicros,
		Kind:        orDefault(in.Kind, "adjustment"),
		Description: in.Description,
		At:          at,
		Key:         "operator:" + in.Reference,
	})
	if err != nil {
		_ = tx.Rollback()
		return schemas.CreditChargedOut{}, walletError(err)
	}

	if result.Applied {
		if err := auditFor(ctx).forOrganization(account.OrganizationID).
			byStaff(ctx).
			record(ctx.Context(), tx, audit.CreditCharged, "billing_account", in.OrgSlug,
				map[string]any{
					"amount_micros": in.AmountMicros,
					"kind":          orDefault(in.Kind, "adjustment"),
					"description":   in.Description,
					"reference":     in.Reference,
					// Which grants paid for it, because that is the part a statement's
					// single line deliberately does not say.
					"lots": len(result.Draws),
				}); err != nil {
			_ = tx.Rollback()
			return schemas.CreditChargedOut{}, core.AsHTTPError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return schemas.CreditChargedOut{}, core.AsHTTPError(err)
	}

	// The lots the draws name, so the response can identify them the way a client does — by uid,
	// never by the row id a draw carries.
	uids := map[int]*ent.CreditLot{}
	for _, draw := range result.Draws {
		row, err := db.Client.CreditLot.Get(ctx.Context(), draw.LotID)
		if err != nil {
			return schemas.CreditChargedOut{}, core.AsHTTPError(err)
		}
		uids[draw.LotID] = row
	}

	available, err := availableOf(ctx.Context(), db, account.ID, at)
	if err != nil {
		return schemas.CreditChargedOut{}, err
	}

	out := schemas.CreditChargedOut{
		AmountMicros:    in.AmountMicros,
		Entry:           ledgerEntryOut(result.Entry),
		AvailableMicros: available,
		Available:       billing.USD(available),
		Charged:         result.Applied,
	}
	for _, draw := range result.Draws {
		out.Draws = append(out.Draws, schemas.DrawOut{
			Lot:          uids[draw.LotID].UID,
			Kind:         draw.Kind,
			AmountMicros: draw.Amount,
		})
	}
	return out, nil
}

// expiryFor renders a lot's expiry for an audit entry, or nothing when it has none.
//
// A string rather than a time, because the metadata is read by a person looking at a log and an RFC
// 3339 date is what they can compare against the one they were told.
func expiryFor(lot *ent.CreditLot) any {
	if lot.ExpiresAt == nil {
		return nil
	}
	return lot.ExpiresAt.Format(time.RFC3339)
}

// ---- lookups and mapping ----------------------------------------------------

// accountOf reads the billing account for an organization the principal is already inside.
func accountOf(ctx context.Context, db *core.DB, orgID int) (*ent.BillingAccount, error) {
	account, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(orgID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// An organization created before billing existed, or one whose account was never
			// provisioned. Said plainly rather than answered with an empty wallet, because a
			// balance of zero and no account at all are different problems.
			return nil, muzak.NotFound("this organization has no billing account yet").
				WithCode("no_billing_account")
		}
		return nil, core.AsHTTPError(err)
	}
	return account, nil
}

// accountBySlug is the operator path, which has no principal to read an organization from.
func accountBySlug(ctx context.Context, db *core.DB, slug string) (*ent.BillingAccount, error) {
	org, err := db.Client.Organization.Query().Where(organization.Slug(slug)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("no organization has the slug " + slug)
		}
		return nil, core.AsHTTPError(err)
	}
	return accountOf(ctx, db, org.ID)
}

// availableOf is what the wallet can spend right now.
func availableOf(ctx context.Context, db *core.DB, accountID int, at time.Time) (int64, error) {
	lots, err := wallet.LotsOf(ctx, db.Client.CreditLot, accountID)
	if err != nil {
		return 0, core.AsHTTPError(err)
	}
	return wallet.Available(lots, at), nil
}

// ExpiryWarning is how far ahead a balance says what it is about to lose.
//
// Fourteen days. Long enough that a studio can spend it or ask for an extension, and short enough
// that the warning means something — a banner about credit expiring in two months is a banner people
// learn to ignore.
const ExpiryWarning = 14 * 24 * time.Hour

func billingOut(ctx context.Context, db *core.DB, account *ent.BillingAccount,
	at time.Time, policy suspensionPolicy,
) (schemas.BillingOut, error) {
	rows, err := db.Client.CreditLot.Query().
		Where(creditlot.AccountID(account.ID), creditlot.RemainingMicrosGT(0)).
		All(ctx)
	if err != nil {
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}

	lots := make([]wallet.Lot, 0, len(rows))
	byID := make(map[int]*ent.CreditLot, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
		lots = append(lots, wallet.Lot{
			ID: row.ID, Kind: string(row.Kind), Remaining: row.RemainingMicros,
			ExpiresAt: row.ExpiresAt, GrantedAt: row.GrantedAt,
		})
	}

	out := schemas.BillingOut{
		State:               string(account.State),
		Currency:            account.Currency,
		AvailableMicros:     wallet.Available(lots, at),
		LedgerBalanceMicros: account.BalanceMicros,
		AutoTopUp: schemas.AutoTopUpOut{
			Enabled:         account.AutoTopupEnabled,
			ThresholdMicros: account.AutoTopupThresholdMicros,
			AmountMicros:    account.AutoTopupAmountMicros,
		},
	}
	out.Available = billing.USD(out.AvailableMicros)
	out.ArrearsMicros = account.ArrearsMicros
	out.Arrears = billing.USD(account.ArrearsMicros)
	out.SuspendsAt = suspension.SuspendsAt(account, policy.grace)
	limit, source, err := suspension.Limit(ctx, db.Client, account, policy.limit, at)
	if err != nil {
		return schemas.BillingOut{}, core.AsHTTPError(err)
	}
	out.CreditLimitMicros, out.CreditLimitSource = limit, string(source)
	month, err := monthOut(ctx, db, account, at)
	if err != nil {
		return schemas.BillingOut{}, err
	}
	out.Month = month
	out.AutoTopUp.Pending = account.AutoTopupPendingKey != nil
	out.AutoTopUp.Queued = account.AutoTopupRequestedAt != nil
	out.AutoTopUp.DailyLimit = account.AutoTopupDailyLimit
	if account.AutoTopupWindowStartedAt != nil && at.Sub(*account.AutoTopupWindowStartedAt) < payments.Day {
		out.AutoTopUp.UsedToday = account.AutoTopupWindowCount
	}
	out.AutoTopUp.RetryAfter = account.AutoTopupRetryAfter
	out.AutoTopUp.LastError = account.AutoTopupLastError
	out.Features = []string{}
	if account.SubscriptionStatus != nil {
		sub := &schemas.SubscriptionOut{
			Status: *account.SubscriptionStatus, PeriodEnd: account.SubscriptionPeriodEnd,
			CancelAtPeriodEnd: account.SubscriptionCancelAtPeriodEnd,
		}
		if account.SubscriptionInterval != nil {
			sub.Interval = string(*account.SubscriptionInterval)
		}
		out.Subscription = sub
	}
	if plan := account.Edges.Plan; plan != nil {
		if plan.Features != nil {
			out.Features = plan.Features
		}
		out.Plan = schemas.PlanOut{
			Key:                plan.Key,
			Name:               plan.Name,
			MonthlyPriceMicros: plan.MonthlyPriceMicros,
			Entitlements:       resolvedEntitlements(account),
		}
	}

	// In the order it will be spent, so a customer reading the list reads it in the order their
	// statement will consume it.
	for _, lot := range wallet.Order(lots) {
		out.Lots = append(out.Lots, creditLotOut(byID[lot.ID], at))
		if lot.Kind == wallet.Promo && lot.Spendable(at) {
			out.PromoMicros += lot.Remaining
		}
		if lot.Kind == wallet.Purchase && lot.Spendable(at) {
			out.PurchasedMicros += lot.Remaining
		}
	}
	for _, lot := range wallet.Expiring(lots, at, at.Add(ExpiryWarning)) {
		out.ExpiringSoon = append(out.ExpiringSoon, creditLotOut(byID[lot.ID], at))
	}
	out.Live = liveOut(account, lots, month.PendingMicros, at)
	if err := invoicedOut(ctx, db, account, &out, at, policy); err != nil {
		return schemas.BillingOut{}, err
	}
	return out, nil
}

// invoicedOut adds what an organization billed by invoice needs to read (ADR 0045): its terms, what it
// owes in three parts, and when it is suspended if it does not pay — the end of the grace after its
// first missed due date, not ADR 0043's seven days.
func invoicedOut(ctx context.Context, db *core.DB, account *ent.BillingAccount, out *schemas.BillingOut,
	at time.Time, policy suspensionPolicy,
) error {
	out.Collection = string(account.Collection)
	contract, err := db.Client.BillingContract.Query().
		Where(billingcontract.AccountID(account.ID)).
		Order(ent.Desc(billingcontract.FieldEffectiveFrom), ent.Desc(billingcontract.FieldID)).
		First(ctx)
	switch {
	case err == nil:
		out.Contract = &schemas.ContractOut{
			Collection: string(contract.Collection), EffectiveFrom: contract.EffectiveFrom,
			TermsDays: contract.TermsDays, OverdueGraceDays: contract.OverdueGraceDays,
			PlatformFeeMicros: contract.PlatformFeeMicros, MinimumCommitmentMicros: contract.MinimumCommitmentMicros,
			CreditLimitMicros: contract.CreditLimitMicros, PONumber: contract.PoNumber, Memo: contract.Memo,
		}
	case !ent.IsNotFound(err):
		return core.AsHTTPError(err)
	}
	if !wallet.IsInvoiced(account) && account.ReceivableMicros == 0 && account.OverdueSince == nil {
		return nil
	}

	open, err := db.Client.Invoice.Query().
		Where(invoice.AccountID(account.ID), invoice.StatusEQ(invoice.StatusOpen)).All(ctx)
	if err != nil {
		return core.AsHTTPError(err)
	}
	receivable := &schemas.ReceivableOut{OverdueSince: account.OverdueSince}
	for _, row := range open {
		receivable.OpenMicros += row.AmountRemainingMicros
		if row.DueAt != nil && row.DueAt.Before(at) {
			receivable.OverdueMicros += row.AmountRemainingMicros
		}
	}
	// Everything owed that no open invoice asks for, and pending usage credit will not cover.
	receivable.UnbilledMicros = max(0, out.Live.ReceivableMicros-receivable.OpenMicros)
	if wallet.IsInvoiced(account) && account.InvoicedThrough != nil {
		next := invoicing.MonthOf(*account.InvoicedThrough).AddDate(0, 1, 0)
		receivable.NextInvoiceAt = &next
	}
	out.Receivable = receivable
	if at := suspension.OverdueSuspendsAt(ctx, db.Client, account, policy.overdueGrace); at != nil {
		out.SuspendsAt = at
	}
	return nil
}

// resolvedEntitlements layers the account's overrides onto its plan.
//
// One enterprise customer with ten times the included MAU is a map on their account, not a plan
// nobody else is on and not a branch in code — which is where a discount that outlives its deal
// comes from.
func resolvedEntitlements(account *ent.BillingAccount) map[string]int64 {
	plan := account.Edges.Plan
	if plan == nil {
		return nil
	}
	resolved := billing.Resolve(limitsOf(plan.Entitlements), limitsOf(account.EntitlementOverrides))
	out := make(map[string]int64, len(resolved))
	for name, amount := range resolved {
		out[string(name)] = amount
	}
	return out
}

// withinPlan refuses one more of something the plan counts (projects, webhook destinations)
// when the organization already has as many as it allows. It runs inside the transaction that
// makes the new one, with the organization's row locked, so two made at once cannot both take
// the last place. An organization with no billing account, or a plan without the limit, is not
// held to one.
func withinPlan(ctx *muzak.Context, tx *ent.Tx, orgID int, limit billing.Entitlement,
	count func() (int, error), refusal string,
) error {
	if _, err := tx.Organization.Query().Where(organization.ID(orgID)).ForUpdate().Only(ctx.Context()); err != nil {
		return core.AsHTTPError(err)
	}
	account, err := tx.BillingAccount.Query().Where(billingaccount.OrganizationID(orgID)).WithPlan().Only(ctx.Context())
	switch {
	case ent.IsNotFound(err):
		return nil
	case err != nil:
		return core.AsHTTPError(err)
	}
	allowed, found := resolvedEntitlements(account)[string(limit)]
	if !found {
		return nil
	}
	have, err := count()
	if err != nil {
		return core.AsHTTPError(err)
	}
	if int64(have) >= allowed {
		return muzak.Conflict(fmt.Sprintf("%s (%d); a larger plan allows more", refusal, allowed)).
			WithCode(string(limit))
	}
	return nil
}

func limitsOf(raw map[string]int64) billing.Limits {
	out := make(billing.Limits, len(raw))
	for name, amount := range raw {
		out[billing.Entitlement(name)] = amount
	}
	return out
}

func creditLotOut(row *ent.CreditLot, at time.Time) schemas.CreditLotOut {
	out := schemas.CreditLotOut{
		ID:              row.UID,
		Kind:            string(row.Kind),
		AmountMicros:    row.AmountMicros,
		RemainingMicros: row.RemainingMicros,
		Remaining:       billing.USD(row.RemainingMicros),
		ExpiresAt:       row.ExpiresAt,
		GrantedAt:       row.GrantedAt,
		Description:     row.Description,
	}
	out.Expired = row.ExpiresAt != nil && !at.Before(*row.ExpiresAt)
	return out
}

func ledgerEntryOut(row *ent.LedgerEntry) schemas.LedgerEntryOut {
	return schemas.LedgerEntryOut{
		ID:                 row.UID,
		Kind:               string(row.Kind),
		AmountMicros:       row.AmountMicros,
		Amount:             billing.USD(row.AmountMicros),
		BalanceAfterMicros: row.BalanceAfterMicros,
		Description:        row.Description,
		At:                 row.CreatedAt,
	}
}

// orKeep leaves a stored amount alone when the caller named none.
func orKeep(chosen *int64, current int64) int64 {
	if chosen == nil {
		return current
	}
	return *chosen
}

// walletError turns a domain refusal into the status it deserves.
//
// An insufficient balance is a 409 rather than a 422: the request is well-formed and would have
// worked yesterday. A malformed one — a negative amount, an expiry on purchased credit — is a 422,
// because no amount of waiting fixes it.
func walletError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, wallet.ErrInsufficient):
		return muzak.Conflict(err.Error()).WithCode("insufficient_credit")
	case errors.Is(err, wallet.ErrNotPositive), errors.Is(err, wallet.ErrCurrencyMismatch):
		return muzak.UnprocessableEntity(err.Error()).WithCode("invalid_amount")
	case errors.Is(err, wallet.ErrBadExpiry):
		return muzak.UnprocessableEntity(err.Error()).WithCode("invalid_expiry")
	default:
		return core.AsHTTPError(err)
	}
}

// suspensionPolicy is the deployment's tolerance for an account that owes (ADR 0043).
type suspensionPolicy struct {
	grace        time.Duration
	overdueGrace time.Duration // after a missed due date, when no contract says (ADR 0045)
	limit        suspension.Policy
}

func policyOf(ctx *muzak.Context) suspensionPolicy {
	if settings, found := muzak.TryFrom[config.Settings](ctx); found {
		return suspensionPolicy{grace: settings.BillingGracePeriod, overdueGrace: settings.BillingInvoiceOverdueGrace,
			limit: suspension.Policy{Floor: settings.BillingDefaultCreditLimit, Percent: settings.BillingCreditLimitPercent}}
	}
	return suspensionPolicy{grace: 7 * 24 * time.Hour, overdueGrace: 14 * 24 * time.Hour,
		limit: suspension.Policy{Floor: 50 * billing.Micros, Percent: 20}}
}

// liveOut projects the queued usage onto the wallet, exactly as the drain will apply it.
//
// From the lots alone, skipping lapsed ones as the spend path does, so the billing page and
// /v1/users/me cannot compute the same balance two ways. For an invoiced account what credit will not
// cover goes to the receivable, as the drain will put it — owed on the next invoice, not late.
func liveOut(account *ent.BillingAccount, lots []wallet.Lot, pending int64, at time.Time) schemas.LiveBalanceOut {
	arrears, balance := account.ArrearsMicros, account.BalanceMicros
	invoiced := wallet.IsInvoiced(account)
	var promo, purchased int64
	for _, lot := range lots {
		if !lot.Spendable(at) {
			continue
		}
		switch lot.Kind {
		case wallet.Promo:
			promo += lot.Remaining
		case wallet.Purchase:
			purchased += lot.Remaining
		}
	}
	live := schemas.LiveBalanceOut{
		PendingMicros:    pending,
		BalanceMicros:    balance - pending,
		AvailableMicros:  wallet.Available(lots, at),
		PromoMicros:      promo,
		PurchasedMicros:  purchased,
		OwedMicros:       arrears,
		ReceivableMicros: account.ReceivableMicros,
	}
	switch {
	case pending > 0:
		draws, covered := wallet.Cover(lots, pending, at)
		for _, d := range draws {
			if d.Kind == wallet.Promo {
				live.PromoMicros -= d.Amount
			} else {
				live.PurchasedMicros -= d.Amount
			}
		}
		live.AvailableMicros -= covered
		if invoiced {
			live.ReceivableMicros += pending - covered
		} else {
			live.OwedMicros += pending - covered
		}
	case pending < 0:
		// A volume card's credit: it pays arrears first — or an invoiced account's receivable —
		// and the rest is purchased credit.
		credit := -pending
		if invoiced {
			paid := min(account.ReceivableMicros, credit)
			live.ReceivableMicros -= paid
			live.PurchasedMicros += credit - paid
			live.AvailableMicros += credit - paid
			break
		}
		paid := min(arrears, credit)
		live.OwedMicros -= paid
		live.PurchasedMicros += credit - paid
		live.AvailableMicros += credit - paid
	}
	live.Balance = billing.USD(live.BalanceMicros)
	live.Pending = billing.USD(live.PendingMicros)
	return live
}

// balancesOf is the live balance of several organizations at once, keyed by organization: three
// queries however many there are — accounts, their credit, their queued usage. An organization
// with no billing account is absent.
func balancesOf(ctx context.Context, db *core.DB, orgIDs []int, at time.Time) (map[int]schemas.BalanceSummaryOut, error) {
	out := make(map[int]schemas.BalanceSummaryOut, len(orgIDs))
	if len(orgIDs) == 0 {
		return out, nil
	}
	accounts, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationIDIn(orgIDs...)).All(ctx)
	if err != nil || len(accounts) == 0 {
		return out, err
	}
	ids := make([]int, len(accounts))
	for i, account := range accounts {
		ids[i] = account.ID
	}
	lots, err := wallet.LotsOfAccounts(ctx, db.Client.CreditLot, ids)
	if err != nil {
		return nil, err
	}
	var sums []struct {
		AccountID int   `json:"account_id"`
		Sum       int64 `json:"sum"`
	}
	if err := db.Client.UsageCharge.Query().
		Where(usagecharge.AccountIDIn(ids...), usagecharge.SettledAtIsNil()).
		GroupBy(usagecharge.FieldAccountID).
		Aggregate(ent.Sum(usagecharge.FieldAmountMicros)).
		Scan(ctx, &sums); err != nil {
		return nil, fmt.Errorf("sum pending usage: %w", err)
	}
	pending := make(map[int]int64, len(sums))
	for _, row := range sums {
		pending[row.AccountID] = row.Sum
	}

	for _, account := range accounts {
		live := liveOut(account, lots[account.ID], pending[account.ID], at)
		out[account.OrganizationID] = schemas.BalanceSummaryOut{
			State:            string(account.State),
			Currency:         account.Currency,
			AvailableMicros:  live.AvailableMicros,
			Available:        billing.USD(live.AvailableMicros),
			OwedMicros:       live.OwedMicros,
			Owed:             billing.USD(live.OwedMicros),
			PendingMicros:    live.PendingMicros,
			Collection:       string(account.Collection),
			ReceivableMicros: live.ReceivableMicros,
		}
	}
	return out, nil
}
