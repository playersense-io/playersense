import { defineStore } from 'pinia'
import type { Schema } from '@playersense/contracts'

// The project's Live Ops events (…/liveops-events), what each window did
// (…/analytics/production/liveops-events/{key}/impact) and the game's own days
// (…/analytics/production/metrics/query), shared by the list, one event's page and the calendar.
//
// The API keeps an event, not its history: who created it and who last saved it. The history
// here is those two, with the last save read as the pause or the archive when that is where the
// event stands, which is also what says when it stopped being served.
//
// A window's impact is measured against what the same weekdays did in the four weeks before.
// The API gives the lift; how sure it is comes from how far the days before the window strayed
// from their own expectation, which is the noise any lift has to stand out from.

type EventRecord = Schema<'LiveOpsEventOut'>
type ImpactRecord = Schema<'EventImpactOut'>

const DAY = 86_400_000
const MEASURED: LiveOpsMetricKey[] = ['players', 'new_players', 'revenue', 'arpdau', 'payer_conversion', 'sessions_per_player', 'playtime_minutes', 'd1_retention']

const ruleOf = (value: unknown): TargetRule => (value && typeof value === 'object' ? value as TargetRule : {})

function historyOf(out: EventRecord): LiveOpsHistoryEntry[] {
  const entries: LiveOpsHistoryEntry[] = [{ at: out.created_at, actor: out.created_by ?? '', text: 'Created' }]
  if (out.updated_at !== out.created_at) {
    const text = out.status === 'paused' ? 'Paused' : out.status === 'archived' ? 'Archived' : out.status === 'active' ? 'Saved while active' : 'Saved'
    entries.push({ at: out.updated_at, actor: out.updated_by ?? '', text })
  }
  return entries
}

function eventOf(out: EventRecord): LiveOpsEventDef {
  const schedule = out.schedule
  return {
    key: out.key,
    kind: out.kind,
    name: out.name,
    description: out.description ?? '',
    status: out.status as LiveOpsStatus,
    schedule: {
      mode: schedule.mode as ScheduleMode,
      anchor: schedule.anchor,
      duration: schedule.duration,
      ...(schedule.interval ? { interval: schedule.interval } : {}),
      ...(schedule.until ? { until: schedule.until } : {}),
      ...(schedule.weekdays?.length ? { weekdays: [...schedule.weekdays] } : {}),
      ...(schedule.timezone ? { timezone: schedule.timezone } : {})
    },
    priority: out.priority,
    audience: ruleOf(out.audience),
    payload: out.payload as unknown as Json,
    rollout: out.rollout,
    salt: out.salt ?? '',
    goal: (out.goal || null) as LiveOpsMetricKey | null,
    watch: [...(out.watch ?? [])] as LiveOpsMetricKey[],
    created_at: out.created_at,
    updated_at: out.updated_at,
    updated_by: out.updated_by ?? '',
    history: historyOf(out)
  }
}

/** An event as written: an empty audience left out, since absent is every player. */
function bodyOf(event: LiveOpsDraft, salt = '') {
  return {
    kind: event.kind,
    name: event.name.trim(),
    status: event.status,
    schedule: event.schedule,
    priority: event.priority,
    ...(Object.keys(event.audience).length ? { audience: event.audience } : {}),
    payload: event.payload,
    rollout: event.rollout,
    salt,
    description: event.description.trim(),
    goal: event.goal ?? '',
    watch: event.watch
  }
}

const mean = (values: number[]) => values.reduce((sum, value) => sum + value, 0) / Math.max(values.length, 1)

/** A lift and how sure it is, from the spread of the days before the window around what was expected. */
function comparisonOf(lift: number, actual: number, expected: number, noise: number, days: number): Comparison {
  const se = (noise || 0.05) / Math.sqrt(Math.max(days, 1))
  return { lift, low: lift - 1.96 * se, high: lift + 1.96 * se, chance: normalCdf(lift / se), difference: actual - expected }
}

function impactOf(out: ImpactRecord): EventImpact {
  const occurrence = { start: Date.parse(out.occurrence.start), end: Date.parse(out.occurrence.end) }
  const days = (out.days ?? []).map(day => Date.parse(day))
  const inWindow = days.map(day => day + DAY > occurrence.start && day < occurrence.end)
  const before = days.map(day => day + DAY <= occurrence.start)
  const after = days.map(day => day >= occurrence.end && day < occurrence.end + 3 * DAY)

  const daily = {} as EventImpact['daily']
  const metrics = {} as EventImpact['metrics']
  for (const metric of MEASURED) {
    const series = out.daily?.[metric]
    const actual = (series?.actual ?? []).map(value => value ?? Number.NaN)
    const expected = (series?.expected ?? []).map(value => value ?? Number.NaN)
    daily[metric] = { actual, expected }

    const ratio = (index: number) => (Number.isFinite(actual[index]!) && Number.isFinite(expected[index]!) && expected[index]! ? actual[index]! / expected[index]! - 1 : null)
    const residuals = days.map((_, index) => (before[index] ? ratio(index) : null)).filter((value): value is number => value !== null)
    const spread = residuals.length > 1 ? Math.sqrt(mean(residuals.map(value => (value - mean(residuals)) ** 2))) : 0
    const windowDays = inWindow.filter(Boolean).length

    const figure = out.metrics?.[metric]
    const during = figure?.actual ?? 0
    const expectedValue = figure?.expected ?? 0
    const lift = figure?.lift ?? (expectedValue ? during / expectedValue - 1 : 0)
    const later = days.map((_, index) => (after[index] ? ratio(index) : null)).filter((value): value is number => value !== null)
    metrics[metric] = {
      metric,
      during,
      expected: expectedValue,
      comparison: comparisonOf(lift, during, expectedValue, spread, windowDays),
      after: out.live || !later.length ? null : comparisonOf(mean(later), 0, 0, spread, later.length),
      measured: figure?.actual !== null && figure?.actual !== undefined,
      baseline: figure?.expected !== null && figure?.expected !== undefined
    }
  }
  daily.ad_revenue = { actual: [], expected: [] }

  const windowDays = Math.max(1, Math.round((occurrence.end - occurrence.start) / DAY))
  return {
    occurrence,
    method: 'baseline',
    players: Math.round(out.metrics?.players?.actual ?? 0),
    heldBack: 0,
    revenue: (out.metrics?.revenue?.actual ?? 0) * windowDays,
    live: out.live,
    days,
    daily,
    metrics
  }
}

const isoDay = (at: number) => new Date(at).toISOString().slice(0, 10)

export const useLiveOpsStore = defineStore('liveops', () => {
  const workspace = useWorkspaceStore()

  const events = shallowRef<LiveOpsEventDef[]>([])
  const impacts = shallowRef<Record<string, EventImpact | null>>({})
  const gameDays = shallowRef<Record<number, DayFigures>>({})
  const readingImpacts = new Set<string>()
  const readDays = new Set<string>()
  const loading = ref(false)
  const error = ref<ApiError | null>(null)
  const loadedFor = ref<string | null>(null)

  const path = computed(() => workspace.projectPath && `${workspace.projectPath}/liveops-events`)
  const analytics = computed(() => workspace.projectPath && `${workspace.projectPath}/analytics/production`)

  async function load(force = false) {
    if (!path.value || loading.value) return
    if (!force && loadedFor.value === path.value) return
    loading.value = true
    error.value = null
    try {
      const out = await apiRequest<Schema<'LiveOpsEventListOut'>>('GET', path.value)
      events.value = (out.liveops_events ?? []).map(eventOf)
      loadedFor.value = path.value
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
    } finally {
      loading.value = false
    }
  }

  /** What one window did, read once and kept; null before it began or while it is read. */
  function impactFor(event: LiveOpsEventDef, occurrence: Occurrence): EventImpact | null {
    if (occurrence.start > Date.now() || !analytics.value) return null
    const id = `${event.key}:${occurrence.start}`
    if (id in impacts.value) return impacts.value[id] ?? null
    if (!readingImpacts.has(id)) {
      readingImpacts.add(id)
      apiRequest<ImpactRecord>('GET', withQuery(`${analytics.value}/liveops-events/${encodeURIComponent(event.key)}/impact`, { at: new Date(occurrence.start).toISOString() }))
        .then(out => (impacts.value = { ...impacts.value, [id]: impactOf(out) }))
        .catch(() => (impacts.value = { ...impacts.value, [id]: null }))
        .finally(() => readingImpacts.delete(id))
    }
    return null
  }

  /** The game's figures for a day that has happened, read a month at a time; null until read. */
  function gameDay(dayStart: number): DayFigures | null {
    if (dayStart > Date.now() || !analytics.value) return null
    const known = gameDays.value[dayStart]
    if (known) return known
    const month = isoDay(dayStart).slice(0, 7)
    if (!readDays.has(month)) {
      readDays.add(month)
      void readMonth(month)
    }
    return null
  }

  async function readMonth(month: string) {
    const [year, number] = month.split('-').map(Number) as [number, number]
    const from = `${month}-01`
    const to = isoDay(Math.min(Date.UTC(year, number, 0), Date.now()))
    try {
      const out = await apiRequest<Schema<'MetricResult'>>('POST', `${analytics.value}/metrics/query`, { metric: 'active_players', interval: 'day', from, to })
      const next = { ...gameDays.value }
      for (const row of out.rows ?? []) {
        // A day held back for having too few players is no data, not a day of nobody.
        const counted = row.active_players ?? row.value
        if (counted === null || counted === undefined) continue
        const players = counted
        const revenue = (row.revenue_micros ?? 0) / 1_000_000
        next[Date.parse(row.bucket.slice(0, 10))] = {
          players,
          new_players: row.new_players ?? 0,
          revenue,
          arpdau: players ? revenue / players : 0,
          payer_conversion: players ? (row.payers ?? 0) / players : 0,
          sessions_per_player: players ? (row.sessions ?? 0) / players : 0,
          playtime_minutes: players ? (row.playtime_ms ?? 0) / 60_000 / players : 0,
          d1_retention: Number.NaN,
          ad_revenue: Number.NaN
        }
      }
      gameDays.value = next
    } catch {
      readDays.delete(month)
    }
  }

  /** Runs a write; the event as the API answers it, or the refusal in its words. */
  async function write(run: () => Promise<EventRecord>): Promise<string | null> {
    try {
      const next = eventOf(await run())
      events.value = events.value.some(entry => entry.key === next.key)
        ? events.value.map(entry => (entry.key === next.key ? next : entry))
        : [...events.value, next]
      return null
    } catch (caught) {
      return caught instanceof Error ? caught.message : String(caught)
    }
  }

  const create = (draft: LiveOpsDraft) => write(() => apiRequest<EventRecord>('POST', path.value!, { key: draft.key, ...bodyOf(draft) }))

  const update = (key: string, draft: LiveOpsDraft) => {
    const salt = events.value.find(entry => entry.key === key)?.salt ?? ''
    return write(() => apiRequest<EventRecord>('PUT', `${path.value}/${encodeURIComponent(key)}`, bodyOf(draft, salt)))
  }

  /** The same event with another status: a whole-object PUT, as every edit is. */
  function setStatus(key: string, status: LiveOpsStatus): Promise<string | null> {
    const event = events.value.find(entry => entry.key === key)
    if (!event) return Promise.resolve(`There is no event "${key}".`)
    return update(key, { ...event, status })
  }

  watch(path, () => {
    loadedFor.value = null
    events.value = []
    impacts.value = {}
    gameDays.value = {}
    readDays.clear()
    void load()
  })

  return { events, impacts, gameDays, loading, error, load, impactFor, gameDay, create, update, setStatus }
})
