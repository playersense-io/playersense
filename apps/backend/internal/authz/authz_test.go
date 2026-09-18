package authz_test

import (
	"strings"
	"testing"

	"playersense.dev/backend/internal/authz"
)

const (
	orgID     = 1
	gameA     = 10
	gameB     = 20
	someUser  = 100
	otherUser = 200
)

func role(key string) []authz.Permission {
	for _, template := range authz.Templates() {
		if template.Key == key {
			return template.Seed()
		}
	}
	return nil
}

// Hasan was explicit: a LiveOps manager is LiveOps and nothing else. A role whose name says
// what it does and then does more is a role nobody can reason about.
func TestALiveOpsManagerIsOnlyLiveOps(t *testing.T) {
	manager := authz.UserActor(orgID, someUser, "", role(authz.RoleLiveOpsManager), true, nil)

	for _, allowed := range []authz.Permission{
		authz.LiveOpsRead, authz.LiveOpsWrite, authz.LiveOpsPublish,
	} {
		if !manager.Can(allowed, gameA) {
			t.Errorf("a LiveOps manager cannot %s", allowed)
		}
	}

	for _, refused := range []authz.Permission{
		authz.AnalyticsRead, authz.KeyRead, authz.KeyCreate,
		authz.PrivacyErase, authz.PrivacyExport, authz.ProjectDelete,
	} {
		if manager.Can(refused, gameA) {
			t.Errorf("a LiveOps manager can %s, and should not", refused)
		}
	}
	for _, refused := range []authz.Permission{
		authz.BillingRead, authz.BillingManage, authz.MemberInvite, authz.AuditRead,
	} {
		if manager.Can(refused, 0) {
			t.Errorf("a LiveOps manager can %s, and should not", refused)
		}
	}
}

// Drafting is reversible and publishing is not — players have already seen it. That split is
// what replaces the per-environment permission, so it has to actually hold.
func TestADeveloperDraftsAndCannotPublish(t *testing.T) {
	developer := authz.UserActor(orgID, someUser, "", role(authz.RoleDeveloper), true, nil)

	if !developer.Can(authz.LiveOpsWrite, gameA) {
		t.Error("a developer cannot draft")
	}
	if developer.Can(authz.LiveOpsPublish, gameA) {
		t.Error("a developer can publish to live players")
	}
}

// What you may do is the role; where you may do it is the access list. A role that reached
// every project would make "hide this game from this person" impossible to express.
func TestARoleDoesNotReachAProjectItWasNotGiven(t *testing.T) {
	// Full LiveOps rights, but only in game A.
	scoped := authz.UserActor(orgID, someUser, "", role(authz.RoleLiveOpsManager), false, []int{gameA})

	if !scoped.Can(authz.LiveOpsPublish, gameA) {
		t.Error("cannot publish to the project they were given")
	}
	if scoped.Can(authz.LiveOpsPublish, gameB) {
		t.Error("published to a project they were never given")
	}

	// And the same role with everything open does reach both, so the test is about the
	// access list rather than about the role.
	open := authz.UserActor(orgID, someUser, "", role(authz.RoleLiveOpsManager), true, nil)
	if !open.Can(authz.LiveOpsPublish, gameB) {
		t.Error("an all-projects member could not reach a project")
	}
}

// Asking "may they publish" without saying where is not a question with an answer.
func TestScopeMismatchesAreRefusedRatherThanGuessed(t *testing.T) {
	owner := authz.UserActor(orgID, someUser, authz.RoleOwner, role(authz.RoleOwner), true, nil)

	// A project-scoped permission with no project.
	if owner.Can(authz.LiveOpsPublish, 0) {
		t.Error("a project permission was granted without naming a project")
	}
	// An organisation-scoped permission with one. A call site that passes a project here
	// has misunderstood something, and answering would hide it.
	if owner.Can(authz.BillingManage, gameA) {
		t.Error("an organisation permission was answered for a project")
	}
	if !owner.Can(authz.BillingManage, 0) {
		t.Error("an owner cannot manage billing")
	}
}

// An owner may do everything, and an admin everything except destroy the organisation.
// Computed rather than listed, so a permission added tomorrow reaches them without anybody
// remembering to edit a file.
func TestSystemRolesFollowTheVocabulary(t *testing.T) {
	owner := authz.UserActor(orgID, someUser, authz.RoleOwner, authz.SystemPermissions(authz.RoleOwner), true, nil)
	admin := authz.UserActor(orgID, otherUser, authz.RoleAdmin, authz.SystemPermissions(authz.RoleAdmin), true, nil)

	for _, permission := range authz.All() {
		project := 0
		if permission.Scope() == authz.ScopeProject {
			project = gameA
		}
		if !owner.Can(permission, project) {
			t.Errorf("an owner cannot %s", permission)
		}

		if permission == authz.OrgDelete {
			if admin.Can(permission, project) {
				t.Error("an admin can delete the organisation")
			}
			continue
		}
		if !admin.Can(permission, project) {
			t.Errorf("an admin cannot %s", permission)
		}
	}
}

// Role management is reserved rather than granted, which is what removes the escalation
// question: a grantable "manage roles" is silently equivalent to owner.
func TestOnlyTheSystemRolesManageRoles(t *testing.T) {
	for _, systemRole := range []string{authz.RoleOwner, authz.RoleAdmin} {
		actor := authz.UserActor(orgID, someUser, systemRole, authz.SystemPermissions(systemRole), true, nil)
		if !actor.ManagesRoles() {
			t.Errorf("%s cannot manage roles", systemRole)
		}
	}

	// Even a custom role holding every permission in the vocabulary cannot.
	everything := authz.UserActor(orgID, someUser, "", authz.All(), true, nil)
	if everything.ManagesRoles() {
		t.Error("a custom role with every permission can manage roles; that is owner by another name")
	}
}

// The vocabulary is closed. A typo is refused when the role is saved, so somebody sees it
// while looking at the role that caused it — not as a campaign that reaches nobody.
func TestAnUnknownPermissionIsRefusedWhenSaved(t *testing.T) {
	err := authz.Validate([]authz.Permission{authz.LiveOpsRead, "liveops.publsh"})
	if err == nil {
		t.Fatal("a misspelled permission was accepted")
	}
	if !strings.Contains(err.Error(), "liveops.publsh") {
		t.Errorf("the error does not name the typo: %v", err)
	}

	if err := authz.Validate(authz.All()); err != nil {
		t.Errorf("the vocabulary does not validate against itself: %v", err)
	}
	if err := authz.Validate([]authz.Permission{authz.LiveOpsRead, authz.LiveOpsRead}); err == nil {
		t.Error("a duplicate permission was accepted")
	}

	// And an unknown permission never grants anything, whatever an actor carries.
	actor := authz.UserActor(orgID, someUser, authz.RoleOwner, []authz.Permission{"liveops.publsh"}, true, nil)
	if actor.Can("liveops.publsh", gameA) {
		t.Error("an unknown permission granted access")
	}
}

// Every permission belongs to a namespace, which is what a role editor groups by and what
// Hasan meant by tool-level access.
func TestEveryPermissionIsNamespaced(t *testing.T) {
	for _, permission := range authz.All() {
		namespace := permission.Namespace()
		if namespace == "" || namespace == string(permission) {
			t.Errorf("%q has no namespace", permission)
		}
	}
	if authz.LiveOpsPublish.Namespace() != "liveops" {
		t.Errorf("namespace = %q", authz.LiveOpsPublish.Namespace())
	}
}

// An actor with no organisation is not a member of anything.
func TestAnActorWithNoOrganisationCanDoNothing(t *testing.T) {
	nobody := authz.Actor{Permissions: map[authz.Permission]bool{authz.LiveOpsPublish: true}, AllProjects: true}
	if nobody.Can(authz.LiveOpsPublish, gameA) {
		t.Error("an actor with no organisation was granted access")
	}
}

// A refusal says which permission was missing: the caller is a studio's own developer reading
// their own dashboard, and "forbidden" with no reason is a support ticket.
func TestARefusalNamesThePermission(t *testing.T) {
	growth := authz.UserActor(orgID, someUser, "", role(authz.RoleGrowthManager), true, nil)

	err := growth.Require(authz.LiveOpsPublish, gameA)
	if err == nil {
		t.Fatal("a growth manager was allowed to publish")
	}
	if !strings.Contains(err.Error(), string(authz.LiveOpsPublish)) {
		t.Errorf("the refusal does not name the permission: %v", err)
	}
	if err := growth.Require(authz.AnalyticsRead, gameA); err != nil {
		t.Errorf("a growth manager cannot read analytics: %v", err)
	}
}
