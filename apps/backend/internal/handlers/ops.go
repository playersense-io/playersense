package handlers

import (
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent/organization"
	"playersense.dev/backend/ent/project"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/ops"
	"playersense.dev/backend/internal/schemas"
)

// The operations console's analytics (§56), behind a signed-in staff member.

// operatorRange reads a range, defaulting to the last days days.
func operatorRange(from, to string, days int) (time.Time, time.Time, error) {
	end := time.Now().UTC().Truncate(24 * time.Hour)
	if to != "" {
		t, err := time.Parse(time.DateOnly, to)
		if err != nil {
			return time.Time{}, time.Time{}, muzak.BadRequest("to is not a YYYY-MM-DD date")
		}
		end = t
	}
	start := end.AddDate(0, 0, -(days - 1))
	if from != "" {
		t, err := time.Parse(time.DateOnly, from)
		if err != nil {
			return time.Time{}, time.Time{}, muzak.BadRequest("from is not a YYYY-MM-DD date")
		}
		start = t
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, muzak.BadRequest("to is before from")
	}
	if end.Sub(start) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, muzak.BadRequest("a range covers at most 400 days")
	}
	return start, end, nil
}

// names reads the slugs of the organizations and projects a console answer mentions.
func names(ctx *muzak.Context, orgs, projects []int) (map[int]string, map[int]string) {
	db := muzak.From[*core.DB](ctx)
	orgNames, projectNames := map[int]string{}, map[int]string{}
	if rows, err := db.Client.Organization.Query().Where(organization.IDIn(orgs...)).All(ctx.Context()); err == nil {
		for _, o := range rows {
			orgNames[o.ID] = o.Slug
		}
	}
	if rows, err := db.Client.Project.Query().Where(project.IDIn(projects...)).All(ctx.Context()); err == nil {
		for _, p := range rows {
			projectNames[p.ID] = p.Slug
		}
	}
	// Names are a convenience: a console that cannot read PostgreSQL still shows the numbers.
	return orgNames, projectNames
}

// OperatorIngestion reads every tenant's ingestion.
func OperatorIngestion(ctx *muzak.Context, in schemas.IngestionParams) (schemas.IngestionOut, error) {
	console := muzak.From[*ops.Console](ctx)
	from, to, err := operatorRange(in.From, in.To, 7)
	if err != nil {
		return schemas.IngestionOut{}, err
	}
	if in.Interval == "minute" && to.Sub(from) > 0 {
		return schemas.IngestionOut{}, muzak.BadRequest("a minute series covers one day; name the same day as from and to")
	}
	out, err := console.Ingestion(ctx.Context(), from, to, in.Interval, uint32(in.Project), time.Now())
	if err != nil {
		return schemas.IngestionOut{}, muzak.ServiceUnavailable("the ingestion rollups could not be read").Wrap(err)
	}
	var orgs, projects []int
	for _, p := range out.Projects {
		orgs, projects = append(orgs, int(p.OrgSeq)), append(projects, int(p.ProjectSeq))
	}
	orgNames, projectNames := names(ctx, orgs, projects)
	for i := range out.Projects {
		out.Projects[i].Organization = orgNames[int(out.Projects[i].OrgSeq)]
		out.Projects[i].Project = projectNames[int(out.Projects[i].ProjectSeq)]
	}
	return *out, nil
}

// OperatorClickHouse reads the analytics node's health.
func OperatorClickHouse(ctx *muzak.Context, _ struct{}) (schemas.ClickHouseHealthOut, error) {
	console := muzak.From[*ops.Console](ctx)
	out, err := console.Health(ctx.Context(), time.Now().UTC())
	if err != nil {
		return schemas.ClickHouseHealthOut{}, muzak.ServiceUnavailable("ClickHouse could not be read").Wrap(err)
	}
	return *out, nil
}

// OperatorStorage reads storage by table and by project.
func OperatorStorage(ctx *muzak.Context, in schemas.StorageParams) (schemas.StorageOut, error) {
	console := muzak.From[*ops.Console](ctx)
	from, to, err := operatorRange(in.From, in.To, 30)
	if err != nil {
		return schemas.StorageOut{}, err
	}
	out, err := console.Storage(ctx.Context(), from, to)
	if err != nil {
		return schemas.StorageOut{}, muzak.ServiceUnavailable("storage could not be read").Wrap(err)
	}
	var orgs, projects []int
	for _, p := range out.Projects {
		orgs, projects = append(orgs, int(p.OrgSeq)), append(projects, int(p.ProjectSeq))
	}
	orgNames, projectNames := names(ctx, orgs, projects)
	for i := range out.Projects {
		out.Projects[i].Organization = orgNames[int(out.Projects[i].OrgSeq)]
		out.Projects[i].Project = projectNames[int(out.Projects[i].ProjectSeq)]
	}
	return *out, nil
}
