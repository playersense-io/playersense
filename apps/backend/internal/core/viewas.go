package core

import (
	"net/http"
	"strings"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/staffmember"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/staff"
)

// Viewing a customer's dashboard as support (§56: "a 'view as' mode for debugging a customer's
// dashboard state"). A staff member with the support role sends X-Staff-View-As naming the
// organization in the path, and a ticket, and the dashboard's own routes answer as they would for
// a member who may read everything and change nothing: every read permission, every project, and
// read-only on the actor, so a write is refused even on a route that forgot to say it writes.
// Only GET and HEAD are served, and every request is written to staff_access before it is.

// StaffViewAsHeader names the organization a staff member is viewing as.
const StaffViewAsHeader = "X-Staff-View-As"

// ViewAsReason is what a write attempted while viewing as support is told.
const ViewAsReason = "viewing as PlayerSense support is read-only"

// viewAs builds the principal for a staff member viewing an organization, or refuses.
func viewAs(ctx *muzak.Context, db *DB, claims identity.Claims, slug string) (*Principal, error) {
	if !strings.EqualFold(strings.TrimSpace(ctx.Header(StaffViewAsHeader)), slug) {
		return nil, muzak.BadRequest(StaffViewAsHeader + " must name the organization in the path")
	}
	if !claims.EmailVerified {
		return nil, muzak.Forbidden("viewing as a customer needs an address Firebase has verified")
	}
	method := ctx.Request().Method
	if method != http.MethodGet && method != http.MethodHead {
		return nil, muzak.Forbidden(ViewAsReason).WithCode("read_only")
	}
	row, err := db.Client.StaffMember.Query().
		Where(staffmember.Email(staff.NormalizeEmail(claims.Email)), staffmember.Active(true)).Only(ctx.Context())
	if ent.IsNotFound(err) {
		return nil, muzak.Forbidden("this account is not PlayerSense staff")
	}
	if err != nil {
		return nil, muzak.ServiceUnavailable("cannot read the staff list right now").Wrap(err)
	}
	roles, err := staff.Parse(row.Roles)
	if err != nil {
		return nil, muzak.InternalServerError("this staff member's roles are not valid").Wrap(err)
	}
	if !(staff.Member{Roles: roles}).HasAny(staff.Support) {
		return nil, muzak.Forbidden("viewing as a customer needs the staff role support")
	}
	ticket := strings.TrimSpace(ctx.Header(StaffTicketHeader))
	if ticket == "" || len(ticket) > 128 {
		return nil, muzak.BadRequest("viewing as a customer needs a ticket reference in " + StaffTicketHeader)
	}

	org, err := db.Client.Organization.Query().Where(organization.Slug(slug)).Only(ctx.Context())
	if ent.IsNotFound(err) {
		return nil, muzak.NotFound("no such organization")
	}
	if err != nil {
		return nil, muzak.ServiceUnavailable("cannot read that organization right now").Wrap(err)
	}

	// The staff member's own account if they have one; never one made for them, which would
	// come with a personal organization nobody asked for.
	person, err := db.Client.User.Query().Where(user.FirebaseUID(claims.UID)).Only(ctx.Context())
	if err != nil {
		if !ent.IsNotFound(err) {
			return nil, muzak.ServiceUnavailable("cannot read your account right now").Wrap(err)
		}
		person = &ent.User{Email: claims.Email, Name: claims.Name, FirebaseUID: claims.UID, Active: true}
	}

	var reads []authz.Permission
	var names []string
	for _, p := range authz.All() {
		if !p.Mutates() {
			reads = append(reads, p)
			names = append(names, string(p))
		}
	}
	actor := authz.UserActor(org.ID, person.ID, "", reads, true, nil)
	actor.ReadOnly, actor.ReadOnlyReason = true, ViewAsReason
	member := &ent.OrganizationMember{OrganizationID: org.ID, UserID: person.ID, AllProjects: true}
	member.Edges.Role = &ent.Role{Key: "support_view", Name: "PlayerSense support, read-only", Permissions: names}
	p := &Principal{Claims: claims, User: person, Organization: org, Member: member, Actor: actor}
	if err := p.enterProject(ctx, db); err != nil {
		return nil, err
	}

	note := strings.TrimSpace(ctx.Header(StaffNoteHeader))
	note = strings.TrimSpace(note + " " + method + " " + ctx.Request().URL.Path)
	if len(note) > 500 {
		note = note[:500]
	}
	create := db.Client.StaffAccess.Create().SetStaffEmail(row.Email).SetAction("support.view_as").
		SetTicket(ticket).SetNote(note).SetIP(ClientAddressOf(ctx)).SetOrganizationID(org.ID)
	if p.Project != nil {
		create = create.SetProjectID(p.Project.ID)
	}
	if err := create.Exec(ctx.Context()); err != nil {
		// No record, no read.
		return nil, muzak.ServiceUnavailable("cannot record this access, so it was not made").Wrap(err)
	}
	return p, nil
}
