package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/featureflag"
	"playersense.dev/backend/ent/segment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/schemas"
)

// A published version's draft: the flags and segments it was compiled from, kept with it so the
// draft can be set back to what a version was. The compiled document writes each segment into the
// rules that name it, so it alone cannot say which segment a rule meant.

type draftFlag struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Fallback json.RawMessage `json:"fallback"`
	Rules    []any           `json:"rules"`
	Rollout  int             `json:"rollout"`
	Salt     string          `json:"salt,omitempty"`
}

type draftSegment struct {
	Key  string         `json:"key"`
	Name string         `json:"name"`
	Rule map[string]any `json:"rule"`
}

type draftSnapshot struct {
	Flags    []draftFlag    `json:"flags"`
	Segments []draftSegment `json:"segments"`
}

// snapshotDraft is the project's draft as it stands, for a version about to be published from it.
func snapshotDraft(ctx context.Context, client *ent.Client, projectID int) (map[string]any, error) {
	flags, err := client.FeatureFlag.Query().Where(featureflag.ProjectID(projectID), featureflag.Archived(false)).All(ctx)
	if err != nil {
		return nil, err
	}
	segments, err := client.Segment.Query().Where(segment.ProjectID(projectID), segment.Archived(false)).All(ctx)
	if err != nil {
		return nil, err
	}
	snap := draftSnapshot{Flags: []draftFlag{}, Segments: []draftSegment{}}
	for _, f := range flags {
		snap.Flags = append(snap.Flags, draftFlag{Key: f.Key, Name: f.Name, Kind: string(f.Kind), Fallback: f.Fallback,
			Rules: f.Rules, Rollout: f.Rollout, Salt: f.Salt})
	}
	for _, s := range segments {
		snap.Segments = append(snap.Segments, draftSegment{Key: s.Key, Name: s.Name, Rule: s.Rule})
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

// RestoreDraft sets the draft back to what a published version was compiled from: each flag's
// fallback, rules, reach and bucketing as they were, flags that were not in it archived,
// flags and segments since deleted made again, and segments' rules as they were. Nothing is
// published: the draft is then published like any other, and the environments keep serving
// what they serve until it is.
func RestoreDraft(ctx *muzak.Context, in schemas.ConfigVersionParams) (schemas.DraftRestoredOut, error) {
	db := muzak.From[*core.DB](ctx)
	project := muzak.From[*core.Principal](ctx).Project
	env, err := environmentOf(ctx.Context(), db, project.ID, in.Environment)
	if err != nil {
		return schemas.DraftRestoredOut{}, err
	}
	version, err := versionRow(ctx.Context(), db, env.ID, in.Version)
	if err != nil {
		return schemas.DraftRestoredOut{}, err
	}
	if len(version.Draft) == 0 {
		return schemas.DraftRestoredOut{}, muzak.Conflict("version " + strconv.Itoa(in.Version) +
			" was published before drafts were kept with versions; roll back to it instead").WithCode("no_draft_kept")
	}
	raw, _ := json.Marshal(version.Draft)
	var snap draftSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return schemas.DraftRestoredOut{}, core.AsHTTPError(err)
	}
	who := actorOf(ctx).Label
	out := schemas.DraftRestoredOut{Environment: in.Environment, Version: in.Version,
		FlagsRestored: []string{}, FlagsArchived: []string{}, FlagsSkipped: []string{}, SegmentsRestored: []string{}}
	_, err = inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		kept := map[string]bool{}
		for _, f := range snap.Flags {
			kept[f.Key] = true
			fallback := f.Fallback
			if len(fallback) == 0 {
				fallback = json.RawMessage("null")
			}
			rules := f.Rules
			if rules == nil {
				rules = []any{}
			}
			current, err := tx.FeatureFlag.Query().Where(featureflag.ProjectID(project.ID), featureflag.Key(f.Key)).Only(ctx.Context())
			switch {
			case ent.IsNotFound(err):
				err = tx.FeatureFlag.Create().SetProjectID(project.ID).SetKey(f.Key).SetName(nameOr(f.Name, f.Key)).
					SetKind(featureflag.Kind(f.Kind)).SetFallback(fallback).SetRules(rules).SetRollout(f.Rollout).
					SetSalt(f.Salt).SetCreatedBy(who).SetUpdatedBy(who).Exec(ctx.Context())
			case err == nil && string(current.Kind) != f.Kind:
				// A flag's kind is fixed when it is made: one made again under the same key with another
				// kind cannot take the old value back, and is left as it is.
				out.FlagsSkipped = append(out.FlagsSkipped, f.Key)
				continue
			case err == nil:
				err = tx.FeatureFlag.UpdateOne(current).SetFallback(fallback).
					SetRules(rules).SetRollout(f.Rollout).SetSalt(f.Salt).SetArchived(false).SetUpdatedBy(who).Exec(ctx.Context())
			}
			if err != nil {
				return struct{}{}, core.AsHTTPError(fmt.Errorf("flag %s: %w", f.Key, err))
			}
			out.FlagsRestored = append(out.FlagsRestored, f.Key)
		}
		others, err := tx.FeatureFlag.Query().Where(featureflag.ProjectID(project.ID), featureflag.Archived(false)).All(ctx.Context())
		if err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		for _, f := range others {
			if kept[f.Key] {
				continue
			}
			if err := tx.FeatureFlag.UpdateOne(f).SetArchived(true).SetUpdatedBy(who).Exec(ctx.Context()); err != nil {
				return struct{}{}, core.AsHTTPError(err)
			}
			out.FlagsArchived = append(out.FlagsArchived, f.Key)
		}
		for _, sg := range snap.Segments {
			current, err := tx.Segment.Query().Where(segment.ProjectID(project.ID), segment.Key(sg.Key)).Only(ctx.Context())
			switch {
			case ent.IsNotFound(err):
				err = tx.Segment.Create().SetProjectID(project.ID).SetKey(sg.Key).SetName(nameOr(sg.Name, sg.Key)).
					SetRule(sg.Rule).SetCreatedBy(who).SetUpdatedBy(who).Exec(ctx.Context())
			case err == nil:
				err = tx.Segment.UpdateOne(current).SetRule(sg.Rule).SetArchived(false).SetUpdatedBy(who).Exec(ctx.Context())
			}
			if err != nil {
				return struct{}{}, core.AsHTTPError(fmt.Errorf("segment %s: %w", sg.Key, err))
			}
			out.SegmentsRestored = append(out.SegmentsRestored, sg.Key)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.DraftRestored, "config_version", version.UID.String(),
			map[string]any{"environment": in.Environment, "version": in.Version, "flags_restored": len(out.FlagsRestored),
				"flags_archived": out.FlagsArchived, "segments_restored": len(out.SegmentsRestored)})
	})
	if err != nil {
		return schemas.DraftRestoredOut{}, err
	}
	return out, nil
}

func nameOr(name, key string) string {
	if name != "" {
		return name
	}
	return key
}
