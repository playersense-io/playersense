package authz

import "fmt"

// Actor is whoever is asking. Two kinds reach a handler and they are authorised differently.
//
// A **user** signed in through Firebase carries a role and a project access list. An **API
// key** carries neither: its privileges come from its kind, fixed when it was minted, and it
// is already scoped to one environment of one project. Keeping them in one type means a
// handler asks the same question whoever is calling.
type Actor struct {
	// OrganizationID is which organisation this actor is acting inside. Zero means none,
	// and nothing is permitted.
	OrganizationID int

	// UserID is set for a signed-in person, zero for a key.
	UserID int

	// Permissions is what their role grants. Empty for a key.
	Permissions map[Permission]bool

	// AllProjects, and Projects when it is false, are the second axis: what they may do is
	// the role, where they may do it is this.
	AllProjects bool
	Projects    map[int]bool

	// SystemRole is "owner" or "admin" when the person holds one, and empty otherwise.
	// Role and member management is reserved to these two rather than being a permission —
	// a grantable "manage roles" is silently equivalent to owner, because whoever holds it
	// can write themselves a role containing everything.
	SystemRole string

	// KeyKind is set for an API key: project, reporting or server.
	KeyKind string

	// ReadOnly withholds every permission that changes something, whatever the role says.
	//
	// It is an account-level state rather than a role, because it is not about what this
	// person was hired to do: today it is set for somebody whose email address Firebase
	// has not confirmed, and it is the shape a suspended bill would want too. They can
	// sign in, open the dashboard and see their organisation; they simply cannot act.
	ReadOnly bool

	// ReadOnlyReason is what to tell them, because "forbidden" when the role plainly
	// grants it is a support ticket. Set beside ReadOnly and shown in the refusal.
	ReadOnlyReason string
}

// UserActor builds an actor from a person's membership.
func UserActor(organizationID, userID int, systemRole string, permissions []Permission, allProjects bool, projects []int) Actor {
	actor := Actor{
		OrganizationID: organizationID,
		UserID:         userID,
		SystemRole:     systemRole,
		AllProjects:    allProjects,
		Permissions:    make(map[Permission]bool, len(permissions)),
		Projects:       make(map[int]bool, len(projects)),
	}
	for _, permission := range permissions {
		actor.Permissions[permission] = true
	}
	for _, project := range projects {
		actor.Projects[project] = true
	}
	return actor
}

// Can reports whether an actor may do something, in a project when the permission names one.
//
// `projectID` is ignored for an organisation-scoped permission and **required** for a
// project-scoped one: asking "may they publish" without saying where is not a question with an
// answer, so it is refused rather than guessed.
func (a Actor) Can(permission Permission, projectID int) bool {
	if a.OrganizationID == 0 || !permission.Known() {
		return false
	}
	// Checked before the role, not after. A read-only account holding OWNER must be
	// refused by this and not reach a line that says "owner may do everything".
	if a.ReadOnly && permission.Mutates() {
		return false
	}

	switch permission.Scope() {
	case ScopeOrganization:
		// A project is meaningless here. Passing one is a call-site mistake rather than a
		// permission question, and answering it would hide the mistake.
		if projectID != 0 {
			return false
		}
		return a.Permissions[permission]

	case ScopeProject:
		if projectID == 0 {
			return false
		}
		return a.Permissions[permission] && a.Reaches(projectID)
	}
	return false
}

// Reaches reports whether a project is inside the actor's access list.
//
// The separate axis. A LiveOps manager with `liveops.publish` and no access to a project
// cannot publish to it, and that is the whole of "hide some projects from some people" —
// there is no `project.read` permission to get out of step with it.
//
// Exported because visibility is asked on its own, before any permission: a project somebody
// cannot see has to be answered as "no such project", not as "you may not do that there", or
// the refusal leaks the names of the games they were not shown.
func (a Actor) Reaches(projectID int) bool {
	if a.AllProjects {
		return true
	}
	return a.Projects[projectID]
}

// ManagesRoles reports whether this actor may create roles and assign them.
//
// Not a permission, deliberately. See the comment on Actor.SystemRole.
func (a Actor) ManagesRoles() bool {
	return a.SystemRole == RoleOwner || a.SystemRole == RoleAdmin
}

// Require returns the error a handler gives when an actor may not do something.
//
// One message for every refusal, naming the permission. Naming it is a deliberate choice: the
// caller is a studio's own developer reading their own dashboard, not an attacker probing for
// what exists, and "forbidden" with no reason is a support ticket.
func (a Actor) Require(permission Permission, projectID int) error {
	if a.Can(permission, projectID) {
		return nil
	}
	if !permission.Known() {
		return fmt.Errorf("unknown permission %q", permission)
	}
	// Two very different refusals, and telling them apart is the difference between a
	// banner somebody can act on and a support ticket. The role is wrong here; there is
	// nothing this person can do about it but ask an owner.
	if a.ReadOnly && permission.Mutates() {
		reason := a.ReadOnlyReason
		if reason == "" {
			reason = "this account is read-only"
		}
		return fmt.Errorf("%s, so it cannot %s", reason, permission)
	}
	return fmt.Errorf("this account does not have %s", permission)
}
