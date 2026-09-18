package routers

import (
	"net/http"

	"muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Config returns the SDK's configuration router (§41, §42).
//
// # Why a POST for something that reads
//
// It looks like a GET and it is not one. The answer depends on who is asking — the
// installation, the account, the app version, the studio's own context — and that is a
// request body rather than a query string a proxy might truncate, log or cache. A GET whose
// answer varies by half a dozen parameters is a GET that something in the middle will cache
// wrongly exactly once, for one player, and nobody will ever reproduce it.
//
// Caching is still had where it is worth having: the response carries an ETag over the
// decision, and an SDK that sends it back gets a 304 with no body.
func Config() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("config"))

	r.Post("/v1/config", handlers.ServeConfig,
		muzak.Summary("What this player gets"),
		muzak.Description("Resolves every feature flag and every LiveOps event for one "+
			"player: values and windows, never the rules behind them. Geography and "+
			"network are derived from the request's own connection and are never read "+
			"from the body, because a value a client sends is one a modified build can "+
			"choose."),
		muzak.Status(http.StatusOK),

		muzak.Needs(core.ResolveProjectKey),

		// The same bound ingestion uses. A config request is small — an installation, an
		// app version and a handful of context values — and anything larger is either a
		// mistake or an attempt to make the server work for nothing.
		muzak.MaxBodySize(16<<10),

		muzak.WithResponseDoc(http.StatusNotModified,
			"Nothing this player would see has changed since the ETag they sent"),
		muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked"),
		muzak.WithResponseDoc(http.StatusForbidden,
			"This endpoint needs a project key, and one belonging to the environment asked for"),
		muzak.WithResponseDoc(http.StatusMisdirectedRequest,
			"This project is served by another cell; the body names where to ask"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"The published configuration could not be read; the SDK should keep using its last known good copy"),
	)

	return r
}
