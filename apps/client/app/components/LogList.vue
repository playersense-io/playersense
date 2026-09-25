<script setup lang="ts">
import type { LogRecordSummary } from '~/composables/logs'

/**
 * The records, newest first, as a log reads: one line each, the time to the millisecond, the
 * level, where it came from and what it said. Tens of thousands of lines stay smooth because only
 * the lines in view (and a screen either side) are in the DOM: VueUse's useVirtualList, over rows
 * of one fixed height. Detail never grows a row; it opens beside the list.
 *
 * Scrolling near the end asks for the next page, so the list is as long as the range, a page at a
 * time. The keyboard moves the selection (up, down, j, k) and the detail follows it.
 */
const props = withDefaults(defineProps<{
  records: readonly LogRecordSummary[]
  loading?: boolean
  done?: boolean
  selected?: string | null
  /** Records newer than the top one, waiting while the person reads. */
  newer?: number
  /** A narrower list beside an open record drops the columns it can live without. */
  compact?: boolean
}>(), { loading: false, done: false, selected: null, newer: 0, compact: false })

const emit = defineEmits<{
  more: []
  select: [record: LogRecordSummary]
  showNewer: []
}>()

const ROW = 32

const source = computed(() => props.records as LogRecordSummary[])
const { list, containerProps, wrapperProps, scrollTo } = useVirtualList(source, { itemHeight: ROW, overscan: 24 })

/* Near the end, the next page: a screen and a half ahead, so a steady scroll never waits. */
watch(() => list.value.at(-1)?.index, (last) => {
  if (last === undefined || props.done || props.loading) return
  if (last >= props.records.length - 40) emit('more')
})

/* A new search starts at the top. */
watch(() => props.records[0]?.id, (first, before) => {
  if (first !== before && props.records.length <= 200) scrollTo(0)
})

const colors = useLogColors()

function move(delta: number) {
  if (!props.records.length) return
  const at = props.records.findIndex(record => record.id === props.selected)
  const next = Math.max(0, Math.min(props.records.length - 1, at < 0 ? 0 : at + delta))
  const record = props.records[next]!
  emit('select', record)
  // Keep the selection in view without jumping when it already is.
  const element = containerProps.ref.value
  if (element) {
    const top = next * ROW
    if (top < element.scrollTop) element.scrollTop = top
    else if (top + ROW > element.scrollTop + element.clientHeight) element.scrollTop = top + ROW - element.clientHeight
  }
}

function onKey(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'j') {
    event.preventDefault()
    move(1)
  } else if (event.key === 'ArrowUp' || event.key === 'k') {
    event.preventDefault()
    move(-1)
  }
}

const LEVEL_WORDS: Record<string, string> = { trace: 'TRACE', debug: 'DEBUG', info: 'INFO', warn: 'WARN', error: 'ERROR', fatal: 'FATAL' }

function summaryOf(record: LogRecordSummary): string {
  if (record.kind !== 'log' && record.exception_type) {
    return record.exception_value ? `${record.exception_type}: ${record.exception_value}` : record.exception_type
  }
  return record.message ?? ''
}
</script>

<template>
  <div class="flex min-h-0 flex-col">
    <!-- The head, outside the scroller so it stays put. -->
    <div aria-hidden="true" class="log-row flex-none border-b border-line bg-canvas/60 text-[10px] font-medium tracking-wide text-ink/45 uppercase">
      <span class="log-row__time">Time</span>
      <span class="log-row__level">Level</span>
      <span v-if="!compact" class="log-row__logger">Source</span>
      <span class="min-w-0 flex-1">Message</span>
      <span v-if="!compact" class="log-row__release text-right">Release</span>
    </div>

    <button
      v-if="newer > 0"
      class="flex flex-none items-center justify-center gap-1.5 border-b border-line bg-primary/8 py-1 text-[11px] font-medium text-primary transition-colors hover:bg-primary/12"
      type="button"
      @click="emit('showNewer')"
    >
      <Icon aria-hidden="true" name="material-symbols:arrow-upward" />
      {{ writeFigure(newer) }} new {{ newer === 1 ? 'record' : 'records' }}
    </button>

    <div
      v-bind="containerProps"
      aria-label="Log records, newest first. Arrow keys move between them."
      class="log-scroll min-h-0 flex-1 overflow-y-auto outline-none focus-visible:ring-2 focus-visible:ring-primary/40 focus-visible:ring-inset"
      role="listbox"
      tabindex="0"
      @keydown="onKey"
    >
      <div v-bind="wrapperProps">
        <div
          v-for="{ data: record, index } in list"
          :key="record.id"
          :aria-selected="record.id === selected"
          :class="[record.id === selected ? 'bg-primary/10' : index % 2 ? 'bg-ink/[0.018] hover:bg-ink/5' : 'hover:bg-ink/5', record.kind === 'crash' ? 'log-row--crash' : '']"
          :style="{ '--log-level': colors[bandOf(record.level)] }"
          class="log-row group cursor-pointer"
          role="option"
          @click="emit('select', record)"
        >
          <span class="log-row__time font-mono text-ink/55">
            <span class="text-ink/35">{{ recordTime(record.at).day }}</span>
            {{ recordTime(record.at).clock }}<span class="text-ink/35">.{{ recordTime(record.at).ms }}</span>
          </span>
          <span class="log-row__level">
            <span class="log-level font-mono">{{ LEVEL_WORDS[record.level] ?? record.level }}</span>
          </span>
          <span v-if="!compact" :title="record.logger" class="log-row__logger truncate font-mono text-ink/55">{{ record.logger || '–' }}</span>
          <span class="flex min-w-0 flex-1 items-center gap-2">
            <span v-if="record.kind === 'crash'" class="flex-none rounded-sm bg-error/12 px-1 font-mono text-[10px] leading-4 font-medium text-error">CRASH</span>
            <span class="truncate font-mono text-ink/85">{{ summaryOf(record) }}</span>
            <span v-if="record.culprit" class="hidden flex-none truncate font-mono text-[11px] text-ink/40 xl:inline">in {{ record.culprit }}</span>
          </span>
          <span v-if="!compact" class="log-row__release truncate text-right font-mono text-ink/45">{{ record.app_version }}</span>
        </div>
      </div>

      <div v-if="loading && !records.length" class="flex flex-col">
        <div v-for="index in (records.length ? 3 : 14)" :key="index" class="log-row">
          <span class="log-row__time"><span class="block h-2.5 w-28 animate-pulse rounded bg-ink/8" /></span>
          <span class="log-row__level"><span class="block h-2.5 w-10 animate-pulse rounded bg-ink/8" /></span>
          <span class="flex-1"><span :style="{ width: `${30 + ((index * 23) % 55)}%` }" class="block h-2.5 animate-pulse rounded bg-ink/6" /></span>
        </div>
      </div>
      <p v-else-if="loading" class="m-0 py-3 text-center text-[11px] text-ink/40">
        Loading more…
      </p>
      <p v-else-if="done && records.length" class="m-0 py-4 text-center text-[11px] text-ink/40">
        The start of the range. {{ writeFigure(records.length) }} {{ records.length === 1 ? 'record' : 'records' }}.
      </p>
      <slot v-else-if="!records.length" name="empty" />
    </div>
  </div>
</template>

<style scoped>
.log-row {
  align-items: center;
  box-shadow: inset 3px 0 0 transparent;
  display: flex;
  font-size: 12px;
  gap: 12px;
  height: 32px;
  padding: 0 12px 0 14px;
  white-space: nowrap;
}

/* The level's colour as a rule down the left edge: scanned, not read. */
.log-row[style*='--log-level'] {
  box-shadow: inset 3px 0 0 var(--log-level);
}

.log-row__time {
  flex: none;
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
  width: 158px;
}

.log-row__level {
  flex: none;
  width: 52px;
}

.log-row__logger {
  flex: none;
  font-size: 11.5px;
  width: 112px;
}

.log-row__release {
  flex: none;
  font-size: 11px;
  width: 64px;
}

.log-level {
  color: var(--log-level);
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.log-row--crash {
  background-image: linear-gradient(90deg, rgba(var(--v-theme-error), 0.07), transparent 40%);
}
</style>
