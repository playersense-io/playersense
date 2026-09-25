import { useWorkspaceStore } from '~/stores/workspace'

// Which organization and which project the dashboard is showing, in the shape the pages read:
// every analytics screen is scoped to one project (the API's routes are
// /organizations/{org}/projects/{project}/…), picked at the top of the drawer; the organization,
// which changes far less often, from the account menu at its foot. The workspace store
// (stores/workspace.ts) holds the real memberships and projects.

export interface WorkspaceProject {
  /** The project's id. */
  id: string
  name: string
  /** Set from the name at creation, and fixed after: what the API's paths name. */
  slug: string
}

export interface WorkspaceOrganization {
  /** The organization's slug: what the API's paths name. */
  id: string
  name: string
  projects: WorkspaceProject[]
}

export function useWorkspace() {
  const store = useWorkspaceStore()

  const toProject = (entry: { id: string, name: string, slug: string }): WorkspaceProject =>
    ({ id: entry.id, name: entry.name, slug: entry.slug })

  const organizations = computed<WorkspaceOrganization[]>(() => store.organizations.map(entry => ({
    id: entry.slug,
    name: entry.name,
    projects: entry.slug === store.organization?.slug ? store.projects.map(toProject) : []
  })))

  const organization = computed<WorkspaceOrganization>(() => ({
    id: store.organization?.slug ?? '',
    name: store.organization?.name ?? '',
    projects: store.projects.map(toProject)
  }))

  const project = computed<WorkspaceProject>(() =>
    store.project ? toProject(store.project) : { id: '', name: '', slug: '' })

  return {
    organizations,
    organization,
    project,
    environment: computed(() => store.environment),
    selectOrganization: (slug: string) => store.selectOrganization(slug),
    selectProject: (slug: string) => store.selectProject(slug)
  }
}
