<script setup lang="ts">
// The selected node's settings, beside the canvas. What can be chosen comes from what flows
// into the node: a filter offers the columns its input has, a sum only the numeric ones, so
// a setting that cannot run is not on offer. Every change applies at once, and the preview
// under the canvas shows what came of it.
const props = defineProps<{
  node: GraphNode
  /** What flows in: the columns and rows of the node before it. Empty for a source. */
  input: NodeResult | null
  result: NodeResult | null
}>()

const emit = defineEmits<{
  update: [patch: Partial<ReportNodeData>]
  remove: []
}>()

const data = computed(() => props.node.data)
const config = computed(() => data.value.config)
const kind = computed(() => KINDS[data.value.kind])
const tone = computed(() => toneColour(familyTone(kind.value.family)))

function set(patch: NodeConfig) {
  emit('update', { config: { ...config.value, ...patch } })
}

const columns = computed(() => props.input?.columns ?? [])
const columnItems = (filter?: (column: ReportColumn) => boolean) => columns.value.filter(filter ?? (() => true)).map(column => ({ value: column.key, title: column.label }))
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

// --- Source -------------------------------------------------------------------------------

const RANGE_OPTIONS: SegmentedOption[] = RANGES.map(entry => ({ value: entry.value, label: entry.label.replace('Last ', '') }))
const EVENT_ITEMS = [{ value: '', title: 'Every event' }, ...EVENT_NAMES.map(name => ({ value: name, title: name }))]

function chooseSource(source: SourceKey) {
  const renamed = data.value.title === SOURCES[config.value.source ?? 'purchases'].label
  emit('update', { config: { ...config.value, source, event: '' }, ...(renamed ? { title: SOURCES[source].label } : {}) })
}

// --- Filter -------------------------------------------------------------------------------

const MATCH_OPTIONS: SegmentedOption[] = [
  { value: 'all', label: 'All of these' },
  { value: 'any', label: 'Any of these' }
]
const conditions = computed(() => config.value.conditions ?? [])
const opsFor = (column: string) => (Object.entries(FILTER_OPS) as [FilterOp, typeof FILTER_OPS[FilterOp]][])
  .filter(([, info]) => info.types === 'any' || info.types.includes(typeOf(column)))
  .map(([value, info]) => ({ value, title: info.label }))

function setCondition(index: number, patch: Partial<FilterCondition>) {
  const next = conditions.value.map((condition, position) => {
    if (position !== index) return condition
    const merged = { ...condition, ...patch }
    /* A new column may not take the old comparison: fall back to "is". */
    if (patch.column && !opsFor(patch.column).some(op => op.value === merged.op)) merged.op = typeOf(patch.column) === 'time' ? 'within' : 'is'
    return merged
  })
  set({ conditions: next })
}

function addCondition() {
  set({ conditions: [...conditions.value, { column: columns.value[0]?.key ?? '', op: 'is', value: '' }] })
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
const metrics = computed(() => config.value.metrics ?? [])
const groupsByTime = computed(() => (config.value.by ?? []).some(key => typeOf(key) === 'time'))
const METRIC_ITEMS = (Object.entries(METRIC_FNS) as [MetricFn, typeof METRIC_FNS[MetricFn]][]).map(([value, info]) => ({ value, title: info.label }))

function setMetric(index: number, patch: Partial<GroupMetric>) {
  set({ metrics: metrics.value.map((metric, position) => (position === index ? { ...metric, ...patch } : metric)) })
}

function addMetric() {
  const numeric = columns.value.find(column => isNumeric(column.type))
  set({ metrics: [...metrics.value, numeric ? { fn: 'sum', column: numeric.key, as: numeric.key } : { fn: 'count', column: '', as: `rows_${metrics.value.length + 1}` }] })
}

// --- Formula, sort, chart, export ----------------------------------------------------------

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
const ORDER_OPTIONS: SegmentedOption[] = [
  { value: 'desc', label: 'Highest first' },
  { value: 'asc', label: 'Lowest first' }
]
const CHART_OPTIONS: SegmentedOption[] = [
  { value: 'line', label: 'Line' },
  { value: 'bar', label: 'Bars' },
  { value: 'area', label: 'Area' }
]
const FILE_OPTIONS: SegmentedOption[] = [
  { value: 'csv', label: 'CSV' },
  { value: 'parquet', label: 'Parquet' },
  { value: 'json', label: 'JSON' }
]
const DESTINATION_OPTIONS = computed<ChoiceOption[]>(() => (Object.entries(DESTINATIONS) as [keyof typeof DESTINATIONS, typeof DESTINATIONS[keyof typeof DESTINATIONS]][]).map(([value, info]) => ({ value, label: info.label, note: info.note })))
const SCHEDULE_ITEMS = (Object.entries(SCHEDULES) as [string, string][]).map(([value, title]) => ({ value, title }))

const limitText = computed({
  get: () => (config.value.limit ? String(config.value.limit) : ''),
  set: (value: string) => {
    const number = Math.floor(Number(value))
    set({ limit: value.trim() && Number.isFinite(number) && number > 0 ? number : null })
  }
})

/* The result as a file, now: what a download destination does. */
const downloaded = ref(false)
function downloadNow() {
  const result = props.result
  if (!result || result.error) return
  const header = result.columns.map(column => column.key)
  const rows = result.rows.map(row => result.columns.map(column => row[column.key] ?? null))
  downloadCsv(fileSlug(data.value.title || 'report'), [header, ...rows])
  downloaded.value = true
}

// Every choice reads a string from its control and writes the typed value back.
const str = (value: unknown) => (typeof value === 'string' ? value : '')

const ids = { title: useId() }
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
        <span class="text-[11px] text-ink/50">{{ kind.label }}</span>
        <span class="text-[11px] leading-4 text-ink/60">{{ kind.note }}</span>
      </span>
      <v-btn
        aria-label="Delete this node"
        color="neutral"
        icon="material-symbols:delete-outline"
        size="small"
        variant="text"
        @click="emit('remove')"
      />
    </header>

    <div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
      <div class="flex flex-col gap-1.5">
        <label :for="ids.title" class="text-xs font-medium text-ink">Name</label>
        <v-text-field
          :id="ids.title"
          :model-value="data.title"
          density="compact"
          hide-details
          maxlength="60"
          variant="outlined"
          @update:model-value="emit('update', { title: str($event) })"
        />
      </div>

      <!-- Source. -->
      <template v-if="data.kind === 'source'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Rows from</span>
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
          <span class="text-[11px] text-ink/50">{{ SOURCES[config.source ?? 'purchases'].note }}</span>
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Over</span>
          <SegmentedControl
            :model-value="config.range ?? '30'"
            :options="RANGE_OPTIONS"
            label="Over"
            stretch
            @update:model-value="set({ range: $event })"
          />
        </div>
        <div v-if="config.source === 'events'" class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Which events</span>
          <v-select
            :items="EVENT_ITEMS"
            :model-value="config.event ?? ''"
            aria-label="Which events"
            density="compact"
            hide-details
            variant="outlined"
            @update:model-value="set({ event: str($event) })"
          />
        </div>
      </template>

      <!-- Filter. -->
      <template v-else-if="data.kind === 'filter'">
        <div class="flex flex-col gap-2">
          <span class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium text-ink">Keep rows matching</span>
            <SegmentedControl
              :model-value="config.match ?? 'all'"
              :options="MATCH_OPTIONS"
              label="Match"
              @update:model-value="set({ match: $event === 'any' ? 'any' : 'all' })"
            />
          </span>
          <ul class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(condition, index) in conditions" :key="index" class="flex flex-col gap-1.5 rounded-(--ps-radius) border border-line p-2.5">
              <div class="flex items-center gap-1.5">
                <v-select
                  :items="columnItems()"
                  :model-value="condition.column"
                  aria-label="Column"
                  class="min-w-0 flex-1"
                  density="compact"
                  hide-details
                  variant="outlined"
                  @update:model-value="setCondition(index, { column: str($event) })"
                />
                <v-btn
                  :aria-label="`Remove condition ${index + 1}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="x-small"
                  variant="text"
                  @click="set({ conditions: conditions.filter((_, position) => position !== index) })"
                />
              </div>
              <div class="grid grid-cols-[7.5rem_minmax(0,1fr)] gap-1.5">
                <v-select
                  :items="opsFor(condition.column)"
                  :model-value="condition.op"
                  aria-label="Comparison"
                  density="compact"
                  hide-details
                  variant="outlined"
                  @update:model-value="setCondition(index, { op: str($event) as FilterOp })"
                />
                <v-combobox
                  v-if="!isNumeric(typeOf(condition.column)) && typeOf(condition.column) !== 'time'"
                  :items="valuesOf(condition.column)"
                  :model-value="condition.value"
                  aria-label="Value"
                  density="compact"
                  hide-details
                  variant="outlined"
                  @update:model-value="setCondition(index, { value: str($event) })"
                />
                <v-text-field
                  v-else
                  :model-value="condition.value"
                  :suffix="condition.op === 'within' ? 'days' : undefined"
                  aria-label="Value"
                  density="compact"
                  hide-details
                  inputmode="decimal"
                  variant="outlined"
                  @update:model-value="setCondition(index, { value: str($event) })"
                />
              </div>
            </li>
          </ul>
          <p v-if="!conditions.length" class="m-0 text-[11px] text-ink/50">
            No conditions yet: every row passes.
          </p>
          <div>
            <v-btn
              :disabled="!columns.length"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="outlined"
              @click="addCondition"
            >
              Add condition
            </v-btn>
          </div>
        </div>
      </template>

      <!-- Join. -->
      <template v-else-if="data.kind === 'join'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Keep</span>
          <SegmentedControl
            :model-value="config.mode ?? 'inner'"
            :options="JOIN_OPTIONS"
            label="Keep"
            stretch
            @update:model-value="set({ mode: $event === 'left' ? 'left' : 'inner' })"
          />
          <span class="text-[11px] leading-4 text-ink/55">
            {{ config.mode === 'left' ? 'Every row from the left input, with the right one\'s columns where the player matches, and empty where not.' : 'Only rows whose player is in both inputs.' }}
            Rows are matched on Player; a column both sides have is kept from the left.
          </span>
        </div>
      </template>

      <!-- Group. -->
      <template v-else-if="data.kind === 'group'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Group by</span>
          <v-select
            :items="columnItems()"
            :model-value="config.by ?? []"
            aria-label="Group by"
            chips
            closable-chips
            density="compact"
            hide-details
            multiple
            placeholder="Nothing: one row for everything"
            variant="outlined"
            @update:model-value="set({ by: Array.isArray($event) ? $event.map(String) : [] })"
          />
        </div>
        <div v-if="groupsByTime" class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Times by the</span>
          <SegmentedControl
            :model-value="config.bucket ?? 'day'"
            :options="BUCKET_OPTIONS"
            label="Times by the"
            stretch
            @update:model-value="set({ bucket: $event === 'week' ? 'week' : $event === 'month' ? 'month' : 'day' })"
          />
        </div>
        <div class="flex flex-col gap-2">
          <span class="text-xs font-medium text-ink">Figures</span>
          <ul class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(metric, index) in metrics" :key="index" class="flex flex-col gap-1.5 rounded-(--ps-radius) border border-line p-2.5">
              <div class="flex items-center gap-1.5">
                <v-select
                  :items="METRIC_ITEMS"
                  :model-value="metric.fn"
                  aria-label="Work out"
                  class="min-w-0 flex-1"
                  density="compact"
                  hide-details
                  variant="outlined"
                  @update:model-value="setMetric(index, { fn: str($event) as MetricFn })"
                />
                <v-btn
                  :aria-label="`Remove figure ${index + 1}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="x-small"
                  variant="text"
                  @click="set({ metrics: metrics.filter((_, position) => position !== index) })"
                />
              </div>
              <div class="grid grid-cols-2 gap-1.5">
                <v-select
                  v-if="METRIC_FNS[metric.fn].needsColumn"
                  :items="columnItems(column => !METRIC_FNS[metric.fn].numeric || isNumeric(column.type))"
                  :model-value="metric.column"
                  aria-label="Of"
                  density="compact"
                  hide-details
                  placeholder="Of"
                  variant="outlined"
                  @update:model-value="setMetric(index, { column: str($event) })"
                />
                <span v-else class="flex items-center px-1 text-[11px] text-ink/50">Every row</span>
                <v-text-field
                  :model-value="metric.as"
                  aria-label="Called"
                  class="font-mono"
                  density="compact"
                  hide-details
                  placeholder="Called"
                  prefix="as"
                  variant="outlined"
                  @update:model-value="setMetric(index, { as: str($event) })"
                />
              </div>
            </li>
          </ul>
          <div>
            <v-btn
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="outlined"
              @click="addMetric"
            >
              Add figure
            </v-btn>
          </div>
        </div>
      </template>

      <!-- Formula. -->
      <template v-else-if="data.kind === 'formula'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">New column</span>
          <v-text-field
            :model-value="config.name"
            aria-label="New column"
            class="font-mono"
            density="compact"
            hide-details
            placeholder="per_player"
            variant="outlined"
            @update:model-value="set({ name: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Is</span>
          <v-select
            :items="columnItems(column => isNumeric(column.type))"
            :model-value="config.left"
            aria-label="Starts from"
            density="compact"
            hide-details
            placeholder="A column"
            variant="outlined"
            @update:model-value="set({ left: str($event) })"
          />
          <SegmentedControl
            :model-value="config.op ?? '/'"
            :options="OP_OPTIONS"
            label="Operation"
            stretch
            @update:model-value="set({ op: $event as NodeConfig['op'] })"
          />
          <v-combobox
            :items="columnItems(column => isNumeric(column.type))"
            :model-value="config.right"
            :return-object="false"
            aria-label="Worked out with"
            density="compact"
            hide-details
            placeholder="A column, or a number"
            variant="outlined"
            @update:model-value="set({ right: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Shown as</span>
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
          <span class="text-xs font-medium text-ink">Order by</span>
          <v-select
            :items="columnItems()"
            :model-value="config.column"
            aria-label="Order by"
            density="compact"
            hide-details
            placeholder="A column"
            variant="outlined"
            @update:model-value="set({ column: str($event) })"
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
          <span class="text-xs font-medium text-ink">Keep the first</span>
          <v-text-field
            v-model="limitText"
            aria-label="Keep the first"
            density="compact"
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
          <span class="text-xs font-medium text-ink">Drawn as</span>
          <SegmentedControl
            :model-value="config.chart ?? 'line'"
            :options="CHART_OPTIONS"
            label="Drawn as"
            stretch
            @update:model-value="set({ chart: $event as NodeConfig['chart'] })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Along the bottom</span>
          <v-select
            :items="columnItems()"
            :model-value="config.x"
            aria-label="Along the bottom"
            density="compact"
            hide-details
            placeholder="A column"
            variant="outlined"
            @update:model-value="set({ x: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Figures</span>
          <v-select
            :items="columnItems(column => isNumeric(column.type))"
            :model-value="config.y ?? []"
            aria-label="Figures"
            chips
            closable-chips
            density="compact"
            hide-details
            multiple
            placeholder="One or more"
            variant="outlined"
            @update:model-value="set({ y: Array.isArray($event) ? $event.map(String) : [] })"
          />
        </div>
      </template>

      <!-- Table. -->
      <p v-else-if="data.kind === 'table'" class="m-0 text-xs leading-5 text-ink/60">
        Every column of its input, as a table. Put a Sort before it to choose the order and how many rows it keeps.
      </p>

      <!-- Export. -->
      <template v-else-if="data.kind === 'export'">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">File</span>
          <SegmentedControl
            :model-value="config.file ?? 'csv'"
            :options="FILE_OPTIONS"
            label="File"
            stretch
            @update:model-value="set({ file: $event as NodeConfig['file'] })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">Sent to</span>
          <ChoiceList
            :model-value="config.destination ?? 'download'"
            :options="DESTINATION_OPTIONS"
            label="Sent to"
            @update:model-value="set({ destination: ($event ?? 'download') as NodeConfig['destination'], target: DESTINATIONS[($event ?? 'download') as keyof typeof DESTINATIONS].target })"
          />
        </div>
        <div v-if="config.destination && config.destination !== 'download'" class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">{{ config.destination === 'email' ? 'To' : config.destination === 'bigquery' ? 'Table' : 'Path' }}</span>
          <v-text-field
            :model-value="config.target"
            :placeholder="DESTINATIONS[config.destination].target"
            aria-label="Where"
            class="font-mono"
            density="compact"
            hide-details
            variant="outlined"
            @update:model-value="set({ target: str($event) })"
          />
        </div>
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-ink">When</span>
          <v-select
            :items="SCHEDULE_ITEMS"
            :model-value="config.schedule ?? 'once'"
            aria-label="When"
            density="compact"
            hide-details
            variant="outlined"
            @update:model-value="set({ schedule: str($event) as NodeConfig['schedule'] })"
          />
        </div>
        <div v-if="(config.destination ?? 'download') === 'download'" class="flex flex-col gap-1.5">
          <v-btn
            :disabled="!result || Boolean(result.error)"
            block
            class="text-xs font-medium"
            color="primary"
            prepend-icon="material-symbols:download"
            variant="flat"
            @click="downloadNow"
          >
            Download {{ result && !result.error ? `${result.rows.length.toLocaleString('en')} rows` : '' }}
          </v-btn>
          <span class="text-[11px] text-ink/50">{{ downloaded ? 'Downloaded as CSV from the sample rows.' : 'Downloads as CSV from the sample rows here; Parquet and JSON once reports run on the server.' }}</span>
        </div>
      </template>

      <!-- What comes out. -->
      <div v-if="result && !result.error && result.columns.length" class="flex flex-col gap-1.5 border-t border-line pt-4">
        <span class="text-xs font-medium text-ink">Columns out <span class="font-normal text-ink/45">{{ result.columns.length }}</span></span>
        <ul class="m-0 flex list-none flex-wrap gap-1 p-0">
          <li v-for="column in result.columns" :key="column.key" class="rounded-full bg-ink/5 px-2 py-0.5 font-mono text-[10px] text-ink/70">
            {{ column.key }}
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
