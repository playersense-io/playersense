package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Authoring returns the routes for what a studio edits (§41).
//
// Three resources and one read, mounted beside [LiveConfig]'s publish. The split between them is
// the permission: everything here is `liveops.read` or `liveops.write`, and the one irreversible
// route is `liveops.publish` next door. That is what lets DEVELOPER draft a rollout and hold no
// power to ship it.
//
// # DELETE archives
//
// Every one of these tables keeps its rows. A published version holds each rule *inlined*, so a
// deleted row would leave a running configuration nobody can explain — and for a flag it is
// worse than that: a key that vanishes from the next document is a key whose SDK falls back, and
// the fallback is the value a studio chose for an offline device rather than for this. So DELETE
// sets `archived`, exactly as it revokes rather than deletes an API key, and the next publish
// stops carrying the row.
//
// # No rate limit beyond the application's own
//
// This is a dashboard surface: authenticated, low-volume, used by people. docs/rate-limits.md
// says why there is nothing narrower here — a limit on it has one observable effect, which is
// refusing a customer who did nothing wrong.
func Authoring() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("liveops"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization or project does not exist"))

	const (
		segments = "/organizations/{org}/projects/{project}/segments"
		flags    = "/organizations/{org}/projects/{project}/flags"
		events   = "/organizations/{org}/projects/{project}/liveops-events"
	)

	// ---- segments -----------------------------------------------------------

	r.Get(segments, handlers.ListSegments,
		muzak.Summary("List a project's segments"),
		muzak.Description("Named audiences, archived ones included: an archived segment is "+
			"still inlined in every published version that used it, so a dashboard "+
			"explaining one has to be able to name it."),
		core.Require(authz.LiveOpsRead))

	r.Post(segments, handlers.CreateSegment,
		muzak.Summary("Name an audience"),
		muzak.Description("The rule is parsed and every segment it refers to is resolved "+
			"here, not at the publish: a rule that cannot compile is refused while the "+
			"person who wrote it is still looking at it."),
		core.Require(authz.LiveOpsWrite),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "A segment with that key already exists"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The rule is malformed, names a segment that does not exist, or refers to itself"))

	r.Get(segments+"/{key}", handlers.ReadSegment,
		muzak.Summary("Read a segment"),
		core.Require(authz.LiveOpsRead))

	r.Put(segments+"/{key}", handlers.UpdateSegment,
		muzak.Summary("Replace a segment"),
		muzak.Description("A whole-object write. A partial update of a rule tree would mean "+
			"two people merging into an audience neither of them wrote."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The rule is malformed, names a segment that does not exist, or refers to itself"))

	r.Delete(segments+"/{key}", handlers.ArchiveSegment,
		muzak.Summary("Archive a segment"),
		muzak.Description("The row is kept and marked archived; the next publish leaves it "+
			"out. A rule still naming it will then refuse to compile, which is the "+
			"intended way to find out."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusConflict, "That segment is already archived"))

	// ---- feature flags ------------------------------------------------------

	r.Get(flags, handlers.ListFlags,
		muzak.Summary("List a project's flags"),
		core.Require(authz.LiveOpsRead))

	r.Post(flags, handlers.CreateFlag,
		muzak.Summary("Declare a flag"),
		muzak.Description("`kind` and `key` are fixed at creation: a game calls a typed "+
			"reader on the key, so a kind that changed would be a document the installed "+
			"build cannot read. Every value is checked against the kind here."),
		core.Require(authz.LiveOpsWrite),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "A flag with that key already exists"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"A value does not match the flag's kind, or a rule cannot be resolved"))

	r.Get(flags+"/{key}", handlers.ReadFlag,
		muzak.Summary("Read a flag"),
		core.Require(authz.LiveOpsRead))

	r.Put(flags+"/{key}", handlers.UpdateFlag,
		muzak.Summary("Replace a flag"),
		muzak.Description("`kind` is not a member of this body. A field a caller may send "+
			"that has no effect is worse than one that does not exist."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"A value does not match the flag's kind, or a rule cannot be resolved"))

	r.Delete(flags+"/{key}", handlers.ArchiveFlag,
		muzak.Summary("Archive a flag"),
		muzak.Description("The row is kept. Until somebody publishes, the game keeps "+
			"getting the answer the last published document gave it."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusConflict, "That flag is already archived"))

	// ---- LiveOps events ----------------------------------------------------

	r.Get(events, handlers.ListLiveOpsEvents,
		muzak.Summary("List a project's LiveOps events"),
		muzak.Description("Highest priority first. Each carries the next few concrete "+
			"windows its schedule produces, computed by the same code the config path "+
			"runs."),
		core.Require(authz.LiveOpsRead))

	r.Post(events, handlers.CreateLiveOpsEvent,
		muzak.Summary("Schedule a LiveOps event"),
		muzak.Description("Created as a draft unless told otherwise: publishing reads only "+
			"the active ones, so an event cannot reach a player by being typed."),
		core.Require(authz.LiveOpsWrite),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "A LiveOps event with that key already exists"),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The schedule or the audience is not valid, and the message says which"))

	r.Get(events+"/{key}", handlers.ReadLiveOpsEvent,
		muzak.Summary("Read a LiveOps event"),
		core.Require(authz.LiveOpsRead))

	r.Put(events+"/{key}", handlers.UpdateLiveOpsEvent,
		muzak.Summary("Replace a LiveOps event"),
		muzak.Description("`status` moves freely between draft, active, paused and "+
			"archived. Every pair is something a studio legitimately does, and a state "+
			"machine here would forbid the one somebody needs at two in the morning."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The schedule or the audience is not valid, and the message says which"))

	r.Delete(events+"/{key}", handlers.ArchiveLiveOpsEvent,
		muzak.Summary("Archive a LiveOps event"),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusConflict, "That LiveOps event is already archived"))

	// ---- the draft ---------------------------------------------------------

	// Read, not write, and that is the whole point of it existing. The person who presses
	// publish holds a permission the person who wrote the change may not, so this is the
	// route that lets them look first — and looking costs a GET rather than a refused
	// publish in the middle of a release.
	r.Get("/organizations/{org}/projects/{project}/config/{environment}/draft",
		handlers.ReadDraft,
		muzak.Summary("Preview what the next publish would produce"),
		muzak.Description("Compiles the same input a publish would and throws the document "+
			"away. Says whether it compiles, what it would carry, whether serving it costs "+
			"a history lookup per request, and what is being left out and why."),
		core.Require(authz.LiveOpsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "This project has no such environment"))

	return r
}
