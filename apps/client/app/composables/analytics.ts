import type { Schema } from '@playersense/contracts'

// What the analytics pages share: a period of whole days, written the way the API reads dates,
// and the daily series one project's metrics/query answers. A row carries every measure the
// source had (active players, sessions, playtime, revenue, payers), so one query is a whole
// page's figures. A day with too few players to show is absent from the answer, and is drawn as
// nothing rather than as zero.

export type MetricDay = Schema<'MetricRow'>

/** "2026-09-24", in UTC, the way the API names a day. */
export const utcDay = (at: number | Date) => new Date(at).toISOString().slice(0, 10)

const DAY_MS = 86_400_000

/**
 * The last `days` whole days, ending yesterday (today is still being written), and the same
 * number of days before them.
 */
export function periodOf(days: number, now = Date.now()) {
  const end = Date.UTC(new Date(now).getUTCFullYear(), new Date(now).getUTCMonth(), new Date(now).getUTCDate()) - DAY_MS
  const start = end - (days - 1) * DAY_MS
  return {
    from: utcDay(start),
    to: utcDay(end),
    previousFrom: utcDay(start - days * DAY_MS),
    previousTo: utcDay(start - DAY_MS),
    /** Every day of the period, oldest first. */
    days: Array.from({ length: days }, (_, index) => utcDay(start + index * DAY_MS)),
    previousDays: Array.from({ length: days }, (_, index) => utcDay(start - days * DAY_MS + index * DAY_MS))
  }
}

/** One project's days, every measure, from `from` to `to` inclusive. */
export async function dailyMetrics(analyticsPath: string, from: string, to: string) {
  return apiRequest<Schema<'MetricResult'>>('POST', `${analyticsPath}/metrics/query`, { metric: 'active_players', interval: 'day', from, to })
}

/** A page's days, by day, from the rows of one or more answers added together. */
export function byDay(rows: readonly MetricDay[]): Map<string, MetricDay> {
  const out = new Map<string, MetricDay>()
  for (const row of rows) {
    const day = row.bucket.slice(0, 10)
    const held = out.get(day)
    if (!held) {
      out.set(day, { ...row, bucket: day })
      continue
    }
    const add = (key: keyof MetricDay) => ((held[key] as number | undefined) ?? 0) + ((row[key] as number | undefined) ?? 0)
    out.set(day, {
      ...held,
      active_players: add('active_players'),
      new_players: add('new_players'),
      returning_players: add('returning_players'),
      sessions: add('sessions'),
      playtime_ms: add('playtime_ms'),
      events: add('events'),
      revenue_micros: add('revenue_micros'),
      purchases: add('purchases'),
      payers: add('payers'),
      value: held.value + row.value
    })
  }
  return out
}

// --- Cohorts ---------------------------------------------------------------------------------

export type CohortAnswer = Schema<'CohortResult'>
export type CohortAnswerRow = Schema<'CohortRow'>

/** One cohorts/query of the selected project. */
export async function cohortQuery(analyticsPath: string, body: Record<string, unknown>) {
  return apiRequest<CohortAnswer>('POST', `${analyticsPath}/cohorts/query`, body)
}

/**
 * The curve a set of cohorts makes: at each offset, the players back over the players in the
 * cohorts old enough to have reached it, so a big cohort weighs more than a small one. Null
 * where no cohort has reached the offset (or every one was too small to show).
 */
export function weightedCurve(rows: readonly CohortAnswerRow[], offsets: readonly number[]): (number | null)[] {
  return offsets.map((offset) => {
    const at = rows.filter(row => row.day_offset === offset)
    const size = at.reduce((sum, row) => sum + row.cohort_size, 0)
    return size ? at.reduce((sum, row) => sum + row.players, 0) / size : null
  })
}

/** The players the cohorts are made of: each cohort's size once. */
export function cohortPlayers(rows: readonly CohortAnswerRow[]): number {
  const sizes = new Map<string, number>()
  for (const row of rows) sizes.set(`${row.cohort_date}|${row.dim_value ?? ''}`, row.cohort_size)
  return [...sizes.values()].reduce((sum, size) => sum + size, 0)
}

// --- The event catalogue, for the pickers ---------------------------------------------------

type CatalogueEntry = import('~/utils/events-sample').EventDefinition
type CatalogueProperty = import('~/utils/events-sample').EventProperty

const PROPERTY_TYPES: Record<string, CatalogueProperty['type']> = { int: 'number', float: 'number', bool: 'boolean' }

/**
 * The project's events as the event pickers and condition menus read them: every name seen in
 * the last 30 days (…/events), and, once an event is chosen, the properties it carried with
 * their most frequent values (…/events/{name}). Properties are read on demand: one event's
 * detail is a scan, and a picker lists dozens.
 */
export function useEventCatalogue() {
  const workspace = useWorkspaceStore()
  const range = computed(() => ({ from: utcDay(Date.now() - 29 * 86_400_000), to: utcDay(Date.now()) }))

  const list = useApiGet<Schema<'EventCatalogueOut'>>(() => workspace.analyticsPath && withQuery(`${workspace.analyticsPath}/events`, range.value))
  const properties = ref<Record<string, CatalogueProperty[]>>({})
  const asked = new Set<string>()

  watch(() => workspace.analyticsPath, () => {
    properties.value = {}
    asked.clear()
  })

  /** Reads an event's properties, once. */
  async function ensure(name: string) {
    const path = workspace.analyticsPath
    if (!path || !name || asked.has(name)) return
    asked.add(name)
    try {
      const out = await apiRequest<Schema<'EventDetailOut'>>('GET', withQuery(`${path}/events/${encodeURIComponent(name)}`, range.value))
      properties.value = {
        ...properties.value,
        [name]: out.properties.map(property => ({
          key: property.key,
          type: PROPERTY_TYPES[property.type] ?? 'string',
          fill: property.fill,
          values: property.top.map(value => value.value)
        }))
      }
    } catch {
      // Without its properties an event can still be chosen; it just offers no conditions.
      asked.delete(name)
    }
  }

  const catalogue = computed<CatalogueEntry[]>(() => (list.data.value?.events ?? []).map(event => ({
    name: event.name,
    category: (event.category || 'custom') as CatalogueEntry['category'],
    description: event.description ?? '',
    rate: 0,
    pace: 1,
    properties: properties.value[event.name] ?? []
  })))

  const loading = computed(() => list.isFetching.value && !list.data.value)

  return { catalogue, ensure, loading }
}
