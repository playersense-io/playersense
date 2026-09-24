package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/stripeevent"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ops"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
)

// OperatorOverview is the console's front page: the pipeline, the node, the queue, the worker and,
// for staff who handle money, the accounts that need somebody. Each area is read on its own, so
// one that cannot be read is named in errors while the rest still answer.
func OperatorOverview(ctx *muzak.Context, _ struct{}) (schemas.OverviewOut, error) {
	console := muzak.From[*ops.Console](ctx)
	db := muzak.From[*core.DB](ctx)
	me := muzak.From[*core.StaffPrincipal](ctx)
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	out := schemas.OverviewOut{CheckedAt: now, Errors: map[string]string{}}

	if ingestion, err := console.Ingestion(ctx.Context(), today, today, "hour", now); err != nil {
		out.Errors["ingestion"] = err.Error()
	} else {
		one := &schemas.OverviewIngestionOut{Now: ingestion.Now}
		for _, b := range ingestion.Series {
			one.EventsToday += b.Events
			one.BytesToday += b.Bytes
			one.LateToday += b.LateEvents
		}
		out.Ingestion = one
	}

	if health, err := console.Health(ctx.Context(), now); err != nil {
		out.Errors["clickhouse"] = err.Error()
	} else {
		one := &schemas.OverviewClickHouseOut{Version: health.Version, UnfinishedMutations: len(health.Mutations)}
		for _, r := range health.Replicas {
			if r.ReadOnly || r.SessionExpired {
				one.ReadOnlyReplicas++
			}
			one.MaxReplicationDelayS = max(one.MaxReplicationDelayS, r.AbsoluteDelay)
			one.ReplicationQueue += uint64(r.QueueSize)
		}
		for _, m := range health.Mutations {
			if m.LatestFailed != "" {
				one.FailingMutations++
			}
		}
		for _, h := range health.Hotspots {
			one.MostPartsInPartition = max(one.MostPartsInPartition, h.Parts)
		}
		for _, e := range health.Errors {
			one.ErrorsToday += e.Count
		}
		for _, d := range health.Disks {
			if d.Total > 0 {
				one.FullestDiskRatio = max(one.FullestDiskRatio, float64(d.Total-d.Free)/float64(d.Total))
			}
		}
		out.ClickHouse = one
	}

	if reader, found := muzak.TryFrom[eventbus.BacklogReader](ctx); found {
		for _, group := range []string{eventbus.GroupAnalyticsWriter, eventbus.GroupArchiveWriter, eventbus.GroupBillingWriter} {
			backlog, err := reader.Backlog(ctx.Context(), group, 24*time.Hour)
			one := schemas.QueueGroupOut{Group: group, Messages: backlog.Messages, Bytes: backlog.Bytes,
				OldestUnread: backlog.OldestUnread, DeadLettered: backlog.DeadLettered}
			if err != nil {
				one.Error = err.Error()
			}
			if backlog.OldestUnread != nil {
				one.LagSeconds = int64(now.Sub(*backlog.OldestUnread).Seconds())
			}
			out.Queue = append(out.Queue, one)
		}
	}

	if summaries, _, err := console.Jobs(ctx.Context(), today.AddDate(0, 0, -1), today, "", true); err != nil {
		out.Errors["jobs"] = err.Error()
	} else {
		one := &schemas.OverviewJobsOut{Latest: summaries, FailingNow: []string{}, NotRun: []string{}}
		for _, j := range summaries {
			if j.LastRunAt == nil || now.Sub(*j.LastRunAt) > 25*time.Hour {
				one.NotRun = append(one.NotRun, j.Job)
				continue
			}
			if now.Sub(*j.LastRunAt) <= 24*time.Hour && !j.LastOK {
				one.FailingNow = append(one.FailingNow, j.Job)
			}
			one.Failures += j.Failures
		}
		if erasures, err := erasureQueue(ctx.Context(), db, now); err == nil {
			one.ErasuresOpen, one.ErasuresLate = len(erasures.Open), erasures.Overdue
		}
		if archive, err := archiveQueue(ctx.Context(), db, now); err == nil {
			one.ArchiveFailed = archive.Failed
		}
		out.Jobs = one
	}

	if me.HasAny(staff.Billing, staff.Support) {
		one := &schemas.OverviewBillingOut{}
		var err error
		if one.AccountsNeedingAttention, err = db.Client.BillingAccount.Query().
			Where(needsAttention(now)).Count(ctx.Context()); err == nil {
			one.Suspended, err = db.Client.BillingAccount.Query().
				Where(billingaccount.StateEQ(billingaccount.StateSuspended)).Count(ctx.Context())
		}
		if err == nil {
			one.StripeEventsFailed, err = db.Client.StripeEvent.Query().
				Where(stripeevent.StatusEQ(stripeevent.StatusFailed)).Count(ctx.Context())
		}
		if err != nil {
			out.Errors["billing"] = err.Error()
		} else {
			out.Billing = one
		}
	}
	return out, nil
}
