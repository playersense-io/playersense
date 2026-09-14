// Package routers decides which handler answers which path.
//
// Each router is written on its own and knows nothing about where it will be mounted or
// what will guard it. Those decisions live in cmd/control-api, which keeps the routers
// reusable and puts the security decision somewhere a reviewer will find it.
package routers

import (
	"net/http"

	"muzak.dev/framework"

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

	r.Post("/organizations", handlers.CreateOrganization,
		muzak.Summary("Create an organization"),
		muzak.Status(http.StatusCreated),
		muzak.WithResponseDoc(http.StatusConflict, "An organization with that slug already exists"))

	r.Get("/organizations", handlers.ListOrganizations,
		muzak.Summary("List organizations"))

	r.Get("/organizations/{org}", handlers.ReadOrganization,
		muzak.Summary("Read an organization"),
		muzak.WithResponseDoc(http.StatusNotFound, "No organization has that slug"))

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
		muzak.WithResponseDoc(http.StatusConflict, "A project with that slug already exists in the organization"))

	r.Get("/organizations/{org}/projects", handlers.ListProjects,
		muzak.Summary("List an organization's projects"))

	r.Get("/organizations/{org}/projects/{project}", handlers.ReadProject,
		muzak.Summary("Read a project"))

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
		muzak.Status(http.StatusCreated))

	r.Get("/organizations/{org}/projects/{project}/keys", handlers.ListAPIKeys,
		muzak.Summary("List a project's API keys"))

	r.Delete("/organizations/{org}/projects/{project}/keys/{key_id}", handlers.RevokeAPIKey,
		muzak.Summary("Revoke an API key"),
		muzak.Description("The row is kept and marked revoked rather than deleted."),
		muzak.WithResponseDoc(http.StatusConflict, "That key is already revoked"))

	return r
}
