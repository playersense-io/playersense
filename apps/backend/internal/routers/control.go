// Package routers decides which handler answers which path.
//
// Each router is written on its own and knows nothing about where it will be mounted or
// what will guard it. Those decisions live in cmd/control-api, which keeps the routers
// reusable and puts the security decision somewhere a reviewer will find it.
package routers

import (
	"net/http"

	"muzak.dev/framework"

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

	return r
}
