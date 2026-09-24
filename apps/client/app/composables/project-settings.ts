/**
 * The project's settings and its privacy requests, kept in this browser per project until
 * the dashboard is wired to the project routes. Only what a reader changed is stored; the
 * rest is the sample, so a project nobody touched is always the seed.
 */
interface ProjectChanges {
  name?: string
  retention_days?: RetentionDays
  retention_changed_at?: string
  deletion_at?: string | null
  privacy?: PrivacyRequest[]
}

let shared: Ref<Record<string, ProjectChanges>> | null = null

function stored(): Ref<Record<string, ProjectChanges>> {
  shared ??= effectScope(true).run(() => useLocalStorage<Record<string, ProjectChanges>>('playersense.project-settings.v1', {}))!
  return shared
}

export function useProjectSettings() {
  const store = stored()
  const { project: current } = useWorkspace()

  const changes = computed(() => store.value[current.value.id] ?? {})

  const project = computed<ProjectSettings>(() => {
    const seed = seedProjectSettings(current.value)
    const change = changes.value
    return {
      ...seed,
      name: change.name ?? seed.name,
      retention_days: change.retention_days ?? seed.retention_days,
      retention_changed_at: change.retention_changed_at,
      deletion_at: change.deletion_at ?? undefined
    }
  })

  const privacy = computed(() => changes.value.privacy ?? seedPrivacyRequests())

  function write(change: ProjectChanges) {
    store.value = { ...store.value, [current.value.id]: { ...changes.value, ...change } }
  }

  function rename(name: string) {
    write({ name: name.trim() })
  }

  function setRetention(days: RetentionDays) {
    write({ retention_days: days, retention_changed_at: new Date().toISOString() })
  }

  function scheduleDeletion() {
    write({ deletion_at: new Date(Date.now() + 30 * 86_400_000).toISOString() })
  }

  function keepProject() {
    write({ deletion_at: null })
  }

  function requestPrivacy(kind: PrivacyKind, playerId: string, reference: string, by: string) {
    const request: PrivacyRequest = {
      id: `prv_${Math.random().toString(36).slice(2, 8)}`,
      kind,
      player_id: playerId.trim(),
      status: 'queued',
      requested_by: by,
      requested_at: new Date().toISOString(),
      reference: reference.trim() || undefined
    }
    write({ privacy: [request, ...privacy.value] })
    return request
  }

  return { project, privacy, rename, setRetention, scheduleDeletion, keepProject, requestPrivacy }
}

/** A project's name as the reader last saved it, for the drawer and the breadcrumb. */
export function projectNameOf(id: string, fallback: string) {
  return stored().value[id]?.name ?? fallback
}
