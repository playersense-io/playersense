package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent/staffaccess"
	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/staff"
	"playersense.dev/backend/internal/testsupport"
	"playersense.dev/backend/internal/wallet"
)

// The operations console's credential (§56, §104): a signed-in staff member, a role per route, and a
// ticket plus a staff_access row for every request that reaches one customer.

// TestOnlyStaffReachTheConsole: a customer's valid sign-in is not a staff one, and an address Firebase
// has not verified is not enough to be recognised as staff.
func TestOnlyStaffReachTheConsole(t *testing.T) {
	s := newStack(t)

	customer := s.as(t, person(t))
	customer.Get("/v1/operator/me").AssertStatus(http.StatusForbidden)

	unverified := person(t)
	unverified.EmailVerified = false
	seedStaff(t, s.db, unverified, staff.Admin)
	s.as(t, unverified).Get("/v1/operator/me").AssertStatus(http.StatusForbidden)

	deactivated, who := s.staffClient(t, staff.Admin)
	if _, err := s.db.Client.StaffMember.Update().SetActive(false).
		Where(staffEmailIs(who)).Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	deactivated.Get("/v1/operator/me").AssertStatus(http.StatusForbidden)

	testclient.New(t, s.app).Get("/v1/operator/me").AssertStatus(http.StatusUnauthorized)

	ops, _ := s.staffClient(t, staff.Ops)
	me := testclient.Decoded[schemas.StaffMemberOut](ops.Get("/v1/operator/me").AssertStatus(http.StatusOK))
	if !slices.Equal(me.Roles, []string{"ops"}) || !me.You {
		t.Fatalf("me is %+v", me)
	}
}

// TestEachRouteNeedsItsRole: ops reaches the platform and not a customer's wallet; billing the
// reverse; admin both.
func TestEachRouteNeedsItsRole(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	testsupport.SeedBillingAccount(t, s.db, s.organization(t, slug).ID)
	grant := func(c *testclient.Client, want int) {
		t.Helper()
		c.Post("/v1/operator/organizations/"+slug+"/credits", testclient.JSON(map[string]any{
			"kind": wallet.Promo, "amount_micros": 1_000_000, "description": "x", "reference": unique("ref"),
		})).AssertStatus(want)
	}

	ops, _ := s.staffClient(t, staff.Ops)
	grant(ops, http.StatusForbidden)
	ops.Get("/v1/operator/staff").AssertStatus(http.StatusForbidden)

	billing, _ := s.staffClient(t, staff.Billing)
	grant(billing, http.StatusCreated)
	billing.Get("/v1/operator/storage").AssertStatus(http.StatusForbidden)

	admin, _ := s.staffClient(t, staff.Admin)
	grant(admin, http.StatusCreated)
}

// TestACustomerReadNeedsATicketAndLeavesARecord is §104: every staff read of customer data is
// logged with who, which customer and a ticket, and without a ticket it does not happen.
func TestACustomerReadNeedsATicketAndLeavesARecord(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	slug := s.createOrg(t, owner)
	org := s.organization(t, slug)
	testsupport.SeedBillingAccount(t, s.db, org.ID)

	who := person(t)
	seedStaff(t, s.db, who, staff.Billing)
	noTicket := s.as(t, who)
	noTicket.Get("/v1/operator/organizations/" + slug + "/invoices").AssertStatus(http.StatusBadRequest)
	if n := s.accessCount(t, who); n != 0 {
		t.Fatalf("a refused read left %d records", n)
	}

	withTicket := testclient.New(t, s.app,
		testclient.WithHeader("Authorization", "Bearer "+s.google.Token(who)),
		testclient.WithHeader("X-Staff-Ticket", "SUP-7"),
		testclient.WithHeader("X-Staff-Note", "customer asked about March"))
	withTicket.Get("/v1/operator/organizations/" + slug + "/invoices").AssertStatus(http.StatusOK)

	rows, err := s.db.Client.StaffAccess.Query().
		Where(staffaccess.StaffEmail(strings.ToLower(who.Email))).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d records for one read", len(rows))
	}
	r := rows[0]
	if r.Ticket != "SUP-7" || r.Action != "billing.invoices" || r.OrganizationID == nil ||
		*r.OrganizationID != org.ID || r.Note != "customer asked about March" {
		t.Fatalf("the record is %+v", r)
	}

	// And an admin reads it back.
	admin, _ := s.staffClient(t, staff.Admin)
	log := testclient.Decoded[schemas.StaffAccessOut](
		admin.Get("/v1/operator/staff-access?org=" + slug).AssertStatus(http.StatusOK))
	if len(log.Entries) != 1 || log.Entries[0].Organization != slug || log.Entries[0].Ticket != "SUP-7" {
		t.Fatalf("the admin's view is %+v", log.Entries)
	}
}

// TestTheConsoleCannotLockItselfOut: an admin cannot deactivate themselves, and adding staff
// normalises the address and records who added them. That the last admin stays one is
// staff.KeepsAnAdmin's test: exercising it here would mean deactivating every other staff member in
// a database other tests and people share.
func TestTheConsoleCannotLockItselfOut(t *testing.T) {
	s := newStack(t)
	admin, me := s.staffClient(t, staff.Admin)
	mine := testclient.Decoded[schemas.StaffMemberOut](admin.Get("/v1/operator/me").AssertStatus(http.StatusOK))

	admin.Put("/v1/operator/staff/"+mine.ID.String(), testclient.JSON(map[string]any{"active": false})).
		AssertStatus(http.StatusUnprocessableEntity)

	address := unique("grace") + "@PlayerSense.dev"
	added := testclient.Decoded[schemas.StaffMemberOut](admin.Post("/v1/operator/staff", testclient.JSON(map[string]any{
		"email": address, "roles": []string{"support"},
	})).AssertStatus(http.StatusCreated))
	if added.Email != strings.ToLower(address) || added.CreatedBy != strings.ToLower(me.Email) {
		t.Fatalf("added %+v", added)
	}
	admin.Put("/v1/operator/staff/"+added.ID.String(), testclient.JSON(map[string]any{"active": false})).
		AssertStatus(http.StatusOK)

	admin.Post("/v1/operator/staff", testclient.JSON(map[string]any{
		"email": unique("someone") + "@playersense.dev", "roles": []string{"owner"},
	})).AssertStatus(http.StatusUnprocessableEntity)
}

// TestEveryCustomerRouteAsksForATicket reads the API's own description, so a new operator route
// that names a customer and forgets the ticket fails here rather than in an access review.
func TestEveryCustomerRouteAsksForATicket(t *testing.T) {
	app := mustBuild(t, testSettings(t))
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	doc := testclient.New(t, app).Get("/openapi.json").AssertStatus(http.StatusOK)
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(doc.Body, &spec); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for path, ops := range spec.Paths {
		if !strings.HasPrefix(path, "/v1/operator/") {
			continue
		}
		if !strings.Contains(path, "{org}") && !strings.Contains(path, "{project}") {
			continue
		}
		for method, op := range ops {
			checked++
			if !slices.ContainsFunc(op.Parameters, func(p struct {
				Name string `json:"name"`
				In   string `json:"in"`
			}) bool {
				return p.In == "header" && strings.EqualFold(p.Name, "X-Staff-Ticket")
			}) {
				t.Errorf("%s %s reaches a customer and does not ask for X-Staff-Ticket", strings.ToUpper(method), path)
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d operator routes name a customer; the test is not reading the document", checked)
	}
}

func (s *stack) accessCount(t *testing.T, who fbtest.Person) int {
	t.Helper()
	n, err := s.db.Client.StaffAccess.Query().
		Where(staffaccess.StaffEmail(strings.ToLower(who.Email))).Count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
