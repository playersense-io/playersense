import type { Schema } from '@playersense/contracts'

/**
 * The project's webhook destinations (…/projects/{project}/webhooks) and every write to them:
 * add, edit, enable, disable, delete, roll the signing secret, send a test event and resend a
 * delivery. Each destination's day comes with the list; its hour by hour and its deliveries are
 * read on their own (…/health, …/deliveries), since the log lives in ClickHouse and is paged.
 */

type EndpointOut = Schema<'EndpointOut'>
type DayOut = Schema<'WebhookDayOut'>
type DeliveryOut = Schema<'WebhookDeliveryOut'>
type TryOut = Schema<'WebhookTryOut'>

export type WebhookDraft = Pick<WebhookDestination, 'name' | 'description' | 'url' | 'events' | 'environments' | 'headers'>

/* The history is the audit log's entries for the destination, by their action names. */
const ACTIONS: Record<string, string> = {
  'webhook.write': 'Saved',
  'webhook.delete': 'Deleted',
  'webhook.secret_roll': 'Signing secret rolled',
  'webhook.secret_read': 'Signing secret revealed'
}

export function dayOf(out: DayOut | undefined): WebhookDay {
  if (!out) return NO_DAY
  return { deliveries: out.deliveries, firstTry: out.first_try, trySuccess: out.try_success, succeeded: out.succeeded, failed: out.failed, p50: out.p50_ms, p95: out.p95_ms }
}

export function destinationOf(out: EndpointOut): WebhookDestination {
  return {
    id: out.id,
    name: out.name,
    description: out.description,
    url: out.url,
    events: [...out.events],
    environments: out.environments as EnvironmentKind[],
    status: out.status as WebhookStatus,
    health: out.health as WebhookHealth,
    disabled_reason: (out.disabled_reason || undefined) as WebhookDestination['disabled_reason'],
    disabled_at: out.disabled_at ?? undefined,
    failing_since: out.failing_since ?? undefined,
    auto_disable_at: out.auto_disable_at ?? undefined,
    version: out.api_version,
    headers: (out.headers ?? []).map(header => ({ name: header.name, value: header.value ?? '', secret: header.secret })),
    secret: { created_at: out.secret.created_at, previous_expires_at: out.secret.previous_expires_at ?? undefined },
    retrying: out.retrying,
    lastDay: dayOf(out.last_day),
    created_at: out.created_at,
    created_by: out.created_by,
    updated_at: out.updated_at,
    updated_by: out.updated_by,
    history: (out.history ?? []).map(entry => ({ at: entry.at, actor: entry.actor, text: ACTIONS[entry.action] ?? entry.action }))
  }
}

export function deliveryOf(out: DeliveryOut, destinationId: string): WebhookDelivery {
  return {
    id: out.id,
    destinationId,
    eventId: out.event_id,
    type: out.type,
    environment: (out.environment || undefined) as EnvironmentKind | undefined,
    created: Date.parse(out.created_at),
    status: out.status as DeliveryStatus,
    attempts: out.attempts,
    lastAt: Date.parse(out.last_at),
    lastCode: out.last_code ?? null,
    lastError: out.last_error || undefined,
    next: out.next_at ? Date.parse(out.next_at) : undefined,
    test: out.test || undefined
  }
}

export function attemptOf(out: TryOut): DeliveryAttempt {
  return {
    number: out.number,
    at: Date.parse(out.at),
    code: out.code ?? null,
    error: out.error || undefined,
    ms: out.ms,
    body: out.response,
    outcome: out.outcome as DeliveryStatus,
    next: out.next_at ? Date.parse(out.next_at) : undefined,
    manual: out.manual || undefined
  }
}

/** A destination as the API takes it. A secret header left blank keeps the value it has. */
function bodyOf(draft: WebhookDraft, enabled?: boolean) {
  return {
    name: draft.name,
    description: draft.description,
    url: draft.url,
    events: draft.events,
    environments: draft.environments,
    headers: draft.headers.map(header => ({ name: header.name, value: header.value, secret: Boolean(header.secret) })),
    ...(enabled === undefined ? {} : { enabled })
  }
}

function webhooksPath() {
  const workspace = useWorkspaceStore()
  return computed(() => workspace.projectPath && `${workspace.projectPath}/webhooks`)
}

/** Every write to the project's destinations, with no read: for a dialog that changes one. */
export function useWebhookActions() {
  const path = webhooksPath()

  /** A new destination, enabled, with a secret of its own. Returns its id. */
  async function create(draft: WebhookDraft): Promise<string> {
    const out = await apiRequest<EndpointOut>('POST', path.value!, bodyOf(draft))
    return out.id
  }

  async function update(destination: WebhookDestination, draft: WebhookDraft, enabled?: boolean) {
    await apiRequest<EndpointOut>('PUT', `${path.value}/${destination.id}`, bodyOf(draft, enabled))
  }

  /** On or off, with everything else as it is. A secret header is sent blank, which keeps it. */
  async function setEnabled(destination: WebhookDestination, enabled: boolean) {
    await update(destination, destination, enabled)
  }

  /** Deleted for good: its secret with it. */
  async function remove(destination: WebhookDestination) {
    await apiRequest('DELETE', `${path.value}/${destination.id}`)
  }

  /** A new secret, signing from now; the old one signs beside it for `hours`, 0 ending it at once. */
  async function rollSecret(destination: WebhookDestination, hours: number): Promise<string> {
    const out = await apiRequest<EndpointOut>('POST', `${path.value}/${destination.id}/secret/roll`, { expire_after_hours: hours })
    return out.signing_secret ?? ''
  }

  /** The signing secret itself, read on request. */
  async function revealSecret(destination: WebhookDestination): Promise<string> {
    const out = await apiRequest<EndpointOut>('GET', `${path.value}/${destination.id}/secret`)
    return out.signing_secret ?? ''
  }

  /** A made-up event of this type, sent to one destination now: the delivery and its try. */
  async function sendTest(destination: WebhookDestination, type: string, environment?: EnvironmentKind) {
    const out = await apiRequest<Schema<'WebhookSentOut'>>('POST', `${path.value}/${destination.id}/test`, { type, environment: environment ?? '' })
    return { deliveryId: out.delivery_id, eventId: out.event_id, attempt: attemptOf(out.try) }
  }

  /** One more try of a delivery, now, with the same event id: the server sees a repeat. */
  async function resend(destinationId: string, deliveryId: string): Promise<DeliveryAttempt> {
    const out = await apiRequest<Schema<'WebhookSentOut'>>('POST', `${path.value}/${destinationId}/deliveries/${deliveryId}/resend`)
    return attemptOf(out.try)
  }

  /** Every delivery that gave up since `since` (three days when absent), queued to go again. */
  async function resendFailed(destination: WebhookDestination, since?: string): Promise<number> {
    const out = await apiRequest<Schema<'ResendFailedOut'>>('POST', `${path.value}/${destination.id}/resend-failed`, { since: since ?? null })
    return out.queued
  }

  return { create, update, setEnabled, remove, rollSecret, revealSecret, sendTest, resend, resendFailed }
}

/** The project's destinations, as a list that reads again after each change made through it. */
export function useWebhooks() {
  const path = webhooksPath()
  const actions = useWebhookActions()

  const list = useApiGet<Schema<'WebhookListOut'>>(path)
  const destinations = computed(() => (list.data.value?.webhooks ?? []).map(destinationOf))
  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  const destinationById = (id: string) => destinations.value.find(entry => entry.id === id) ?? null

  async function reload() {
    await list.execute()
  }

  async function create(draft: WebhookDraft) {
    const id = await actions.create(draft)
    await reload()
    return id
  }

  async function update(destination: WebhookDestination, draft: WebhookDraft) {
    await actions.update(destination, draft)
    await reload()
  }

  return { destinations, loading, error, reload, destinationById, create, update }
}

/** One destination read by itself, with its history. */
export function useWebhook(id: MaybeRefOrGetter<string>) {
  const path = webhooksPath()
  const one = useApiGet<EndpointOut>(() => path.value && toValue(id) && `${path.value}/${toValue(id)}`)
  const destination = computed(() => (one.data.value ? destinationOf(one.data.value) : null))
  const loading = computed(() => one.isFetching.value && !one.data.value)
  const error = computed(() => one.error.value as ApiError | null)
  return { destination, loading, error, reload: () => one.execute() }
}

/** A destination's last 24 hours hour by hour, against the 24 before. */
export function useWebhookHealth(id: MaybeRefOrGetter<string | null>) {
  const path = webhooksPath()
  const read = useApiGet<Schema<'WebhookHealthOut'>>(() => path.value && toValue(id) && `${path.value}/${toValue(id)}/health`)
  const health = computed(() => {
    const out = read.data.value
    if (!out) return null
    return {
      current: dayOf(out.current),
      previous: dayOf(out.previous),
      retrying: out.retrying,
      hours: (out.hours ?? []).map(hour => ({ at: Date.parse(hour.at), ok: hour.ok, failed: hour.failed }))
    }
  })
  return { health, loading: computed(() => read.isFetching.value && !read.data.value), reload: () => read.execute() }
}

/** Every destination's health, read side by side: for the project's figures and each row's line. */
export function useWebhooksHealth(ids: MaybeRefOrGetter<readonly string[]>) {
  const path = webhooksPath()
  const byId = ref<Record<string, { current: WebhookDay, previous: WebhookDay, hours: { at: number, ok: number, failed: number }[] }>>({})
  const loading = ref(false)

  async function load() {
    const list = toValue(ids)
    if (!path.value || !list.length) {
      byId.value = {}
      return
    }
    loading.value = true
    try {
      const read = await Promise.all(list.map(async (id) => {
        try {
          const out = await apiRequest<Schema<'WebhookHealthOut'>>('GET', `${path.value}/${id}/health`)
          return [id, {
            current: dayOf(out.current),
            previous: dayOf(out.previous),
            hours: (out.hours ?? []).map(hour => ({ at: Date.parse(hour.at), ok: hour.ok, failed: hour.failed }))
          }] as const
        } catch {
          return null
        }
      }))
      byId.value = Object.fromEntries(read.filter(entry => entry !== null))
    } finally {
      loading.value = false
    }
  }

  watch(() => [path.value, [...toValue(ids)].join(',')], () => void load(), { immediate: true })

  return { byId, loading, reload: load }
}

export interface DeliveryQuery {
  status?: DeliveryStatus | 'all'
  type?: string
}

/**
 * Deliveries to some destinations, newest first, a page at a time. With several destinations,
 * each is read and the pages merged, so "load older" asks each for its next page.
 */
export function useWebhookDeliveries(destinationIds: MaybeRefOrGetter<readonly string[]>, query: MaybeRefOrGetter<DeliveryQuery> = {}) {
  const path = webhooksPath()
  const deliveries = ref<WebhookDelivery[]>([])
  const cursors = ref<Record<string, string | undefined>>({})
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref<ApiError | null>(null)

  const PAGE = 50

  async function page(id: string, before?: string) {
    const { status, type } = toValue(query)
    const out = await apiRequest<Schema<'WebhookDeliveriesOut'>>('GET', withQuery(`${path.value}/${id}/deliveries`, {
      limit: PAGE,
      before,
      status: status && status !== 'all' ? status : undefined,
      type
    }))
    return { id, deliveries: (out.deliveries ?? []).map(entry => deliveryOf(entry, id)), next: out.next || undefined }
  }

  async function load() {
    const ids = toValue(destinationIds)
    if (!path.value || !ids.length) {
      deliveries.value = []
      cursors.value = {}
      return
    }
    loading.value = true
    error.value = null
    try {
      const pages = await Promise.all(ids.map(id => page(id)))
      deliveries.value = pages.flatMap(entry => entry.deliveries).sort((a, b) => b.created - a.created)
      cursors.value = Object.fromEntries(pages.map(entry => [entry.id, entry.next]))
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      loading.value = false
    }
  }

  async function loadMore() {
    const pending = Object.entries(cursors.value).filter(([, next]) => next)
    if (!pending.length || loadingMore.value) return
    loadingMore.value = true
    try {
      const pages = await Promise.all(pending.map(([id, next]) => page(id, next)))
      deliveries.value = [...deliveries.value, ...pages.flatMap(entry => entry.deliveries)].sort((a, b) => b.created - a.created)
      cursors.value = { ...cursors.value, ...Object.fromEntries(pages.map(entry => [entry.id, entry.next])) }
    } finally {
      loadingMore.value = false
    }
  }

  const hasMore = computed(() => Object.values(cursors.value).some(Boolean))

  watch(() => [path.value, [...toValue(destinationIds)].join(','), JSON.stringify(toValue(query))], () => void load(), { immediate: true })

  return { deliveries, loading, loadingMore, error, hasMore, reload: load, loadMore }
}

/** One delivery opened: its tries and, while it is kept, the body it carried. */
export async function readDelivery(destinationId: string, deliveryId: string): Promise<WebhookDeliveryDetail> {
  const workspace = useWorkspaceStore()
  const out = await apiRequest<Schema<'WebhookDeliveryDetailOut'>>('GET', `${workspace.projectPath}/webhooks/${destinationId}/deliveries/${deliveryId}`)
  return { ...deliveryOf(out, destinationId), tries: (out.tries ?? []).map(attemptOf), payload: out.payload || undefined }
}

/**
 * How many destinations the plan allows (max_webhook_endpoints), or null when the plan cannot
 * be read here: somebody without billing access still adds destinations.
 */
export function useWebhookLimit() {
  const billing = useBillingStore()
  onMounted(async () => {
    if (billing.billing) return
    try {
      await billing.loadBilling()
    } catch {
      // Not theirs to read: no limit is shown.
    }
  })
  return computed<number | null>(() => billing.billing?.plan.entitlements?.max_webhook_endpoints ?? null)
}
