// Package handlers holds the functions that answer requests.
package handlers

import (
	"context"
	"time"

	"muzak.dev/framework"

	"playersense.dev/backend/ent/global"
	"playersense.dev/backend/ent/global/apikey"
	"playersense.dev/backend/ent/global/cell"
	"playersense.dev/backend/ent/global/environment"
	"playersense.dev/backend/ent/global/project"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/identity"
	"playersense.dev/backend/internal/schemas"
)

// Health is the liveness probe.
func Health(ctx *muzak.Context, _ muzak.Empty) (schemas.HealthOut, error) {
	return schemas.HealthOut{Status: "ok"}, nil
}

// ---- organizations ----------------------------------------------------------

// CreateOrganization registers an organization, owned by whoever asked.
//
// The caller becomes its owner and its six roles are seeded, in one transaction: an
// organization that exists with no owner and no roles is one nobody can administer, and it
// would have to be repaired by hand.
func CreateOrganization(ctx *muzak.Context, in schemas.CreateOrganizationIn) (schemas.OrganizationOut, error) {
	ctrl := muzak.From[*core.Global](ctx)
	principal := muzak.From[*core.Principal](ctx)

	org, err := identity.CreateOrganization(ctx.Context(), ctrl.Client,
		in.Name, in.Slug, principal.User.ID, time.Now())
	if err != nil {
		return schemas.OrganizationOut{}, core.AsHTTPError(err)
	}

	// The log's first line, and the only one written against an organisation the principal was
	// not inside when the request began — so the id comes from what was just created.
	auditFor(ctx).forOrganization(org.ID).after(ctx, ctrl.Client, audit.OrganizationCreated,
		"organization", org.UID.String(), map[string]any{"slug": org.Slug, "name": org.Name})

	return organizationOut(org), nil
}

// ListOrganizations returns the organizations the caller belongs to.
//
// Their own, not everyone's. This used to return every organization in the deployment, which
// was defensible only while the single credential that reached it was an operator's shared
// admin token — the moment a customer holds a credential, the same query is a list of every
// other customer.
func ListOrganizations(ctx *muzak.Context, _ muzak.Empty) (schemas.OrganizationListOut, error) {
	ctrl := muzak.From[*core.Global](ctx)
	principal := muzak.From[*core.Principal](ctx)

	orgs, err := identity.Organizations(ctx.Context(), ctrl.Client, principal.User.ID)
	if err != nil {
		return schemas.OrganizationListOut{}, core.AsHTTPError(err)
	}
	out := schemas.OrganizationListOut{Organizations: make([]schemas.OrganizationOut, 0, len(orgs))}
	for _, org := range orgs {
		out.Organizations = append(out.Organizations, organizationOut(org))
	}
	return out, nil
}

// ReadOrganization returns the organization named in the path.
//
// Resolved by the principal rather than looked up again here. Not only to save a query: the
// object the permission check ran against has to be the object the handler answers with, and
// two independent lookups of one slug are two chances for those to differ.
func ReadOrganization(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.OrganizationOut, error) {
	principal := muzak.From[*core.Principal](ctx)
	return organizationOut(principal.Organization), nil
}

// ---- projects ---------------------------------------------------------------

// CreateProject registers a project and its three environments.
//
// The environments are created here rather than on demand because §76 requires every
// event to carry an environment resolved from its key, and a project with no environments
// is a project nothing can send to.
func CreateProject(ctx *muzak.Context, in schemas.CreateProjectIn) (schemas.ProjectOut, error) {
	ctrl := muzak.From[*core.Global](ctx)

	org := muzak.From[*core.Principal](ctx).Organization

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
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.ProjectCreated,
		"project", proj.UID.String(), map[string]any{
			"slug": proj.Slug, "name": proj.Name, "cell": home.Slug,
			"policy_tier": string(proj.PolicyTier), "retention_days": proj.RetentionDays,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.ProjectOut{}, core.AsHTTPError(err)
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

// ListProjects returns the organization's projects this member can see.
//
// Filtered by the access list, which is the second axis: the role says what somebody may do
// and this says where. A studio that hid two of its five games from a contractor has to see
// that reflected in the list, not only in what happens when the contractor opens one.
func ListProjects(ctx *muzak.Context, _ schemas.OrganizationParams) (schemas.ProjectListOut, error) {
	ctrl := muzak.From[*core.Global](ctx)
	principal := muzak.From[*core.Principal](ctx)

	projects, err := ctrl.Client.Project.Query().
		Where(project.OrganizationID(principal.Organization.ID)).
		WithCell().
		Order(global.Asc(project.FieldSlug)).
		All(ctx.Context())
	if err != nil {
		return schemas.ProjectListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ProjectListOut{Projects: make([]schemas.ProjectOut, 0, len(projects))}
	for _, proj := range projects {
		if !principal.Actor.Reaches(proj.ID) {
			continue
		}
		out.Projects = append(out.Projects, projectOut(proj, proj.Edges.Cell, nil))
	}
	return out, nil
}

// ReadProject returns one project with its environments.
//
// The project comes from the principal, which already refused it as "no such project" if this
// member cannot reach it.
func ReadProject(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ProjectOut, error) {
	ctrl := muzak.From[*core.Global](ctx)
	principal := muzak.From[*core.Principal](ctx)
	proj := principal.Project

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

	proj := muzak.From[*core.Principal](ctx).Project
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

	tx, err := ctrl.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.APIKeyCreatedOut{}, core.AsHTTPError(err)
	}
	key, err := tx.APIKey.Create().
		SetKind(apikey.Kind(in.Kind)).
		SetPublicID(generated.PublicID).
		SetSecretHash(generated.Hash).
		SetName(in.Name).
		SetEnvironmentID(env.ID).
		Save(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		return schemas.APIKeyCreatedOut{}, core.AsHTTPError(err)
	}
	// The public half only. The secret is shown once to the caller and never stored, so an audit
	// entry carrying it would be the one place in the product it could be read twice.
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.KeyCreated,
		"api_key", key.UID.String(), map[string]any{
			"kind": in.Kind, "environment": string(env.Kind),
			"name": in.Name, "public_id": key.PublicID,
			"project": proj.Slug,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.APIKeyCreatedOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
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

	proj := muzak.From[*core.Principal](ctx).Project
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

	proj := muzak.From[*core.Principal](ctx).Project
	key, err := ctrl.Client.APIKey.Query().
		Where(apikey.UID(in.KeyID), apikey.HasEnvironmentWith(environment.ProjectID(proj.ID))).
		Only(ctx.Context())
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	if key.RevokedAt != nil {
		return schemas.APIKeyOut{}, muzak.Conflict("that key is already revoked")
	}
	tx, err := ctrl.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	updated, err := tx.APIKey.UpdateOneID(key.ID).SetRevokedAt(nowUTC()).Save(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.KeyRevoked,
		"api_key", key.UID.String(), map[string]any{
			"kind": string(key.Kind), "name": key.Name, "public_id": key.PublicID,
			"project": proj.Slug,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
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
	// Through the application's client, not the row's. `updated` was returned by the transaction
	// above, so its client is that transaction — and querying an edge off it after the commit is
	// a use of a closed connection, which is what made revoking a key a 500.
	env, err := ctrl.Client.Environment.Get(ctx.Context(), updated.EnvironmentID)
	if err != nil {
		return schemas.APIKeyOut{}, core.AsHTTPError(err)
	}
	return apiKeyOut(updated, string(env.Kind)), nil
}

// ---- lookups and mapping ----------------------------------------------------

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
