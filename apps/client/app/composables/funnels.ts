import type { FunnelDefinition } from '~/utils/funnel-sample'

/**
 * The project's saved funnels. Kept in this browser until funnels are saved server-side
 * (project.md §31 asks for them to be canonical definitions the server can cache by), and
 * seeded with a few a new project would want.
 *
 * Every read is checked, so a stored funnel from an older shape, or one hand-edited into
 * nonsense, is dropped rather than drawn; with nothing left, the samples come back.
 */
export function useSavedFunnels() {
  const stored = useLocalStorage<FunnelDefinition[]>('playersense.funnels', () => SAMPLE_FUNNELS.map(copyFunnel))

  const funnels = computed<FunnelDefinition[]>(() => {
    const valid = Array.isArray(stored.value) ? stored.value.filter(isFunnel) : []
    return valid.length ? valid : SAMPLE_FUNNELS.map(copyFunnel)
  })

  /** Writes the funnel over the saved one with its id. */
  function save(funnel: FunnelDefinition) {
    stored.value = funnels.value.map(entry => (entry.id === funnel.id ? copyFunnel(funnel) : entry))
  }

  /** Saves a copy under a new id and name, and hands the id back. */
  function create(funnel: FunnelDefinition, name: string): string {
    const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'funnel'
    const id = `${slug}-${Date.now().toString(36)}`
    stored.value = [...funnels.value, { ...copyFunnel(funnel), id, name }]
    return id
  }

  function remove(id: string) {
    stored.value = funnels.value.filter(entry => entry.id !== id)
  }

  return { funnels, save, create, remove }
}

export function copyFunnel(funnel: FunnelDefinition): FunnelDefinition {
  return {
    ...funnel,
    steps: funnel.steps.map(step => ({ event: step.event, where: step.where.map(condition => ({ ...condition })) }))
  }
}

function isFunnel(value: unknown): value is FunnelDefinition {
  const funnel = value as Partial<FunnelDefinition> | null
  return typeof funnel?.id === 'string'
    && typeof funnel.name === 'string'
    && typeof funnel.window === 'string' && funnel.window in WINDOW_LABELS
    && Array.isArray(funnel.steps) && funnel.steps.length >= MIN_STEPS && funnel.steps.length <= MAX_STEPS
    && funnel.steps.every(step => typeof step?.event === 'string' && Array.isArray(step.where))
}
