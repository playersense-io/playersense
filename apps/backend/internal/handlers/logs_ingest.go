package handlers

import (
	"strconv"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/logs"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/suspension"
)

// AcceptLogs is Cloud Logging's ingestion endpoint (ADR 0050).
//
// AcceptBatch's shape, step for step, onto the logs topic: authenticate, bound, normalize, publish,
// answer. It never touches PostgreSQL or ClickHouse, and the 202 means the batch reached durable
// queueing.
func AcceptLogs(ctx *muzak.Context, in schemas.IngestLogsIn) (protocol.LogsAccepted, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	queue := muzak.From[*logs.Queue](ctx)
	limits := muzak.From[ingest.LogLimits](ctx)

	// A suspended account sends no logs either (ADR 0043): they are billed storage.
	if gate, found := muzak.TryFrom[*suspension.Gate](ctx); found && gate.Suspended(ctx.Context(), tenant.OrgSeq) {
		ctx.SetHeader(muzak.HeaderRetryAfter, strconv.Itoa(int(suspension.RetryAfter.Seconds())))
		return protocol.LogsAccepted{}, muzak.TooManyRequests(
			"this organization's ingestion is suspended until its balance is paid; " +
				"the batch was not stored; keep it and retry after Retry-After").
			WithCode("payment_required")
	}

	if limiter, found := muzak.TryFrom[*ratelimit.Limiter](ctx); found {
		if err := limiter.Allow(ctx,
			core.LogBudgets(tenant, in.InstallID, core.ClientAddressOf(ctx))...); err != nil {
			return protocol.LogsAccepted{}, err
		}
	}

	if in.SchemaVersion != protocol.SchemaVersion {
		return protocol.LogsAccepted{}, muzak.BadRequest("unsupported schema_version").
			WithCode("unsupported_schema_version")
	}
	if in.Environment != "" && in.Environment != tenant.Environment {
		return protocol.LogsAccepted{}, muzak.Forbidden(
			"this key is scoped to the " + tenant.Environment + " environment")
	}

	// §97 holds for diagnostics as for analytics: the consent the SDK already carries is the
	// basis, and without it the batch is accepted and discarded rather than refused. Either
	// answer is a basis for error reports: the analytics one, or the player's separate "share
	// error reports" answer (§15.9).
	if !in.Consent.Analytics && !in.Consent.Diagnostics {
		return protocol.LogsAccepted{
			BatchID: in.BatchID,
			Dropped: len(in.Records),
			Reasons: []string{"neither analytics nor error-report consent was granted, so nothing was stored"},
		}, nil
	}

	batch := in.LogBatch
	if tenant.PolicyTier == "kids_strict" {
		batch.InstallID = uuid.NewString()
	}

	now := time.Now().UTC()
	result, err := ingest.NormalizeLogs(&batch, tenant, ctx.ClientIP(), now, limits)
	if err != nil {
		return protocol.LogsAccepted{}, muzak.BadRequest(err.Error()).WithCode("batch_rejected")
	}
	accepted := len(result.Batch.Records)
	if accepted == 0 {
		return protocol.LogsAccepted{BatchID: in.BatchID, Dropped: result.Dropped, Reasons: result.Reasons}, nil
	}

	payload, err := logs.EncodeBatch(result.Batch)
	if err != nil {
		return protocol.LogsAccepted{}, err
	}
	msg := &eventbus.Message{
		PublishedAt:    now,
		DedupKey:       dedupKey(tenant, "logs/"+in.BatchID),
		OrgSeq:         tenant.OrgSeq,
		ProjectSeq:     tenant.ProjectSeq,
		EnvironmentSeq: tenant.EnvironmentSeq,
		OrgID:          tenant.OrgID,
		ProjectID:      tenant.ProjectID,
		EnvironmentID:  tenant.EnvironmentID,
		Environment:    tenant.Environment,
		PolicyTier:     tenant.PolicyTier,
		// On the logs topic this is the log retention, which is what the writer stamps.
		RetentionTier: tenant.LogRetention(),
		EventCount:    accepted,
		Payload:       payload,
	}
	if err := queue.Publish(ctx.Context(), msg); err != nil {
		return protocol.LogsAccepted{}, muzak.ServiceUnavailable(
			"the batch could not be queued; retry with backoff").Wrap(err)
	}
	return protocol.LogsAccepted{
		BatchID:  in.BatchID,
		Accepted: accepted,
		Dropped:  result.Dropped,
		Reasons:  result.Reasons,
	}, nil
}
