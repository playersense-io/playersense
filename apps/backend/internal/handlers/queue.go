package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/schemas"
)

// OperatorQueue reads how far behind each writer is: every consumer group's unconsumed batches,
// the oldest one's age, and what it parked. One group that cannot be read says so beside the others.
func OperatorQueue(ctx *muzak.Context, in schemas.QueueParams) (schemas.QueueOut, error) {
	reader := muzak.From[eventbus.BacklogReader](ctx)
	now := time.Now().UTC()
	out := schemas.QueueOut{Bus: reader.Name(), CheckedAt: now}
	window := time.Duration(in.Hours) * time.Hour
	for _, group := range []string{eventbus.GroupAnalyticsWriter, eventbus.GroupArchiveWriter, eventbus.GroupBillingWriter} {
		backlog, err := reader.Backlog(ctx.Context(), group, window)
		one := schemas.QueueGroupOut{
			Group: group, Messages: backlog.Messages, Bytes: backlog.Bytes,
			OldestUnread: backlog.OldestUnread, DeadLettered: backlog.DeadLettered,
		}
		if err != nil {
			one.Error = err.Error()
		}
		if backlog.OldestUnread != nil {
			one.LagSeconds = int64(now.Sub(*backlog.OldestUnread).Seconds())
		}
		for _, p := range backlog.Parked {
			one.Parked = append(one.Parked, schemas.ParkedBatchOut{
				Reason: p.Reason, FailedAt: p.FailedAt, Attempts: p.Attempts,
				DedupKey: p.DedupKey, ProjectID: p.ProjectID, EventCount: p.EventCount,
			})
		}
		out.Groups = append(out.Groups, one)
	}
	return out, nil
}
