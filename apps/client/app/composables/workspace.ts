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
    { id: 'prj_starfall', name: 'Starfall Tactics' },
    { id: 'prj_moonrise', name: 'Moonrise Farm' },
    { id: 'prj_pocketrally', name: 'Pocket Rally' }
  ]
}, {
  id: 'org_nova',
  name: 'Nova Interactive',
  projects: [
    { id: 'prj_skyline', name: 'Skyline Drift' }
  ]
}]

export function useWorkspace() {
  const organizationId = useState('workspace-organization', () => ORGANIZATIONS[0]!.id)
  const projectId = useState('workspace-project', () => ORGANIZATIONS[0]!.projects[0]!.id)

  const organization = computed(() =>
    ORGANIZATIONS.find(candidate => candidate.id === organizationId.value) ?? ORGANIZATIONS[0]!)

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
