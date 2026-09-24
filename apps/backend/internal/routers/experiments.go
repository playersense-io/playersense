package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Experiments are tests on a flag (§43, §44). Reading them is liveops.read and changing them
// liveops.write: starting one changes what the next publish serves, and publishing stays the
// separate permission it is.
func Experiments() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("experiments"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or experiment does not exist"))
	base := "/organizations/{org}/projects/{project}/experiments"
	one := base + "/{experiment}"
	wrongState := muzak.WithResponseDoc(http.StatusConflict, "The experiment is not in a state that allows this; the message says which")

	r.Get(base, handlers.ListExperiments,
		muzak.Summary("List experiments"),
		muzak.Description("Newest first, narrowed by status or flag."),
		core.Require(authz.LiveOpsRead))

	r.Post(base, handlers.CreateExperiment,
		muzak.Summary("Write an experiment"),
		muzak.Description("In draft, on one flag: its arms (each a value of the flag's kind, different "+
			"from the others, with weights adding to 100), the share of the audience let in, and the "+
			"metric it is judged on with any guardrails."),
		core.Require(authz.LiveOpsWrite),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The arms, audience or metrics cannot work; every problem is named"),
		muzak.WithResponseDoc(http.StatusConflict, "The flag is archived"))

	r.Get(one, handlers.ReadExperiment,
		muzak.Summary("One experiment, with its history"),
		core.Require(authz.LiveOpsRead))

	r.Put(one, handlers.UpdateExperiment,
		muzak.Summary("Edit a draft"),
		muzak.Description("A test that has started keeps what it started with."),
		core.Require(authz.LiveOpsWrite), wrongState,
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The arms, audience or metrics cannot work"))

	r.Delete(one, handlers.DeleteExperiment,
		muzak.Summary("Delete a draft"),
		muzak.Description("A test that ran is history and stays."),
		core.Require(authz.LiveOpsWrite), muzak.Status(http.StatusNoContent), wrongState)

	r.Post(one+"/start", handlers.StartExperiment,
		muzak.Summary("Start"),
		muzak.Description("From the next publish, the flag splits its audience between the arms. One "+
			"experiment runs on a flag at a time."),
		core.Require(authz.LiveOpsWrite), wrongState)

	r.Put(one+"/traffic", handlers.SetExperimentTraffic,
		muzak.Summary("Let more or fewer of the audience in"),
		muzak.Description("Raising it keeps everybody already in, in the same arm."),
		core.Require(authz.LiveOpsWrite), wrongState)

	r.Post(one+"/stop", handlers.StopExperiment,
		muzak.Summary("Stop early"),
		muzak.Description("With why. The next publish serves the flag's own rules again."),
		core.Require(authz.LiveOpsWrite), wrongState)

	r.Post(one+"/conclude", handlers.ConcludeExperiment,
		muzak.Summary("Decide"),
		muzak.Description("A winning arm, or none; from running or stopped."),
		core.Require(authz.LiveOpsWrite), wrongState,
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "The winner is not one of its arms"))

	r.Post(one+"/ship", handlers.ShipExperiment,
		muzak.Summary("Ship the winner"),
		muzak.Description("The flag's fallback becomes the winning value, and rules serving another "+
			"arm's value to part of an audience go. A write to the draft, published like any other."),
		core.Require(authz.LiveOpsWrite), wrongState)

	return r
}
