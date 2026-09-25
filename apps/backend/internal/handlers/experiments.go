package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/experiment"
	"playersense.dev/backend/internal/audit"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/liveconfig"
	"playersense.dev/backend/internal/query"
	"playersense.dev/backend/internal/schemas"
	"playersense.dev/backend/internal/targeting"
)

// Experiments (§43, §44): a test on one flag. While one runs, every publish compiles it into its
// flag as a split (gatherConfig); stopping or concluding it takes the split out of the next
// publish. Nothing here publishes: a change reaches players when somebody presses publish, as
// every other change to the draft does.

var armID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// arm is one variant as stored.
type arm struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Value   any    `json:"value"`
	Weight  int    `json:"weight"`
	Control bool   `json:"control,omitempty"`
}

func armsOf(row *ent.Experiment) ([]arm, error) {
	encoded, err := json.Marshal(row.Variants)
	if err != nil {
		return nil, err
	}
	var out []arm
	return out, json.Unmarshal(encoded, &out)
}

// authoredArms checks the arms against the flag's kind and each other: ids that are short and
// distinct, values of the flag's kind that differ from each other, and weights that divide the
// test whole. Every problem is named, not only the first.
func authoredArms(kind liveconfig.Kind, in []schemas.ExperimentArmIn) ([]map[string]any, error) {
	var problems []string
	ids, values := map[string]bool{}, map[string]string{}
	total, controls := 0, 0
	out := make([]map[string]any, 0, len(in))
	for i, a := range in {
		position := "arm " + strconv.Itoa(i+1)
		id := strings.TrimSpace(a.ID)
		switch {
		case !armID.MatchString(id):
			problems = append(problems, position+": the id is 1 to 32 lower-case letters, digits, - or _")
		case ids[id]:
			problems = append(problems, position+": the id "+id+" is used twice")
		}
		ids[id] = true
		if a.Weight < 1 || a.Weight > 99 {
			problems = append(problems, position+": a weight is 1 to 99 percent")
		}
		total += a.Weight
		if a.Control {
			controls++
		}
		var value any
		if err := json.Unmarshal(a.Value, &value); err != nil || len(a.Value) == 0 {
			problems = append(problems, position+": the value is required, as JSON")
			continue
		}
		if err := kind.Accepts(value); err != nil {
			problems = append(problems, position+" value: "+err.Error())
			continue
		}
		text, _ := canonical(value)
		if other, seen := values[string(text)]; seen {
			problems = append(problems, position+": serves the same value as "+other+
				", so the two could not be told apart")
		}
		values[string(text)] = position
		out = append(out, map[string]any{
			"id": id, "name": strings.TrimSpace(a.Name), "value": value, "weight": a.Weight, "control": a.Control,
		})
	}
	if total != 100 {
		problems = append(problems, fmt.Sprintf("the arms' weights add up to %d percent, not 100", total))
	}
	if controls > 1 {
		problems = append(problems, "only one arm can be the control")
	}
	if len(problems) > 0 {
		return nil, muzak.UnprocessableEntity(strings.Join(problems, "; ")).WithCode("invalid_arms")
	}
	return out, nil
}

func experimentRecordOut(row *ent.Experiment) (schemas.ExperimentRecordOut, error) {
	out := schemas.ExperimentRecordOut{
		ID: row.UID, Name: row.Name, Hypothesis: row.Hypothesis, Flag: row.FlagKey,
		AudienceLabel: row.AudienceLabel, Traffic: row.Traffic, Status: string(row.Status),
		PrimaryMetric: row.PrimaryMetric, Guardrails: row.Guardrails, PlannedDays: row.PlannedDays,
		CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, StartedAt: row.StartedAt, EndedAt: row.EndedAt,
		Winner: row.Winner, Shipped: row.Shipped, Conclusion: row.Conclusion,
		Arms: []schemas.ExperimentArmOut{}, Timeline: []schemas.TimelineEntryOut{},
	}
	if out.Guardrails == nil {
		out.Guardrails = []string{}
	}
	if len(row.Audience) > 0 {
		audience, err := json.Marshal(row.Audience)
		if err != nil {
			return out, err
		}
		out.Audience = audience
	}
	arms, err := armsOf(row)
	if err != nil {
		return out, err
	}
	for _, a := range arms {
		value, err := canonical(a.Value)
		if err != nil {
			return out, err
		}
		out.Arms = append(out.Arms, schemas.ExperimentArmOut{
			ID: a.ID, Name: a.Name, Value: value, Weight: a.Weight, Control: a.Control,
		})
	}
	for _, entry := range row.Timeline {
		at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(entry["at"]))
		out.Timeline = append(out.Timeline, schemas.TimelineEntryOut{
			At: at, Actor: fmt.Sprint(entry["actor"]), Text: fmt.Sprint(entry["text"]),
		})
	}
	return out, nil
}

// line is one entry for an experiment's timeline, by whoever is asking.
func line(ctx *muzak.Context, text string) map[string]any {
	actor := actorOf(ctx).Label
	return map[string]any{"at": nowUTC().Format(time.RFC3339Nano), "actor": actor, "text": text}
}

func findExperiment(ctx *muzak.Context, tx *ent.Tx, id schemas.ExperimentIDParams) (*ent.Experiment, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := tx.Experiment.Query().
		Where(experiment.UID(id.ExperimentID), experiment.ProjectID(proj.ID)).ForUpdate().Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, muzak.NotFound("this project has no such experiment")
		}
		return nil, core.AsHTTPError(err)
	}
	return row, nil
}

// ListExperiments is a project's experiments, newest first.
func ListExperiments(ctx *muzak.Context, in schemas.ExperimentListParams) (schemas.ExperimentRecordListOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	q := db.Client.Experiment.Query().Where(experiment.ProjectID(proj.ID))
	if in.Status != "" {
		q = q.Where(experiment.StatusEQ(experiment.Status(in.Status)))
	}
	if in.Flag != "" {
		q = q.Where(experiment.FlagKey(in.Flag))
	}
	rows, err := q.Order(ent.Desc(experiment.FieldID)).Limit(500).All(ctx.Context())
	if err != nil {
		return schemas.ExperimentRecordListOut{}, core.AsHTTPError(err)
	}
	out := schemas.ExperimentRecordListOut{Experiments: make([]schemas.ExperimentRecordOut, 0, len(rows))}
	for _, row := range rows {
		one, err := experimentRecordOut(row)
		if err != nil {
			return out, core.AsHTTPError(err)
		}
		out.Experiments = append(out.Experiments, one)
	}
	return out, nil
}

// ReadExperiment is one experiment.
func ReadExperiment(ctx *muzak.Context, in schemas.ExperimentIDParams) (schemas.ExperimentRecordOut, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := db.Client.Experiment.Query().
		Where(experiment.UID(in.ExperimentID), experiment.ProjectID(proj.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.ExperimentRecordOut{}, muzak.NotFound("this project has no such experiment")
		}
		return schemas.ExperimentRecordOut{}, core.AsHTTPError(err)
	}
	out, err := experimentRecordOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// authored checks an experiment's editable parts against its flag and the project's segments.
func authored(ctx *muzak.Context, flagKey string, in schemas.ExperimentIn) (map[string]any, []map[string]any, error) {
	db := muzak.From[*core.DB](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	flag, err := findFlag(ctx, flagKey)
	if err != nil {
		return nil, nil, err
	}
	if flag.Archived {
		return nil, nil, muzak.Conflict("the flag " + flagKey + " is archived").WithCode("flag_archived")
	}
	arms, err := authoredArms(liveconfig.Kind(flag.Kind), in.Arms)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range in.Guardrails {
		if !slices.Contains(schemas.ExperimentMetrics, g) {
			return nil, nil, muzak.UnprocessableEntity("guardrail " + g + " is not a metric an experiment measures: " +
				strings.Join(schemas.ExperimentMetrics, ", ")).WithCode("unknown_metric")
		}
	}
	audience := map[string]any{}
	if len(in.Audience) > 0 && string(in.Audience) != "null" {
		if audience, err = authoredRule(ctx.Context(), db, proj.ID, "", in.Audience); err != nil {
			return nil, nil, err
		}
	}
	return audience, arms, nil
}

// CreateExperiment writes a new experiment, in draft.
func CreateExperiment(ctx *muzak.Context, in schemas.CreateExperimentIn) (schemas.ExperimentRecordOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	audience, arms, err := authored(ctx, in.FlagKey, in.ExperimentIn)
	if err != nil {
		return schemas.ExperimentRecordOut{}, err
	}
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.Experiment, error) {
		row, err := tx.Experiment.Create().
			SetProjectID(proj.ID).SetFlagKey(in.FlagKey).SetName(in.Name).SetHypothesis(in.Hypothesis).
			SetAudience(audience).SetAudienceLabel(in.AudienceLabel).SetTraffic(intOr(in.Traffic, 100)).
			SetPrimaryMetric(in.PrimaryMetric).SetGuardrails(nonNil(in.Guardrails)).SetVariants(arms).
			SetPlannedDays(intOr(in.PlannedDays, 14)).SetCreatedBy(actorOf(ctx).Label).
			SetTimeline([]map[string]any{line(ctx, "Created")}).
			Save(ctx.Context())
		if err != nil {
			return nil, core.AsHTTPError(err)
		}
		return row, auditFor(ctx).record(ctx.Context(), tx, audit.ExperimentWritten, "experiment", row.UID.String(),
			map[string]any{"project": proj.Slug, "flag": in.FlagKey, "name": in.Name, "created": true})
	})
	if err != nil {
		return schemas.ExperimentRecordOut{}, err
	}
	ctx.SetStatus(201)
	out, err := experimentRecordOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

// UpdateExperiment replaces a draft's editable parts. A test that has started keeps what it
// started with: changing its arms would compare players who were in two different tests.
func UpdateExperiment(ctx *muzak.Context, in schemas.UpdateExperimentIn) (schemas.ExperimentRecordOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	return transition(ctx, in.ExperimentIDParams, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusDraft {
			return nil, "", "", nil, muzak.Conflict("only a draft can be edited; this one is " + string(row.Status)).
				WithCode("not_a_draft")
		}
		audience, arms, err := authored(ctx, row.FlagKey, in.ExperimentIn)
		if err != nil {
			return nil, "", "", nil, err
		}
		update := tx.Experiment.UpdateOne(row).SetName(in.Name).SetHypothesis(in.Hypothesis).
			SetAudience(audience).SetAudienceLabel(in.AudienceLabel).SetTraffic(intOr(in.Traffic, 100)).
			SetPrimaryMetric(in.PrimaryMetric).SetGuardrails(nonNil(in.Guardrails)).SetVariants(arms).
			SetPlannedDays(intOr(in.PlannedDays, 14))
		return update, "Edited", audit.ExperimentWritten,
			map[string]any{"project": proj.Slug, "flag": row.FlagKey, "created": false}, nil
	})
}

// DeleteExperiment removes a draft. A test that ran is history and stays.
func DeleteExperiment(ctx *muzak.Context, in schemas.ExperimentIDParams) (muzak.Empty, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	_, err := inTx(ctx, func(tx *ent.Tx) (struct{}, error) {
		row, err := findExperiment(ctx, tx, in)
		if err != nil {
			return struct{}{}, err
		}
		if row.Status != experiment.StatusDraft {
			return struct{}{}, muzak.Conflict("only a draft can be deleted; a test that ran is history").
				WithCode("not_a_draft")
		}
		if err := tx.Experiment.DeleteOne(row).Exec(ctx.Context()); err != nil {
			return struct{}{}, core.AsHTTPError(err)
		}
		return struct{}{}, auditFor(ctx).record(ctx.Context(), tx, audit.ExperimentDeleted, "experiment",
			row.UID.String(), map[string]any{"project": proj.Slug, "flag": row.FlagKey, "name": row.Name})
	})
	if err != nil {
		return muzak.Empty{}, err
	}
	ctx.SetStatus(204)
	return muzak.Empty{}, nil
}

// StartExperiment puts a draft into every publish from now on. One experiment runs on a flag at
// a time.
func StartExperiment(ctx *muzak.Context, in schemas.ExperimentIDParams) (schemas.ExperimentRecordOut, error) {
	return transition(ctx, in, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusDraft {
			return nil, "", "", nil, muzak.Conflict("only a draft can start; this one is " + string(row.Status)).
				WithCode("not_a_draft")
		}
		arms, err := armsOf(row)
		if err != nil {
			return nil, "", "", nil, core.AsHTTPError(err)
		}
		split := make([]string, len(arms))
		for i, a := range arms {
			split[i] = fmt.Sprintf("%d%% %s", a.Weight, armTitle(a))
		}
		text := fmt.Sprintf("Started with %d%% of the audience: %s. Players are split from the next publish",
			row.Traffic, strings.Join(split, ", "))
		return tx.Experiment.UpdateOne(row).SetStatus(experiment.StatusRunning).SetStartedAt(nowUTC()),
			text, audit.ExperimentStarted, map[string]any{"flag": row.FlagKey, "traffic": row.Traffic}, nil
	})
}

// SetExperimentTraffic lets more or fewer of the audience into a running test. Raising it keeps
// everybody already in, in the same arm (liveconfig.Split).
func SetExperimentTraffic(ctx *muzak.Context, in schemas.SetTrafficIn) (schemas.ExperimentRecordOut, error) {
	return transition(ctx, in.ExperimentIDParams, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusRunning {
			return nil, "", "", nil, muzak.Conflict("only a running test takes traffic; this one is " + string(row.Status)).
				WithCode("not_running")
		}
		if row.Traffic == in.Traffic {
			return nil, "", "", nil, muzak.Conflict("the test already has " + strconv.Itoa(in.Traffic) + "% of the audience").
				WithCode("unchanged")
		}
		text := fmt.Sprintf("Traffic %d%% → %d%% of the audience", row.Traffic, in.Traffic)
		return tx.Experiment.UpdateOne(row).SetTraffic(in.Traffic), text, audit.ExperimentTraffic,
			map[string]any{"flag": row.FlagKey, "traffic": in.Traffic, "was_traffic": row.Traffic}, nil
	})
}

// StopExperiment ends a running test early, with why.
func StopExperiment(ctx *muzak.Context, in schemas.StopExperimentIn) (schemas.ExperimentRecordOut, error) {
	return transition(ctx, in.ExperimentIDParams, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusRunning {
			return nil, "", "", nil, muzak.Conflict("only a running test can be stopped; this one is " + string(row.Status)).
				WithCode("not_running")
		}
		return tx.Experiment.UpdateOne(row).SetStatus(experiment.StatusStopped).SetEndedAt(nowUTC()).
				ClearWinner().SetConclusion(in.Reason),
			"Stopped early: " + in.Reason, audit.ExperimentStopped,
			map[string]any{"flag": row.FlagKey, "reason": in.Reason}, nil
	})
}

// ConcludeExperiment ends a test with a decision: a winning arm, or none.
func ConcludeExperiment(ctx *muzak.Context, in schemas.ConcludeExperimentIn) (schemas.ExperimentRecordOut, error) {
	return transition(ctx, in.ExperimentIDParams, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusRunning && row.Status != experiment.StatusStopped {
			return nil, "", "", nil, muzak.Conflict("only a test that ran can be concluded; this one is " + string(row.Status)).
				WithCode("not_run")
		}
		arms, err := armsOf(row)
		if err != nil {
			return nil, "", "", nil, core.AsHTTPError(err)
		}
		update := tx.Experiment.UpdateOne(row).SetStatus(experiment.StatusConcluded).SetConclusion(in.Conclusion)
		if row.EndedAt == nil {
			update = update.SetEndedAt(nowUTC())
		}
		text := "Finished: no clear winner"
		if in.Winner != nil && *in.Winner != "" {
			at := slices.IndexFunc(arms, func(a arm) bool { return a.ID == *in.Winner })
			if at < 0 {
				return nil, "", "", nil, muzak.UnprocessableEntity("the experiment has no arm " + *in.Winner).
					WithCode("unknown_arm")
			}
			update = update.SetWinner(*in.Winner)
			text = "Finished: " + armTitle(arms[at]) + " won"
		} else {
			update = update.ClearWinner()
		}
		return update, text, audit.ExperimentConcluded,
			map[string]any{"flag": row.FlagKey, "winner": in.Winner, "conclusion": in.Conclusion}, nil
	})
}

// ShipExperiment makes the winning arm's value the flag's answer for everybody: its fallback
// becomes that value, and rules serving another arm's value to part of an audience go. A write
// to the draft, published like any other, and in the audit log as the flag's as well.
func ShipExperiment(ctx *muzak.Context, in schemas.ExperimentIDParams) (schemas.ExperimentRecordOut, error) {
	proj := muzak.From[*core.Principal](ctx).Project
	return transition(ctx, in, func(tx *ent.Tx, row *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error) {
		if row.Status != experiment.StatusConcluded || row.Winner == nil {
			return nil, "", "", nil, muzak.Conflict("there is no winner to ship; conclude the test with one first").
				WithCode("no_winner")
		}
		if row.Shipped {
			return nil, "", "", nil, muzak.Conflict("the winner is already shipped").WithCode("already_shipped")
		}
		arms, err := armsOf(row)
		if err != nil {
			return nil, "", "", nil, core.AsHTTPError(err)
		}
		winner := arms[slices.IndexFunc(arms, func(a arm) bool { return a.ID == *row.Winner })]
		flag, err := findFlag(ctx, row.FlagKey)
		if err != nil {
			return nil, "", "", nil, err
		}
		armValues := map[string]bool{}
		for _, a := range arms {
			text, _ := canonical(a.Value)
			armValues[string(text)] = true
		}
		var rules []any
		dropped := 0
		for _, raw := range flag.Rules {
			variant, err := variantFrom(raw)
			if err != nil {
				return nil, "", "", nil, core.AsHTTPError(err)
			}
			text, _ := canonical(variant.Value)
			if armValues[string(text)] && variant.Rollout < targeting.Buckets {
				dropped++
				continue
			}
			rules = append(rules, raw)
		}
		fallback, err := canonical(winner.Value)
		if err != nil {
			return nil, "", "", nil, err
		}
		if rules == nil {
			rules = []any{}
		}
		if err := tx.FeatureFlag.UpdateOneID(flag.ID).SetFallback(fallback).SetRules(rules).
			Exec(ctx.Context()); err != nil {
			return nil, "", "", nil, core.AsHTTPError(err)
		}
		if err := auditFor(ctx).record(ctx.Context(), tx, audit.FlagWritten, "flag", flag.Key, map[string]any{
			"project": proj.Slug, "created": false, "experiment": row.UID.String(), "rules_removed": dropped,
		}); err != nil {
			return nil, "", "", nil, err
		}
		text := "Shipped " + armTitle(winner) + ": " + row.FlagKey + " now serves " + string(fallback) +
			" to everybody from the next publish"
		return tx.Experiment.UpdateOne(row).SetShipped(true), text, audit.ExperimentShipped,
			map[string]any{"flag": row.FlagKey, "winner": winner.ID, "rules_removed": dropped}, nil
	})
}

// transition runs one change to an experiment in a transaction holding its row: the change, a
// line on its timeline, and an audit entry.
func transition(ctx *muzak.Context, id schemas.ExperimentIDParams,
	change func(*ent.Tx, *ent.Experiment) (*ent.ExperimentUpdateOne, string, audit.Action, map[string]any, error),
) (schemas.ExperimentRecordOut, error) {
	row, err := inTx(ctx, func(tx *ent.Tx) (*ent.Experiment, error) {
		row, err := findExperiment(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		update, text, action, meta, err := change(tx, row)
		if err != nil {
			return nil, err
		}
		updated, err := update.SetTimeline(append(row.Timeline, line(ctx, text))).Save(ctx.Context())
		if err != nil {
			if ent.IsConstraintError(err) {
				return nil, muzak.Conflict("another experiment is already running on the flag " + row.FlagKey).
					WithCode("flag_busy")
			}
			return nil, core.AsHTTPError(err)
		}
		if err := auditFor(ctx).record(ctx.Context(), tx, action, "experiment", row.UID.String(), meta); err != nil {
			return nil, err
		}
		switch action {
		case audit.ExperimentStarted:
			return updated, emit(ctx, tx, "", "experiment.started", map[string]any{"experiment": experimentEvent(updated)})
		case audit.ExperimentConcluded:
			return updated, emit(ctx, tx, "", "experiment.concluded", map[string]any{"experiment": experimentEvent(updated)})
		}
		return updated, nil
	})
	if err != nil {
		return schemas.ExperimentRecordOut{}, err
	}
	out, err := experimentRecordOut(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	return out, nil
}

func armTitle(a arm) string {
	if a.Name != "" {
		return a.Name
	}
	return a.ID
}

func intOr(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	return *v
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// runningSplits is every running experiment of a project, as the split each compiles to, by flag.
func runningSplits(ctx context.Context, db *core.DB, projectID int) (map[string]*liveconfig.Split, error) {
	rows, err := db.Client.Experiment.Query().
		Where(experiment.ProjectID(projectID), experiment.StatusEQ(experiment.StatusRunning)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*liveconfig.Split, len(rows))
	for _, row := range rows {
		audience, err := ruleFrom(row.Audience)
		if err != nil {
			return nil, fmt.Errorf("experiment %q audience: %w", row.Name, err)
		}
		arms, err := armsOf(row)
		if err != nil {
			return nil, err
		}
		split := &liveconfig.Split{Experiment: row.UID.String(), Audience: audience,
			Traffic: row.Traffic * targeting.Buckets / 100}
		for _, a := range arms {
			split.Arms = append(split.Arms, liveconfig.Arm{ID: a.ID, Value: a.Value, Weight: a.Weight * targeting.Buckets / 100})
		}
		out[row.FlagKey] = split
	}
	return out, nil
}

// ExperimentResults is an experiment's arms measured over the days it ran, each of the flag's
// measured variants matched to the arm it is: by id, which an SDK reports when config-api told it
// the arm, or by value, from an SDK that predates experiments.
func ExperimentResults(ctx *muzak.Context, in schemas.ExperimentResultsParams) (schemas.ExperimentResultsOut, error) {
	db := muzak.From[*core.DB](ctx)
	engine := muzak.From[*query.Engine](ctx)
	proj := muzak.From[*core.Principal](ctx).Project
	row, err := db.Client.Experiment.Query().
		Where(experiment.UID(in.ExperimentID), experiment.ProjectID(proj.ID)).Only(ctx.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return schemas.ExperimentResultsOut{}, muzak.NotFound("this project has no such experiment")
		}
		return schemas.ExperimentResultsOut{}, core.AsHTTPError(err)
	}
	record, err := experimentRecordOut(row)
	if err != nil {
		return schemas.ExperimentResultsOut{}, core.AsHTTPError(err)
	}
	out := schemas.ExperimentResultsOut{Experiment: record}
	if row.StartedAt == nil {
		return out, nil
	}

	scope, err := scopeForMember(ctx, in.Environment)
	if err != nil {
		return out, err
	}
	day := func(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }
	from, to := day(*row.StartedAt), day(nowUTC())
	if row.EndedAt != nil {
		to = day(*row.EndedAt)
	}
	if limit := from.AddDate(0, 0, schemas.MaxExplorerDays-1); to.After(limit) {
		to = limit
	}
	measured, err := engine.Experiment(ctx.Context(), scope, row.FlagKey, query.NewWindow(from, to))
	switch {
	case errors.Is(err, query.ErrNotFound):
		// Started, and nobody exposed yet: an empty result rather than a missing experiment.
		out.Result = &schemas.ExperimentOut{Flag: row.FlagKey, From: from, To: to,
			Variants: []schemas.ExperimentVariantOut{}, Daily: []schemas.ExperimentDayOut{}}
		return out, nil
	case err != nil:
		return out, exploreFailed(err, "experiment")
	}

	arms, err := armsOf(row)
	if err != nil {
		return out, core.AsHTTPError(err)
	}
	armOf := map[string]string{}
	for _, a := range arms {
		armOf[a.ID] = a.ID
		armOf[valueText(a.Value)] = a.ID
	}
	variants := []schemas.ExperimentVariantOut{}
	for _, v := range measured.Variants {
		id, found := armOf[v.Variant]
		if !found {
			out.OutsidePlayers += v.Players
			continue
		}
		v.Variant = id
		variants = append(variants, v)
	}
	measured.Variants = variants
	daily := []schemas.ExperimentDayOut{}
	for _, d := range measured.Daily {
		if id, found := armOf[d.Variant]; found {
			d.Variant = id
			daily = append(daily, d)
		}
	}
	measured.Daily = daily
	out.Result = measured
	return out, nil
}

// valueText is a value as flag_assignments spells it (00019): a string as itself, a boolean as
// true or false, a number as ClickHouse's toString writes it, anything else as JSON.
func valueText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
}

// experimentEvent is an experiment as its webhook events carry it.
func experimentEvent(row *ent.Experiment) map[string]any {
	out := map[string]any{"id": row.UID, "name": row.Name, "flag": row.FlagKey, "traffic": row.Traffic,
		"status": string(row.Status), "started_at": row.StartedAt, "ended_at": row.EndedAt}
	if arms, err := armsOf(row); err == nil {
		ids := make([]string, len(arms))
		for i, a := range arms {
			ids[i] = a.ID
		}
		out["arms"] = ids
	}
	if row.Winner != nil {
		out["winner"] = *row.Winner
	}
	if row.Conclusion != "" {
		out["conclusion"] = row.Conclusion
	}
	return out
}
