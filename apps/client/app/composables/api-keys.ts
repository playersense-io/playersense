/**
 * The project's API keys and every write to them: create, roll and revoke. Kept in this
 * browser until the dashboard is wired to /organizations/{org}/projects/{project}/keys, and
 * seeded with a studio's usual keys.
 *
 * Nothing is edited in place, as the API edits nothing: a key's kind, environment and name
 * are fixed when it is made. Rolling is making a new key beside the old one, which keeps
 * working until it is revoked, so the new one can be deployed first.
 */
let shared: Ref<ApiKeyDef[]> | null = null

function storedKeys(): Ref<ApiKeyDef[]> {
  shared ??= effectScope(true).run(() => useLocalStorage<ApiKeyDef[]>('playersense.apikeys.v1', seedApiKeys, { deep: false }))!
  return shared
}

export function useApiKeys() {
  const stored = storedKeys()

  const keys = computed<ApiKeyDef[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && typeof entry.public_id === 'string' && Array.isArray(entry.hourly))
    return valid ? value : seedApiKeys()
  })

  const keyById = (id: string) => keys.value.find(entry => entry.id === id) ?? null

  /** A new key. The secret is returned here and nowhere else, ever. */
  function create(kind: ApiKeyKind, environment: EnvironmentKind, name: string, replaces?: string) {
    const { publicId, secret } = mintKey(kind, environment)
    const key: ApiKeyDef = {
      id: `key_${Math.random().toString(36).slice(2, 14)}`,
      kind,
      environment,
      name,
      public_id: publicId,
      created_at: new Date().toISOString(),
      created_by: CURRENT_ACTOR,
      last_used_at: null,
      revoked_at: null,
      hourly: Array.from({ length: 24 }, () => 0)
    }
    /* Rolling retires the key it replaces: it stops working the moment the new one exists. */
    const now = new Date().toISOString()
    stored.value = [key, ...keys.value.map(entry => (entry.id === replaces ? { ...entry, rolled_at: now, replaced_by: key.id } : entry))]
    return { key, secret }
  }

  /** Refused from the next request, for good. */
  function revoke(id: string) {
    const now = new Date().toISOString()
    stored.value = keys.value.map(entry => (entry.id === id ? { ...entry, revoked_at: now, revoked_by: CURRENT_ACTOR } : entry))
  }

  return { keys, keyById, create, revoke }
}
