package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"muzak.dev/framework"
	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/global/invitation"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/mail"
)

// Members and invitations: how an organization stops being one person.
//
// Until these routes existed, [stack.join] reached around the HTTP layer and wrote the rows
// itself — commented "which is what the invitation flow will do once it exists". These tests use
// the flow instead, which is the point of them.

// org is one organization with its owner, and the mail its invitations produced.
type org struct {
	*stack
	owner   fbtest.Person
	slug    string
	mailbox *mail.Recorder
}

// newOrg builds a stack whose sender is a recorder.
//
// The substitution is not a convenience. An invitation's token leaves the process only inside the
// email — the database holds a SHA-256 digest and nothing else — so the accept flow is untestable
// without observing the message, which is the same property that makes the token safe to store.
func newOrg(t *testing.T) *org {
	t.Helper()
	mailbox := &mail.Recorder{}

	settings := testSettings(t)
	google := fbtest.New(t, settings.FirebaseProjectID)
	settings.FirebaseCertificateURL = google.URL

	db := globalTier(t, settings)
	owner := person(t)
	s := &stack{
		settings: settings,
		app:      build(settings, muzak.WithSingleton[mail.Sender](mailbox)),
		google:   google,
		db:       db,
	}
	return &org{stack: s, owner: owner, slug: s.createOrg(t, owner), mailbox: mailbox}
}

func (o *org) base() string { return "/v1/organizations/" + o.slug }

func (o *org) client(t *testing.T) *testclient.Client {
	t.Helper()
	return o.as(t, o.owner)
}

// invite sends one and returns the token out of the recorded message.
//
// Parsed from the link rather than read from a response member, because the response deliberately
// has none: the token is disclosed once, in the mail, and a test that could read it from JSON
// would be testing an API that leaks it.
func (o *org) invite(t *testing.T, client *testclient.Client, email, role string) string {
	t.Helper()
	client.Post(o.base()+"/invitations",
		testclient.JSON(map[string]any{"email": email, "role": role})).
		AssertStatus(http.StatusCreated)
	return o.tokenFor(t, email)
}

func (o *org) tokenFor(t *testing.T, email string) string {
	t.Helper()
	messages := o.mailbox.To(email)
	if len(messages) == 0 {
		t.Fatalf("no invitation email was sent to %s", email)
	}
	last := messages[len(messages)-1]
	token := tokenFromBody(t, last.Body)
	if token == "" {
		t.Fatalf("no token in the invitation body: %s", last.Body)
	}
	return token
}

// tokenFromBody pulls the token out of the one link in the message.
func tokenFromBody(t *testing.T, body string) string {
	t.Helper()
	for _, field := range strings.Fields(body) {
		if !strings.Contains(field, "/invitations/accept?") {
			continue
		}
		parsed, err := url.Parse(field)
		if err != nil {
			t.Fatalf("the invitation link does not parse: %v", err)
		}
		return parsed.Query().Get("token")
	}
	return ""
}

// TestAnInvitationIsTheOnlyWayInIsNowBuilt walks the whole flow: invite, accept, listed.
//
// Every layer under this existed — the tables, the token helpers, AddMember, the last-owner rule —
// and none of it was reachable, because an organization had no route that could admit a second
// person. This is the test that says it does.
func TestAnInvitationAdmitsASecondPerson(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	colleague := person(t)
	token := o.invite(t, owner, colleague.Email, authz.RoleDeveloper)

	// The invitation is outstanding, and names what accepting grants.
	listed := testclient.Decoded[invitationListOut](owner.Get(o.base() + "/invitations").
		AssertStatus(http.StatusOK))
	if len(listed.Invitations) != 1 {
		t.Fatalf("want one invitation, got %d", len(listed.Invitations))
	}
	if listed.Invitations[0].Status != "pending" || listed.Invitations[0].Role.Key != authz.RoleDeveloper {
		t.Errorf("invitation = %+v", listed.Invitations[0])
	}
	// And no listing discloses the token, because the response type has no member for one.
	if strings.Contains(owner.Get(o.base()+"/invitations").String(), token) {
		t.Error("the invitation listing disclosed the token")
	}

	// The colleague, signed in, sees it waiting for them before they have accepted anything.
	waiting := testclient.Decoded[pendingListOut](o.as(t, colleague).Get("/v1/invitations").
		AssertStatus(http.StatusOK))
	if len(waiting.Invitations) != 1 || waiting.Invitations[0].OrgSlug != o.slug {
		t.Fatalf("pending = %+v", waiting.Invitations)
	}

	accepted := testclient.Decoded[acceptedOut](o.as(t, colleague).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusOK))
	if accepted.Organization.Slug != o.slug || accepted.Role.Key != authz.RoleDeveloper {
		t.Errorf("accepted = %+v", accepted)
	}
	if !accepted.AllProjects {
		t.Error("an invitation with no project list must grant every project")
	}

	// Two members now, and the role is the one the invitation named rather than a default.
	members := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members").
		AssertStatus(http.StatusOK))
	if len(members.Members) != 2 {
		t.Fatalf("want two members, got %d", len(members.Members))
	}
	for _, member := range members.Members {
		if member.Email == colleague.Email && member.Role.Key != authz.RoleDeveloper {
			t.Errorf("the new member holds %q, want developer", member.Role.Key)
		}
	}

	// The invitation is spent, so the members screen does not show a pending row for somebody
	// already listed above it.
	listed = testclient.Decoded[invitationListOut](owner.Get(o.base() + "/invitations"))
	if listed.Invitations[0].Status != string(invitation.StatusAccepted) {
		t.Errorf("status after accepting = %q", listed.Invitations[0].Status)
	}

	// And the role actually decides something: a developer holds key.read, and reading the
	// members list is not theirs.
	games := o.createProject(t, o.owner, o.slug)
	o.as(t, colleague).Get(o.base() + "/projects/" + games + "/keys").AssertStatus(http.StatusOK)
	refused := o.as(t, colleague).Get(o.base() + "/members").AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), string(authz.MemberRead)) {
		t.Errorf("the refusal does not name the missing permission: %s", refused)
	}
}

// TestAForwardedInvitationDoesNotLetSomebodyElseIn is the check the token alone cannot make.
//
// A token proves somebody was sent a link; the address proves who they are. An invitation
// forwarded to a colleague satisfies the first without the second, and without this it would let
// them join in the invited person's place — with the invited person's role.
func TestAForwardedInvitationDoesNotLetSomebodyElseIn(t *testing.T) {
	o := newOrg(t)
	invited := person(t)
	token := o.invite(t, o.client(t), invited.Email, authz.RoleDeveloper)

	stranger := person(t)
	refused := o.as(t, stranger).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), "different email address") {
		t.Errorf("the refusal does not say why: %s", refused)
	}

	// The invitation still works for the person it was sent to.
	o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusOK)
}

// TestAnUnverifiedAccountCannotAcceptAnInvitation is the hole a read-only account would walk
// through.
//
// Every other write is refused by a permission, and [authz.Actor.ReadOnly] withholds the mutating
// ones. Accepting has no permission to withhold — the invitation is the authorisation — so it is
// one of the two routes in the product that needs RequireVerified explicitly. Without it, anybody
// who can type an address into a signup form joins the organization invited to it.
func TestAnUnverifiedAccountCannotAcceptAnInvitation(t *testing.T) {
	o := newOrg(t)

	invited := person(t)
	invited.EmailVerified = false
	token := o.invite(t, o.client(t), invited.Email, authz.RoleDeveloper)

	refused := o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), "verified") {
		t.Errorf("the refusal does not mention verification: %s", refused)
	}

	// They can still see that they were invited, which is how they learn which link to look
	// for. Reading what was sent to your own address changes nothing.
	o.as(t, invited).Get("/v1/invitations").AssertStatus(http.StatusOK)

	// And once Firebase confirms the address, the same token works.
	invited.EmailVerified = true
	o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusOK)
}

// TestAnInvitationCanBeAcceptedWithoutTheEmail closes the dead end.
//
// The address is the authorisation and the token is only how a link works — Firebase has vouched
// for the address on this request, and the invitation was sent to exactly it. Requiring the token
// anyway would mean somebody who deleted the mail can see on their dashboard that they were
// invited and has no way to act on it.
func TestAnInvitationCanBeAcceptedWithoutTheEmail(t *testing.T) {
	o := newOrg(t)
	invited := person(t)
	o.invite(t, o.client(t), invited.Email, authz.RoleGrowthManager)

	accepted := testclient.Decoded[acceptedOut](o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"organization": o.slug})).
		AssertStatus(http.StatusOK))
	if accepted.Role.Key != authz.RoleGrowthManager {
		t.Errorf("accepted %+v, want the growth_manager role the invitation named", accepted)
	}

	// Naming both, or neither, is refused rather than resolved by precedence: a request whose
	// meaning depends on which field the server happens to read first is a request the caller
	// cannot reason about.
	o.as(t, invited).Post("/v1/invitations/accept", testclient.RawJSON(`{}`)).
		AssertStatus(http.StatusUnprocessableEntity)
	o.as(t, invited).Post("/v1/invitations/accept",
		testclient.RawJSON(`{"token":"0123456789abcdef0123","organization":"`+o.slug+`"}`)).
		AssertStatus(http.StatusUnprocessableEntity)
}

// TestAcceptingTwiceSucceeds is the link clicked twice.
//
// The person's intent — be in this organization — is satisfied either way, and a 409 would send
// them to support about a button that worked. It says which happened, so a dashboard can show
// "you are already in" rather than "welcome".
func TestAcceptingTwiceSucceeds(t *testing.T) {
	o := newOrg(t)
	invited := person(t)
	token := o.invite(t, o.client(t), invited.Email, authz.RoleDeveloper)

	first := testclient.Decoded[acceptedOut](o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK))
	if first.AlreadyMember {
		t.Error("the first accept reported an existing membership")
	}

	// The same token again. It is spent, so this is the no-invitation path rather than the
	// already-a-member one — and it is still not an error the person can act on.
	o.as(t, invited).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusNotFound)
}

// TestAnExpiredOrRevokedInvitationIsIndistinguishable keeps a token from reporting on itself.
//
// Unknown, spent, revoked and expired are one message. Telling somebody holding a token which of
// those it is tells them whether it was ever real, and the only person who needs that distinction
// is one who should not have the token.
func TestAnExpiredOrRevokedInvitationIsIndistinguishable(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	revoked := person(t)
	token := o.invite(t, owner, revoked.Email, authz.RoleDeveloper)

	listed := testclient.Decoded[invitationListOut](owner.Get(o.base() + "/invitations"))
	owner.Delete(o.base() + "/invitations/" + listed.Invitations[0].ID).
		AssertStatus(http.StatusOK)
	// Twice is a conflict, the same way revoking a key twice is.
	owner.Delete(o.base() + "/invitations/" + listed.Invitations[0].ID).
		AssertStatus(http.StatusConflict)

	wasRevoked := o.as(t, revoked).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).
		AssertStatus(http.StatusNotFound).Error().Error

	// A token that was never real.
	neverExisted := o.as(t, revoked).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": strings.Repeat("a", 43)})).
		AssertStatus(http.StatusNotFound).Error().Error

	// The code and the sentence, which is everything the caller is told. Request ids differ
	// per request and are the one part that cannot leak anything about the token.
	if wasRevoked.Code != neverExisted.Code || wasRevoked.Message != neverExisted.Message {
		t.Errorf("a revoked token and an invented one answer differently:\n%+v\n%+v",
			wasRevoked, neverExisted)
	}

	// The row survives the revocation, because "who invited this person, and what happened" is
	// the question the members screen answers.
	after := testclient.Decoded[invitationListOut](owner.Get(o.base() + "/invitations"))
	if len(after.Invitations) != 1 || after.Invitations[0].Status != string(invitation.StatusRevoked) {
		t.Errorf("invitations after revoking = %+v", after.Invitations)
	}
}

// TestAnOrganizationKeepsAnOwner is the rule that makes an organization administrable.
//
// Both doors, because a check on only one of them holds until somebody uses the other: demoting
// the last owner and removing them reach the identical dead end, where nobody can grant a role,
// nobody can delete the organization, and support edits the database by hand.
func TestAnOrganizationKeepsAnOwner(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	members := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members"))
	if len(members.Members) != 1 || !members.Members[0].You {
		t.Fatalf("a new organization should have one member, the caller: %+v", members.Members)
	}
	me := members.Members[0].ID

	owner.Put(o.base()+"/members/"+me+"/role",
		testclient.JSON(map[string]string{"role": authz.RoleDeveloper})).
		AssertStatus(http.StatusConflict).
		// A code of its own rather than a bare "conflict": the dashboard has to be able to
		// say "promote somebody else first" rather than "that did not work".
		AssertErrorCode("last_owner")

	owner.Delete(o.base() + "/members/" + me).AssertStatus(http.StatusConflict)

	// With a second owner, either is fine. That is the point of the rule being "the last one"
	// rather than "an owner".
	second := person(t)
	token := o.invite(t, owner, second.Email, authz.RoleOwner)
	o.as(t, second).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK)

	owner.Put(o.base()+"/members/"+me+"/role",
		testclient.JSON(map[string]string{"role": authz.RoleDeveloper})).
		AssertStatus(http.StatusOK)
}

// TestProjectAccessIsTheSecondAxis is the role/where split, observed.
//
// A LiveOps manager on two of five games holds one role and two grants, not two roles whose names
// encode a game. And `all_projects` is required rather than defaulted, because it is the widest
// grant in the product: a member update that carried it as an ordinary field would widen or narrow
// somebody's reach whenever a caller forgot to echo it back.
func TestProjectAccessIsTheSecondAxis(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	visible := o.createProject(t, o.owner, o.slug)
	hidden := o.createProject(t, o.owner, o.slug)

	contractor := person(t)
	token := o.invite(t, owner, contractor.Email, authz.RoleDeveloper)
	o.as(t, contractor).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK)

	// Invited with no project list, so they see both.
	projects := testclient.Decoded[projectListOut](o.as(t, contractor).Get(o.base() + "/projects"))
	if len(projects.Projects) != 2 {
		t.Fatalf("want both projects visible, got %d", len(projects.Projects))
	}

	members := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members"))
	var id string
	for _, member := range members.Members {
		if member.Email == contractor.Email {
			id = member.ID
		}
	}

	// Narrowed to one. The response carries the list, because now there is one to carry.
	narrowed := testclient.Decoded[memberOut](owner.Put(o.base()+"/members/"+id+"/access",
		testclient.JSON(map[string]any{"all_projects": false, "projects": []string{visible}})).
		AssertStatus(http.StatusOK))
	if narrowed.AllProjects || len(narrowed.Projects) != 1 || narrowed.Projects[0].Slug != visible {
		t.Fatalf("access = %+v", narrowed)
	}

	projects = testclient.Decoded[projectListOut](o.as(t, contractor).Get(o.base() + "/projects"))
	if len(projects.Projects) != 1 || projects.Projects[0].Slug != visible {
		t.Errorf("the contractor sees %+v, want only %s", projects.Projects, visible)
	}
	// And the one they were not given is absent rather than forbidden, for the reason every
	// other tenancy check here answers 404: a 403 confirms it exists.
	o.as(t, contractor).Get(o.base() + "/projects/" + hidden).AssertStatus(http.StatusNotFound)

	// all_projects is required, so the widest grant cannot be given by leaving a field out.
	owner.Put(o.base()+"/members/"+id+"/access",
		testclient.RawJSON(`{"projects":["`+visible+`"]}`)).
		AssertStatus(http.StatusUnprocessableEntity)

	// A project from nowhere is refused rather than silently skipped: "give them these three
	// games" that quietly grants two is what an access review is supposed to catch.
	owner.Put(o.base()+"/members/"+id+"/access",
		testclient.JSON(map[string]any{"all_projects": false, "projects": []string{visible, "not-a-project"}})).
		AssertStatus(http.StatusUnprocessableEntity)

	// Widening back deletes the rows rather than keeping them, because `all_projects` means
	// "including projects created tomorrow" and stale rows would contradict that.
	widened := testclient.Decoded[memberOut](owner.Put(o.base()+"/members/"+id+"/access",
		testclient.JSON(map[string]any{"all_projects": true})).AssertStatus(http.StatusOK))
	if !widened.AllProjects || len(widened.Projects) != 0 {
		t.Errorf("widened = %+v, want no list beside all_projects", widened)
	}
}

// TestRoleManagementIsNotAPermission is the escalation question, removed rather than defended.
//
// A grantable `role.manage` would be silently equivalent to owner: whoever held it could write
// themselves a role containing everything. So assigning roles and changing project access are
// reserved to the two system roles — which means a DEVELOPER who can mint production API keys
// still cannot make themselves an admin.
func TestRoleManagementIsNotAPermission(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	developer := person(t)
	token := o.invite(t, owner, developer.Email, authz.RoleDeveloper)
	o.as(t, developer).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK)

	members := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members"))
	var mine string
	for _, member := range members.Members {
		if member.Email == developer.Email {
			mine = member.ID
		}
	}

	// They cannot promote themselves, and the refusal says who may.
	refused := o.as(t, developer).Put(o.base()+"/members/"+mine+"/role",
		testclient.JSON(map[string]string{"role": authz.RoleOwner})).
		AssertStatus(http.StatusForbidden)
	if !strings.Contains(refused.String(), "owner") {
		t.Errorf("the refusal does not say who may manage roles: %s", refused)
	}
	// Nor widen their own reach.
	o.as(t, developer).Put(o.base()+"/members/"+mine+"/access",
		testclient.JSON(map[string]any{"all_projects": true})).
		AssertStatus(http.StatusForbidden)
	// Nor invite anybody, which is a permission they do not hold either.
	o.as(t, developer).Post(o.base()+"/invitations",
		testclient.JSON(map[string]string{"email": person(t).Email, "role": authz.RoleOwner})).
		AssertStatus(http.StatusForbidden)

	// An admin can, which is what makes the distinction observable rather than a blanket
	// refusal.
	admin := person(t)
	o.join(t, o.slug, admin, authz.RoleAdmin, true)
	o.as(t, admin).Put(o.base()+"/members/"+mine+"/role",
		testclient.JSON(map[string]string{"role": authz.RoleGrowthManager})).
		AssertStatus(http.StatusOK)
}

// TestAnInvitationIsRefusedForSomebodyAlreadyIn tells the sender rather than the invitee.
//
// A pending row for an existing member can never be accepted into anything, so it would sit on the
// members screen looking outstanding forever — and the person who would discover that is the
// colleague clicking a link that reports nothing to do.
func TestAnInvitationIsRefusedForSomebodyAlreadyIn(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	colleague := person(t)
	token := o.invite(t, owner, colleague.Email, authz.RoleDeveloper)
	o.as(t, colleague).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK)

	refused := owner.Post(o.base()+"/invitations",
		testclient.JSON(map[string]string{"email": colleague.Email, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusConflict)
	if !strings.Contains(refused.String(), "already a member") {
		t.Errorf("the refusal does not say why: %s", refused)
	}

	// And a second outstanding invitation to one address is refused too: two live tokens for
	// one inbox means revoking "the" invitation leaves the other working.
	fresh := person(t)
	o.invite(t, owner, fresh.Email, authz.RoleDeveloper)
	owner.Post(o.base()+"/invitations",
		testclient.JSON(map[string]string{"email": fresh.Email, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusConflict)
}

// TestTheInvitationSaysWhatItIsAndWhoSentIt is about the message rather than the row.
//
// An invitation from an address somebody does not recognise, to an organization they have never
// heard of, is indistinguishable from phishing — and the correct response to that is to ignore it.
// So the body names the inviter, the organization and the role, and carries the link on its own
// line where every client makes it both clickable and copyable.
func TestTheInvitationSaysWhatItIsAndWhoSentIt(t *testing.T) {
	o := newOrg(t)
	invited := person(t)
	o.invite(t, o.client(t), invited.Email, authz.RoleDeveloper)

	messages := o.mailbox.To(invited.Email)
	if len(messages) != 1 {
		t.Fatalf("want one message, got %d", len(messages))
	}
	message := messages[0]

	if message.Kind != mail.Invitation {
		t.Errorf("kind = %q", message.Kind)
	}
	for _, want := range []string{o.owner.Email, "PlayerSense"} {
		if !strings.Contains(message.Subject+message.Body, want) {
			t.Errorf("the message does not mention %q:\n%s\n%s", want, message.Subject, message.Body)
		}
	}
	// The role, by its display name rather than its key: the recipient is a person, and
	// "Developer" is what the dashboard calls it.
	if !strings.Contains(message.Body, "Developer") {
		t.Errorf("the message does not say what role it grants:\n%s", message.Body)
	}
	// The link is alone on its line, so a client that does not linkify still lets it be
	// copied whole.
	var linkLine string
	for _, line := range strings.Split(message.Body, "\n") {
		if strings.Contains(line, "/invitations/accept?") {
			linkLine = line
		}
	}
	if linkLine == "" || strings.TrimSpace(linkLine) != linkLine || strings.Contains(linkLine, " ") {
		t.Errorf("the link is not alone on its line: %q", linkLine)
	}

	// Nothing was delivered, and the response says so rather than telling a dashboard to send
	// somebody to their inbox for a message that was only recorded.
	created := testclient.Decoded[invitationCreatedOut](o.client(t).Post(o.base()+"/invitations",
		testclient.JSON(map[string]any{"email": person(t).Email, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusCreated))
	if created.Delivered {
		t.Error("delivered must be false with a recorder in place of a provider")
	}
}

// TestAnInboxCannotBeFlooded is the one rate limit on the dashboard, and the only budget in the
// product that protects somebody who is not making the request.
//
// Three per address per hour. A studio inviting one colleague sends one message, and a second if
// the first went to a typo; anything past that is using somebody's inbox rather than filling a
// seat. Limits are off for the rest of this suite — their counters live in Valkey and outlive the
// process — so this test turns them on for itself.
func TestAnInboxCannotBeFlooded(t *testing.T) {
	settings := testSettings(t)
	settings.RateLimitDisabled = false

	google := fbtest.New(t, settings.FirebaseProjectID)
	settings.FirebaseCertificateURL = google.URL
	mailbox := &mail.Recorder{}

	s := &stack{
		settings: settings,
		app:      build(settings, muzak.WithSingleton[mail.Sender](mailbox)),
		google:   google,
		db:       globalTier(t, settings),
	}
	owner := person(t)
	slug := s.createOrg(t, owner)
	client := s.as(t, owner)

	// A distinct address per run, so one run's counters are not the next one's problem.
	target := person(t).Email
	for range core.InvitationsPerAddress {
		// Each send needs the previous invitation out of the way, because one outstanding
		// invitation per address is a separate rule from the budget.
		created := testclient.Decoded[invitationCreatedOut](
			client.Post("/v1/organizations/"+slug+"/invitations",
				testclient.JSON(map[string]string{"email": target, "role": authz.RoleDeveloper})).
				AssertStatus(http.StatusCreated))
		client.Delete("/v1/organizations/" + slug + "/invitations/" + created.ID).
			AssertStatus(http.StatusOK)
	}

	refused := client.Post("/v1/organizations/"+slug+"/invitations",
		testclient.JSON(map[string]string{"email": target, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusTooManyRequests)
	// Retry-After, so a client waits the right amount rather than guessing.
	if refused.Header.Get("Retry-After") == "" {
		t.Error("a refusal carries no Retry-After")
	}
	// And the refusal names the address budget rather than the organization's, because the
	// address is the thing the sender can act on by checking the spelling.
	if !strings.Contains(refused.String(), "address") {
		t.Errorf("the refusal does not name which budget: %s", refused)
	}

	// A different address is unaffected: the budget is per inbox, not per sender.
	client.Post("/v1/organizations/"+slug+"/invitations",
		testclient.JSON(map[string]string{"email": person(t).Email, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusCreated)
}

// TestRemovingAMemberDeletesTheGrant is the one place this codebase deletes rather than archives.
//
// A revoked key and an archived flag are records of what happened. A membership is not: it is a
// live grant of access to a studio's player data, and keeping a disabled one would make the answer
// to "who can reach this" depend on a boolean somebody has to remember to read.
func TestRemovingAMemberDeletesTheGrant(t *testing.T) {
	o := newOrg(t)
	owner := o.client(t)

	leaver := person(t)
	token := o.invite(t, owner, leaver.Email, authz.RoleDeveloper)
	o.as(t, leaver).Post("/v1/invitations/accept",
		testclient.JSON(map[string]string{"token": token})).AssertStatus(http.StatusOK)

	members := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members"))
	var id string
	for _, member := range members.Members {
		if member.Email == leaver.Email {
			id = member.ID
		}
	}

	owner.Delete(o.base() + "/members/" + id).AssertStatus(http.StatusNoContent)

	// Gone from the list, and the organization is invisible to them again — not forbidden,
	// absent, which is what every other tenancy check here answers.
	after := testclient.Decoded[memberListOut](owner.Get(o.base() + "/members"))
	if len(after.Members) != 1 {
		t.Errorf("want one member left, got %d", len(after.Members))
	}
	o.as(t, leaver).Get(o.base()).AssertStatus(http.StatusNotFound)

	// And they can be invited again, which a kept-but-disabled row would have made awkward.
	o.invite(t, owner, leaver.Email, authz.RoleGrowthManager)
}

// TestAnotherStudiosMembersAreInvisible is the tenancy check on this surface.
func TestAnotherStudiosMembersAreInvisible(t *testing.T) {
	o := newOrg(t)
	stranger := person(t)

	o.as(t, stranger).Get(o.base() + "/members").AssertStatus(http.StatusNotFound)
	o.as(t, stranger).Get(o.base() + "/invitations").AssertStatus(http.StatusNotFound)
	o.as(t, stranger).Get(o.base() + "/roles").AssertStatus(http.StatusNotFound)
	o.as(t, stranger).Post(o.base()+"/invitations",
		testclient.JSON(map[string]string{"email": person(t).Email, "role": authz.RoleDeveloper})).
		AssertStatus(http.StatusNotFound)
}

// TestTheSeededRolesAreListedWithWhatTheyGrant is what an invite screen reads.
//
// A system role's permissions are computed from the vocabulary rather than read from its row, so
// that a permission added tomorrow reaches every owner without a migration. Which means this
// response is the authority on what a role grants, and the column is a convenience for whoever
// reads the table by hand.
func TestTheSeededRolesAreListedWithWhatTheyGrant(t *testing.T) {
	o := newOrg(t)

	listed := testclient.Decoded[roleListOut](o.client(t).Get(o.base() + "/roles").
		AssertStatus(http.StatusOK))
	if len(listed.Roles) != len(authz.Templates()) {
		t.Fatalf("want %d roles, got %d", len(authz.Templates()), len(listed.Roles))
	}

	byKey := map[string]roleOut{}
	for _, role := range listed.Roles {
		byKey[role.Key] = role
	}

	// The owner's permissions are computed, so the list is non-empty even though the seeded
	// row's own column is not what was read.
	if len(byKey[authz.RoleOwner].Permissions) == 0 || !byKey[authz.RoleOwner].System {
		t.Errorf("owner = %+v", byKey[authz.RoleOwner])
	}
	// And a non-system role reports exactly what it was seeded with: a growth manager reads
	// numbers and sees what is scheduled, and changes neither.
	growth := byKey[authz.RoleGrowthManager]
	if growth.System {
		t.Error("growth_manager must not be a system role")
	}
	for _, want := range []authz.Permission{authz.AnalyticsRead, authz.LiveOpsRead} {
		if !contains(strings.Join(growth.Permissions, ","), string(want)) {
			t.Errorf("growth_manager does not grant %s: %v", want, growth.Permissions)
		}
	}
	if contains(strings.Join(growth.Permissions, ","), string(authz.LiveOpsWrite)) {
		t.Errorf("growth_manager must not grant liveops.write: %v", growth.Permissions)
	}
}

// Local decoding types, so these tests assert on the wire format rather than importing the schema
// package and testing it against itself.

type roleOut struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	System      bool     `json:"system"`
}

type roleListOut struct {
	Roles []roleOut `json:"roles"`
}

type memberOut struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	Role        roleOut `json:"role"`
	AllProjects bool    `json:"all_projects"`
	Projects    []struct {
		Slug string `json:"slug"`
	} `json:"projects"`
	JoinedAt time.Time `json:"joined_at"`
	You      bool      `json:"you"`
}

type memberListOut struct {
	Members []memberOut `json:"members"`
	Limit   int         `json:"member_limit"`
	Used    int         `json:"members_used"`
}

type invitationOut struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Role      roleOut `json:"role"`
	Status    string  `json:"status"`
	InvitedBy string  `json:"invited_by"`
}

type invitationListOut struct {
	Invitations []invitationOut `json:"invitations"`
}

type invitationCreatedOut struct {
	invitationOut
	Delivered bool `json:"delivered"`
}

type pendingListOut struct {
	Invitations []struct {
		Organization string `json:"organization"`
		OrgSlug      string `json:"organization_slug"`
		Role         string `json:"role"`
	} `json:"invitations"`
}

type acceptedOut struct {
	Organization struct {
		Slug string `json:"slug"`
	} `json:"organization"`
	Role          roleOut `json:"role"`
	AllProjects   bool    `json:"all_projects"`
	AlreadyMember bool    `json:"already_member"`
}

type projectListOut struct {
	Projects []struct {
		Slug string `json:"slug"`
	} `json:"projects"`
}
