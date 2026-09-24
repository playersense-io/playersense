package main

import (
	"playersense.dev/backend/ent/predicate"
	"playersense.dev/backend/ent/staffmember"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/internal/firebase/fbtest"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/staff"
)

// testTicket is the ticket every staff client sends, so staff_access has a reason to record.
const testTicket = "SUP-1042"

// seedStaff makes somebody PlayerSense staff with the given roles.
func seedStaff(t *testing.T, db *pgdb.DB, who fbtest.Person, roles ...staff.Role) {
	t.Helper()
	if _, err := db.Client.StaffMember.Create().
		SetEmail(staff.NormalizeEmail(who.Email)).
		SetName(who.Name).
		SetRoles(staff.Names(roles)).
		SetCreatedBy("test").
		Save(t.Context()); err != nil {
		t.Fatalf("seed staff %s: %v", who.Email, err)
	}
}

// staffClient is a new staff member with roles, signed in, sending a ticket with every request.
func (s *stack) staffClient(t *testing.T, roles ...staff.Role) (*testclient.Client, fbtest.Person) {
	t.Helper()
	who := person(t)
	seedStaff(t, s.db, who, roles...)
	return testclient.New(t, s.app,
		testclient.WithHeader("Authorization", "Bearer "+s.google.Token(who)),
		testclient.WithHeader("X-Staff-Ticket", testTicket)), who
}

// staffOnly keeps the client of a staffClient pair.
func staffOnly(c *testclient.Client, _ fbtest.Person) *testclient.Client { return c }

// staffEmailIs matches a staff row by the person's address.
func staffEmailIs(who fbtest.Person) predicate.StaffMember {
	return staffmember.Email(staff.NormalizeEmail(who.Email))
}
