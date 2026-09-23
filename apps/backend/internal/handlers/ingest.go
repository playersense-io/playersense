package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/eventbus"
	"playersense.dev/backend/internal/ingest"
	"playersense.dev/backend/internal/protocol"
	"playersense.dev/backend/internal/ratelimit"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/suspension"
)

// AcceptBatch is the ingestion endpoint (§19).
//
// It does the cheap things and nothing else: authenticate, bound, normalize, publish,
// answer. It never queries PostgreSQL — the tenant was resolved from a cached key before
// binding — and it never touches ClickHouse, so neither being unavailable stops a batch
// being accepted (§10.2, §64).
//
// The 202 means exactly one thing: the batch reached durable queueing (Appendix C.1).
func AcceptBatch(ctx *muzak.Context, in schemas.IngestBatchIn) (protocol.Accepted, error) {
	tenant := muzak.From[*core.Tenant](ctx)
	bus := muzak.From[eventbus.Bus](ctx)
	limits := muzak.From[ingest.Limits](ctx)

	// An account past its grace period or credit limit sends no more data until it pays (ADR
	// 0043). 429 with Retry-After, because that is the answer an SDK keeps the batch for and
	// retries later (protocol §9) — so nothing is lost while the studio pays. A map lookup, and
	// a Valkey round trip per organization every few seconds; never PostgreSQL.
	if gate, found := muzak.TryFrom[*suspension.Gate](ctx); found && gate.Suspended(ctx.Context(), tenant.OrgSeq) {
		ctx.SetHeader(muzak.HeaderRetryAfter, strconv.Itoa(int(suspension.RetryAfter.Seconds())))
		return protocol.Accepted{}, muzak.TooManyRequests(
			"this organization's ingestion is suspended until its balance is paid; " +
				"the batch was not stored — keep it and retry after Retry-After").
			WithCode("payment_required")
	}

	// Counted here rather than by the framework, because this is the first point at which
	// the caller is known by name: the project from the credential, the installation from
	// the body. Before binding there is only an address, and an address behind carrier NAT
	// is tens of thousands of players (docs/rate-limits.md).
	if limiter, found := muzak.TryFrom[*ratelimit.Limiter](ctx); found {
		if err := limiter.Allow(ctx,
			core.IngestBudgets(tenant, in.InstallID, core.ClientAddressOf(ctx))...); err != nil {
			return protocol.Accepted{}, err
		}
	}

	// The key class was already enforced by the provider: only a project key reaches
	// here (§52).
	if in.SchemaVersion != protocol.SchemaVersion {
		return protocol.Accepted{}, muzak.BadRequest("unsupported schema_version").
			WithCode("unsupported_schema_version")
	}

	// The environment comes from the key, never from the body. A key is scoped to one
	// environment (§76), so a batch claiming another is either a mistake or an attempt,
	// and either way the key is the authority.
	if in.Environment != "" && in.Environment != tenant.Environment {
		return protocol.Accepted{}, muzak.Forbidden(
			"this key is scoped to the " + tenant.Environment + " environment")
	}

	now := time.Now().UTC()

	// §97: consent is the basis for storing anything. Without it the batch is accepted
	// and discarded rather than refused — an SDK that retries a rejected batch forever
	// is worse for everyone than one told plainly that nothing was stored.
	if !in.Consent.Analytics {
		return protocol.Accepted{
			BatchID: in.BatchID,
			Dropped: len(in.Events),
			Reasons: []string{"analytics consent was not granted, so nothing was stored"},
		}, nil
	}

	batch := in.Batch

	// §98: a kids_strict project has no persistent identifier at all. Enforcing that
	// here rather than trusting the SDK is the difference between a policy and a
	// promise: the gateway replaces the installation identifier with a value scoped to
	// this batch, so nothing downstream can link the same device across two days even if
	// the client sent something stable.
	if tenant.PolicyTier == "kids_strict" {
		batch.InstallID = uuid.NewString()
	}

	result, err := ingest.Normalize(&batch, tenant, ctx.ClientIP(), now, limits)
	if err != nil {
		return protocol.Accepted{}, muzak.BadRequest(err.Error()).WithCode("batch_rejected")
	}

	accepted := len(result.Batch.Events)
	if accepted == 0 {
		// Nothing survived. There is nothing to publish, and saying so is more useful
		// than a 202 that implies something was stored.
		return protocol.Accepted{
			BatchID: in.BatchID,
			Dropped: result.Dropped,
			Reasons: result.Reasons,
		}, nil
	}

	payload, err := json.Marshal(result.Batch)
	if err != nil {
		return protocol.Accepted{}, err
	}

	msg := &eventbus.Message{
		PublishedAt:    now,
		DedupKey:       dedupKey(tenant, in.BatchID),
		OrgSeq:         tenant.OrgSeq,
		ProjectSeq:     tenant.ProjectSeq,
		EnvironmentSeq: tenant.EnvironmentSeq,
		OrgID:          tenant.OrgID,
		ProjectID:      tenant.ProjectID,
		EnvironmentID:  tenant.EnvironmentID,
		Environment:    tenant.Environment,
		PolicyTier:     tenant.PolicyTier,
		RetentionTier:  tenant.RetentionTier,
		EventCount:     accepted,
		Payload:        payload,
	}

	// Publish returns only once the message is durable, which is what lets the status
	// below mean what §19 says it means.
	if err := bus.Publish(ctx.Context(), msg); err != nil {
		// The queue is the one dependency ingestion genuinely cannot do without. A 503
		// is honest and an SDK will retry it with backoff, which is exactly the
		// behaviour §18's local spool exists to provide.
		return protocol.Accepted{}, muzak.ServiceUnavailable(
			"the batch could not be queued; retry with backoff").Wrap(err)
	}

	return protocol.Accepted{
		BatchID:  in.BatchID,
		Accepted: accepted,
		Dropped:  result.Dropped,
		Reasons:  result.Reasons,
	}, nil
}

// dedupKey is stable across retries of the same batch (§21).
//
// Delivery is at-least-once and a client retries on its own, so the writer needs a token
// that identifies the batch rather than the delivery. It is scoped by tenant so two
// projects cannot collide on a client-chosen identifier.
func dedupKey(tenant *core.Tenant, batchID string) string {
	sum := sha256.Sum256([]byte(tenant.ProjectID + "/" +
		tenant.EnvironmentID + "/" + batchID))
	return hex.EncodeToString(sum[:16])
}
