import type { Schema } from '@playersense/contracts'

// The organization's audit log (GET …/audit), newest first, a page at a time: the first page is
// read with the page, and older ones are added on asking.

const PAGE = 200

function eventOf(out: Schema<'AuditEventOut'>): AuditEvent {
  return {
    id: out.id,
    action: out.action,
    actor: out.actor,
    actor_email: out.actor_email || undefined,
    target_type: out.target_type ?? '',
    target_id: out.target_id ?? '',
    metadata: { ...(out.metadata ?? {}) },
    ip: out.ip || undefined,
    at: out.at
  }
}

export function useAuditLog() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.orgPath && `${workspace.orgPath}/audit`)

  const first = useApiGet<Schema<'AuditListOut'>>(() => path.value && withQuery(path.value, { limit: PAGE }))
  const older = ref<AuditEvent[]>([])
  const next = ref<string | undefined>()
  const loadingMore = ref(false)

  watch(first.data, (out) => {
    older.value = []
    next.value = out?.next || undefined
  })

  const events = computed(() => [...(first.data.value?.events ?? []).map(eventOf), ...older.value])
  const loading = computed(() => first.isFetching.value && !first.data.value)
  const error = computed(() => first.error.value as ApiError | null)

  async function reload() {
    await first.execute()
  }

  /** The page before the oldest one read. */
  async function loadMore() {
    if (!path.value || !next.value || loadingMore.value) return
    loadingMore.value = true
    try {
      const out = await apiRequest<Schema<'AuditListOut'>>('GET', withQuery(path.value, { limit: PAGE, before: next.value }))
      older.value = [...older.value, ...(out.events ?? []).map(eventOf)]
      next.value = out.next || undefined
    } finally {
      loadingMore.value = false
    }
  }

  const hasMore = computed(() => Boolean(next.value))

  return { events, loading, error, reload, loadMore, hasMore, loadingMore }
}
