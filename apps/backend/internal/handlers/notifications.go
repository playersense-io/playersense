package handlers

import (
	"fmt"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/notificationchannel"
	"playersense.dev/backend/ent/notificationrule"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/alerts"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/notify"
	"playersense.dev/backend/internal/schemas"
)

// Notification rules (ADR 0051): a project's, which hear its events, and the organization's, which
// hear its account's. One set of handlers over both; the scope is where a rule lives and which
// channels it may point at.

// ruleScope is where a request's rules live.
type ruleScope struct {
	scope     alerts.Scope
	orgID     int
	projectID *int
	label     string // the project's slug, or "organization", for the audit log
}

func projectScope(ctx *muzak.Context) ruleScope {
	p := muzak.From[*core.Principal](ctx)
	id := p.Project.ID
	return ruleScope{scope: alerts.ScopeProject, orgID: p.Organization.ID, projectID: &id, label: p.Project.Slug}
}

func orgScope(ctx *muzak.Context) ruleScope {
	p := muzak.From[*core.Principal](ctx)
	return ruleScope{scope: alerts.ScopeOrganization, orgID: p.Organization.ID, label: "organization"}
}

func (s ruleScope) query(client *ent.Client) *ent.NotificationRuleQuery {
	q := client.NotificationRule.Query().Where(notificationrule.OrganizationID(s.orgID))
	if s.projectID == nil {
		return q.Where(notificationrule.ProjectIDIsNil())
	}
	return q.Where(notificationrule.ProjectID(*s.projectID))
}

// channels is the channels a rule of this scope may notify: the project's own, or any of the
// organization's projects'.
func (s ruleScope) channels(client *ent.Client) *ent.NotificationChannelQuery {
	if s.projectID != nil {
		return client.NotificationChannel.Query().Where(notificationchannel.ProjectID(*s.projectID))
	}
	return client.NotificationChannel.Query().
		Where(notificationchannel.HasProjectWith(project.OrganizationID(s.orgID)))
}

func ruleOut(row *ent.NotificationRule) schemas.NotificationRuleOut {
	scope := alerts.ScopeProject
	if row.ProjectID == nil {
		scope = alerts.ScopeOrganization
	}
	environments := row.Environments
	if environments == nil {
		environments = []string{}
	}
	return schemas.NotificationRuleOut{
		ID: row.UID, Scope: string(scope), Name: row.Name, Enabled: row.Enabled, Events: row.Events,
		Environments: environments, Channels: row.Channels, CooldownMinutes: row.CooldownMinutes,
		MutedUntil: row.MutedUntil, LastFiredAt: row.LastFiredAt, CreatedAt: row.CreatedAt,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy,
	}
}

// checkRule refuses what a rule of this scope cannot say: an event of the other scope, an
// environment that does not exist, a channel it cannot reach.
func checkRule(ctx *muzak.Context, s ruleScope, in schemas.NotificationRuleBody) error {
	if t, ok := alerts.Valid(s.scope, in.Events); !ok {
		return muzak.UnprocessableEntity(fmt.Sprintf("a rule of the %s cannot hear %q", s.scope, t)).
			WithCode("invalid_event")
	}
	for _, env := range in.Environments {
		if env != "production" && env != "staging" && env != "development" {
			return muzak.UnprocessableEntity(fmt.Sprintf("%q is not an environment", env)).
				WithCode("invalid_environment")
		}
	}
	db := muzak.From[*core.DB](ctx)
	found, err := s.channels(db.Client).Where(notificationchannel.UIDIn(in.Channels...)).Count(ctx.Context())
	if err != nil {
		return core.AsHTTPError(err)
	}
	if found != len(unique(in.Channels)) {
		return muzak.UnprocessableEntity("a channel is not one this rule can notify").WithCode("invalid_channel")
	}
	return nil
}

func unique(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func listRules(ctx *muzak.Context, s ruleScope) (schemas.NotificationRuleListOut, error) {
	db := muzak.From[*core.DB](ctx)
	rows, err := s.query(db.Client).Order(ent.Asc(notificationrule.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.NotificationRuleListOut{}, core.AsHTTPError(err)
	}
	out := schemas.NotificationRuleListOut{Rules: make([]schemas.NotificationRuleOut, 0, len(rows))}
	for _, row := range rows {
		out.Rules = append(out.Rules, ruleOut(row))
	}
	return out, nil
}

func findRule(ctx *muzak.Context, s ruleScope, id uuid.UUID) (*ent.NotificationRule, error) {
	db := muzak.From[*core.DB](ctx)
	row, err := s.query(db.Client).Where(notificationrule.UID(id)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("there is no such notification rule")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

func environmentsOf(in schemas.NotificationRuleBody) []string {
	if in.Environments == nil {
		return []string{}
	}
	return in.Environments
}

func createRule(ctx *muzak.Context, s ruleScope, in schemas.NotificationRuleBody) (schemas.NotificationRuleOut, error) {
	if err := checkRule(ctx, s, in); err != nil {
		return schemas.NotificationRuleOut{}, err
	}
	who := actorOf(ctx).Label
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.NotificationRule, error) {
		create := tx.NotificationRule.Create().SetOrganizationID(s.orgID).SetNillableProjectID(s.projectID).
			SetName(in.Name).SetEvents(in.Events).SetEnvironments(environmentsOf(in)).
			SetChannels(unique(in.Channels)).SetCooldownMinutes(in.CooldownMinutes).
			SetNillableMutedUntil(in.MutedUntil).SetCreatedBy(who).SetUpdatedBy(who)
		if in.Enabled != nil {
			create = create.SetEnabled(*in.Enabled)
		}
		row, err := create.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.NotificationRuleWritten, "notification_rule",
			row.UID.String(), map[string]any{"scope": s.label, "name": in.Name, "events": in.Events, "created": true})
	})
	if err != nil {
		return schemas.NotificationRuleOut{}, err
	}
	ctx.SetStatus(201)
	return ruleOut(row), nil
}

func updateRule(ctx *muzak.Context, s ruleScope, id uuid.UUID, in schemas.NotificationRuleBody) (schemas.NotificationRuleOut, error) {
	row, err := findRule(ctx, s, id)
	if err != nil {
		return schemas.NotificationRuleOut{}, err
	}
	if err := checkRule(ctx, s, in); err != nil {
		return schemas.NotificationRuleOut{}, err
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.NotificationRule, error) {
		update := tx.NotificationRule.UpdateOne(row).SetName(in.Name).SetEvents(in.Events).
			SetEnvironments(environmentsOf(in)).SetChannels(unique(in.Channels)).
			SetCooldownMinutes(in.CooldownMinutes).SetUpdatedBy(actorOf(ctx).Label)
		if in.MutedUntil != nil {
			update = update.SetMutedUntil(*in.MutedUntil)
		} else {
			update = update.ClearMutedUntil()
		}
		if in.Enabled != nil {
			update = update.SetEnabled(*in.Enabled)
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.NotificationRuleWritten, "notification_rule",
			row.UID.String(), map[string]any{"scope": s.label, "name": in.Name, "events": in.Events,
				"enabled": updated.Enabled, "created": false})
	})
	if err != nil {
		return schemas.NotificationRuleOut{}, err
	}
	return ruleOut(updated), nil
}

func deleteRule(ctx *muzak.Context, s ruleScope, id uuid.UUID) (muzak.Empty, error) {
	row, err := findRule(ctx, s, id)
	if err != nil {
		return muzak.Empty{}, err
	}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		if err := tx.NotificationRule.DeleteOne(row).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.NotificationRuleDeleted,
			"notification_rule", row.UID.String(), map[string]any{"scope": s.label, "name": row.Name})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}

// sampleData is what a test's message shows: the kind of detail the real event carries.
func sampleData(kind alerts.Kind) map[string]any {
	if kind.Group == "billing" {
		return map[string]any{"reason": "Your card was declined.", "amount": "$50.00", "balance": "-$4.20"}
	}
	return map[string]any{"sample": "a test of this rule, not a real event"}
}

// testRule sends a sample of the rule's first event to each of its channels, now, as a test: kept
// in each channel's log and as its last test.
func testRule(ctx *muzak.Context, s ruleScope, id uuid.UUID) (schemas.NotificationRuleTestOut, error) {
	db := muzak.From[*core.DB](ctx)
	sender := muzak.From[*notify.Sender](ctx)
	dashboard := ""
	if settings, found := muzak.TryFrom[config.Settings](ctx); found {
		dashboard = settings.DashboardURL
	}
	row, err := findRule(ctx, s, id)
	if err != nil {
		return schemas.NotificationRuleTestOut{}, err
	}
	kind, ok := firstKind(s.scope, row.Events)
	if !ok {
		return schemas.NotificationRuleTestOut{}, muzak.UnprocessableEntity("the rule hears no event").
			WithCode("invalid_event")
	}
	channels, err := s.channels(db.Client).Where(notificationchannel.UIDIn(row.Channels...)).All(ctx.Context())
	if err != nil {
		return schemas.NotificationRuleTestOut{}, core.AsHTTPError(err)
	}
	p := muzak.From[*core.Principal](ctx)
	where := alerts.Where{Organization: p.Organization.Name}
	if p.Project != nil {
		where.Project = p.Project.Name
	}
	msg := alerts.Render(kind, alerts.Event{OrganizationID: s.orgID, Type: kind.Type, Data: sampleData(kind),
		At: nowUTC(), UID: uuid.New()}, where, dashboard)
	msg.Test = true
	msg.Source = "Test of rule " + row.Name
	out := schemas.NotificationRuleTestOut{Type: kind.Type, Channels: []schemas.NotificationRuleTestResult{}}
	for _, ch := range channels {
		r := sender.DeliverFor(ctx.Context(), db.Client, ch, msg, "Test of rule "+row.Name+" by "+actorOf(ctx).Label, &row.ID)
		out.Channels = append(out.Channels, schemas.NotificationRuleTestResult{ChannelID: ch.UID, Name: ch.Name,
			OK: r.OK, Detail: r.Detail})
	}
	return out, nil
}

func firstKind(scope alerts.Scope, events []string) (alerts.Kind, bool) {
	for _, t := range events {
		if t == alerts.Every {
			for _, k := range alerts.Catalog {
				if k.Scope == scope {
					return k, true
				}
			}
			continue
		}
		if k, ok := alerts.Lookup(t); ok {
			return k, true
		}
	}
	return alerts.Kind{}, false
}

// ListNotificationKinds is every event a rule can hear, the organization's and the projects'.
func ListNotificationKinds(_ *muzak.Context, _ schemas.OrganizationParams) (schemas.NotificationKindsOut, error) {
	out := schemas.NotificationKindsOut{Kinds: make([]schemas.NotificationKindOut, 0, len(alerts.Catalog))}
	for _, k := range alerts.Catalog {
		out.Kinds = append(out.Kinds, schemas.NotificationKindOut{Type: k.Type, Group: k.Group, Title: k.Title,
			Summary: k.Summary, Scope: string(k.Scope), Environments: k.Environments, Emitted: k.Emitted,
			Severity: k.Severity, Owners: k.Owners})
	}
	return out, nil
}

// ListNotificationRules is a project's rules.
func ListNotificationRules(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.NotificationRuleListOut, error) {
	return listRules(ctx, projectScope(ctx))
}

// CreateNotificationRule makes a rule of a project.
func CreateNotificationRule(ctx *muzak.Context, in schemas.NotificationRuleIn) (schemas.NotificationRuleOut, error) {
	return createRule(ctx, projectScope(ctx), in.NotificationRuleBody)
}

// UpdateNotificationRule changes a rule of a project.
func UpdateNotificationRule(ctx *muzak.Context, in schemas.UpdateNotificationRuleIn) (schemas.NotificationRuleOut, error) {
	return updateRule(ctx, projectScope(ctx), in.RuleID, in.NotificationRuleBody)
}

// DeleteNotificationRule removes a rule of a project.
func DeleteNotificationRule(ctx *muzak.Context, in schemas.NotificationRuleParams) (muzak.Empty, error) {
	return deleteRule(ctx, projectScope(ctx), in.RuleID)
}

// TestNotificationRule sends a sample to a project rule's channels.
func TestNotificationRule(ctx *muzak.Context, in schemas.NotificationRuleParams) (schemas.NotificationRuleTestOut, error) {
	return testRule(ctx, projectScope(ctx), in.RuleID)
}

// ListOrgNotificationRules is the organization's rules.
func ListOrgNotificationRules(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.NotificationRuleListOut, error) {
	return listRules(ctx, orgScope(ctx))
}

// CreateOrgNotificationRule makes a rule of the organization.
func CreateOrgNotificationRule(ctx *muzak.Context, in schemas.OrgNotificationRuleIn) (schemas.NotificationRuleOut, error) {
	return createRule(ctx, orgScope(ctx), in.NotificationRuleBody)
}

// UpdateOrgNotificationRule changes a rule of the organization.
func UpdateOrgNotificationRule(ctx *muzak.Context, in schemas.UpdateOrgNotificationRuleIn) (schemas.NotificationRuleOut, error) {
	return updateRule(ctx, orgScope(ctx), in.RuleID, in.NotificationRuleBody)
}

// DeleteOrgNotificationRule removes a rule of the organization.
func DeleteOrgNotificationRule(ctx *muzak.Context, in schemas.OrgNotificationRuleParams) (muzak.Empty, error) {
	return deleteRule(ctx, orgScope(ctx), in.RuleID)
}

// TestOrgNotificationRule sends a sample to an organization rule's channels.
func TestOrgNotificationRule(ctx *muzak.Context, in schemas.OrgNotificationRuleParams) (schemas.NotificationRuleTestOut, error) {
	return testRule(ctx, orgScope(ctx), in.RuleID)
}

// ListOrgChannels is every channel in the organization's projects, which its rules may notify.
// Names and types only: where each sends is its project's to show.
func ListOrgChannels(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.OrgChannelListOut, error) {
	db := muzak.From[*core.DB](ctx)
	rows, err := orgScope(ctx).channels(db.Client).WithProject().
		Order(ent.Asc(notificationchannel.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.OrgChannelListOut{}, core.AsHTTPError(err)
	}
	out := schemas.OrgChannelListOut{Channels: make([]schemas.OrgChannelOut, 0, len(rows))}
	for _, row := range rows {
		name := ""
		if row.Edges.Project != nil {
			name = row.Edges.Project.Name
		}
		out.Channels = append(out.Channels, schemas.OrgChannelOut{ID: row.UID, Type: string(row.Type),
			Name: row.Name, Enabled: row.Enabled, Project: name})
	}
	return out, nil
}

// ReadChannelActivity is what was sent to the project's channels in a window, beside the window
// before it: the Activity page's figures and charts.
func ReadChannelActivity(ctx *muzak.Context, in schemas.ChannelActivityParams) (schemas.ChannelActivityOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	r, ok := alerts.Ranges[in.Range]
	if !ok {
		r = alerts.Ranges["7d"]
	}
	a, err := alerts.ProjectActivity(ctx.Context(), db.Client, proj.ID, r, nowUTC())
	if err != nil {
		return schemas.ChannelActivityOut{}, core.AsHTTPError(err)
	}
	totals := func(t alerts.Totals) schemas.ActivityTotalsOut {
		return schemas.ActivityTotalsOut{Sent: t.Sent, Delivered: t.Delivered, Failed: t.Failed, Tests: t.Tests,
			P50Ms: t.P50Ms, P95Ms: t.P95Ms}
	}
	out := schemas.ChannelActivityOut{Range: r.Name, Bucket: r.Bucket, Current: totals(a.Current),
		Previous: totals(a.Previous), Waiting: a.Waiting, Retrying: a.Retrying,
		Buckets:  make([]schemas.ActivityBucketOut, 0, len(a.Buckets)),
		Channels: make([]schemas.ChannelActivityRowOut, 0, len(a.Channels)),
		Sources:  make([]schemas.ActivitySourceOut, 0, len(a.Sources))}
	for _, b := range a.Buckets {
		out.Buckets = append(out.Buckets, schemas.ActivityBucketOut{At: b.At, Delivered: b.Delivered, Failed: b.Failed, Tests: b.Tests})
	}
	for _, c := range a.Channels {
		row := schemas.ChannelActivityRowOut{Name: c.Name, Type: c.Type, Sent: c.Sent, Failed: c.Failed,
			P50Ms: c.P50Ms, LastAt: c.LastAt, LastOK: c.LastOK, Spark: c.Spark}
		if c.UID != uuid.Nil {
			id := c.UID
			row.ChannelID = &id
		}
		out.Channels = append(out.Channels, row)
	}
	ruleIDs := map[int]uuid.UUID{}
	var ids []int
	for _, s := range a.Sources {
		if s.RuleID != nil {
			ids = append(ids, *s.RuleID)
		}
	}
	if len(ids) > 0 {
		rows, err := db.Client.NotificationRule.Query().Where(notificationrule.IDIn(ids...)).All(ctx.Context())
		if err != nil {
			return schemas.ChannelActivityOut{}, core.AsHTTPError(err)
		}
		for _, row := range rows {
			ruleIDs[row.ID] = row.UID
		}
	}
	for _, s := range a.Sources {
		src := schemas.ActivitySourceOut{Source: s.Source, Sent: s.Sent, Failed: s.Failed}
		if s.RuleID != nil {
			if uid, found := ruleIDs[*s.RuleID]; found {
				src.RuleID = &uid
			}
		}
		out.Sources = append(out.Sources, src)
	}
	return out, nil
}

// ruleStatDays is how far back a rule's sparkline reaches.
const ruleStatDays = 7

func ruleStats(ctx *muzak.Context, s ruleScope) (schemas.RuleStatsOut, error) {
	db := muzak.From[*core.DB](ctx)
	rules, err := s.query(db.Client).All(ctx.Context())
	if err != nil {
		return schemas.RuleStatsOut{}, core.AsHTTPError(err)
	}
	ids := make([]int, len(rules))
	for i, r := range rules {
		ids[i] = r.ID
	}
	stats, err := alerts.RuleStats(ctx.Context(), db.Client, ids, ruleStatDays, nowUTC())
	if err != nil {
		return schemas.RuleStatsOut{}, core.AsHTTPError(err)
	}
	out := schemas.RuleStatsOut{Days: ruleStatDays, Rules: make([]schemas.RuleStatOut, 0, len(rules))}
	for _, r := range rules {
		st := stats[r.ID]
		out.Rules = append(out.Rules, schemas.RuleStatOut{RuleID: r.UID, Sent: st.Sent, Failed: st.Failed,
			LastAt: st.LastAt, Spark: st.Spark})
	}
	return out, nil
}

// ReadNotificationRuleStats is what each of a project's rules sent in the last seven days.
func ReadNotificationRuleStats(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.RuleStatsOut, error) {
	return ruleStats(ctx, projectScope(ctx))
}

// ReadOrgNotificationRuleStats is what each of the organization's rules sent in the last seven days.
func ReadOrgNotificationRuleStats(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.RuleStatsOut, error) {
	return ruleStats(ctx, orgScope(ctx))
}
