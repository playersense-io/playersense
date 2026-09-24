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

	return r
}
