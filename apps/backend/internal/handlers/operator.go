package handlers

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/apikey"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/configversion"
	"playersense.dev/backend/ent/creditlot"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/featureflag"
	"playersense.dev/backend/ent/invitation"
	"playersense.dev/backend/ent/liveopsevent"
	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/organizationmember"
	"playersense.dev/backend/ent/plan"
	"playersense.dev/backend/ent/predicate"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/ent/segment"
	"playersense.dev/backend/ent/usagedaily"
	"playersense.dev/backend/ent/user"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// The operations console's view of one customer (§56). Every handler here runs behind
// core.StaffReads, so by the time it does, the ticket is recorded and the customer the path names
// has been resolved: StaffAccessRecord carries it.

// ---- finding a customer -----------------------------------------------------

// SearchOrganizations is the console's directory.
//
// q is read as whatever it looks like: an address finds the organizations its person belongs to,
// a UUID or a number finds one organization, and anything else is a slug prefix or part of a name.
func SearchOrganizations(ctx *muzak.Context, in schemas.OperatorOrganizationSearchIn) (schemas.OperatorOrganizationListOut, error) {
	db := muzak.From[*core.DB](ctx)
	q := db.Client.Organization.Query()

	if in.Q != "" {
		switch text := in.Q; {
		case strings.Contains(text, "@"):
			q = q.Where(organization.HasMembersWith(
				organizationmember.HasUserWith(user.Email(strings.ToLower(text)))))
		case isUUID(text):
			q = q.Where(organization.UID(uuid.MustParse(text)))
		case isSeq(text):
			seq, _ := strconv.Atoi(text)
			q = q.Where(organization.ID(seq))
		default:
			q = q.Where(organization.Or(
				organization.SlugHasPrefix(strings.ToLower(text)),
				organization.NameContainsFold(text)))
		}
	}
	var account []predicate.BillingAccount
	if in.Plan != "" {
		account = append(account, billingaccount.HasPlanWith(plan.Key(in.Plan)))
	}
	if in.State != "" {
		account = append(account, billingaccount.StateEQ(billingaccount.State(in.State)))
	}
	if in.Collection != "" {
		account = append(account, billingaccount.CollectionEQ(billingaccount.Collection(in.Collection)))
	}
	if len(account) > 0 {
		q = q.Where(organization.HasBillingAccountWith(account...))
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.OperatorOrganizationListOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(organization.IDLT(id))
	}

	rows, err := q.Order(ent.Desc(organization.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.OperatorOrganizationListOut{}, core.AsHTTPError(err)
	}
	out := schemas.OperatorOrganizationListOut{Organizations: []schemas.OperatorOrganizationSummaryOut{}}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	summaries, err := organizationSummaries(ctx.Context(), db, rows)
	if err != nil {
		return schemas.OperatorOrganizationListOut{}, core.AsHTTPError(err)
	}
	out.Organizations = summaries
	return out, nil
}

// organizationSummaries renders a page of organizations in five queries however long it is:
// accounts with their plans, balances, and one grouped count each of projects and members.
func organizationSummaries(ctx context.Context, db *core.DB, rows []*ent.Organization) ([]schemas.OperatorOrganizationSummaryOut, error) {
	out := make([]schemas.OperatorOrganizationSummaryOut, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]int, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	accounts, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationIDIn(ids...)).WithPlan().All(ctx)
	if err != nil {
		return nil, err
	}
	plans := make(map[int]string, len(accounts))
	for _, account := range accounts {
		if account.Edges.Plan != nil {
			plans[account.OrganizationID] = account.Edges.Plan.Key
		}
	}
	balances, err := balancesOf(ctx, db, ids, nowUTC())
	if err != nil {
		return nil, err
	}
	projects, err := countBy(ctx, db.Client.Project.Query().Where(project.OrganizationIDIn(ids...)).
		GroupBy(project.FieldOrganizationID))
	if err != nil {
		return nil, err
	}
	members, err := countBy(ctx, db.Client.OrganizationMember.Query().
		Where(organizationmember.OrganizationIDIn(ids...)).GroupBy(organizationmember.FieldOrganizationID))
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		summary := schemas.OperatorOrganizationSummaryOut{
			ID: row.UID, Seq: row.ID, Slug: row.Slug, Name: row.Name, CreatedAt: row.CreatedAt,
			Plan: plans[row.ID], Projects: projects[row.ID], Members: members[row.ID],
		}
		if balance, found := balances[row.ID]; found {
			summary.Balance = &balance
		}
		out = append(out, summary)
	}
	return out, nil
}

// countBy counts rows per organization. Each query builder's GroupBy has its own type, so this takes
// the one thing they share.
func countBy[G interface {
	Aggregate(...ent.AggregateFunc) S
}, S interface {
	Scan(context.Context, any) error
}](ctx context.Context, by G) (map[int]int, error) {
	var rows []struct {
		OrganizationID int `json:"organization_id"`
		Count          int `json:"count"`
	}
	if err := by.Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[int]int, len(rows))
	for _, row := range rows {
		out[row.OrganizationID] = row.Count
	}
	return out, nil
}

// ReadOperatorOrganization is one customer on one screen: who they are, who is in it, what it
// runs, and the account behind it.
func ReadOperatorOrganization(ctx *muzak.Context, _ schemas.OperatorOrganizationParams) (schemas.OperatorOrganizationOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization

	summaries, err := organizationSummaries(ctx.Context(), db, []*ent.Organization{org})
	if err != nil {
		return schemas.OperatorOrganizationOut{}, core.AsHTTPError(err)
	}
	out := schemas.OperatorOrganizationOut{
		OperatorOrganizationSummaryOut: summaries[0],
		MemberLimit:                    org.MemberLimit,
	}

	projects, err := db.Client.Project.Query().Where(project.OrganizationID(org.ID)).
		Order(ent.Asc(project.FieldSlug)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	out.ProjectList = make([]schemas.OperatorProjectRefOut, 0, len(projects))
	for _, proj := range projects {
		out.ProjectList = append(out.ProjectList, operatorProjectRef(proj))
	}

	members, err := db.Client.OrganizationMember.Query().Where(organizationmember.OrganizationID(org.ID)).
		WithUser().WithRole().Order(ent.Asc(organizationmember.FieldJoinedAt)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	out.MemberList = make([]schemas.OperatorMemberOut, 0, len(members))
	for _, member := range members {
		one := schemas.OperatorMemberOut{AllProjects: member.AllProjects, JoinedAt: member.JoinedAt}
		if person := member.Edges.User; person != nil {
			one.Email, one.Name, one.LastSeenAt, one.Active = person.Email, person.Name, person.LastSeenAt, person.Active
		}
		if member.Edges.Role != nil {
			one.Role = roleKeyOf(member.Edges.Role)
		}
		out.MemberList = append(out.MemberList, one)
	}

	out.PendingInvitations, err = db.Client.Invitation.Query().Where(
		invitation.OrganizationID(org.ID), invitation.StatusEQ(invitation.StatusPending),
		invitation.ExpiresAtGT(nowUTC())).Count(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}

	account, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(org.ID)).Only(ctx.Context())
	switch {
	case err == nil:
		one := operatorAccountOut(account)
		out.Account = &one
	case !ent.IsNotFound(err):
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

func operatorAccountOut(account *ent.BillingAccount) schemas.OperatorAccountOut {
	out := schemas.OperatorAccountOut{
		ID: account.UID, State: string(account.State), StateChangedAt: account.StateChangedAt,
		Collection: string(account.Collection), SubscriptionEnds: account.SubscriptionPeriodEnd,
		Overrides: account.EntitlementOverrides,
	}
	if account.StripeCustomerID != nil {
		out.StripeCustomerID = *account.StripeCustomerID
	}
	if account.StripeSubscriptionID != nil {
		out.StripeSubscriptionID = *account.StripeSubscriptionID
	}
	if account.SubscriptionStatus != nil {
		out.SubscriptionStatus = *account.SubscriptionStatus
	}
	if account.SubscriptionInterval != nil {
		out.SubscriptionInterval = string(*account.SubscriptionInterval)
	}
	if account.BillingEmail != nil {
		out.BillingEmail = *account.BillingEmail
	}
	return out
}

func operatorProjectRef(proj *ent.Project) schemas.OperatorProjectRefOut {
	return schemas.OperatorProjectRefOut{
		ID: proj.UID, Seq: proj.ID, Slug: proj.Slug, Name: proj.Name,
		PolicyTier: string(proj.PolicyTier), RetentionDays: proj.RetentionDays, CreatedAt: proj.CreatedAt,
	}
}

// SearchProjects finds a project by what support or the ingestion page has: its id, the sequence
// number ClickHouse knows it by, its slug or its name.
func SearchProjects(ctx *muzak.Context, in schemas.OperatorProjectSearchIn) (schemas.OperatorProjectListOut, error) {
	db := muzak.From[*core.DB](ctx)
	q := db.Client.Project.Query()
	if in.Q != "" {
		switch text := in.Q; {
		case isUUID(text):
			q = q.Where(project.UID(uuid.MustParse(text)))
		case isSeq(text):
			seq, _ := strconv.Atoi(text)
			q = q.Where(project.ID(seq))
		default:
			q = q.Where(project.Or(project.SlugHasPrefix(strings.ToLower(text)), project.NameContainsFold(text)))
		}
	}
	if in.Tier != "" {
		q = q.Where(project.PolicyTierEQ(project.PolicyTier(in.Tier)))
	}
	if in.Before != "" {
		id, err := strconv.Atoi(in.Before)
		if err != nil {
			return schemas.OperatorProjectListOut{}, muzak.BadRequest("before is not a cursor this API returned")
		}
		q = q.Where(project.IDLT(id))
	}
	rows, err := q.WithOrganization().Order(ent.Desc(project.FieldID)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.OperatorProjectListOut{}, core.AsHTTPError(err)
	}
	out := schemas.OperatorProjectListOut{Projects: make([]schemas.OperatorProjectSummaryOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = strconv.Itoa(rows[len(rows)-1].ID)
	}
	for _, row := range rows {
		out.Projects = append(out.Projects, operatorProjectSummary(row, row.Edges.Organization))
	}
	return out, nil
}

func operatorProjectSummary(proj *ent.Project, org *ent.Organization) schemas.OperatorProjectSummaryOut {
	out := schemas.OperatorProjectSummaryOut{OperatorProjectRefOut: operatorProjectRef(proj)}
	if org != nil {
		out.Organization = schemas.OrganizationRefOut{Slug: org.Slug, Name: org.Name}
	}
	return out
}

// ReadOperatorProject is one project: its environments and every key each has had, how much
// targeting it defines, and thirty days of its meter.
func ReadOperatorProject(ctx *muzak.Context, _ schemas.OperatorProjectParams) (schemas.OperatorProjectOut, error) {
	db := muzak.From[*core.DB](ctx)
	record := muzak.From[core.StaffAccessRecord](ctx)
	proj, org := record.Project, record.Organization
	out := schemas.OperatorProjectOut{OperatorProjectSummaryOut: operatorProjectSummary(proj, org)}

	envs, err := db.Client.Environment.Query().Where(environment.ProjectID(proj.ID)).
		Order(ent.Asc(environment.FieldID)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	envIDs := make([]int, len(envs))
	for i, env := range envs {
		envIDs[i] = env.ID
	}
	keys, err := db.Client.APIKey.Query().Where(apikey.EnvironmentIDIn(envIDs...)).
		Order(ent.Desc(apikey.FieldID)).All(ctx.Context())
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	out.Environments = make([]schemas.OperatorEnvironmentOut, 0, len(envs))
	for _, env := range envs {
		one := schemas.OperatorEnvironmentOut{ID: env.UID, Seq: env.ID, Kind: string(env.Kind), Keys: []schemas.APIKeyOut{}}
		for _, key := range keys {
			if key.EnvironmentID == env.ID {
				one.Keys = append(one.Keys, apiKeyOut(key, string(env.Kind)))
			}
		}
		out.Environments = append(out.Environments, one)
	}

	counts := []struct {
		into  *int
		count func(context.Context) (int, error)
	}{
		{&out.Segments, db.Client.Segment.Query().Where(segment.ProjectID(proj.ID)).Count},
		{&out.Flags, db.Client.FeatureFlag.Query().Where(featureflag.ProjectID(proj.ID)).Count},
		{&out.LiveOpsEvents, db.Client.LiveOpsEvent.Query().Where(liveopsevent.ProjectID(proj.ID)).Count},
		{&out.ConfigVersions, db.Client.ConfigVersion.Query().Where(configversion.ProjectID(proj.ID)).Count},
	}
	for _, c := range counts {
		if *c.into, err = c.count(ctx.Context()); err != nil {
			return out, core.AsHTTPError(err)
		}
	}

	now := nowUTC()
	usage, err := usageOf(ctx.Context(), db, org.ID,
		now.AddDate(0, 0, -29).Format(time.DateOnly), now.Format(time.DateOnly))
	if err != nil {
		return out, err
	}
	for _, share := range usage.Projects {
		if share.Project.ID == proj.UID {
			out.Usage = share.Totals
		}
	}
	out.Days, err = projectDays(ctx.Context(), db, proj.ID, now.AddDate(0, 0, -29))
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// ---- entitlements -----------------------------------------------------------

// ReadEntitlements is what an organization may use: the plan's, the overrides, and the two
// layered.
func ReadEntitlements(ctx *muzak.Context, _ schemas.OperatorOrganizationParams) (schemas.EntitlementsOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.EntitlementsOut{}, err
	}
	return entitlementsOut(org, account), nil
}

func entitlementsOut(org *ent.Organization, account *ent.BillingAccount) schemas.EntitlementsOut {
	out := schemas.EntitlementsOut{
		PlanEntitlements:    map[string]int64{},
		Overrides:           map[string]int64{},
		Resolved:            map[string]int64{},
		MemberLimit:         org.MemberLimit,
		AutoTopUpDailyLimit: account.AutoTopupDailyLimit,
	}
	if p := account.Edges.Plan; p != nil {
		out.Plan = p.Key
		maps.Copy(out.PlanEntitlements, p.Entitlements)
	}
	maps.Copy(out.Overrides, account.EntitlementOverrides)
	maps.Copy(out.Resolved, resolvedEntitlements(account))
	for _, name := range billing.Entitlements() {
		out.Vocabulary = append(out.Vocabulary, string(name))
	}
	return out
}

// SetEntitlements changes what one organization may use, merged into what it has: a number sets an
// override, null clears it back to the plan's, and a name not sent is left alone.
//
// Recorded in the organization's audit log with the previous values and the reason, and the
// cached pricing is dropped so the charger enforces the change from the next player.
func SetEntitlements(ctx *muzak.Context, in schemas.SetEntitlementsIn) (schemas.EntitlementsOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization

	changes := billing.Limits{}
	for name, amount := range in.Overrides {
		if amount != nil {
			changes[billing.Entitlement(name)] = *amount
		} else if !billing.Known(billing.Entitlement(name)) {
			return schemas.EntitlementsOut{}, muzak.UnprocessableEntity(name + " is not an entitlement")
		}
	}
	if err := changes.Validate(); err != nil {
		return schemas.EntitlementsOut{}, catalogueError(err)
	}
	if len(in.Overrides) == 0 && in.MemberLimit == nil && in.AutoTopUpDailyLimit == nil {
		return schemas.EntitlementsOut{}, muzak.UnprocessableEntity("nothing to change")
	}

	_, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		var none struct{}
		account, err := tx.BillingAccount.Query().
			Where(billingaccount.OrganizationID(org.ID)).ForUpdate().Only(ctx.Context())
		if err != nil {
			return none, err
		}
		was := map[string]any{}
		now := map[string]any{}

		overrides := maps.Clone(account.EntitlementOverrides)
		if overrides == nil {
			overrides = map[string]int64{}
		}
		for _, name := range slices.Sorted(maps.Keys(in.Overrides)) {
			previous, had := overrides[name]
			if had {
				was[name] = previous
			} else {
				was[name] = nil
			}
			if amount := in.Overrides[name]; amount != nil {
				overrides[name] = *amount
				now[name] = *amount
			} else {
				delete(overrides, name)
				now[name] = nil
			}
		}
		update := tx.BillingAccount.UpdateOne(account).SetEntitlementOverrides(overrides)
		if in.AutoTopUpDailyLimit != nil {
			was["auto_topup_daily_limit"] = account.AutoTopupDailyLimit
			now["auto_topup_daily_limit"] = *in.AutoTopUpDailyLimit
			update = update.SetAutoTopupDailyLimit(*in.AutoTopUpDailyLimit)
		}
		if err := update.Exec(ctx.Context()); err != nil {
			return none, err
		}
		if in.MemberLimit != nil {
			was["member_limit"] = org.MemberLimit
			now["member_limit"] = *in.MemberLimit
			if err := tx.Organization.UpdateOneID(org.ID).SetMemberLimit(*in.MemberLimit).
				Exec(ctx.Context()); err != nil {
				return none, err
			}
		}
		return none, auditFor(ctx).forOrganization(org.ID).byStaff(ctx).
			record(ctx.Context(), tx, audit.EntitlementsSet, "billing_account", org.Slug,
				map[string]any{"was": was, "now": now, "reason": in.Reason})
	})
	if err != nil {
		return schemas.EntitlementsOut{}, catalogueError(err)
	}
	invalidateOrg(ctx, org.ID)

	org, err = db.Client.Organization.Get(ctx.Context(), org.ID)
	if err != nil {
		return schemas.EntitlementsOut{}, core.AsHTTPError(err)
	}
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.EntitlementsOut{}, err
	}
	return entitlementsOut(org, account), nil
}

// ---- the wallet, as staff see it --------------------------------------------

// ReadOperatorBilling is the customer's billing page, plus the account's Stripe identity and the
// automatic top-up in flight.
func ReadOperatorBilling(ctx *muzak.Context, _ schemas.OperatorOrganizationParams) (schemas.OperatorBillingOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.OperatorBillingOut{}, err
	}
	page, err := billingOut(ctx.Context(), db, account, nowUTC(), policyOf(ctx))
	if err != nil {
		return schemas.OperatorBillingOut{}, err
	}
	out := schemas.OperatorBillingOut{
		BillingOut: page, Account: operatorAccountOut(account),
		AutoTopUpPendingAt: account.AutoTopupPendingAt, AutoTopUpPendingMicros: account.AutoTopupPendingAmountMicros,
		InvoicedThrough: account.InvoicedThrough,
	}
	if account.AutoTopupPendingKey != nil {
		out.AutoTopUpPendingKey = *account.AutoTopupPendingKey
	}
	return out, nil
}

// ReadOperatorLedger is the customer's statement, paged.
func ReadOperatorLedger(ctx *muzak.Context, in schemas.OperatorLedgerIn) (schemas.LedgerOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.LedgerOut{}, err
	}
	return ledgerOf(ctx.Context(), db, account.ID, in.Limit, in.Before)
}

// ReadOperatorCreditLots is every grant: by default those with credit left, and with all=true the
// spent and lapsed ones too, which is where "where did my $100 go" is answered.
func ReadOperatorCreditLots(ctx *muzak.Context, in schemas.OperatorCreditLotsIn) (schemas.OperatorCreditLotsOut, error) {
	db := muzak.From[*core.DB](ctx)
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	account, err := accountOf(ctx.Context(), db, org.ID)
	if err != nil {
		return schemas.OperatorCreditLotsOut{}, err
	}
	q := db.Client.CreditLot.Query().Where(creditlot.AccountID(account.ID))
	if !in.All {
		q = q.Where(creditlot.RemainingMicrosGT(0))
	}
	rows, err := q.Order(ent.Desc(creditlot.FieldGrantedAt), ent.Desc(creditlot.FieldID)).
		Limit(1000).All(ctx.Context())
	if err != nil {
		return schemas.OperatorCreditLotsOut{}, core.AsHTTPError(err)
	}
	now := nowUTC()
	out := schemas.OperatorCreditLotsOut{Lots: make([]schemas.OperatorCreditLotOut, 0, len(rows))}
	for _, row := range rows {
		one := schemas.OperatorCreditLotOut{
			CreditLotOut: creditLotOut(row, now), GrantedByStaff: row.GrantedByStaff, Reference: row.IdempotencyKey,
		}
		if row.StripeReference != nil {
			one.StripeReference = *row.StripeReference
		}
		out.Lots = append(out.Lots, one)
	}
	return out, nil
}

// ReadOperatorUsage is the customer's usage page.
func ReadOperatorUsage(ctx *muzak.Context, in schemas.OperatorUsageIn) (schemas.UsageOut, error) {
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	return usageOf(ctx.Context(), muzak.From[*core.DB](ctx), org.ID, in.From, in.To)
}

// ReadOperatorAudit is the customer's own log, as they would read it.
func ReadOperatorAudit(ctx *muzak.Context, in schemas.OperatorAuditIn) (schemas.AuditListOut, error) {
	org := muzak.From[core.StaffAccessRecord](ctx).Organization
	return auditOf(ctx.Context(), muzak.From[*core.DB](ctx), org.ID, in.AuditFilter)
}

// ---- helpers ----------------------------------------------------------------

// projectDays is one project's meter, day by day, from since.
func projectDays(ctx context.Context, db *core.DB, projectID int, since time.Time) ([]schemas.UsageDayOut, error) {
	rows, err := db.Client.UsageDaily.Query().
		Where(usagedaily.ProjectID(projectID), usagedaily.DayGTE(since.Truncate(24*time.Hour))).
		Order(ent.Asc(usagedaily.FieldDay)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]schemas.UsageDayOut, 0, len(rows))
	for _, row := range rows {
		out = append(out, schemas.UsageDayOut{
			Day: row.Day, TrackedPlayers: row.TrackedPlayers, Events: row.EventsAccepted,
			RetainedBytes: row.RetainedBytes, Final: row.Final,
		})
	}
	return out, nil
}

func isUUID(text string) bool {
	_, err := uuid.Parse(text)
	return err == nil
}

func isSeq(text string) bool {
	n, err := strconv.Atoi(text)
	return err == nil && n > 0
}
