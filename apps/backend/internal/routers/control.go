// Package routers decides which handler answers which path.
//
// Each router is written on its own and knows nothing about where it will be mounted or
// what will guard it. Those decisions live in cmd/control-api, which keeps the routers
// reusable and puts the security decision somewhere a reviewer will find it.
package routers

import (
	"net/http"

	muzak "muzak.dev/framework"

	"playersense.dev/backend/internal/authz"
	"playersense.dev/backend/internal/core"
	"playersense.dev/backend/internal/handlers"
)

// Meta returns the operational routes.
func Meta() *muzak.Router {
	r := muzak.NewRouter()

	r.Get("/healthz", handlers.Health,
		muzak.Summary("Liveness probe"),
		// Routable, but out of the document: its existence is uninteresting.
		muzak.Hidden(),
		// A monitor polling every second is the one client that should never be told to
		// slow down.
		muzak.SkipRateLimit())

	return r
}

// Organizations returns the organization routes.
func Organizations() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("organizations"))

	// Neither path names an organisation, so there is no membership and no role to check
	// against: anybody signed in may create one, and listing returns only what the caller
	// belongs to.
	//
	// Creating one still changes something, which is why it carries RequireVerified. It is
	// the one write in the product that cannot be expressed as a permission — there is no
	// organisation yet to hold the role that would grant it — so it is also the one place a
	// read-only account could otherwise act.
	r.Post("/organizations", handlers.CreateOrganization,
		muzak.Summary("Create an organization"),
		muzak.Description("The caller becomes its owner, and its six roles are seeded."),
		core.RequireVerified(),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "An organization with that slug already exists"))

	r.Get("/organizations", handlers.ListOrganizations,
		muzak.Summary("List the organizations you belong to"))

	r.Get("/organizations/{org}", handlers.ReadOrganization,
		muzak.Summary("Read an organization"),
		core.Require(authz.OrgRead),
		muzak.WithResponseDoc(http.StatusNotFound,
			"No organization has that slug, or you are not a member of it"))

	return r
}

// Projects returns the project routes.
func Projects() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("projects"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization or project does not exist"))

	r.Post("/organizations/{org}/projects", handlers.CreateProject,
		muzak.Summary("Create a project"),
		muzak.Description("Creates a project and its development, staging and production "+
			"environments. `home_region` and `policy_tier` are fixed at creation: a tier "+
			"change applies forward and never retroactively, so changing one is a new project."),
		muzak.Status(http.StatusCreated),
		// project.create is organisation-scoped: there is no project to be inside yet.
		core.Require(authz.ProjectCreate),
		muzak.WithResponseDoc(http.StatusConflict, "A project with that slug already exists in the organization"))

	r.Get("/organizations/{org}/projects", handlers.ListProjects,
		muzak.Summary("List the projects you can see"),
		muzak.Description("An organization's projects, filtered to the ones this member has "+
			"been given access to."),
		// No permission, for the same reason ReadProject has none: whether you can see a
		// project is the access list, not a privilege. Requiring one here would mean a
		// role that grants nothing about projects could still hide the list, which is two
		// mechanisms answering one question.
		muzak.Needs(core.ResolveUser))

	r.Get("/organizations/{org}/projects/{project}", handlers.ReadProject,
		muzak.Summary("Read a project"),
		// Deliberately no permission of its own: whether you can see a project is the
		// access list, not a privilege, and two mechanisms for one question is how a
		// permission system becomes incomprehensible. ResolveUser answers 404 for a
		// project outside the member's access.
		muzak.Needs(core.ResolveUser))

	r.Patch("/organizations/{org}/projects/{project}", handlers.UpdateProject,
		muzak.Summary("Rename a project"),
		muzak.Description("The slug, the ids and the policy tier are fixed at creation; the name is "+
			"the one thing that changes, and links that carry the slug keep working."),
		core.Require(authz.ProjectUpdate))

	r.Put("/organizations/{org}/projects/{project}/retention", handlers.SetRetention,
		muzak.Summary("Change how long raw events are kept"),
		muzak.Description("One of 7, 30, 90, 180 or 365 days, at most the policy tier's cap and the "+
			"plan's max_raw_retention_days. New rows take it within minutes. A raised tier does "+
			"not bring back what already expired; a lowered one is enforced on rows written before "+
			"it too, by the worker, within a day."),
		core.Require(authz.ProjectUpdate),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity, "Not a tier, or above what this project may keep"))

	r.Post("/organizations/{org}/projects/{project}/deletion", handlers.ScheduleDeletion,
		muzak.Summary("Delete a project in thirty days"),
		muzak.Description("Events are collected until then and it can be kept at any point before. "+
			"Then its keys stop working and its analytics and archive are deleted; its usage and "+
			"invoices stay, because they are the organization's."),
		core.Require(authz.ProjectDelete),
		muzak.WithResponseDoc(http.StatusConflict, "It is already to be deleted"))

	r.Delete("/organizations/{org}/projects/{project}/deletion", handlers.CancelDeletion,
		muzak.Summary("Keep a project that was to be deleted"),
		core.Require(authz.ProjectDelete),
		muzak.WithResponseDoc(http.StatusConflict, "It is not to be deleted"))

	r.Get("/organizations/{org}/projects/{project}/apps", handlers.ListProjectApps,
		muzak.Summary("Where a project's events come from"),
		muzak.Description("Each platform and SDK, when first and last seen, how many events, and the "+
			"newest build and SDK version, across every environment."),
		muzak.Needs(core.ResolveUser),
		muzak.WithResponseDoc(http.StatusServiceUnavailable, "The facts could not be read"))

	return r
}

// LiveConfig returns the publish route (§41).
//
// One route, and it is the irreversible one. Drafting a flag or a LiveOps event is a change
// nobody has seen; publishing is a change every player has, which is why `liveops.publish` is
// a permission of its own and why DEVELOPER holds `liveops.write` without it.
func LiveConfig() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("liveops"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization or project does not exist"))

	r.Post("/organizations/{org}/projects/{project}/config/{environment}/publish",
		handlers.PublishConfig,
		muzak.Summary("Publish this project's configuration"),
		muzak.Description("Compiles every active flag and LiveOps event into one immutable "+
			"version for the named environment, with each segment reference replaced by "+
			"the segment's own rule. A version that compiles is a version that evaluates."),
		core.Require(authz.LiveOpsPublish),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusUnprocessableEntity,
			"The configuration does not compile; the message names the flag or event and why"))

	r.Get("/organizations/{org}/projects/{project}/config/{environment}/versions",
		handlers.ListConfigVersions,
		muzak.Summary("An environment's published versions"),
		muzak.Description("Newest first: who published each, when, why, what it carried, which one "+
			"is being served, and which were rollbacks and of what."),
		core.Require(authz.LiveOpsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "This project has no such environment"))

	r.Get("/organizations/{org}/projects/{project}/config/{environment}/versions/{version}",
		handlers.ReadConfigVersion,
		muzak.Summary("One published version, with its document"),
		muzak.Description("The compiled document exactly as the SDK received it, so what a player "+
			"was served on a given day can be read rather than reconstructed."),
		core.Require(authz.LiveOpsRead),
		muzak.WithResponseDoc(http.StatusNotFound, "No such environment or version"))

	r.Post("/organizations/{org}/projects/{project}/config/{environment}/versions/{version}/restore",
		handlers.RestoreDraft,
		muzak.Summary("Set the draft back to a version"),
		muzak.Description("The flags and segments that version was compiled from, written back into the draft in "+
			"one step: each flag's fallback, rules, reach and bucketing as they were, flags not in it "+
			"archived, and anything since deleted made again. Nothing is published; the environments serve what "+
			"they serve until the draft is published. For a version to serve again as it was, roll back instead."),
		core.Require(authz.LiveOpsWrite),
		muzak.WithResponseDoc(http.StatusNotFound, "No such environment or version"),
		muzak.WithResponseDoc(http.StatusConflict, "The version was published before drafts were kept with versions"))

	r.Post("/organizations/{org}/projects/{project}/config/{environment}/versions/{version}/rollback",
		handlers.RollbackConfig,
		muzak.Summary("Go back to an earlier version"),
		muzak.Description("Publishes that version's document again, unchanged, as the environment's "+
			"next version, with the reason in its note. Nothing is deleted and the draft is left "+
			"alone, so what was rolled back is still there to fix and publish. Needs what a publish "+
			"needs, and is in the audit log as liveops.rollback."),
		core.Require(authz.LiveOpsPublish),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "The environment already serves that version"),
		muzak.WithResponseDoc(http.StatusNotFound, "No such environment or version"))

	return r
}

// Keys returns the API key routes.
func Keys() *muzak.Router {
	r := muzak.NewRouter(muzak.WithTags("api-keys"),
		muzak.WithResponseDoc(http.StatusNotFound, "The organization, project or key does not exist"))

	r.Post("/organizations/{org}/projects/{project}/keys", handlers.CreateAPIKey,
		muzak.Summary("Mint an API key"),
		muzak.Description("Returns the key once. Only a SHA-256 digest is stored, so it "+
			"cannot be shown again."),
		core.Require(authz.KeyCreate),
		muzak.Status(http.StatusCreated))

	r.Get("/organizations/{org}/projects/{project}/keys", handlers.ListAPIKeys,
		muzak.Summary("List a project's API keys"),
		core.Require(authz.KeyRead))

	r.Delete("/organizations/{org}/projects/{project}/keys/{key_id}", handlers.RevokeAPIKey,
		muzak.Summary("Revoke an API key"),
		muzak.Description("The row is kept and marked revoked rather than deleted."),
		core.Require(authz.KeyRevoke),
		muzak.WithResponseDoc(http.StatusConflict, "That key is already revoked"))

	r.Post("/organizations/{org}/projects/{project}/keys/{key_id}/roll", handlers.RollAPIKey,
		muzak.Summary("Replace a key"),
		muzak.Description("Mints a key of the same kind, environment and name, shown once, and retires "+
			"this one: at once, or after grace_hours so a fleet of servers can be moved over without "+
			"an outage. An SDK key is inside shipped builds, so rolling one with no grace stops every "+
			"build that carries it."),
		core.Require(authz.KeyCreate, authz.KeyRevoke),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "The key is already revoked or rolled"))

	r.Get("/organizations/{org}/projects/{project}/keys/{key_id}/usage", handlers.ReadKeyUsage,
		muzak.Summary("A key's requests by hour"),
		muzak.Description("Up to the last 48 hours, counted as requests are authenticated, and when "+
			"it was last used."),
		core.Require(authz.KeyRead))

	return r
}
