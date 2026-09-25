package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Ingest returns the SDK ingestion router (§19).
//
// Two routes, events and logs, and deliberately no more: this is the highest-volume surface in the
// product and everything on it is on the hot path. Anything that is not needed to accept a batch
// belongs on control-api instead.
func Ingest() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("ingestion"))

	r.Post("/v1/batches", handlers.AcceptBatch,
		muzak.Summary("Accept a batch of analytics events"),
		muzak.Description("Answers 202 once the batch has reached durable queueing. "+
			"The response says how many events were accepted and how many were dropped, "+
			"so an SDK can stop retrying a batch that will never be accepted. "+
			"The body may be sent with Content-Encoding: gzip or zstd; the size limit "+
			"below applies to the decoded batch either way."),
		muzak.Status(http.StatusAccepted),

		// The tenant is resolved from the Authorization header before binding, so an
		// unauthenticated caller gets a 401 rather than a map of the schema. It insists
		// on a project key: a reporting or server credential reaching this endpoint is a
		// misconfiguration, and accepting it would widen what a leaked analytics key can
		// do (§52).
		muzak.Needs(core.ResolveProjectKey),

		// A batch is larger than a control-plane document and smaller than an upload.
		// §19.1 wants this bounded per project; this is the ceiling.
		//
		// It is also §19.1's "maximum decompressed bytes". The framework applies it while
		// binding, to whatever `transport.Decompress` left in the body — so a compressed
		// batch is held to the same size as an uncompressed one, and a decompression bomb
		// stops being read here rather than being read and then measured. The companion
		// limit on the *compressed* wire bytes is INGEST_MAX_COMPRESSED_BYTES.
		muzak.MaxBodySize(4<<20),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "The key is scoped to another environment"),
		muzak.WithResponseDoc(http.StatusRequestEntityTooLarge,
			"The batch exceeded the body limit, compressed or decoded"),
		muzak.WithResponseDoc(http.StatusUnsupportedMediaType,
			"The Content-Encoding is not gzip or zstd"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The batch could not be queued; retry with backoff"),
	)

	// Cloud Logging's batches (ADR 0050): the same credential, the same encodings and the same
	// ceiling as an analytics batch, onto a topic of its own.
	r.Post("/v1/logs", handlers.AcceptLogs,
		muzak.Summary("Accept a batch of logs, exceptions and crashes"),
		muzak.Description("Answers 202 once the batch has reached durable queueing, with how many "+
			"records were stored and how many dropped. Text past its limit is cut rather than "+
			"refused, so a long log line or a deep stack is kept. The body may be sent with "+
			"Content-Encoding: gzip or zstd."),
		muzak.Status(http.StatusAccepted),
		muzak.Needs(core.ResolveProjectKey),
		muzak.MaxBodySize(4<<20),
		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "The key is scoped to another environment"),
		muzak.WithResponseDoc(http.StatusRequestEntityTooLarge,
			"The batch exceeded the body limit, compressed or decoded"),
		muzak.WithResponseDoc(http.StatusUnsupportedMediaType,
			"The Content-Encoding is not gzip or zstd"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The batch could not be queued; retry with backoff"),
	)

	return r
}
