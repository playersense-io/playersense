package handlers

import (
	"strings"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/eventdefinition"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// What a studio says its event names mean.

func eventDefinitionOut(row *ent.EventDefinition) schemas.EventDefinitionOut {
	return schemas.EventDefinitionOut{Name: row.Name, Category: string(row.Category),
		Description: row.Description, UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}
}

// defaultCategory is an event's category before anybody has described it.
func defaultCategory(name, kind string) string {
	switch {
	case kind == "session" || strings.HasPrefix(name, "session_"):
		return "session"
	case kind == "purchase":
		return "economy"
	case kind == "exposure":
		return "engagement"
	default:
		return "custom"
	}
}

// ListEventDefinitions is every event a project has described.
func ListEventDefinitions(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.EventDefinitionListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	rows, err := db.Client.EventDefinition.Query().Where(eventdefinition.ProjectID(proj.ID)).
		Order(ent.Asc(eventdefinition.FieldName)).All(ctx.Context())
	if err != nil {
		return schemas.EventDefinitionListOut{}, core.AsHTTPError(err)
	}
	out := schemas.EventDefinitionListOut{Definitions: make([]schemas.EventDefinitionOut, 0, len(rows))}
	for _, row := range rows {
		out.Definitions = append(out.Definitions, eventDefinitionOut(row))
	}
	return out, nil
}

// DefineEvent says what an event name means, whether or not the game has sent it yet: a tracking
// plan written before the build is a plan.
func DefineEvent(ctx *muzak.Context, in schemas.EventDefinitionIn) (schemas.EventDefinitionOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	who := actorOf(ctx).Label
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.EventDefinition, error) {
		existing, err := tx.EventDefinition.Query().
			Where(eventdefinition.ProjectID(proj.ID), eventdefinition.Name(in.Name)).ForUpdate().Only(ctx.Context())
		var row *ent.EventDefinition
		switch {
		case ent.IsNotFound(err):
			row, err = tx.EventDefinition.Create().SetProjectID(proj.ID).SetName(in.Name).
				SetCategory(eventdefinition.Category(in.Category)).SetDescription(in.Description).
				SetCreatedBy(who).SetUpdatedBy(who).Save(ctx.Context())
		case err == nil:
			row, err = tx.EventDefinition.UpdateOne(existing).SetCategory(eventdefinition.Category(in.Category)).
				SetDescription(in.Description).SetUpdatedBy(who).Save(ctx.Context())
		}
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.EventDefined, "event", in.Name,
			map[string]any{"project": proj.Slug, "category": in.Category})
	})
	if err != nil {
		return schemas.EventDefinitionOut{}, err
	}
	return eventDefinitionOut(row), nil
}
