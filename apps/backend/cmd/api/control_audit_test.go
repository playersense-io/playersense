package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/staff"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The customer's own log: who did what to their account.
//
// §08 asks for two things and both are tested here rather than asserted in a document: that the
// records are append-only, and that the actions that matter are in them. The second is the one that
// rots, which is why the coverage test below reads the routes rather than a list somebody maintains.

// TestEveryMutatingRouteDecidesAboutAudit is the test that keeps this log honest.
//
// Writing entries by hand is right — middleware records requests, and an audit log has to record
// decisions, which means the previous value and the thing only the handler knows. The cost is that
// one can be forgotten, and this is how that cost is paid down: it reads the OpenAPI document, finds
// every route that changes something, and fails unless each is either known to be audited or listed
// as deliberately exempt with a reason.
//
// So adding a mutating route and saying nothing about audit does not get past the suite. That is a
// different guarantee from "we remembered", and it is the only one that survives a year of changes.
func TestEveryMutatingRouteDecidesAboutAudit(t *testing.T) {
	// Recorded: the route writes an audit entry. The value is the action, so this table doubles
	// as the map from a route to the line it produces.
	recorded := map[string]audit.Action{
		"POST /v1/organizations":                                          audit.OrganizationCreated,
		"POST /v1/organizations/{org}/projects":                           audit.ProjectCreated,
		"POST /v1/organizations/{org}/projects/{project}/keys":            audit.KeyCreated,
		"DELETE /v1/organizations/{org}/projects/{project}/keys/{key_id}": audit.KeyRevoked,

		"POST /v1/organizations/{org}/invitations":                   audit.MemberInvited,
		"DELETE /v1/organizations/{org}/invitations/{invitation_id}": audit.InvitationRevoked,
		"POST /v1/invitations/accept":                                audit.MemberJoined,
		"DELETE /v1/organizations/{org}/members/{member_id}":         audit.MemberRemoved,
		"PUT /v1/organizations/{org}/members/{member_id}/role":       audit.MemberRoleChanged,
		"PUT /v1/organizations/{org}/members/{member_id}/access":     audit.MemberAccessChanged,

		"POST /v1/organizations/{org}/projects/{project}/segments":               audit.SegmentWritten,
		"PUT /v1/organizations/{org}/projects/{project}/segments/{key}":          audit.SegmentWritten,
		"DELETE /v1/organizations/{org}/projects/{project}/segments/{key}":       audit.SegmentArchived,
		"POST /v1/organizations/{org}/projects/{project}/flags":                  audit.FlagWritten,
		"PUT /v1/organizations/{org}/projects/{project}/flags/{key}":             audit.FlagWritten,
		"DELETE /v1/organizations/{org}/projects/{project}/flags/{key}":          audit.FlagArchived,
		"POST /v1/organizations/{org}/projects/{project}/liveops-events":         audit.EventWritten,
		"PUT /v1/organizations/{org}/projects/{project}/liveops-events/{key}":    audit.EventWritten,
		"DELETE /v1/organizations/{org}/projects/{project}/liveops-events/{key}": audit.EventArchived,

		"POST /v1/organizations/{org}/projects/{project}/config/{environment}/publish":                     audit.ConfigPublished,
		"POST /v1/organizations/{org}/projects/{project}/config/{environment}/versions/{version}/rollback": audit.ConfigRolledBack,

		"POST /v1/organizations/{org}/projects/{project}/experiments":                       audit.ExperimentWritten,
		"PUT /v1/organizations/{org}/projects/{project}/experiments/{experiment}":           audit.ExperimentWritten,
		"DELETE /v1/organizations/{org}/projects/{project}/experiments/{experiment}":        audit.ExperimentDeleted,
		"POST /v1/organizations/{org}/projects/{project}/experiments/{experiment}/start":    audit.ExperimentStarted,
		"PUT /v1/organizations/{org}/projects/{project}/experiments/{experiment}/traffic":   audit.ExperimentTraffic,
		"POST /v1/organizations/{org}/projects/{project}/experiments/{experiment}/stop":     audit.ExperimentStopped,
		"POST /v1/organizations/{org}/projects/{project}/experiments/{experiment}/conclude": audit.ExperimentConcluded,
		"POST /v1/organizations/{org}/projects/{project}/experiments/{experiment}/ship":     audit.ExperimentShipped,

		"PATCH /v1/organizations/{org}/projects/{project}":           audit.ProjectUpdated,
		"PUT /v1/organizations/{org}/projects/{project}/retention":   audit.RetentionSet,
		"POST /v1/organizations/{org}/projects/{project}/deletion":   audit.ProjectDeletionScheduled,
		"DELETE /v1/organizations/{org}/projects/{project}/deletion": audit.ProjectDeletionCancelled,

		"POST /v1/organizations/{org}/projects/{project}/privacy/erasures": audit.PrivacyErasure,
		"POST /v1/organizations/{org}/projects/{project}/privacy/exports":  audit.PrivacyExport,

		"POST /v1/organizations/{org}/projects/{project}/keys/{key_id}/roll": audit.KeyRolled,

		"PUT /v1/organizations/{org}/projects/{project}/event-definitions/{name}": audit.EventDefined,

		"POST /v1/organizations/{org}/projects/{project}/funnels":            audit.FunnelWritten,
		"PUT /v1/organizations/{org}/projects/{project}/funnels/{funnel}":    audit.FunnelWritten,
		"DELETE /v1/organizations/{org}/projects/{project}/funnels/{funnel}": audit.FunnelDeleted,

		"PUT /v1/organizations/{org}/billing/auto-topup":    audit.AutoTopUpSet,
		"POST /v1/operator/organizations/{org}/credits":     audit.CreditGranted,
		"POST /v1/operator/organizations/{org}/charges":     audit.CreditCharged,
		"PUT /v1/operator/organizations/{org}/plan":         audit.PlanChanged,
		"PUT /v1/operator/organizations/{org}/credit-limit": audit.CreditLimitSet,
		"PUT /v1/operator/organizations/{org}/entitlements": audit.EntitlementsSet,

		"PUT /v1/operator/organizations/{org}/contract":                            audit.ContractSet,
		"POST /v1/operator/organizations/{org}/invoices/{invoice_id}/mark-paid":    audit.InvoicePaid,
		"POST /v1/operator/organizations/{org}/invoices/{invoice_id}/credit-notes": audit.InvoiceCredited,
	}

	// Exempt, each with the reason. A route is allowed to change something and write no entry —
	// but only on purpose, and only with the reason written down where the next person finds it.
	//
	// Every entry here arrived at once, when the four services became one binary and this test
	// started reading their routes too. None of them is a dashboard action, which is the thing
	// §08's log is about: ADR 0037's rule is that an audit log records **decisions**, and a
	// decision is something a person made about a customer's account.
	exempt := map[string]string{
		// POST because the query is a document, not because anything changes. A read that
		// writes an audit entry per request is the request log ADR 0037 exists to refuse.
		"POST /v1/cohorts/query": "a read; POST only because the query is a document",
		"POST /v1/metrics/query": "a read; POST only because the query is a document",
		"POST /v1/organizations/{org}/projects/{project}/analytics/{environment}/cohorts/query": "a read",
		"POST /v1/organizations/{org}/projects/{project}/analytics/{environment}/metrics/query": "a read",
		"POST /v1/organizations/{org}/projects/{project}/analytics/{environment}/funnels/query": "a read",
		"POST /v1/config": "a read; POST only because the player's context is a document",

		// The SDK path. A batch is player data arriving, not a decision somebody made about
		// an account, and one entry per batch would be tens of millions of rows a day.
		"POST /v1/batches": "ingestion; an entry per batch is a request log, at ingestion volume",

		// These two genuinely change something, and they keep a **better** record than an
		// audit line: their own tables carry a status machine through to completion, plus
		// what was removed as counts (§61, §101). That record outlives the data it describes,
		// which an entry keyed to an organization does not.
		"POST /v1/privacy/erasures": "recorded in data_deletion_requests, with a status machine and counts",
		"POST /v1/privacy/exports":  "recorded in data_export_requests, with a status machine and an expiry",

		// The catalogue belongs to no organization, and this log is an organization's. Each of
		// these is recorded in staff_access instead, with the verified staff member and a ticket;
		// a rate card also carries who published it and why, and the list of cards *is* the
		// price history (ADR 0039).
		"POST /v1/operator/plans":                   "the catalogue; recorded in staff_access",
		"PUT /v1/operator/plans/{plan}":             "the catalogue; recorded in staff_access",
		"POST /v1/operator/plans/{plan}/rate-cards": "an immutable card that records who published it and why",

		// Who is staff is not a customer's business, and each change is in staff_access with
		// the admin who made it and a ticket.
		"POST /v1/operator/projects/{project}/archive-manifests/{manifest_id}/retry": "recorded in staff_access; the manifest keeps its own state",
		"POST /v1/operator/stripe-events/{event_id}/replay":                          "recorded in staff_access; what it moves is on the ledger under Stripe's reference",
		"POST /v1/operator/staff":                                                    "recorded in staff_access",
		"PUT /v1/operator/staff/{staff_id}":                                          "recorded in staff_access",

		// Payments (ADR 0040). Opening a Checkout page or the portal changes nothing on our side
		// until Stripe reports a payment, and a payment is recorded better than a log line could:
		// in stripe_events with its whole body, and in the ledger with its PaymentIntent.
		"POST /v1/organizations/{org}/billing/top-ups":      "opens a Stripe page; the payment is in stripe_events and the ledger",
		"POST /v1/organizations/{org}/billing/portal":       "opens Stripe's portal; nothing here changes",
		"POST /v1/organizations/{org}/billing/subscription": "opens a Stripe page; the plan changes by webhook",
		"POST /v1/organizations/{org}/billing/cards":        "opens a Stripe page; the card arrives by webhook",
		"PUT /v1/operator/plans/{plan}/subscription":        "the catalogue; the prices are Stripe objects that keep their own history",
		"POST /v1/stripe/webhook":                           "Stripe's delivery, recorded whole in stripe_events",
	}

	app := mustBuild(t, testSettings(t))
	if err := app.Build(); err != nil {
		t.Fatalf("Build = %v", err)
	}

	doc := testclient.New(t, app).Get("/openapi.json").AssertStatus(http.StatusOK)
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	doc.JSON(&spec)
	if len(spec.Paths) == 0 {
		t.Fatal("the OpenAPI document has no paths, so this test proves nothing")
	}

	var undecided []string
	for path, operations := range spec.Paths {
		for method := range operations {
			switch strings.ToUpper(method) {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			route := strings.ToUpper(method) + " " + path
			if _, found := recorded[route]; found {
				continue
			}
			if _, found := exempt[route]; found {
				continue
			}
			undecided = append(undecided, route)
		}
	}

	slices.Sort(undecided)
	if len(undecided) > 0 {
		t.Errorf("these routes change something and say nothing about the audit log.\n"+
			"Either record an entry and add the route to `recorded`, or add it to `exempt`\n"+
			"with the reason it does not need one:\n  %s", strings.Join(undecided, "\n  "))
	}

	// And the other direction: a route that was removed leaves a stale entry in the table above,
	// which would quietly stop this test from checking anything.
	for route := range recorded {
		method, path, _ := strings.Cut(route, " ")
		if ops, found := spec.Paths[path]; !found {
			t.Errorf("`recorded` names %s, which no longer exists", route)
		} else if _, found := ops[strings.ToLower(method)]; !found {
			t.Errorf("`recorded` names %s, but that path has no such method", route)
		}
	}
}

// TestRemovingAMemberIsTheEntryThatMattersMost.
//
// A membership is the one thing this product deletes rather than archives, so the log is the *only*
// record it ever existed. Without this entry, "who ended Ada's access, and when" has no answer
// anywhere — which is exactly the gap ADR 0035 recorded when it shipped.
func TestRemovingAMemberIsTheEntryThatMattersMost(t *testing.T) {
	a := newAuditing(t)

	leaver := person(t)
	a.join(t, a.slug, leaver, authz.RoleDeveloper, true)
	member := a.member(t, a.slug, leaver)

	a.client(t).Delete(a.base() + "/members/" + member.UID.String()).
		AssertStatus(http.StatusNoContent)

	entry := a.find(t, audit.MemberRemoved)
	if entry.Metadata["email"] != leaver.Email {
		t.Errorf("the entry does not name who was removed: %v", entry.Metadata)
	}
	if entry.Metadata["role"] != authz.RoleDeveloper {
		t.Errorf("the entry does not say what they held: %v", entry.Metadata)
	}
	if entry.ActorEmail != a.owner.Email {
		t.Errorf("actor = %q, want the owner who did it", entry.ActorEmail)
	}
	// The membership row is gone, so nothing else could have answered this.
	if _, err := a.db.Client.OrganizationMember.Get(t.Context(), member.ID); err == nil {
		t.Error("the membership still exists, so this test is not proving what it claims")
	}
}

// TestTheLogSaysWhatSomethingWasBefore is why middleware could not have written these.
//
// "Ada's role changed to owner" is in the request. "From developer" is not — it was only ever in the
// database, read by the handler before the change. An audit log that could not say what something was
// is a log that cannot answer whether a change was wrong.
func TestTheLogSaysWhatSomethingWasBefore(t *testing.T) {
	a := newAuditing(t)

	colleague := person(t)
	a.join(t, a.slug, colleague, authz.RoleDeveloper, true)
	member := a.member(t, a.slug, colleague)

	a.client(t).Put(a.base()+"/members/"+member.UID.String()+"/role",
		testclient.JSON(map[string]string{"role": authz.RoleGrowthManager})).
		AssertStatus(http.StatusOK)

	entry := a.find(t, audit.MemberRoleChanged)
	if entry.Metadata["was"] != authz.RoleDeveloper {
		t.Errorf("the entry does not say what the role was: %v", entry.Metadata)
	}
	if entry.Metadata["role"] != authz.RoleGrowthManager {
		t.Errorf("the entry does not say what it became: %v", entry.Metadata)
	}

	// The same property on a rollout, which is the change with players on the other side of it.
	project := a.createProject(t, a.owner, a.slug)
	flags := a.base() + "/projects/" + project + "/flags"
	a.client(t).Post(flags, testclient.RawJSON(
		`{"key":"hud_v2","name":"HUD v2","kind":"bool","fallback":false,"rollout":1000}`)).
		AssertStatus(http.StatusCreated)
	a.client(t).Put(flags+"/hud_v2", testclient.RawJSON(
		`{"name":"HUD v2","fallback":false,"rollout":10000}`)).
		AssertStatus(http.StatusOK)

	entry = a.find(t, audit.FlagWritten)
	if entry.Metadata["was_rollout"] != float64(1000) || entry.Metadata["rollout"] != float64(10000) {
		t.Errorf("the entry does not record the rollout moving: %v", entry.Metadata)
	}
}

// TestAnAuditEntryCannotBeChanged is §08's "a log the operator can edit proves nothing", as a
// property of the generated code rather than a policy.
//
// Every field on the entity is immutable and the mixin omits `updated_at`, so ent emits an update
// builder with no setter on it at all. This test asserts the consequence from the outside: what the
// row said when it was written is what it says now.
func TestAnAuditEntryCannotBeChanged(t *testing.T) {
	a := newAuditing(t)

	project := a.createProject(t, a.owner, a.slug)
	entry := a.find(t, audit.ProjectCreated)

	// There is no `SetAction`, `SetMetadata` or `SetActorLabel` to call — that is a compile-time
	// fact, and internal/identity's TestAnAppendOnlyRowHasNoSetter covers it directly. What is
	// checked here is that an update builder exists and changes nothing.
	changed, err := a.db.Client.AuditEvent.Update().Save(t.Context())
	if err != nil {
		t.Fatalf("an empty update failed: %v", err)
	}
	_ = changed

	after := a.find(t, audit.ProjectCreated)
	if after.Metadata["slug"] != project || after.At != entry.At {
		t.Errorf("the entry moved: %+v then %+v", entry, after)
	}
}

// TestAStaffGrantNamesTheVerifiedStaffMember is the gap ADR 0036 left open, closed.
//
// The shared operator token could not prove who was holding it, so a grant carried a name the caller
// typed and said it was self-asserted. A staff member now signs in, and the entry names the address
// their verified sign-in carried.
func TestAStaffGrantNamesTheVerifiedStaffMember(t *testing.T) {
	a := newAuditing(t)
	testsupport.SeedBillingAccount(t, a.db, a.organization(t, a.slug).ID)

	operator, who := a.staffClient(t, staff.Billing)
	operator.Post("/v1/operator/organizations/"+a.slug+"/credits",
		testclient.JSON(map[string]any{
			"kind": wallet.Promo, "amount_micros": 100_000_000,
			"description": "Launch credit", "reference": unique("ref"),
		})).AssertStatus(http.StatusCreated)

	entry := a.find(t, audit.CreditGranted)
	if entry.Actor != "PlayerSense staff "+strings.ToLower(who.Email) {
		t.Errorf("the entry names %q, want the staff member who signed in", entry.Actor)
	}
	if entry.ActorEmail != "" {
		t.Errorf("actor_email = %q; staff are not users of the organization", entry.ActorEmail)
	}
}

// TestAFailedActionIsNotLogged is the other half of an audit log being trustworthy.
//
// A log that records attempts is a log where "who did this" and "who tried this" are the same line.
// Every entry is written inside the transaction that made the change, so a refused request leaves
// nothing behind.
func TestAFailedActionIsNotLogged(t *testing.T) {
	a := newAuditing(t)
	before := a.count(t)

	// Refused by the last-owner rule, after the handler has already read the row it would have
	// recorded.
	owner := a.member(t, a.slug, a.owner)
	a.client(t).Delete(a.base() + "/members/" + owner.UID.String()).
		AssertStatus(http.StatusConflict)

	// Refused by validation, before anything is read.
	project := a.createProject(t, a.owner, a.slug)
	a.client(t).Post(a.base()+"/projects/"+project+"/flags",
		testclient.RawJSON(`{"key":"bad","name":"Bad","kind":"number","fallback":true}`)).
		AssertStatus(http.StatusUnprocessableEntity)

	// One entry for the project that was created, and nothing for either refusal.
	if after := a.count(t); after != before+1 {
		t.Errorf("the log grew by %d, want 1 — only the project was created", after-before)
	}
	for _, action := range []audit.Action{audit.MemberRemoved, audit.FlagWritten} {
		if a.exists(t, action) {
			t.Errorf("a refused request wrote a %s entry", action)
		}
	}
}

// TestTheLogNeedsAuditRead is why it is not on the members screen's permission.
//
// The log names what every member of the organisation has been doing. A contractor can reasonably see
// who their colleagues are without seeing what each of them changed last Tuesday, so reading it is
// reserved to the two system roles among the seeded six.
func TestTheLogNeedsAuditRead(t *testing.T) {
	a := newAuditing(t)

	admin := person(t)
	a.join(t, a.slug, admin, authz.RoleAdmin, true)
	a.as(t, admin).Get(a.base() + "/audit").AssertStatus(http.StatusOK)

	for _, role := range []string{
		authz.RoleDeveloper, authz.RoleGrowthManager, authz.RoleLiveOpsManager, authz.RoleBilling,
	} {
		who := person(t)
		a.join(t, a.slug, who, role, true)
		refused := a.as(t, who).Get(a.base() + "/audit").AssertStatus(http.StatusForbidden)
		if !strings.Contains(refused.String(), string(authz.AuditRead)) {
			t.Errorf("%s: the refusal does not name the missing permission: %s", role, refused)
		}
	}

	// And another studio's log is absent rather than forbidden, like everything else here.
	a.as(t, person(t)).Get(a.base() + "/audit").AssertStatus(http.StatusNotFound)
}

// TestTheLogIsFilterableAndNewestFirst is what makes it usable on a busy account.
func TestTheLogIsFilterableAndNewestFirst(t *testing.T) {
	a := newAuditing(t)

	first := a.createProject(t, a.owner, a.slug)
	second := a.createProject(t, a.owner, a.slug)
	a.client(t).Post(a.base()+"/projects/"+first+"/keys",
		testclient.JSON(map[string]string{
			"kind": "project", "environment": "production", "name": "unity",
		})).AssertStatus(http.StatusCreated)

	all := a.list(t, "")
	if len(all.Events) < 3 {
		t.Fatalf("the log has %d entries, want at least three", len(all.Events))
	}
	// Newest first: the key came last.
	if all.Events[0].Action != string(audit.KeyCreated) {
		t.Errorf("the newest entry is %q, want the key", all.Events[0].Action)
	}
	for i := 1; i < len(all.Events); i++ {
		if all.Events[i].At.After(all.Events[i-1].At) {
			t.Errorf("entry %d is newer than the one before it", i)
		}
	}

	projects := a.list(t, string(audit.ProjectCreated))
	if len(projects.Events) != 2 {
		t.Fatalf("filtering to project.create gave %d entries, want two", len(projects.Events))
	}
	slugs := []string{
		projects.Events[0].Metadata["slug"].(string),
		projects.Events[1].Metadata["slug"].(string),
	}
	if !slices.Contains(slugs, first) || !slices.Contains(slugs, second) {
		t.Errorf("the filtered entries name %v, want both projects", slugs)
	}
}

// ---- fixture ----------------------------------------------------------------

// auditing is one organization whose log is being read.
type auditing struct {
	*stack
	owner fbtest.Person
	slug  string
}

func newAuditing(t *testing.T) *auditing {
	t.Helper()
	s := newStack(t)
	owner := person(t)
	return &auditing{stack: s, owner: owner, slug: s.createOrg(t, owner)}
}

func (a *auditing) base() string { return "/v1/organizations/" + a.slug }

func (a *auditing) client(t *testing.T) *testclient.Client {
	t.Helper()
	return a.as(t, a.owner)
}

func (a *auditing) list(t *testing.T, action string) auditListOut {
	t.Helper()
	path := a.base() + "/audit?limit=200"
	if action != "" {
		path += "&action=" + action
	}
	return testclient.Decoded[auditListOut](
		a.client(t).Get(path).AssertStatus(http.StatusOK))
}

// find returns the newest entry for one action, failing when there is none.
func (a *auditing) find(t *testing.T, action audit.Action) auditEventOut {
	t.Helper()
	listed := a.list(t, string(action))
	if len(listed.Events) == 0 {
		t.Fatalf("the log has no %s entry", action)
	}
	return listed.Events[0]
}

func (a *auditing) exists(t *testing.T, action audit.Action) bool {
	t.Helper()
	return len(a.list(t, string(action)).Events) > 0
}

func (a *auditing) count(t *testing.T) int {
	t.Helper()
	return len(a.list(t, "").Events)
}

type auditEventOut struct {
	Action     string         `json:"action"`
	Actor      string         `json:"actor"`
	ActorEmail string         `json:"actor_email"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Metadata   map[string]any `json:"metadata"`
	IP         string         `json:"ip"`
	At         time.Time      `json:"at"`
}

type auditListOut struct {
	Events []auditEventOut `json:"events"`
}
