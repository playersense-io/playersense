package main

import (
	"net/http"
	"testing"

	"muzak.dev/framework/testclient"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
)

// A plan's counted limits hold: an organization makes as many projects as its plan allows and
// no more, and a project as many webhook destinations. The refusal says which limit it hit, so
// the dashboard can offer the plan that lifts it.
func TestPlanLimitsHoldProjectsAndWebhookDestinations(t *testing.T) {
	s := newStack(t)
	owner := person(t)
	org := s.createOrg(t, owner)
	client := s.as(t, owner)

	// A plan of its own, so the limits are this test's and nobody else's.
	ctx := t.Context()
	plan, err := s.db.Client.Plan.Create().SetKey(unique("limits")).SetName("Limits").
		SetEntitlements(map[string]int64{"max_projects": 1, "max_webhook_endpoints": 1}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orgID := s.organization(t, org).ID
	account, err := s.db.Client.BillingAccount.Query().Where(billingaccount.OrganizationID(orgID)).Only(ctx)
	switch {
	case err == nil:
		err = account.Update().SetPlanID(plan.ID).SetEntitlementOverrides(map[string]int64{}).Exec(ctx)
	case ent.IsNotFound(err):
		err = s.db.Client.BillingAccount.Create().SetOrganizationID(orgID).SetPlanID(plan.ID).Exec(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}

	project := s.createProject(t, owner, org)
	refused := client.Post("/v1/organizations/"+org+"/projects",
		testclient.JSON(map[string]string{"name": "Second", "slug": unique("game")})).AssertStatus(http.StatusConflict).String()
	if !contains(refused, "max_projects") {
		t.Errorf("a second project was refused without naming its limit: %s", refused)
	}

	hooks := "/v1/organizations/" + org + "/projects/" + project + "/webhooks"
	destination := map[string]any{"name": "Backend", "url": "https://hooks.example.com/playersense",
		"events": []string{"*"}, "environments": []string{"production"}}
	client.Post(hooks, testclient.JSON(destination)).AssertStatus(http.StatusCreated)
	refused = client.Post(hooks, testclient.JSON(destination)).AssertStatus(http.StatusConflict).String()
	if !contains(refused, "max_webhook_endpoints") {
		t.Errorf("a second destination was refused without naming its limit: %s", refused)
	}

	// An override lifts the plan's limit for this organization alone.
	if err := s.db.Client.BillingAccount.Update().Where(billingaccount.OrganizationID(orgID)).
		SetEntitlementOverrides(map[string]int64{"max_projects": 2}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	s.createProject(t, owner, org)
}
