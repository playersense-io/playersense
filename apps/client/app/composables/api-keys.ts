import type { Schema } from '@playersense/contracts'

/**
 * The project's API keys (…/projects/{project}/keys) and every write to them: create, roll and
 * revoke. The list carries each key's last day of requests an hour at a time.
 *
 * Nothing is edited in place, as the API edits nothing: a key's kind, environment and name
 * are fixed when it is made. Rolling makes a new key beside the old one, which keeps working
 * for the grace asked for, so the new one can be deployed first.
 */

function keyOf(out: Schema<'APIKeyOut'>): ApiKeyDef {
  return {
    id: out.id,
    kind: out.kind as ApiKeyKind,
    environment: out.environment as EnvironmentKind,
    name: out.name ?? '',
    public_id: out.public_id,
    created_at: out.created_at,
    created_by: out.created_by ?? '',
    last_used_at: out.last_used_at ?? null,
    revoked_at: out.revoked_at ?? null,
    revoked_by: out.revoked_by || undefined,
    hourly: [...(out.hourly ?? [])],
    rolled_at: out.rolled_at ?? null,
    replaced_by: out.replaced_by ?? undefined,
    expires_at: out.expires_at ?? null
  }
}

export function useApiKeys() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/keys`)

  const list = useApiGet<Schema<'APIKeyListOut'>>(path)
  const keys = computed(() => (list.data.value?.keys ?? []).map(keyOf))
  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  const keyById = (id: string) => keys.value.find(entry => entry.id === id) ?? null

  async function reload() {
    await list.execute()
  }

  /** A new key. The secret is returned here and nowhere else, ever. */
  async function create(kind: ApiKeyKind, environment: EnvironmentKind, name: string) {
    const out = await apiRequest<Schema<'APIKeyCreatedOut'>>('POST', path.value!, { kind, environment, name })
    await reload()
    return { key: keyOf(out), secret: out.secret }
  }

  /** A new key in the old one's place; the old one works on for `graceHours`, or stops now. */
  async function roll(id: string, graceHours: number) {
    const out = await apiRequest<Schema<'RolledAPIKeyOut'>>('POST', `${path.value}/${id}/roll`, { grace_hours: graceHours })
    await reload()
    return { key: keyOf(out.key), secret: out.key.secret }
  }

  /** Refused from the next request, for good. */
  async function revoke(id: string) {
    await apiRequest('DELETE', `${path.value}/${id}`)
    await reload()
  }

  return { keys, loading, error, reload, keyById, create, roll, revoke }
}
