package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	environmentpkg "playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/monthlyusage"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/charging"
	"playersense.dev/backend/internal/chdb"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/logs"
	"playersense.dev/backend/internal/schemas"
)

// logPrices is the deployment's list price and free tier for Cloud Logging.
func logPrices(ctx *muzak.Context) charging.LogPrices {
	settings := muzak.From[config.Settings](ctx)
	return charging.LogPrices{
		IngestPerGBMicros:       settings.LogIngestPricePerGBMicros,
		StoragePerGBMonthMicros: settings.LogStoragePricePerGBMonthMicros,
		FreeIngestGB:            settings.LogFreeIngestGB,
		FreeStorageGBMonths:     settings.LogFreeStorageGBMonths,
	}
}

// LogStorage is a project's log storage: what it keeps, for how long, and what logs cost the
// organization this month.
func LogStorage(ctx *muzak.Context, _ schemas.LogStorageParams) (schemas.LogStorageOut, error) {
	return logStorageOut(ctx, muzak.From[*core.Principal](ctx).Project)
}

func logStorageOut(ctx *muzak.Context, proj *ent.Project) (schemas.LogStorageOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	now := nowUTC()
	out := schemas.LogStorageOut{
		RetentionDays: logRetention(proj), RetentionChoices: schemas.LogRetentionChoices,
		RetentionChangedAt: proj.LogRetentionChangedAt, Environments: []schemas.LogEnvBytesOut{},
		Ingested: []schemas.LogPointOut{},
	}
	if proj.LogRetentionPurgeUntil != nil && proj.LogRetentionPurgeUntil.After(now) {
		out.RetentionPurgeUntil = proj.LogRetentionPurgeUntil
	}

	conn, err := muzak.From[*chdb.Conn](ctx).DB()
	if err != nil {
		return out, logsUnavailable(err)
	}
	kept, err := logs.Kept(ctx.Context(), conn, uint32(proj.ID), logRetention(proj), now)
	if err != nil {
		return out, logsUnavailable(err)
	}
	envs, err := db.Client.Environment.Query().Where(environmentpkg.ProjectID(proj.ID)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	for _, env := range envs {
		bytes := kept[uint16(env.ID)]
		out.KeptBytes += bytes
		out.Environments = append(out.Environments, schemas.LogEnvBytesOut{Environment: string(env.Kind), Bytes: bytes})
	}
	days, err := logs.Ingested(ctx.Context(), conn, uint32(proj.ID), now.Truncate(24*time.Hour).AddDate(0, 0, -29))
	if err != nil {
		return out, logsUnavailable(err)
	}
	for _, d := range days {
		out.Ingested = append(out.Ingested, schemas.LogPointOut{At: d.At, Value: d.Value})
	}

	// The organization's month, which is what the two meters bill.
	period := charging.PeriodOf(now)
	out.PeriodStart = period
	orgSeq := principal.Organization.ID
	ingested, err := logs.IngestedSince(ctx.Context(), conn, period, now.Truncate(time.Hour).Add(time.Hour))
	if err != nil {
		return out, logsUnavailable(err)
	}
	out.IngestedBytes = ingested[uint32(orgSeq)]

	prices := logPrices(ctx)
	account, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(orgSeq)).WithPlan().Only(ctx.Context())
	switch {
	case ent.IsNotFound(err):
		// No account: the list price and the free tier, so the page still says what logs cost.
		out.IncludedIngestBytes = uint64(prices.FreeIngestGB * charging.BytesPerGB)
		out.IncludedGBMonths = float64(prices.FreeStorageGBMonths)
		out.IngestPricePerGBMicros = prices.ListUnit(billing.LogIngest) * (charging.BytesPerGB / charging.BytesPerMB)
		out.StoragePricePerGBMonthMicros = prices.ListUnit(billing.LogStorage) * 30
		return out, nil
	case err != nil:
		return out, core.AsHTTPError(err)
	}

	daysIn := charging.DaysIn(period)
	out.IncludedIngestBytes = uint64(prices.Included(account, billing.LogIngest, period) * charging.BytesPerMB)
	out.IncludedGBMonths = float64(prices.Included(account, billing.LogStorage, period)) / float64(daysIn)
	ingestUnit, err := prices.UnitPrice(ctx.Context(), db, account.PlanID, billing.LogIngest, now)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	storageUnit, err := prices.UnitPrice(ctx.Context(), db, account.PlanID, billing.LogStorage, now)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	out.IngestPricePerGBMicros = ingestUnit * (charging.BytesPerGB / charging.BytesPerMB)
	out.StoragePricePerGBMonthMicros = storageUnit * 30

	usage, err := db.Client.MonthlyUsage.Query().
		Where(monthlyusage.AccountID(account.ID), monthlyusage.PeriodStart(period)).Only(ctx.Context())
	if err != nil && !ent.IsNotFound(err) {
		return out, core.AsHTTPError(err)
	}
	var byteDays int64
	if usage != nil {
		byteDays = usage.LogByteDays
		out.ChargedMicros = usage.LogChargedMicros
	}
	out.GBDays = float64(byteDays) / charging.BytesPerGB
	out.GBMonths = out.GBDays / float64(daysIn)

	// The month at its rate so far: the bytes taken in and the GB-days kept, each carried to the
	// month's end at the pace they have run, less the free tier, at the first band's price.
	elapsed := now.Sub(period).Hours() / 24
	if elapsed > 0 {
		scale := float64(daysIn) / elapsed
		ingestMB := float64(out.IngestedBytes) / charging.BytesPerMB * scale
		freeMB := float64(out.IncludedIngestBytes) / charging.BytesPerMB
		storedDays := max(elapsed-1, 1)
		gbDays := out.GBDays * float64(daysIn) / storedDays
		freeGBDays := out.IncludedGBMonths * float64(daysIn)
		out.ProjectedMicros = int64(max(ingestMB-freeMB, 0)*float64(ingestUnit) +
			max(gbDays-freeGBDays, 0)*float64(storageUnit))
	}
	return out, nil
}

// SetLogRetention changes how long a project keeps its logs.
//
// Longer applies to records from now on: what was stamped with the old retention expires on its
// own schedule. Shorter applies to everything: the worker deletes older records over the next
// days (log_retention_purge_until), and the pages and the bill stop counting them at once.
func SetLogRetention(ctx *muzak.Context, in schemas.SetLogRetentionIn) (schemas.LogStorageOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	was := logRetention(proj)
	if in.Days == was {
		return logStorageOut(ctx, proj)
	}
	now := nowUTC()
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Project, error) {
		update := tx.Project.UpdateOneID(proj.ID).SetLogRetentionDays(in.Days).SetLogRetentionChangedAt(now)
		if in.Days < was {
			update = update.SetLogRetentionPurgeUntil(now.AddDate(0, 0, int(was))).ClearLogRetentionPurgedAt()
		} else {
			update = update.ClearLogRetentionPurgeUntil()
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.LogRetentionSet, "project", proj.Slug,
			map[string]any{"log_retention_days": in.Days, "was_log_retention_days": was})
	})
	if err != nil {
		return schemas.LogStorageOut{}, err
	}
	return logStorageOut(ctx, updated)
}
