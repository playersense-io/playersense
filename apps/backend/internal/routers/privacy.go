package routers

import (
	"net/http"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Privacy returns the data-subject rights router (§61, §101; docs/compliance/05).
//
// It sits on the reporting service rather than on control-api, and that placement is a
// residency decision rather than a convenience. Every request here names a player, and a
// player identifier never reaches the global tier (§99) — control-api is US-hosted and holds
// no cell's data, so it is the one service that must not see one of these.
//
// # A server key, not a reporting key
//
// Erasure and export are server-to-server operations a studio's backend performs on behalf
// of a player who asked them for something (§52, docs/compliance/00: the studio is the
// controller and PlayerSense is the processor). A reporting key belongs to a dashboard and
// reads aggregates; a project key ships inside a game binary and is public by assumption
// (§71). Accepting either here would mean anyone who unpacked an APK could erase a studio's
// players.
func Privacy() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("privacy"))

	r.Post("/v1/privacy/erasures", handlers.SubmitErasure,
		muzak.Summary("Erase everything held about one player"),
		muzak.Description("Records the request, stops the actor's events being accepted or "+
			"served, and removes them asynchronously from the events, the two device-keyed "+
			"summaries, the verified purchases and the archive. Returns immediately: removal "+
			"is a rewrite of whole parts and whole objects and is measured in hours, which is "+
			"why a tombstone goes up first."),
		muzak.Status(http.StatusAccepted),

		muzak.Needs(core.ResolveServerKey),
		muzak.MaxBodySize(4<<10),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "This endpoint needs a server key"),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the request"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"The request could not be recorded, so nothing was started; send it again"),
	)

	r.Get("/v1/privacy/erasures/{request_id}", handlers.ReadErasure,
		muzak.Summary("Check an erasure"),
		muzak.Description("Reports the state, the timestamps a deletion SLA is measured "+
			"against, and what was removed."),
		muzak.Status(http.StatusOK),

		muzak.Needs(core.ResolveServerKey),

		muzak.WithResponseDoc(http.StatusNotFound, "No such erasure request for this project"),
	)

	r.Post("/v1/privacy/exports", handlers.SubmitExport,
		muzak.Summary("Export everything held about one player"),
		muzak.Description("Assembles the events, the device records, the daily activity and "+
			"the verified purchases into one newline-delimited JSON bundle in this cell's own "+
			"region. The bundle expires, so collect it before then."),
		muzak.Status(http.StatusAccepted),

		muzak.Needs(core.ResolveServerKey),
		muzak.MaxBodySize(4<<10),

		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden, "This endpoint needs a server key"),
		muzak.WithResponseDoc(http.StatusConflict,
			"This actor is being erased, so there is nothing left to export"),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project's data lives in another cell; the body names where to send the request"),
	)

	r.Get("/v1/privacy/exports/{request_id}", handlers.ReadExport,
		muzak.Summary("Check an export"),
		muzak.Status(http.StatusOK),

		muzak.Needs(core.ResolveServerKey),

		muzak.WithResponseDoc(http.StatusNotFound, "No such export request for this project"),
	)

	r.Get("/v1/privacy/exports/{request_id}/download", handlers.DownloadExport,
		muzak.Summary("Download an export bundle"),
		muzak.Description("Streams the newline-delimited JSON bundle. The first line says what "+
			"the bundle is and what it does not cover; the last line is a summary, so a "+
			"truncated file is recognisable as one."),
		muzak.Status(http.StatusOK),

		muzak.Needs(core.ResolveServerKey),

		muzak.WithResponseDoc(http.StatusConflict, "The bundle is not ready yet"),
		muzak.WithResponseDoc(http.StatusNotFound,
			"The bundle has expired or was removed by an erasure"),
	)

	return r
}
