<script setup lang="ts">
import type { LogBucket } from '~/composables/logs'

/**
 * Records over time: Cloud Logging's histogram. One bar of volume per step, every level together,
 * so the shape of the traffic reads at a glance; which levels a bar holds is in its tooltip, and
 * the level chips above narrow the bars themselves. Drag across the bars to zoom into that
 * stretch: the chart is how a person finds the minute something went wrong, and the list under
 * it is what happened in that minute.
 *
 * No animation. A log chart redraws on every zoom, filter and live refresh, and bars growing into
 * place each time read as lag rather than as motion.
 *
 * The drag is ECharts' brush, taken as the chart's own cursor so a press on the plot starts one
 * with no toolbar button first. On release the stretch is handed up as times and the brush is
 * cleared: the page's range changes and the chart redraws at the new width, which is the feedback.
 */
const props = withDefaults(defineProps<{
  buckets: readonly LogBucket[]
  /** One bar's width, in seconds. */
  step: number
  loading?: boolean
  height?: number
  /** The levels the bars count; every level when empty. The chips above set it, so switching
      one off redraws the bars without asking the server again. */
  levels?: readonly string[]
}>(), { loading: false, height: 184, levels: () => [] })

const emit = defineEmits<{ select: [from: number, to: number] }>()

const colors = useLogColors()
const palette = useMetricPalette()

const LEVELS = ['fatal', 'error', 'warn', 'info', 'debug', 'trace'] as const
const LEVEL_WORDS: Record<(typeof LEVELS)[number], string> = { fatal: 'Fatal', error: 'Error', warn: 'Warning', info: 'Info', debug: 'Debug', trace: 'Trace' }

const counted = computed(() => LEVELS.filter(level => !props.levels.length || props.levels.includes(level)))
const totalOf = (bucket: LogBucket) => counted.value.reduce((sum, level) => sum + bucket[level], 0)

const times = computed(() => props.buckets.map(bucket => Date.parse(bucket.at)))

const tickDay = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric' })
const tickClock = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hour12: false })
const tickSeconds = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const titleFormat = new Intl.DateTimeFormat('en', { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

/* A tick says as little as its bar width needs: the day for day bars, the clock otherwise, and
   the day again where a clock tick crosses midnight so a multi-day range stays readable. */
function tick(at: number, index: number): string {
  if (props.step >= 86_400) return tickDay.format(at)
  const previous = times.value[index - 1]
  const crossed = previous !== undefined && new Date(previous).getDate() !== new Date(at).getDate()
  if (props.step < 60) return tickSeconds.format(at)
  return crossed || index === 0 ? `${tickDay.format(at)} ${tickClock.format(at)}` : tickClock.format(at)
}

function stepWords(seconds: number): string {
  if (seconds % 86_400 === 0) return `${seconds / 86_400} day`
  if (seconds % 3600 === 0) return `${seconds / 3600} hour`
  if (seconds % 60 === 0) return `${seconds / 60} min`
  return `${seconds} s`
}

function escape(text: string): string {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
}

/* The bar's total leads; the levels it holds follow, the empty ones left out. */
function tooltip(index: number): string {
  const bucket = props.buckets[index]
  if (!bucket) return ''
  const start = times.value[index]!
  const rows = counted.value.filter(level => bucket[level] > 0).map(level =>
    `<li class="log-tooltip__row"><span class="log-tooltip__key" style="background:${colors.value[bandOf(level)]}"></span>`
    + `<span class="log-tooltip__name">${LEVEL_WORDS[level]}</span>`
    + `<span class="log-tooltip__value">${writeFigure(bucket[level])}</span></li>`).join('')
  return `<div class="log-tooltip__card"><div class="log-tooltip__title">${escape(titleFormat.format(start))}`
    + `<span class="log-tooltip__step"> · ${stepWords(props.step)}</span></div>`
    + `<div class="log-tooltip__total log-tooltip__total--lead"><span class="log-tooltip__name">Records</span><span class="log-tooltip__value">${writeFigure(totalOf(bucket))}</span></div>`
    + (rows ? `<ul class="log-tooltip__rows">${rows}</ul>` : '')
    + '<div class="log-tooltip__hint">Drag across bars to zoom in</div></div>'
}

const option = computed<ECOption>(() => {
  const ink = palette.value.ink
  const accent = palette.value.series[0] ?? ink
  return {
    animation: false,
    textStyle: { fontFamily: SANS },
    grid: { top: 8, right: 4, bottom: 0, left: 0 },
    legend: { show: false },
    brush: {
      xAxisIndex: 0,
      brushType: 'lineX',
      brushMode: 'single',
      transformable: false,
      throttleType: 'debounce',
      throttleDelay: 0,
      removeOnClick: true,
      brushStyle: { color: alpha(accent, 0.14), borderColor: alpha(accent, 0.6), borderWidth: 1 },
      outOfBrush: { colorAlpha: 0.35 }
    },
    tooltip: {
      trigger: 'axis',
      renderMode: 'html',
      className: 'log-tooltip',
      backgroundColor: 'transparent',
      borderWidth: 0,
      padding: 0,
      extraCssText: 'box-shadow: none;',
      transitionDuration: 0.1,
      confine: true,
      axisPointer: { type: 'shadow', shadowStyle: { color: alpha(ink, 0.05) } },
      formatter: (params: unknown) => {
        const first = Array.isArray(params) ? params[0] : params
        return tooltip((first as { dataIndex?: number })?.dataIndex ?? 0)
      }
    },
    xAxis: {
      type: 'category',
      data: times.value.map((at, index) => tick(at, index)),
      boundaryGap: true,
      axisLine: { show: true, lineStyle: { color: alpha(ink, 0.12) } },
      axisTick: { show: false },
      axisLabel: { color: alpha(ink, 0.5), fontFamily: MONO, fontSize: 10, margin: 10, hideOverlap: true },
      splitLine: { show: false }
    },
    yAxis: {
      type: 'value',
      splitNumber: 3,
      minInterval: 1,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: alpha(ink, 0.5), fontFamily: MONO, fontSize: 10, margin: 10, formatter: (value: number) => writeFigure(value, { compact: true }) },
      splitLine: { show: true, lineStyle: { color: alpha(ink, 0.07) } }
    },
    series: [{
      name: 'Records',
      type: 'bar' as const,
      data: props.buckets.map(bucket => totalOf(bucket) || null),
      barCategoryGap: '18%',
      barMaxWidth: 28,
      itemStyle: { color: accent, borderRadius: [3, 3, 0, 0] },
      emphasis: { focus: 'none' as const, itemStyle: { color: alpha(accent, 0.8) } }
    }]
  }
})

const chart = useTemplateRef<{
  resize: () => void
  dispatchAction: (action: Record<string, unknown>) => void
}>('chart')

/* The brush as the chart's cursor: pressing on the plot starts a selection. Re-armed after every
   redraw, because a new option drops the cursor ECharts was holding. */
function arm() {
  chart.value?.dispatchAction({ type: 'takeGlobalCursor', key: 'brush', brushOption: { brushType: 'lineX', brushMode: 'single' } })
}

function onBrushEnd(event: unknown) {
  const range = (event as { areas?: { coordRange?: [number, number] }[] }).areas?.[0]?.coordRange
  chart.value?.dispatchAction({ type: 'brush', areas: [] })
  if (!range || !times.value.length) return
  const first = Math.max(0, Math.min(range[0], range[1]))
  const last = Math.min(times.value.length - 1, Math.max(range[0], range[1]))
  const from = times.value[first]
  const to = times.value[last]
  if (from === undefined || to === undefined) return
  emit('select', from, to + props.step * 1000)
}

watch(option, () => nextTick(arm))
onMounted(() => {
  document.fonts?.ready.then(() => chart.value?.resize())
  nextTick(arm)
})

const label = computed(() => {
  const total = props.buckets.reduce((sum, bucket) => sum + totalOf(bucket), 0)
  return `Records over time in ${props.buckets.length} bars of ${stepWords(props.step)}, ${writeFigure(total)} in all. The list below has every record.`
})
</script>

<template>
  <div :style="{ height: `${height}px` }" class="relative">
    <VChart
      v-if="buckets.length"
      ref="chart"
      :aria-label="label"
      :option="option"
      autoresize
      class="log-histogram block h-full w-full"
      role="img"
      @brush-end="onBrushEnd"
      @finished.once="arm"
    />
    <div v-if="loading" aria-hidden="true" class="absolute inset-0 flex items-end gap-[3px] px-1 pb-6">
      <span
        v-for="index in 60"
        :key="index"
        :style="{ height: `${18 + ((index * 37) % 53)}%` }"
        class="flex-1 animate-pulse rounded-t-[3px] bg-ink/6"
      />
    </div>
  </div>
</template>

<style>
/* The tooltip is DOM ECharts writes outside any template, so its styles are global under one
   prefix; the look is the metric chart's readout. */
.log-histogram canvas {
  cursor: crosshair;
}

.log-tooltip {
  font-family: var(--v-font-body);
}

.log-tooltip__card {
  background: rgb(var(--v-theme-surface));
  border: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: var(--ps-radius-small);
  box-shadow: 0 8px 24px -4px rgba(0, 0, 0, 0.24);
  color: rgb(var(--v-theme-on-surface));
  font-size: 0.75rem;
  line-height: 1rem;
  min-width: 12rem;
  padding: 0.5rem 0.625rem;
}

.log-tooltip__title {
  color: rgba(var(--v-theme-on-surface), 0.7);
  font-weight: 500;
  margin-bottom: 0.375rem;
}

.log-tooltip__step {
  color: rgba(var(--v-theme-on-surface), 0.45);
  font-weight: 400;
}

.log-tooltip__rows {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  list-style: none;
  margin: 0;
  padding: 0;
}

.log-tooltip__row,
.log-tooltip__total {
  align-items: center;
  display: flex;
  gap: 0.5rem;
}

.log-tooltip__total--lead {
  border-bottom: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
  margin-bottom: 0.375rem;
  padding-bottom: 0.375rem;
}

.log-tooltip__key {
  border-radius: 2px;
  flex: none;
  height: 8px;
  margin-inline: 2px;
  width: 8px;
}

.log-tooltip__name {
  color: rgba(var(--v-theme-on-surface), 0.6);
  flex: 1;
}

.log-tooltip__value {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  padding-left: 1rem;
}

.log-tooltip__hint {
  color: rgba(var(--v-theme-on-surface), 0.4);
  font-size: 0.6875rem;
  margin-top: 0.375rem;
}
</style>
