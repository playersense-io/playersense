package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// PurchaseVerification is the surface the game and its server verify purchases on (§47). The
// verify route takes the game's project key or the server key; reading a purchase back takes the
// server key alone, because it is what a game server grants from.
func PurchaseVerification() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("purchases"))
	unauthorized := muzak.WithResponseDoc(http.StatusUnauthorized, "The key is missing, unknown or revoked")

	r.Post("/v1/purchases/verify", handlers.VerifyPurchase,
		muzak.Summary("Verify a purchase with its store"),
		muzak.Description("PlayerSense asks the store (for a StoreKit 2 transaction, checks Apple's signature, "+
			"which needs no call) and records the purchase once: a retry, or the same receipt from a second "+
			"device, answers the same purchase with duplicate set, and the same transaction sent for another "+
			"player is rejected as replayed. The answer's status says what to do: grant on verified or "+
			"sandbox, keyed by the purchase's id; do not grant on rejected. Takes the game's project key, so a "+
			"game without a server can act on the answer, or the server key, which §47 prefers for granting."),
		muzak.Needs(core.ResolvePurchaseKey),
		muzak.MaxBodySize(32<<10),
		unauthorized,
		muzak.WithResponseDoc(http.StatusConflict, "The project has not connected this store"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The receipt needs store credentials the project has not added"),
		muzak.WithResponseDoc(http.StatusTooManyRequests, "Too many verifications for this player or project"),
		muzak.WithResponseDoc(http.StatusServiceUnavailable,
			"The store could not answer, or refused the project's credentials; keep the receipt and retry"))

	r.Get("/v1/purchases/{store}/{transaction}", handlers.LookupPurchase,
		muzak.Summary("Read a verified purchase"),
		muzak.Description("By the store's transaction id: whether it was verified, and whether it has since been "+
			"refunded."),
		muzak.Needs(core.ResolveServerKey), unauthorized,
		muzak.WithResponseDoc(http.StatusNotFound, "No verified purchase has that transaction id in this project"))

	r.Get("/v1/players/{player}/purchases", handlers.PlayerPurchases,
		muzak.Summary("A player's verified purchases"),
		muzak.Description("Newest first, refunded ones included."),
		muzak.Needs(core.ResolveServerKey), unauthorized)
	return r
}

// StoreNotifications is where the stores post: App Store Server Notifications V2, and Google
// Play's Real-time Developer Notifications through a Pub/Sub push subscription. The token in the
// path is the project's, from Settings; Apple's notifications are also signed, and checked.
func StoreNotifications() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("purchases"),
		muzak.WithResponseDoc(http.StatusNotFound, "No project listens at this URL"))

	r.Post("/v1/store-notifications/apple/{project}/{token}", handlers.AppleStoreNotification,
		muzak.Summary("Take an App Store Server Notification"),
		muzak.Description("Point App Store Connect's V2 notification URL here. A refund or a reversed refund "+
			"changes the purchase it names; everything else is acknowledged."),
		muzak.MaxBodySize(512<<10),
		muzak.WithResponseDoc(http.StatusBadRequest, "The notification is not signed by Apple, or is for another app"))

	r.Post("/v1/store-notifications/google/{project}/{token}", handlers.GoogleStoreNotification,
		muzak.Summary("Take a Google Play developer notification"),
		muzak.Description("Point a Pub/Sub push subscription on the app's Real-time Developer Notifications "+
			"topic here. A voided purchase is recorded as refunded; the refund sweep adds the reason."),
		muzak.MaxBodySize(64<<10))
	return r
}

// Stores is where a project connects its stores and reads its verification log: purchases.configure
// to connect and test them, player.read for the log and the purchases, which name players.
func Stores() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("purchases"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or store does not exist"))
	base := "/organizations/{org}/projects/{project}/stores"

	r.Get(base, handlers.ListStores,
		muzak.Summary("The project's stores"),
		muzak.Description("App Store, Google Play and Steam, connected or not: how each is reached (never a "+
			"secret), the URL its notifications go to, how its last test went, and the last day's "+
			"verifications by outcome."),
		core.Require(authz.PurchasesConfigure))
	r.Put(base+"/{store}", handlers.ConnectStore,
		muzak.Summary("Connect a store"),
		muzak.Description("Or change how it is reached; a secret not sent is kept. From the moment a store is "+
			"connected, its revenue is its verified purchases rather than what the game reports."),
		core.Require(authz.PurchasesConfigure),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "Something the store needs is missing or malformed"))
	r.Delete(base+"/{store}", handlers.DisconnectStore,
		muzak.Summary("Disconnect a store"),
		muzak.Description("Its credentials go. Its purchases stay; its revenue is what the game reports again."),
		core.Require(authz.PurchasesConfigure), muzak.Status(http.StatusNoContent))
	r.Post(base+"/{store}/test", handlers.TestStore,
		muzak.Summary("Test a store's credentials"),
		muzak.Description("Without a purchase. For the App Store with a key, Apple also sends a test notification "+
			"to the project's URL. Kept as the store's last test."),
		core.Require(authz.PurchasesConfigure))
	r.Post(base+"/{store}/notification-url", handlers.RotateStoreURL,
		muzak.Summary("Replace a store's notification URL"),
		muzak.Description("The old URL stops at once: point the store at the new one."),
		core.Require(authz.PurchasesConfigure))

	r.Get("/organizations/{org}/projects/{project}/purchase-attempts", handlers.ListPurchaseAttempts,
		muzak.Summary("The verification log"),
		muzak.Description("Every receipt the game or its server sent, newest first: what the store said and, step "+
			"by step, how it was checked. Kept thirty days."),
		core.Require(authz.PlayerRead))
	r.Get("/organizations/{org}/projects/{project}/verified-purchases/{purchase}", handlers.ReadVerifiedPurchase,
		muzak.Summary("One verified purchase"),
		muzak.Description("As the API hands it to the game, with every verification that named it."),
		core.Require(authz.PlayerRead))
	return r
}

// OperatorPurchases is purchase verification as staff debug it (§56: "purchase verification
// debugging").
func OperatorPurchases() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"))
	lookup := core.RequireStaff(staff.Support, staff.Ops)

	r.Get("/purchase-verification", handlers.PurchaseVerification,
		lookup, core.StaffReads("purchases.overview"),
		muzak.Summary("Purchase verification across the platform"),
		muzak.Description("The last day's verifications by store and outcome, rejections by reason, and the "+
			"connected stores whose last call failed."))
	r.Get("/purchase-attempts", handlers.OperatorPurchaseAttempts,
		lookup, core.StaffReads("purchases.attempts"),
		muzak.Summary("Every project's verification log"),
		muzak.Description("Narrowed by project, store, outcome, rejection code or player; newest first."))
	r.Get("/purchase-attempts/{attempt}", handlers.OperatorPurchaseAttempt,
		lookup, core.StaffReads("purchases.attempt"),
		muzak.Summary("One verification"),
		muzak.Description("With every step it took."),
		muzak.WithResponseDoc(http.StatusNotFound, "No attempt has that id"))
	r.Post("/projects/{project}/purchase-check", handlers.CheckPurchase,
		lookup, core.StaffReads("purchases.check"),
		muzak.Summary("Check a receipt against a project's store"),
		muzak.Description("What a failed verification would do now, step by step, with the store's answer and the "+
			"purchase on record for it. Nothing is recorded: not a purchase, not an attempt."),
		muzak.WithResponseDoc(http.StatusNotFound, "No project has that id"))
	return r
}
