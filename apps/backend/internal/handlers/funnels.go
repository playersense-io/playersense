package handlers

import (
	"encoding/json"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/savedfunnel"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// Saved funnels (§31): a funnel's definition kept by name, for the team to ask again.

func definitionOf(in schemas.FunnelDefinition) (map[string]any, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(encoded, &out)
}

func savedFunnelOut(row *ent.SavedFunnel) (schemas.SavedFunnelOut, error) {
	out := schemas.SavedFunnelOut{
		ID: row.UID, Name: row.Name, Description: row.Description, CreatedBy: row.CreatedBy,
		UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	encoded, err := json.Marshal(row.Definition)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(encoded, &out.FunnelDefinition)
}

func funnelConflict(err error, name string) error {
	if ent.IsConstraintError(err) {
		return muzak.Conflict("this project already has a funnel called " + name).WithCode("name_taken")
	}
	return core.AsHTTPError(err)
}

// ListSavedFunnels is a project's saved funnels, by name.
func ListSavedFunnels(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.SavedFunnelListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.SavedFunnel.Query().Where(savedfunnel.ProjectID(proj.ID)).
		Order(ent.Asc(savedfunnel.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.SavedFunnelListOut{}, core.AsHTTPError(err)
	}
	out := schemas.SavedFunnelListOut{Funnels: make([]schemas.SavedFunnelOut, 0, len(rows))}
	for _, row := range rows {
		one, err := savedFunnelOut(row)
		if err != nil {
			return out, core.AsHTTPError(err)
		}
		out.Funnels = append(out.Funnels, one)
	}
	return out, nil
}

func findSavedFunnel(ctx *muzak.Context, tx *ent.Client, in schemas.SavedFunnelParams) (*ent.SavedFunnel, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := tx.SavedFunnel.Query().
		Where(savedfunnel.UID(in.FunnelID), savedfunnel.ProjectID(proj.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no such funnel")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// ReadSavedFunnel is one saved funnel.
func ReadSavedFunnel(ctx *muzak.Context, in schemas.SavedFunnelParams) (schemas.SavedFunnelOut, error) {
	row, err := findSavedFunnel(ctx, muzak.From[*core.DB](ctx).Client, in)
	if err != nil {
		return schemas.SavedFunnelOut{}, err
	}
	out, err := savedFunnelOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// CreateSavedFunnel keeps a funnel by name.
func CreateSavedFunnel(ctx *muzak.Context, in schemas.SavedFunnelIn) (schemas.SavedFunnelOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	definition, err := definitionOf(in.FunnelDefinition)
	if err != nil {
		return schemas.SavedFunnelOut{}, core.AsHTTPError(err)
	}
	who := actorOf(ctx).Label
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.SavedFunnel, error) {
		row, err := tx.SavedFunnel.Create().SetProjectID(proj.ID).SetName(in.Name).
			SetDescription(in.Description).SetDefinition(definition).SetCreatedBy(who).SetUpdatedBy(who).
			Save(ctx.Context())
		if err != nil {
			return nil, funnelConflict(err, in.Name)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.FunnelWritten, "funnel", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": in.Name, "created": true})
	})
	if err != nil {
		return schemas.SavedFunnelOut{}, err
	}
	ctx.SetStatus(201)
	out, err := savedFunnelOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// UpdateSavedFunnel replaces a saved funnel's name and definition.
func UpdateSavedFunnel(ctx *muzak.Context, in schemas.UpdateSavedFunnelIn) (schemas.SavedFunnelOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	definition, err := definitionOf(in.FunnelDefinition)
	if err != nil {
		return schemas.SavedFunnelOut{}, core.AsHTTPError(err)
	}
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.SavedFunnel, error) {
		row, err := findSavedFunnel(ctx, tx.Client(), in.SavedFunnelParams)
		if err != nil {
			return nil, err
		}
		updated, err := tx.SavedFunnel.UpdateOne(row).SetName(in.Name).SetDescription(in.Description).
			SetDefinition(definition).SetUpdatedBy(actorOf(ctx).Label).Save(ctx.Context())
		if err != nil {
			return nil, funnelConflict(err, in.Name)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.FunnelWritten, "funnel", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": in.Name, "was_name": row.Name, "created": false})
	})
	if err != nil {
		return schemas.SavedFunnelOut{}, err
	}
	out, err := savedFunnelOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// DeleteSavedFunnel forgets a saved funnel. The events it asked about are untouched.
func DeleteSavedFunnel(ctx *muzak.Context, in schemas.SavedFunnelParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	_, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		row, err := findSavedFunnel(ctx, tx.Client(), in)
		if err != nil {
			return struct{}{}, err
		}
		if err := tx.SavedFunnel.DeleteOne(row).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.FunnelDeleted, "funnel", row.UID.String(),
			map[string]any{"project": proj.Slug, "name": row.Name})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}
