package schemas

import (
	"encoding/json/jsontext"
	"time"

	"github.com/google/uuid"
	muzak "muzak.dev/framework"
)

// Experiments (§43, §44): a test on one flag, its arms, and what was decided.

// ExperimentMetrics is what an experiment can be judged on: what experiment_daily measures.
var ExperimentMetrics = []string{
	"revenue_per_player", "payer_conversion", "d1_retention", "d7_retention",
	"sessions_per_player", "playtime_minutes",
}

// ExperimentArmIn is one arm as written.
type ExperimentArmIn struct {
	ID      string         `json:"id" doc:"Short and stable: a, b, control. What the SDK reports on the exposure"`
	Name    string         `json:"name,omitzero"`
	Value   jsontext.Value `json:"value" doc:"What the flag serves this arm; must match the flag's kind, and differ from every other arm's"`
	Weight  int            `json:"weight" doc:"Percent of the test's players; the arms add up to 100"`
	Control bool           `json:"control,omitzero"`
}

// ExperimentIn is an experiment's editable parts, for create and for update while it is a draft.
type ExperimentIn struct {
	Name          string            `json:"name"`
	Hypothesis    string            `json:"hypothesis,omitzero" doc:"What the team expects: the thing the result answers"`
	Audience      jsontext.Value    `json:"audience,omitzero" doc:"A targeting rule; absent is everybody the flag reaches"`
	AudienceLabel string            `json:"audience_label,omitzero" doc:"The audience in words: New players on Android"`
	Traffic       *int              `json:"traffic,omitzero" doc:"Percent of the audience let in, 1 to 100; defaults to 100"`
	PrimaryMetric string            `json:"primary_metric"`
	Guardrails    []string          `json:"guardrails,omitzero" doc:"Metrics that must not get worse"`
	Arms          []ExperimentArmIn `json:"arms" doc:"Two to eight"`
	PlannedDays   *int              `json:"planned_days,omitzero" doc:"Defaults to 14"`
}

func (in *ExperimentIn) validate(v *muzak.Validation) {
	v.String(&in.Name).Trim().Required().MaxLen(120)
	v.String(&in.Hypothesis).Trim().MaxLen(1000)
	v.String(&in.AudienceLabel).Trim().MaxLen(200)
	v.String(&in.PrimaryMetric).Trim().Lower().Required().OneOf(ExperimentMetrics...)
	v.Slice(&in.Guardrails).MaxItems(len(ExperimentMetrics))
	v.Slice(&in.Arms).Required().MinItems(2).MaxItems(8)
	if in.Traffic != nil {
		v.Number(in.Traffic).Min(1).Max(100)
	}
	if in.PlannedDays != nil {
		v.Number(in.PlannedDays).Min(1).Max(365)
	}
}

// CreateExperimentIn is a new experiment, in draft.
type CreateExperimentIn struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	FlagKey     string `json:"flag" doc:"The flag it runs on. Cannot be changed afterwards"`
	ExperimentIn
}

func (in *CreateExperimentIn) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.FlagKey).Trim().Required().MaxLen(64)
	in.ExperimentIn.validate(v)
}

// ExperimentIDParams names one experiment.
type ExperimentIDParams struct {
	OrgSlug      string    `path:"org"`
	ProjectSlug  string    `path:"project"`
	ExperimentID uuid.UUID `path:"experiment"`
}

func (in *ExperimentIDParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
}

// UpdateExperimentIn replaces a draft's editable parts.
type UpdateExperimentIn struct {
	ExperimentIDParams
	ExperimentIn
}

func (in *UpdateExperimentIn) Validate(v *muzak.Validation) {
	in.ExperimentIDParams.Validate(v)
	in.ExperimentIn.validate(v)
}

// ExperimentListParams lists a project's experiments.
type ExperimentListParams struct {
	OrgSlug     string `path:"org"`
	ProjectSlug string `path:"project"`
	Status      string `query:"status,omitzero" doc:"draft, running, concluded or stopped"`
	Flag        string `query:"flag,omitzero" doc:"Only those on this flag"`
}

func (in *ExperimentListParams) Validate(v *muzak.Validation) {
	v.String(&in.OrgSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.ProjectSlug).Trim().Lower().Required().MaxLen(64)
	v.String(&in.Status).Trim().Lower().OneOf("", "draft", "running", "concluded", "stopped")
	v.String(&in.Flag).Trim().MaxLen(64)
}

// SetTrafficIn lets more or fewer of the audience into a running test.
type SetTrafficIn struct {
	ExperimentIDParams
	Traffic int `json:"traffic" doc:"Percent of the audience, 1 to 100"`
}

func (in *SetTrafficIn) Validate(v *muzak.Validation) {
	in.ExperimentIDParams.Validate(v)
	v.Number(&in.Traffic).Min(1).Max(100)
}

// StopExperimentIn ends a test early.
type StopExperimentIn struct {
	ExperimentIDParams
	Reason string `json:"reason" doc:"Why, for whoever reads it next"`
}

func (in *StopExperimentIn) Validate(v *muzak.Validation) {
	in.ExperimentIDParams.Validate(v)
	v.String(&in.Reason).Trim().Required().MaxLen(2000)
}

// ConcludeExperimentIn ends a test with a decision.
type ConcludeExperimentIn struct {
	ExperimentIDParams
	Winner     *string `json:"winner,omitzero" doc:"The winning arm's id; absent for no clear winner"`
	Conclusion string  `json:"conclusion,omitzero" doc:"What was decided, and why"`
}

func (in *ConcludeExperimentIn) Validate(v *muzak.Validation) {
	in.ExperimentIDParams.Validate(v)
	v.String(&in.Conclusion).Trim().MaxLen(2000)
}

// ExperimentRecordListOut is a project's experiments, newest first.
type ExperimentRecordListOut struct {
	Experiments []ExperimentRecordOut `json:"experiments"`
}

// ExperimentRecordOut is one experiment. (ExperimentOut is the other thing: a flag's arms measured.)
type ExperimentRecordOut struct {
	ID            uuid.UUID          `json:"id"`
	Name          string             `json:"name"`
	Hypothesis    string             `json:"hypothesis,omitzero"`
	Flag          string             `json:"flag"`
	Audience      jsontext.Value     `json:"audience,omitzero"`
	AudienceLabel string             `json:"audience_label,omitzero"`
	Traffic       int                `json:"traffic"`
	Status        string             `json:"status" doc:"draft, running, concluded or stopped"`
	PrimaryMetric string             `json:"primary_metric"`
	Guardrails    []string           `json:"guardrails"`
	Arms          []ExperimentArmOut `json:"arms"`
	PlannedDays   int                `json:"planned_days"`
	CreatedAt     time.Time          `json:"created_at"`
	CreatedBy     string             `json:"created_by,omitzero"`
	StartedAt     *time.Time         `json:"started_at,omitzero"`
	EndedAt       *time.Time         `json:"ended_at,omitzero"`
	Winner        *string            `json:"winner,omitzero" doc:"The arm that won; absent before a decision and for no clear winner"`
	Shipped       bool               `json:"shipped"`
	Conclusion    string             `json:"conclusion,omitzero"`
	Timeline      []TimelineEntryOut `json:"timeline"`
}

// ExperimentArmOut is one arm.
type ExperimentArmOut struct {
	ID      string         `json:"id"`
	Name    string         `json:"name,omitzero"`
	Value   jsontext.Value `json:"value"`
	Weight  int            `json:"weight"`
	Control bool           `json:"control,omitzero"`
}

// TimelineEntryOut is one decision in an experiment's history.
type TimelineEntryOut struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"`
	Text  string    `json:"text"`
}

// ExperimentResultsParams reads an experiment's result in one environment.
type ExperimentResultsParams struct {
	AnalyticsScope
	ExperimentID uuid.UUID `path:"experiment"`
}

func (in *ExperimentResultsParams) Validate(v *muzak.Validation) {
	in.validateScope(v)
}

// ExperimentResultsOut is an experiment's arms measured over the days it ran.
//
// Each of the flag's measured variants is matched to the arm it is: by the arm's id, which is what
// an SDK reports when config-api told it the arm, or by the arm's value from an SDK that predates
// experiments. Variants that are no arm's (players the test did not take in) are left out and
// counted in outside_players.
type ExperimentResultsOut struct {
	Experiment     ExperimentRecordOut `json:"experiment"`
	Result         *ExperimentOut      `json:"result,omitzero" doc:"Absent before it starts; variants are named by arm id"`
	OutsidePlayers uint64              `json:"outside_players" doc:"Players exposed to the flag in the window who were in no arm"`
}
