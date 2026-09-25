import { defineStore } from 'pinia'
import type { Schema } from '@playersense/contracts'

// Which organization, project and environment the dashboard is showing. The organizations are
// the signed-in person's memberships (GET /v1/users/me), each organization's projects those the
// person may reach; the choice is kept in this browser, so a reload lands where it was.

export type Me = Schema<'MeOut'>
export type MeOrganization = Schema<'MeOrganizationOut'>
export type ProjectRecord = Schema<'ProjectOut'>
export type EnvironmentKind = 'production' | 'staging' | 'development'

export const useWorkspaceStore = defineStore('workspace', () => {
  const me = ref<Me | null>(null)
  const projects = ref<ProjectRecord[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<string | null>(null)

  const orgSlug = useLocalStorage<string>('playersense:organization', '')
  const projectSlug = useLocalStorage<string>('playersense:project', '')
  const environment = useLocalStorage<EnvironmentKind>('playersense:environment', 'production')

  const organizations = computed(() => me.value?.organizations ?? [])
  const organization = computed<MeOrganization | null>(() =>
    organizations.value.find(entry => entry.slug === orgSlug.value) ?? organizations.value[0] ?? null)
  const project = computed<ProjectRecord | null>(() =>
    projects.value.find(entry => entry.slug === projectSlug.value) ?? projects.value[0] ?? null)

  /** /v1/organizations/{org}, or null before an organization is known. */
  const orgPath = computed(() => organization.value ? `/v1/organizations/${organization.value.slug}` : null)
  /** /v1/organizations/{org}/projects/{project}, or null before a project is known. */
  const projectPath = computed(() => orgPath.value && project.value ? `${orgPath.value}/projects/${project.value.slug}` : null)
  /** The project's analytics in the chosen environment. */
  const analyticsPath = computed(() => projectPath.value ? `${projectPath.value}/analytics/${environment.value}` : null)

  async function loadProjects() {
    if (!orgPath.value) {
      projects.value = []
      return
    }
    const out = await apiRequest<Schema<'ProjectListOut'>>('GET', `${orgPath.value}/projects`)
    projects.value = [...(out.projects ?? [])]
    if (!projects.value.some(entry => entry.slug === projectSlug.value)) projectSlug.value = projects.value[0]?.slug ?? ''
  }

  /** Reads who the person is and where they may go; once, unless forced. */
  async function load(force = false) {
    if ((loaded.value && !force) || loading.value) return
    loading.value = true
    error.value = null
    try {
      me.value = await apiRequest<Me>('GET', '/v1/users/me')
      if (!organizations.value.some(entry => entry.slug === orgSlug.value)) orgSlug.value = organizations.value[0]?.slug ?? ''
      await loadProjects()
      loaded.value = true
    } catch (caught) {
      error.value = caught instanceof Error ? caught.message : String(caught)
    } finally {
      loading.value = false
    }
  }

  async function selectOrganization(slug: string) {
    if (slug === orgSlug.value || !organizations.value.some(entry => entry.slug === slug)) return
    orgSlug.value = slug
    projectSlug.value = ''
    await loadProjects()
  }

  function selectProject(slug: string) {
    if (projects.value.some(entry => entry.slug === slug)) projectSlug.value = slug
  }

  function selectEnvironment(kind: EnvironmentKind) {
    environment.value = kind
  }

  /** Forgets everything, on signing out. */
  function reset() {
    me.value = null
    projects.value = []
    loaded.value = false
  }

  return {
    me, projects, loading, loaded, error, organizations, organization, project, environment,
    orgPath, projectPath, analyticsPath, load, loadProjects, selectOrganization, selectProject, selectEnvironment, reset
  }
})
