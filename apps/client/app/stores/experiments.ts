import { defineStore } from 'pinia'
import type { Schema } from '@playersense/contracts'

// The project's experiments (…/experiments) and what each has measured
// (…/analytics/production/experiments/{flag}), shared by the list, one experiment's page and
// the calendar. An experiment's history is the API's timeline: every start, ramp, stop and
// decision, with who made it.
//
// Results are read per flag, over the days the test has run: each player counted in the arm of
// their first exposure and measured over the seven days from it. An arm is matched to what the
// API reports by the value it serves.

type ExperimentRecord = Schema<'ExperimentRecordOut'>
type ExperimentAnalytics = Schema<'ExperimentOut'>

const MICROS = 1_000_000

function experimentOf(out: ExperimentRecord): ExperimentDef {
  return {
    id: out.id,
    name: out.name,
    hypothesis: out.hypothesis ?? '',
    flagKey: out.flag,
    audience: out.audience_label ?? '',
    traffic: out.traffic,
    status: out.status as ExperimentStatus,
    primaryMetric: out.primary_metric as MetricKey,
    guardrails: [...(out.guardrails ?? [])] as MetricKey[],
    variants: (out.arms ?? []).map(arm => ({
      id: arm.id,
      name: arm.name ?? '',
      value: arm.value as unknown as Json,
      weight: arm.weight,
      control: arm.control ?? false
    })),
    plannedDays: out.planned_days,
    created_at: out.created_at,
    started_at: out.started_at ?? undefined,
    ended_at: out.ended_at ?? undefined,
    winner: out.winner ?? null,
    shipped: out.shipped,
    conclusion: out.conclusion || undefined,
    events: (out.timeline ?? []).map(entry => ({ at: entry.at, actor: entry.actor, text: entry.text }))
  }
}

const EMPTY_STAT: VariantStat = { n: 0, mean: 0, sd: 0 }

/** The API's arms against the experiment's variants: each variant's players and figures. */
function resultsOf(experiment: ExperimentDef, out: ExperimentAnalytics): ExperimentResults | null {
  const armOf = (text: string, published: boolean) => (published
    ? experiment.variants.find(variant => sameJson(variant.value, (() => {
        try {
          return JSON.parse(text) as Json
        } catch {
          return text
        }
      })()))
    : undefined)

  const variants: VariantResult[] = experiment.variants.map((variant) => {
    const row = (out.variants ?? []).find(entry => armOf(entry.variant, entry.published)?.id === variant.id)
    const stat = (value?: { mean: number, n: number, sd: number } | null, scale = 1): VariantStat =>
      value ? { n: value.n, mean: value.mean / scale, sd: value.sd / scale } : EMPTY_STAT
    const retention = (value: unknown) => stat(value as { mean: number, n: number, sd: number } | null | undefined)
    return {
      variant,
      players: row?.players ?? 0,
      stats: {
        revenue_per_player: stat(row?.revenue_per_player, MICROS),
        payer_conversion: stat(row?.payer_conversion),
        d1_retention: retention(row?.d1_retention),
        d7_retention: retention(row?.d7_retention),
        sessions_per_player: stat(row?.sessions_per_player),
        playtime_minutes: stat(row?.playtime_minutes),
        ad_revenue_per_player: EMPTY_STAT
      }
    }
  })

  const dayKeys = [...new Set((out.daily ?? []).map(entry => entry.day))].sort()
  const daily: ExperimentResults['daily'] = {}
  for (const variant of experiment.variants) {
    const rows = new Map((out.daily ?? []).filter(entry => armOf(entry.variant, entry.published)?.id === variant.id).map(entry => [entry.day, entry]))
    // Each day is the players who entered on it; the chart reads a running mean over the days so far.
    let players = 0
    let revenue = 0
    let payers = 0
    const cumulative = { players: [] as number[], revenue: [] as number[], conversion: [] as number[] }
    for (const day of dayKeys) {
      const row = rows.get(day)
      if (row) {
        players += row.players
        revenue += row.players * row.revenue_per_player / MICROS
        payers += row.players * row.payer_conversion
      }
      cumulative.players.push(players)
      cumulative.revenue.push(players ? revenue / players : 0)
      cumulative.conversion.push(players ? payers / players : 0)
    }
    daily[variant.id] = {
      players: cumulative.players,
      metrics: {
        revenue_per_player: cumulative.revenue,
        payer_conversion: cumulative.conversion,
        d1_retention: [],
        d7_retention: [],
        sessions_per_player: [],
        playtime_minutes: [],
        ad_revenue_per_player: []
      }
    }
  }

  return { days: dayKeys.map(day => Date.parse(day)), variants, daily }
}

const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)

export const useExperimentsStore = defineStore('experiments', () => {
  const workspace = useWorkspaceStore()

  const experiments = shallowRef<ExperimentDef[]>([])
  const results = shallowRef<Record<string, ExperimentResults | null>>({})
  const reading = new Set<string>()
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  const loadedFor = ref<string | null>(null)

  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/experiments`)

  function put(out: ExperimentRecord) {
    const next = experimentOf(out)
    experiments.value = experiments.value.some(entry => entry.id === next.id)
      ? experiments.value.map(entry => (entry.id === next.id ? next : entry))
      : [...experiments.value, next]
    return next
  }

  async function load(force = false) {
    if (!path.value || loading.value) return
    if (!force && loadedFor.value === path.value) return
    loading.value = true
    error.value = null
    try {
      const out = await apiRequest<Schema<'ExperimentRecordListOut'>>('GET', path.value)
      experiments.value = (out.experiments ?? []).map(experimentOf)
      results.value = {}
      loadedFor.value = path.value
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      loading.value = false
    }
  }

  /** What a started test has measured, read once and kept; null before it starts or while it is read. */
  function resultsFor(experiment: ExperimentDef): ExperimentResults | null {
    if (!experiment.started_at || !workspace.projectPath) return null
    if (experiment.id in results.value) return results.value[experiment.id] ?? null
    if (!reading.has(experiment.id)) {
      reading.add(experiment.id)
      const to = experiment.ended_at ? Date.parse(experiment.ended_at) : Date.now()
      const from = Math.max(Date.parse(experiment.started_at), to - 399 * 86_400_000)
      apiRequest<ExperimentAnalytics>('GET', withQuery(`${workspace.projectPath}/analytics/production/experiments/${encodeURIComponent(experiment.flagKey)}`, { from: isoDay(from), to: isoDay(to) }))
        .then(out => (results.value = { ...results.value, [experiment.id]: resultsOf(experiment, out) }))
        .catch(() => (results.value = { ...results.value, [experiment.id]: null }))
        .finally(() => reading.delete(experiment.id))
    }
    return null
  }

  /** Runs a write; the experiment as the API answers it, or the refusal in its words. */
  async function write(run: () => Promise<ExperimentRecord>): Promise<{ experiment: ExperimentDef | null, problem: string | null }> {
    try {
      const experiment = put(await run())
      const { [experiment.id]: _dropped, ...kept } = results.value
      results.value = kept
      return { experiment, problem: null }
    } catch (caught) {
      return { experiment: null, problem: caught instanceof Error ? caught.message : String(caught) }
    }
  }

  const bodyOf = (draft: ExperimentDraft) => ({
    name: draft.name.trim(),
    hypothesis: draft.hypothesis.trim(),
    audience_label: draft.audience.trim(),
    traffic: draft.traffic,
    primary_metric: draft.primaryMetric,
    guardrails: draft.guardrails,
    arms: draft.variants.map(variant => ({ id: variant.id, name: variant.name, value: variant.value, weight: variant.weight, control: variant.control })),
    planned_days: draft.plannedDays
  })

  const create = (draft: ExperimentDraft) => write(() => apiRequest<ExperimentRecord>('POST', path.value!, { flag: draft.flagKey, ...bodyOf(draft) }))
  const start = (id: string) => write(() => apiRequest<ExperimentRecord>('POST', `${path.value}/${id}/start`))
  const setTraffic = (id: string, traffic: number) => write(() => apiRequest<ExperimentRecord>('PUT', `${path.value}/${id}/traffic`, { traffic }))
  const stop = (id: string, reason: string) => write(() => apiRequest<ExperimentRecord>('POST', `${path.value}/${id}/stop`, { reason: reason.trim() }))
  const conclude = (id: string, winner: string | null, conclusion: string) =>
    write(() => apiRequest<ExperimentRecord>('POST', `${path.value}/${id}/conclude`, { winner, conclusion: conclusion.trim() }))
  const ship = (id: string) => write(() => apiRequest<ExperimentRecord>('POST', `${path.value}/${id}/ship`))

  async function remove(id: string): Promise<string | null> {
    try {
      await apiRequest('DELETE', `${path.value}/${id}`)
      experiments.value = experiments.value.filter(entry => entry.id !== id)
      return null
    } catch (caught) {
      return caught instanceof Error ? caught.message : String(caught)
    }
  }

  watch(path, () => {
    loadedFor.value = null
    experiments.value = []
    results.value = {}
    void load()
  })

  return { experiments, results, loading, error, load, resultsFor, create, start, setTraffic, stop, conclude, ship, remove }
})
