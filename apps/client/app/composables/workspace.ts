// Which organization and which project the dashboard is showing. Every analytics screen is
// scoped to one project (the API's routes are /organizations/{org}/projects/{project}/…),
// so the project is picked at the top of the drawer; the organization, which changes far
// less often, is picked from the account menu at its foot.
//
// Placeholder until control-api's /organizations list and each member's project access are
// wired in (§55.1) — only the projects a member was granted will be listed then.

export interface WorkspaceProject {
  id: string
  name: string
  /** Set from the name at creation, and fixed after. */
  slug: string
}

export interface WorkspaceOrganization {
  id: string
  name: string
  projects: WorkspaceProject[]
}

const ORGANIZATIONS: WorkspaceOrganization[] = [{
  id: 'org_acme',
  name: 'Acme Games',
  projects: [
    { id: 'prj_starfall', name: 'Starfall Tactics', slug: 'starfall-tactics' },
    { id: 'prj_moonrise', name: 'Moonrise Farm', slug: 'moonrise-farm' },
    { id: 'prj_pocketrally', name: 'Pocket Rally', slug: 'pocket-rally' }
  ]
}, {
  id: 'org_nova',
  name: 'Nova Interactive',
  projects: [
    { id: 'prj_skyline', name: 'Skyline Drift', slug: 'skyline-drift' }
  ]
}]

export function useWorkspace() {
  const organizationId = useState('workspace-organization', () => ORGANIZATIONS[0]!.id)
  const projectId = useState('workspace-project', () => ORGANIZATIONS[0]!.projects[0]!.id)

  /* Projects under the names Settings > General last saved for them. */
  const organization = computed(() => {
    const found = ORGANIZATIONS.find(candidate => candidate.id === organizationId.value) ?? ORGANIZATIONS[0]!
    return { ...found, projects: found.projects.map(entry => ({ ...entry, name: projectNameOf(entry.id, entry.name) })) }
  })

  const project = computed(() =>
    organization.value.projects.find(candidate => candidate.id === projectId.value) ?? organization.value.projects[0]!)

  /** Moves to another organization, landing on its first project. */
  function selectOrganization(id: string) {
    const next = ORGANIZATIONS.find(candidate => candidate.id === id)
    if (!next) return
    organizationId.value = next.id
    projectId.value = next.projects[0]!.id
  }

  function selectProject(id: string) {
    if (organization.value.projects.some(candidate => candidate.id === id)) projectId.value = id
  }

  return {
    organizations: ORGANIZATIONS,
    organization,
    project,
    selectOrganization,
    selectProject
  }
}
