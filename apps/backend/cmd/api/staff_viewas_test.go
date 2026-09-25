package main

import (
	"net/http"
	"strings"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/staffaccess"
	"playersense.dev/backend/internal/staff"
)

// Support viewing a customer's dashboard: the customer's own routes answer, reads only, every read
// recorded with its ticket, and nobody but support may do it.
func TestSupportViewsACustomersDashboardReadOnly(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	org := s.createOrg(t, owner)
	proj := s.createProject(t, owner, org)
	as := testclient.Header("X-Staff-View-As", org)

	desk, who := s.staffClient(t, staff.Support)
	before, _ := s.db.Client.StaffAccess.Query().Where(staffaccess.StaffEmail(staff.NormalizeEmail(who.Email)),
		staffaccess.Action("support.view_as")).Count(t.Context())

	projects := desk.Get("/v1/organizations/"+org+"/projects", as).AssertStatus(http.StatusOK).String()
	if !strings.Contains(projects, proj) {
		t.Fatalf("the customer's projects, as support: %s", projects)
	}
	desk.Get("/v1/organizations/"+org+"/projects/"+proj+"/flags", as).AssertStatus(http.StatusOK)
	desk.Get("/v1/organizations/"+org+"/members", as).AssertStatus(http.StatusOK)

	// Nothing changes.
	desk.Post("/v1/organizations/"+org+"/projects/"+proj+"/flags", as,
		testclient.RawJSON(`{"key":"x","name":"X","kind":"bool","fallback":false}`)).AssertStatus(http.StatusForbidden)

	// Every read is recorded.
	after, _ := s.db.Client.StaffAccess.Query().Where(staffaccess.StaffEmail(staff.NormalizeEmail(who.Email)),
		staffaccess.Action("support.view_as")).Count(t.Context())
	if after-before != 3 {
		t.Fatalf("%d reads recorded, want 3", after-before)
	}

	// Without a ticket, as another organization, as the wrong staff role, or as a customer: no.
	noTicket := testclient.New(t, s.app, testclient.WithHeader("Authorization", "Bearer "+s.google.Token(who)))
	noTicket.Get("/v1/organizations/"+org+"/projects", as).AssertStatus(http.StatusBadRequest)
	desk.Get("/v1/organizations/"+org+"/projects", testclient.Header("X-Staff-View-As", "someone-else")).
		AssertStatus(http.StatusBadRequest)
	billing := staffOnly(s.staffClient(t, staff.Billing))
	billing.Get("/v1/organizations/"+org+"/projects", as).AssertStatus(http.StatusForbidden)
	stranger := s.as(t, person(t))
	stranger.Get("/v1/organizations/"+org+"/projects", as).AssertStatus(http.StatusForbidden)

	// The owner's own access is untouched by the header's existence.
	s.as(t, owner).Get("/v1/organizations/" + org + "/projects").AssertStatus(http.StatusOK)
}

// The admin console is another origin: its staff headers must pass the browser's preflight.
func TestTheConsolesHeadersPassThePreflight(t *testing.T) {
	s := newStack(t)
	res := testclient.New(t, s.app).Do(http.MethodOptions, "/v1/operator/organizations",
		testclient.Header("Origin", "http://localhost:3002"),
		testclient.Header("Access-Control-Request-Method", "GET"),
		testclient.Header("Access-Control-Request-Headers", "authorization,x-staff-ticket,x-staff-view-as"))
	allowed := strings.ToLower(res.Header.Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"x-staff-ticket", "x-staff-note", "x-staff-view-as", "authorization"} {
		if !strings.Contains(allowed, h) {
			t.Fatalf("the preflight allows %q, not %s", allowed, h)
		}
	}
}
