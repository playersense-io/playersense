/**
 * The project's experiments and every write to them: create, start, stop, finish, and ship a
 * winner. Kept in this browser until the API has experiments, and seeded with a game's recent
 * tests. Every write is recorded on the experiment's own history, which is what answers "when
 * did we try this, and what happened" months later.
 *
 * Shipping a winner is the one write that reaches past the experiment: it makes the winning
 * value the flag's fallback and drops the rules that split players between variants, as an
 * ordinary save to the draft (useLiveConfig), to be published like any other edit.
 */
let shared: Ref<ExperimentDef[]> | null = null

/* One ref for the app, in a detached scope, so its write to storage outlives any one screen. */
function storedExperiments(): Ref<ExperimentDef[]> {
  shared ??= effectScope(true).run(() => useLocalStorage<ExperimentDef[]>('playersense.experiments', seedExperiments, { deep: false }))!
  return shared
}

export function useExperiments() {
  const stored = storedExperiments()
  const { flagByKey, saveFlag } = useLiveConfig()

  const experiments = computed<ExperimentDef[]>(() => {
    const value = stored.value
    const valid = Array.isArray(value) && value.every(entry => typeof entry?.id === 'string' && Array.isArray(entry.variants) && Array.isArray(entry.events))
    return valid ? value : seedExperiments()
  })

  const experimentById = (id: string) => experiments.value.find(entry => entry.id === id) ?? null

  function write(id: string, change: (experiment: ExperimentDef) => ExperimentDef, text: string) {
    stored.value = experiments.value.map(entry => (entry.id === id
      ? { ...change(entry), events: [...entry.events, { at: new Date().toISOString(), actor: CURRENT_ACTOR, text }] }
      : entry))
  }

  /** A new test in draft, and its id. */
  function create(draft: Omit<ExperimentDef, 'id' | 'status' | 'created_at' | 'events'>): string {
    const slug = draft.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'experiment'
    const id = `exp-${slug}-${Date.now().toString(36)}`
    const now = new Date().toISOString()
    stored.value = [...experiments.value, { ...draft, id, status: 'draft', created_at: now, events: [{ at: now, actor: CURRENT_ACTOR, text: 'Created' }] }]
    return id
  }

  function start(id: string) {
    const split = experimentById(id)?.variants.map(entry => `${entry.weight}% ${entry.name}`).join(', ') ?? ''
    write(id, entry => ({ ...entry, status: 'running', started_at: new Date().toISOString() }), `Started: ${split}`)
  }

  function stop(id: string, reason: string) {
    write(id, entry => ({ ...entry, status: 'stopped', ended_at: new Date().toISOString(), winner: null, conclusion: reason || undefined }), `Stopped early${reason ? `: ${reason}` : ''}`)
  }

  /** Ends a test with a decision: a winning variant, or null for none. */
  function finish(id: string, winner: string | null, conclusion: string) {
    const name = experimentById(id)?.variants.find(entry => entry.id === winner)?.name
    write(id, entry => ({ ...entry, status: 'concluded', ended_at: entry.ended_at ?? new Date().toISOString(), winner, conclusion: conclusion || undefined }), name ? `Finished: ${name} won` : 'Finished: no clear winner')
  }

  /**
   * Makes the winning variant's value the flag's answer for everybody: the fallback becomes it,
   * and the rules serving other variants' values to a share of players go. Refused, with the
   * reason, where the flag's own checks refuse it.
   */
  function ship(id: string): string | null {
    const experiment = experimentById(id)
    const winner = experiment?.variants.find(entry => entry.id === experiment.winner)
    if (!experiment || !winner) return 'There is no winner to ship.'
    const flag = flagByKey(experiment.flagKey)
    if (!flag) return `Flag "${experiment.flagKey}" does not exist.`
    const values = experiment.variants.map(entry => JSON.stringify(entry.value))
    const problem = saveFlag({
      ...flag,
      fallback: winner.value,
      rules: flag.rules.filter(rule => !(values.includes(JSON.stringify(rule.value)) && rule.rollout < FULL_ROLLOUT))
    })
    if (problem) return problem
    write(id, entry => ({ ...entry, shipped: true }), `Shipped ${winner.name}: ${experiment.flagKey} now serves ${writeValue(winner.value)} to everybody`)
    return null
  }

  return { experiments, experimentById, create, start, stop, finish, ship }
}
