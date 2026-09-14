package routers

import (
	"net/http"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Ingest returns the SDK ingestion router (§19).
//
// One route, and deliberately so: this is the highest-volume surface in the product and
// everything on it is on the hot path. Anything that is not needed to accept a batch
// belongs on control-api instead.
func Ingest() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("ingestion"))

	r.Post("/v1/batches", handlers.AcceptBatch,
		muzak.Summary("Accept a batch of analytics events"),
		muzak.Description("Answers 202 once the batch has reached durable queueing. "+
			"The response says how many events were accepted and how many were dropped, "+
			"so an SDK can stop retrying a batch that will never be accepted."),
		muzak.Status(http.StatusAccepted),

		// The tenant is resolved from the Authorization header before binding, so an
		// unauthenticated caller gets a 401 rather than a map of the schema. It insists
		// on a project key: a reporting or server credential reaching this endpoint is a
		// misconfiguration, and accepting it would widen what a leaked analytics key can
		// do (§52).
		muzak.Needs(core.ResolveProjectKey),

		// A batch is larger than a control-plane document and smaller than an upload.
		// §19.1 wants this bounded per project; this is the ceiling.
		muzak.MaxBodySize(4<<20),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "The key is scoped to another environment"),
		muzak.WithResponseDoc(http.StatusRequestEntityTooLarge, "The batch exceeded the body limit"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The batch could not be queued; retry with backoff"),
	)

	return r
}
