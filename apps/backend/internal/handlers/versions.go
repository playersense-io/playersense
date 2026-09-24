package handlers

import (
	"context"
	"encoding/json"
	"strconv"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/configversion"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/schemas"
)

// An environment's published history, and going back in it (§41).

// environmentOf is one of a project's environments by kind.
func environmentOf(ctx context.Context, db *core.DB, projectID int, kind string) (*ent.Environment, error) {
	env, err := db.Client.Environment.Query().
		Where(environment.ProjectID(projectID), environment.KindEQ(environment.Kind(kind))).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no " + kind + " environment")
		}
		return nil, core.AsHTTPError(err)
	}
	return env, nil
}

// versionOut summarises a published version from its row, counting what its document carries.
func versionOut(row *ent.ConfigVersion, kind string, current int) schemas.ConfigVersionOut {
	out := schemas.ConfigVersionOut{
		Version: row.Version, Environment: kind, PublishedBy: row.PublishedByLabel,
		PublishedAt: row.CreatedAt, Note: row.Note, Current: row.Version == current,
	}
	if flags, ok := row.Document["flags"].([]any); ok {
		out.Flags = len(flags)
	}
	if events, ok := row.Document["liveops_events"].([]any); ok {
		out.Events = len(events)
	}
	out.NeedsPlayerFacts, _ = row.Document["needs_player_facts"].(bool)
	if row.RollbackOf != nil {
		out.RollbackOf = *row.RollbackOf
	}
	return out
}

// currentVersion is the version an environment serves: its newest. Zero before the first publish.
func currentVersion(ctx context.Context, db *core.DB, envID int) (int, error) {
	next, err := nextVersion(ctx, db, envID)
	return next - 1, err
}

// ListConfigVersions is an environment's history, newest first.
func ListConfigVersions(ctx *muzak.Context, in schemas.ConfigVersionsParams) (schemas.ConfigVersionListOut, error) {
	db := muzak.From[*core.DB](ctx)
	project := muzak.From[*core.Principal](ctx).Project
	env, err := environmentOf(ctx.Context(), db, project.ID, in.Environment)
	if err != nil {
		return schemas.ConfigVersionListOut{}, err
	}
	current, err := currentVersion(ctx.Context(), db, env.ID)
	if err != nil {
		return schemas.ConfigVersionListOut{}, core.AsHTTPError(err)
	}
	q := db.Client.ConfigVersion.Query().Where(configversion.EnvironmentID(env.ID))
	if in.Before > 0 {
		q = q.Where(configversion.VersionLT(in.Before))
	}
	rows, err := q.Order(ent.Desc(configversion.FieldVersion)).Limit(in.Limit + 1).All(ctx.Context())
	if err != nil {
		return schemas.ConfigVersionListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ConfigVersionListOut{Current: current, Versions: make([]schemas.ConfigVersionOut, 0, len(rows))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		out.Next = rows[len(rows)-1].Version
	}
	for _, row := range rows {
		out.Versions = append(out.Versions, versionOut(row, in.Environment, current))
	}
	return out, nil
}

// ReadConfigVersion is one version with the document it served.
func ReadConfigVersion(ctx *muzak.Context, in schemas.ConfigVersionParams) (schemas.ConfigVersionDocumentOut, error) {
	db := muzak.From[*core.DB](ctx)
	project := muzak.From[*core.Principal](ctx).Project
	env, err := environmentOf(ctx.Context(), db, project.ID, in.Environment)
	if err != nil {
		return schemas.ConfigVersionDocumentOut{}, err
	}
	row, err := versionRow(ctx.Context(), db, env.ID, in.Version)
	if err != nil {
		return schemas.ConfigVersionDocumentOut{}, err
	}
	current, err := currentVersion(ctx.Context(), db, env.ID)
	if err != nil {
		return schemas.ConfigVersionDocumentOut{}, core.AsHTTPError(err)
	}
	return schemas.ConfigVersionDocumentOut{
		ConfigVersionOut: versionOut(row, in.Environment, current), Document: row.Document,
	}, nil
}

func versionRow(ctx context.Context, db *core.DB, envID, version int) (*ent.ConfigVersion, error) {
	row, err := db.Client.ConfigVersion.Query().
		Where(configversion.EnvironmentID(envID), configversion.Version(version)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this environment has no version " + strconv.Itoa(version))
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// RollbackConfig puts an environment back on an earlier version: that version's document,
// unchanged but for its number, published again as the environment's next version, with why in
// its note. Nothing is deleted and the draft is left alone, so what was rolled back is still there
// to fix and publish again. Needs what a publish needs, since it is one.
func RollbackConfig(ctx *muzak.Context, in schemas.RollbackConfigIn) (schemas.ConfigVersionOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	project := principal.Project
	env, err := environmentOf(ctx.Context(), db, project.ID, in.Environment)
	if err != nil {
		return schemas.ConfigVersionOut{}, err
	}
	target, err := versionRow(ctx.Context(), db, env.ID, in.Version)
	if err != nil {
		return schemas.ConfigVersionOut{}, err
	}
	next, err := nextVersion(ctx.Context(), db, env.ID)
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	if target.Version == next-1 {
		return schemas.ConfigVersionOut{}, muzak.Conflict(
			in.Environment + " already serves version " + strconv.Itoa(target.Version)).WithCode("already_current")
	}

	// Through the document's own type, so the only thing that changes is the number the SDK
	// sends back as its ETag.
	encoded, err := json.Marshal(target.Document)
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	var document liveconfig.Document
	if err := json.Unmarshal(encoded, &document); err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	document.Version = next
	if encoded, err = json.Marshal(document); err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	note := "Rolled back to v" + strconv.Itoa(target.Version) + ": " + in.Reason
	if len(note) > 500 {
		note = note[:500]
	}
	created, err := inTx(ctx, func(tx *ent.Tx) (*ent.ConfigVersion, error) {
		create := tx.ConfigVersion.Create().
			SetProjectID(project.ID).SetEnvironmentID(env.ID).SetVersion(next).SetDocument(stored).
			SetNote(note).SetRollbackOf(target.Version)
		if principal.User != nil {
			create = create.SetPublishedByUserID(principal.User.ID).SetPublishedByLabel(principal.User.Email)
		}
		created, err := create.Save(ctx.Context())
		if err != nil {
			return nil, err
		}
		return created, auditFor(ctx).record(ctx.Context(), tx, audit.ConfigRolledBack,
			"config_version", created.UID.String(), map[string]any{
				"environment": in.Environment, "version": created.Version, "rollback_of": target.Version,
				"reason": in.Reason,
			})
	})
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	if store, found := muzak.TryFrom[*liveconfig.Store](ctx); found {
		store.Invalidate(env.ID)
	}
	ctx.SetStatus(201)
	return versionOut(created, in.Environment, created.Version), nil
}
