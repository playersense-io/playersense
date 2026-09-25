<script setup lang="ts">
import type { Schema } from '@playersense/contracts'
import type { Kpi } from '~/components/KpiBoard.vue'
import type { LogBand, LogRecord, LogRecordSummary } from '~/composables/logs'

// Cloud Logging's explorer: the game's own log lines, exceptions and crashes, for the environment
// the header has chosen (…/logs/{environment}). A histogram to find the moment, a search to narrow
// it, the records newest first, and one record whole beside them. The same records grouped into
// bugs are the Issues page beside this one.
//
// Everything a view is (the range, the level bands, the search) is in the URL, so a view can be
// sent to somebody and opens the same for them.
const workspace = useWorkspaceStore()
const path = useLogsPath()

const { range, setPreset, setAbsolute, zoomOut, shift, refresh } = useLogRange('24h')
const colors = useLogColors()

// --- The search -------------------------------------------------------------------------------

const route = useRoute()
const patch = useQueryPatch()

const bands = useQueryList('levels', LOG_BANDS.map(band => band.key)) as unknown as Ref<LogBand[]>
const text = computed(() => (typeof route.query.q === 'string' ? route.query.q : ''))
const draft = ref(text.value)
watch(text, (value) => {
  if (value !== draft.value) draft.value = value
})
// Applied as the person pauses, not per keystroke: a search reads ClickHouse.
watchDebounced(draft, value => patch({ q: value.trim() || undefined }), { debounce: 450 })

const query = computed(() => effectiveQuery(bands.value, text.value))

function toggleBand(band: LogBand) {
  bands.value = bands.value.includes(band) ? bands.value.filter(key => key !== band) : [...bands.value, band]
}

/** Adds a clause to the search, once. */
function addFilter(clauseText: string) {
  const current = draft.value.trim()
  if (current.split(/\s+/).includes(clauseText)) return
  draft.value = current ? `${current} ${clauseText}` : clauseText
  patch({ q: draft.value })
}

const SEARCH_HINT = 'Search: timeout "failed to connect" level:error release:1.4.2 -heartbeat'

const SYNTAX = [
  { example: 'timeout', means: 'A whole word, in the message or the exception. TimeoutException counts.' },
  { example: '"failed to connect"', means: 'This phrase, as written' },
  { example: '-heartbeat', means: 'Leave these out' },
  { example: 'level:error,fatal', means: 'Only these levels' },
  { example: 'is:crash', means: 'Crashes only; also is:exception' },
  { example: 'logger:Net', means: 'Where it was logged from' },
  { example: 'release:1.4.2', means: 'App version; also platform:, device:, os:, country:' },
  { example: 'attr.scene:Shop', means: 'An attribute the game sent' },
  { example: 'install:<id>', means: 'One install; also player:, session:, issue:' }
]

// --- Reads ------------------------------------------------------------------------------------

const span = computed(() => ({ from: new Date(range.value.from).toISOString(), to: new Date(range.value.to).toISOString() }))

// Every level, whatever the chips say: the chips count each level from it, and the chart sums the
// chosen ones itself, so a chip is a redraw rather than a query.
const histogramRead = useApiGet<Schema<'LogHistogramOut'>>(() => path.value
  && withQuery(`${path.value}/histogram`, { ...span.value, q: text.value }))
const chosenLevels = computed(() => LOG_BANDS.filter(band => bands.value.includes(band.key)).flatMap(band => band.levels))
const summaryRead = useApiGet<Schema<'LogSummaryOut'>>(() => path.value
  && withQuery(`${path.value}/summary`, span.value))

const histogram = computed(() => histogramRead.data.value)
const buckets = computed(() => histogram.value?.buckets ?? [])
const searchError = computed(() => histogramRead.error.value as ApiError | null)

const log = useLogRecords(path, range, query)

// --- Figures ----------------------------------------------------------------------------------

const board = computed<Kpi[]>(() => {
  const s = summaryRead.data.value
  const known = s !== null && s !== undefined
  const current = s?.current
  const previous = s?.previous
  const figure = (value: number | undefined) => (known && value !== undefined ? writeFigure(value, { compact: true }) : null)
  const move = (value: number, before: number) => (before > 0 ? trendOf(value, before, 'down') : undefined)
  return [
    { key: 'records', label: 'Records', value: figure(current?.records), exact: current ? writeFigure(current.records) : undefined, trend: current && previous && previous.records ? trendOf(current.records, previous.records) : undefined, info: 'Every log line, exception and crash in the range.' },
    { key: 'errors', label: 'Errors', value: figure(current?.errors), trend: current && previous ? move(current.errors, previous.errors) : undefined, info: 'Records at error or fatal, exceptions and crashes among them.' },
    { key: 'crashes', label: 'Crashes', value: figure(current?.crashes), trend: current && previous ? move(current.crashes, previous.crashes) : undefined, info: 'Times the game died, as reported on the next launch.' },
    { key: 'issues', label: 'Issues', value: figure(current?.issues), info: current ? `${writeFigure(current.new_issues)} first seen in the range. An issue is every record of one bug: the same exception from the same place, or the same error message.` : 'An issue is every record of one bug.' },
    { key: 'installs', label: 'Installs affected', value: figure(current?.affected_installs), info: 'Installs any issue reached in the range, approximately.' }
  ]
})

// --- Live -------------------------------------------------------------------------------------

/*
 * A range that follows the present can stay live. Refreshing it would move the list under a
 * person reading it, so a refresh happens only while they are at the top with nothing open;
 * otherwise the page counts what arrived and offers it.
 */
const live = ref(false)
const newer = ref(0)
const atTop = ref(true)
const selected = ref<LogRecordSummary | null>(null)

function goLive() {
  if (!range.value.preset) setPreset('15m')
  live.value = !live.value
}

useIntervalFn(async () => {
  if (!live.value || !range.value.preset || !path.value || document.hidden) return
  if (atTop.value && !selected.value) {
    refresh()
    newer.value = 0
    return
  }
  const top = log.records.value[0]
  if (top) newer.value = await countNewer(path.value, top.at, query.value).catch(() => newer.value)
}, 8000)

watch(() => range.value.preset, (preset) => {
  if (!preset) live.value = false
})

function showNewer() {
  newer.value = 0
  refresh()
}

function onZoom(from: number, to: number) {
  live.value = false
  setAbsolute(from, to)
}

// --- One record -------------------------------------------------------------------------------

const detail = ref<LogRecord | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const narrow = useMediaQuery('(max-width: 1023px)')
const dialogOpen = computed({
  get: () => narrow.value && selected.value !== null,
  set: (value) => {
    if (!value) selected.value = null
  }
})

async function select(record: LogRecordSummary) {
  selected.value = record
  if (!path.value) return
  detailLoading.value = true
  detailError.value = ''
  try {
    const read = await readLogRecord(path.value, record)
    if (selected.value?.id === record.id) detail.value = read
  } catch (caught) {
    if (selected.value?.id === record.id) detailError.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    if (selected.value?.id === record.id) detailLoading.value = false
  }
}

function closeRecord() {
  selected.value = null
  detail.value = null
}

watch(query, closeRecord)

const listBox = useTemplateRef<HTMLElement>('listBox')
useEventListener(listBox, 'scroll', (event) => {
  atTop.value = (event.target as HTMLElement).scrollTop < 8
}, { capture: true, passive: true })

const nothingYet = computed(() => !histogramRead.isFetching.value && histogram.value && buckets.value.every(bucket => !LOG_BANDS.some(band => bandCount(bucket, band.key))) && !text.value)

const EXAMPLE = `POST /v1/logs
Authorization: Bearer <project key>

{
  "schema_version": 1,
  "batch_id": "<uuid>",
  "install_id": "<uuid>",
  "sdk": { "name": "unity", "version": "1.0.0" },
  "app": { "version": "1.4.2" },
  "device": { "platform": "android", "model": "Pixel 7" },
  "consent": { "analytics": true, "policy_version": "2026-01" },
  "records": [
    { "time": "2026-09-25T14:05:11.204Z", "level": "error",
      "logger": "Store", "message": "Receipt validation failed",
      "attributes": { "scene": "Shop" } }
  ]
}`
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Logs">
      <template #description>
        The game's own logs, exceptions and crashes in {{ workspace.environment }}, searchable to the millisecond.
      </template>
      <template #actions>
        <LogStorageButton />
      </template>
    </PageHeader>

    <KpiBoard
      :description="`${rangeWords(range)}, against the stretch before it.`"
      :items="board"
      :loading="summaryRead.isFetching.value && !summaryRead.data.value"
      title="In this range"
    />

    <SectionCard :padded="false" title="Explorer">
      <template #actions>
        <span v-if="histogram" class="font-mono text-[11px] text-ink/50">
          {{ writeFigure(buckets.reduce((sum, bucket) => sum + LOG_BANDS.filter(band => !bands.length || bands.includes(band.key)).reduce((inner, band) => inner + bandCount(bucket, band.key), 0), 0)) }} records
        </span>
      </template>

      <div class="flex flex-col gap-3 border-b border-line px-4 pt-3 pb-3">
        <div class="flex flex-wrap items-center gap-2">
          <SearchField
            v-model="draft"
            class="min-w-64 flex-1 font-mono"
            label="Search the logs"
            :placeholder="SEARCH_HINT"
          />
          <v-menu location="bottom end">
            <template #activator="{ props: activator }">
              <v-btn
                v-bind="activator"
                aria-label="Search syntax"
                class="ps-toolbar-control"
                density="comfortable"
                icon="material-symbols:help-outline"
                size="small"
                variant="outlined"
              />
            </template>
            <v-card class="p-3" max-width="420">
              <h3 class="m-0 mb-2 text-xs font-medium text-ink">
                Search syntax
              </h3>
              <dl class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
                <template v-for="row in SYNTAX" :key="row.example">
                  <dt class="font-mono text-[11px] text-primary">
                    {{ row.example }}
                  </dt>
                  <dd class="m-0 text-ink/65">
                    {{ row.means }}
                  </dd>
                </template>
              </dl>
            </v-card>
          </v-menu>
          <LogTimeRange
            :earliest="histogram ? Date.parse(histogram.earliest) : undefined"
            :range="range"
            @absolute="(from, to) => { live = false; setAbsolute(from, to) }"
            @preset="setPreset"
            @shift="shift"
            @zoom-out="zoomOut"
          />
          <v-btn
            :color="live ? 'primary' : undefined"
            :variant="live ? 'tonal' : 'outlined'"
            class="ps-toolbar-control text-xs font-medium"
            size="small"
            @click="goLive"
          >
            <span :class="live ? 'bg-primary animate-pulse' : 'bg-ink/30'" aria-hidden="true" class="mr-1.5 inline-block size-1.5 rounded-full" />
            Live
          </v-btn>
        </div>

        <!-- The bands: the chart's legend, and a filter. -->
        <div aria-label="Filter by level" class="flex flex-wrap items-center gap-1.5" role="group">
          <v-tooltip
            v-for="band in LOG_BANDS"
            :key="band.key"
            :text="band.hint"
            location="top"
          >
            <template #activator="{ props: tip }">
              <button
                v-bind="tip"
                :aria-pressed="bands.includes(band.key)"
                :class="bands.length && !bands.includes(band.key) ? 'opacity-45' : ''"
                class="flex items-center gap-1.5 rounded-full border border-line px-2.5 py-0.5 text-[11px] text-ink/80 transition-opacity hover:border-ink/25"
                type="button"
                @click="toggleBand(band.key)"
              >
                <span :style="{ background: colors[band.key] }" aria-hidden="true" class="size-2 rounded-[2px]" />
                {{ band.label }}
                <span class="font-mono text-ink/50">{{ writeFigure(buckets.reduce((sum, bucket) => sum + bandCount(bucket, band.key), 0), { compact: true }) }}</span>
              </button>
            </template>
          </v-tooltip>
          <button
            v-if="bands.length"
            class="ml-1 text-[11px] text-primary hover:underline"
            type="button"
            @click="bands = []"
          >
            Every level
          </button>
          <span class="ml-auto hidden text-[11px] text-ink/40 sm:inline">Drag across the chart to zoom</span>
        </div>

        <p v-if="searchError" class="m-0 flex items-center gap-1.5 text-xs text-error">
          <Icon aria-hidden="true" name="material-symbols:error-outline" />
          {{ searchError.message }}
        </p>
        <LogHistogram
          v-else
          :buckets="buckets"
          :levels="chosenLevels"
          :loading="histogramRead.isFetching.value && !histogram"
          :step="histogram?.step_seconds ?? 60"
          @select="onZoom"
        />
      </div>

      <div ref="listBox" :class="selected && !narrow ? 'lg:grid-cols-[minmax(0,1fr)_minmax(0,440px)]' : ''" class="grid h-[min(68vh,760px)] min-h-[420px]">
        <LogList
          :compact="!!selected && !narrow"
          :done="log.done.value"
          :loading="log.loading.value"
          :newer="newer"
          :records="log.records.value"
          :selected="selected?.id ?? null"
          class="min-h-0"
          @more="log.more"
          @select="select"
          @show-newer="showNewer"
        >
          <template #empty>
            <div v-if="log.error.value" class="p-6 text-center text-xs text-error">
              {{ log.error.value.message }}
            </div>
            <EmptyState
              v-else-if="nothingYet"
              description="Nothing was logged in this range. Send records from the game to POST /v1/logs with a project key; the example is below."
              icon="material-symbols:receipt-long-outline"
              title="No logs yet"
            />
            <EmptyState
              v-else
              description="Nothing in this range matches. Widen the range, or loosen the search."
              icon="material-symbols:search-off"
              title="No matching records"
            />
          </template>
        </LogList>
        <LogRecordPanel
          v-if="selected && !narrow"
          :error="detailError"
          :loading="detailLoading"
          :record="detail"
          class="min-h-0 border-l border-line"
          closable
          @close="closeRecord"
          @filter="addFilter"
        />
      </div>
    </SectionCard>

    <LayerCard
      v-if="nothingYet"
      description="Batches of up to a thousand records, gzip or zstd, with the same key and consent block as analytics. Crashes are written to disk as they happen and sent on the next launch with crash: true."
      expandable
      title="Sending logs"
    >
      <CodeBlock :code="EXAMPLE" lang="json" title="HTTP" />
    </LayerCard>

    <!-- A record on a narrow screen, where there is no room beside the list. -->
    <v-dialog v-model="dialogOpen" fullscreen>
      <v-card>
        <LogRecordPanel
          :error="detailError"
          :loading="detailLoading"
          :record="detail"
          class="h-full"
          closable
          @close="closeRecord"
          @filter="(value) => { addFilter(value); closeRecord() }"
        />
      </v-card>
    </v-dialog>
  </div>
</template>
