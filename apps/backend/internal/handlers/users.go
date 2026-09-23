package handlers

import (
	"cmp"
	"slices"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// ReadMe answers who the caller is, where they belong, and what each organization has to spend.
//
// A balance is shown only where the caller's role holds billing.read, decided by the same actor the
// organization's own routes build (core.MemberActor) — so this cannot show a balance that
// GET …/billing would refuse. A fixed number of queries however many organizations: memberships
// with their roles and organizations, then accounts, credit and queued usage for all of them at once.
func ReadMe(ctx *muzak.Context, _ muzak.Empty) (schemas.MeOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	person := principal.User

	members, err := db.Client.OrganizationMember.Query().
		Where(organizationmember.UserID(person.ID)).
		WithOrganization().
		WithRole().
		All(ctx.Context())
	if err != nil {
		return schemas.MeOut{}, core.AsHTTPError(err)
	}
	slices.SortFunc(members, func(a, b *ent.OrganizationMember) int {
		return cmp.Or(cmp.Compare(a.Edges.Organization.Name, b.Edges.Organization.Name),
			cmp.Compare(a.OrganizationID, b.OrganizationID))
	})

	var billable []int
	for _, member := range members {
		if member.Edges.Role == nil {
			continue // unreachable: the schema requires a role; shown with no balance
		}
		if core.MemberActor(member, principal.Claims.EmailVerified, nil).Can(authz.BillingRead, 0) {
			billable = append(billable, member.OrganizationID)
		}
	}
	balances, err := balancesOf(ctx.Context(), db, billable, nowUTC())
	if err != nil {
		return schemas.MeOut{}, core.AsHTTPError(err)
	}

	out := schemas.MeOut{
		Person: schemas.PersonOut{
			ID: person.UID, Email: person.Email, EmailVerified: principal.Claims.EmailVerified,
			Name: person.Name, AvatarURL: person.AvatarURL,
		},
		Organizations: make([]schemas.MeOrganizationOut, 0, len(members)),
	}
	for _, member := range members {
		org := member.Edges.Organization
		entry := schemas.MeOrganizationOut{ID: org.UID, Name: org.Name, Slug: org.Slug}
		if role := member.Edges.Role; role != nil {
			entry.Role = schemas.MeRoleOut{Key: role.Key, Name: role.Name}
		}
		if balance, found := balances[member.OrganizationID]; found {
			entry.Balance = &balance
		}
		out.Organizations = append(out.Organizations, entry)
	}
	return out, nil
}
