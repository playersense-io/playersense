package staff

import "testing"

func TestAdminMayActAsEveryRole(t *testing.T) {
	admin := Member{Roles: []Role{Admin}}
	for _, r := range Roles {
		if !admin.Has(r) {
			t.Errorf("an admin may not act as %s", r)
		}
	}
	billing := Member{Roles: []Role{Billing}}
	if billing.Has(Support) || billing.Has(Admin) || !billing.Has(Billing) {
		t.Errorf("billing's reach is wrong: support=%v admin=%v billing=%v",
			billing.Has(Support), billing.Has(Admin), billing.Has(Billing))
	}
}

func TestOnlyTheFourRolesParse(t *testing.T) {
	if _, err := Parse([]string{"ops", " Support "}); err != nil {
		t.Fatalf("valid roles refused: %v", err)
	}
	for _, bad := range [][]string{{"owner"}, {"ops", "ops"}, {""}} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%v parsed", bad)
		}
	}
}

func TestTheLastAdminStaysOne(t *testing.T) {
	if KeepsAnAdmin(false, 0) {
		t.Error("the last admin was allowed to stop being one")
	}
	if !KeepsAnAdmin(false, 1) || !KeepsAnAdmin(true, 0) {
		t.Error("a change that leaves an admin was refused")
	}
}
