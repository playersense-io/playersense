package handlers

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/configversion"
	"playersense.dev/backend/ent/environment"
	"playersense.dev/backend/ent/featureflag"
	"playersense.dev/backend/ent/liveopsevent"
	"playersense.dev/backend/ent/segment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// PublishConfig compiles what a studio has edited into what a player will be served (§41).
//
// This is the irreversible half, which is why it has a permission of its own: drafting a
// LiveOps event is a change nobody has seen, and publishing one is a change every player has.
// `liveops.write` and `liveops.publish` are separate so that a junior can draft and a senior
// can ship.
//
// # Everything expensive happens here
//
// Segments are inlined into the rules that name them, every audience is re-validated, and the
// document records once whether any rule in the whole project needs the player's history. A
// document that compiles is a document that evaluates: there is no second class of error
// waiting at request time, when the person who could fix it is asleep and the only symptom is
// a flag that reaches nobody.
//
// # The version is immutable
//
// A new row rather than an update, so version 7 keeps saying what version 7 said. It is the
// only record of what a rule *meant* on the day it ran — editing a segment tomorrow cannot
// rewrite what a player was served today — and it is what makes a rollback a publish rather
// than an archaeology exercise.
func PublishConfig(ctx *muzak.Context, in schemas.PublishConfigIn) (schemas.ConfigVersionOut, error) {
	db := muzak.From[*core.DB](ctx)
	principal := muzak.From[*core.Principal](ctx)
	project := principal.Project

	env, err := db.Client.Environment.Query().
		Where(environment.ProjectID(project.ID), environment.KindEQ(environment.Kind(in.Environment))).
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.ConfigVersionOut{}, muzak.NotFound(
				"this project has no " + in.Environment + " environment")
		}
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	input, err := gatherConfig(ctx.Context(), db, project.ID)
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	next, err := nextVersion(ctx.Context(), db, env.ID)
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	input.Version = next

	document, err := liveconfig.Compile(input)
	if err != nil {
		// A 422 rather than a 500, and the message is the compiler's own. "Segment
		// 'whales' does not exist" is something the person looking at the dashboard can
		// fix; "internal error" is a support ticket about the same sentence.
		return schemas.ConfigVersionOut{}, muzak.NewHTTPError(
			http.StatusUnprocessableEntity, err.Error()).WithCode("config_does_not_compile")
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	tx, err := db.Client.Tx(ctx.Context())
	if err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	created, err := tx.ConfigVersion.Create().
		SetProjectID(project.ID).
		SetEnvironmentID(env.ID).
		SetVersion(next).
		SetDocument(stored).
		SetPublishedByUserID(principal.User.ID).
		SetPublishedByLabel(principal.User.Email).
		SetNote(in.Note).
		Save(ctx.Context())
	if err != nil {
		_ = tx.Rollback()
		// The unique index on (version, environment) is what makes two people pressing
		// publish at the same moment produce two versions or one conflict, never one
		// version holding somebody else's document.
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	// The version already records who published it, so this entry is not that — it is the line
	// that puts a publish in the same list as the role change and the key revocation, which is
	// where somebody reading "what happened on Tuesday" is looking.
	if err := auditFor(ctx).record(ctx.Context(), tx, audit.ConfigPublished,
		"config_version", created.UID.String(), map[string]any{
			"environment": in.Environment, "version": created.Version,
			"flags": len(document.Flags), "liveops_events": len(document.Events),
			"needs_player_facts": document.NeedsPlayerFacts, "note": in.Note,
		}); err != nil {
		_ = tx.Rollback()
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}
	if err := tx.Commit(); err != nil {
		return schemas.ConfigVersionOut{}, core.AsHTTPError(err)
	}

	// Visible immediately on any instance holding a store, which in practice is the
	// config-api processes rather than this one. Harmless here and correct if they are
	// ever combined.
	if store, found := muzak.TryFrom[*liveconfig.Store](ctx); found {
		store.Invalidate(env.ID)
	}

	return schemas.ConfigVersionOut{
		Version:          created.Version,
		Environment:      in.Environment,
		Flags:            len(document.Flags),
		Events:           len(document.Events),
		NeedsPlayerFacts: document.NeedsPlayerFacts,
		PublishedBy:      created.PublishedByLabel,
		PublishedAt:      created.CreatedAt,
		Note:             created.Note,
	}, nil
}

// gatherConfig reads everything a publish compiles.
//
// Archived segments and flags are left out, which is what archiving is for: the row stays so
// that an older published version still explains itself, and the next publish stops carrying
// it. A LiveOps event is excluded unless somebody has set it active — a draft is a draft.
func gatherConfig(ctx context.Context, db *core.DB, projectID int) (liveconfig.Input, error) {
	segments, err := db.Client.Segment.Query().
		Where(segment.ProjectID(projectID), segment.Archived(false)).All(ctx)
	if err != nil {
		return liveconfig.Input{}, err
	}
	input := liveconfig.Input{Segments: make(liveconfig.Segments, len(segments))}
	for _, row := range segments {
		rule, err := ruleFrom(row.Rule)
		if err != nil {
			return liveconfig.Input{}, err
		}
		input.Segments[row.Key] = rule
	}

	flags, err := db.Client.FeatureFlag.Query().
		Where(featureflag.ProjectID(projectID), featureflag.Archived(false)).All(ctx)
	if err != nil {
		return liveconfig.Input{}, err
	}
	// A running experiment is compiled into its flag, so starting or stopping one changes what
	// the next publish serves and nothing before it.
	splits, err := runningSplits(ctx, db, projectID)
	if err != nil {
		return liveconfig.Input{}, err
	}
	for _, row := range flags {
		fallback, err := valueFrom(row.Fallback)
		if err != nil {
			return liveconfig.Input{}, fmt.Errorf("flag %q fallback: %w", row.Key, err)
		}
		flag := liveconfig.Flag{
			Key: row.Key, Kind: string(row.Kind), Fallback: fallback,
			Rollout: row.Rollout, Salt: row.Salt, Split: splits[row.Key],
		}
		for _, raw := range row.Rules {
			variant, err := variantFrom(raw)
			if err != nil {
				return liveconfig.Input{}, err
			}
			flag.Rules = append(flag.Rules, variant)
		}
		input.Flags = append(input.Flags, flag)
	}

	events, err := db.Client.LiveOpsEvent.Query().
		Where(liveopsevent.ProjectID(projectID),
			liveopsevent.StatusEQ(liveopsevent.StatusActive)).All(ctx)
	if err != nil {
		return liveconfig.Input{}, err
	}
	for _, row := range events {
		audience, err := ruleFrom(row.Audience)
		if err != nil {
			return liveconfig.Input{}, err
		}
		payload, err := valueFrom(row.Payload)
		if err != nil {
			return liveconfig.Input{}, fmt.Errorf("liveops event %q payload: %w", row.Key, err)
		}
		input.Events = append(input.Events, liveconfig.LiveOpsEvent{
			Key: row.Key, Kind: row.Kind, Priority: row.Priority,
			Schedule: row.Schedule, Audience: audience, Payload: payload,
			Rollout: row.Rollout, Salt: row.Salt,
		})
	}
	return input, nil
}

// nextVersion is the environment's highest published version plus one.
//
// Monotonic per environment rather than per project, because an SDK compares it as an ETag
// and two environments moving each other's numbers would make "version 7" mean two things.
func nextVersion(ctx context.Context, db *core.DB, environmentID int) (int, error) {
	latest, err := db.Client.ConfigVersion.Query().
		Where(configversion.EnvironmentID(environmentID)).
		Order(ent.Desc(configversion.FieldVersion)).
		First(ctx)
	switch {
	case err == nil:
		return latest.Version + 1, nil
	case ent.IsNotFound(err):
		return 1, nil
	default:
		return 0, err
	}
}

// valueFrom decodes a raw JSON column into the value it holds.
//
// A flag's fallback and a LiveOps payload are stored raw because the database has no opinion
// about their shape, and they are decoded here because [liveconfig.Input] does: its fields are
// `any`, meaning the value, and handing them the undecoded bytes would put the SDK's answer at
// the mercy of how a byte slice happens to marshal.
//
// An absent column is a nil value rather than an error. `null` is a legitimate fallback for a
// json flag, and a row written before a column existed must not fail a publish.
func valueFrom(raw jsontext.Value) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// ruleFrom re-reads a rule out of the JSON column ent hands back as a map.
func ruleFrom(raw map[string]any) (targeting.Rule, error) {
	if len(raw) == 0 {
		return targeting.Rule{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return targeting.Rule{}, err
	}
	return targeting.Parse(encoded)
}

// variantFrom re-reads one of a flag's ordered rules.
func variantFrom(raw any) (liveconfig.Variant, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return liveconfig.Variant{}, err
	}
	var shape struct {
		Audience json.RawMessage `json:"audience"`
		Value    any             `json:"value"`
		Rollout  int             `json:"rollout"`
	}
	if err := json.Unmarshal(encoded, &shape); err != nil {
		return liveconfig.Variant{}, err
	}
	variant := liveconfig.Variant{Value: shape.Value, Rollout: shape.Rollout}
	if len(shape.Audience) > 0 {
		if variant.Audience, err = targeting.Parse(shape.Audience); err != nil {
			return liveconfig.Variant{}, err
		}
	}
	return variant, nil
}
