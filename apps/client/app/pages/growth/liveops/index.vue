<script setup lang="ts">
// Everything the game has scheduled, what is on now, and what each event did. Laid out for the
// questions people come here with: what is live and is it working (the cards at the top), what
// is coming (beside them), what ran when and on top of what (the calendar), did the game move
// when it ran (the chart, with every event shaded where it was on), which events worked (the
// ranking), and everything else (the table).
//
// Several events can be on at once, and usually are: a season runs under a weekend boost under
// a sale. That is why every figure on these screens is for the players an event reached against
// what they would have done without it, and why a result says what else was on at the time.
//
// Sample data (app/utils/liveops-sample.ts). Kept in this browser until the dashboard is wired to
// /liveops-events and a daily query over sessions and purchases.
const { project } = useWorkspace()
const { events } = useLiveOps()
const { experiments } = useExperiments()

const now = useNow({ interval: 60_000 })
const time = computed(() => now.value.getTime())

interface Row extends LiveOpsEventDef {
  state: LiveOpsState
  current: Occurrence | null
  next: Occurrence | null
  /** The last window that began, and what it did. */
  last: EventImpact | null
}

const rows = computed<Row[]>(() => events.value.map((event) => {
  const { state, current, next } = stateOf(event, time.value)
  const run = runsOf(event, time.value, 1)[0]
  return { ...event, state, current, next, last: run && event.status !== 'draft' ? sampleEventImpact(event, run, time.value) : null }
}))

// --- On now, and next ---------------------------------------------------------------------

const live = computed(() => rows.value.filter(row => row.state === 'live'))

const upcoming = computed(() => rows.value
  .filter(row => row.state === 'scheduled' && row.next && row.next.start - time.value < 45 * DAY)
  .sort((a, b) => a.next!.start - b.next!.start)
  .slice(0, 5))

/* "30 days left", "5 hours left": the one thing about a live event's time worth a line. */
function leftWords(row: Row): string {
  if (!row.current) return ''
  return `${relativeWords(row.current.end, time.value).replace('in ', '')} left`
}

/*
 * A live event as a metric tile: its name, the metric it is meant to move a day while on, and
 * how far that is from what its players would have done without it. An event not meant to
 * move anything shows the players it reached.
 */
function tileOf(row: Row) {
  const goal = row.goal
  if (!row.last || !goal) return { label: row.name, value: row.last ? row.last.players : null, compact: true }
  const kind = LIVEOPS_METRICS[goal].kind
  const scale = kind === 'rate' ? 100 : 1
  const metric = row.last.metrics[goal]
  return {
    label: row.name,
    value: metric.during * scale,
    previous: metric.expected * scale,
    compare: row.last.method === 'holdout' ? 'vs held back' : 'vs expected',
    prefix: kind === 'money' ? '$' : '',
    unit: ({ rate: '%', money: '', count: '', minutes: 'min', ratio: '' })[kind],
    decimals: kind === 'money' && metric.during < 10 ? 3 : 1,
    compact: true
  }
}

const startFormat = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric' })

/* "Fri, Sep 25 at 18:00". */
const startWords = (window: Occurrence) => `${startFormat.format(window.start)} at ${new Date(window.start).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`

/* "18h", "3d": the countdown, as short as it can be read. */
function untilWords(window: Occurrence): string {
  const gap = Math.max(0, window.start - time.value)
  if (gap >= 2 * DAY) return `${Math.round(gap / DAY)}d`
  if (gap >= 3_600_000) return `${Math.round(gap / 3_600_000)}h`
  return `${Math.max(1, Math.round(gap / 60_000))}m`
}

const progress = (row: Row) => (row.current ? Math.min(1, (time.value - row.current.start) / (row.current.end - row.current.start)) : 0)

const OUTCOME_TEXT = { better: 'text-success', worse: 'text-error', unclear: 'text-ink' } as const

// --- The game, with every event on it -----------------------------------------------------

const metric = ref<LiveOpsMetricKey>('revenue')
const METRIC_OPTIONS = LIVEOPS_METRIC_KEYS.map(key => ({ value: key, label: LIVEOPS_METRICS[key].label, description: LIVEOPS_METRICS[key].note }))
const metricModel = computed({
  get: () => metric.value as string,
  set: (value: string) => {
    metric.value = value as LiveOpsMetricKey
  }
})

const PERIODS = [
  { value: '28', label: '4 weeks' },
  { value: '56', label: '8 weeks' },
  { value: '91', label: '13 weeks' }
]
const period = ref('56')

const served = computed(() => events.value.filter(event => event.status !== 'draft'))

const chartDays = computed(() => {
  const count = Number(period.value)
  const today = startOfDay(time.value)
  return Array.from({ length: count }, (_, index) => startOfDay(today - (count - 1 - index) * DAY + 12 * 3_600_000))
})

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })
const titleFormat = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric' })

const categories = computed(() => chartDays.value.map(at => dayFormat.format(at)))

/* Titles name what was on that day, so the tooltip says why a day stands out. */
const titles = computed(() => chartDays.value.map((at) => {
  const on = served.value.filter(event => servedWindows(event, at, at + DAY).length && !isRhythm(event.schedule) && event.kind !== 'announcement').map(event => event.name)
  return on.length ? `${titleFormat.format(at)} · ${on.slice(0, 3).join(', ')}${on.length > 3 ? ` and ${on.length - 3} more` : ''}` : titleFormat.format(at)
}))

const scaleOf = (key: LiveOpsMetricKey) => (LIVEOPS_METRICS[key].kind === 'rate' ? 100 : 1)

const gameSeries = computed<MetricSeries[]>(() => [{
  name: LIVEOPS_METRICS[metric.value].label,
  data: chartDays.value.map((at) => {
    const figures = sampleGameDay(at, served.value)
    return figures && at !== startOfDay(time.value) ? Math.round(figures[metric.value] * scaleOf(metric.value) * 1000) / 1000 : null
  }),
  type: 'area',
  tone: 1
}])

/*
 * Which events are marked. Every event at once is a chart of stripes, so it opens on the short
 * one-offs, a sale or a festival, which are what a spike is usually asked about; a season, a
 * weekly boost or a monthly calendar is a filter away. Hover a day for everything on it.
 */
const chartStart = computed(() => chartDays.value[0]!)
const chartEnd = computed(() => chartDays.value.at(-1)! + DAY)

const eventsInView = computed(() => served.value.filter(event => !isRhythm(event.schedule) && servedWindows(event, chartStart.value, chartEnd.value).length))

const markedByDefault = (event: LiveOpsEventDef) => event.kind !== 'announcement'
  && (event.schedule.mode === 'once' || event.schedule.mode === 'yearly')
  && parseDuration(event.schedule.duration) <= 21 * DAY

const markFilter = ref<Record<string, string[]>>({ event: eventsInView.value.filter(markedByDefault).map(event => event.key) })
const marked = computed(() => markFilter.value.event ?? [])

const MARK_GROUPS = computed<FilterGroup[]>(() => [{
  key: 'event',
  label: 'Mark on the chart',
  noun: 'event',
  options: eventsInView.value.map(event => ({ value: event.key, label: event.name }))
}])

const marks = computed<MetricMark[]>(() => {
  const first = chartStart.value
  const index = (at: number) => Math.max(0, Math.min(chartDays.value.length - 1, Math.round((startOfDay(at) - first) / DAY)))
  return eventsInView.value
    .filter(event => marked.value.includes(event.key))
    .flatMap(event => servedWindows(event, first, chartEnd.value).map(window => ({
      from: index(window.start),
      to: index(lastMinute(window)),
      label: window.start >= first ? event.name : undefined,
      tone: kindLook(event.kind).tone,
      quiet: window.start < first
    })))
})

const metricPrefix = computed(() => (LIVEOPS_METRICS[metric.value].kind === 'money' ? '$' : ''))
const metricUnit = computed(() => ({ rate: '%', money: '', count: '', minutes: 'min', ratio: '' })[LIVEOPS_METRICS[metric.value].kind])

// --- What worked --------------------------------------------------------------------------

interface Ranked {
  id: string
  event: LiveOpsEventDef
  impact: EventImpact
  goal: LiveOpsMetricKey
}

/* Every window that ended in the period, on the metric each event was meant to move. */
const ranked = computed<Ranked[]>(() => served.value
  .filter(event => event.goal)
  .flatMap(event => runsOf(event, time.value, 12)
    .filter(run => run.end <= time.value && run.end > time.value - Number(period.value) * DAY)
    .map(run => ({ id: `${event.key}:${run.start}`, event, impact: sampleEventImpact(event, run, time.value)!, goal: event.goal! })))
  .filter(entry => entry.impact)
  .sort((a, b) => b.impact.metrics[b.goal].comparison.lift - a.impact.metrics[a.goal].comparison.lift))

const RANK_COLUMNS: DataTableColumn<Ranked>[] = [
  { key: 'event', title: 'Event', fixed: true, minWidth: 240, value: entry => entry.event.name },
  { key: 'when', title: 'When', value: entry => entry.impact.occurrence.start },
  { key: 'goal', title: 'Meant to move', value: entry => LIVEOPS_METRICS[entry.goal].label },
  { key: 'effect', title: 'Effect against expected', minWidth: 300, value: entry => entry.impact.metrics[entry.goal].comparison.lift },
  { key: 'after', title: 'After it', align: 'end', value: entry => entry.impact.metrics[entry.goal].after?.lift ?? 0 }
]

const rankColumns = useLocalStorage('playersense.liveops.worked.columns', defaultColumnState(RANK_COLUMNS))
const rankSort = ref<DataTableSort[]>([{ key: 'effect', order: 'desc' }])

/* The scale the effect bars share: the lowest and highest the ranges reach. */
const rankDomain = computed<[number, number]>(() => {
  const all = ranked.value.map(entry => entry.impact.metrics[entry.goal].comparison)
  return [Math.min(0, ...all.map(item => item.low)), Math.max(0.05, ...all.map(item => item.high))]
})

// --- The list -----------------------------------------------------------------------------

const states = useQueryList('state', STATE_ORDER)
const kinds = ref<string[]>([])
const query = ref('')

const FILTER_GROUPS = computed<FilterGroup[]>(() => [
  { key: 'state', label: 'State', options: STATE_ORDER.map(value => ({ value, label: LIVEOPS_STATE[value].label })) },
  { key: 'kind', label: 'Kind', options: [...new Set(events.value.map(event => event.kind))].map(value => ({ value, label: kindLook(value).label })) }
])

const filters = computed<Record<string, string[]>>({
  get: () => ({ state: states.value, kind: kinds.value }),
  set: (value) => {
    states.value = value.state ?? []
    kinds.value = value.kind ?? []
  }
})

const activeFilters = computed<ActiveFilter[]>(() => [
  ...states.value.map(value => ({ key: `state:${value}`, label: `State: ${LIVEOPS_STATE[value as LiveOpsState].label}` })),
  ...kinds.value.map(value => ({ key: `kind:${value}`, label: `Kind: ${kindLook(value).label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') query.value = ''
  else if (key.startsWith('state:')) states.value = states.value.filter(value => `state:${value}` !== key)
  else kinds.value = kinds.value.filter(value => `kind:${value}` !== key)
}

function clearFilters() {
  query.value = ''
  states.value = []
  kinds.value = []
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return rows.value.filter(row => (!states.value.length || states.value.includes(row.state))
    && (!kinds.value.length || kinds.value.includes(row.kind))
    && (!needle || row.name.toLowerCase().includes(needle) || row.key.includes(needle) || row.description.toLowerCase().includes(needle)))
})

const COLUMNS: DataTableColumn<Row>[] = [
  { key: 'name', title: 'Event', fixed: true, minWidth: 280, skeleton: 'identity' },
  { key: 'state', title: 'State', value: row => STATE_ORDER.indexOf(row.state), skeleton: 'pill' },
  { key: 'kind', title: 'Kind', value: row => kindLook(row.kind).label },
  { key: 'schedule', title: 'When', minWidth: 260, sortable: false },
  { key: 'audience', title: 'Who', minWidth: 200, sortable: false },
  { key: 'result', title: 'Last result', minWidth: 200, value: row => (row.last && row.goal ? row.last.metrics[row.goal].comparison.lift : -Infinity) },
  { key: 'updated_at', title: 'Updated', value: row => row.updated_at, hidden: true },
  { key: 'key', title: 'Key', mono: true, hidden: true }
]

const columns = useLocalStorage('playersense.liveops.columns', defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'state', order: 'asc' }])

const { segmentName } = useLiveConfig()
const audienceWords = (rule: TargetRule) => describeRule(rule, segmentName) || 'Every player'

const creating = ref(false)
const calendarFull = ref(false)
const router = useRouter()
</script>

<template>
  <div class="flex flex-col gap-8">
    <PageHeader title="Live Ops">
      <template #description>
        What <span class="text-ink">{{ project.name }}</span> has scheduled, what is on now, and what each event did to the game.
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
          New event
        </v-btn>
      </template>
    </PageHeader>

    <InfoNote text="Events and their results are sample data until the dashboard reads /liveops-events and the daily figures. Whether an event is on for a player is already decided by the server, from the published schedule." />

    <!-- On now: a tile an event, its metric while on against what was expected without it. -->
    <section class="flex flex-col gap-3">
      <h2 class="m-0 flex items-center gap-2 text-sm font-medium text-ink">
        On now
        <span class="rounded-full bg-success/12 px-1.5 font-mono text-[11px] leading-4 text-success">{{ live.length }}</span>
      </h2>
      <div v-if="live.length" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <NuxtLink
          v-for="row in live"
          :key="row.key"
          :to="`/growth/liveops/${row.key}`"
          class="block rounded-(--ps-radius-large) no-underline transition-shadow hover:shadow-[0_0_0_1px_rgba(var(--v-theme-on-surface),0.18)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        >
          <Statistic v-bind="tileOf(row)">
            <template #description>
              <span class="flex flex-col gap-2">
                <span class="truncate text-ink/50">{{ row.goal ? LIVEOPS_METRICS[row.goal].label : 'Players reached' }} a day · {{ leftWords(row) }}</span>
                <span :aria-label="`${Math.round(progress(row) * 100)}% through`" class="relative h-1 overflow-hidden rounded-full bg-ink/8" role="img">
                  <span :style="{ width: `${progress(row) * 100}%`, background: toneColour(kindLook(row.kind).tone) }" class="absolute inset-y-0 left-0 rounded-full" />
                </span>
              </span>
            </template>
          </Statistic>
        </NuxtLink>
      </div>
      <LayerCard v-else>
        <p class="m-0 py-6 text-center text-xs text-ink/50">
          Nothing is on right now.
        </p>
      </LayerCard>
    </section>

    <!-- Next up: the windows due, soonest first, in one row. -->
    <section class="flex flex-col gap-3">
      <h2 class="m-0 text-sm font-medium text-ink">
        Next up
      </h2>
      <div class="flex overflow-x-auto rounded-(--ps-radius-large) border border-line bg-surface">
        <NuxtLink
          v-for="row in upcoming"
          :key="row.key"
          :to="`/growth/liveops/${row.key}`"
          class="group flex min-w-52 flex-1 items-center gap-3 border-r border-line/70 px-4 py-3 no-underline last:border-r-0 hover:bg-ink/3"
        >
          <LiveOpsKindMark :kind="row.kind" bare />
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="truncate text-[13px] text-ink group-hover:text-primary">{{ row.name }}</span>
            <span class="truncate text-[11px] text-ink/50">{{ startWords(row.next!) }}</span>
          </span>
          <span class="flex-none font-mono text-xs text-ink/60">{{ untilWords(row.next!) }}</span>
        </NuxtLink>
        <p v-if="!upcoming.length" class="m-0 flex-1 px-4 py-6 text-center text-xs text-ink/50">
          Nothing is due in the next six weeks.
        </p>
      </div>
    </section>

    <SectionCard :padded="false" description="Every event on the days it was on, in a band per kind, with the A/B tests running at the time and the whole game's day along the top. Hover a bar for what it did; select it for its results." title="Calendar">
      <div class="p-4">
        <LiveOpsCalendar :events="events" :experiments="experiments" @expand="calendarFull = true" />
      </div>
    </SectionCard>

    <!-- The game, with every event shaded where it ran. -->
    <MetricCard
      v-model="metricModel"
      :categories="categories"
      :decimals="LIVEOPS_METRICS[metric].kind === 'money' && metric === 'arpdau' ? 3 : 1"
      :height="300"
      :marks="marks"
      :options="METRIC_OPTIONS"
      :prefix="metricPrefix"
      :series="gameSeries"
      :titles="titles"
      :unit="metricUnit"
      category-label="Day"
      type="area"
    >
      <template #description>
        The whole game, day by day, with the events chosen under Filter shaded in their kind's colour and a line where each began: a figure that rises with the shading rose with the event. Hover a day for everything that was on. Each event's own page says by how much, against its own players.
      </template>
      <template #actions>
        <FilterMenu v-model="markFilter" :groups="MARK_GROUPS" />
        <SegmentedControl v-model="period" :options="PERIODS" label="Period" />
      </template>
    </MetricCard>

    <SectionCard :padded="false" :description="`Every window that ended in the last ${PERIODS.find(entry => entry.value === period)?.label}, on the metric each event was meant to move, best first. The line is the range the true effect is in, 95 times in 100.`" title="What worked">
      <DataTable
        v-model:column-state="rankColumns"
        v-model:sort-by="rankSort"
        :columns="RANK_COLUMNS"
        :items="ranked"
        empty-description="No event with a metric to move ended in this period."
        empty-title="Nothing ended in this period."
        item-value="id"
        label="What worked"
      >
        <template #cell.event="{ item }">
          <NuxtLink :to="`/growth/liveops/${item.event.key}?at=${item.impact.occurrence.start}`" class="group flex min-w-0 items-center gap-2.5 py-1 no-underline">
            <LiveOpsKindMark :kind="item.event.kind" bare />
            <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.event.name }}</span>
          </NuxtLink>
        </template>
        <template #cell.when="{ item }">
          <span class="text-xs whitespace-nowrap text-ink/65">{{ windowShort(item.impact.occurrence) }}</span>
        </template>
        <template #cell.goal="{ item }">
          <span class="text-xs text-ink/65">{{ LIVEOPS_METRICS[item.goal].label }}</span>
        </template>
        <template #cell.effect="{ item }">
          <span class="flex items-center gap-3">
            <span :class="OUTCOME_TEXT[outcomeOf(item.impact.metrics[item.goal].comparison)]" class="w-16 flex-none text-right font-mono text-xs">{{ writeOpsLift(item.goal, item.impact.metrics[item.goal].comparison) }}</span>
            <EffectBar :comparison="item.impact.metrics[item.goal].comparison" :domain="rankDomain" class="min-w-40 flex-1" />
          </span>
        </template>
        <template #cell.after="{ item }">
          <span v-if="item.impact.metrics[item.goal].after && outcomeOf(item.impact.metrics[item.goal].after!) === 'worse'" class="font-mono text-[11px] text-warning">
            {{ writeOpsLift(item.goal, item.impact.metrics[item.goal].after!) }}
          </span>
          <span v-else class="font-mono text-[11px] text-ink/40">{{ item.impact.metrics[item.goal].after ? 'held' : '–' }}</span>
        </template>
      </DataTable>
    </SectionCard>

    <SectionCard :padded="false" description="Every event, with when it is on, who it is for, and what it did last time." title="All events">
      <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
        <SearchField
          v-model="query"
          class="w-56"
          label="Search events"
          placeholder="Name, key or description"
        />
        <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
        <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
      </TableToolbar>

      <DataTable
        v-model:column-state="columns"
        v-model:sort-by="sortBy"
        :columns="COLUMNS"
        :items="matched"
        empty-description="No event matches these filters."
        empty-title="No events match."
        item-value="key"
        label="Live Ops events"
      >
        <template #cell.name="{ item }">
          <NuxtLink :to="`/growth/liveops/${item.key}`" class="group flex min-w-0 items-center gap-3 py-1 no-underline">
            <LiveOpsKindMark :kind="item.kind" bare />
            <span class="flex min-w-0 flex-col">
              <span class="truncate text-[13px] text-ink transition-colors group-hover:text-primary">{{ item.name }}</span>
              <span class="truncate font-mono text-[11px] text-ink/45">{{ item.key }}</span>
            </span>
          </NuxtLink>
        </template>
        <template #cell.state="{ item }">
          <span class="flex flex-col items-start gap-0.5">
            <StatusBadge :label="LIVEOPS_STATE[item.state].label" :tone="LIVEOPS_STATE[item.state].tone" />
            <span class="text-[11px] text-ink/45">{{ stateWords(item, time) }}</span>
          </span>
        </template>
        <template #cell.kind="{ item }">
          <span class="text-xs text-ink/70">{{ kindLook(item.kind).label }}</span>
        </template>
        <template #cell.schedule="{ item }">
          <span class="flex items-start gap-1.5 text-xs whitespace-normal text-ink/70">
            <Icon
              v-if="item.schedule.mode !== 'once'"
              aria-label="Repeats"
              class="mt-0.5 flex-none text-sm text-ink/45"
              name="material-symbols:event-repeat-outline"
            />
            <span class="line-clamp-2">{{ scheduleWords(item.schedule) }}</span>
          </span>
        </template>
        <template #cell.audience="{ item }">
          <span class="line-clamp-2 text-xs whitespace-normal text-ink/70">{{ audienceWords(item.audience) }}<template v-if="item.rollout < FULL_ROLLOUT">, {{ formatRollout(item.rollout) }}</template></span>
        </template>
        <template #cell.result="{ item }">
          <span v-if="item.last && item.goal" class="flex min-w-0 items-baseline gap-2">
            <span :class="OUTCOME_TEXT[outcomeOf(item.last.metrics[item.goal].comparison)]" class="font-mono text-xs">{{ writeOpsLift(item.goal, item.last.metrics[item.goal].comparison) }}</span>
            <span class="truncate text-[11px] text-ink/50">{{ LIVEOPS_METRICS[item.goal].label.toLowerCase() }}, {{ windowShort(item.last.occurrence) }}</span>
          </span>
          <span v-else class="text-[11px] text-ink/40">{{ item.goal ? 'Not run yet' : 'Not measured' }}</span>
        </template>
      </DataTable>
    </SectionCard>

    <v-dialog v-model="calendarFull" fullscreen transition="fade-transition">
      <v-card class="flex h-full flex-col" flat>
        <LiveOpsCalendar
          v-if="calendarFull"
          :events="events"
          :experiments="experiments"
          class="min-h-0 flex-1"
          fullscreen
          @close="calendarFull = false"
        />
      </v-card>
    </v-dialog>

    <LiveOpsEventDialog v-if="creating" v-model="creating" @saved="router.push(`/growth/liveops/${$event}`)" />
  </div>
</template>
