<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// Whether players come back: the day-N figures for the cohorts that installed in the range,
// the curve they make, how it differs by platform or acquisition source, and every cohort
// in the triangle. Classic or rolling is always chosen and always said — studios quote
// both, and a number read under the wrong one is a different number (project.md §29.10).
//
// Sample data until the API client is wired in (app/utils/retention-sample.ts says what it
// will query).
const { project } = useWorkspace()

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

const data = computed(() => sampleRetention(basis.value, range.value, grain.value, breakdown.value))

const basisWords = computed(() => (basis.value === 'classic'
  ? 'Classic: the share of a cohort active on exactly day N.'
  : 'Rolling: the share of a cohort active on day N or any day after.'))

// --- Figures ------------------------------------------------------------------------------

const percent = (rate: number) => writeFigure(rate * 100, { decimals: 1, unit: '%' })

function dayKpi(day: number): Kpi {
  const now = data.value.curve[day] ?? null
  const before = data.value.previousCurve[day] ?? null
  return {
    key: `d${day}`,
    label: `Day ${day}`,
    value: now === null ? '—' : percent(now),
    trend: now !== null && before !== null ? trendOf(now, before) : undefined,
    info: now === null
      ? `No cohort in the range is ${day} days old yet.`
      : `Weighted over the cohorts at least ${day} days old, by their size.`
  }
}

const board = computed<Kpi[]>(() => [
  {
    key: 'new',
    label: 'New players',
    value: writeFigure(data.value.newPlayers, { compact: true }),
    trend: trendOf(data.value.newPlayers, data.value.previousNewPlayers),
    info: 'Installs in the range: the players these cohorts are made of.'
  },
  dayKpi(1),
  dayKpi(7),
  dayKpi(14),
  dayKpi(30)
])

// --- Curve --------------------------------------------------------------------------------

const BREAKDOWNS: SegmentedOption[] = [
  { value: 'none', label: 'All' },
  { value: 'platform', label: 'Platform' },
  { value: 'source', label: 'Source' }
]

// From day 1: day 0 is every player by definition, and drawn it only squashes the decay
// that is the whole point of the chart into the bottom of the axis.
const categories = Array.from({ length: CURVE_DAYS }, (_, day) => `D${day + 1}`)
const titles = Array.from({ length: CURVE_DAYS }, (_, day) => `Day ${day + 1}`)

const toPercent = (curve: (number | null)[]) => curve.slice(1).map(rate => (rate === null ? null : Math.round(rate * 1000) / 10))

const series = computed<MetricSeries[]>(() => {
  if (breakdown.value !== 'none') {
    return data.value.breakdown.map((row, index) => ({
      name: row.value,
      data: toPercent(row.curve),
      type: 'line',
      tone: (index + 1) as 1 | 2 | 3 | 4 | 5 | 6
    }))
  }
  return [
    { name: 'These cohorts', data: toPercent(data.value.curve), type: 'area', tone: 1 },
    { name: 'The range before', data: toPercent(data.value.previousCurve), dashed: true, tone: 'muted' }
  ]
})

// --- Cohorts ------------------------------------------------------------------------------

const GRAINS: SegmentedOption[] = [
  { value: 'week', label: 'Weekly' },
  { value: 'day', label: 'Daily' }
]

// Each cell is the share that came back, with the players it is of under it.
const MODES = [rateMode()]

const COLORS: SegmentedOption[] = [
  { value: 'heat', label: 'Heat', title: 'Shaded by the figure' },
  { value: 'average', label: 'vs average', title: 'Shaded by how far each cohort is from all cohorts on the same day' }
]

// The days and the colouring are the reader's, kept in this browser.
const {
  days: cohortDays,
  offsets: cohortOffsets,
  mode: cohortMode,
  color: cohortColor
} = useCohortTableSettings('insights.retention', { modes: MODES })
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Retention">
      <template #description>
        Whether <span class="text-ink">{{ project.name }}</span>'s players come back after they
        install, cohort by cohort.
      </template>
      <template #actions>
        <SegmentedControl v-model="basis" :options="BASES" label="Retention basis" />
        <SegmentedControl v-model="range" :options="RANGES" label="Cohorts" />
      </template>
    </PageHeader>

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
      :description="`${basisWords} Newest cohorts first.`"
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
        :rows="data.cohorts"
        :threshold="SUPPRESSION_THRESHOLD"
        label="Retention by cohort"
      />
    </SectionCard>
  </div>
</template>
