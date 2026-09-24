package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
	"playersense.dev/backend/internal/staff"
)

// OperatorStaff is who may use the operations console, and the record of what they did in it
// (§56, §104). Mounted under /v1/operator behind a signed-in staff member.
//
// Changing who is staff needs a ticket like a customer read does, so every grant and every
// removal is in staff_access beside the reads it made possible.
func OperatorStaff() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("operator"))

	r.Get("/me", handlers.StaffMe,
		muzak.Summary("The signed-in staff member"),
		muzak.Description("Who you are to the console, and which roles you hold. Any active staff "+
			"member may read it."))

	r.Get("/staff", handlers.ListStaff,
		core.RequireStaff(staff.Admin),
		muzak.Summary("List staff"),
		muzak.Description("Everybody who is, or was, PlayerSense staff, with their roles, who added "+
			"them and when they left: the access review §104 asks for."))

	r.Post("/staff", handlers.AddStaff,
		core.RequireStaff(staff.Admin), core.StaffReads("staff.add"),
		muzak.Summary("Give somebody the console"),
		muzak.Description("Roles are ops, support, billing and admin. They sign in with the address "+
			"given here, which Firebase must have verified."),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "That address is already staff"))

	r.Put("/staff/{staff_id}", handlers.UpdateStaff,
		core.RequireStaff(staff.Admin), core.StaffReads("staff.update"),
		muzak.Summary("Change a staff member"),
		muzak.Description("Their name, their roles, or whether they may sign in. Deactivating keeps the "+
			"row for the access review. You cannot deactivate yourself, and the last active admin "+
			"cannot stop being one."),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"An unknown role, yourself deactivated, or no admin left"))

	r.Get("/staff-access", handlers.ListStaffAccess,
		core.RequireStaff(staff.Admin),
		muzak.Summary("Read what staff read"),
		muzak.Description("Every staff read of a customer's data and every change to who is staff, "+
			"newest first: who, which customer, the ticket, and when. Filter by staff member, "+
			"organization or action."))

	return r
}
