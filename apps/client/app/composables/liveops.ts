/**
 * The project's Live Ops events and every write to them: create, edit, activate, pause, resume,
 * duplicate and archive. Kept in this browser until the dashboard is wired to
 * `/liveops-events`, and seeded with a game's year of events. Every write is recorded on the
 * event's own history, which is what answers "who changed the Halloween dates, and when".
 *
 * Nothing is deleted, as the API deletes nothing: archiving keeps the event and its results.
 * The key is fixed at creation, because the game reads the event by it.
 */
let shared: Ref<LiveOpsEventDef[]> | null = null

function storedEvents(): Ref<LiveOpsEventDef[]> {
  shared ??= effectScope(true).run(() => useLocalStorage<LiveOpsEventDef[]>('playersense.liveops.v1', seedLiveOps, { deep: false }))!
  return shared
}

export type LiveOpsDraft = Omit<LiveOpsEventDef, 'created_at' | 'updated_at' | 'updated_by' | 'history' | 'salt'>

export function useLiveOps() {
  const stored = storedEvents()

  const events = computed<LiveOpsEventDef[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.key === 'string' && typeof entry.schedule?.mode === 'string' && Array.isArray(entry.history))
    return valid ? value : seedLiveOps()
  })

  const eventByKey = (key: string) => events.value.find(entry => entry.key === key) ?? null

  function write(key: string, change: (event: LiveOpsEventDef) => LiveOpsEventDef, text: string) {
    const now = new Date().toISOString()
    stored.value = events.value.map(entry => (entry.key === key
      ? { ...change(entry), updated_at: now, updated_by: CURRENT_ACTOR, history: [...entry.history, { at: now, actor: CURRENT_ACTOR, text }] }
      : entry))
  }

  /** Why a key cannot be used, or null. */
  function keyProblem(key: string): string | null {
    if (key.length < 2) return 'A key needs two characters or more.'
    if (!KEY_PATTERN.test(key)) return 'Letters, digits, dots, dashes and underscores, starting with a letter or a digit.'
    /* /growth/liveops/new is the page a new event is made on. */
    if (key === 'new') return '"new" is kept for the page a new event is made on. Choose another key.'
    if (eventByKey(key)) return `An event with the key "${key}" already exists, archived ones included.`
    return null
  }

  /** A new event, in draft unless it is activated as it is made. */
  function create(draft: LiveOpsDraft) {
    const now = new Date().toISOString()
    const activated = draft.status === 'active'
    stored.value = [...events.value, {
      ...draft,
      salt: '',
      created_at: now,
      updated_at: now,
      updated_by: CURRENT_ACTOR,
      history: [
        { at: now, actor: CURRENT_ACTOR, text: 'Created' },
        ...(activated ? [{ at: now, actor: CURRENT_ACTOR, text: `Activated: ${scheduleWords(draft.schedule).replace(/^./, first => first.toLowerCase())}` }] : [])
      ]
    }]
  }

  /** Saves an edit, with what it changed, a line each. */
  function update(key: string, next: LiveOpsDraft) {
    const before = eventByKey(key)
    if (!before) return
    const changes: string[] = []
    if (before.name !== next.name) changes.push(`Name: ${next.name}`)
    if (before.kind !== next.kind) changes.push(`Kind: ${kindLook(before.kind).label} → ${kindLook(next.kind).label}`)
    if (!sameJson(before.schedule, next.schedule)) changes.push(`Schedule: ${scheduleWords(next.schedule)}`)
    if (!sameJson(before.audience, next.audience)) changes.push('Audience changed')
    if (before.rollout !== next.rollout) changes.push(`Rollout ${formatRollout(before.rollout)} → ${formatRollout(next.rollout)}`)
    if (before.priority !== next.priority) changes.push(`Priority ${before.priority} → ${next.priority}`)
    if (!sameJson(before.payload, next.payload)) changes.push('Payload changed')
    if (before.goal !== next.goal || !sameJson(before.watch, next.watch)) changes.push('Measurement changed')
    if (before.description !== next.description && !changes.length) changes.push('Description changed')
    write(key, entry => ({ ...entry, ...next, key: entry.key, status: entry.status }), changes.join('. ') || 'Saved with no change')
  }

  function activate(key: string) {
    const event = eventByKey(key)
    write(key, entry => ({ ...entry, status: 'active' }), event ? `Activated: ${scheduleWords(event.schedule).replace(/^./, first => first.toLowerCase())}` : 'Activated')
  }

  function pause(key: string, reason: string) {
    write(key, entry => ({ ...entry, status: 'paused' }), `Paused${reason ? `: ${reason}` : ''}`)
  }

  function resume(key: string) {
    write(key, entry => ({ ...entry, status: 'active' }), 'Resumed')
  }

  function archive(key: string) {
    write(key, entry => ({ ...entry, status: 'archived' }), 'Archived')
  }

  function restore(key: string) {
    write(key, entry => ({ ...entry, status: 'draft' }), 'Restored to draft')
  }

  /** A copy in draft under a new key: next year's event, or the same offer for another country. */
  function duplicate(key: string, nextKey: string, name: string) {
    const event = eventByKey(key)
    if (!event) return
    const { created_at: _c, updated_at: _u, updated_by: _b, history: _h, salt: _s, ...rest } = event
    create({ ...JSON.parse(JSON.stringify(rest)) as LiveOpsDraft, key: nextKey, name, status: 'draft' })
  }

  return { events, eventByKey, keyProblem, create, update, activate, pause, resume, archive, restore, duplicate }
}
