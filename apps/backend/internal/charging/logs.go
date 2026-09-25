package charging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/ratecard"
	"playersense.dev/backend/ent/usagecharge"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/logs"
	"playersense.dev/backend/internal/pgdb"
)

// Cloud Logging's charges (ADR 0050): the megabytes an organization takes in and the gigabyte-days
// it keeps, priced by their own rate cards and queued as usage charges the drain settles like any
// other. The players' accrual prices each player as it arrives; logs are priced by a pass, every
// few minutes for ingestion and once a day for storage, because a byte is not a thing that arrives
// once and a gigabyte-day is only known when the day is over.
//
// Both meters count forward from a stored position, under the month's lock, so a pass that runs
// twice, or two passes at once, price nothing twice: the second finds the position already moved.

// LogPrices is the deployment's list price and free tier, for a plan that has not stated its own.
type LogPrices struct {
	IngestPerGBMicros       int64
	StoragePerGBMonthMicros int64
	FreeIngestGB            int64
	FreeStorageGBMonths     int64
}

// bytesPerMB and bytesPerGB are decimal, as a storage bill's units are.
const (
	bytesPerMB = 1_000_000
	bytesPerGB = 1_000_000_000
)

// storageDaysInPrice is the month a "per GB-month" list price is divided by to give a gigabyte-day.
const storageDaysInPrice = 30

// LogAccruer prices Cloud Logging's two meters.
type LogAccruer struct {
	DB     *pgdb.DB
	CH     interface{ DB() (driver.Conn, error) }
	Prices LogPrices
	Log    *slog.Logger
	Now    func() time.Time

	// storedThrough remembers the last day this process accrued storage for every organization, so
	// the passes between midnights skip the day's query entirely.
	storedThrough time.Time
}

// LogAccrual is what one pass did.
type LogAccrual struct {
	Organizations int
	IngestMicros  int64
	StorageMicros int64
}

// lookBack is how many finished days a storage pass considers, so a worker that was down for a
// few days catches up rather than skipping them.
const lookBack = 7

// Run prices what has been taken in this month and, once a day, what was kept the day before.
func (a *LogAccruer) Run(ctx context.Context) (LogAccrual, error) {
	var out LogAccrual
	conn, err := a.CH.DB()
	if err != nil {
		return out, err
	}
	now := a.now()
	period := PeriodOf(now)

	// Ingestion: every organization's megabytes this month so far, the current hour included.
	ingested, err := logs.IngestedSince(ctx, conn, period, now.Truncate(time.Hour).Add(time.Hour))
	if err != nil {
		return out, err
	}
	for org, bytes := range ingested {
		micros, err := a.accrue(ctx, int(org), period, now, billing.LogIngest, func(u *ent.MonthlyUsage) (int64, int64, func(*ent.MonthlyUsageUpdateOne)) {
			to := int64(bytes / bytesPerMB)
			return u.LogIngestMB, to, func(up *ent.MonthlyUsageUpdateOne) { up.SetLogIngestMB(to) }
		})
		if err != nil {
			return out, fmt.Errorf("log ingestion for organization %d: %w", org, err)
		}
		out.IngestMicros += micros
		out.Organizations++
	}

	// Storage: each finished day, oldest first, that this process has not already done.
	today := now.Truncate(24 * time.Hour)
	for day := today.AddDate(0, 0, -lookBack); day.Before(today); day = day.AddDate(0, 0, 1) {
		if !day.After(a.storedThrough) && !a.storedThrough.IsZero() {
			continue
		}
		micros, err := a.accrueStorageDay(ctx, conn, day)
		if err != nil {
			return out, err
		}
		out.StorageMicros += micros
		a.storedThrough = day
	}
	return out, nil
}

// accrueStorageDay prices one day's kept bytes for every organization that kept any.
func (a *LogAccruer) accrueStorageDay(ctx context.Context, conn driver.Conn, day time.Time) (int64, error) {
	projects, err := a.DB.Client.Project.Query().
		Select(project.FieldID, project.FieldLogRetentionDays).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("read projects' log retention: %w", err)
	}
	retention := make(map[uint32]uint16, len(projects))
	for _, p := range projects {
		retention[uint32(p.ID)] = p.LogRetentionDays
	}
	usage, err := logs.DayUsage(ctx, conn, day.AddDate(0, 0, 1), func(seq uint32) (uint16, bool) {
		days, found := retention[seq]
		return days, found
	})
	if err != nil {
		return 0, err
	}
	kept := map[uint32]uint64{}
	for _, u := range usage {
		kept[u.OrgSeq] += u.Kept
	}

	period := PeriodOf(day)
	var total int64
	for org, bytes := range kept {
		if bytes == 0 {
			continue
		}
		micros, err := a.accrue(ctx, int(org), period, day, billing.LogStorage, func(u *ent.MonthlyUsage) (int64, int64, func(*ent.MonthlyUsageUpdateOne)) {
			// A day already accrued, by this process before a restart or by another, moves nothing.
			if u.LogStorageThrough != nil && !u.LogStorageThrough.Before(day) {
				return u.LogStorageGBDays, u.LogStorageGBDays, nil
			}
			byteDays := u.LogByteDays + int64(bytes)
			to := byteDays / bytesPerGB
			return u.LogStorageGBDays, to, func(up *ent.MonthlyUsageUpdateOne) {
				up.SetLogByteDays(byteDays).SetLogStorageGBDays(to).SetLogStorageThrough(day)
			}
		})
		if err != nil {
			return total, fmt.Errorf("log storage for organization %d on %s: %w", org, day.Format(time.DateOnly), err)
		}
		total += micros
	}
	return total, nil
}

// advance reads the month's position for a meter and says where it moves to, and how to record it.
// A nil record means nothing moves.
type advance func(*ent.MonthlyUsage) (from, to int64, record func(*ent.MonthlyUsageUpdateOne))

// accrue moves one organization's meter for one month and queues what the move costs.
func (a *LogAccruer) accrue(ctx context.Context, orgSeq int, period, at time.Time, meter billing.Meter, step advance) (int64, error) {
	account, err := a.DB.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(orgSeq)).WithPlan().Only(ctx)
	if ent.IsNotFound(err) {
		// No account, no bill: an organization that has never been set up for billing is the
		// operator's to notice, as the players' accrual counts it Unbilled.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read the billing account: %w", err)
	}
	card, err := a.card(ctx, account.Edges.Plan, meter, at)
	if err != nil {
		return 0, err
	}
	included := a.Prices.Included(account, meter, period)

	tx, err := a.DB.Client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	amount, err := a.move(ctx, tx, account, card, period, included, step)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	return amount, tx.Commit()
}

func (a *LogAccruer) move(ctx context.Context, tx *ent.Tx, account *ent.BillingAccount, card *ent.RateCard, period time.Time, included int64, step advance) (int64, error) {
	if err := tx.MonthlyUsage.Create().
		SetAccountID(account.ID).SetPeriodStart(period).
		OnConflictColumns(monthlyusage.FieldAccountID, monthlyusage.FieldPeriodStart).
		DoNothing().Exec(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("open the month: %w", err)
	}
	usage, err := tx.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(account.ID), monthlyusage.PeriodStart(period)).
		ForUpdate().Only(ctx)
	if err != nil {
		return 0, fmt.Errorf("lock the month: %w", err)
	}
	from, to, record := step(usage)
	if record == nil || to < from {
		return 0, nil
	}
	amount := billing.Card{Mode: billing.Mode(card.Mode), Tiers: card.Tiers}.Charge(from, to, included)

	update := tx.MonthlyUsage.UpdateOne(usage).AddChargedMicros(amount).AddLogChargedMicros(amount)
	record(update)
	if err := update.Exec(ctx); err != nil {
		return 0, fmt.Errorf("advance the month: %w", err)
	}
	if amount == 0 || to == from {
		return 0, nil
	}

	// One open row per account, month and card, extended while it waits (ADR 0044).
	extended, err := tx.UsageCharge.Update().
		Where(
			usagecharge.AccountID(account.ID),
			usagecharge.PeriodStart(period),
			usagecharge.RateCardID(card.ID),
			usagecharge.SettledAtIsNil(),
			usagecharge.LastUnit(from),
		).
		SetLastUnit(to).
		AddAmountMicros(amount).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("extend the open charge: %w", err)
	}
	if extended == 0 {
		if err := tx.UsageCharge.Create().
			SetAccountID(account.ID).
			SetPeriodStart(period).
			SetRateCardID(card.ID).
			SetFirstUnit(from + 1).
			SetLastUnit(to).
			SetAmountMicros(amount).
			SetCurrency(card.Currency).
			Exec(ctx); err != nil {
			return 0, fmt.Errorf("queue the charge: %w", err)
		}
	}
	return amount, nil
}

// Included is the free tier, in the meter's units: the plan's (or the account's override), or the
// deployment's when the plan states none. The account must carry its plan.
func (p LogPrices) Included(account *ent.BillingAccount, meter billing.Meter, period time.Time) int64 {
	limits := billing.Limits{}
	if account.Edges.Plan != nil {
		for name, amount := range account.Edges.Plan.Entitlements {
			limits[billing.Entitlement(name)] = amount
		}
	}
	overrides := billing.Limits{}
	for name, amount := range account.EntitlementOverrides {
		overrides[billing.Entitlement(name)] = amount
	}
	resolved := billing.Resolve(limits, overrides)
	switch meter {
	case billing.LogIngest:
		gb, stated := resolved.Get(billing.IncludedLogIngestGB)
		if !stated {
			gb = p.FreeIngestGB
		}
		return gb * (bytesPerGB / bytesPerMB)
	default:
		months, stated := resolved.Get(billing.IncludedLogStorageGBMonth)
		if !stated {
			months = p.FreeStorageGBMonths
		}
		// A GB-month is the month's own days, so a gigabyte kept all month is free in February
		// and in March alike.
		return months * int64(DaysIn(period))
	}
}

// DaysIn is how many days a month has.
func DaysIn(period time.Time) int {
	return int(period.AddDate(0, 1, 0).Sub(period).Hours() / 24)
}

// card is the plan's rate card for a meter in force at a moment. A plan with none gets one: the
// deployment's list price, published as a card effective from the beginning of time, so every
// charge still names the card that priced it and a later card from staff replaces it in the
// ordinary way.
func (a *LogAccruer) card(ctx context.Context, p *ent.Plan, meter billing.Meter, at time.Time) (*ent.RateCard, error) {
	if p == nil {
		return nil, fmt.Errorf("the billing account has no plan")
	}
	find := func() (*ent.RateCard, error) {
		return a.DB.Client.RateCard.Query().
			Where(ratecard.PlanID(p.ID), ratecard.MeterEQ(ratecard.Meter(meter)), ratecard.EffectiveFromLTE(at)).
			Order(ent.Desc(ratecard.FieldEffectiveFrom)).
			First(ctx)
	}
	found, err := find()
	if err == nil || !ent.IsNotFound(err) {
		return found, err
	}
	unit := a.Prices.ListUnit(meter)
	note := "Deployment list price: a gigabyte taken in"
	if meter == billing.LogStorage {
		note = "Deployment list price: a gigabyte kept a day"
	}
	err = a.DB.Client.RateCard.Create().
		SetPlanID(p.ID).
		SetMeter(ratecard.Meter(meter)).
		SetMode(ratecard.ModeGraduated).
		SetCurrency(p.Currency).
		SetTiers(billing.Tiers{{UnitPriceMicros: unit}}).
		SetEffectiveFrom(time.Unix(0, 0).UTC()).
		SetNote(note).
		SetCreatedBy("system").
		Exec(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return nil, fmt.Errorf("publish the list price for %s: %w", meter, err)
	}
	return find()
}

// ListUnit is the deployment's price for one unit of a meter, in micros.
func (p LogPrices) ListUnit(meter billing.Meter) int64 {
	if meter == billing.LogStorage {
		return p.StoragePerGBMonthMicros / storageDaysInPrice
	}
	return p.IngestPerGBMicros / (bytesPerGB / bytesPerMB)
}

// UnitPrice is what one unit of a meter costs a plan at a moment: its card's first band, or the
// list price when it has no card yet.
func (p LogPrices) UnitPrice(ctx context.Context, db *pgdb.DB, planID int, meter billing.Meter, at time.Time) (int64, error) {
	card, err := db.Client.RateCard.Query().
		Where(ratecard.PlanID(planID), ratecard.MeterEQ(ratecard.Meter(meter)), ratecard.EffectiveFromLTE(at)).
		Order(ent.Desc(ratecard.FieldEffectiveFrom)).
		First(ctx)
	switch {
	case ent.IsNotFound(err):
		return p.ListUnit(meter), nil
	case err != nil:
		return 0, err
	case len(card.Tiers) == 0:
		return 0, nil
	default:
		return card.Tiers[0].UnitPriceMicros, nil
	}
}

// Units converts bytes into a meter's units, for a page to say how far through its allowance an
// organization is.
const (
	BytesPerMB = bytesPerMB
	BytesPerGB = bytesPerGB
)

func (a *LogAccruer) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}
