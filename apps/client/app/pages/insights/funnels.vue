<script setup lang="ts">
import type { Kpi } from '~/components/KpiBoard.vue'

// How far players get through a sequence of steps: a saved funnel, or one being built, as
// the players who did each step in order within the funnel's window of their first step
// (project.md §31). The steps are edited in place and the answer follows them; saving
// keeps the edit. Around the funnel: the headline figures against the range before, the
// same funnel split by an install dimension, its conversion over time, and how long the
// players who converted took.
//
// Sample data until the funnel query exists (app/utils/funnel-sample.ts says what it will
// ask). Funnels are saved in this browser until then.
const { project } = useWorkspace()

const route = useRoute()
const router = useRouter()

const RANGES: SegmentedOption[] = [
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: '90d', label: 'Last 90 days' }
]

const RANGE_WORDS: Record<FunnelRange, string> = { '7d': 'the last 7 days', '30d': 'the last 30 days', '90d': 'the last 90 days' }

const range = computed<FunnelRange>({
  get: () => (['7d', '30d', '90d'].includes(route.query.range as string) ? route.query.range as FunnelRange : '30d'),
  set: value => void router.replace({ query: { ...route.query, range: value } })
})

// --- Which funnel -------------------------------------------------------------------------

const { funnels, save, create, remove } = useSavedFunnels()

const NEW = 'new'

const selected = computed<string>({
  get: () => {
    const id = route.query.funnel as string | undefined
    return id === NEW || funnels.value.some(funnel => funnel.id === id) ? id! : funnels.value[0]!.id
  },
  set: id => void router.replace({ query: { ...route.query, funnel: id } })
})

const saved = computed(() => funnels.value.find(funnel => funnel.id === selected.value) ?? null)

const blank = (): FunnelDefinition => ({
  id: NEW,
  name: 'Untitled funnel',
  window: '1d',
  steps: [{ event: 'session_start', where: [] }, { event: 'level_complete', where: [] }]
})

/** The funnel on screen: the saved one, as edited so far. */
const draft = ref<FunnelDefinition>(copyFunnel(saved.value ?? blank()))

watch(selected, () => {
  draft.value = copyFunnel(saved.value ?? blank())
})

const same = (a: FunnelDefinition, b: FunnelDefinition) => JSON.stringify([a.steps, a.window]) === JSON.stringify([b.steps, b.window])

const dirty = computed(() => !saved.value || !same(draft.value, saved.value))

function onSave() {
  save(draft.value)
}

function onSaveAs(name: string) {
  const id = create(draft.value, name)
  selected.value = id
}

function onRename(name: string) {
  if (!saved.value) return
  save({ ...saved.value, name })
  draft.value = { ...draft.value, name }
}

function onRemove() {
  if (!saved.value) return
  remove(saved.value.id)
  selected.value = funnels.value[0]!.id
}

function onDiscard() {
  draft.value = copyFunnel(saved.value ?? blank())
}

const WINDOWS: SegmentedOption[] = [
  { value: 'session', label: 'Session', title: 'Within the session of the first step' },
  { value: '1h', label: '1h', title: 'Within 1 hour of the first step' },
  { value: '1d', label: '1d', title: 'Within 1 day of the first step' },
  { value: '7d', label: '7d', title: 'Within 7 days of the first step' },
  { value: '30d', label: '30d', title: 'Within 30 days of the first step' }
]

const conversionWindow = computed<string>({
  get: () => draft.value.window,
  set: value => void (draft.value = { ...draft.value, window: value as FunnelWindow })
})

const steps = computed<FunnelStep[]>({
  get: () => draft.value.steps,
  set: value => void (draft.value = { ...draft.value, steps: value })
})

// --- The answer ---------------------------------------------------------------------------

const BREAKDOWNS: SegmentedOption[] = [
  { value: 'none', label: 'All' },
  { value: 'install_platform', label: 'Platform' },
  { value: 'install_country', label: 'Country' },
  { value: 'first_app_version', label: 'Version', title: 'First app version' }
]

const breakdown = ref<FunnelBreakdown>('none')

const data = computed(() => sampleFunnel(draft.value, range.value, breakdown.value, EVENT_CATALOGUE))

const windowWords = computed(() => (draft.value.window === 'session' ? 'within the same session' : `within ${WINDOW_LABELS[draft.value.window]}`))

const percent = formatShare

function last<T>(values: readonly T[]): T | undefined {
  return values[values.length - 1]
}

const conversionOf = (series: FunnelSeries) => {
  const first = series.players[0]
  const end = last(series.players)
  return first && end !== null && end !== undefined ? end / first : null
}

/** The step that loses the largest share of the players who reached the one before it. */
const worstStep = computed(() => {
  let worst: { step: number, lost: number } | null = null
  const players = data.value.total.players
  for (let step = 1; step < players.length; step += 1) {
    const before = players[step - 1]
    const here = players[step]
    if (!before || here === null || here === undefined) continue
    const lost = 1 - here / before
    if (!worst || lost > worst.lost) worst = { step, lost }
  }
  return worst
})

const board = computed<Kpi[]>(() => {
  const { total, previous, medianToConvert } = data.value
  const entered = total.players[0] ?? null
  const converted = last(total.players) ?? null
  const rate = conversionOf(total)
  const previousRate = conversionOf(previous)
  const worst = worstStep.value
  return [
    {
      key: 'entered',
      label: 'Entered',
      value: entered === null ? '–' : writeFigure(entered, { compact: true }),
      exact: entered === null ? undefined : writeFigure(entered),
      trend: entered !== null && previous.players[0] ? trendOf(entered, previous.players[0]) : undefined,
      info: 'Players who did step 1 in the range, counted once however often they did it.'
    },
    {
      key: 'converted',
      label: 'Converted',
      value: converted === null ? `<${FUNNEL_THRESHOLD}` : writeFigure(converted, { compact: true }),
      exact: converted === null ? undefined : writeFigure(converted),
      trend: converted !== null && last(previous.players) ? trendOf(converted, last(previous.players)!) : undefined,
      info: `Players who went on to do every step, in order, ${windowWords.value} of step 1.`
    },
    {
      key: 'conversion',
      label: 'Conversion',
      value: rate === null ? '–' : percent(rate),
      trend: rate !== null && previousRate !== null ? trendOf(rate, previousRate) : undefined,
      info: 'Converted over entered.'
    },
    {
      key: 'time',
      label: 'Median time to convert',
      value: medianToConvert === null ? '–' : formatSeconds(medianToConvert),
      info: 'From step 1 to the last step, for the players who converted.'
    },
    {
      key: 'drop',
      label: worst ? `Biggest drop · step ${worst.step} → ${worst.step + 1}` : 'Biggest drop',
      value: worst ? percent(worst.lost) : '–',
      info: worst
        ? `The share of players who reached ${draft.value.steps[worst.step - 1]?.event} and did not go on to ${draft.value.steps[worst.step]?.event}.`
        : 'No step can be compared yet.'
    }
  ]
})

// --- As a table, and as a file -----------------------------------------------------------

const VIEWS: SegmentedOption[] = [
  { value: 'chart', icon: 'material-symbols:bar-chart', title: 'Chart' },
  { value: 'table', icon: 'material-symbols:table-chart-outline', title: 'Table' }
]

const funnelView = ref<'chart' | 'table'>('chart')

/**
 * The funnel as a CSV: a row per step, and for every player and each breakdown value its
 * players, its shares and its median wait. Shares as fractions and waits in seconds, so a
 * spreadsheet reads them as numbers; a withheld count is an empty cell.
 */
function downloadFunnel() {
  const series = [data.value.total, ...data.value.breakdown]
  // Every player's columns bare, a breakdown value's named: "players", "iOS players".
  const header = ['step', 'event', 'conditions', ...series.flatMap((entry, index) => {
    const of = index === 0 ? '' : `${entry.name} `
    return [`${of}players`, `${of}share of step 1`, `${of}share of step before`, `${of}median wait (s)`]
  })]
  const rows = draft.value.steps.map((step, index) => [
    index + 1,
    step.event,
    step.where.map(condition => `${condition.property} = ${condition.value}`).join('; '),
    ...series.flatMap((entry) => {
      const here = entry.players[index] ?? null
      const first = entry.players[0]
      const before = entry.players[index - 1]
      const median = entry.medians[index]
      return [
        here,
        here !== null && first ? Math.round(here / first * 10_000) / 10_000 : null,
        here !== null && index > 0 && before ? Math.round(here / before * 10_000) / 10_000 : null,
        median === null || median === undefined ? null : Math.round(median)
      ]
    })
  ])
  const split = breakdown.value === 'none' ? '' : `-by-${fileSlug(BREAKDOWN_LABELS[breakdown.value])}`
  downloadCsv(`${fileSlug(draft.value.name)}-${range.value}${split}`, [header, ...rows])
}

// --- Breakdown table ----------------------------------------------------------------------

const breakdownRows = computed(() => [data.value.total, ...data.value.breakdown].map((series, index) => ({
  series,
  colour: index === 0 ? null : `rgb(var(--v-theme-chart-${((index - 1) % 6) + 1}))`,
  shares: series.players.map((players, step) => {
    const first = series.players[0]
    return step === 0 || !first || players === null || players === undefined ? null : players / first
  }),
  conversion: conversionOf(series)
})))

const bestConversion = computed(() => Math.max(...data.value.breakdown.map(series => conversionOf(series) ?? 0)))

// --- Over time ----------------------------------------------------------------------------

const unsettled = computed(() => data.value.trend.settled.filter(settled => !settled).length)

const trendSeries = computed<MetricSeries[]>(() => {
  const { series, settled } = data.value.trend
  // Two decimals: a funnel into a purchase converts under 1%, and to one it draws as steps.
  const toPercent = (rate: number | null) => (rate === null ? null : Math.round(rate * 10_000) / 100)
  if (breakdown.value !== 'none') {
    return series.slice(1).map((entry, index) => ({
      name: entry.name,
      data: entry.rates.map(toPercent),
      type: 'line',
      tone: ((index % 6) + 1) as 1 | 2 | 3 | 4 | 5 | 6
    }))
  }
  // The points whose players are still inside their window are drawn apart, dashed, joined
  // to the last settled one: they will rise, and read as settled they look like a slump.
  const rates = series[0]?.rates ?? []
  const lastSettled = settled.lastIndexOf(true)
  return [
    { name: 'Conversion', data: rates.map((rate, index) => (settled[index] ? toPercent(rate) : null)), type: 'area', tone: 1 },
    {
      name: 'Still converting',
      data: rates.map((rate, index) => (!settled[index] || index === lastSettled ? toPercent(rate) : null)),
      type: 'line',
      dashed: true,
      tone: 'muted'
    }
  ]
})

const trendDescription = computed(() => {
  const unit = range.value === '90d' ? 'week' : 'day'
  const count = unsettled.value
  const recent = count === 0 ? '' : count === 1 ? `The last ${unit} is` : `The last ${count} ${unit}s are`
  return `Conversion of the players who entered each ${unit}.${recent ? ` ${recent} still inside the window, so still rising.` : ''}`
})

// --- Time to convert ----------------------------------------------------------------------

const timeCategories = computed(() => data.value.timeToConvert.map(bucket => bucket.label))
const timeSeries = computed<MetricSeries[]>(() => [
  { name: 'Players', data: data.value.timeToConvert.map(bucket => bucket.players), type: 'bar', tone: 1 }
])
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Funnels">
      <template #description>
        How far <span class="text-ink">{{ project.name }}</span>'s players get through a
        sequence of steps, and where they stop.
      </template>
      <template #actions>
        <FunnelPicker
          v-model="selected"
          :dirty="dirty"
          :funnels="funnels"
          :name="draft.name"
          :unsaved="!saved"
          @discard="onDiscard"
          @remove="onRemove"
          @rename="onRename"
          @save="onSave"
          @save-as="onSaveAs"
        />
        <SegmentedControl v-model="range" :options="RANGES" label="Range" />
      </template>
    </PageHeader>

    <SectionCard
      :description="`Players who did each step in order, other events in between allowed, ${windowWords} of step 1.`"
      :padded="false"
      title="Steps"
    >
      <template #actions>
        <span class="text-xs text-ink/55">Converted within</span>
        <SegmentedControl v-model="conversionWindow" :options="WINDOWS" label="Conversion window" />
      </template>
      <FunnelSteps v-model="steps" :catalogue="EVENT_CATALOGUE" />
    </SectionCard>

    <KpiBoard :description="`Players who entered in ${RANGE_WORDS[range]}, against the range before.`" :items="board" title="Conversion" />

    <SectionCard
      :description="breakdown === 'none'
        ? 'The share of step 1 that reached each step. Between two steps: how many went on, and the median time they took.'
        : `Each ${BREAKDOWN_LABELS[breakdown].toLowerCase()} against its own step 1, so a small one is compared by how it converts. The figures above are every player's.`"
      :padded="false"
      title="Funnel"
    >
      <template #actions>
        <SegmentedControl v-model="breakdown" :options="BREAKDOWNS" label="Break down by" />
        <SegmentedControl v-model="funnelView" :options="VIEWS" label="View" />
        <v-btn
          aria-label="Download as CSV"
          color="neutral"
          icon="material-symbols:download"
          size="small"
          variant="text"
          @click="downloadFunnel"
        />
      </template>

      <FunnelStepTable
        v-if="funnelView === 'table'"
        :breakdown="data.breakdown"
        :steps="draft.steps"
        :threshold="FUNNEL_THRESHOLD"
        :total="data.total"
      />

      <div v-else class="p-5">
        <FunnelChart
          :breakdown="data.breakdown"
          :steps="draft.steps"
          :threshold="FUNNEL_THRESHOLD"
          :total="data.total"
        />
      </div>

      <div v-if="funnelView === 'chart' && breakdown !== 'none'" class="overflow-x-auto border-t border-line">
        <table class="w-full border-separate border-spacing-0 text-xs">
          <thead>
            <tr class="text-ink/55">
              <th class="border-b border-line px-5 py-2.5 text-left font-normal">
                {{ BREAKDOWN_LABELS[breakdown] }}
              </th>
              <th class="border-b border-line px-3 py-2.5 text-right font-normal">
                Entered
              </th>
              <th
                v-for="(_, step) in draft.steps.slice(1)"
                :key="step"
                class="border-b border-line px-3 py-2.5 text-right font-mono font-normal"
              >
                Step {{ step + 2 }}
              </th>
              <th class="border-b border-line px-5 py-2.5 text-right font-normal">
                Conversion
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in breakdownRows" :key="row.series.name" class="hover:bg-ink/3">
              <td :class="row.colour ? 'text-ink/80' : 'font-medium text-ink'" class="border-b border-line px-5 py-2">
                <span class="flex items-center gap-2">
                  <span
                    v-if="row.colour"
                    :style="{ background: row.colour }"
                    aria-hidden="true"
                    class="size-2 flex-none rounded-full"
                  />
                  {{ row.series.name }}
                </span>
              </td>
              <td class="border-b border-line px-3 py-2 text-right font-mono text-ink/70">
                {{ row.series.players[0] === null ? `<${FUNNEL_THRESHOLD}` : writeFigure(row.series.players[0]!) }}
              </td>
              <td
                v-for="(value, step) in row.shares.slice(1)"
                :key="step"
                class="border-b border-line px-3 py-2 text-right font-mono text-ink/70"
              >
                {{ row.series.players[step + 1] === null ? `<${FUNNEL_THRESHOLD}` : value === null ? '–' : percent(value) }}
              </td>
              <td class="border-b border-line px-5 py-2 text-right font-mono">
                <span
                  :class="row.colour && row.conversion !== null && row.conversion === bestConversion ? 'bg-success/12 text-success' : 'text-ink'"
                  class="rounded-(--ps-radius-x-small) px-1.5 py-0.5"
                >
                  {{ row.conversion === null ? '–' : percent(row.conversion) }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </SectionCard>

    <div class="grid gap-8 lg:grid-cols-2">
      <MetricCard
        :categories="data.trend.categories"
        :description="trendDescription"
        :series="trendSeries"
        :titles="data.trend.titles"
        :category-label="range === '90d' ? 'Week' : 'Day'"
        title="Conversion over time"
        unit="%"
      />

      <MetricCard
        :categories="timeCategories"
        :series="timeSeries"
        :value="data.medianToConvert === null ? null : formatSeconds(data.medianToConvert)"
        category-label="Time taken"
        description="How long the players who converted took, from step 1 to the last. The figure is the median."
        title="Time to convert"
        type="bar"
      />
    </div>
  </div>
</template>
