// Package staff says who at PlayerSense may use the operations console, and for what (§56, §104).
//
// The customer side has roles a studio composes from permissions (package authz). Staff have four
// fixed roles instead, because the console is ours and its separations are ours to decide:
//
//	ops      the platform itself: ingestion, the queue, ClickHouse, storage, jobs, rate limits.
//	         Aggregates across tenants; nothing that reads one customer's records.
//	support  one customer's records: their organization, projects, members, audit log and
//	         data-subject requests. Every such read names a ticket (§104).
//	billing  one customer's money: wallets, ledgers, invoices, plans, prices, entitlements, and
//	         the writes on them. Every such request names a ticket too.
//	admin    all of the above, and who is staff.
//
// Roles are separable on purpose. The person on the billing desk grants credit without being able
// to open a studio's audit log, and an on-call engineer reads the queue without reaching anybody's
// customer data at all.
package staff

import (
	"fmt"
	"slices"
	"strings"
)

// Role is one of the four.
type Role string

const (
	Ops     Role = "ops"
	Support Role = "support"
	Billing Role = "billing"
	Admin   Role = "admin"
)

// Roles is the closed list, in the order a console shows it.
var Roles = []Role{Ops, Support, Billing, Admin}

// Member is a signed-in staff member, as the console's routes see them.
type Member struct {
	ID    int
	Email string
	Name  string
	Roles []Role
}

// Has reports whether the member may act as role. Admin may act as every role.
func (m Member) Has(role Role) bool {
	return slices.Contains(m.Roles, role) || slices.Contains(m.Roles, Admin)
}

// HasAny reports whether the member holds at least one of roles.
func (m Member) HasAny(roles ...Role) bool {
	return slices.ContainsFunc(roles, m.Has)
}

// Parse validates a list of role names, rejecting any outside the closed list and any repeat.
func Parse(names []string) ([]Role, error) {
	out := make([]Role, 0, len(names))
	for _, name := range names {
		role := Role(strings.ToLower(strings.TrimSpace(name)))
		if !slices.Contains(Roles, role) {
			return nil, fmt.Errorf("%q is not a staff role; the roles are ops, support, billing and admin", name)
		}
		if slices.Contains(out, role) {
			return nil, fmt.Errorf("%q is listed twice", name)
		}
		out = append(out, role)
	}
	return out, nil
}

// Names renders roles as the strings the table stores.
func Names(roles []Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

// NormalizeEmail is how an address is compared: trimmed and lowercased, so the verified token's
// address matches however somebody typed it when adding them.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// KeepsAnAdmin reports whether a change to one staff member leaves the console with an active
// admin: either they are still one, or somebody else is. A console with none could not add one back.
func KeepsAnAdmin(stillAdmin bool, otherActiveAdmins int) bool {
	return stillAdmin || otherActiveAdmins > 0
}
