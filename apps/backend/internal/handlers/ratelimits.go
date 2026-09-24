package handlers

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/valkey"
)

// OperatorRateLimits is the limits on the SDK paths and the invitations, who has been refused and
// how often, and where one project's project-wide budgets stand now (docs/rate-limits.md). There is
// nothing to change here: §9 of that document is why there are no overrides.
func OperatorRateLimits(ctx *muzak.Context, in schemas.RateLimitsParams) (schemas.RateLimitsOut, error) {
	storage := muzak.From[*valkey.RateLimitStorage](ctx)
	db := muzak.From[*core.DB](ctx)
	now := time.Now().UTC()
	from := now.Add(-time.Duration(in.Hours) * time.Hour)
	out := schemas.RateLimitsOut{From: from, To: now, Totals: map[string]int64{},
		Series: []schemas.RefusalHourOut{}, Top: []schemas.RefusalScopeOut{}}

	limits := map[string]int{}
	for _, b := range core.Budgets() {
		limits[b.Quota] = b.Limit
		out.Budgets = append(out.Budgets, schemas.BudgetOut{
			Path: b.Path, Quota: b.Quota, Per: b.Per, WindowSeconds: int64(b.Window.Seconds()),
			Limit: b.Limit, FailOpen: b.FailOpen,
		})
	}

	refusals, err := storage.Refusals(ctx.Context(), from, now)
	if err != nil {
		// The page still lists the limits; that nothing could be counted is itself the finding.
		ctx.Logger().Warn("could not read rate-limit refusals", "error", err)
		return out, nil
	}
	out.CountedOK = true

	type hourQuota struct {
		hour  time.Time
		quota string
	}
	type quotaScope struct{ quota, scope string }
	byHour := map[hourQuota]int64{}
	byScope := map[quotaScope]int64{}
	for _, r := range refusals {
		out.Totals[r.Quota] += r.Count
		byHour[hourQuota{r.Hour, r.Quota}] += r.Count
		byScope[quotaScope{r.Quota, r.Scope}] += r.Count
	}
	for k, n := range byHour {
		out.Series = append(out.Series, schemas.RefusalHourOut{Hour: k.hour, Quota: k.quota, Count: n})
	}
	slices.SortFunc(out.Series, func(a, b schemas.RefusalHourOut) int {
		return cmp.Or(a.Hour.Compare(b.Hour), cmp.Compare(a.Quota, b.Quota))
	})
	for k, n := range byScope {
		out.Top = append(out.Top, schemas.RefusalScopeOut{Quota: k.quota, Scope: k.scope, Count: n})
	}
	slices.SortFunc(out.Top, func(a, b schemas.RefusalScopeOut) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Scope, b.Scope))
	})
	if len(out.Top) > 50 {
		out.Top = out.Top[:50]
	}

	// Names, for a person reading it. A scope is a project's id on the SDK paths and an
	// organization's slug on invitations.
	var uids []uuid.UUID
	for _, t := range out.Top {
		if id, err := uuid.Parse(t.Scope); err == nil {
			uids = append(uids, id)
		}
	}
	if len(uids) > 0 {
		if rows, err := db.Client.Project.Query().Where(project.UIDIn(uids...)).WithOrganization().
			All(ctx.Context()); err == nil {
			named := map[string][2]string{}
			for _, p := range rows {
				org := ""
				if p.Edges.Organization != nil {
					org = p.Edges.Organization.Slug
				}
				named[p.UID.String()] = [2]string{p.Slug, org}
			}
			for i := range out.Top {
				if n, found := named[out.Top[i].Scope]; found {
					out.Top[i].Project, out.Top[i].Organization = n[0], n[1]
				}
			}
		}
	}
	var slugs []string
	for _, t := range out.Top {
		if t.Project == "" {
			slugs = append(slugs, t.Scope)
		}
	}
	if len(slugs) > 0 {
		if found, err := db.Client.Organization.Query().Where(organization.SlugIn(slugs...)).
			Select(organization.FieldSlug).Strings(ctx.Context()); err == nil {
			for i := range out.Top {
				if out.Top[i].Project == "" && slices.Contains(found, out.Top[i].Scope) {
					out.Top[i].Organization = out.Top[i].Scope
				}
			}
		}
	}

	if in.Project != "" {
		id, err := uuid.Parse(in.Project)
		if err != nil {
			return out, muzak.UnprocessableEntity("project is not a project id")
		}
		live := &schemas.ProjectCountersOut{ProjectID: id.String()}
		for _, quota := range []string{"project-sustained", "project-config-sustained"} {
			count, reset, err := storage.Peek(ctx.Context(), quota, id.String())
			if err != nil {
				return out, muzak.ServiceUnavailable("the counters could not be read").Wrap(err)
			}
			live.Counters = append(live.Counters, schemas.LiveCounterOut{
				Quota: quota, Count: count, Limit: limits[quota], ResetMs: reset.Milliseconds(),
			})
		}
		out.Project = live
	}
	return out, nil
}
