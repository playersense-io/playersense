<script setup lang="ts">
// One test's results, read top to bottom as the questions come: what is the answer, how did
// each variant do, how did it get there day by day, what else did it move, and what exactly was
// tested, when and by whom. The answer comes first and in words, because a page of charts that
// leaves the reader to work out the verdict is how a test gets misread.
//
// Sample data (app/utils/experiments-sample.ts).
const route = useRoute()

const { experimentById, start, stop, finish, ship } = useExperiments()
const { flagByKey } = useLiveConfig()

const id = computed(() => String(route.params.id ?? ''))
const experiment = computed(() => experimentById(id.value))

usePageCrumb(() => experiment.value?.name ?? 'Experiment')

const results = computed(() => (experiment.value ? sampleExperimentResults(experiment.value) : null))
const verdict = computed(() => (experiment.value ? verdictOf(experiment.value, results.value) : null))
const split = computed(() => (results.value ? againstControl(results.value) : null))

const flag = computed(() => (experiment.value ? flagByKey(experiment.value.flagKey) : null))

// --- Variants -----------------------------------------------------------------------------

/* The scale the ranges share: the widest any variant's reaches on the metric shown. */
const extentOf = (metric: MetricKey) => Math.max(0.02, ...(split.value?.others ?? []).flatMap(entry => [Math.abs(entry.comparisons[metric].low), Math.abs(entry.comparisons[metric].high)]))

const tones = computed(() => Object.fromEntries((experiment.value?.variants ?? []).map((entry, index) => [entry.id, (index % 6) + 1])) as Record<string, 1 | 2 | 3 | 4 | 5 | 6>)

// --- Day by day ---------------------------------------------------------------------------

const metric = ref<MetricKey>('revenue_per_player')
watch(experiment, (value) => {
  if (value) metric.value = value.primaryMetric
}, { immediate: true })

const METRIC_OPTIONS = METRIC_KEYS.map(key => ({ value: key, label: EXPERIMENT_METRICS[key].label, description: EXPERIMENT_METRICS[key].note }))

const metricModel = computed({
  get: () => metric.value as string,
  set: (value: string) => {
    metric.value = value as MetricKey
  }
})

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })
const categories = computed(() => results.value?.days.map(at => dayFormat.format(at)) ?? [])

const scaleOf = (key: MetricKey) => (EXPERIMENT_METRICS[key].kind === 'rate' ? 100 : 1)

const series = computed<MetricSeries[]>(() => (experiment.value?.variants ?? []).map(entry => ({
  name: entry.name,
  data: (results.value?.daily[entry.id]?.metrics[metric.value] ?? []).map(value => Math.round(value * scaleOf(metric.value) * 1000) / 1000),
  type: 'line',
  tone: tones.value[entry.id]
})))

/* The chance each variant beats the control, day by day, from the running figures. */
const chanceSeries = computed<MetricSeries[]>(() => {
  const exp = experiment.value
  const res = results.value
  if (!exp || !res) return []
  const control = exp.variants.find(entry => entry.control) ?? exp.variants[0]!
  const base = res.daily[control.id]
  const final = res.variants.find(entry => entry.variant.id === control.id)
  if (!base || !final) return []
  return exp.variants.filter(entry => entry.id !== control.id).map((entry) => {
    const daily = res.daily[entry.id]!
    const sd = res.variants.find(other => other.variant.id === entry.id)!.stats[metric.value].sd
    return {
      name: entry.name,
      data: daily.metrics[metric.value].map((value, index) => Math.round(compareVariants(
        { n: base.players[index]!, mean: base.metrics[metric.value][index]!, sd: final.stats[metric.value].sd },
        { n: daily.players[index]!, mean: value, sd }
      ).chance * 1000) / 10),
      type: 'line' as const,
      tone: tones.value[entry.id]
    }
  })
})

const metricPrefix = computed(() => (EXPERIMENT_METRICS[metric.value].kind === 'money' ? '$' : ''))
const metricUnit = computed(() => ({ rate: '%', money: '', count: '', minutes: 'min' })[EXPERIMENT_METRICS[metric.value].kind])

// --- Every metric -------------------------------------------------------------------------

const metricRows = computed(() => {
  const exp = experiment.value
  if (!exp || !split.value) return []
  const order = [exp.primaryMetric, ...exp.guardrails, ...METRIC_KEYS.filter(key => key !== exp.primaryMetric && !exp.guardrails.includes(key))]
  return order.map(key => ({
    key,
    role: key === exp.primaryMetric ? 'Primary' : exp.guardrails.includes(key) ? 'Guardrail' : '',
    control: split.value!.control.stats[key].mean,
    others: split.value!.others.map(entry => ({ variant: entry.variant, value: results.value!.variants.find(other => other.variant.id === entry.variant.id)!.stats[key].mean, comparison: entry.comparisons[key] }))
  }))
})

const OUTCOME_TEXT = { better: 'text-success', worse: 'text-error', unclear: 'text-ink/60' } as const

// --- Facts --------------------------------------------------------------------------------

const facts = computed<Fact[]>(() => {
  const exp = experiment.value
  if (!exp) return []
  return [
    { key: 'flag', label: 'Flag', value: exp.flagKey, mono: true, ...(flag.value ? { to: `/growth/flags/${encodeURIComponent(exp.flagKey)}` } : {}), info: 'Each variant is a value this flag serves. The server decides who gets which, by install, so a player stays in theirs.' },
    { key: 'audience', label: 'Who is in it', value: exp.audience },
    { key: 'split', label: 'Split', value: exp.variants.map(entry => `${entry.weight}%`).join(' / ') },
    { key: 'metric', label: 'Decided by', value: EXPERIMENT_METRICS[exp.primaryMetric].label, info: EXPERIMENT_METRICS[exp.primaryMetric].note },
    { key: 'when', label: 'When', value: whenWords(exp) },
    { key: 'players', label: 'Players so far', value: results.value ? writeFigure(results.value.variants.reduce((sum, entry) => sum + entry.players, 0)) : 'None yet', mono: true }
  ]
})

const now = useNow({ interval: 60_000 })

// --- Actions ------------------------------------------------------------------------------

const starting = ref(false)
const ending = ref<'finish' | 'stop' | null>(null)
const endOpen = computed({
  get: () => ending.value !== null,
  set: (value) => {
    if (!value) ending.value = null
  }
})
const shipping = ref(false)
const refusal = ref('')

const winner = computed(() => experiment.value?.variants.find(entry => entry.id === experiment.value?.winner) ?? null)

function onShip() {
  if (!experiment.value) return
  refusal.value = ship(experiment.value.id) ?? ''
}

function onEnd(decision: { winner: string | null, note: string }) {
  if (!experiment.value || !ending.value) return
  if (ending.value === 'stop') stop(experiment.value.id, decision.note)
  else finish(experiment.value.id, decision.winner, decision.note)
}
</script>

<template>
  <div v-if="!experiment || !verdict" class="flex flex-col gap-8">
    <PageHeader title="Experiment not found" />
    <LayerCard>
      <EmptyState description="There is no experiment with this id in the project." icon="material-symbols:science-outline" title="No such experiment">
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/growth/experiments"
            variant="flat"
          >
            All experiments
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :description="experiment.hypothesis" :title="experiment.name">
      <template #badge>
        <StatusBadge :label="EXPERIMENT_STATUS[experiment.status].label" :tone="EXPERIMENT_STATUS[experiment.status].tone" />
      </template>
      <template #actions>
        <template v-if="experiment.status === 'draft'">
          <v-btn
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:play-arrow-outline"
            size="small"
            variant="flat"
            @click="starting = true"
          >
            Start experiment
          </v-btn>
        </template>
        <template v-else-if="experiment.status === 'running'">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            prepend-icon="material-symbols:stop-circle-outline"
            size="small"
            variant="outlined"
            @click="ending = 'stop'"
          >
            Stop early
          </v-btn>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:flag-outline"
            size="small"
            variant="flat"
            @click="ending = 'finish'"
          >
            Finish
          </v-btn>
        </template>
      </template>
    </PageHeader>

    <ExperimentVerdict :experiment="experiment" :verdict="verdict">
      <v-btn
        v-if="experiment.status === 'concluded' && winner && !experiment.shipped && !winner.control"
        class="text-xs font-medium"
        color="success"
        prepend-icon="material-symbols:rocket-launch-outline"
        size="small"
        variant="flat"
        @click="shipping = true"
      >
        Ship {{ winner.name }}
      </v-btn>
      <StatusBadge v-else-if="experiment.shipped && winner" :label="`Shipped: ${winner.name} for everybody`" tone="success" />
      <v-btn
        v-else-if="verdict.kind === 'ready'"
        class="text-xs font-medium"
        color="primary"
        size="small"
        variant="flat"
        @click="ending = 'finish'"
      >
        Finish and pick {{ verdict.variant?.name }}
      </v-btn>
    </ExperimentVerdict>
    <p v-if="refusal" class="-mt-5 text-xs text-error">
      {{ refusal }}
    </p>

    <FactGrid :columns="3" :facts="facts" label="About this experiment" />

    <template v-if="results && split">
      <!-- Each variant as a card: what it served, to how many, and how it did on the one metric
           the test is decided by. -->
      <section class="flex flex-col gap-3">
        <div class="flex flex-col gap-0.5">
          <h2 class="m-0 text-sm font-medium text-ink">
            Variants
          </h2>
          <p class="m-0 text-xs text-ink/60">
            {{ EXPERIMENT_METRICS[experiment.primaryMetric].label }}, the metric this test is decided by, for each variant against {{ split.control.variant.name }}.
          </p>
        </div>
        <div :class="experiment.variants.length > 2 ? 'lg:grid-cols-3' : 'lg:grid-cols-2'" class="grid gap-3">
          <article
            v-for="entry in results.variants"
            :key="entry.variant.id"
            :class="verdict.variant?.id === entry.variant.id && !entry.variant.control ? 'border-success/50' : 'border-line'"
            class="flex flex-col gap-4 rounded-(--ps-radius-x-large) border bg-surface p-5"
          >
            <header class="flex items-start justify-between gap-3">
              <div class="flex min-w-0 items-center gap-2">
                <span :style="{ background: `rgb(var(--v-theme-chart-${tones[entry.variant.id]}))` }" aria-hidden="true" class="size-2.5 flex-none rounded-full" />
                <h3 class="m-0 truncate text-sm font-medium text-ink">
                  {{ entry.variant.name }}
                </h3>
              </div>
              <Tag v-if="entry.variant.control" label="Control" />
              <StatusBadge v-else-if="experiment.winner === entry.variant.id" label="Winner" tone="success" />
            </header>

            <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink/55">
              <span class="flex items-center gap-1.5">Serves <FlagValue :value="entry.variant.value" /></span>
              <span>{{ entry.variant.weight }}% of players</span>
              <span class="font-mono">{{ writeFigure(entry.players, { compact: true }) }} so far</span>
            </div>

            <div class="flex items-end justify-between gap-4">
              <div class="flex flex-col">
                <span class="text-[11px] text-ink/50">{{ EXPERIMENT_METRICS[experiment.primaryMetric].label }}</span>
                <span class="font-mono text-2xl leading-8 text-ink">{{ writeMetric(experiment.primaryMetric, entry.stats[experiment.primaryMetric].mean) }}</span>
              </div>
              <template v-for="other in split.others" :key="other.variant.id">
                <div v-if="other.variant.id === entry.variant.id" class="flex flex-col items-end">
                  <span :class="OUTCOME_TEXT[outcomeOf(other.comparisons[experiment.primaryMetric])]" class="font-mono text-lg leading-7">
                    {{ writeLift(experiment.primaryMetric, other.comparisons[experiment.primaryMetric]) }}
                  </span>
                  <span class="text-[11px] text-ink/50">{{ writeChance(other.comparisons[experiment.primaryMetric].chance) }} chance it is better</span>
                </div>
              </template>
              <span v-if="entry.variant.control" class="text-[11px] text-ink/45">What the others are measured against</span>
            </div>

            <template v-for="other in split.others" :key="`range-${other.variant.id}`">
              <div v-if="other.variant.id === entry.variant.id" class="flex flex-col gap-1">
                <LiftRange :comparison="other.comparisons[experiment.primaryMetric]" :extent="extentOf(experiment.primaryMetric)" />
                <div class="flex justify-between font-mono text-[10px] text-ink/40">
                  <span>worse</span>
                  <span>same as control</span>
                  <span>better</span>
                </div>
              </div>
            </template>
          </article>
        </div>
      </section>

      <MetricCard
        v-model="metricModel"
        :categories="categories"
        :decimals="EXPERIMENT_METRICS[metric].kind === 'money' ? 3 : 1"
        :options="METRIC_OPTIONS"
        :prefix="metricPrefix"
        :series="series"
        :unit="metricUnit"
        category-label="Day"
        type="line"
      />

      <MetricCard
        :categories="categories"
        :decimals="0"
        :series="chanceSeries"
        category-label="Day"
        :description="`For ${EXPERIMENT_METRICS[metric].label.toLowerCase()}, how sure the test was each day that a variant beats ${split.control.variant.name}. A result is called at 95%, and only after the first week: a line that crosses it early and falls back is the noise this waits out.`"
        title="Chance to beat the control, day by day"
        type="line"
        unit="%"
      />

      <SectionCard :padded="false" :description="`Everything the test measured, for each variant against ${split.control.variant.name}. A guardrail is a metric that must not get worse whatever the primary does.`" title="Every metric">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[720px] border-collapse text-left">
            <thead>
              <tr class="border-b border-line text-[11px] text-ink/45">
                <th class="px-5 py-2.5 font-normal">
                  Metric
                </th>
                <th class="px-3 py-2.5 text-right font-normal">
                  {{ split.control.variant.name }}
                </th>
                <th v-for="other in split.others" :key="other.variant.id" class="px-5 py-2.5 font-normal">
                  {{ other.variant.name }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in metricRows" :key="row.key" class="border-b border-line/70 last:border-b-0">
                <td class="px-5 py-3">
                  <span class="flex items-center gap-2">
                    <span class="text-xs text-ink">{{ EXPERIMENT_METRICS[row.key].label }}</span>
                    <Tag v-if="row.role" :label="row.role" :tone="row.role === 'Primary' ? 'chart-1' : 'neutral'" />
                  </span>
                </td>
                <td class="px-3 py-3 text-right font-mono text-xs text-ink/70">
                  {{ writeMetric(row.key, row.control) }}
                </td>
                <td v-for="other in row.others" :key="other.variant.id" class="px-5 py-3">
                  <div class="flex items-center gap-4">
                    <div class="flex w-28 flex-none flex-col">
                      <span class="font-mono text-xs text-ink">{{ writeMetric(row.key, other.value) }}</span>
                      <span :class="OUTCOME_TEXT[outcomeOf(other.comparison)]" class="font-mono text-[11px]">{{ writeLift(row.key, other.comparison) }} · {{ writeChance(other.comparison.chance) }}</span>
                    </div>
                    <LiftRange :comparison="other.comparison" :extent="extentOf(row.key)" class="min-w-24 flex-1" />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </SectionCard>
    </template>

    <SectionCard description="What was tested and decided, and when, as it happened." title="History">
      <div class="flex flex-col gap-5">
        <p v-if="experiment.conclusion" class="m-0 rounded-(--ps-radius) bg-ink/4 px-4 py-3 text-sm leading-6 text-ink/80 ring-1 ring-ink/6 ring-inset">
          {{ experiment.conclusion }}
        </p>
        <ol class="m-0 flex list-none flex-col p-0">
          <li v-for="(item, index) in [...experiment.events].reverse()" :key="index" class="relative flex gap-3 pb-4 last:pb-0">
            <span aria-hidden="true" class="absolute top-4 bottom-0 left-[5px] w-px bg-line [li:last-child>&]:hidden" />
            <span aria-hidden="true" class="relative mt-1.5 size-[11px] flex-none rounded-full border-2 border-primary bg-surface" />
            <div class="flex min-w-0 flex-1 flex-wrap items-baseline justify-between gap-x-3">
              <span class="text-xs text-ink">{{ item.text }}</span>
              <span class="text-[11px] text-ink/45">{{ shortDate(item.at) }}, {{ sinceWords(item.at, now) }} · {{ item.actor }}</span>
            </div>
          </li>
        </ol>
      </div>
    </SectionCard>

    <ConfirmDialog
      v-if="starting"
      v-model="starting"
      :description="`Players ${experiment.audience.toLowerCase()} are split ${experiment.variants.map(entry => `${entry.weight}% ${entry.name}`).join(', ')}, through ${experiment.flagKey}. Publish the flag for the split to reach players.`"
      action="Start experiment"
      title="Start this experiment?"
      tone="primary"
      @confirm="start(experiment.id)"
    />

    <ConfirmDialog
      v-if="shipping && winner"
      v-model="shipping"
      :description="`${experiment.flagKey} will serve ${writeValue(winner.value)} to everybody, and its test split is removed. It is saved to the flag's draft; publish it for players to get it.`"
      :action="`Ship ${winner.name}`"
      :title="`Ship ${winner.name}?`"
      tone="primary"
      @confirm="onShip"
    />

    <ExperimentEndDialog
      v-if="endOpen && ending"
      v-model="endOpen"
      :experiment="experiment"
      :mode="ending"
      :suggested="verdict.variant?.id ?? null"
      @confirm="onEnd"
    />
  </div>
</template>
