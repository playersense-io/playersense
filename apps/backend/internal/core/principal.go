package core

import (
	"errors"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/memberproject"
	"playersense.dev/backend/ent/global/organization"
	"playersense.dev/backend/ent/global/organizationmember"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/firebase"
	"playersense.dev/backend/internal/identity"
)

// Principal is a signed-in person, the organisation they are acting inside, and what they may
// do there.
//
// Producing one is proof that the Firebase token verified and that this person is a member of
// the organisation the path names — the same idiom as [Tenant]: the only thing that builds one
// is [ResolveUser], so a handler holding one needs no further checks to know those two things.
//
// It carries the resolved organisation and project rather than leaving the handler to look
// them up again. That is not only to save a query: **the object the permission check ran
// against has to be the object the handler acts on**, and two independent lookups of the same
// slug are two chances for those to differ.
type Principal struct {
	Claims identity.Claims
	User   *global.User

	// Organization and Member are nil on a route that names no organisation — signing in,
	// listing what you belong to, creating a new one.
	Organization *global.Organization
	Member       *global.OrganizationMember

	// Project is set when the route's path names one and the person can reach it.
	Project *global.Project

	// Actor is the permission question's subject. Its OrganizationID is zero when the
	// route names no organisation, and authz.Can answers false for everything in that
	// state, so a route that forgot to say which organisation it is about fails closed.
	Actor authz.Actor
}

// OrgPathValue and ProjectPathValue are the path parameters an organisation-scoped and a
// project-scoped route use. Named here so the resolver and the routers cannot drift: a route
// that spelled it `{organization}` would silently resolve no organisation and fail closed,
// which is safe but very confusing to debug.
const (
	OrgPathValue     = "org"
	ProjectPathValue = "project"
)

// ResolveUser is the provider a user-facing route declares.
//
// Muzak's own guidance: a shared secret belongs in a guard, and "anything involving per-user
// credentials wants a provider that resolves the user instead". It has to be a provider for a
// second reason too — every guard runs before every provider, so a guard could not read the
// verifier or the database, both of which are published as singletons.
//
//	api.Include(routers.Projects(), muzak.Needs(core.ResolveUser))
func ResolveUser(ctx *muzak.Context) (*Principal, error) {
	verifier := muzak.From[*firebase.Verifier](ctx)
	ctrl := muzak.From[*Global](ctx)

	presented, present := muzak.BearerToken(ctx)
	if !present {
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="playersense"`)
		return nil, muzak.Unauthorized("a Firebase ID token is required")
	}

	claims, err := verifier.Verify(ctx.Context(), presented)
	if err != nil {
		if firebase.Rejected(err) {
			// Deliberately not the verifier's own reason. "the token has expired" is
			// useful; "the token was issued for X" tells an attacker which project
			// this deployment belongs to, and the client's only action is the same
			// either way: sign in again.
			return nil, muzak.Unauthorized("that sign-in is not valid")
		}
		// Google is unreachable and nothing was cached. Never a 401: telling somebody
		// their session is invalid during an outage sends them to do the one thing that
		// cannot help.
		return nil, muzak.ServiceUnavailable("cannot verify a sign-in right now").Wrap(err)
	}
	if err := claims.Valid(); err != nil {
		return nil, muzak.Unauthorized("that sign-in is not usable").Wrap(err)
	}

	// A token whose address Firebase has not verified must not be able to accept an
	// invitation sent to that address, which is how somebody joins an organisation they
	// were never invited to.
	//
	// It carries more weight than it looks. The product is passwordless, and an email-link
	// sign-in is indistinguishable from a password one by `sign_in_provider` — both report
	// "password", because they are two methods of one Firebase provider. `email_verified`
	// is the only claim that separates them: clicking the link is what proves control of
	// the address, so a passwordless sign-in arrives true and a fresh password sign-up
	// arrives false. See internal/firebase for the measurement.
	// An address Firebase has not confirmed does not stop somebody signing in. They open
	// the dashboard, see their organisation and their projects, and change nothing — with
	// a banner saying which link to click. A wall at the door is a worse first experience
	// and teaches nobody what to do about it.
	//
	// What it does stop is every permission that changes something, which is set below on
	// the actor rather than checked at each call site. The invitation case is why it has
	// to stop *anything*: accepting an invitation sent to an address is how somebody joins
	// an organisation they were never invited to, and accepting is a write.
	// The dashboard requests verification emails directly from Firebase. Resolving a
	// principal does not send mail; the verified claim controls write access below.

	person, err := identity.EnsureUser(ctx.Context(), ctrl.Client, claims, time.Now())
	if err != nil {
		return nil, muzak.ServiceUnavailable("cannot read your account right now").Wrap(err)
	}
	if !person.Active {
		return nil, muzak.Forbidden("this account has been deactivated")
	}

	principal := &Principal{Claims: claims, User: person}

	slug := ctx.PathValue(OrgPathValue)
	if slug == "" {
		// A route about the person rather than about an organisation. The actor stays
		// zero-valued, and every permission question against it answers false.
		return principal, nil
	}
	if err := principal.enterOrganization(ctx, ctrl, slug); err != nil {
		return nil, err
	}
	return principal, nil
}

// VerifyEmailFirst is what a read-only account is told, and why.
const VerifyEmailFirst = "this account has not verified its email address"

// enterOrganization resolves the organisation in the path and the person's standing in it.
func (p *Principal) enterOrganization(ctx *muzak.Context, ctrl *Global, slug string) error {
	org, err := ctrl.Client.Organization.Query().
		Where(organization.Slug(slug)).Only(ctx.Context())
	if err != nil {
		if global.IsNotFound(err) {
			return muzak.NotFound("no such organization")
		}
		return muzak.ServiceUnavailable("cannot read that organization right now").Wrap(err)
	}

	member, err := ctrl.Client.OrganizationMember.Query().
		Where(
			organizationmember.OrganizationID(org.ID),
			organizationmember.UserID(p.User.ID),
		).
		WithRole().
		Only(ctx.Context())
	if err != nil {
		if global.IsNotFound(err) {
			// 404, not 403. A 403 confirms the organisation exists, which turns this
			// endpoint into a way to enumerate customers by guessing slugs.
			return muzak.NotFound("no such organization")
		}
		return muzak.ServiceUnavailable("cannot read your membership right now").Wrap(err)
	}
	if member.Edges.Role == nil {
		// Only reachable if a membership lost its role, which the schema makes required.
		// Refused rather than treated as "no permissions", because silently zero
		// privileges is indistinguishable from a correctly restricted account and nobody
		// would ever find it.
		return muzak.ServiceUnavailable("your membership has no role")
	}

	permissions := make([]authz.Permission, 0, len(member.Edges.Role.Permissions))
	for _, name := range member.Edges.Role.Permissions {
		permissions = append(permissions, authz.Permission(name))
	}

	var reachable []int
	if !member.AllProjects {
		ids, err := ctrl.Client.MemberProject.Query().
			Where(memberproject.MemberID(member.ID)).
			Select(memberproject.FieldProjectID).
			Ints(ctx.Context())
		if err != nil {
			return muzak.ServiceUnavailable("cannot read your project access right now").Wrap(err)
		}
		reachable = ids
	}

	systemRole := ""
	if member.Edges.Role.System {
		systemRole = member.Edges.Role.Key
	}

	p.Organization = org
	p.Member = member
	p.Actor = authz.UserActor(org.ID, p.User.ID, systemRole, permissions, member.AllProjects, reachable)

	// Set on the actor rather than checked at each call site, so a route that forgets is
	// a route that is still safe: every permission that changes something is withheld,
	// including from an owner.
	if !p.Claims.EmailVerified {
		p.Actor.ReadOnly = true
		p.Actor.ReadOnlyReason = VerifyEmailFirst
	}

	return p.enterProject(ctx, ctrl)
}

// enterProject resolves the project in the path, when the route names one.
func (p *Principal) enterProject(ctx *muzak.Context, ctrl *Global) error {
	slug := ctx.PathValue(ProjectPathValue)
	if slug == "" {
		return nil
	}

	proj, err := ctrl.Client.Project.Query().
		Where(project.Slug(slug), project.OrganizationID(p.Organization.ID)).
		Only(ctx.Context())
	if err != nil {
		if global.IsNotFound(err) {
			return muzak.NotFound("no such project")
		}
		return muzak.ServiceUnavailable("cannot read that project right now").Wrap(err)
	}

	// The access list, checked before the permission. A project somebody cannot see must
	// be indistinguishable from one that does not exist — that is the whole of "the owner
	// chooses which people see which apps", and answering 403 here would leak the names of
	// the games they were not shown.
	if !p.Actor.Reaches(proj.ID) {
		return muzak.NotFound("no such project")
	}

	p.Project = proj
	return nil
}

// Authorized is proof that a permission check ran.
//
// A handler does not have to hold it — the check already happened, and the request would not
// have reached the handler otherwise. It exists because [Require] has to produce *something*
// to be a provider, and a named type reads better in a stack trace than an empty struct.
type Authorized struct{ Principal *Principal }

// Require declares the permissions a route needs.
//
// A provider rather than a guard, and not by preference: muzak runs **every guard before every
// provider**, so a guard cannot read the [Principal] that [ResolveUser] produced. The
// framework's own documentation says to move such a check into a provider of its own, which is
// what this is.
//
//	r.Post("/organizations/{org}/projects/{project}/keys", handlers.CreateAPIKey,
//	    core.Require(authz.KeyCreate))
//
// **Declare it once per route, with every permission it needs.** Muzak keeps one provider per
// produced type and the most recent declaration wins, so two separate Require calls on one
// route would silently run only the second — which is why this is variadic and why a route
// needing two permissions passes both here.
func Require(permissions ...authz.Permission) muzak.SharedOption {
	return muzak.Needs(func(ctx *muzak.Context) (Authorized, error) {
		principal := muzak.From[*Principal](ctx)

		for _, permission := range permissions {
			projectID := 0
			if permission.Scope() == authz.ScopeProject {
				if principal.Project == nil {
					// The route asked for a project-scoped permission on a path
					// that names no project. A wiring mistake, and it fails
					// closed rather than being answered as "no".
					return Authorized{}, muzak.InternalServerError(
						"this route checks a project permission but names no project")
				}
				projectID = principal.Project.ID
			}
			if err := principal.Actor.Require(permission, projectID); err != nil {
				// The permission is named. The caller is a studio's own developer
				// reading their own dashboard, not an attacker probing for what
				// exists, and "forbidden" with no reason is a support ticket.
				return Authorized{}, muzak.Forbidden(err.Error())
			}
		}
		return Authorized{Principal: principal}, nil
	})
}

// RequireVerified declares that a route changes something but has no permission to hang that
// on.
//
// There is exactly one today — creating an organisation, which happens *before* there is an
// organisation to hold a role — and it is the hole a read-only account would otherwise walk
// through, because [Actor.ReadOnly] can only withhold permissions that are checked.
//
// Not a general-purpose "must be verified" guard. Anything with a permission gets this for
// free and should use [Require] instead; adding it beside one would also collide, since muzak
// keeps one provider per produced type.
func RequireVerified() muzak.SharedOption {
	return muzak.Needs(func(ctx *muzak.Context) (Authorized, error) {
		principal := muzak.From[*Principal](ctx)
		if !principal.Claims.EmailVerified {
			return Authorized{}, muzak.Forbidden(VerifyEmailFirst +
				", so it cannot create anything yet; check your inbox for a link")
		}
		return Authorized{Principal: principal}, nil
	})
}

// RequireRoleManagement declares that a route creates roles or assigns them.
//
// Not a permission, deliberately: a grantable "manage roles" is silently equivalent to owner,
// because whoever holds it can write themselves a role containing everything. It is reserved
// to the two system roles instead, which removes the privilege-escalation question rather than
// defending against it.
func RequireRoleManagement() muzak.SharedOption {
	return muzak.Needs(func(ctx *muzak.Context) (Authorized, error) {
		principal := muzak.From[*Principal](ctx)
		if !principal.Actor.ManagesRoles() {
			return Authorized{}, muzak.Forbidden("only an owner or an admin can manage roles")
		}
		return Authorized{Principal: principal}, nil
	})
}

// ErrNoOrganization is returned by helpers that need one and were given a principal without.
var ErrNoOrganization = errors.New("this request names no organization")
