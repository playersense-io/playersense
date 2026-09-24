package handlers

import (
	"context"
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/auditevent"
	"playersense.dev/backend/ent/predicate"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// Reading the audit log, and the two helpers every handler that writes to it uses.

// ReadAudit answers "who did this".
//
// Under `audit.read`, which the two system roles hold and no other seeded role does — the log names
// what every member of the organisation has been doing, so showing it to all of them is a different
// decision from showing them the members list.
//
// Newest first, because the question is almost always about something that just happened. Filterable
// by action, because "who published" and "who changed a role" are different questions and the second
// is buried under the first on a busy account.
func ReadAudit(ctx *muzak.Context, in schemas.AuditParams) (schemas.AuditListOut, error) {
	org := muzak.From[*core.Principal](ctx).Organization
	return auditOf(ctx.Context(), muzak.From[*core.DB](ctx), org.ID, in.AuditFilter)
}

// auditOf is one page of an organization's log, newest first, paged by id so a page never repeats
// or skips an entry written while somebody reads.
func auditOf(ctx context.Context, db *core.DB, orgID int, in schemas.AuditFilter) (schemas.AuditListOut, error) {
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}

	query := db.Client.AuditEvent.Query().Where(auditevent.OrganizationID(orgID))
	if in.Action != "" {
		query = query.Where(auditevent.Action(in.Action))
	}
	if in.TargetType != "" {
		query = query.Where(auditevent.TargetType(in.TargetType))
	}
	if in.TargetID != "" {
		query = query.Where(auditevent.TargetID(in.TargetID))
	}
	if in.Actor != "" {
		// A person acted either signed in, which is an edge to their user, or as PlayerSense
		// staff, which is only ever the label.
		actor := []predicate.AuditEvent{auditevent.ActorLabel(audit.StaffActor(in.Actor).Label)}
		person, err := db.Client.User.Query().Where(user.Email(in.Actor)).Only(ctx)
		switch {
		case err == nil:
			actor = append(actor, auditevent.ActorUserID(person.ID))
		case !ent.IsNotFound(err):
			return schemas.AuditListOut{}, core.AsHTTPError(err)
		}
		query = query.Where(auditevent.Or(actor...))
	}
	if in.From != "" || in.To != "" {
		from, to, err := dayRange(in.From, in.To)
		if err != nil {
			return schemas.AuditListOut{}, err
		}
		if !from.IsZero() {
			query = query.Where(auditevent.CreatedAtGTE(from))
		}
		if !to.IsZero() {
			query = query.Where(auditevent.CreatedAtLT(to))
		}
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.AuditListOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		query = query.Where(auditevent.IDLT(id))
	}

	rows, err := query.WithActorUser().WithActorKey().
		Order(ent.Desc(auditevent.FieldID)).Limit(limit + 1).All(ctx)
	if err != nil {
		return schemas.AuditListOut{}, core.AsHTTPError(err)
	}

	out := schemas.AuditListOut{Events: make([]schemas.AuditEventOut, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	for _, row := range rows {
		out.Events = append(out.Events, auditEventOut(row))
	}
	return out, nil
}

// dayRange reads an optional inclusive pair of dates as a half-open range of instants: from the
// start of from to the start of the day after to. Either may be absent, as a zero time.
func dayRange(fromText, toText string) (time.Time, time.Time, error) {
	var from, to time.Time
	if fromText != "" {
		day, err := time.Parse(time.DateOnly, fromText)
		if err != nil {
			return from, to, muzak.UnprocessableEntity("from is not a date: YYYY-MM-DD")
		}
		from = day
	}
	if toText != "" {
		day, err := time.Parse(time.DateOnly, toText)
		if err != nil {
			return from, to, muzak.UnprocessableEntity("to is not a date: YYYY-MM-DD")
		}
		to = day.AddDate(0, 0, 1)
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return from, to, muzak.UnprocessableEntity("to is before from")
	}
	return from, to, nil
}

func auditEventOut(row *ent.AuditEvent) schemas.AuditEventOut {
	out := schemas.AuditEventOut{
		ID:         row.UID,
		Action:     row.Action,
		TargetType: row.TargetType,
		TargetID:   row.TargetID,
		Metadata:   row.Metadata,
		IP:         row.IP,
		At:         row.CreatedAt,
	}

	// The label first, because it is what was written at the time and therefore what is true
	// about the moment. The edges refine it when they still resolve: a person's current name is
	// more useful than the address they had, and a deactivated account falls back to the label
	// rather than becoming "unknown".
	out.Actor = row.ActorLabel
	switch {
	case row.Edges.ActorUser != nil:
		out.ActorEmail = row.Edges.ActorUser.Email
		out.Actor = row.Edges.ActorUser.Email
		if row.Edges.ActorUser.Name != "" {
			out.Actor = row.Edges.ActorUser.Name + " (" + row.Edges.ActorUser.Email + ")"
		}
	case row.Edges.ActorKey != nil:
		out.Actor = "key " + row.Edges.ActorKey.PublicID
		if row.Edges.ActorKey.Name != "" {
			out.Actor = "key " + row.Edges.ActorKey.Name + " (" + row.Edges.ActorKey.PublicID + ")"
		}
	}
	return out
}

// ---- writing ----------------------------------------------------------------

// actorOf reads the actor out of a request.
//
// The label is written even when the edge resolves, because the edge can stop resolving: a
// deactivated user or a deleted key leaves an entry that would otherwise say nothing about who acted.
// An entry that becomes "unknown" when somebody leaves the company has lost the thing it was for.
func actorOf(ctx *muzak.Context) audit.Actor {
	if principal, found := muzak.TryFrom[*core.Principal](ctx); found && principal.User != nil {
		return audit.Actor{UserID: principal.User.ID, Label: principal.User.Email}
	}
	if tenant, found := muzak.TryFrom[*core.Tenant](ctx); found {
		return audit.Actor{Label: "key " + tenant.KeyPublicID}
	}
	// A route with neither. Not silently empty: an entry whose actor is blank is one somebody
	// will read as "the system did it", and no route in this product acts on nobody's behalf.
	return audit.Actor{Label: "unattributed"}
}

// auditing collects what every entry from this request shares.
//
// A value rather than four arguments threaded through each call, because the two that are easy to
// forget — the organisation and the address — are the two that make an entry findable and
// attributable.
type auditing struct {
	org   int
	actor audit.Actor
	ip    string
}

// auditFor builds the common part from the request.
func auditFor(ctx *muzak.Context) auditing {
	out := auditing{actor: actorOf(ctx), ip: core.ClientAddressOf(ctx)}
	if principal, found := muzak.TryFrom[*core.Principal](ctx); found && principal.Organization != nil {
		out.org = principal.Organization.ID
	}
	return out
}

// entry completes one.
func (a auditing) entry(action audit.Action, targetType, targetID string, metadata map[string]any) audit.Entry {
	return audit.Entry{
		OrganizationID: a.org,
		Action:         action,
		Actor:          a.actor,
		TargetType:     targetType,
		TargetID:       targetID,
		Metadata:       metadata,
		IP:             a.ip,
	}
}

// record appends inside a transaction, which is the default and the one with a guarantee.
func (a auditing) record(ctx context.Context, tx *ent.Tx, action audit.Action,
	targetType, targetID string, metadata map[string]any,
) error {
	_, err := audit.Record(ctx, tx, a.entry(action, targetType, targetID, metadata))
	return err
}

// forOrganization overrides the organisation, for a route that resolved one without a principal.
//
// The operator surface names an organisation by slug and has no membership behind it, so the common
// path cannot fill this in.
func (a auditing) forOrganization(id int) auditing {
	a.org = id
	return a
}

// byStaff replaces the actor with the signed-in PlayerSense staff member, whose address the
// console's sign-in verified.
func (a auditing) byStaff(ctx *muzak.Context) auditing {
	a.actor = audit.StaffActor(staffEmail(ctx))
	return a
}

// staffEmail is the verified address of the staff member making this request.
func staffEmail(ctx *muzak.Context) string {
	return muzak.From[*core.StaffPrincipal](ctx).Email
}

// inTx runs one write and its audit entry as a single transaction.
//
// The pattern it replaces is eight lines of begin/rollback/commit around two statements, repeated
// once per handler — and the failure mode of writing that out by hand nine times is the ninth one
// where the rollback is forgotten. Here the transaction cannot outlive the closure.
//
// It exists because of the rule in internal/audit: an action that happened is an action that was
// logged, which only holds if the entry commits with the change rather than after it.
func inTx[T any](ctx *muzak.Context, fn func(*ent.Tx) (T, error)) (T, error) {
	var zero T

	db := muzak.From[*core.DB](ctx)
	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return zero, core.AsHTTPError(err)
	}

	out, err := fn(tx)
	if err != nil {
		_ = tx.Rollback()
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, core.AsHTTPError(err)
	}
	return out, nil
}
