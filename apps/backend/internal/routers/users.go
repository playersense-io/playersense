package routers

import (
	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/handlers"
)

// Users returns the routes about the signed-in person.
//
// No permission, like [Invitations]: the path names no organization, so there is no role to check a
// permission against, and the caller is only ever reading about themselves. What each organization
// shows is decided per organization inside the handler, by the role the person holds there.
func Users() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("users"))

	r.Get("/users/me", handlers.ReadMe,
		muzak.Summary("Who you are, and where you belong"),
		muzak.Description("The signed-in person and every organization they belong to, with "+
			"their role in each. `balance` is the organization's live balance — "+
			"`available_micros`, or `owed_micros` when it owes, is the headline — and is null "+
			"where the role does not hold `billing.read`, exactly where GET …/billing would refuse."))

	return r
}
