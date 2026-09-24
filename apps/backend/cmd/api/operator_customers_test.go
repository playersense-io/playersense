package main

import (
	"net/http"
	"slices"
	"strconv"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
	"playersense.dev/backend/internal/testsupport"
)

// The console finding a customer and reading their account (§56).

// TestStaffFindACustomerByWhatTheyHave: support has an address, the ingestion page a sequence
// number, and an engineer a slug. Each finds the same organization, and each search is a
// ticketed read.
func TestStaffFindACustomerByWhatTheyHave(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	org := s.organization(t, slug)
	testsupport.SeedBillingAccount(t, s.db, org.ID)
	projectSlug := s.createProject(t, owner, slug)

	support, who := s.staffClient(t, staff.Support)
	for _, q := range []string{owner.Email, slug, strconv.Itoa(org.ID), org.UID.String()} {
		found := testclient.Decoded[schemas.OperatorOrganizationListOut](
			support.Get("/v1/operator/organizations?q=" + q).AssertStatus(http.StatusOK))
		// An address also finds the personal workspace its person was given on sign-up.
		at := slices.IndexFunc(found.Organizations, func(o schemas.OperatorOrganizationSummaryOut) bool {
			return o.Slug == slug
		})
		if at < 0 || (q != owner.Email && len(found.Organizations) != 1) {
			t.Fatalf("q=%s found %+v", q, found.Organizations)
		}
		if one := found.Organizations[at]; one.Projects != 1 || one.Members != 1 || one.Balance == nil || one.Plan == "" {
			t.Fatalf("q=%s summary %+v", q, one)
		}
	}
	if n := s.accessCount(t, who); n != 4 {
		t.Fatalf("four searches left %d records", n)
	}

	detail := testclient.Decoded[schemas.OperatorOrganizationOut](
		support.Get("/v1/operator/organizations/" + slug).AssertStatus(http.StatusOK))
	if len(detail.ProjectList) != 1 || len(detail.MemberList) != 1 || detail.MemberList[0].Role != "owner" ||
		detail.Account == nil {
		t.Fatalf("detail %+v", detail)
	}

	projects := testclient.Decoded[schemas.OperatorProjectListOut](
		support.Get("/v1/operator/projects?q=" + strconv.Itoa(detail.ProjectList[0].Seq)).AssertStatus(http.StatusOK))
	if len(projects.Projects) != 1 || projects.Projects[0].Slug != projectSlug || projects.Projects[0].Organization.Slug != slug {
		t.Fatalf("projects %+v", projects.Projects)
	}
	proj := testclient.Decoded[schemas.OperatorProjectOut](
		support.Get("/v1/operator/projects/" + projects.Projects[0].ID.String()).AssertStatus(http.StatusOK))
	if len(proj.Environments) == 0 || proj.Organization.Slug != slug {
		t.Fatalf("project %+v", proj)
	}

	// Each in-page read names the customer in staff_access, and a bad cursor is the caller's.
	support.Get("/v1/operator/organizations?before=x").AssertStatus(http.StatusBadRequest)
	support.Get("/v1/operator/projects/" + org.UID.String()).AssertStatus(http.StatusNotFound)
}

// TestAnOverrideIsMergedAndLogged: one change does not undo another, null goes back to the plan,
// and the customer's log carries who, the previous value and why.
func TestAnOverrideIsMergedAndLogged(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	testsupport.SeedBillingAccount(t, s.db, s.organization(t, slug).ID)
	billing, who := s.staffClient(t, staff.Billing)
	path := "/v1/operator/organizations/" + slug + "/entitlements"

	set := func(body map[string]any, want int) schemas.EntitlementsOut {
		t.Helper()
		r := billing.Put(path, testclient.JSON(body)).AssertStatus(want)
		if want != http.StatusOK {
			return schemas.EntitlementsOut{}
		}
		return testclient.Decoded[schemas.EntitlementsOut](r)
	}
	set(map[string]any{"overrides": map[string]any{"max_projects": 40}, "reason": "Deal SUP-9"}, http.StatusOK)
	got := set(map[string]any{"overrides": map[string]any{"max_webhook_endpoints": 25}, "member_limit": 120,
		"reason": "Deal SUP-9"}, http.StatusOK)
	if got.Overrides["max_projects"] != 40 || got.Overrides["max_webhook_endpoints"] != 25 ||
		got.Resolved["max_projects"] != 40 || got.MemberLimit != 120 {
		t.Fatalf("after two changes %+v", got)
	}
	got = set(map[string]any{"overrides": map[string]any{"max_projects": nil}, "reason": "Deal ended"}, http.StatusOK)
	if _, still := got.Overrides["max_projects"]; still || got.Overrides["max_webhook_endpoints"] != 25 {
		t.Fatalf("after clearing one %+v", got)
	}
	if got.Resolved["max_projects"] != got.PlanEntitlements["max_projects"] {
		t.Fatalf("a cleared override resolves to %d, the plan says %d",
			got.Resolved["max_projects"], got.PlanEntitlements["max_projects"])
	}

	set(map[string]any{"overrides": map[string]any{"max_frogs": 1}, "reason": "x"}, http.StatusUnprocessableEntity)
	set(map[string]any{"overrides": map[string]any{"max_projects": -1}, "reason": "x"}, http.StatusUnprocessableEntity)
	set(map[string]any{"reason": "x"}, http.StatusUnprocessableEntity)
	set(map[string]any{"overrides": map[string]any{"max_projects": 3}}, http.StatusUnprocessableEntity)

	// Support reads, and cannot write.
	supportClient, _ := s.staffClient(t, staff.Support)
	supportClient.Get(path).AssertStatus(http.StatusOK)
	supportClient.Put(path, testclient.JSON(map[string]any{"member_limit": 5, "reason": "x"})).
		AssertStatus(http.StatusForbidden)

	// The customer's own log, filtered to the staff member, newest first, paged.
	log := testclient.Decoded[schemas.AuditListOut](supportClient.Get(
		"/v1/operator/organizations/" + slug + "/audit?action=billing.entitlements&actor=" + who.Email + "&limit=2").
		AssertStatus(http.StatusOK))
	if len(log.Events) != 2 || log.Next == "" {
		t.Fatalf("first page %+v", log)
	}
	if reason := log.Events[0].Metadata["reason"]; reason != "Deal ended" {
		t.Fatalf("the newest entry's reason is %v", reason)
	}
	rest := testclient.Decoded[schemas.AuditListOut](supportClient.Get(
		"/v1/operator/organizations/" + slug + "/audit?action=billing.entitlements&before=" + log.Next).
		AssertStatus(http.StatusOK))
	if len(rest.Events) != 1 || rest.Next != "" {
		t.Fatalf("second page %+v", rest)
	}

	// And the owner sees the same entries in their own log.
	owned := testclient.Decoded[schemas.AuditListOut](s.as(t, owner).Get(
		"/v1/organizations/" + slug + "/audit?target_type=billing_account&target_id=" + slug).
		AssertStatus(http.StatusOK))
	if len(owned.Events) != 3 {
		t.Fatalf("the owner reads %d entries about their account", len(owned.Events))
	}
}

// TestStaffReadAWallet: the billing inspection routes answer on a ticket, page the ledger, and list
// spent grants when asked.
func TestStaffReadAWallet(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	testsupport.SeedBillingAccount(t, s.db, s.organization(t, slug).ID)
	billing, _ := s.staffClient(t, staff.Billing)
	base := "/v1/operator/organizations/" + slug
	for i := range 3 {
		billing.Post(base+"/credits", testclient.JSON(map[string]any{
			"kind": "promo", "amount_micros": dollars(int64(10 + i)), "description": "x", "reference": unique("ref"),
		})).AssertStatus(http.StatusCreated)
	}

	page := testclient.Decoded[schemas.OperatorBillingOut](billing.Get(base + "/billing").AssertStatus(http.StatusOK))
	if page.AvailableMicros != dollars(33) || page.Account.State == "" {
		t.Fatalf("billing %+v", page)
	}
	ledger := testclient.Decoded[schemas.LedgerOut](billing.Get(base + "/ledger?limit=2").AssertStatus(http.StatusOK))
	if len(ledger.Entries) != 2 || ledger.Next == "" {
		t.Fatalf("ledger %+v", ledger)
	}
	lots := testclient.Decoded[schemas.OperatorCreditLotsOut](billing.Get(base + "/credit-lots?all=true").AssertStatus(http.StatusOK))
	if len(lots.Lots) != 3 || lots.Lots[0].GrantedByStaff == "" || lots.Lots[0].Reference == "" {
		t.Fatalf("lots %+v", lots.Lots)
	}
	billing.Get(base + "/usage").AssertStatus(http.StatusOK)
}
