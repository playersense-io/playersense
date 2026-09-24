package core

import (
	"strings"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/staffmember"
	"playersense.dev/backend/internal/firebase"
	"playersense.dev/backend/internal/staff"
)

// The operations console's credential (§56, §104).
//
// It replaced one shared bearer secret. A secret cannot say who is holding it, so every operator
// write carried a name the caller typed and every read was nobody's. Staff now sign in with the same
// Firebase project as customers, and are staff because an active row in staff_members says so:
//
//	operator.Include(routers.OperatorBilling(), muzak.Needs(core.ResolveStaff))
//	r.Get("/organizations/{org}/billing", handlers.OperatorBilling,
//	    core.RequireStaff(staff.Billing, staff.Support), core.StaffReads("billing.read"))
//
// RequireStaff says which roles a route needs. StaffReads marks a route that reaches one customer's
// records: it demands a ticket and writes the staff_access row before the handler runs, so a read
// that was not logged cannot happen (§104).

// StaffPrincipal is the signed-in staff member.
type StaffPrincipal struct {
	staff.Member
}

// StaffTicketHeader carries the reason for a read of customer data: a ticket reference, not free
// text (StaffAccess's own doc comment says why).
const StaffTicketHeader = "X-Staff-Ticket"

// StaffNoteHeader is an optional line beside the ticket.
const StaffNoteHeader = "X-Staff-Note"

// ResolveStaff is the provider every /v1/operator route declares.
//
// The same token verification a customer's sign-in gets, then one more rule: the verified address
// must be an active staff member. Unverified addresses are refused outright, because the table is
// keyed on the address and an unverified one proves nothing about who is holding it.
//
// A signed-in customer is refused with the same answer as a stranger: a 403 that says this is not
// a staff account, and nothing about what the console has.
func ResolveStaff(ctx *muzak.Context) (*StaffPrincipal, error) {
	verifier := muzak.From[*firebase.Verifier](ctx)
	db := muzak.From[*DB](ctx)

	presented, present := muzak.BearerToken(ctx)
	if !present {
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="playersense-staff"`)
		return nil, muzak.Unauthorized("a Firebase ID token is required")
	}
	claims, err := verifier.Verify(ctx.Context(), presented)
	if err != nil {
		if firebase.Rejected(err) {
			return nil, muzak.Unauthorized("that sign-in is not valid")
		}
		return nil, muzak.ServiceUnavailable("cannot verify a sign-in right now").Wrap(err)
	}
	if err := claims.Valid(); err != nil {
		return nil, muzak.Unauthorized("that sign-in is not usable").Wrap(err)
	}
	if !claims.EmailVerified {
		return nil, muzak.Forbidden("the console needs an address Firebase has verified")
	}

	row, err := db.Client.StaffMember.Query().
		Where(staffmember.Email(staff.NormalizeEmail(claims.Email)), staffmember.Active(true)).
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.Forbidden("this account is not PlayerSense staff")
		}
		return nil, muzak.ServiceUnavailable("cannot read the staff list right now").Wrap(err)
	}
	roles, err := staff.Parse(row.Roles)
	if err != nil {
		// A role outside the list can only have been written by hand. Refused rather than
		// treated as none, so it is noticed.
		return nil, muzak.InternalServerError("this staff member's roles are not valid").Wrap(err)
	}
	return &StaffPrincipal{Member: staff.Member{
		ID: row.ID, Email: row.Email, Name: row.Name, Roles: roles,
	}}, nil
}

// StaffAuthorized is what RequireStaff produces.
type StaffAuthorized struct{ Staff *StaffPrincipal }

// RequireStaff declares the roles a route needs; holding any one of them is enough, and admin
// holds them all. Declare it once per route, with every role that may use it, for the reason
// [Require] gives.
func RequireStaff(roles ...staff.Role) muzak.SharedOption {
	return muzak.Needs(func(ctx *muzak.Context) (StaffAuthorized, error) {
		principal := muzak.From[*StaffPrincipal](ctx)
		if !principal.HasAny(roles...) {
			names := make([]string, len(roles))
			for i, r := range roles {
				names[i] = string(r)
			}
			return StaffAuthorized{}, muzak.Forbidden(
				"this needs the staff role " + strings.Join(names, " or "))
		}
		return StaffAuthorized{Staff: principal}, nil
	})
}

// StaffAccessRecord is what StaffReads produces: the customer the route is about, and the proof
// that the read was logged.
type StaffAccessRecord struct {
	Organization *ent.Organization
	Project      *ent.Project
	Ticket       string
}

// StaffReads declares that a route reaches one customer's records, as action.
//
// It resolves the customer from the path ({org} is an organization's slug, {project} a project's
// uid), refuses a request without a ticket, and writes the staff_access row, all before the
// handler runs. Written first rather than after, because a read that failed half way still read
// something, and because the alternative is a handler that forgets.
//
// A customer that does not exist is a 404; the attempt is not recorded, since there is nobody's
// data it could have reached.
func StaffReads(action string) muzak.SharedOption {
	return muzak.Needs(func(ctx *muzak.Context) (StaffAccessRecord, error) {
		principal := muzak.From[*StaffPrincipal](ctx)
		db := muzak.From[*DB](ctx)

		ticket := strings.TrimSpace(ctx.Header(StaffTicketHeader))
		if ticket == "" {
			return StaffAccessRecord{}, muzak.BadRequest(
				"reading a customer's data needs a ticket reference in " + StaffTicketHeader)
		}
		if len(ticket) > 128 {
			return StaffAccessRecord{}, muzak.BadRequest("a ticket reference is at most 128 characters")
		}
		note := strings.TrimSpace(ctx.Header(StaffNoteHeader))
		if len(note) > 500 {
			note = note[:500]
		}

		var out StaffAccessRecord
		out.Ticket = ticket
		if slug := ctx.PathValue(OrgPathValue); slug != "" {
			org, err := db.Client.Organization.Query().Where(organization.Slug(slug)).Only(ctx.Context())
			if err != nil {
				if ent.IsNotFound(err) {
					return out, muzak.NotFound("no organization has that slug")
				}
				return out, muzak.ServiceUnavailable("cannot read that organization right now").Wrap(err)
			}
			out.Organization = org
		}
		if uid := ctx.PathValue(ProjectPathValue); uid != "" {
			proj, err := projectByUID(ctx, db, uid)
			if err != nil {
				return out, err
			}
			out.Project = proj
			if out.Organization == nil {
				org, err := db.Client.Organization.Get(ctx.Context(), proj.OrganizationID)
				if err != nil {
					return out, muzak.ServiceUnavailable("cannot read that project's organization").Wrap(err)
				}
				out.Organization = org
			}
		}

		create := db.Client.StaffAccess.Create().
			SetStaffEmail(principal.Email).
			SetAction(action).
			SetTicket(ticket).
			SetNote(note).
			SetIP(ClientAddressOf(ctx))
		if out.Organization != nil {
			create = create.SetOrganizationID(out.Organization.ID)
		}
		if out.Project != nil {
			create = create.SetProjectID(out.Project.ID)
		}
		if err := create.Exec(ctx.Context()); err != nil {
			// No record, no read.
			return out, muzak.ServiceUnavailable("cannot record this access, so it was not made").Wrap(err)
		}
		return out, nil
	})
}

func projectByUID(ctx *muzak.Context, db *DB, raw string) (*ent.Project, error) {
	uid, err := uuid.Parse(raw)
	if err != nil {
		return nil, muzak.NotFound("no project has that id")
	}
	proj, err := db.Client.Project.Query().Where(project.UID(uid)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("no project has that id")
		}
		return nil, muzak.ServiceUnavailable("cannot read that project right now").Wrap(err)
	}
	return proj, nil
}
