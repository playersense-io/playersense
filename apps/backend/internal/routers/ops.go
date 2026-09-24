package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// OperatorAnalytics is the operations console's view of the platform (§56, §67): ingestion across
// every tenant, the ClickHouse node's health, and storage. Mounted under /v1/operator behind a
// signed-in staff member with the ops role. Aggregate only: nothing here names a player.
func OperatorAnalytics() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "ClickHouse could not be read"))

	r.Get("/ingestion", handlers.OperatorIngestion,
		core.RequireStaff(staff.Ops),
		muzak.Summary("Every tenant's ingestion"),
		muzak.Description("Events, metered bytes, batches, players, events that arrived more than an "+
			"hour late, and how long after the gateway accepted them analytics-writer stored them, "+
			"by hour (or by minute for one day), with the hundred busiest projects and the last five "+
			"minutes. Read from activity_1m and activity_1h."))

	r.Get("/clickhouse", handlers.OperatorClickHouse,
		core.RequireStaff(staff.Ops),
		muzak.Summary("The analytics node's health"),
		muzak.Description("Replication queues, merges and unfinished mutations, active parts by table "+
			"and disk (the hot and cold volumes), the partitions with the most parts, disk space, "+
			"memory, and errors raised in the last day."))

	r.Get("/storage", handlers.OperatorStorage,
		core.RequireStaff(staff.Ops),
		muzak.Summary("Storage by table and by project"),
		muzak.Description("Bytes on disk by table and volume, and each project's metered logical "+
			"bytes over the range with its share, which is what §39's storage accounting apportions."))

	r.Get("/overview", handlers.OperatorOverview,
		core.RequireStaff(staff.Ops, staff.Support, staff.Billing),
		muzak.Summary("The console's front page"),
		muzak.Description("One line of each area: the pipeline now and today, the node reduced to what "+
			"should be zero, how far behind each writer is, the worker's last day, and, for billing "+
			"and support staff, the accounts that need somebody. An area that cannot be read is named "+
			"in errors while the rest still answer."))

	r.Get("/queue", handlers.OperatorQueue,
		core.RequireStaff(staff.Ops),
		muzak.Summary("How far behind each writer is"),
		muzak.Description("For analytics-writer, archive-writer and billing-writer: batches and bytes not "+
			"yet consumed, when the gateway accepted the oldest and how long it has waited, and what "+
			"each dead-lettered over the window. From the NDJSON spool locally, and from Cloud "+
			"Monitoring on Pub/Sub, where the numbers trail by a minute or two."))

	r.Get("/rate-limits", handlers.OperatorRateLimits,
		core.RequireStaff(staff.Ops),
		muzak.Summary("Who the limits refused"),
		muzak.Description("Every budget on the ingestion and configuration paths and on invitations, "+
			"with its size and whose it is; refusals by hour and by quota over the range; the fifty "+
			"projects and organizations refused most; and, for one project, where its project-wide "+
			"budgets stand in their current window. Never a device or an address. No overrides: "+
			"docs/rate-limits.md §9 says why."))

	r.Get("/jobs", handlers.OperatorJobs,
		core.RequireStaff(staff.Ops),
		muzak.Summary("The worker's jobs"),
		muzak.Description("Each job's passes over the range (the nightly derivation, erasures, exports, "+
			"archive expiry, the usage meter and credit expiry): runs, failures, the last error, how "+
			"long they take and what the latest did; the latest two hundred passes; and what waits on "+
			"them: erasures by state with those past their one-month deadline, exports, and archive "+
			"objects due, stuck or failed. No player's identifier."))

	r.Post("/projects/{project}/archive-manifests/{manifest_id}/retry", handlers.RetryArchiveManifest,
		core.RequireStaff(staff.Ops), core.StaffReads("archive.retry"),
		muzak.Summary("Hand a failed archive object back to the sweep"),
		muzak.Description("For after its cause is fixed. The next pass tries it again and, if the cause "+
			"is still there, fails it again with the reason."),
		muzak.WithResponseDoc(http.StatusNotFound, "This project has no archive object with that id"),
		muzak.WithResponseDoc(http.StatusConflict, "The object has not failed"))

	return r
}
