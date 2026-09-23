<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Players grouped by when they arrived, and what each group went on to do: retain, play,
// spend, convert to paying. One metric at a time, per day or as the running total, by day,
// week or month of install; narrowed to platforms, countries or first app versions, and
// split by one of them to compare segments side by side — never two at once (§57.1).
// Three views: the triangle, the curves, and for revenue, payback against a cost per
// install the reader knows and PlayerSense does not.
//
// Every choice is in the URL, so a view is a link. Sample data until the API client is
// wired in (app/utils/cohort-sample.ts says what it will query).
const { project } = useWorkspace()

// --- The question -------------------------------------------------------------------------

const METRICS: GhostSelectOption[] = (Object.keys(METRIC_LABELS) as CohortMetric[]).map(value => ({ value, label: METRIC_LABELS[value] }))

const metric = useQueryChoice<CohortMetric>('metric', ['retention', 'playtime', 'revenue', 'payers'], 'retention')
const basis = useQueryChoice<CohortBasis>('basis', ['classic', 'rolling'], 'classic')
const sum = useQueryChoice<'total' | 'day'>('sum', ['total', 'day'], 'total')
const grain = useQueryChoice<CohortGrain>('grain', ['day', 'week', 'month'], 'week')
const range = useQueryChoice<CohortRange>('range', ['30d', '90d', '180d'], '90d')
const split = useQueryChoice<CohortSplit>('split', ['none', ...DIMENSION_KEYS], 'none')

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

const accumulates = computed(() => metric.value === 'playtime' || metric.value === 'revenue')

const DEFINITIONS: Record<CohortMetric, string> = {
  retention: 'The share of each cohort active on day N.',
  playtime: 'Minutes played per install: every install counted, whether it came back or not.',
  revenue: 'Revenue per install, verified purchases only: every install counted, paying or not. To date, it is the LTV.',
  payers: 'The share of each cohort that has made a purchase by day N.'
}

const definition = computed(() => {
  const words = [DEFINITIONS[metric.value]]
  if (metric.value === 'retention') words.push(basis.value === 'classic' ? 'Classic: active on exactly day N.' : 'Rolling: active on day N or any day after.')
  if (accumulates.value) words.push(sum.value === 'total' ? 'Added up to each day.' : 'On each day alone.')
  return words.join(' ')
})

// --- Filters ------------------------------------------------------------------------------

const QUERY_KEYS: Record<CohortDimension, string> = { install_platform: 'platform', install_country: 'country', first_app_version: 'version' }

const lists = Object.fromEntries(DIMENSION_KEYS.map(dimension => [
  dimension,
  useQueryList(QUERY_KEYS[dimension], COHORT_DIMENSIONS[dimension].values.map(([value]) => value))
])) as Record<CohortDimension, ReturnType<typeof useQueryList>>

const filters = computed<CohortFilters>({
  get: () => Object.fromEntries(DIMENSION_KEYS.map(dimension => [dimension, lists[dimension].value])) as CohortFilters,
  set: (value) => {
    for (const dimension of DIMENSION_KEYS) lists[dimension].value = value[dimension] ?? []
  }
})

const FILTER_GROUPS: FilterGroup[] = DIMENSION_KEYS.map(dimension => ({
  key: dimension,
  label: COHORT_DIMENSIONS[dimension].label,
  options: COHORT_DIMENSIONS[dimension].values.map(([value]) => ({ value, label: value }))
}))

const activeFilters = computed<ActiveFilter[]>(() => DIMENSION_KEYS.flatMap(dimension => lists[dimension].value.map(value => ({
  key: `${dimension}:${value}`,
  label: `${COHORT_DIMENSIONS[dimension].label}: ${value}`
}))))

function removeFilter(key: string) {
  const [dimension, value] = key.split(':') as [CohortDimension, string]
  lists[dimension].value = lists[dimension].value.filter(entry => entry !== value)
}

function clearFilters() {
  for (const dimension of DIMENSION_KEYS) lists[dimension].value = []
}

// --- The answer ---------------------------------------------------------------------------

const data = computed(() => sampleCohorts({
  metric: metric.value,
  basis: basis.value,
  range: range.value,
  grain: grain.value,
  split: split.value,
  filters: filters.value
}))

const mode = computed(() => cohortModeOf(metric.value, accumulates.value && sum.value === 'total'))

const installs = (rows: readonly CohortTableRow[]) => rows.reduce((total, row) => total + (row.size ?? 0), 0)

const MILESTONES = [1, 7, 30, 90] as const

const board = computed<Kpi[]>(() => {
  const { rows, previous } = data.value
  const now = installs(rows)
  const before = installs(previous)
  return [
    {
      key: 'installs',
      label: 'Installs',
      value: writeFigure(now, { compact: true }),
      exact: writeFigure(now),
      trend: trendOf(now, before),
      info: 'Players who installed in the range and match the filters: the cohorts below are made of them.'
    },
    ...MILESTONES.map((day): Kpi => {
      const value = mode.value.summarize(rows, day)
      const was = mode.value.summarize(previous, day)
      return {
        key: `d${day}`,
        label: `Day ${day}`,
        value: value === null ? '–' : mode.value.format(value),
        trend: value !== null && was !== null ? trendOf(value, was) : undefined,
        info: value === null
          ? `No cohort in the range is ${day} days old yet.`
          : `Every cohort at least ${day} days old, weighted by its size.`
      }
    })
  ]
})

// --- Views --------------------------------------------------------------------------------

const tabs = computed<PageTab[]>(() => [
  { value: 'table', label: split.value === 'none' ? 'Cohorts' : 'Segments', count: data.value.rows.length },
  { value: 'curves', label: 'Curves' },
  ...(metric.value === 'revenue' ? [{ value: 'payback', label: 'Payback' }] : [])
])

const tab = useQueryChoice('tab', () => tabs.value.map(entry => entry.value), 'table')

// The table's days and colouring are the reader's, kept in this browser.
const { days: cohortDays, offsets: cohortOffsets, color: cohortColor } = useCohortTableSettings('insights.cohorts', {})

const COLORS: SegmentedOption[] = [
  { value: 'heat', label: 'Heat', title: 'Shaded by the figure' },
  { value: 'average', label: 'vs average', title: 'Shaded by how far each row is from all of them on the same day' }
]

function downloadTable() {
  const offsets = cohortOffsets.value
  const header = [split.value === 'none' ? 'cohort' : COHORT_DIMENSIONS[split.value as CohortDimension].label.toLowerCase(), 'installs', ...offsets.map(offset => `D${offset}`)]
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
  downloadCsv(`cohorts-${metric.value}-${range.value}-${grain.value}${split.value === 'none' ? '' : `-by-${fileSlug(COHORT_DIMENSIONS[split.value as CohortDimension].label)}`}`, [header, ...lines])
}

// Curves: every offset some row is old enough for, so a line is drawn day by day.
const curveDays = computed(() => COHORT_OFFSETS.filter(offset => data.value.rows.some(row => cellAt(row, offset).state === 'ok')))

const asPercent = computed(() => metric.value === 'retention' || metric.value === 'payers')

const curveValue = (value: number | null) => {
  if (value === null) return null
  return asPercent.value ? Math.round(value * 1000) / 10 : Math.round(value * 100) / 100
}

/** Split: a line per segment. Otherwise every cohort weighted, and the five newest beside it. */
const curveSeries = computed<MetricSeries[]>(() => {
  const { rows } = data.value
  if (split.value !== 'none') {
    return rows.map((row, index) => ({
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
const router = useRouter()

const cpi = computed<number>({
  get: () => {
    const value = Number(route.query.cpi)
    return Number.isFinite(value) && value > 0 ? value : DEFAULT_CPI
  },
  set: (value) => {
    const { cpi: _, ...rest } = route.query
    void router.replace({ query: value === DEFAULT_CPI ? rest : { ...rest, cpi: value.toFixed(2) } })
  }
})
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Cohorts">
      <template #description>
        What <span class="text-ink">{{ project.name }}</span>'s players went on to do, grouped by
        when they installed.
      </template>
      <template #actions>
        <SegmentedControl v-model="range" :options="RANGES" label="Installed in" />
      </template>
    </PageHeader>

    <SectionCard :description="definition" :padded="false" :title="METRIC_LABELS[metric]">
      <template #title>
        <GhostSelect v-model="metric" :options="METRICS" label="Metric" />
      </template>
      <template #actions>
        <SegmentedControl
          v-if="metric === 'retention'"
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

      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <span class="text-xs text-ink/55">Split by</span>
        <SegmentedControl v-model="split" :options="SPLITS" label="Split by" />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
      </TableToolbar>
    </SectionCard>

    <KpiBoard
      :description="`${METRIC_LABELS[metric]} of the players who installed in the range, against the range before.`"
      :items="board"
      :title="METRIC_LABELS[metric]"
    />

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="tabs" label="Cohort views" />

      <SectionCard
        v-if="tab === 'table'"
        :description="split === 'none'
          ? `Newest cohort first. The first row is every cohort together, weighted by size.`
          : `Every cohort in the range, one ${COHORT_DIMENSIONS[split as CohortDimension].label.toLowerCase()} to a row. The first row is all of them together.`"
        :padded="false"
        :title="split === 'none' ? 'By cohort' : `By ${COHORT_DIMENSIONS[split as CohortDimension].label.toLowerCase()}`"
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
          :color="cohortColor"
          :grain="grain"
          :label="`${METRIC_LABELS[metric]} by cohort`"
          :mode="mode"
          :offsets="cohortOffsets"
          :row-label="split === 'none' ? 'Cohort' : COHORT_DIMENSIONS[split as CohortDimension].label"
          :rows="data.rows"
          :threshold="COHORT_THRESHOLD"
          size-label="Installs"
          unit="installs"
        />
      </SectionCard>

      <MetricCard
        v-else-if="tab === 'curves'"
        :categories="curveDays.map(offset => `D${offset}`)"
        :decimals="asPercent ? 1 : 2"
        :description="split === 'none'
          ? 'Every cohort together, weighted by size, and the five newest cohorts old enough to draw.'
          : 'One line per segment, every cohort in it weighted by size.'"
        :prefix="metric === 'revenue' ? '$' : ''"
        :series="curveSeries"
        :titles="curveDays.map(offset => `Day ${offset}`)"
        :title="`${METRIC_LABELS[metric]} by day since install`"
        :unit="asPercent ? '%' : metric === 'playtime' ? 'min' : ''"
        category-label="Day"
      />

      <CohortPayback
        v-else
        v-model:cpi="cpi"
        :rows="data.rows"
        :split="split !== 'none'"
      />
    </div>
  </div>
</template>
