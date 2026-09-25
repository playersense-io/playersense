<script setup lang="ts">
// What the game has tested, what it is testing now, and what each test decided. Laid out for
// the questions people come here with: what is running and is it working (the cards at the
// top), when did we try that and what happened (the timeline), and everything else (the list).
//
// Several tests can run at once, as long as each changes something different: each is built on
// its own flag and draws its own players, so one test's variants are spread evenly across the
// other's. Two tests on the same screen or the same number would each blur the other's result,
// which is why a flag carries one test at a time.
//
// Experiments are the API's, and their results its measure of each arm from production.
const { project } = useWorkspace()
const { experiments, resultsFor, loading, error, reload } = useExperiments()
const router = useRouter()

interface Row extends ExperimentDef {
  verdict: Verdict
  results: ExperimentResults | null
  lead: Comparison | null
}

const rows = computed<Row[]>(() => experiments.value.map((experiment) => {
  const results = resultsFor(experiment)
  const verdict = verdictOf(experiment, results)
  const split = results ? againstControl(results) : null
  const leader = split?.others.find(entry => entry.variant.id === verdict.variant?.id) ?? split?.others[0]
  return { ...experiment, verdict, results, lead: leader?.comparisons[experiment.primaryMetric] ?? null }
}))

const running = computed(() => rows.value.filter(row => row.status === 'running'))

// --- The list -----------------------------------------------------------------------------

const STATUS_ORDER: ExperimentStatus[] = ['running', 'draft', 'concluded', 'stopped']

const statuses = useQueryList('status', STATUS_ORDER)
const query = ref('')

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'status', label: 'Status', options: STATUS_ORDER.map(value => ({ value, label: EXPERIMENT_STATUS[value].label })) }
]

const filters = computed<Record<string, string[]>>({
  get: () => ({ status: statuses.value }),
  set: (value) => {
    statuses.value = value.status ?? []
  }
})

const activeFilters = computed<ActiveFilter[]>(() => [
  ...statuses.value.map(value => ({ key: `status:${value}`, label: `Status: ${EXPERIMENT_STATUS[value as ExperimentStatus].label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else statuses.value = statuses.value.filter(value => `status:${value}` !== key)
}

function clearFilters() {
  query.value = ''
  statuses.value = []
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return rows.value.filter(row => (!statuses.value.length || statuses.value.includes(row.status))
    && (!needle || row.name.toLowerCase().includes(needle) || row.hypothesis.toLowerCase().includes(needle) || row.flagKey.includes(needle)))
})

const COLUMNS: DataTableColumn<Row>[] = [
  { key: 'name', title: 'Experiment', fixed: true, minWidth: 280, skeleton: 'identity' },
  { key: 'status', title: 'Status', value: row => STATUS_ORDER.indexOf(row.status), skeleton: 'pill' },
  { key: 'result', title: 'Result', sortable: false, minWidth: 240 },
  { key: 'primaryMetric', title: 'Measured by', value: row => EXPERIMENT_METRICS[row.primaryMetric].label },
  { key: 'started_at', title: 'When', minWidth: 200, value: row => row.started_at ?? row.created_at },
  { key: 'flagKey', title: 'Flag', mono: true, hidden: true }
]

const columns = useLocalStorage('playersense.experiments.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'started_at', order: 'desc' }])

const RESULT_TONES: Record<VerdictKind, BadgeTone> = {
  'draft': 'neutral',
  'collecting': 'neutral',
  'leading': 'success',
  'ready': 'success',
  'won': 'success',
  'control': 'warning',
  'unclear': 'neutral',
  'no-winner': 'neutral',
  'stopped': 'warning'
}

const creating = ref(false)
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Experiments">
      <template #description>
        What <span class="text-ink">{{ project?.name }}</span> has tested, what it is testing now, and what each test decided.
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:add"
          size="small"
          variant="flat"
          @click="creating = true"
        >
          New experiment
        </v-btn>
      </template>
    </PageHeader>

    <LoadState
      :error="error"
      :loading="loading && !experiments.length"
      what="the experiments"
      @retry="reload"
    >
      <!-- Running now: the tests somebody is waiting on, each with its answer so far. -->
      <section class="flex flex-col gap-3">
        <div class="flex flex-col gap-0.5">
          <h2 class="m-0 text-sm font-medium text-ink">
            Running now
          </h2>
          <p class="m-0 text-xs text-ink/60">
            Each test changes something different, so several can run at once without spoiling one another.
          </p>
        </div>

        <div v-if="running.length" class="grid gap-3 lg:grid-cols-2">
          <NuxtLink
            v-for="row in running"
            :key="row.id"
            :to="`/growth/experiments/${row.id}`"
            class="group flex flex-col gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-5 no-underline transition-colors hover:border-ink/25"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="flex min-w-0 flex-col gap-0.5">
                <span class="truncate text-sm font-medium text-ink group-hover:text-primary">{{ row.name }}</span>
                <span class="truncate text-xs text-ink/55">{{ row.audience }} · {{ EXPERIMENT_METRICS[row.primaryMetric].label }}</span>
              </div>
              <StatusBadge :label="row.verdict.title" :tone="RESULT_TONES[row.verdict.kind]" />
            </div>

            <div v-if="row.lead && row.verdict.kind !== 'collecting'" class="flex items-end justify-between gap-4">
              <div class="flex flex-col">
                <span class="text-[11px] text-ink/50">{{ row.verdict.variant ? variantTitle(row, row.verdict.variant) : 'Best variant' }} against A</span>
                <span :class="outcomeOf(row.lead) === 'better' ? 'text-success' : outcomeOf(row.lead) === 'worse' ? 'text-error' : 'text-ink'" class="font-mono text-2xl leading-8">{{ writeLift(row.primaryMetric, row.lead) }}</span>
              </div>
              <div class="flex flex-col items-end">
                <span class="text-[11px] text-ink/50">Chance it is really better</span>
                <span class="font-mono text-2xl leading-8 text-ink">{{ writeChance(row.lead.chance) }}</span>
              </div>
            </div>
            <p v-else class="m-0 text-xs leading-5 text-ink/60">
              {{ row.verdict.detail }}
            </p>

            <div class="flex flex-col gap-1.5">
              <span class="relative h-1.5 overflow-hidden rounded-full bg-ink/8">
                <span :style="{ width: `${Math.min(1, daysRun(row) / row.plannedDays) * 100}%` }" class="absolute inset-y-0 left-0 rounded-full bg-primary" />
              </span>
              <span class="text-[11px] text-ink/50">{{ whenWords(row) }}</span>
            </div>
          </NuxtLink>
        </div>
        <LayerCard v-else>
          <EmptyState description="Start one from a flag: its values become the variants, and its players are split between them." icon="material-symbols:science-outline" title="Nothing is running" />
        </LayerCard>
      </section>

      <SectionCard :padded="false" description="Every test on the days it ran, against the whole game's players and revenue. Hover a bar for how it ended; select it to open the results." title="Calendar">
        <div class="p-4">
          <ExperimentCalendar :experiments="experiments" />
        </div>
      </SectionCard>

      <SectionCard :padded="false" description="Every experiment, with what it was testing and what came of it." title="All experiments">
        <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
          <SearchField
            v-model="query"
            class="w-56"
            label="Search experiments"
            placeholder="Name, hypothesis or flag"
          />
          <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
          <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
        </TableToolbar>

        <DataTable
          v-model:column-state="columns"
          v-model:sort-by="sortBy"
          :columns="COLUMNS"
          :items="matched"
          :paged="false"
          empty-description="No experiment matches these filters."
          empty-title="No experiments match."
          item-value="id"
          label="Experiments"
        >
          <template #cell.name="{ item }">
            <NuxtLink :to="`/growth/experiments/${item.id}`" class="group flex min-w-0 flex-col py-1 no-underline">
              <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
              <span class="line-clamp-1 text-[11px] whitespace-normal text-ink/50">{{ item.hypothesis }}</span>
            </NuxtLink>
          </template>
          <template #cell.status="{ item }">
            <StatusBadge :label="EXPERIMENT_STATUS[item.status].label" :tone="EXPERIMENT_STATUS[item.status].tone" />
          </template>
          <template #cell.result="{ item }">
            <span class="flex min-w-0 items-center gap-2">
              <Icon
                v-if="item.shipped"
                aria-label="Shipped"
                class="flex-none text-sm text-success"
                name="material-symbols:rocket-launch-outline"
              />
              <span class="truncate text-xs text-ink/80">{{ item.verdict.title }}</span>
              <span v-if="item.lead && item.status !== 'draft'" class="flex-none font-mono text-[11px] text-ink/50">{{ writeLift(item.primaryMetric, item.lead) }}</span>
            </span>
          </template>
          <template #cell.primaryMetric="{ item }">
            <span class="text-xs">{{ EXPERIMENT_METRICS[item.primaryMetric].label }}</span>
          </template>
          <template #cell.started_at="{ item }">
            <span class="text-xs text-ink/70">{{ whenWords(item) }}</span>
          </template>
        </DataTable>
      </SectionCard>
    </LoadState>

    <NewExperimentDialog v-if="creating" v-model="creating" @created="router.push(`/growth/experiments/${$event}`)" />
  </div>
</template>
