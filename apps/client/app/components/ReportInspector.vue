<script setup lang="ts">
import type { Schema } from '@playersense/contracts'

// The selected node's settings, beside the canvas, in the same fields a flag's rules are
// written with: 36px outlined fields, the panel's own menus, a column picker drawn as a field,
// and a list of conditions with the joining word between rows. What can be chosen comes from
// what flows into the node: a filter offers the columns its input has, a sum only the numeric
// ones, so a setting that cannot run is not on offer. Every change applies at once, and the
// preview under the canvas shows what came of it.
const props = defineProps<{
  node: GraphNode
  /** What flows in: the columns and rows of the node before it. Empty for a source. */
  input: NodeResult | null
  result: NodeResult | null
  /** For Start and a delivery, when the report runs, in words; for a Schedule, nothing. */
  timing?: string | null
  /** For a Schedule on Start, the deliveries it sends; for Start, its Schedules; for Finish, the outputs. */
  drives?: readonly string[]
  /** Whose rows the report reads, for the events a source can pick. */
  environment?: string
}>()

const emit = defineEmits<{
  update: [patch: Partial<ReportNodeData>]
  remove: []
  /** Preview a Notify's message and send it as a test; run the report, for an Export. */
  test: []
}>()

const data = computed(() => props.node.data)
const config = computed(() => data.value.config)
const kind = computed(() => KINDS[data.value.kind])
const tone = computed(() => toneColour(familyTone(kind.value.family)))

function set(patch: NodeConfig) {
  emit('update', { config: { ...config.value, ...patch } })
}

const columns = computed(() => props.input?.columns ?? [])
const typeOf = (key: string) => columns.value.find(column => column.key === key)?.type ?? 'string'

/* The values a column takes in the rows flowing in, most common first: a filter's suggestions. */
function valuesOf(key: string): string[] {
  const counts = new Map<string, number>()
  for (const row of props.input?.rows ?? []) {
    const value = row[key]
    if (value === null || value === undefined) continue
    counts.set(String(value), (counts.get(String(value)) ?? 0) + 1)
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, 30).map(([value]) => value)
}

/* Every choice reads a string from its control and writes the typed value back. */
const str = (value: unknown) => (typeof value === 'string' ? value : '')

/* A list edited a row at a time: replace one, remove one. */
function replaced<T>(list: readonly T[], index: number, value: T): T[] {
  return list.map((entry, at) => (at === index ? value : entry))
}

function without<T>(list: readonly T[], index: number): T[] {
  return list.filter((_, at) => at !== index)
}

// --- Source -------------------------------------------------------------------------------

const RANGE_OPTIONS: SegmentedOption[] = RANGES.map(entry => ({ value: entry.value, label: entry.label.replace('Last ', '') }))
/* The events the project has had, in the report's environment (GET …/analytics/{env}/events). */
const workspace = useWorkspaceStore()
const catalogue = useApiGet<Schema<'EventCatalogueOut'>>(() => workspace.projectPath && `${workspace.projectPath}/analytics/${props.environment ?? workspace.environment}/events`)
const EVENT_ITEMS = computed(() => {
  const names = (catalogue.data.value?.events ?? []).map(event => event.name).sort()
  /* One picked before and not seen lately stays on offer, so the field never blanks. */
  if (config.value.event && !names.includes(config.value.event)) names.unshift(config.value.event)
  return [{ value: '', title: 'Every event' }, ...names.map(name => ({ value: name, title: name }))]
})

function chooseSource(source: SourceKey) {
  const renamed = data.value.title === SOURCES[config.value.source ?? 'purchases'].label
  emit('update', { config: { ...config.value, source, event: '' }, ...(renamed ? { title: SOURCES[source].label } : {}) })
}

// --- Filter -------------------------------------------------------------------------------

const MATCH_ITEMS = [{ value: 'all', title: 'All' }, { value: 'any', title: 'Any' }]
const conditions = computed(() => config.value.conditions ?? [])
const joiner = computed(() => (config.value.match === 'any' ? 'or' : 'and'))

const opsFor = (column: string) => (Object.entries(FILTER_OPS) as [FilterOp, typeof FILTER_OPS[FilterOp]][])
  .filter(([, info]) => info.types === 'any' || info.types.includes(typeOf(column)))
  .map(([value, info]) => ({ value, title: info.label }))

function setCondition(index: number, patch: Partial<FilterCondition>) {
  const merged = { ...conditions.value[index]!, ...patch }
  /* A new column may not take the old comparison: fall back to the one that fits it. */
  if (patch.column !== undefined && !opsFor(patch.column).some(op => op.value === merged.op)) {
    merged.op = typeOf(patch.column) === 'time' ? 'within' : 'is'
    merged.value = ''
  }
  set({ conditions: replaced(conditions.value, index, merged) })
}

function addCondition() {
  set({ conditions: [...conditions.value, { column: '', op: 'is', value: '' }] })
}

const valuePlaceholder = (condition: FilterCondition) => {
  const type = typeOf(condition.column)
  if (condition.op === 'within') return 'Days'
  if (type === 'money') return 'Dollars'
  return isNumeric(type) ? '0' : 'Value'
}

// --- Join ---------------------------------------------------------------------------------

const JOIN_OPTIONS: SegmentedOption[] = [
  { value: 'inner', label: 'Only matches' },
  { value: 'left', label: 'Every left row' }
]

// --- Group --------------------------------------------------------------------------------

const BUCKET_OPTIONS: SegmentedOption[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' }
]
const by = computed(() => config.value.by ?? [])
const metrics = computed(() => config.value.metrics ?? [])
const groupsByTime = computed(() => by.value.some(key => typeOf(key) === 'time'))
const METRIC_ITEMS = (Object.entries(METRIC_FNS) as [MetricFn, typeof METRIC_FNS[MetricFn]][]).map(([value, info]) => ({ value, title: info.label }))

function setMetric(index: number, patch: Partial<GroupMetric>) {
  set({ metrics: replaced(metrics.value, index, { ...metrics.value[index]!, ...patch }) })
}

function addMetric() {
  const numeric = columns.value.find(column => isNumeric(column.type))
  set({ metrics: [...metrics.value, numeric ? { fn: 'sum', column: numeric.key, as: numeric.key } : { fn: 'count', column: '', as: `rows_${metrics.value.length + 1}` }] })
}

/* A figure's name follows what it works out until it is renamed by hand. */
function metricName(fn: MetricFn, column: string) {
  return fn === 'count' ? 'rows' : fn === 'count_distinct' ? `unique_${column || 'values'}` : `${fn}_${column || 'value'}`
}

function setMetricFn(index: number, fn: MetricFn) {
  const metric = metrics.value[index]!
  const automatic = metric.as === metricName(metric.fn, metric.column) || metric.as === metric.column
  const column = METRIC_FNS[fn].numeric && !isNumeric(typeOf(metric.column)) ? '' : metric.column
  setMetric(index, { fn, column, ...(automatic ? { as: metricName(fn, column) } : {}) })
}

function setMetricColumn(index: number, column: string) {
  const metric = metrics.value[index]!
  const automatic = !metric.as || metric.as === metricName(metric.fn, metric.column) || metric.as === metric.column
  setMetric(index, { column, ...(automatic ? { as: metricName(metric.fn, column) } : {}) })
}

// --- Formula ------------------------------------------------------------------------------

const OP_OPTIONS: SegmentedOption[] = [
  { value: '+', label: '+', title: 'Plus' },
  { value: '-', label: '−', title: 'Minus' },
  { value: '*', label: '×', title: 'Times' },
  { value: '/', label: '÷', title: 'Divided by' }
]
const FORMAT_OPTIONS: SegmentedOption[] = [
  { value: 'number', label: 'Number' },
  { value: 'money', label: 'Money' },
  { value: 'percent', label: 'Percent' }
]
const RIGHT_OPTIONS: SegmentedOption[] = [
  { value: 'column', label: 'A column' },
  { value: 'number', label: 'A number' }
]
/* Whether the formula's second half is a column or a number typed in. */
const rightMode = ref<'column' | 'number'>(config.value.right && !columns.value.some(column => column.key === config.value.right) ? 'number' : 'column')

function setRightMode(mode: string) {
  rightMode.value = mode === 'number' ? 'number' : 'column'
  set({ right: '' })
}

// --- Sort, chart, export ------------------------------------------------------------------

const ORDER_OPTIONS: SegmentedOption[] = [
  { value: 'desc', label: 'Highest first' },
  { value: 'asc', label: 'Lowest first' }
]
const CHART_OPTIONS: SegmentedOption[] = [
  { value: 'line', label: 'Line' },
  { value: 'bar', label: 'Bars' },
  { value: 'area', label: 'Area' }
]
/* What the server writes today: JSON or CSV, to a download. The rest are said to be coming, and
   a run that meets one says it was skipped. */
const FILE_OPTIONS: SegmentedOption[] = [
  { value: 'json', label: 'JSON' },
  { value: 'csv', label: 'CSV' }
]
const DESTINATION_OPTIONS = computed<ChoiceOption[]>(() => (Object.entries(DESTINATIONS) as [keyof typeof DESTINATIONS, typeof DESTINATIONS[keyof typeof DESTINATIONS]][]).map(([value, info]) => ({
  value,
  label: value === 'download' ? info.label : `${info.label}, coming`,
  note: value === 'download' ? info.note : `${info.note} Not written yet: a run skips it and says so.`
})))

const limitText = computed({
  get: () => (config.value.limit ? String(config.value.limit) : ''),
  set: (value: string) => {
    const number = Math.floor(Number(value))
    set({ limit: value.trim() && Number.isFinite(number) && number > 0 ? number : null })
  }
})

const figures = computed(() => config.value.y ?? [])

const file = computed(() => config.value.file ?? 'json')
const writable = computed(() => (config.value.destination ?? 'download') === 'download' && (file.value === 'json' || file.value === 'csv'))

// --- Schedule -----------------------------------------------------------------------------

const PRESET_ITEMS = [...CRON_PRESETS.map(entry => ({ value: entry.value, title: entry.label })), { value: 'custom', title: 'Custom cron' }]
const TIMEZONE_ITEMS = CRON_TIMEZONES.map(zone => ({ value: zone.value, title: zone.label }))

/* The cron line as typed, kept apart so a half-typed line is not thrown away. */
const cronText = ref(config.value.cron ?? '')
const cronProblem = computed(() => {
  const parsed = parseCron(cronText.value)
  return typeof parsed === 'string' ? parsed : null
})
const preset = computed(() => (CRON_PRESETS.some(entry => entry.value === cronText.value.trim()) ? cronText.value.trim() : 'custom'))

function setCron(text: string) {
  cronText.value = text
  if (typeof parseCron(text) !== 'string') {
    const renamed = data.value.title === describeCron(config.value.cron ?? '') || data.value.title === KINDS.schedule.label
    emit('update', { config: { ...config.value, cron: text.trim() }, ...(renamed ? { title: describeCron(text) } : {}) })
  }
}

function choosePreset(value: string) {
  if (value !== 'custom') setCron(value)
}

const now = useNow({ interval: 60_000 })
const upcoming = computed(() => (config.value.paused || cronProblem.value ? [] : nextRuns(config.value.cron ?? '', config.value.timezone ?? 'UTC', now.value.getTime(), 5)))

const paused = computed({
  get: () => Boolean(config.value.paused),
  set: (value: boolean) => set({ paused: value })
})

// --- Notify -------------------------------------------------------------------------------

const { channels } = useChannels()
const chosenChannels = computed(() => config.value.channels ?? [])

function toggleChannel(id: string) {
  set({ channels: chosenChannels.value.includes(id) ? chosenChannels.value.filter(entry => entry !== id) : [...chosenChannels.value, id] })
}

const WHEN_OPTIONS = computed<ChoiceOption[]>(() => (Object.keys(NOTIFY_WHEN) as NotifyWhen[]).map(value => ({ value, label: NOTIFY_WHEN[value].label, note: NOTIFY_WHEN[value].note })))
const INCLUDE_OPTIONS: SegmentedOption[] = (Object.keys(NOTIFY_INCLUDE) as NotifyInclude[]).map(value => ({ value, label: NOTIFY_INCLUDE[value].label }))
const SEVERITY_OPTIONS: SegmentedOption[] = (Object.keys(SEVERITIES) as Severity[]).map(value => ({ value, label: SEVERITIES[value].label }))
const THRESHOLD_OPS = [{ value: 'lt', title: 'is under' }, { value: 'gt', title: 'is over' }]

const threshold = computed<NotifyThreshold>(() => config.value.threshold ?? { column: '', op: 'lt', value: '' })
const pagesSomeone = computed(() => chosenChannels.value.some(id => channels.value.find(channel => channel.id === id)?.type === 'pagerduty'))

/* A variable as it is written in a message. Built, not typed, so no template reads it as its own. */
const braced = (key: string) => `${'{'.repeat(2)}${key}${'}'.repeat(2)}`

/* A variable clicked is added to the end of the message. */
function insertVariable(name: string) {
  const key = name.includes('<column>') ? name.replace('<column>', columns.value.find(column => isNumeric(column.type))?.key ?? 'column') : name
  set({ body: `${(config.value.body ?? '').trimEnd()} {{${key}}}`.trim() })
}

const ids = { title: useId(), newColumn: useId(), limit: useId(), target: useId(), cron: useId(), subject: useId(), body: useId() }
</script>

<template>
  <div class="flex h-full flex-col">
    <header class="flex items-start gap-3 border-b border-line p-4">
      <span
        :style="{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }"
        aria-hidden="true"
        class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) text-base"
      >
        <Icon :name="node.data.kind === 'source' ? SOURCES[config.source ?? 'purchases'].icon : kind.icon" />
      </span>
      <span class="flex min-w-0 flex-1 flex-col gap-0.5">
        <span class="text-xs font-medium text-ink">{{ kind.label }}</span>
        <span class="text-[11px] leading-4 text-ink/55">{{ kind.note }}</span>
      </span>
      <v-menu v-if="!isFrame(data.kind)" location="bottom end">
        <template #activator="{ props: activator }">
          <v-btn
            v-bind="activator"
            aria-label="Node options"
            color="neutral"
            icon="material-symbols:more-horiz"
            size="small"
            variant="text"
          />
        </template>
        <v-list min-width="200" nav>
          <v-list-item
            base-color="error"
            prepend-icon="material-symbols:delete-outline"
            title="Delete node"
            @click="emit('remove')"
          />
        </v-list>
      </v-menu>
    </header>

    <div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
      <!-- Start: when the report runs. -->
      <template v-if="data.kind === 'start'">
        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Runs
          </p>
          <ul class="m-0 flex list-none flex-col gap-1 p-0">
            <li class="flex items-center gap-2 text-xs text-ink/75">
              <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:play-arrow-outline" />
              When you run it
            </li>
            <li v-for="name in drives" :key="name" class="flex items-center gap-2 text-xs text-ink/75">
              <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:schedule-outline" />
              {{ name }}
            </li>
          </ul>
          <p v-if="!drives?.length" class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:schedule-outline" />
            To run it on its own, add a Schedule from the parts: it joins Start's top edge.
          </p>
        </div>
        <p class="m-0 text-[11px] leading-4 text-ink/50">
          Every source follows Start, so a run reads them all. Start is in every report and cannot be deleted.
        </p>
      </template>

      <!-- Finish: what a run waits for. -->
      <template v-else-if="data.kind === 'finish'">
        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Waits for <span class="font-mono font-normal">{{ drives?.length ?? 0 }}</span>
          </p>
          <ul v-if="drives?.length" class="m-0 flex list-none flex-col gap-1 p-0">
            <li v-for="name in drives" :key="name" class="flex items-center gap-2 text-xs text-ink/75">
              <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:subdirectory-arrow-right" />
              {{ name }}
            </li>
          </ul>
          <p v-else class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:bar-chart" />
            Nothing yet. Every chart, table, export and notify leads here on its own.
          </p>
        </div>
        <p class="m-0 text-[11px] leading-4 text-ink/50">
          A run ends here once every output is done. Finish is in every report and cannot be deleted.
        </p>
      </template>

      <div v-else class="flex flex-col gap-1.5">
        <label :for="ids.title" class="text-xs text-ink/60">Name</label>
        <v-text-field
          :id="ids.title"
          :model-value="data.title"
          density="comfortable"
          hide-details
          maxlength="60"
          variant="outlined"
          @update:model-value="emit('update', { title: str($event) })"
        />
      </div>

      <!-- Schedule. -->
      <template v-if="data.kind === 'schedule'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Runs</span>
          <v-select
            :items="PRESET_ITEMS"
            :model-value="preset"
            v-bind="FIELD_MENU"
            aria-label="Runs"
            density="comfortable"
            hide-details
            variant="outlined"
            @update:model-value="choosePreset(str($event))"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <label :for="ids.cron" class="text-xs text-ink/60">Cron <span class="text-ink/40">minute hour day month weekday</span></label>
          <v-text-field
            :id="ids.cron"
            :error="Boolean(cronProblem)"
            :model-value="cronText"
            class="ps-report-mono"
            density="comfortable"
            hide-details
            placeholder="0 9 * * 1"
            spellcheck="false"
            variant="outlined"
            @update:model-value="setCron(str($event))"
          />
          <span :class="cronProblem ? 'text-error' : 'text-ink/55'" class="text-[11px] leading-4">{{ cronProblem ?? describeCron(cronText) }}</span>
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Timezone</span>
          <v-select
            :items="TIMEZONE_ITEMS"
            :model-value="config.timezone ?? 'UTC'"
            v-bind="FIELD_MENU"
            aria-label="Timezone"
            density="comfortable"
            hide-details
            variant="outlined"
            @update:model-value="set({ timezone: str($event) })"
          />
        </div>
        <SwitchRow v-model="paused" label="Paused" note="Nothing it runs is sent until it is resumed." />

        <div class="flex flex-col gap-2 border-t border-line pt-4">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Next runs
          </p>
          <ol v-if="upcoming.length" class="m-0 flex list-none flex-col p-0">
            <li v-for="(at, index) in upcoming" :key="at" class="flex items-center justify-between gap-2 border-t border-line/60 py-1.5 text-xs first:border-t-0">
              <span :class="index ? 'text-ink/65' : 'font-medium text-ink'">{{ writeRun(at, config.timezone ?? 'UTC') }}</span>
              <span class="text-[11px] text-ink/40">{{ relativeWords(at, now.getTime()) }}</span>
            </li>
          </ol>
          <p v-else class="m-0 text-xs text-ink/55">
            {{ config.paused ? 'Paused: nothing runs until it is resumed.' : 'Set a schedule that fires to see when.' }}
          </p>
        </div>

        <div class="flex flex-col gap-2 border-t border-line pt-4">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Runs
          </p>
          <ul v-if="drives?.length" class="m-0 flex list-none flex-col gap-1 p-0">
            <li v-for="name in drives" :key="name" class="flex items-center gap-2 text-xs text-ink/75">
              <Icon aria-hidden="true" class="text-sm text-ink/45" name="material-symbols:subdirectory-arrow-right" />
              {{ name }}
            </li>
          </ul>
          <p v-else class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:schedule-outline" />
            Not running anything. Drag from its bottom edge to the top of Start.
          </p>
        </div>
      </template>

      <!-- Notify. -->
      <template v-else-if="data.kind === 'notify'">
        <div class="flex flex-col gap-2">
          <span class="flex items-center justify-between gap-2">
            <span class="text-xs text-ink/60">Send to</span>
            <NuxtLink class="text-[11px] text-primary no-underline hover:underline" to="/settings/notifications">Manage channels</NuxtLink>
          </span>
          <div class="flex flex-col gap-1.5" role="group" aria-label="Send to">
            <button
              v-for="channel in channels"
              :key="channel.id"
              :aria-pressed="chosenChannels.includes(channel.id)"
              :class="chosenChannels.includes(channel.id) ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'"
              class="flex cursor-pointer items-center gap-2.5 rounded-(--ps-radius-small) border px-2.5 py-2 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              type="button"
              @click="toggleChannel(channel.id)"
            >
              <span
                :class="chosenChannels.includes(channel.id) ? 'border-primary bg-primary text-white' : 'border-ink/25'"
                aria-hidden="true"
                class="flex size-3.5 flex-none items-center justify-center rounded-(--ps-radius-x-small) border-2"
              >
                <Icon v-if="chosenChannels.includes(channel.id)" class="text-[11px]" name="material-symbols:check" />
              </span>
              <ChannelMark :type="channel.type" size="small" />
              <span class="flex min-w-0 flex-1 flex-col">
                <span :class="channel.enabled ? 'text-ink' : 'text-ink/45'" class="truncate text-xs">{{ channel.name }}</span>
                <span class="truncate text-[10px] text-ink/45">{{ targetWords(channel) }}</span>
              </span>
              <span v-if="statusOf(channel) === 'failing' || statusOf(channel) === 'off'" :class="statusOf(channel) === 'failing' ? 'text-error' : 'text-ink/45'" class="flex-none text-[10px]">
                {{ CHANNEL_STATUS[statusOf(channel)].label }}
              </span>
            </button>
          </div>
          <p v-if="!channels.length" class="m-0 text-xs text-ink/55">
            No channels yet. Add one under Settings, Notification channels.
          </p>
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Send</span>
          <ChoiceList
            :model-value="config.when ?? 'always'"
            :options="WHEN_OPTIONS"
            label="Send"
            @update:model-value="set({ when: ($event ?? 'always') as NotifyWhen })"
          />
        </div>

        <div v-if="config.when === 'threshold'" class="flex flex-col gap-2">
          <span class="text-xs text-ink/60">When the first row's</span>
          <ReportColumnPicker
            :columns="columns"
            :model-value="threshold.column"
            :types="NUMERIC_TYPES"
            label="Figure to watch"
            @update:model-value="set({ threshold: { ...threshold, column: $event } })"
          />
          <div class="flex items-center gap-2">
            <v-select
              :items="THRESHOLD_OPS"
              :model-value="threshold.op"
              v-bind="FIELD_MENU"
              aria-label="Crosses"
              class="w-32 flex-none"
              density="comfortable"
              hide-details
              variant="outlined"
              @update:model-value="set({ threshold: { ...threshold, op: $event === 'gt' ? 'gt' : 'lt' } })"
            />
            <v-text-field
              :model-value="threshold.value"
              :prefix="typeOf(threshold.column) === 'money' ? '$' : undefined"
              aria-label="The line"
              class="ps-report-mono min-w-0 flex-1"
              density="comfortable"
              hide-details
              inputmode="decimal"
              placeholder="500"
              variant="outlined"
              @update:model-value="set({ threshold: { ...threshold, value: str($event) } })"
            />
          </div>
        </div>

        <div class="flex flex-col gap-2 border-t border-line pt-4">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Message
          </p>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.subject" class="text-xs text-ink/60">Subject</label>
            <v-text-field
              :id="ids.subject"
              :model-value="config.subject"
              density="comfortable"
              hide-details
              placeholder="{{report}}"
              variant="outlined"
              @update:model-value="set({ subject: str($event) })"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.body" class="text-xs text-ink/60">Message</label>
            <v-textarea
              :id="ids.body"
              :model-value="config.body"
              auto-grow
              hide-details
              placeholder="What happened, and what to do about it"
              rows="3"
              variant="outlined"
              @update:model-value="set({ body: str($event) })"
            />
            <span class="flex flex-wrap gap-1">
              <button
                v-for="variable in MESSAGE_VARIABLES"
                :key="variable.key"
                :title="variable.note"
                class="rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 font-mono text-[10px] leading-5 text-ink/65 hover:bg-ink/8 hover:text-ink"
                type="button"
                @click="insertVariable(variable.key)"
                v-text="braced(variable.key)"
              />
            </span>
          </div>
          <div class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">With it</span>
            <SegmentedControl
              :model-value="config.include ?? 'rows'"
              :options="INCLUDE_OPTIONS"
              label="With it"
              stretch
              @update:model-value="set({ include: $event as NotifyInclude })"
            />
          </div>
          <div v-if="pagesSomeone" class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">Pages as</span>
            <SegmentedControl
              :model-value="config.severity ?? 'warning'"
              :options="SEVERITY_OPTIONS"
              label="Pages as"
              stretch
              @update:model-value="set({ severity: $event as Severity })"
            />
          </div>
        </div>

        <div class="flex flex-col gap-2 border-t border-line pt-4">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            When it runs
          </p>
          <p class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:schedule-outline" />
            {{ timing ?? 'Only when you run it. Join a Schedule to Start to send it on its own.' }}
          </p>
          <v-btn
            :disabled="!chosenChannels.length || Boolean(result?.error)"
            block
            class="text-xs font-medium"
            color="neutral"
            prepend-icon="material-symbols:visibility-outline"
            variant="outlined"
            @click="emit('test')"
          >
            Preview and send a test
          </v-btn>
        </div>
      </template>

      <!-- Source. -->
      <template v-else-if="data.kind === 'source'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Rows from</span>
          <div class="grid grid-cols-2 gap-1.5" role="radiogroup" aria-label="Rows from">
            <button
              v-for="key in SOURCE_KEYS"
              :key="key"
              :aria-checked="config.source === key"
              :class="config.source === key ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'"
              class="flex cursor-pointer items-center gap-2 rounded-(--ps-radius-small) border px-2.5 py-2 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              role="radio"
              type="button"
              @click="chooseSource(key)"
            >
              <Icon :name="SOURCES[key].icon" aria-hidden="true" class="flex-none text-base text-ink/60" />
              <span class="truncate text-xs text-ink">{{ SOURCES[key].label }}</span>
            </button>
          </div>
          <span class="text-[11px] leading-4 text-ink/50">{{ SOURCES[config.source ?? 'purchases'].note }}</span>
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Over</span>
          <SegmentedControl
            :model-value="config.range ?? '30'"
            :options="RANGE_OPTIONS"
            label="Over"
            stretch
            @update:model-value="set({ range: $event })"
          />
        </div>
        <div v-if="config.source === 'events'" class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Which events</span>
          <v-select
            :items="EVENT_ITEMS"
            :model-value="config.event ?? ''"
            v-bind="FIELD_MENU"
            aria-label="Which events"
            class="font-mono"
            density="comfortable"
            hide-details
            variant="outlined"
            @update:model-value="set({ event: str($event) })"
          />
        </div>
      </template>

      <!-- Filter: conditions, the way a flag rule's are written. -->
      <template v-else-if="data.kind === 'filter'">
        <div class="flex flex-col gap-3">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Keep rows where
          </p>

          <div v-if="conditions.length > 1" class="flex items-center gap-2">
            <v-select
              :items="MATCH_ITEMS"
              :model-value="config.match ?? 'all'"
              v-bind="FIELD_MENU"
              aria-label="How the conditions combine"
              class="w-24 flex-none"
              density="comfortable"
              hide-details
              variant="outlined"
              @update:model-value="set({ match: $event === 'any' ? 'any' : 'all' })"
            />
            <span class="text-[13px] text-ink/55">of these are true</span>
          </div>

          <p v-if="!conditions.length" class="m-0 flex items-center gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/70">
            <Icon aria-hidden="true" class="text-base text-ink/45" name="material-symbols:filter-alt-outline" />
            Every row. Add a condition to narrow it.
          </p>

          <ol class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(condition, index) in conditions" :key="index" class="flex flex-col gap-2">
              <span
                v-if="index > 0"
                class="w-fit rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 text-[10px] leading-5 font-medium tracking-wide text-ink/55 uppercase"
              >{{ joiner }}</span>
              <div class="flex items-center gap-1">
                <ReportColumnPicker
                  :columns="columns"
                  :label="`Condition ${index + 1} column`"
                  :model-value="condition.column"
                  class="min-w-0 flex-1"
                  @update:model-value="setCondition(index, { column: $event })"
                />
                <v-btn
                  :aria-label="`Remove condition ${index + 1}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="small"
                  variant="text"
                  @click="set({ conditions: without(conditions, index) })"
                />
              </div>
              <div v-if="condition.column" class="flex items-center gap-2 pr-10">
                <v-select
                  :items="opsFor(condition.column)"
                  :model-value="condition.op"
                  v-bind="FIELD_MENU"
                  :aria-label="`Condition ${index + 1} comparison`"
                  class="w-32 flex-none"
                  density="comfortable"
                  hide-details
                  variant="outlined"
                  @update:model-value="setCondition(index, { op: str($event) as FilterOp })"
                />
                <v-combobox
                  v-if="!isNumeric(typeOf(condition.column)) && typeOf(condition.column) !== 'time'"
                  :items="valuesOf(condition.column)"
                  :model-value="condition.value"
                  v-bind="FIELD_MENU"
                  :aria-label="`Condition ${index + 1} value`"
                  :placeholder="valuePlaceholder(condition)"
                  class="min-w-0 flex-1"
                  density="comfortable"
                  hide-details
                  variant="outlined"
                  @update:model-value="setCondition(index, { value: str($event) })"
                />
                <v-text-field
                  v-else
                  :aria-label="`Condition ${index + 1} value`"
                  :model-value="condition.value"
                  :placeholder="valuePlaceholder(condition)"
                  :prefix="typeOf(condition.column) === 'money' && condition.op !== 'within' ? '$' : undefined"
                  :suffix="condition.op === 'within' ? 'days' : undefined"
                  class="min-w-0 flex-1"
                  density="comfortable"
                  hide-details
                  inputmode="decimal"
                  variant="outlined"
                  @update:model-value="setCondition(index, { value: str($event) })"
                />
              </div>
            </li>
          </ol>

          <div>
            <v-btn
              :disabled="!columns.length"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="text"
              @click="addCondition"
            >
              Condition
            </v-btn>
          </div>
        </div>
      </template>

      <!-- Join. -->
      <template v-else-if="data.kind === 'join'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Keep</span>
          <SegmentedControl
            :model-value="config.mode ?? 'inner'"
            :options="JOIN_OPTIONS"
            label="Keep"
            stretch
            @update:model-value="set({ mode: $event === 'left' ? 'left' : 'inner' })"
          />
        </div>
        <p class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:join-inner" />
          <span>
            {{ config.mode === 'left' ? 'Every row from the left input, with the right one\'s columns where the player matches, and empty where not.' : 'Only rows whose player is in both inputs.' }}
            Matched on Player; a column both sides have is kept from the left.
          </span>
        </p>
      </template>

      <!-- Group. -->
      <template v-else-if="data.kind === 'group'">
        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Group by
          </p>
          <p v-if="!by.length" class="m-0 flex items-center gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs text-ink/70">
            <Icon aria-hidden="true" class="text-base text-ink/45" name="material-symbols:functions" />
            Nothing: one row for everything.
          </p>
          <ol class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(key, index) in by" :key="index" class="flex items-center gap-1">
              <ReportColumnPicker
                :columns="columns"
                :label="`Group by ${index + 1}`"
                :model-value="key"
                class="min-w-0 flex-1"
                @update:model-value="set({ by: replaced(by, index, $event) })"
              />
              <v-btn
                :aria-label="`Stop grouping by ${key}`"
                color="neutral"
                icon="material-symbols:close"
                size="small"
                variant="text"
                @click="set({ by: without(by, index) })"
              />
            </li>
          </ol>
          <div v-if="groupsByTime" class="flex flex-col gap-1.5">
            <span class="text-xs text-ink/60">Times by the</span>
            <SegmentedControl
              :model-value="config.bucket ?? 'day'"
              :options="BUCKET_OPTIONS"
              label="Times by the"
              stretch
              @update:model-value="set({ bucket: $event === 'week' ? 'week' : $event === 'month' ? 'month' : 'day' })"
            />
          </div>
          <div>
            <v-btn
              :disabled="!columns.length"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="text"
              @click="set({ by: [...by, ''] })"
            >
              Column
            </v-btn>
          </div>
        </div>

        <div class="flex flex-col gap-2 border-t border-line pt-4">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Work out
          </p>
          <ol class="m-0 flex list-none flex-col gap-3 p-0">
            <li v-for="(metric, index) in metrics" :key="index" class="flex flex-col gap-2">
              <div class="flex items-center gap-2">
                <v-select
                  :items="METRIC_ITEMS"
                  :model-value="metric.fn"
                  v-bind="FIELD_MENU"
                  :aria-label="`Figure ${index + 1}`"
                  :class="METRIC_FNS[metric.fn].needsColumn ? 'w-36 flex-none' : 'min-w-0 flex-1'"
                  density="comfortable"
                  hide-details
                  variant="outlined"
                  @update:model-value="setMetricFn(index, str($event) as MetricFn)"
                />
                <ReportColumnPicker
                  v-if="METRIC_FNS[metric.fn].needsColumn"
                  :columns="columns"
                  :label="`Figure ${index + 1} of`"
                  :model-value="metric.column"
                  :types="METRIC_FNS[metric.fn].numeric ? NUMERIC_TYPES : undefined"
                  class="min-w-0 flex-1"
                  placeholder="Of"
                  @update:model-value="setMetricColumn(index, $event)"
                />
                <v-btn
                  :aria-label="`Remove figure ${index + 1}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="small"
                  variant="text"
                  @click="set({ metrics: without(metrics, index) })"
                />
              </div>
              <v-text-field
                :aria-label="`Figure ${index + 1} is called`"
                :model-value="metric.as"
                class="ps-report-mono mr-10"
                density="comfortable"
                hide-details
                placeholder="Its column's name"
                prefix="as"
                variant="outlined"
                @update:model-value="setMetric(index, { as: str($event) })"
              />
            </li>
          </ol>
          <div>
            <v-btn
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="text"
              @click="addMetric"
            >
              Figure
            </v-btn>
          </div>
        </div>
      </template>

      <!-- Formula. -->
      <template v-else-if="data.kind === 'formula'">
        <div class="flex flex-col gap-1.5">
          <label :for="ids.newColumn" class="text-xs text-ink/60">New column</label>
          <v-text-field
            :id="ids.newColumn"
            :model-value="config.name"
            class="ps-report-mono"
            density="comfortable"
            hide-details
            placeholder="per_player"
            variant="outlined"
            @update:model-value="set({ name: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-2">
          <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
            Is
          </p>
          <ReportColumnPicker
            :columns="columns"
            :model-value="config.left ?? ''"
            :types="NUMERIC_TYPES"
            label="Starts from"
            @update:model-value="set({ left: $event })"
          />
          <SegmentedControl
            :model-value="config.op ?? '/'"
            :options="OP_OPTIONS"
            label="Operation"
            stretch
            @update:model-value="set({ op: $event as NodeConfig['op'] })"
          />
          <SegmentedControl
            :model-value="rightMode"
            :options="RIGHT_OPTIONS"
            label="Worked out with"
            stretch
            @update:model-value="setRightMode"
          />
          <ReportColumnPicker
            v-if="rightMode === 'column'"
            :columns="columns"
            :model-value="config.right ?? ''"
            :types="NUMERIC_TYPES"
            label="Worked out with"
            @update:model-value="set({ right: $event })"
          />
          <v-text-field
            v-else
            :model-value="config.right"
            aria-label="Worked out with"
            class="ps-report-mono"
            density="comfortable"
            hide-details
            inputmode="decimal"
            placeholder="100"
            variant="outlined"
            @update:model-value="set({ right: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Shown as</span>
          <SegmentedControl
            :model-value="config.format ?? 'number'"
            :options="FORMAT_OPTIONS"
            label="Shown as"
            stretch
            @update:model-value="set({ format: $event as NodeConfig['format'] })"
          />
        </div>
      </template>

      <!-- Sort. -->
      <template v-else-if="data.kind === 'sort'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Order by</span>
          <ReportColumnPicker
            :columns="columns"
            :model-value="config.column ?? ''"
            label="Order by"
            @update:model-value="set({ column: $event })"
          />
          <SegmentedControl
            :model-value="config.order ?? 'desc'"
            :options="ORDER_OPTIONS"
            label="Order"
            stretch
            @update:model-value="set({ order: $event === 'asc' ? 'asc' : 'desc' })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <label :for="ids.limit" class="text-xs text-ink/60">Keep the first</label>
          <v-text-field
            :id="ids.limit"
            v-model="limitText"
            class="ps-report-mono"
            density="comfortable"
            hide-details
            inputmode="numeric"
            placeholder="Every row"
            suffix="rows"
            variant="outlined"
          />
        </div>
      </template>

      <!-- Chart. -->
      <template v-else-if="data.kind === 'chart'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Drawn as</span>
          <SegmentedControl
            :model-value="config.chart ?? 'line'"
            :options="CHART_OPTIONS"
            label="Drawn as"
            stretch
            @update:model-value="set({ chart: $event as NodeConfig['chart'] })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Along the bottom</span>
          <ReportColumnPicker
            :columns="columns"
            :model-value="config.x ?? ''"
            label="Along the bottom"
            @update:model-value="set({ x: $event })"
          />
        </div>
        <div class="flex flex-col gap-2">
          <span class="text-xs text-ink/60">Figures</span>
          <ol class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(key, index) in figures" :key="index" class="flex items-center gap-1">
              <ReportColumnPicker
                :columns="columns"
                :label="`Figure ${index + 1}`"
                :model-value="key"
                :types="NUMERIC_TYPES"
                class="min-w-0 flex-1"
                @update:model-value="set({ y: replaced(figures, index, $event) })"
              />
              <v-btn
                :aria-label="`Remove figure ${index + 1}`"
                color="neutral"
                icon="material-symbols:close"
                size="small"
                variant="text"
                @click="set({ y: without(figures, index) })"
              />
            </li>
          </ol>
          <div>
            <v-btn
              :disabled="!columns.some(column => isNumeric(column.type))"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="text"
              @click="set({ y: [...figures, ''] })"
            >
              Figure
            </v-btn>
          </div>
        </div>
      </template>

      <!-- Table. -->
      <p v-else-if="data.kind === 'table'" class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
        <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:table-outline" />
        Every column of its input, as a table. Put a Sort before it to choose the order and how many rows it keeps.
      </p>

      <!-- Export. -->
      <template v-else-if="data.kind === 'export'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">File</span>
          <SegmentedControl
            :model-value="config.file ?? 'json'"
            :options="FILE_OPTIONS"
            label="File"
            stretch
            @update:model-value="set({ file: $event as NodeConfig['file'] })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">Sent to</span>
          <ChoiceList
            :model-value="config.destination ?? 'download'"
            :options="DESTINATION_OPTIONS"
            label="Sent to"
            @update:model-value="set({ destination: ($event ?? 'download') as NodeConfig['destination'], target: DESTINATIONS[($event ?? 'download') as keyof typeof DESTINATIONS].target })"
          />
        </div>
        <div v-if="config.destination && config.destination !== 'download'" class="flex flex-col gap-1.5">
          <label :for="ids.target" class="text-xs text-ink/60">{{ config.destination === 'email' ? 'To' : config.destination === 'bigquery' ? 'Table' : 'Path' }}</label>
          <v-text-field
            :id="ids.target"
            :model-value="config.target"
            :placeholder="DESTINATIONS[config.destination].target"
            class="ps-report-mono"
            density="comfortable"
            hide-details
            variant="outlined"
            @update:model-value="set({ target: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs text-ink/60">When</span>
          <p class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-xs leading-5 text-ink/70">
            <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:schedule-outline" />
            {{ timing ?? 'Only when you run it. Join a Schedule to Start to write it on its own.' }}
          </p>
        </div>
        <div class="flex flex-col gap-1.5">
          <v-btn
            :disabled="!result || Boolean(result.error) || !writable"
            block
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:play-arrow-outline"
            variant="flat"
            @click="emit('test')"
          >
            Write it now{{ result && !result.error ? `, ${rowsOf(result).toLocaleString('en')} rows` : '' }}
          </v-btn>
          <span class="text-[11px] leading-4 text-ink/50">{{ writable ? `A test run writes the whole result as ${file.toUpperCase()}. Every run's file is kept for seven days, to download from the report's runs.` : 'Only JSON or CSV to a download is written for now. Choose those to write it.' }}</span>
        </div>
      </template>

      <!-- What comes out. -->
      <div v-if="result && !result.error && result.columns.length" class="flex flex-col gap-2 border-t border-line pt-4">
        <p class="m-0 text-[11px] font-semibold tracking-wide text-ink/45 uppercase">
          Columns out <span class="font-mono font-normal">{{ result.columns.length }}</span>
        </p>
        <ul class="m-0 flex list-none flex-wrap gap-1 p-0">
          <li v-for="column in result.columns" :key="column.key" class="inline-flex items-center gap-1 rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 py-0.5 font-mono text-[10px] text-ink/70">
            <Icon :name="COLUMN_TYPE_ICONS[column.type]" aria-hidden="true" class="text-[11px] text-ink/45" />
            {{ column.key }}
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Keys and figures are typed in the mono face, the way a flag's key is. */
.ps-report-mono :deep(input) {
  font-family: var(--font-mono);
}
</style>
