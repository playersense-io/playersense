import type { Schema } from '@playersense/contracts'

// The project's settings and its privacy requests, from the API: the project itself
// (GET …/projects/{project}), the apps its events come from, and the erasures and exports asked
// for its players. Every change is a request, and the page reads the project again after it.

type ProjectSettingsOut = Schema<'ProjectSettingsOut'>
type PrivacyRequestOut = Schema<'PrivacyRequestOut'>

function settingsOf(out: ProjectSettingsOut | null, apps: Schema<'ProjectAppOut'>[]): ProjectSettings | null {
  if (!out) return null
  return {
    id: out.id,
    name: out.name,
    slug: out.slug,
    policy_tier: out.policy_tier as PolicyTier,
    retention_days: out.retention_days as RetentionDays,
    retention_cap: out.retention_cap,
    retention_changed_at: out.retention_changed_at ?? undefined,
    retention_purge_until: out.retention_purge_until ?? undefined,
    environments: (out.environments ?? []).map(env => ({ id: env.id, kind: env.kind as EnvironmentKind })),
    apps: apps.map(app => ({ platform: app.platform, sdk: app.sdk, sdk_version: app.sdk_version, app_version: app.app_version,
      events: app.events, first_seen: app.first_seen, last_seen: app.last_seen })),
    created_at: out.created_at,
    deletion_at: out.deletion_at ?? undefined,
    deletion_requested_by: out.deletion_requested_by || undefined
  }
}

function privacyOf(out: PrivacyRequestOut): PrivacyRequest {
  return {
    id: out.id,
    kind: out.kind as PrivacyKind,
    player_id: out.player_id,
    status: out.status as PrivacyStatus,
    requested_by: out.requested_by ?? '',
    requested_at: out.requested_at,
    completed_at: out.completed_at ?? undefined,
    expires_at: out.expires_at ?? undefined,
    failure_reason: out.failure_reason || undefined,
    download_path: out.download_path || undefined,
    reference: out.reference || undefined
  }
}

export function useProjectSettings() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath)

  const settings = useApiGet<ProjectSettingsOut>(path)
  const apps = useApiGet<Schema<'ProjectAppsOut'>>(() => path.value && `${path.value}/apps`)
  const requests = useApiGet<Schema<'PrivacyListOut'>>(() => path.value && withQuery(`${path.value}/privacy`, { limit: 200 }))

  const project = computed(() => settingsOf(settings.data.value, [...(apps.data.value?.apps ?? [])]))
  const privacy = computed(() => (requests.data.value?.requests ?? []).map(privacyOf))
  const loading = computed(() => settings.isFetching.value && !settings.data.value)
  const error = computed(() => settings.error.value as ApiError | null)

  async function reload() {
    await settings.execute()
  }

  async function rename(name: string) {
    if (!path.value) return
    await apiRequest('PATCH', path.value, { name: name.trim() })
    await Promise.all([reload(), workspace.loadProjects()])
  }

  async function setRetention(days: RetentionDays) {
    if (!path.value) return
    await apiRequest('PUT', `${path.value}/retention`, { retention_days: days })
    await reload()
  }

  async function scheduleDeletion() {
    if (!path.value) return
    await apiRequest('POST', `${path.value}/deletion`)
    await reload()
  }

  async function keepProject() {
    if (!path.value) return
    await apiRequest('DELETE', `${path.value}/deletion`)
    await reload()
  }

  async function requestPrivacy(kind: PrivacyKind, playerId: string, reference: string) {
    if (!path.value) return
    await apiRequest('POST', `${path.value}/privacy/${kind === 'erase' ? 'erasures' : 'exports'}`,
      { player_id: playerId.trim(), reference: reference.trim() })
    await requests.execute()
  }

  async function downloadExport(request: PrivacyRequest) {
    if (!request.download_path) return
    await apiDownload(request.download_path, `export-${request.player_id}.json`)
  }

  return { project, privacy, loading, error, reload, rename, setRetention, scheduleDeletion, keepProject, requestPrivacy, downloadExport }
}
