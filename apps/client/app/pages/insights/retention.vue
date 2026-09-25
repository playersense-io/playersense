<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Whether players come back: the day-N figures for the cohorts that installed in the range,
// the curve they make, how it differs by platform or country, and every cohort in the
// triangle (POST …/analytics/{env}/cohorts/query, metric retention). Classic or rolling is
// always chosen and always said: studios quote both, and a number read under the wrong one is
// a different number (project.md §29.10).
const workspace = useWorkspaceStore()

type RetentionBasis = 'classic' | 'rolling'
type RetentionRange = '30d' | '90d'
type RetentionGrain = 'day' | 'week'
type RetentionBreakdown = 'none' | 'install_platform' | 'install_country'

/** Days the curve runs to. */
const CURVE_DAYS = 30

const BASES: SegmentedOption[] = [
  { value: 'classic', label: 'Classic', title: 'Active on exactly day N' },
  { value: 'rolling', label: 'Rolling', title: 'Active on day N or any day after' }
]

const RANGES: SegmentedOption[] = [
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

// Both in the URL, so a link to the page names the retention it shows.
const basis = useQueryChoice<RetentionBasis>('basis', ['classic', 'rolling'], 'classic')
const range = useQueryChoice<RetentionRange>('range', ['30d', '90d'], '90d')

const grain = ref<RetentionGrain>('week')
const breakdown = ref<RetentionBreakdown>('none')

const basisWords = computed(() => (basis.value === 'classic'
  ? 'Classic: the share of a cohort active on exactly day N.'
  : 'Rolling: the share of a cohort active on day N or any day after.'))

// The days and the colouring are the reader's, kept in this browser.
const {
  days: cohortDays,
  offsets: cohortOffsets,
  color: cohortColor
} = useCohortTableSettings('insights.retention', {})

// --- The queries ------------------------------------------------------------------------------

const period = computed(() => periodOf(Number.parseInt(range.value, 10)))
const CURVE_OFFSETS = Array.from({ length: CURVE_DAYS + 1 }, (_, day) => day)
/* The curve's days and the table's, together: one query answers both. */
const offsets = computed(() => [...new Set([...CURVE_OFFSETS, ...cohortOffsets.value])].sort((a, b) => a - b).slice(0, 64))

const answer = ref<CohortAnswer | null>(null)
const previous = ref<CohortAnswer | null>(null)
const split = ref<CohortAnswer | null>(null)
const loading = ref(false)
const error = ref<ApiError | null>(null)

const baseQuery = (from: string, to: string) => ({
  metric: 'retention',
  retention_basis: basis.value,
  cohort: { from, to, grain: grain.value },
  offsets: offsets.value
})

async function load() {
  const path = workspace.analyticsPath
  if (!path) return
  loading.value = true
  error.value = null
  const { from, to, previousFrom, previousTo } = period.value
  try {
    const [now, before, parts] = await Promise.all([
      cohortQuery(path, baseQuery(from, to)),
      cohortQuery(path, { ...baseQuery(previousFrom, previousTo), offsets: CURVE_OFFSETS }),
      breakdown.value === 'none'
        ? Promise.resolve(null)
        : cohortQuery(path, { ...baseQuery(from, to), offsets: CURVE_OFFSETS, breakdown: { dimension: breakdown.value } })
    ])
    answer.value = now
    previous.value = before
    split.value = parts
  } catch (caught) {
    error.value = caught instanceof ApiError ? caught : new ApiError(String(caught), 0)
  } finally {
    loading.value = false
  }
}

watch([period, basis, grain, breakdown, offsets, () => workspace.analyticsPath], load, { immediate: true })

const threshold = computed(() => answer.value?.suppression_threshold ?? 5)
const curve = computed(() => weightedCurve(answer.value?.rows ?? [], CURVE_OFFSETS))
const previousCurve = computed(() => weightedCurve(previous.value?.rows ?? [], CURVE_OFFSETS))

// --- Figures ------------------------------------------------------------------------------

const percent = (rate: number) => writeFigure(rate * 100, { decimals: 1, unit: '%' })

function dayKpi(day: number): Kpi {
  const now = curve.value[day] ?? null
  const before = previousCurve.value[day] ?? null
  return {
    key: `d${day}`,
    label: `Day ${day}`,
    value: now === null ? null : percent(now),
    trend: now !== null && before !== null ? trendOf(now, before) : undefined,
    info: now === null
      ? `No cohort in the range is ${day} days old yet, or none had ${threshold.value} players or more.`
      : `Weighted over the cohorts at least ${day} days old, by their size.`
  }
}

/* No cohort came back (nothing installed, or every cohort was under the threshold): no figure,
   rather than a count of zero, and no trend unless the range before had cohorts too. */
const board = computed<Kpi[]>(() => {
  const rows = (answer.value?.rows ?? []).filter(row => row.day_offset === 0)
  const previousRows = (previous.value?.rows ?? []).filter(row => row.day_offset === 0)
  const players = rows.length ? cohortPlayers(rows) : null
  const before = previousRows.length ? cohortPlayers(previousRows) : null
  return [
    {
      key: 'new',
      label: 'New players',
      value: players === null ? null : writeFigure(players, { compact: true }),
      trend: players !== null && before !== null ? trendOf(players, before) : undefined,
      info: `Installs in the range: the players these cohorts are made of. A cohort under ${threshold.value} players is not counted.`
    },
    dayKpi(1),
    dayKpi(7),
    dayKpi(14),
    dayKpi(30)
  ]
})

// --- Curve --------------------------------------------------------------------------------

const BREAKDOWNS: SegmentedOption[] = [
  { value: 'none', label: 'All' },
  { value: 'install_platform', label: 'Platform' },
  { value: 'install_country', label: 'Country' }
]

// From day 1: day 0 is every player by definition, and drawn it only squashes the decay
// that is the whole point of the chart into the bottom of the axis.
const categories = Array.from({ length: CURVE_DAYS }, (_, day) => `D${day + 1}`)
const titles = Array.from({ length: CURVE_DAYS }, (_, day) => `Day ${day + 1}`)

const toPercent = (values: (number | null)[]) => values.slice(1).map(rate => (rate === null ? null : Math.round(rate * 1000) / 10))

/* The six biggest values of the breakdown, by the players they hold. */
const parts = computed(() => {
  const rows = split.value?.rows ?? []
  const values = [...new Set(rows.map(row => row.dim_value ?? ''))]
  return values
    .map(value => ({ value, rows: rows.filter(row => (row.dim_value ?? '') === value) }))
    .map(part => ({ ...part, players: cohortPlayers(part.rows.filter(row => row.day_offset === 0)) }))
    .sort((a, b) => b.players - a.players)
    .slice(0, 6)
})

const series = computed<MetricSeries[]>(() => {
  if (breakdown.value !== 'none') {
    return parts.value.map((part, index) => ({
      name: part.value || 'Unknown',
      data: toPercent(weightedCurve(part.rows, CURVE_OFFSETS)),
      type: 'line',
      tone: (index + 1) as 1 | 2 | 3 | 4 | 5 | 6
    }))
  }
  return [
    { name: 'These cohorts', data: toPercent(curve.value), type: 'area', tone: 1 },
    { name: 'The range before', data: toPercent(previousCurve.value), dashed: true, tone: 'muted' }
  ]
})

// --- Cohorts ------------------------------------------------------------------------------

const GRAINS: SegmentedOption[] = [
  { value: 'week', label: 'Weekly' },
  { value: 'day', label: 'Daily' }
]

// Each cell is the share that came back, with the players it is of under it.
const cohortMode = rateMode()

const COLORS: SegmentedOption[] = [
  { value: 'heat', label: 'Heat', title: 'Shaded by the figure' },
  { value: 'average', label: 'vs average', title: 'Shaded by how far each cohort is from all cohorts on the same day' }
]

const cohortRows = computed(() => (answer.value
  ? pivotCohortRows(answer.value.rows, {
      from: period.value.from,
      to: period.value.to,
      grain: grain.value,
      asOf: answer.value.computed_at,
      offsets: cohortOffsets.value
    })
  : []))
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Retention">
      <template #description>
        Whether <span class="text-ink">{{ workspace.project?.name }}</span>'s players come back after they
        install, cohort by cohort.
      </template>
      <template #actions>
        <SegmentedControl v-model="basis" :options="BASES" label="Retention basis" />
        <SegmentedControl v-model="range" :options="RANGES" label="Cohorts" />
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading && !answer"
      what="retention"
      @retry="load"
    >
      <KpiBoard :description="`${basisWords} Cohorts that installed in the range, against the range before.`" :items="board" title="Retention" />

      <MetricCard
        :categories="categories"
        :description="`${basisWords} Each day counts only the cohorts old enough to have reached it.`"
        :series="series"
        :titles="titles"
        category-label="Day"
        title="Retention curve"
        unit="%"
      >
        <template #actions>
          <SegmentedControl v-model="breakdown" :options="BREAKDOWNS" label="Break down by" />
        </template>
      </MetricCard>

      <SectionCard
        :padded="false"
        :description="`${basisWords} Newest cohorts first. A cell with fewer than ${threshold} players is held back, so no one player can be picked out.`"
        title="Cohorts"
      >
        <template #actions>
          <CohortDaysMenu v-model="cohortDays" />
          <SegmentedControl v-model="cohortColor" :options="COLORS" label="Colour" />
          <SegmentedControl v-model="grain" :options="GRAINS" label="Cohort size" />
        </template>

        <CohortTable
          :color="cohortColor"
          :grain="grain"
          :mode="cohortMode"
          :offsets="cohortOffsets"
          :rows="cohortRows"
          :threshold="threshold"
          label="Retention by cohort"
        />
      </SectionCard>
    </LoadState>
  </div>
</template>
