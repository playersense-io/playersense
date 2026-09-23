<script setup lang="ts">
// When a cohort pays back what it cost to acquire: its revenue per install to date — the
// LTV — against the cost per install the reader types in, since PlayerSense has no ad
// spend of its own to know it by. The day LTV first reaches the CPI is the payback day;
// LTV over CPI on day 7, 30 and 90 is the return on ad spend a UA team plans by.
//
// Every cohort weighted by its size, or, split, each segment on its own line and in its
// own row: the platform that pays back in a week and the one that never does.
const props = defineProps<{
  /** The cohorts, or the segments, with each cell's running total of revenue per install. */
  rows: readonly CohortTableRow[]
  split: boolean
  /** While the answer is on its way: the figures are bones and the chart its skeleton. */
  loading?: boolean
}>()

const cpi = defineModel<number>('cpi', { required: true })

const ltv = cumulativeMode({ label: 'LTV', format: value => `$${value.toFixed(2)}` })

/** The offsets some cohort is old enough for, in order: the curve's days. */
const days = computed(() => COHORT_OFFSETS.filter(offset => props.rows.some(row => cellAt(row, offset).state === 'ok')))

const lines = computed(() => {
  const all = { name: 'All cohorts', values: days.value.map(offset => ltv.summarize(props.rows, offset)) }
  if (!props.split) return [all]
  return props.rows.map(row => ({ name: row.label ?? '', values: days.value.map(offset => ltv.summarize([row], offset)) }))
})

/** The first day the line reaches the CPI, or null if it has not by the last day known. */
function paybackOf(values: readonly (number | null)[]): number | null {
  const at = values.findIndex(value => value !== null && value >= cpi.value)
  return at === -1 ? null : days.value[at]!
}

const roasAt = (values: readonly (number | null)[], day: number) => {
  const value = values[days.value.indexOf(day)]
  return value === null || value === undefined || !cpi.value ? null : value / cpi.value
}

const ltvOn = (values: readonly (number | null)[], day: number) => {
  const value = values[days.value.indexOf(day)]
  return value === null || value === undefined ? '–' : `$${value.toFixed(2)}`
}

const lastDay = computed(() => days.value[days.value.length - 1] ?? 0)

const percent = (value: number | null) => (value === null ? '–' : `${writeFigure(value * 100, { decimals: 0 })}%`)

const facts = computed<Fact[]>(() => {
  const all = props.split ? days.value.map(offset => ltv.summarize(props.rows, offset)) : lines.value[0]!.values
  const payback = paybackOf(all)
  return [
    {
      key: 'payback',
      label: 'Payback day',
      value: payback === null ? `Not by D${lastDay.value}` : `D${payback}`,
      note: payback === null ? 'LTV is still under the CPI' : 'LTV first reaches the CPI',
      mono: true
    },
    { key: 'd7', label: 'ROAS on day 7', value: percent(roasAt(all, 7)), mono: true },
    { key: 'd30', label: 'ROAS on day 30', value: percent(roasAt(all, 30)), mono: true },
    { key: 'd90', label: 'ROAS on day 90', value: percent(roasAt(all, 90)), mono: true, note: roasAt(all, 90) === null ? 'No cohort is 90 days old in the range' : undefined }
  ]
})

const series = computed<MetricSeries[]>(() => [
  ...lines.value.map((line, index): MetricSeries => ({
    name: line.name,
    data: line.values.map(value => (value === null ? null : Math.round(value * 100) / 100)),
    type: props.split ? 'line' : 'area',
    tone: ((index % 6) + 1) as 1 | 2 | 3 | 4 | 5 | 6
  })),
  { name: 'CPI', data: days.value.map(() => cpi.value), type: 'line', dashed: true, tone: 'muted' }
])

const categories = computed(() => days.value.map(offset => `D${offset}`))
const titles = computed(() => days.value.map(offset => `Day ${offset}`))

// The field keeps what is typed; the CPI takes it once it reads as a price.
const cpiText = ref(cpi.value.toFixed(2))
watch(cpi, (value) => {
  if (Number(cpiText.value) !== value) cpiText.value = value.toFixed(2)
})

function applyCpi() {
  const value = Number(cpiText.value)
  if (Number.isFinite(value) && value > 0) cpi.value = Math.round(value * 100) / 100
  else cpiText.value = cpi.value.toFixed(2)
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard
      description="PlayerSense does not see your ad spend: enter what an install costs you, blended across networks, and each cohort's LTV is read against it."
      title="Cost per install"
    >
      <template #actions>
        <v-text-field
          v-model="cpiText"
          aria-label="Cost per install"
          class="ps-cohort-payback__cpi"
          density="comfortable"
          hide-details
          min="0"
          prefix="$"
          step="0.1"
          type="number"
          variant="filled"
          @blur="applyCpi"
          @keydown.enter="applyCpi"
        />
      </template>

      <div v-if="loading" aria-busy="true" class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="index in 4" :key="index" class="flex flex-col gap-2 rounded-(--ps-radius) p-3 ring-1 ring-line ring-inset">
          <span class="ps-cohort-payback__bone h-2.5 w-20" />
          <span class="ps-cohort-payback__bone h-4 w-14" />
        </div>
      </div>
      <FactGrid
        v-else
        :columns="4"
        :facts="facts"
        label="Payback"
      />
    </SectionCard>

    <MetricCard
      :loading="loading"
      :categories="categories"
      :decimals="2"
      :series="series"
      :titles="titles"
      category-label="Day"
      :description="split ? 'Revenue per install to date for each segment, against the CPI. Where a line crosses it, that segment has paid for itself.' : 'Revenue per install to date, every cohort weighted by its size, against the CPI. Where the line crosses it, the cohorts have paid for themselves.'"
      prefix="$"
      title="LTV against CPI"
    />

    <SectionCard
      v-if="split && !loading"
      :padded="false"
      description="The same, one segment to a row."
      title="By segment"
    >
      <table class="ps-cohort-payback__table">
        <thead>
          <tr>
            <th>Segment</th>
            <th>LTV D30</th>
            <th>ROAS D7</th>
            <th>ROAS D30</th>
            <th>ROAS D90</th>
            <th>Payback</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(line, index) in lines" :key="line.name">
            <td>
              <span class="inline-flex items-center gap-2">
                <span :style="{ background: `rgb(var(--v-theme-chart-${(index % 6) + 1}))` }" aria-hidden="true" class="size-2 rounded-full" />
                {{ line.name }}
              </span>
            </td>
            <td class="font-mono">
              {{ ltvOn(line.values, 30) }}
            </td>
            <td class="font-mono">
              {{ percent(roasAt(line.values, 7)) }}
            </td>
            <td class="font-mono">
              {{ percent(roasAt(line.values, 30)) }}
            </td>
            <td class="font-mono">
              {{ percent(roasAt(line.values, 90)) }}
            </td>
            <td class="font-mono">
              <span :class="paybackOf(line.values) === null ? 'text-ink/50' : 'text-success'">
                {{ paybackOf(line.values) === null ? `Not by D${lastDay}` : `D${paybackOf(line.values)}` }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </SectionCard>
  </div>
</template>

<style scoped>
.ps-cohort-payback__bone {
  background: var(--ps-skeleton-bone);
  border-radius: var(--ps-radius-x-small);
  display: block;
}

.ps-cohort-payback__cpi {
  flex: none;
  width: 8rem;
}

.ps-cohort-payback__table {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-cohort-payback__table th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  text-align: right;
}

.ps-cohort-payback__table td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgb(var(--v-theme-on-surface));
  padding: 8px 12px;
  text-align: right;
}

.ps-cohort-payback__table :is(th, td):first-child {
  padding-left: 20px;
  text-align: left;
}

.ps-cohort-payback__table :is(th, td):last-child {
  padding-right: 20px;
}

.ps-cohort-payback__table tbody tr:last-child td {
  border-bottom: 0;
}
</style>
