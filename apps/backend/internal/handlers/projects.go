package handlers

import (
	"strconv"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/billingaccount"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/billing"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
)

// A project's settings: its name, its raw retention, its deletion, and where its events come from.

// DeletionGrace is how long a deletion waits: it cannot be undone, and the person who asked is
// not always the person who would have said no.
const DeletionGrace = 30 * 24 * time.Hour

// policyRetentionCap is the longest raw retention a policy tier allows (§98): child-directed data
// is kept for less than the plan's normal maximum, and a kids_strict project, which cannot
// recognise a player across days at all, for less still.
func policyRetentionCap(tier string) int {
	switch tier {
	case "kids_strict":
		return 30
	case "child_directed":
		return 90
	default:
		return 365
	}
}

// retentionCap is the lower of the policy tier's cap and the plan's max_raw_retention_days.
func retentionCap(ctx *muzak.Context, proj *ent.Project) (int, error) {
	ceiling := policyRetentionCap(string(proj.PolicyTier))
	db := muzak.From[*core.DB](ctx)
	account, err := db.Client.BillingAccount.Query().
		Where(billingaccount.OrganizationID(proj.OrganizationID)).WithPlan().Only(ctx.Context())
	switch {
	case ent.IsNotFound(err):
		return ceiling, nil
	case err != nil:
		return 0, core.AsHTTPError(err)
	}
	if plan, found := resolvedEntitlements(account)[string(billing.MaxRawRetentionDays)]; found && int(plan) < ceiling {
		ceiling = int(plan)
	}
	return ceiling, nil
}

func projectSettingsOut(ctx *muzak.Context, proj *ent.Project) (schemas.ProjectSettingsOut, error) {
	db := muzak.From[*core.DB](ctx)
	envs, err := db.Client.Environment.Query().Where(environment.ProjectID(proj.ID)).
		Order(ent.Asc(environment.FieldKind)).All(ctx.Context())
	if err != nil {
		return schemas.ProjectSettingsOut{}, core.AsHTTPError(err)
	}
	ceiling, err := retentionCap(ctx, proj)
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	return schemas.ProjectSettingsOut{
		ProjectOut: projectOut(proj, envs), RetentionCap: ceiling,
		RetentionChangedAt: proj.RetentionChangedAt, RetentionPurgeUntil: proj.RetentionPurgeUntil,
		DeletionAt: proj.DeletionAt, DeletionRequestedBy: proj.DeletionRequestedBy,
	}, nil
}

// ReadProject returns one project as its settings page shows it.
//
// The project comes from the principal, which already refused it as "no such project" if this
// member cannot reach it.
func ReadProject(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ProjectSettingsOut, error) {
	return projectSettingsOut(ctx, muzak.From[*core.Principal](ctx).Project)
}

// UpdateProject renames a project. The slug stays, so links and exports that carry it keep
// working.
func UpdateProject(ctx *muzak.Context, in schemas.UpdateProjectIn) (schemas.ProjectSettingsOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Project, error) {
		updated, err := tx.Project.UpdateOneID(proj.ID).SetName(in.Name).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.ProjectUpdated, "project", proj.Slug,
			map[string]any{"name": in.Name, "was_name": proj.Name})
	})
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	return projectSettingsOut(ctx, updated)
}

// SetRetention changes a project's raw retention tier.
//
// New rows take the tier as the key cache turns over, in minutes. A raised tier does not bring
// back what already expired. A lowered one is enforced on rows written before it too: the worker
// deletes raw rows older than the new tier a pass a day until no row stamped with the old tier
// can remain, which is the old tier's length from now.
func SetRetention(ctx *muzak.Context, in schemas.SetRetentionIn) (schemas.ProjectSettingsOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	ceiling, err := retentionCap(ctx, proj)
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	if int(in.RetentionDays) > ceiling {
		return schemas.ProjectSettingsOut{}, muzak.UnprocessableEntity(
			"this project can keep raw events for at most " + strconv.Itoa(ceiling) + " days: its policy tier's " +
				"and its plan's limits, whichever is lower").WithCode("over_retention_cap")
	}
	if in.RetentionDays == proj.RetentionDays {
		return projectSettingsOut(ctx, proj)
	}
	now := nowUTC()
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Project, error) {
		update := tx.Project.UpdateOneID(proj.ID).SetRetentionDays(in.RetentionDays).SetRetentionChangedAt(now)
		if in.RetentionDays < proj.RetentionDays {
			update = update.SetRetentionPurgeUntil(now.AddDate(0, 0, int(proj.RetentionDays))).ClearRetentionPurgedAt()
		} else {
			update = update.ClearRetentionPurgeUntil()
		}
		updated, err := update.Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.RetentionSet, "project", proj.Slug,
			map[string]any{"retention_days": in.RetentionDays, "was_retention_days": proj.RetentionDays})
	})
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	return projectSettingsOut(ctx, updated)
}

// ScheduleDeletion asks for a project to be deleted in thirty days. Events are collected until
// then, and it can be kept at any point before.
func ScheduleDeletion(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ProjectSettingsOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	if proj.DeletionAt != nil {
		return schemas.ProjectSettingsOut{}, muzak.Conflict("this project is already to be deleted on " +
			proj.DeletionAt.Format(time.DateOnly)).WithCode("already_scheduled")
	}
	at := nowUTC().Add(DeletionGrace)
	who := actorOf(ctx).Label
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Project, error) {
		updated, err := tx.Project.UpdateOneID(proj.ID).SetDeletionAt(at).SetDeletionRequestedBy(who).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.ProjectDeletionScheduled, "project", proj.Slug,
			map[string]any{"deletion_at": at})
	})
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	return projectSettingsOut(ctx, updated)
}

// CancelDeletion keeps a project that was to be deleted.
func CancelDeletion(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ProjectSettingsOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	if proj.DeletionAt == nil {
		return schemas.ProjectSettingsOut{}, muzak.Conflict("this project is not to be deleted").WithCode("not_scheduled")
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Project, error) {
		updated, err := tx.Project.UpdateOneID(proj.ID).ClearDeletionAt().SetDeletionRequestedBy("").Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.ProjectDeletionCancelled, "project", proj.Slug,
			map[string]any{"was_deletion_at": proj.DeletionAt})
	})
	if err != nil {
		return schemas.ProjectSettingsOut{}, err
	}
	return projectSettingsOut(ctx, updated)
}

// ListProjectApps is where a project's events come from: each platform and SDK, when first and
// last seen, and the newest build.
func ListProjectApps(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.ProjectAppsOut, error) {
	engine := muzak.From[*query.Engine](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	apps, err := engine.Apps(ctx.Context(), uint32(proj.ID))
	if err != nil {
		return schemas.ProjectAppsOut{}, exploreFailed(err, "apps")
	}
	return schemas.ProjectAppsOut{Apps: apps}, nil
}
