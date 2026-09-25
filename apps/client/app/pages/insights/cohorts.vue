<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Players grouped by when they arrived, and what each group went on to do: retain, play,
// spend, convert to paying, and do any event in the catalogue (POST …/analytics/{env}/cohorts/query). One metric at a time, per day or, for revenue, as the
// running total, by day, week or month of install; narrowed to platforms, countries or first
// app versions, and split by one of them to compare segments side by side, never two at once
// (§57.1). Three views: the triangle, the curves, and for revenue, payback against a cost per
// install the reader knows and PlayerSense does not.
//
// Every choice is in the URL, so a view is a link.
const workspace = useWorkspaceStore()

// --- The question -------------------------------------------------------------------------

type Metric = 'retention' | 'active_players' | 'sessions' | 'sessions_active' | 'playtime' | 'playtime_active'
  | 'events' | 'event_share' | 'revenue' | 'payer_conversion'
type Dimension = 'install_platform' | 'install_country' | 'first_app_version'
type Split = 'none' | Dimension
type Range = '30d' | '90d' | '180d'
type Row = CohortAnswerRow

interface MetricView {
  label: string
  group: 'Engagement' | 'Events' | 'Monetization'
  note: string
  definition: string
  /** What the API is asked. */
  api: 'retention' | 'sessions' | 'playtime' | 'event' | 'ltv' | 'payer_conversion'
  /** A cell's figure, from its row. */
  value: (row: Row) => number
  /** The running total, for the one metric the API sums up. */
  cumulative?: (row: Row) => number
  per: 'share' | 'count' | 'value'
  format: (value: number) => string
  chart: { prefix?: string, unit?: string, decimals: number, scale?: number }
}

const per = (value: number, over: number) => (over ? value / over : 0)
const dollars = (value: number) => `$${value.toFixed(value < 10 ? 2 : 1)}`
const minutesText = (value: number) => `${writeFigure(value, { decimals: value < 10 ? 1 : 0 })} min`
const plain = (value: number) => writeFigure(value, { decimals: value < 10 ? 2 : 1 })
const share = { per: 'share' as const, format: (value: number) => value.toString(), chart: { unit: '%', decimals: 1, scale: 100 } }

const VIEWS: Record<Metric, MetricView> = {
  retention: { label: 'Retention', group: 'Engagement', note: 'Who comes back', definition: 'The share of each cohort active on day N.', api: 'retention', value: row => row.rate, ...share },
  active_players: {
    label: 'Active players', group: 'Engagement', note: 'How many, not what share', per: 'count',
    definition: 'The players of each cohort active on day N, counted: the headcount LiveOps plans an event for.',
    api: 'retention', value: row => row.players, format: value => writeFigure(value, { compact: value >= 10_000 }), chart: { decimals: 0 }
  },
  sessions: {
    label: 'Sessions per install', group: 'Engagement', note: 'How often they open the game', per: 'value',
    definition: 'Sessions started on day N per install: every install counted, whether it came back or not.',
    api: 'sessions', value: row => per(row.sessions ?? 0, row.cohort_size), format: plain, chart: { decimals: 2 }
  },
  sessions_active: {
    label: 'Sessions per active player', group: 'Engagement', note: 'How often the ones who stayed open it', per: 'value',
    definition: 'Sessions on day N by each player active that day: the habit, among the players who kept it.',
    api: 'sessions', value: row => per(row.sessions ?? 0, row.players), format: plain, chart: { decimals: 2 }
  },
  playtime: {
    label: 'Playtime per install', group: 'Engagement', note: 'Minutes, every install counted', per: 'value',
    definition: 'Minutes played on day N per install: every install counted, whether it came back or not.',
    api: 'playtime', value: row => per((row.playtime_ms ?? 0) / 60_000, row.cohort_size), format: minutesText, chart: { unit: ' min', decimals: 1 }
  },
  playtime_active: {
    label: 'Playtime per active player', group: 'Engagement', note: 'Minutes, among the ones who stayed', per: 'value',
    definition: 'Minutes played on day N by each player active that day.',
    api: 'playtime', value: row => per((row.playtime_ms ?? 0) / 60_000, row.players), format: minutesText, chart: { unit: ' min', decimals: 1 }
  },
  events: {
    label: 'Event per install', group: 'Events', note: 'How often, every install counted', per: 'value',
    definition: '', api: 'event', value: row => per(row.events ?? 0, row.cohort_size), format: plain, chart: { decimals: 2 }
  },
  event_share: { label: 'Players who did the event', group: 'Events', note: 'Who has done it, by day N', definition: '', api: 'event', value: row => row.rate, ...share },
  revenue: {
    label: 'Revenue per install', group: 'Monetization', note: 'LTV, and payback', per: 'value',
    definition: 'Revenue on day N per install, gross, every install counted: to date, the lifetime value.',
    api: 'ltv', value: row => per((row.revenue_micros ?? 0) / 1_000_000, row.cohort_size),
    cumulative: row => per((row.revenue_micros_cumulative ?? 0) / 1_000_000, row.cohort_size),
    format: dollars, chart: { prefix: '$', decimals: 2 }
  },
  payer_conversion: {
    label: 'Payer conversion', group: 'Monetization', note: 'Who has paid, by day N',
    definition: 'The share of each cohort that has made a purchase by day N.', api: 'payer_conversion', value: row => row.rate, ...share
  }
}

const METRIC_KEYS = Object.keys(VIEWS) as Metric[]

const METRICS: GhostSelectOption[] = METRIC_KEYS.map(value => ({
  value,
  label: VIEWS[value].label,
  group: VIEWS[value].group,
  note: VIEWS[value].note
}))

const metric = useQueryChoice<Metric>('metric', METRIC_KEYS, 'retention')
const about = computed(() => VIEWS[metric.value])
const basis = useQueryChoice<'classic' | 'rolling'>('basis', ['classic', 'rolling'], 'classic')
const sum = useQueryChoice<'total' | 'day'>('sum', ['total', 'day'], 'total')
const grain = useQueryChoice<CohortGrain>('grain', ['day', 'week', 'month'], 'week')
const range = useQueryChoice<Range>('range', ['30d', '90d', '180d'], '90d')

const DIMENSIONS: Record<Dimension, { label: string, noun: string }> = {
  install_platform: { label: 'Platform', noun: 'platform' },
  install_country: { label: 'Country', noun: 'country' },
  first_app_version: { label: 'First app version', noun: 'version' }
}
const DIMENSION_KEYS = Object.keys(DIMENSIONS) as Dimension[]

const split = useQueryChoice<Split>('split', ['none', ...DIMENSION_KEYS], 'none')

const RANGES: SegmentedOption[] = [
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' },
  { value: '180d', label: 'Last 180 days' }
]

const BASES: SegmentedOption[] = [
  { value: 'classic', label: 'Classic', title: 'Active on exactly day N' },
  { value: 'rolling', label: 'Rolling', title: 'Active on day N or any day after' }
]

const SUMS: SegmentedOption[] = [
  { value: 'total', label: 'To date', title: 'The running total up to each day' },
  { value: 'day', label: 'That day', title: 'On each day alone' }
]

const GRAINS: SegmentedOption[] = [
  { value: 'day', label: 'Daily' },
  { value: 'week', label: 'Weekly' },
  { value: 'month', label: 'Monthly' }
]

const SPLITS: SegmentedOption[] = [
  { value: 'none', label: 'None' },
  { value: 'install_platform', label: 'Platform' },
  { value: 'install_country', label: 'Country' },
  { value: 'first_app_version', label: 'Version', title: 'First app version' }
]

const accumulates = computed(() => Boolean(about.value.cumulative))

// --- The event, for the event metrics -----------------------------------------------------

const { catalogue } = useEventCatalogue()

const eventName = useQueryChoice('event', () => catalogue.value.map(event => event.name), () => catalogue.value[0]?.name ?? '')

function chooseEvent(name: string) {
  eventName.value = name
}

/** The two metrics about one event, which the event row edits. */
const aboutEvent = computed(() => metric.value === 'events' || metric.value === 'event_share')

/** The metric by name: the event itself, for the event metrics. */
const metricLabel = computed(() => {
  if (metric.value === 'events') return `${eventName.value} per install`
  if (metric.value === 'event_share') return `Players who did ${eventName.value}`
  return about.value.label
})

const definition = computed(() => {
  const words = [metric.value === 'events'
    ? `How often each install did ${eventName.value} on day N: every install counted, whether it came back or not.`
    : metric.value === 'event_share'
      ? `The share of each cohort that has done ${eventName.value} at least once by day N.`
      : about.value.definition]
  if (metric.value === 'retention' || metric.value === 'active_players') words.push(basis.value === 'classic' ? 'Classic: active on exactly day N.' : 'Rolling: active on day N or any day after.')
  if (accumulates.value) words.push(sum.value === 'total' ? 'Added up to each day.' : 'On each day alone.')
  return words.join(' ')
})

// --- Filters ------------------------------------------------------------------------------

const QUERY_KEYS: Record<Dimension, string> = { install_platform: 'platform', install_country: 'country', first_app_version: 'version' }

/** Each dimension's values over the range, with their installs: what the filter offers. */
const known = ref<Record<Dimension, { value: string, installs: number }[]>>({ install_platform: [], install_country: [], first_app_version: [] })

const lists = Object.fromEntries(DIMENSION_KEYS.map(dimension => [
  dimension,
  useQueryList(QUERY_KEYS[dimension], () => known.value[dimension].map(entry => entry.value))
])) as Record<Dimension, ReturnType<typeof useQueryList>>

type Filters = Record<Dimension, string[]>

const filters = computed<Filters>({
  get: () => Object.fromEntries(DIMENSION_KEYS.map(dimension => [dimension, lists[dimension].value])) as Filters,
  set: (value) => {
    for (const dimension of DIMENSION_KEYS) lists[dimension].value = value[dimension] ?? []
  }
})

const regionNames = new Intl.DisplayNames(['en'], { type: 'region' })

/** A segment as a reader names it: "Brazil" for BR, a version as it is. */
function segmentName(dimension: Dimension, value: string): string {
  if (!value) return 'Unknown'
  if (dimension !== 'install_country') return value
  try {
    return regionNames.of(value) ?? value
  } catch {
    return value
  }
}

/** A value's flag or platform mark, for the two dimensions that have one. */
function markOf(dimension: Dimension, value: string): SegmentMarkValue | undefined {
  if (dimension === 'install_country') return { kind: 'country', code: value }
  if (dimension === 'install_platform') return { kind: 'platform', code: value }
  return undefined
}

const FILTER_GROUPS = computed<FilterGroup[]>(() => DIMENSION_KEYS.map((dimension) => {
  const total = known.value[dimension].reduce((sum, entry) => sum + entry.installs, 0)
  return {
    key: dimension,
    label: DIMENSIONS[dimension].label,
    noun: DIMENSIONS[dimension].noun,
    options: known.value[dimension].map(entry => ({
      value: entry.value,
      label: segmentName(dimension, entry.value),
      // The code beside a country's name, so "br" finds Brazil; and every value's share of installs.
      note: `${dimension === 'install_country' ? `${entry.value} · ` : ''}${writeFigure(per(entry.installs, total) * 100, { decimals: 1 })}%`,
      mark: markOf(dimension, entry.value)
    }))
  }
}))

/** A chip per value in force: a flag or mark says the dimension, and a version says it in words. */
const activeFilters = computed<ActiveFilter[]>(() => DIMENSION_KEYS.flatMap(dimension => lists[dimension].value.map((value) => {
  const mark = markOf(dimension, value)
  const name = segmentName(dimension, value)
  return { key: `${dimension}:${value}`, label: mark ? name : `${DIMENSIONS[dimension].label}: ${name}`, mark }
})))

function removeFilter(key: string) {
  const [dimension, ...value] = key.split(':') as [Dimension, ...string[]]
  lists[dimension].value = lists[dimension].value.filter(entry => entry !== value.join(':'))
}

function clearFilters() {
  for (const dimension of DIMENSION_KEYS) lists[dimension].value = []
}

// --- The answer ---------------------------------------------------------------------------

const period = computed(() => periodOf(Number.parseInt(range.value, 10)))

// The table's days and colouring are the reader's, kept in this browser.
const { days: cohortDays, offsets: cohortOffsets, color: cohortColor } = useCohortTableSettings('insights.cohorts', {})

const MILESTONES = [1, 7, 30, 90] as const

/* The table's days and the figures', together: one query answers both. */
const offsets = computed(() => [...new Set([0, ...MILESTONES, ...COHORT_OFFSETS, ...cohortOffsets.value])].sort((a, b) => a - b).slice(0, 64))

const answer = shallowRef<CohortAnswer | null>(null)
const previous = shallowRef<CohortAnswer | null>(null)
const loading = ref(false)
const error = ref<ApiError | null>(null)
let asked = 0

function bodyOf(from: string, to: string, withSplit: boolean) {
  const view = about.value
  const chosen = DIMENSION_KEYS.flatMap(dimension => (lists[dimension].value.length ? [{ field: dimension, op: 'in', in: lists[dimension].value }] : []))
  return {
    metric: view.api,
    cohort: { from, to, grain: grain.value },
    offsets: offsets.value,
    ...(view.api === 'retention' ? { retention_basis: basis.value } : {}),
    ...(view.api === 'event' ? { event: eventName.value } : {}),
    ...(chosen.length ? { filters: chosen } : {}),
    ...(withSplit && split.value !== 'none' ? { breakdown: { dimension: split.value } } : {})
  }
}

async function load() {
  const path = workspace.analyticsPath
  if (!path || (aboutEvent.value && !eventName.value)) return
  const ask = ++asked
  loading.value = true
  error.value = null
  const { from, to, previousFrom, previousTo } = period.value
  try {
    const [now, before] = await Promise.all([
      cohortQuery(path, bodyOf(from, to, true)),
      cohortQuery(path, bodyOf(previousFrom, previousTo, false))
    ])
    if (ask !== asked) return
    answer.value = now
    previous.value = before
  } catch (caught) {
    if (ask === asked) error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
  } finally {
    if (ask === asked) loading.value = false
  }
}

watchDebounced(
  () => JSON.stringify([bodyOf(period.value.from, period.value.to, true), workspace.analyticsPath]),
  load,
  { debounce: 250, immediate: true }
)

/** Each dimension's values, once per range: a day-0 breakdown is the installs of each. */
async function loadValues() {
  const path = workspace.analyticsPath
  if (!path) return
  const { from, to } = period.value
  const answers = await Promise.allSettled(DIMENSION_KEYS.map(dimension => cohortQuery(path, {
    metric: 'retention',
    retention_basis: 'classic',
    cohort: { from, to, grain: 'month' },
    offsets: [0],
    breakdown: { dimension }
  })))
  known.value = Object.fromEntries(DIMENSION_KEYS.map((dimension, index) => {
    const settled = answers[index]
    const rows = settled?.status === 'fulfilled' ? settled.value.rows : []
    const installs = new Map<string, number>()
    for (const row of rows) installs.set(row.dim_value ?? '', (installs.get(row.dim_value ?? '') ?? 0) + row.cohort_size)
    return [dimension, [...installs.entries()].map(([value, count]) => ({ value, installs: count })).sort((a, b) => b.installs - a.installs)]
  })) as Record<Dimension, { value: string, installs: number }[]>
}

watch([period, () => workspace.analyticsPath], loadValues, { immediate: true })

const threshold = computed(() => answer.value?.suppression_threshold ?? 5)

const mode = computed<CohortMode>(() => {
  const view = about.value
  if (view.per === 'share') return rateMode(metricLabel.value)
  if (view.per === 'count') return countMode(metricLabel.value)
  return view.cumulative && sum.value === 'total'
    ? cumulativeMode({ label: `${metricLabel.value}, to date`, format: view.format })
    : valueMode({ label: metricLabel.value, format: view.format })
})

/** The API's rows as the table's: a row a cohort, or a row a segment with every cohort in it. */
function tableRows(result: CohortAnswer | null, bySegment: boolean, from: string, to: string): CohortTableRow[] {
  if (!result) return []
  const view = about.value
  if (!bySegment) {
    return pivotCohortRows(result.rows, {
      from,
      to,
      grain: grain.value,
      asOf: result.computed_at,
      offsets: offsets.value,
      value: view.value,
      cumulative: view.cumulative
    })
  }
  const dimension = split.value as Dimension
  const groups = new Map<string, Row[]>()
  for (const row of result.rows) groups.set(row.dim_value ?? '', [...(groups.get(row.dim_value ?? '') ?? []), row])
  return [...groups.entries()].map(([value, rows]) => {
    const cells: Record<number, CohortTableCell> = {}
    for (const offset of offsets.value) {
      const at = rows.filter(row => row.day_offset === offset)
      if (!at.length) continue
      const size = at.reduce((total, row) => total + row.cohort_size, 0)
      const weigh = (pick: (row: Row) => number) => per(at.reduce((total, row) => total + pick(row) * row.cohort_size, 0), size)
      cells[offset] = {
        state: 'ok',
        players: at.reduce((total, row) => total + row.players, 0),
        value: view.per === 'count' ? at.reduce((total, row) => total + row.players, 0) : weigh(view.value),
        cumulative: view.cumulative ? weigh(view.cumulative) : null
      }
    }
    const sizes = new Map(rows.map(row => [row.cohort_date, row.cohort_size]))
    return {
      key: value,
      label: segmentName(dimension, value),
      mark: markOf(dimension, value),
      start: Date.parse(`${from}T00:00:00Z`),
      size: [...sizes.values()].reduce((total, count) => total + count, 0),
      cells
    }
  }).sort((a, b) => (b.size ?? 0) - (a.size ?? 0))
}

const data = computed(() => ({
  rows: tableRows(answer.value, split.value !== 'none', period.value.from, period.value.to),
  previous: tableRows(previous.value, false, period.value.previousFrom, period.value.previousTo)
}))

const installs = (rows: readonly CohortTableRow[]) => rows.reduce((total, row) => total + (row.size ?? 0), 0)

/* No cohort came back (nothing installed, or every cohort was under the threshold): no figure,
   rather than a count of zero, and no trend unless the range before had cohorts too. */
const board = computed<Kpi[]>(() => {
  const { rows, previous } = data.value
  const now = rows.length ? installs(rows) : null
  const before = previous.length ? installs(previous) : null
  return [
    {
      key: 'installs',
      label: 'Installs',
      value: now === null ? null : writeFigure(now, { compact: true }),
      exact: now === null ? undefined : writeFigure(now),
      trend: now !== null && before !== null ? trendOf(now, before) : undefined,
      info: `Players who installed in the range and match the filters: the cohorts below are made of them. A cohort under ${threshold.value} players is left out.`
    },
    ...MILESTONES.map((day): Kpi => {
      const value = mode.value.summarize(rows, day)
      const was = mode.value.summarize(previous, day)
      return {
        key: `d${day}`,
        label: `Day ${day}`,
        value: value === null ? null : mode.value.format(value),
        trend: value !== null && was !== null ? trendOf(value, was) : undefined,
        info: value === null
          ? `No cohort in the range is ${day} days old yet.`
          : `Every cohort at least ${day} days old, weighted by its size.`
      }
    })
  ]
})

// --- Views --------------------------------------------------------------------------------

/** The money per install a cost per install can be paid back from: purchases, ads, or both. */
const PAID_BACK: readonly Metric[] = ['revenue']

const tabs = computed<PageTab[]>(() => [
  { value: 'table', label: split.value === 'none' ? 'Cohorts' : 'Segments', count: data.value.rows.length },
  { value: 'curves', label: 'Curves' },
  ...(PAID_BACK.includes(metric.value) ? [{ value: 'payback', label: 'Payback' }] : [])
])

const tab = useQueryChoice('tab', () => tabs.value.map(entry => entry.value), 'table')

const COLORS: SegmentedOption[] = [
  { value: 'heat', label: 'Heat', title: 'Shaded by the figure' },
  { value: 'average', label: 'vs average', title: 'Shaded by how far each row is from all of them on the same day' }
]

function downloadTable() {
  const offsets = cohortOffsets.value
  const header = [split.value === 'none' ? 'cohort' : DIMENSIONS[split.value as Dimension].label.toLowerCase(), 'installs', ...offsets.map(offset => `D${offset}`)]
  const iso = (at: number) => new Date(at).toISOString().slice(0, 10)
  const lines = data.value.rows.map(row => [
    row.label ?? iso(row.start),
    row.size,
    ...offsets.map((offset) => {
      const cell = cellAt(row, offset)
      const value = cell.state === 'ok' ? mode.value.read(cell, row) : null
      return value === null ? null : Math.round(value * 10_000) / 10_000
    })
  ])
  downloadCsv(`cohorts-${metric.value}-${range.value}-${grain.value}${split.value === 'none' ? '' : `-by-${fileSlug(DIMENSIONS[split.value as Dimension].label)}`}`, [header, ...lines])
}

// Curves: every offset some row is old enough for, so a line is drawn day by day.
const curveDays = computed(() => COHORT_OFFSETS.filter(offset => data.value.rows.some(row => cellAt(row, offset).state === 'ok')))

/** A value as the chart draws it: scaled to percent where it is one, and rounded to its places. */
const curveValue = (value: number | null) => {
  if (value === null) return null
  const { scale = 1, decimals } = about.value.chart
  const places = 10 ** decimals
  return Math.round(value * scale * places) / places
}

/** The chart has six colours, so a split draws its six largest segments; the table has them all. */
const largest = computed(() => [...data.value.rows].sort((a, b) => (b.size ?? 0) - (a.size ?? 0)).slice(0, 6))

/** Split: a line per segment. Otherwise every cohort weighted, and the five newest beside it. */
const curveSeries = computed<MetricSeries[]>(() => {
  const { rows } = data.value
  if (split.value !== 'none') {
    return largest.value.map((row, index) => ({
      name: row.label ?? '',
      data: curveDays.value.map(offset => curveValue(mode.value.summarize([row], offset))),
      type: 'line',
      tone: ((index % 6) + 1) as 1 | 2 | 3 | 4 | 5 | 6
    }))
  }
  const dateOf = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const recent = rows.filter(row => curveDays.value.filter(offset => cellAt(row, offset).state === 'ok').length >= 3).slice(0, 5)
  return [
    { name: 'All cohorts', data: curveDays.value.map(offset => curveValue(mode.value.summarize(rows, offset))), type: 'area', tone: 1 },
    ...recent.map((row, index): MetricSeries => ({
      name: grain.value === 'month' ? new Intl.DateTimeFormat('en', { month: 'short', year: 'numeric', timeZone: 'UTC' }).format(row.start) : dateOf.format(row.start),
      data: curveDays.value.map(offset => curveValue(mode.value.summarize([row], offset))),
      type: 'line',
      tone: (index + 2) as 2 | 3 | 4 | 5 | 6
    }))
  ]
})

// Payback: the cost per install, a price rather than a choice, in the URL like the rest.
const DEFAULT_CPI = 2.5
const route = useRoute()
const patchQuery = useQueryPatch()

const cpi = computed<number>({
  get: () => {
    const value = Number(route.query.cpi)
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_CPI
  },
  set: value => patchQuery({ cpi: value === DEFAULT_CPI ? undefined : value.toFixed(2) })
})
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Cohorts">
      <template #description>
        What <span class="text-ink">{{ workspace.project?.name }}</span>'s players went on to do, grouped by
        when they installed.
      </template>
      <template #actions>
        <SegmentedControl v-model="range" :options="RANGES" label="Installed in" />
      </template>
    </PageHeader>

    <SectionCard :description="definition" :padded="false" :title="about.label">
      <template #title>
        <GhostPicker
          v-model="metric"
          :options="METRICS"
          description="What each cohort is measured by: what its players do, what they pay, and how the build holds up."
          label="Metric"
        />
      </template>
      <template #actions>
        <SegmentedControl
          v-if="metric === 'retention' || metric === 'active_players'"
          v-model="basis"
          :options="BASES"
          label="Retention basis"
        />
        <SegmentedControl
          v-if="accumulates"
          v-model="sum"
          :options="SUMS"
          label="Add up"
        />
        <SegmentedControl v-model="grain" :options="GRAINS" label="Cohort size" />
      </template>

      <div v-if="aboutEvent" class="flex flex-wrap items-center gap-2 border-b border-line px-5 py-2.5">
        <span class="text-xs text-ink/55">{{ metric === 'events' ? 'Count' : 'Players who did' }}</span>
        <EventPicker
          :catalogue="catalogue"
          :model-value="eventName"
          label="Event"
          @update:model-value="chooseEvent"
        />
        <span class="text-xs text-ink/55">{{ metric === 'events' ? 'per install' : 'by day N' }}</span>
      </div>

      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <span class="text-xs text-ink/55">Split by</span>
        <SegmentedControl v-model="split" :options="SPLITS" label="Split by" />
        <FilterDialog v-model="filters" :groups="FILTER_GROUPS" title="Filter cohorts" />
      </TableToolbar>
    </SectionCard>

    <p v-if="error" class="m-0 flex items-center gap-3 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
      <span class="flex-1">{{ error.message }}</span>
      <v-btn
        class="text-xs"
        color="error"
        size="small"
        variant="text"
        @click="load"
      >
        Try again
      </v-btn>
    </p>

    <KpiBoard
      :loading="loading"
      :description="`${metricLabel} of the players who installed in the range, against the range before.`"
      :items="board"
      :title="metricLabel"
    />

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="tabs" label="Cohort views" />

      <SectionCard
        v-if="tab === 'table'"
        :description="split === 'none'
          ? `Newest cohort first. The first row is every cohort together, weighted by size.`
          : `Every cohort in the range, one ${DIMENSIONS[split as Dimension].label.toLowerCase()} to a row. The first row is all of them together.`"
        :padded="false"
        :title="split === 'none' ? 'By cohort' : `By ${DIMENSIONS[split as Dimension].label.toLowerCase()}`"
      >
        <template #actions>
          <CohortDaysMenu v-model="cohortDays" />
          <SegmentedControl v-model="cohortColor" :options="COLORS" label="Colour" />
          <v-btn
            aria-label="Download as CSV"
            color="neutral"
            icon="material-symbols:download"
            size="small"
            variant="text"
            @click="downloadTable"
          />
        </template>

        <CohortTable
          :loading="loading"
          :loading-rows="Math.min(Math.max(data.rows.length, 3), 12)"
          :color="cohortColor"
          :grain="grain"
          :label="`${metricLabel} by cohort`"
          :mode="mode"
          :offsets="cohortOffsets"
          :row-label="split === 'none' ? 'Cohort' : DIMENSIONS[split as Dimension].label"
          :rows="data.rows"
          :threshold="threshold"
          size-label="Installs"
          unit="installs"
        />
      </SectionCard>

      <MetricCard
        v-else-if="tab === 'curves'"
        :loading="loading"
        :categories="curveDays.map(offset => `D${offset}`)"
        :decimals="about.chart.decimals"
        :description="split === 'none'
          ? 'Every cohort together, weighted by size, and the five newest cohorts old enough to draw.'
          : data.rows.length > 6
            ? `The six largest of ${data.rows.length} segments, every cohort in each weighted by size. The table has them all.`
            : 'One line per segment, every cohort in it weighted by size.'"
        :prefix="about.chart.prefix ?? ''"
        :series="curveSeries"
        :titles="curveDays.map(offset => `Day ${offset}`)"
        :title="`${metricLabel} by day since install`"
        :unit="about.chart.unit ?? ''"
        category-label="Day"
      />

      <CohortPayback
        v-else
        v-model:cpi="cpi"
        :loading="loading"
        :rows="data.rows"
        :split="split !== 'none'"
      />
    </div>
  </div>
</template>
