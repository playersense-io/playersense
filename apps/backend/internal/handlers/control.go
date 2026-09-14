// Package handlers holds the functions that answer requests.
package handlers

import (
	"context"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/apikey"
	"playersense.dev/backend/ent/global/cell"
	"playersense.dev/backend/ent/global/environment"
	"playersense.dev/backend/ent/global/organization"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// Health is the liveness probe.
func Health(ctx *muzak.Context, _ muzak.Empty) (schemas.HealthOut, error) {
	return schemas.HealthOut{Status: "ok"}, nil
}

// ---- organizations ----------------------------------------------------------

// CreateOrganization registers an organization.
func CreateOrganization(ctx *muzak.Context, in schemas.CreateOrganizationIn) (schemas.OrganizationOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	org, err := ctrl.Client.Organization.Create().
		SetName(in.Name).
		SetSlug(in.Slug).
		Save(ctx.Context())
	if err != nil {
		return schemas.OrganizationOut{}, core.AsHTTPError(err)
	}
	return organizationOut(org), nil
}

// ListOrganizations returns every organization.
func ListOrganizations(ctx *muzak.Context, _ muzak.Empty) (schemas.OrganizationListOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	orgs, err := ctrl.Client.Organization.Query().
		Order(global.Asc(organization.FieldSlug)).
		All(ctx.Context())
	if err != nil {
		return schemas.OrganizationListOut{}, core.AsHTTPError(err)
	}
	out := schemas.OrganizationListOut{Organizations: make([]schemas.OrganizationOut, 0, len(orgs))}
	for _, org := range orgs {
		out.Organizations = append(out.Organizations, organizationOut(org))
	}
	return out, nil
}

// ReadOrganization returns one organization by slug.
func ReadOrganization(ctx *muzak.Context, in schemas.OrganizationParams) (schemas.OrganizationOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	org, err := findOrg(ctx.Context(), ctrl, in.Slug)
	if err != nil {
		return schemas.OrganizationOut{}, err
	}
	return organizationOut(org), nil
}

// ---- projects ---------------------------------------------------------------

// CreateProject registers a project and its three environments.
//
// The environments are created here rather than on demand because §76 requires every
// event to carry an environment resolved from its key, and a project with no environments
// is a project nothing can send to.
func CreateProject(ctx *muzak.Context, in schemas.CreateProjectIn) (schemas.ProjectOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	org, err := findOrg(ctx.Context(), ctrl, in.OrgSlug)
	if err != nil {
		return schemas.ProjectOut{}, err
	}

	// A project is placed in a cell at creation and never moves: the data goes where the
	// cell is, and moving it later crosses a jurisdiction boundary (§6).
	home, err := findCell(ctx.Context(), ctrl, orDefault(in.Cell, defaultCell))
	if err != nil {
		return schemas.ProjectOut{}, err
	}

	tx, err := ctrl.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.ProjectOut{}, core.AsHTTPError(err)
	}

	proj, err := tx.Project.Create().
		SetName(in.Name).
		SetSlug(in.Slug).
		SetCellID(home.ID).
		SetPolicyTier(project.PolicyTier(orDefault(in.PolicyTier, "standard"))).
		SetRetentionDays(retentionOrDefault(in.RetentionDays)).
		SetOrganizationID(org.ID).
		Save(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		return schemas.ProjectOut{}, core.AsHTTPError(err)
	}

	kinds := []environment.Kind{
		environment.KindDevelopment, environment.KindStaging, environment.KindProduction,
	}
	for _, kind := range kinds {
		if _, err := tx.Environment.Create().
			SetKind(kind).
			SetProjectID(proj.ID).
			Save(ctx.Context()); err != nil {
			_ = tx.Rollback()
			return schemas.ProjectOut{}, core.AsHTTPError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return schemas.ProjectOut{}, core.AsHTTPError(err)
	}

	envs, err := ctrl.Client.Environment.Query().
		Where(environment.ProjectID(proj.ID)).
		Order(global.Asc(environment.FieldKind)).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectOut{}, core.AsHTTPError(err)
	}
	return projectOut(proj, home, envs), nil
}

// ListProjects returns an organization's projects.
func ListProjects(ctx *muzak.Context, in schemas.OrganizationParams) (schemas.ProjectListOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	org, err := findOrg(ctx.Context(), ctrl, in.Slug)
	if err != nil {
		return schemas.ProjectListOut{}, err
	}
	projects, err := ctrl.Client.Project.Query().
		Where(project.OrganizationID(org.ID)).
		WithCell().
		Order(global.Asc(project.FieldSlug)).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ProjectListOut{Projects: make([]schemas.ProjectOut, 0, len(projects))}
	for _, proj := range projects {
		out.Projects = append(out.Projects, projectOut(proj, proj.Edges.Cell, nil))
	}
	return out, nil
}

// ReadProject returns one project with its environments.
func ReadProject(ctx *muzak.Context, in schemas.ProjectParams) (schemas.ProjectOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	proj, err := findProject(ctx.Context(), ctrl, in.OrgSlug, in.ProjectSlug)
	if err != nil {
		return schemas.ProjectOut{}, err
	}
	envs, err := ctrl.Client.Environment.Query().
		Where(environment.ProjectID(proj.ID)).
		Order(global.Asc(environment.FieldKind)).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectOut{}, core.AsHTTPError(err)
	}
	return projectOut(proj, proj.Edges.Cell, envs), nil
}

// ---- API keys ---------------------------------------------------------------

// CreateAPIKey mints a key and returns it once.
func CreateAPIKey(ctx *muzak.Context, in schemas.CreateAPIKeyIn) (schemas.APIKeyCreatedOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	proj, err := findProject(ctx.Context(), ctrl, in.OrgSlug, in.ProjectSlug)
	if err != nil {
		return schemas.APIKeyCreatedOut{}, err
	}
	env, err := ctrl.Client.Environment.Query().
		Where(environment.ProjectID(proj.ID), environment.KindEQ(environment.Kind(in.Environment))).
		Only(ctx.Context())
	if err != nil {
		return schemas.APIKeyCreatedOut{}, core.AsHTTPError(err)
	}

	generated, err := core.NewAPIKey(in.Kind, in.Environment)
	if err != nil {
		return schemas.APIKeyCreatedOut{}, muzak.BadRequest(err.Error())
	}

	key, err := ctrl.Client.APIKey.Create().
		SetKind(apikey.Kind(in.Kind)).
		SetPublicID(generated.PublicID).
		SetSecretHash(generated.Hash).
		SetName(in.Name).
		SetEnvironmentID(env.ID).
		Save(ctx.Context())
	if err != nil {
		return schemas.APIKeyCreatedOut{}, core.AsHTTPError(err)
	}

	ctx.SetStatus(201)
	return schemas.APIKeyCreatedOut{
		APIKeyOut: apiKeyOut(key, string(env.Kind)),
		// The one and only time the private half leaves this process. What was stored is
		// an Argon2id digest, so there is no path by which it could be shown again.
		Secret: generated.Secret,
	}, nil
}

// ListAPIKeys returns a project's keys, without their secrets.
func ListAPIKeys(ctx *muzak.Context, in schemas.ProjectParams) (schemas.APIKeyListOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	proj, err := findProject(ctx.Context(), ctrl, in.OrgSlug, in.ProjectSlug)
	if err != nil {
		return schemas.APIKeyListOut{}, err
	}
	envs, err := ctrl.Client.Environment.Query().
		Where(environment.ProjectID(proj.ID)).All(ctx.Context())
	if err != nil {
		return schemas.APIKeyListOut{}, core.AsHTTPError(err)
	}
	byID := map[int]string{}
	ids := make([]int, 0, len(envs))
	for _, env := range envs {
		byID[env.ID] = string(env.Kind)
		ids = append(ids, env.ID)
	}

	keys, err := ctrl.Client.APIKey.Query().
		Where(apikey.EnvironmentIDIn(ids...)).
		Order(global.Asc(apikey.FieldID)).
		All(ctx.Context())
	if err != nil {
		return schemas.APIKeyListOut{}, core.AsHTTPError(err)
	}
	out := schemas.APIKeyListOut{Keys: make([]schemas.APIKeyOut, 0, len(keys))}
	for _, key := range keys {
		out.Keys = append(out.Keys, apiKeyOut(key, byID[key.EnvironmentID]))
	}
	return out, nil
}

// RevokeAPIKey marks a key unusable.
//
// The row is kept rather than deleted: §53 records revoked_at, and an audit that cannot
// say a key once existed is not an audit.
func RevokeAPIKey(ctx *muzak.Context, in schemas.RevokeAPIKeyIn) (schemas.APIKeyOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	proj, err := findProject(ctx.Context(), ctrl, in.OrgSlug, in.ProjectSlug)
	if err != nil {
		return schemas.APIKeyOut{}, err
	}
	key, err := ctrl.Client.APIKey.Query().
		Where(apikey.UID(in.KeyID), apikey.HasEnvironmentWith(environment.ProjectID(proj.ID))).
		Only(ctx.Context())
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	if key.RevokedAt != nil {
		return schemas.APIKeyOut{}, muzak.Conflict("that key is already revoked")
	}
	updated, err := key.Update().SetRevokedAt(nowUTC()).Save(ctx.Context())
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}

	// Revocation has to reach the cache, or a leaked key keeps working until its entry
	// happens to expire — which is exactly the minutes during which somebody needs it to
	// stop. The cache is keyed by a digest of the whole credential, which this service
	// never saw, so a tombstone is written under the public half instead and every
	// resolver refuses anything matching it.
	if resolver, ok := muzak.TryFrom[*core.KeyResolver](ctx); ok {
		if err := resolver.EvictPublicID(ctx.Context(), updated.PublicID, core.RevocationTombstoneTTL); err != nil {
			// Valkey being unavailable must not fail the revocation: the row is already
			// written, and the key stops working when the cache entry expires. Log it and
			// move on rather than leaving the caller thinking nothing happened.
			ctx.Logger().Warn("could not evict a revoked key from the cache",
				"public_id", updated.PublicID, "error", err.Error())
		}
	}
	env, err := updated.QueryEnvironment().Only(ctx.Context())
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	return apiKeyOut(updated, string(env.Kind)), nil
}

// ---- lookups and mapping ----------------------------------------------------

func findOrg(ctx context.Context, ctrl *core.Global, slug string) (*global.Organization, error) {
	org, err := ctrl.Client.Organization.Query().
		Where(organization.Slug(slug)).Only(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return org, nil
}

func findProject(ctx context.Context, ctrl *core.Global, orgSlug, projectSlug string) (*global.Project, error) {
	org, err := findOrg(ctx, ctrl, orgSlug)
	if err != nil {
		return nil, err
	}
	proj, err := ctrl.Client.Project.Query().
		Where(project.OrganizationID(org.ID), project.Slug(projectSlug)).
		WithCell().Only(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return proj, nil
}

// defaultCell is where a project goes when the caller names none.
//
// Stage 1 runs one cell (§6), so this is every project. It is a constant rather than a
// setting because a typo in a setting would place projects in a cell that does not exist,
// and findCell would then refuse every creation until somebody noticed.
const defaultCell = "eu-1"

// findCell resolves a cell by slug and insists it can accept new projects.
//
// A draining or readonly cell still serves the projects it holds, but placing a new one
// there is how a cell that is being emptied quietly refills.
func findCell(ctx context.Context, ctrl *core.Global, slug string) (*global.Cell, error) {
	home, err := ctrl.Client.Cell.Query().Where(cell.Slug(slug)).Only(ctx)
	if err != nil {
		if global.IsNotFound(err) {
			return nil, muzak.BadRequest("no cell named " + slug).WithCode("unknown_cell")
		}
		return nil, core.AsHTTPError(err)
	}
	if home.Status != cell.StatusActive {
		return nil, muzak.Conflict("cell " + slug + " is not accepting new projects").
			WithCode("cell_not_active")
	}
	return home, nil
}
