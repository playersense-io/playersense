import type { Schema } from '@playersense/contracts'
import type { FunnelDefinition, FunnelSample, FunnelSeries, ProgressionSample } from '~/utils/funnel-sample'

// Funnels, from the API: the project's saved funnels (…/projects/{project}/funnels), and what a
// funnel comes to over a range (…/analytics/{env}/funnels/query), in the shapes the funnel
// reports draw. A funnel's players are the ones who did its first step in the range; the range
// before is asked as well, for the change.

type SavedFunnelOut = Schema<'SavedFunnelOut'>

const DAY_MS = 86_400_000

function definitionOf(out: SavedFunnelOut): FunnelDefinition {
  const window = (out.window || '1d') as FunnelDefinition['window']
  const progression = out.progression as { event: string, property: string, from: number, to: number, where?: FunnelCondition[] } | undefined
  if (out.kind === 'progression' && progression) {
    return {
      id: out.id,
      name: out.name,
      kind: 'progression',
      window,
      event: progression.event,
      property: progression.property,
      from: progression.from,
      to: progression.to,
      where: (progression.where ?? []).map(condition => ({ ...condition }))
    }
  }
  return {
    id: out.id,
    name: out.name,
    kind: 'steps',
    window,
    steps: (out.steps ?? []).map(step => ({ event: step.event, where: (step.where ?? []).map(condition => ({ ...condition })) }))
  }
}

/** What the API is sent for a funnel: its kind, window and steps or ladder. */
export function funnelBody(funnel: FunnelDefinition, breakdown = 'none') {
  if (funnel.kind === 'progression') {
    return {
      kind: 'progression',
      window: funnel.window,
      progression: { event: funnel.event, property: funnel.property, from: funnel.from, to: funnel.to, where: funnel.where }
    }
  }
  return {
    kind: 'steps',
    window: funnel.window,
    steps: funnel.steps.map(step => ({ event: step.event, where: step.where })),
    ...(breakdown === 'none' ? {} : { breakdown })
  }
}

/** The project's saved funnels, and every write to them. */
export function useSavedFunnels() {
  const workspace = useWorkspaceStore()
  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/funnels`)

  const list = useApiGet<Schema<'SavedFunnelListOut'>>(path)
  const funnels = computed<FunnelDefinition[]>(() => (list.data.value?.funnels ?? []).map(definitionOf))
  const loading = computed(() => list.isFetching.value && !list.data.value)
  const error = computed(() => list.error.value as ApiError | null)

  /** Writes the funnel over the saved one with its id. */
  async function save(funnel: FunnelDefinition) {
    await apiRequest('PUT', `${path.value}/${funnel.id}`, { name: funnel.name, ...funnelBody(funnel) })
    await list.execute()
  }

  /** Saves a copy under a new name, and hands its id back. */
  async function create(funnel: FunnelDefinition, name: string): Promise<string> {
    const out = await apiRequest<SavedFunnelOut>('POST', path.value!, { name, ...funnelBody(funnel) })
    await list.execute()
    return out.id
  }

  async function remove(id: string) {
    await apiRequest('DELETE', `${path.value}/${id}`)
    await list.execute()
  }

  return { funnels, loading, error, reload: () => list.execute(), save, create, remove }
}

export function copyFunnel(funnel: FunnelDefinition): FunnelDefinition {
  const where = (conditions: readonly FunnelCondition[]) => conditions.map(condition => ({ ...condition }))
  if (funnel.kind === 'progression') return { ...funnel, where: where(funnel.where) }
  return { ...funnel, steps: funnel.steps.map(step => ({ event: step.event, where: where(step.where) })) }
}

// --- The answer ---------------------------------------------------------------------------------

/** The range's days ending today, and the same number before. */
function rangeOf(range: FunnelRange) {
  const days = Number.parseInt(range, 10)
  const today = Date.parse(`${utcDay(Date.now())}T00:00:00Z`)
  return {
    from: utcDay(today - (days - 1) * DAY_MS),
    to: utcDay(today),
    previousFrom: utcDay(today - (2 * days - 1) * DAY_MS),
    previousTo: utcDay(today - days * DAY_MS)
  }
}

/** Seconds a funnel window gives a player; a session is counted as a day for settling. */
const WINDOW_SECONDS: Record<string, number> = { 'session': 86_400, '1h': 3_600, '1d': 86_400, '7d': 7 * 86_400, '30d': 30 * 86_400 }

function seriesOf(out: Schema<'FunnelSeriesOut'>, name: string): FunnelSeries {
  return {
    name,
    players: out.players.map(value => value ?? null),
    medians: [null, ...out.median_wait_seconds.map(value => value ?? null)]
  }
}

/** "Under 1m", "1h to 1d", "Over 7d". */
function spanWords(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3_600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86_400) return `${Math.round(seconds / 3_600)}h`
  return `${Math.round(seconds / 86_400)}d`
}

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const dayTitle = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })

function sampleOf(now: Schema<'FunnelOut'>, before: Schema<'FunnelOut'> | null, range: FunnelRange, window: string): FunnelSample {
  const weekly = range === '90d'
  const settleMs = (WINDOW_SECONDS[window] ?? 86_400) * 1_000
  const days = [...(now.trend ?? [])].sort((a, b) => Date.parse(a.day) - Date.parse(b.day))
  const points: { at: number, entered: number | null, converted: number | null }[] = []
  for (const day of days) {
    const at = Date.parse(day.day)
    const last = points[points.length - 1]
    if (weekly && last && at - last.at < 7 * DAY_MS) {
      last.entered = day.entered == null || last.entered === null ? null : last.entered + day.entered
      last.converted = day.converted == null || last.converted === null ? null : last.converted + day.converted
    } else {
      points.push({ at, entered: day.entered ?? null, converted: day.converted ?? null })
    }
  }
  const span = weekly ? 7 * DAY_MS : DAY_MS
  return {
    total: seriesOf(now.total, 'All players'),
    previous: before ? seriesOf(before.total, 'The range before') : { name: 'The range before', players: [], medians: [] },
    breakdown: (now.breakdown ?? []).map(entry => seriesOf(entry, entry.value || 'Unknown')),
    trend: {
      categories: points.map(point => dayTick.format(point.at)),
      titles: points.map(point => (weekly ? `Week of ${dayTick.format(point.at)}` : dayTitle.format(point.at))),
      settled: points.map(point => point.at + span + settleMs <= Date.now()),
      series: [{ name: 'All players', rates: points.map(point => (point.entered && point.converted !== null ? point.converted / point.entered : null)) }]
    },
    timeToConvert: (now.time_to_convert ?? []).map(bucket => ({
      label: !bucket.from_seconds ? `Under ${spanWords(bucket.to_seconds ?? 0)}` : bucket.to_seconds ? `${spanWords(bucket.from_seconds)} to ${spanWords(bucket.to_seconds)}` : `Over ${spanWords(bucket.from_seconds)}`,
      players: bucket.players ?? 0
    })),
    medianToConvert: now.median_to_convert_seconds ?? null
  }
}

function progressionOf(now: Schema<'ProgressionOut'>, before: Schema<'ProgressionOut'> | null): ProgressionSample {
  const rows = now.rows.map(row => ({ value: row.value, players: row.reached ?? null, tries: row.tries ?? null, toNext: null }))
  const previousRows = before?.rows ?? []
  return {
    rows,
    previous: {
      first: previousRows[0]?.reached ?? null,
      last: previousRows[previousRows.length - 1]?.reached ?? null
    },
    medianToLast: now.median_to_last_seconds ?? null
  }
}

/**
 * What a funnel comes to over a range, asked again as the funnel or the range changes. A
 * breakdown is asked only of the range itself; the range before is every player.
 */
export function useFunnelAnswer(definition: () => FunnelDefinition, range: () => FunnelRange, breakdown: () => string = () => 'none') {
  const workspace = useWorkspaceStore()

  const steps = shallowRef<FunnelSample | null>(null)
  const progression = shallowRef<ProgressionSample | null>(null)
  const threshold = ref(5)
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  let asked = 0

  async function load() {
    const path = workspace.analyticsPath
    const funnel = definition()
    if (!path) return
    const ask = ++asked
    const { from, to, previousFrom, previousTo } = rangeOf(range())
    loading.value = true
    error.value = null
    try {
      const query = (body: object, start: string, end: string) =>
        apiRequest<Schema<'FunnelAnswerOut'>>('POST', `${path}/funnels/query`, { from: start, to: end, ...body })
      const [now, before] = await Promise.all([
        query(funnelBody(funnel, breakdown()), from, to),
        query(funnelBody(funnel), previousFrom, previousTo)
      ])
      if (ask !== asked) return
      if (funnel.kind === 'progression') {
        const answer = now.progression as Schema<'ProgressionOut'> | undefined
        progression.value = answer ? progressionOf(answer, (before.progression as Schema<'ProgressionOut'> | undefined) ?? null) : null
        threshold.value = answer?.suppression_threshold ?? 5
      } else {
        const answer = now.steps as Schema<'FunnelOut'> | undefined
        steps.value = answer ? sampleOf(answer, (before.steps as Schema<'FunnelOut'> | undefined) ?? null, range(), funnel.window) : null
        threshold.value = answer?.suppression_threshold ?? 5
      }
    } catch (caught) {
      if (ask === asked) error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      if (ask === asked) loading.value = false
    }
  }

  /* Asked again when anything the question is made of changes, a moment after the last edit. */
  watchDebounced(() => [JSON.stringify(funnelBody(definition(), breakdown())), range(), workspace.analyticsPath], load, { debounce: 400, immediate: true })

  return { steps, progression, threshold, loading, error, reload: load }
}
