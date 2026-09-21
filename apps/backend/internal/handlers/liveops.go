package handlers

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"net/http"
	"strconv"
	"time"

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
	"playersense.dev/backend/internal/liveops"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// The authoring half of §41: the three tables a studio edits, before [PublishConfig] compiles
// them into what a player is served.
//
// # Everything is validated here, not only at the publish
//
// The publish was the only validator until these routes existed, and that is the wrong place
// for a message. A rule refused at publish time is refused in front of whoever holds
// `liveops.publish` — a senior, hours or days later, looking at a 422 about a rule somebody
// else wrote. The same refusal here reaches the person whose screen still has the rule on it.
//
// So a write does what a publish does, minus the writing: it parses the rule, checks the
// operators and the depth, resolves every segment reference against the project, and refuses a
// cycle. [PublishConfig] still re-validates everything, because a segment archived after a flag
// referenced it can break a rule nobody touched — the two checks answer the same question at
// two different moments and both are needed.
//
// # Nothing here reaches a player
//
// A write changes a row. Players are served the last published document (§42), so drafting is
// safe by construction and that is why `liveops.write` and `liveops.publish` are separate
// permissions. An event is created as a draft for the same reason: publishing reads only the
// active ones, so an event cannot reach a game by being typed.

// ---- segments ---------------------------------------------------------------

// ListSegments returns a project's named audiences, archived ones included.
//
// Archived rows are returned rather than hidden: an archived segment is still inlined in every
// published version that used it, so a dashboard explaining version 7 has to be able to name it.
// The `archived` member is what a list view filters on.
func ListSegments(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.SegmentListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	rows, err := db.Client.Segment.Query().
		Where(segment.ProjectID(proj.ID)).
		Order(ent.Asc(segment.FieldKey)).
		All(ctx.Context())
	if err != nil {
		return schemas.SegmentListOut{}, core.AsHTTPError(err)
	}
	out := schemas.SegmentListOut{Segments: make([]schemas.SegmentOut, 0, len(rows))}
	for _, row := range rows {
		out.Segments = append(out.Segments, segmentOut(row))
	}
	return out, nil
}

// ReadSegment returns one named audience.
func ReadSegment(ctx *muzak.Context, in schemas.TargetParams) (schemas.SegmentOut, error) {
	row, err := findSegment(ctx, in.Key)
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	return segmentOut(row), nil
}

// CreateSegment names an audience.
func CreateSegment(ctx *muzak.Context, in schemas.CreateSegmentIn) (schemas.SegmentOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	stored, err := authoredRule(ctx.Context(), db, proj.ID, in.Key, in.Rule)
	if err != nil {
		return schemas.SegmentOut{}, err
	}

	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.Segment, error) {
		row, err := tx.Segment.Create().
			SetProjectID(proj.ID).
			SetKey(in.Key).
			SetName(in.Name).
			SetDescription(in.Description).
			SetRule(stored).
			Save(ctx.Context())
		if err != nil {
			return nil, asConflict(err, "a segment", in.Key)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.SegmentWritten,
			"segment", row.Key, map[string]any{"project": proj.Slug, "created": true})
	})
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	ctx.SetStatus(http.StatusCreated)
	return segmentOut(row), nil
}

// UpdateSegment replaces a named audience.
//
// The candidate rule is checked with itself in the project's segment set, which is what catches
// a self-reference: A -> A is only writable by the edit that introduces it, and this is that
// edit.
func UpdateSegment(ctx *muzak.Context, in schemas.UpdateSegmentIn) (schemas.SegmentOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := findSegment(ctx, in.Key)
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	stored, err := authoredRule(ctx.Context(), db, proj.ID, in.Key, in.Rule)
	if err != nil {
		return schemas.SegmentOut{}, err
	}

	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Segment, error) {
		updated, err := tx.Segment.UpdateOneID(row.ID).
			SetName(in.Name).
			SetDescription(in.Description).
			SetRule(stored).
			SetArchived(archivedOr(in.Archived, row.Archived)).
			Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.SegmentWritten,
			"segment", row.Key, map[string]any{"project": proj.Slug, "created": false})
	})
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	return segmentOut(updated), nil
}

// ArchiveSegment leaves a segment out of the next publish, keeping the row.
//
// Never a delete. A published version holds this rule inlined, so the row is the only thing that
// can still say what the audience in version 7 was called — and §41's versions are immutable
// precisely so history stays answerable.
func ArchiveSegment(ctx *muzak.Context, in schemas.TargetParams) (schemas.SegmentOut, error) {
	row, err := findSegment(ctx, in.Key)
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	if row.Archived {
		return schemas.SegmentOut{}, muzak.Conflict("that segment is already archived")
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.Segment, error) {
		updated, err := tx.Segment.UpdateOneID(row.ID).SetArchived(true).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.SegmentArchived,
			"segment", row.Key, map[string]any{"name": row.Name})
	})
	if err != nil {
		return schemas.SegmentOut{}, err
	}
	return segmentOut(updated), nil
}

// ---- feature flags ----------------------------------------------------------

// ListFlags returns a project's flags.
func ListFlags(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.FlagListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	rows, err := db.Client.FeatureFlag.Query().
		Where(featureflag.ProjectID(proj.ID)).
		Order(ent.Asc(featureflag.FieldKey)).
		All(ctx.Context())
	if err != nil {
		return schemas.FlagListOut{}, core.AsHTTPError(err)
	}
	out := schemas.FlagListOut{Flags: make([]schemas.FlagOut, 0, len(rows))}
	for _, row := range rows {
		flag, err := flagOut(row)
		if err != nil {
			return schemas.FlagListOut{}, err
		}
		out.Flags = append(out.Flags, flag)
	}
	return out, nil
}

// ReadFlag returns one flag.
func ReadFlag(ctx *muzak.Context, in schemas.TargetParams) (schemas.FlagOut, error) {
	row, err := findFlag(ctx, in.Key)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	return flagOut(row)
}

// CreateFlag declares a flag.
//
// The kind is fixed here and never again. A game calls `FlagBool` on this key, so a kind that
// changed would be a document the installed build cannot read — and an installed build is the
// one thing a backend cannot migrate.
func CreateFlag(ctx *muzak.Context, in schemas.CreateFlagIn) (schemas.FlagOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	kind, err := liveconfig.ParseKind(in.Kind)
	if err != nil {
		return schemas.FlagOut{}, muzak.UnprocessableEntity(err.Error()).WithCode("unknown_flag_kind")
	}
	fallback, err := typedValue(kind, in.Fallback, "fallback")
	if err != nil {
		return schemas.FlagOut{}, err
	}
	stored, err := canonical(fallback)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	segments, err := projectSegments(ctx.Context(), db, proj.ID)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	rules, err := authoredVariants(kind, in.Rules, segments)
	if err != nil {
		return schemas.FlagOut{}, err
	}

	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.FeatureFlag, error) {
		row, err := tx.FeatureFlag.Create().
			SetProjectID(proj.ID).
			SetKey(in.Key).
			SetName(in.Name).
			SetDescription(in.Description).
			SetKind(featureflag.Kind(kind)).
			SetFallback(stored).
			SetRules(rules).
			SetRollout(rolloutOrFull(in.Rollout)).
			SetSalt(in.Salt).
			Save(ctx.Context())
		if err != nil {
			return nil, asConflict(err, "a flag", in.Key)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.FlagWritten,
			"flag", row.Key, map[string]any{
				"project": proj.Slug, "created": true,
				"kind": string(kind), "rollout": row.Rollout, "rules": len(rules),
			})
	})
	if err != nil {
		return schemas.FlagOut{}, err
	}
	ctx.SetStatus(http.StatusCreated)
	return flagOut(row)
}

// UpdateFlag replaces a flag's editable fields.
//
// The kind is read from the row rather than the body, because the body has no such member: a
// field a caller may send that has no effect is worse than one that does not exist, since
// somebody will send it, see a 200, and believe the kind changed.
func UpdateFlag(ctx *muzak.Context, in schemas.UpdateFlagIn) (schemas.FlagOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := findFlag(ctx, in.Key)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	kind := liveconfig.Kind(row.Kind)
	fallback, err := typedValue(kind, in.Fallback, "fallback")
	if err != nil {
		return schemas.FlagOut{}, err
	}
	stored, err := canonical(fallback)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	segments, err := projectSegments(ctx.Context(), db, proj.ID)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	rules, err := authoredVariants(kind, in.Rules, segments)
	if err != nil {
		return schemas.FlagOut{}, err
	}

	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.FeatureFlag, error) {
		updated, err := tx.FeatureFlag.UpdateOneID(row.ID).
			SetName(in.Name).
			SetDescription(in.Description).
			SetFallback(stored).
			SetRules(rules).
			SetRollout(rolloutOrFull(in.Rollout)).
			SetSalt(in.Salt).
			SetArchived(archivedOr(in.Archived, row.Archived)).
			Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		// The rollout before and after, because moving one is how a flag reaches more
		// players and "who changed it to 100%" is the question an incident asks.
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.FlagWritten,
			"flag", row.Key, map[string]any{
				"project": proj.Slug, "created": false,
				"rollout": updated.Rollout, "was_rollout": row.Rollout,
				"rules": len(rules),
			})
	})
	if err != nil {
		return schemas.FlagOut{}, err
	}
	return flagOut(updated)
}

// ArchiveFlag leaves a flag out of the next publish, keeping the row.
//
// The game keeps reading the key and keeps getting the answer the last published document gave,
// until somebody publishes. That is the property that makes archiving safe and deleting not: a
// flag that vanished from a document is a flag whose SDK falls back, and the fallback is a value
// the studio chose for an offline device rather than for this.
func ArchiveFlag(ctx *muzak.Context, in schemas.TargetParams) (schemas.FlagOut, error) {
	row, err := findFlag(ctx, in.Key)
	if err != nil {
		return schemas.FlagOut{}, err
	}
	if row.Archived {
		return schemas.FlagOut{}, muzak.Conflict("that flag is already archived")
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.FeatureFlag, error) {
		updated, err := tx.FeatureFlag.UpdateOneID(row.ID).SetArchived(true).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.FlagArchived,
			"flag", row.Key, map[string]any{"name": row.Name, "kind": string(row.Kind)})
	})
	if err != nil {
		return schemas.FlagOut{}, err
	}
	return flagOut(updated)
}

// ---- LiveOps events ---------------------------------------------------------

// ListLiveOpsEvents returns a project's events, in priority order.
func ListLiveOpsEvents(ctx *muzak.Context, _ schemas.ProjectParams) (schemas.LiveOpsEventListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	rows, err := db.Client.LiveOpsEvent.Query().
		Where(liveopsevent.ProjectID(proj.ID)).
		Order(ent.Desc(liveopsevent.FieldPriority), ent.Asc(liveopsevent.FieldKey)).
		All(ctx.Context())
	if err != nil {
		return schemas.LiveOpsEventListOut{}, core.AsHTTPError(err)
	}
	now := nowUTC()
	out := schemas.LiveOpsEventListOut{Events: make([]schemas.LiveOpsEventOut, 0, len(rows))}
	for _, row := range rows {
		event, err := liveOpsEventOut(row, now)
		if err != nil {
			return schemas.LiveOpsEventListOut{}, err
		}
		out.Events = append(out.Events, event)
	}
	return out, nil
}

// ReadLiveOpsEvent returns one event.
func ReadLiveOpsEvent(ctx *muzak.Context, in schemas.TargetParams) (schemas.LiveOpsEventOut, error) {
	row, err := findLiveOpsEvent(ctx, in.Key)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	return liveOpsEventOut(row, nowUTC())
}

// CreateLiveOpsEvent schedules an event.
func CreateLiveOpsEvent(ctx *muzak.Context, in schemas.CreateLiveOpsEventIn) (schemas.LiveOpsEventOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	schedule, err := authoredSchedule(in.Schedule)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	stored, err := authoredRule(ctx.Context(), db, proj.ID, "", in.Audience)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	payload, err := rawOrNull(in.Payload)
	if err != nil {
		return schemas.LiveOpsEventOut{}, muzak.UnprocessableEntity(
			"payload is not valid JSON: " + err.Error()).WithCode("invalid_payload")
	}

	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.LiveOpsEvent, error) {
		row, err := tx.LiveOpsEvent.Create().
			SetProjectID(proj.ID).
			SetKey(in.Key).
			SetKind(in.Kind).
			SetName(in.Name).
			SetStatus(liveopsevent.Status(orDefault(in.Status, "draft"))).
			SetSchedule(schedule).
			SetPriority(in.Priority).
			SetAudience(stored).
			SetPayload(payload).
			SetRollout(rolloutOrFull(in.Rollout)).
			SetSalt(in.Salt).
			Save(ctx.Context())
		if err != nil {
			return nil, asConflict(err, "a LiveOps event", in.Key)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.EventWritten,
			"liveops_event", row.Key, map[string]any{
				"project": proj.Slug, "created": true,
				"kind": in.Kind, "status": string(row.Status), "rollout": row.Rollout,
			})
	})
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	ctx.SetStatus(http.StatusCreated)
	return liveOpsEventOut(row, nowUTC())
}

// UpdateLiveOpsEvent replaces an event's editable fields, status included.
//
// The four statuses are a free transition rather than a state machine, because every pair is
// something a studio legitimately does: pausing a live event, resuming it, returning an active
// one to draft to work on it, un-archiving one it turns out to want next year. A machine here
// would only forbid one of those, and the one it forbade would be the one somebody needed at
// two in the morning.
func UpdateLiveOpsEvent(ctx *muzak.Context, in schemas.UpdateLiveOpsEventIn) (schemas.LiveOpsEventOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := findLiveOpsEvent(ctx, in.Key)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	schedule, err := authoredSchedule(in.Schedule)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	stored, err := authoredRule(ctx.Context(), db, proj.ID, "", in.Audience)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	payload, err := rawOrNull(in.Payload)
	if err != nil {
		return schemas.LiveOpsEventOut{}, muzak.UnprocessableEntity(
			"payload is not valid JSON: " + err.Error()).WithCode("invalid_payload")
	}

	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.LiveOpsEvent, error) {
		updated, err := tx.LiveOpsEvent.UpdateOneID(row.ID).
			SetKind(in.Kind).
			SetName(in.Name).
			SetStatus(liveopsevent.Status(orDefault(in.Status, string(row.Status)))).
			SetSchedule(schedule).
			SetPriority(in.Priority).
			SetAudience(stored).
			SetPayload(payload).
			SetRollout(rolloutOrFull(in.Rollout)).
			SetSalt(in.Salt).
			Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		// The status before and after. A draft becoming active is the moment an event
		// starts reaching players on the next publish, and it is the single most
		// consequential edit this route makes.
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.EventWritten,
			"liveops_event", row.Key, map[string]any{
				"project": proj.Slug, "created": false,
				"status": string(updated.Status), "was_status": string(row.Status),
				"rollout": updated.Rollout, "was_rollout": row.Rollout,
			})
	})
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	return liveOpsEventOut(updated, nowUTC())
}

// ArchiveLiveOpsEvent takes an event out of the next publish, keeping the row.
func ArchiveLiveOpsEvent(ctx *muzak.Context, in schemas.TargetParams) (schemas.LiveOpsEventOut, error) {
	row, err := findLiveOpsEvent(ctx, in.Key)
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	if row.Status == liveopsevent.StatusArchived {
		return schemas.LiveOpsEventOut{}, muzak.Conflict("that LiveOps event is already archived")
	}
	updated, err := inTx(ctx, func(tx *ent.Tx) (*ent.LiveOpsEvent, error) {
		updated, err := tx.LiveOpsEvent.UpdateOneID(row.ID).
			SetStatus(liveopsevent.StatusArchived).Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return updated, auditFor(ctx).record(ctx.Context(), tx, audit.EventArchived,
			"liveops_event", row.Key, map[string]any{
				"name": row.Name, "was_status": string(row.Status),
			})
	})
	if err != nil {
		return schemas.LiveOpsEventOut{}, err
	}
	return liveOpsEventOut(updated, nowUTC())
}

// ---- the draft --------------------------------------------------------------

// ReadDraft says what the next publish would produce, without producing it.
//
// It compiles the same input [PublishConfig] would and throws the document away. Two reasons it
// is worth a route of its own:
//
//   - **The publish is somebody else's.** `liveops.publish` is a separate permission, so the
//     person pressing it often did not write the change. This is what they read first, and it
//     needs only `liveops.read`.
//   - **A refusal before the irreversible step is free.** A publish that does not compile is a
//     422 and nothing changes, so nothing is lost — but a studio finding out at that moment is a
//     studio whose release is now blocked on debugging a rule. Here the same message costs a GET.
//
// It also names what is being *left out*, which is the question a draft view otherwise cannot
// answer: an archived flag and a paused event are both absent on purpose, and "my event is
// missing" needs to be distinguishable from a bug.
func ReadDraft(ctx *muzak.Context, in schemas.DraftParams) (schemas.DraftOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	env, err := db.Client.Environment.Query().
		Where(environment.ProjectID(proj.ID), environment.KindEQ(environment.Kind(in.Environment))).
		Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.DraftOut{}, muzak.NotFound(
				"this project has no " + in.Environment + " environment")
		}
		return schemas.DraftOut{}, core.AsHTTPError(err)
	}

	published := 0
	latest, err := db.Client.ConfigVersion.Query().
		Where(configversion.EnvironmentID(env.ID)).
		Order(ent.Desc(configversion.FieldVersion)).
		First(ctx.Context())
	switch {
	case err == nil:
		published = latest.Version
	case ent.IsNotFound(err):
	default:
		return schemas.DraftOut{}, core.AsHTTPError(err)
	}

	out := schemas.DraftOut{
		Environment:      in.Environment,
		PublishedVersion: published,
		NextVersion:      published + 1,
	}
	if out.Excluded, err = excludedFrom(ctx.Context(), db, proj.ID); err != nil {
		return schemas.DraftOut{}, err
	}

	input, err := gatherConfig(ctx.Context(), db, proj.ID)
	if err != nil {
		// A stored rule that no longer parses. Reported as a problem with the draft rather
		// than as a 500, because that is what it is: something a person has to fix, and
		// the message names it.
		out.Problem = err.Error()
		return out, nil
	}
	input.Version = out.NextVersion

	document, err := liveconfig.Compile(input)
	if err != nil {
		out.Problem = err.Error()
		return out, nil
	}
	out.Compiles = true
	out.Flags = len(document.Flags)
	out.Events = len(document.Events)
	out.NeedsPlayerFacts = document.NeedsPlayerFacts
	return out, nil
}

// excludedFrom lists what the next publish will leave out, and why.
func excludedFrom(ctx context.Context, db *core.DB, projectID int) ([]schemas.ExcludedOut, error) {
	var out []schemas.ExcludedOut

	segments, err := db.Client.Segment.Query().
		Where(segment.ProjectID(projectID), segment.Archived(true)).
		Order(ent.Asc(segment.FieldKey)).All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	for _, row := range segments {
		out = append(out, schemas.ExcludedOut{
			Kind: "segment", Key: row.Key,
			Reason: "archived; a rule still naming it will refuse to compile",
		})
	}

	flags, err := db.Client.FeatureFlag.Query().
		Where(featureflag.ProjectID(projectID), featureflag.Archived(true)).
		Order(ent.Asc(featureflag.FieldKey)).All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	for _, row := range flags {
		out = append(out, schemas.ExcludedOut{
			Kind: "flag", Key: row.Key,
			Reason: "archived; the game will fall back to its own default once this is published",
		})
	}

	events, err := db.Client.LiveOpsEvent.Query().
		Where(liveopsevent.ProjectID(projectID),
			liveopsevent.StatusNEQ(liveopsevent.StatusActive)).
		Order(ent.Asc(liveopsevent.FieldKey)).All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	for _, row := range events {
		out = append(out, schemas.ExcludedOut{
			Kind: "liveops_event", Key: row.Key,
			Reason: string(row.Status) + "; only an active event is published",
		})
	}
	return out, nil
}

// ---- authoring checks -------------------------------------------------------

// authoredRule parses a rule, resolves its segment references and returns both the parsed form
// and what to store.
//
// What is stored is what the studio wrote — references and all — because a segment edited
// tomorrow has to reach every rule that names it. Inlining belongs to the publish; this only
// proves it will work.
//
// `self` is the key of the segment being written, or empty for a flag or an event. Passing it
// puts the candidate into the set it is checked against, which is the only way a rule that
// refers to itself can be caught: the row does not exist yet, or still holds the old rule.
func authoredRule(ctx context.Context, db *core.DB, projectID int, self string,
	raw jsontext.Value,
) (map[string]any, error) {
	rule, err := targeting.Parse(raw)
	if err != nil {
		return nil, muzak.UnprocessableEntity(err.Error()).WithCode("invalid_rule")
	}

	segments, err := projectSegments(ctx, db, projectID)
	if err != nil {
		return nil, err
	}
	if self != "" {
		segments[self] = rule
	}
	if err := segments.Check(rule); err != nil {
		return nil, muzak.UnprocessableEntity(err.Error()).WithCode("unresolvable_rule")
	}

	stored, err := jsonObject(rule)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return stored, nil
}

// authoredVariants checks a flag's ordered rules against its kind and the project's segments.
//
// Every variant is checked, not only the first failure, because a flag has one Save and a
// studio correcting four rules one round trip at a time is a studio that stops using the
// dashboard. The error names the position, one-based, matching what the compiler says.
func authoredVariants(kind liveconfig.Kind, in []schemas.VariantIn,
	segments liveconfig.Segments,
) ([]any, error) {
	rules := make([]any, 0, len(in))
	for i, variant := range in {
		position := "rule " + strconv.Itoa(i+1)

		audience, err := targeting.Parse(variant.Audience)
		if err != nil {
			return nil, muzak.UnprocessableEntity(position + ": " + err.Error()).
				WithCode("invalid_rule")
		}
		if err := segments.Check(audience); err != nil {
			return nil, muzak.UnprocessableEntity(position + ": " + err.Error()).
				WithCode("unresolvable_rule")
		}
		value, err := typedValue(kind, variant.Value, position+" value")
		if err != nil {
			return nil, err
		}

		encoded, err := jsonObject(liveconfig.Variant{
			Audience: audience,
			Value:    value,
			Rollout:  rolloutOrFull(variant.Rollout),
		})
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		rules = append(rules, encoded)
	}
	return rules, nil
}

// typedValue checks one value against a flag's kind and returns it as stored.
//
// This is the check [globaltier.FeatureFlag]'s own comment promises — "a dashboard can refuse
// `true` in a number flag while somebody is typing it, rather than at the point a game reads
// it" — and until these routes existed there was nowhere to make it.
func typedValue(kind liveconfig.Kind, raw jsontext.Value, field string) (any, error) {
	if len(raw) == 0 {
		return nil, muzak.UnprocessableEntity(field + " is required: a flag with no answer makes " +
			"the game's behaviour depend on the network").WithCode("missing_value")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, muzak.UnprocessableEntity(field + " is not valid JSON: " + err.Error()).
			WithCode("invalid_value")
	}
	if err := kind.Accepts(value); err != nil {
		return nil, muzak.UnprocessableEntity(field + ": " + err.Error()).
			WithCode("wrong_flag_kind")
	}
	return value, nil
}

// canonical re-encodes a decoded value, so what is stored is JSON with one spelling.
//
// Storing the bytes as they arrived would mean two flags written with different whitespace, or
// with their object members in a different order, comparing unequal — and the first thing built
// on top of these rows is a diff between a draft and what is published.
func canonical(value any) (jsontext.Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	return encoded, nil
}

// authoredSchedule parses and validates a schedule.
//
// Two errors, told apart because they are different mistakes: a duration that is not one, and a
// schedule whose parts contradict each other. [liveops.Schedule.Validate] owns the second, so
// the rule about weekdays lives with the code that reads them.
func authoredSchedule(in schemas.ScheduleIn) (liveops.Schedule, error) {
	schedule, err := in.Schedule()
	if err != nil {
		return liveops.Schedule{}, muzak.UnprocessableEntity(
			"schedule.duration is not a duration: " + err.Error() +
				` — write it with a unit, such as "2h" or "120h"`).
			WithCode("invalid_duration")
	}
	if err := schedule.Validate(); err != nil {
		return liveops.Schedule{}, muzak.UnprocessableEntity("schedule: " + err.Error()).
			WithCode("invalid_schedule")
	}
	return schedule, nil
}

// projectSegments reads the set a rule is resolved against.
//
// Archived segments are left out, matching [gatherConfig]: a rule naming one must fail here for
// the same reason it fails at the publish, and failing here is the version somebody can act on.
func projectSegments(ctx context.Context, db *core.DB, projectID int) (liveconfig.Segments, error) {
	rows, err := db.Client.Segment.Query().
		Where(segment.ProjectID(projectID), segment.Archived(false)).All(ctx)
	if err != nil {
		return nil, core.AsHTTPError(err)
	}
	out := make(liveconfig.Segments, len(rows))
	for _, row := range rows {
		rule, err := ruleFrom(row.Rule)
		if err != nil {
			// A stored rule that no longer parses, which a write must not be blamed for.
			// It is still fatal to this request, because a set with a hole in it would
			// resolve a reference to "does not exist" and refuse a correct rule.
			return nil, muzak.UnprocessableEntity(
				"segment " + row.Key + " no longer parses and has to be fixed first: " +
					err.Error()).WithCode("invalid_stored_rule")
		}
		out[row.Key] = rule
	}
	return out, nil
}

// ---- lookups and mapping ----------------------------------------------------

func findSegment(ctx *muzak.Context, key string) (*ent.Segment, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := db.Client.Segment.Query().
		Where(segment.ProjectID(proj.ID), segment.Key(key)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no segment " + key)
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

func findFlag(ctx *muzak.Context, key string) (*ent.FeatureFlag, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := db.Client.FeatureFlag.Query().
		Where(featureflag.ProjectID(proj.ID), featureflag.Key(key)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no flag " + key)
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

func findLiveOpsEvent(ctx *muzak.Context, key string) (*ent.LiveOpsEvent, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project

	row, err := db.Client.LiveOpsEvent.Query().
		Where(liveopsevent.ProjectID(proj.ID), liveopsevent.Key(key)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no LiveOps event " + key)
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// asConflict turns the unique index on (project, key) into the message a person needs.
//
// [core.AsHTTPError] would say "that already exists", which in a request carrying a key, a name
// and a rule does not say which of them collided.
func asConflict(err error, what, key string) error {
	if ent.IsConstraintError(err) {
		return muzak.Conflict(what + " with the key " + key + " already exists in this project").
			Wrap(err)
	}
	return core.AsHTTPError(err)
}

// archivedOr leaves the archive state alone unless the caller said otherwise.
//
// The whole-object PUT is a replace, and this is the one field deliberately exempt from that.
// Archiving is a lifecycle gesture with its own route; letting an ordinary edit undo it by
// omission would mean a dashboard that forgot to echo one member could return a flag a studio
// archived to the next published document, which a shipped game then starts reading again.
func archivedOr(chosen *bool, current bool) bool {
	if chosen == nil {
		return current
	}
	return *chosen
}

// rolloutOrFull applies §43's default: absent means the whole audience.
//
// A pointer is what makes this possible. An explicit 0 — live, reaching nobody yet — is a thing
// studios set on purpose before a launch, and a plain int could not tell it from silence.
func rolloutOrFull(v *int) int {
	if v == nil {
		return targeting.Buckets
	}
	return *v
}

// rawOrNull canonicalises an authored JSON value, and turns absence into `null`.
//
// A LiveOps payload is the studio's own shape and PlayerSense has no opinion about it beyond it
// being JSON — but an absent one has to become something storable, and `null` is the honest
// spelling of "they sent none".
func rawOrNull(raw jsontext.Value) (jsontext.Value, error) {
	if len(raw) == 0 {
		return jsontext.Value("null"), nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// jsonObject renders a value as the map ent stores in a JSON column.
func jsonObject(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// rawJSON renders a stored map back onto the wire.
//
// Returned as raw JSON rather than as a map so that a rule comes back out shaped the way
// internal/targeting spells it, with its empty members absent, rather than as whatever ent's
// column round-trip produced.
func rawJSON(value any) (jsontext.Value, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func segmentOut(row *ent.Segment) schemas.SegmentOut {
	out := schemas.SegmentOut{
		ID:          row.UID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		Archived:    row.Archived,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	// A rule that fails to marshal is not a reason to fail the read: the row is what a
	// person needs in order to fix it, and an empty rule member with the key beside it is
	// more use than a 500.
	if raw, err := rawJSON(row.Rule); err == nil {
		out.Rule = raw
	}
	return out
}

func flagOut(row *ent.FeatureFlag) (schemas.FlagOut, error) {
	out := schemas.FlagOut{
		ID:          row.UID,
		Key:         row.Key,
		Name:        row.Name,
		Description: row.Description,
		Kind:        string(row.Kind),
		Fallback:    row.Fallback,
		Rules:       make([]schemas.VariantOut, 0, len(row.Rules)),
		Rollout:     row.Rollout,
		Salt:        row.Salt,
		Archived:    row.Archived,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	for _, raw := range row.Rules {
		variant, err := variantFrom(raw)
		if err != nil {
			return schemas.FlagOut{}, muzak.UnprocessableEntity(
				"flag " + row.Key + " has a stored rule that no longer parses: " + err.Error()).
				WithCode("invalid_stored_rule")
		}
		audience, err := rawJSON(variant.Audience)
		if err != nil {
			return schemas.FlagOut{}, core.AsHTTPError(err)
		}
		value, err := rawJSON(variant.Value)
		if err != nil {
			return schemas.FlagOut{}, core.AsHTTPError(err)
		}
		if variant.Audience.Empty() {
			// An empty rule marshals to `{}`, which reads as "an audience somebody
			// configured and left blank". Absent is what it means.
			audience = nil
		}
		out.Rules = append(out.Rules, schemas.VariantOut{
			Audience: audience, Value: value, Rollout: variant.Rollout,
		})
	}
	return out, nil
}

// liveOpsEventOut renders an event and expands the next few windows its schedule produces.
//
// The expansion is the part worth the cost. "The first five days of every month" is a mode, an
// anchor and a duration, and nobody can read back from those three whether they got it right —
// so the response carries dates, computed by the same code the config path runs, which is what
// makes them the dates a player will see rather than a second opinion about them.
func liveOpsEventOut(row *ent.LiveOpsEvent, now time.Time) (schemas.LiveOpsEventOut, error) {
	out := schemas.LiveOpsEventOut{
		ID:        row.UID,
		Key:       row.Key,
		Kind:      row.Kind,
		Name:      row.Name,
		Status:    string(row.Status),
		Schedule:  schemas.ScheduleFrom(row.Schedule),
		Priority:  row.Priority,
		Payload:   row.Payload,
		Rollout:   row.Rollout,
		Salt:      row.Salt,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	audience, err := ruleFrom(row.Audience)
	if err != nil {
		return schemas.LiveOpsEventOut{}, muzak.UnprocessableEntity(
			"LiveOps event " + row.Key + " has a stored audience that no longer parses: " +
				err.Error()).WithCode("invalid_stored_rule")
	}
	if !audience.Empty() {
		if out.Audience, err = rawJSON(audience); err != nil {
			return schemas.LiveOpsEventOut{}, core.AsHTTPError(err)
		}
	}

	// A window it is inside now is included, because "is it on" is the first thing anybody
	// looks at, and a list that began at the next one would answer a live event with its
	// successor. The horizon is a year, [liveops.MaxOccurrences] caps the expansion at 366
	// however dense the schedule is, and draftOccurrences takes the first few of those — so
	// an hourly event costs one bounded walk rather than a row per hour.
	for _, window := range row.Schedule.Occurrences(now.Add(-row.Schedule.Duration), now.AddDate(1, 0, 0)) {
		if len(out.Occurrences) == draftOccurrences {
			break
		}
		out.Occurrences = append(out.Occurrences,
			schemas.WindowOut{Start: window.Start, End: window.End})
	}
	return out, nil
}

// draftOccurrences is how many windows a response shows.
//
// Enough to see the pattern — a weekly event's next month, a monthly one's next quarter — and
// few enough that the answer stays a glance rather than a scroll.
const draftOccurrences = 5
