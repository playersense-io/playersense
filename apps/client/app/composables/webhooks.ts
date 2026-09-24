/**
 * The project's webhook destinations and every write to them: add, edit, enable, disable,
 * delete, roll the signing secret, send a test event and resend a delivery. Kept in this
 * browser until the dashboard is wired to `/webhook-endpoints`, and seeded with a studio's
 * usual five. Every write is recorded on the destination's own history.
 *
 * Deliveries are the sample's (utils/webhooks-sample.ts), with the ones made here on top:
 * test events, and the tries a resend adds to a delivery.
 */
let shared: Ref<WebhookDestination[]> | null = null
let sharedTests: Ref<WebhookDelivery[]> | null = null
let sharedResends: Ref<Record<string, DeliveryAttempt[]>> | null = null

function stores() {
  const scope = effectScope(true)
  shared ??= scope.run(() => useLocalStorage<WebhookDestination[]>('playersense.webhooks.v1', seedWebhooks, { deep: false }))!
  sharedTests ??= scope.run(() => useLocalStorage<WebhookDelivery[]>('playersense.webhooks.tests.v1', [], { deep: false }))!
  sharedResends ??= scope.run(() => useLocalStorage<Record<string, DeliveryAttempt[]>>('playersense.webhooks.resends.v1', {}, { deep: false }))!
  return { stored: shared, tests: sharedTests, resends: sharedResends }
}

export type WebhookDraft = Pick<WebhookDestination, 'name' | 'description' | 'url' | 'events' | 'environments' | 'version' | 'headers'>

/** The plan's cap on destinations (the max_webhook_endpoints entitlement). */
export const WEBHOOK_LIMIT = 10

export function useWebhooks() {
  const { stored, tests, resends } = stores()

  const destinations = computed<WebhookDestination[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && Array.isArray(entry.events) && typeof entry.secret?.value === 'string')
    return valid ? value : seedWebhooks()
  })

  const destinationById = (id: string) => destinations.value.find(entry => entry.id === id) ?? null

  /** Enabled and answering, enabled and failing, or off. */
  function healthOfDestination(destination: WebhookDestination, now: number): WebhookHealth {
    if (destination.status === 'disabled') return 'disabled'
    return failingSince(destination, now) !== null ? 'failing' : 'healthy'
  }

  /** A destination's deliveries, newest first: the sample's, its tests, and every resend. */
  function deliveriesOf(destination: WebhookDestination, now: number): WebhookDelivery[] {
    const own = tests.value.filter(entry => entry.destinationId === destination.id)
    const all = own.length ? [...own, ...sampleDeliveries(destination, now)].sort((a, b) => b.created - a.created) : sampleDeliveries(destination, now)
    const extra = resends.value
    if (!Object.keys(extra).length) return all
    return all.map((entry) => {
      const added = extra[entry.id]
      if (!added?.length) return entry
      const attempts = [...entry.attempts, ...added]
      const ok = answerOk(added.at(-1)!)
      return { ...entry, attempts, status: ok ? 'succeeded' : entry.status, ...(ok ? { next: undefined } : {}) }
    })
  }

  function write(id: string, change: (destination: WebhookDestination) => WebhookDestination, text: string) {
    const now = new Date().toISOString()
    stored.value = destinations.value.map(entry => (entry.id === id
      ? { ...change(entry), updated_at: now, updated_by: CURRENT_ACTOR, history: [...entry.history, { at: now, actor: CURRENT_ACTOR, text }] }
      : entry))
  }

  /** A new destination, enabled, with a secret of its own. Returns its id. */
  function create(draft: WebhookDraft): string {
    const now = new Date().toISOString()
    const id = newDestinationId()
    stored.value = [...destinations.value, {
      ...draft,
      id,
      status: 'enabled',
      secret: { value: newSecret(), created_at: now },
      created_at: now,
      updated_at: now,
      updated_by: CURRENT_ACTOR,
      history: [{ at: now, actor: CURRENT_ACTOR, text: `Created, listening to ${listeningWords(draft.events)}` }]
    }]
    return id
  }

  /** Saves an edit, with what it changed, a line each. */
  function update(id: string, next: WebhookDraft) {
    const before = destinationById(id)
    if (!before) return
    const changes: string[] = []
    if (before.name !== next.name) changes.push(`Renamed ${next.name}`)
    if (before.url !== next.url) changes.push(`URL ${next.url}`)
    if (!sameJson(before.events, next.events)) changes.push(`Now listening to ${listeningWords(next.events)}`)
    if (!sameJson(before.environments, next.environments)) changes.push(`Environments: ${next.environments.map(entry => ENVIRONMENT_LABELS[entry]).join(', ')}`)
    if (before.version !== next.version) changes.push(`Payload version ${before.version} → ${next.version}`)
    if (!sameJson(before.headers, next.headers)) changes.push('Headers changed')
    if (before.description !== next.description && !changes.length) changes.push('Description changed')
    write(id, entry => ({ ...entry, ...next }), changes.join('. ') || 'Saved with no change')
  }

  function enable(id: string) {
    write(id, entry => ({ ...entry, status: 'enabled', disabled_reason: undefined }), 'Enabled')
  }

  function disable(id: string) {
    write(id, entry => ({ ...entry, status: 'disabled', disabled_reason: 'manual' }), 'Disabled')
  }

  /** Deleted for good: its deliveries and its secret with it. */
  function remove(id: string) {
    stored.value = destinations.value.filter(entry => entry.id !== id)
    tests.value = tests.value.filter(entry => entry.destinationId !== id)
  }

  /**
   * A new secret, signing from now. The old one signs beside it until `overlap` has passed,
   * so a server can be moved to the new one with no delivery refused; 0 ends it at once.
   */
  function rollSecret(id: string, overlap: number) {
    const now = Date.now()
    write(id, entry => ({
      ...entry,
      secret: {
        value: newSecret(),
        created_at: new Date(now).toISOString(),
        ...(overlap > 0 ? { previous: { value: entry.secret.value, expires_at: new Date(now + overlap).toISOString() } } : {})
      }
    }), overlap > 0 ? `Signing secret rolled. The old one signs too for ${waitWords(overlap)}` : 'Signing secret rolled. The old one stopped at once')
  }

  /** What the destination's server would answer now: a failing one fails. */
  function tryNow(destination: WebhookDestination, now: number, manual: boolean): DeliveryAttempt {
    if (destination.status === 'enabled' && failingSince(destination, now) !== null) {
      return { at: now, code: 500, ms: 212, body: '{"error":"internal","message":"deletion ledger is read-only (maintenance)"}', manual }
    }
    return { at: now, code: 200, ms: 90 + Math.round(Math.random() * 180), body: '{"received":true}', manual }
  }

  /** A made-up event of this type, sent to one destination now. Returns the delivery. */
  function sendTest(destination: WebhookDestination, type: string, environment: EnvironmentKind): WebhookDelivery {
    const now = Date.now()
    const attempt = tryNow(destination, now, true)
    const delivery: WebhookDelivery = {
      id: `whd_test${Math.random().toString(36).slice(2, 14)}`,
      destinationId: destination.id,
      eventId: `evt_test${Math.random().toString(36).slice(2, 18)}`,
      type,
      environment,
      created: now,
      status: answerOk(attempt) ? 'succeeded' : 'retrying',
      attempts: [attempt],
      test: true,
      ...(answerOk(attempt) ? {} : { next: now + RETRY_AFTER_MS[0] })
    }
    tests.value = [delivery, ...tests.value].slice(0, 50)
    return delivery
  }

  /** One more try of a delivery, now, with the same event id: the server sees a repeat. */
  function resend(destination: WebhookDestination, delivery: WebhookDelivery): DeliveryAttempt {
    const attempt = tryNow(destination, Date.now(), true)
    resends.value = { ...resends.value, [delivery.id]: [...(resends.value[delivery.id] ?? []), attempt] }
    return attempt
  }

  return { destinations, destinationById, healthOfDestination, deliveriesOf, create, update, enable, disable, remove, rollSecret, sendTest, resend }
}
