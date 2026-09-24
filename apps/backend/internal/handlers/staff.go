package handlers

import (
	"slices"
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/staffaccess"
	"playersense.dev/backend/ent/staffmember"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
)

// Who is staff, and what staff did (§56, §104).

func staffOut(row *ent.StaffMember, you string) schemas.StaffMemberOut {
	return schemas.StaffMemberOut{
		ID: row.UID, Email: row.Email, Name: row.Name, Roles: row.Roles, Active: row.Active,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, DeactivatedAt: row.DeactivatedAt,
		You: row.Email == you,
	}
}

// StaffMe is the signed-in staff member, for the console's shell.
func StaffMe(ctx *muzak.Context, _ struct{}) (schemas.StaffMemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	me := muzak.From[*core.StaffPrincipal](ctx)
	row, err := db.Client.StaffMember.Get(ctx.Context(), me.ID)
	if err != nil {
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}
	return staffOut(row, me.Email), nil
}

// ListStaff lists everybody who is, or was, staff.
func ListStaff(ctx *muzak.Context, _ struct{}) (schemas.StaffListOut, error) {
	db := muzak.From[*core.DB](ctx)
	me := muzak.From[*core.StaffPrincipal](ctx)
	rows, err := db.Client.StaffMember.Query().
		Order(ent.Desc(staffmember.FieldActive), ent.Asc(staffmember.FieldEmail)).
		All(ctx.Context())
	if err != nil {
		return schemas.StaffListOut{}, core.AsHTTPError(err)
	}
	out := schemas.StaffListOut{Staff: make([]schemas.StaffMemberOut, 0, len(rows))}
	for _, row := range rows {
		out.Staff = append(out.Staff, staffOut(row, me.Email))
	}
	return out, nil
}

// AddStaff gives somebody the console.
func AddStaff(ctx *muzak.Context, in schemas.AddStaffIn) (schemas.StaffMemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	me := muzak.From[*core.StaffPrincipal](ctx)
	roles, err := staff.Parse(in.Roles)
	if err != nil {
		return schemas.StaffMemberOut{}, muzak.UnprocessableEntity(err.Error())
	}
	row, err := db.Client.StaffMember.Create().
		SetEmail(staff.NormalizeEmail(in.Email)).
		SetName(in.Name).
		SetRoles(staff.Names(roles)).
		SetCreatedBy(me.Email).
		Save(ctx.Context())
	if err != nil {
		if ent.IsConstraintError(err) {
			return schemas.StaffMemberOut{}, muzak.Conflict(
				"that address is already staff; change their roles or reactivate them instead")
		}
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}
	ctx.SetStatus(201)
	return staffOut(row, me.Email), nil
}

// UpdateStaff changes a staff member's name, roles or whether they may sign in.
//
// Two changes are refused because they cannot be undone from the console: deactivating yourself,
// and leaving the console with no active admin, since nobody would remain who could add one.
func UpdateStaff(ctx *muzak.Context, in schemas.UpdateStaffIn) (schemas.StaffMemberOut, error) {
	db := muzak.From[*core.DB](ctx)
	me := muzak.From[*core.StaffPrincipal](ctx)

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := tx.StaffMember.Query().Where(staffmember.UID(in.StaffID)).ForUpdate().Only(ctx.Context())
	if err != nil {
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}

	update := tx.StaffMember.UpdateOne(row)
	roles := row.Roles
	active := row.Active
	if in.Name != nil {
		update = update.SetName(*in.Name)
	}
	if in.Roles != nil {
		parsed, err := staff.Parse(in.Roles)
		if err != nil {
			return schemas.StaffMemberOut{}, muzak.UnprocessableEntity(err.Error())
		}
		roles = staff.Names(parsed)
		update = update.SetRoles(roles)
	}
	if in.Active != nil && *in.Active != row.Active {
		if !*in.Active && row.Email == me.Email {
			return schemas.StaffMemberOut{}, muzak.UnprocessableEntity(
				"you cannot deactivate yourself; another admin can")
		}
		active = *in.Active
		update = update.SetActive(active)
		if active {
			update = update.ClearDeactivatedAt()
		} else {
			update = update.SetDeactivatedAt(time.Now().UTC())
		}
	}

	// The last active admin keeps being one.
	wasAdmin := row.Active && slices.Contains(row.Roles, string(staff.Admin))
	isAdmin := active && slices.Contains(roles, string(staff.Admin))
	if wasAdmin && !isAdmin {
		others, err := tx.StaffMember.Query().
			Where(staffmember.Active(true), staffmember.IDNEQ(row.ID)).
			All(ctx.Context())
		if err != nil {
			return schemas.StaffMemberOut{}, core.AsHTTPError(err)
		}
		admins := 0
		for _, m := range others {
			if slices.Contains(m.Roles, string(staff.Admin)) {
				admins++
			}
		}
		if !staff.KeepsAnAdmin(false, admins) {
			return schemas.StaffMemberOut{}, muzak.UnprocessableEntity(
				"this is the last active admin; make somebody else an admin first")
		}
	}

	saved, err := update.Save(ctx.Context())
	if err != nil {
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return schemas.StaffMemberOut{}, core.AsHTTPError(err)
	}
	return staffOut(saved, me.Email), nil
}

// ListStaffAccess reads the record of staff reads and changes, newest first.
func ListStaffAccess(ctx *muzak.Context, in schemas.StaffAccessParams) (schemas.StaffAccessOut, error) {
	db := muzak.From[*core.DB](ctx)

	q := db.Client.StaffAccess.Query()
	if in.Staff != "" {
		q = q.Where(staffaccess.StaffEmail(staff.NormalizeEmail(in.Staff)))
	}
	if in.Action != "" {
		q = q.Where(staffaccess.Action(in.Action))
	}
	if in.Org != "" {
		org, err := db.Client.Organization.Query().Where(organization.Slug(in.Org)).Only(ctx.Context())
		if err != nil {
			if ent.IsNotFound(err) {
				return schemas.StaffAccessOut{Entries: []schemas.StaffAccessEntryOut{}}, nil
			}
			return schemas.StaffAccessOut{}, core.AsHTTPError(err)
		}
		q = q.Where(staffaccess.OrganizationID(org.ID))
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.StaffAccessOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(staffaccess.IDLT(id))
	}
	rows, err := q.Order(ent.Desc(staffaccess.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.StaffAccessOut{}, core.AsHTTPError(err)
	}

	out := schemas.StaffAccessOut{Entries: make([]schemas.StaffAccessEntryOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}

	orgIDs, projectIDs := map[int]string{}, map[int]string{}
	for _, row := range rows {
		if row.OrganizationID != nil {
			orgIDs[*row.OrganizationID] = ""
		}
		if row.ProjectID != nil {
			projectIDs[*row.ProjectID] = ""
		}
	}
	if len(orgIDs) > 0 {
		orgs, err := db.Client.Organization.Query().
			Where(organization.IDIn(keysOf(orgIDs)...)).All(ctx.Context())
		if err != nil {
			return schemas.StaffAccessOut{}, core.AsHTTPError(err)
		}
		for _, o := range orgs {
			orgIDs[o.ID] = o.Slug
		}
	}
	if len(projectIDs) > 0 {
		projects, err := db.Client.Project.Query().
			Where(project.IDIn(keysOf(projectIDs)...)).All(ctx.Context())
		if err != nil {
			return schemas.StaffAccessOut{}, core.AsHTTPError(err)
		}
		for _, p := range projects {
			projectIDs[p.ID] = p.Slug
		}
	}

	for _, row := range rows {
		entry := schemas.StaffAccessEntryOut{
			ID: row.UID, StaffEmail: row.StaffEmail, Action: row.Action, Ticket: row.Ticket,
			Note: row.Note, IP: row.IP, At: row.CreatedAt,
		}
		if row.OrganizationID != nil {
			entry.Organization = orgIDs[*row.OrganizationID]
		}
		if row.ProjectID != nil {
			entry.Project = projectIDs[*row.ProjectID]
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func keysOf(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
