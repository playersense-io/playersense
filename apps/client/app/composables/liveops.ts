/**
 * The project's Live Ops events and every write to them: create, edit, activate, pause, resume,
 * duplicate and archive, each a write to `/liveops-events` (stores/liveops.ts). A write resolves
 * with the API's refusal in its words, or null.
 *
 * Nothing is deleted, as the API deletes nothing: archiving keeps the event and its results.
 * The key is fixed at creation, because the game reads the event by it.
 */
export type LiveOpsDraft = Omit<LiveOpsEventDef, 'created_at' | 'updated_at' | 'updated_by' | 'history' | 'salt'>

export function useLiveOps() {
  const store = useLiveOpsStore()
  const { events, loading, error } = storeToRefs(store)

  void store.load()

  const eventByKey = (key: string) => events.value.find(entry => entry.key === key) ?? null

  /** Why a key cannot be used, or null. */
  function keyProblem(key: string): string | null {
    if (key.length < 2) return 'A key needs two characters or more.'
    if (!KEY_PATTERN.test(key)) return 'Letters, digits, dots, dashes and underscores, starting with a letter or a digit.'
    /* /growth/liveops/new is the page a new event is made on. */
    if (key === 'new') return '"new" is kept for the page a new event is made on. Choose another key.'
    if (eventByKey(key)) return `An event with the key "${key}" already exists, archived ones included.`
    return null
  }

  const activate = (key: string) => store.setStatus(key, 'active')
  /** The API keeps no reason for a pause: the event is paused, and who did it is on it. */
  const pause = (key: string) => store.setStatus(key, 'paused')
  const resume = (key: string) => store.setStatus(key, 'active')
  const archive = (key: string) => store.setStatus(key, 'archived')
  const restore = (key: string) => store.setStatus(key, 'draft')

  /** A copy in draft under a new key: next year's event, or the same offer for another country. */
  function duplicate(key: string, nextKey: string, name: string): Promise<string | null> {
    const event = eventByKey(key)
    if (!event) return Promise.resolve(`There is no event "${key}".`)
    const { created_at: _c, updated_at: _u, updated_by: _b, history: _h, salt: _s, ...rest } = event
    return store.create({ ...JSON.parse(JSON.stringify(rest)) as LiveOpsDraft, key: nextKey, name, status: 'draft' })
  }

  return {
    events,
    loading,
    error,
    reload: () => store.load(true),
    impactFor: store.impactFor,
    gameDay: store.gameDay,
    eventByKey,
    keyProblem,
    create: store.create,
    update: store.update,
    activate,
    pause,
    resume,
    archive,
    restore,
    duplicate
  }
}
