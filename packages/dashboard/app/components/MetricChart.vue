<script setup lang="ts">
/**
 * The canvas half of a metric card: ECharts in the panel's own type, ink and palette.
 * What it draws is decided in `utils/metric-chart.ts`; this component reads the theme,
 * honours the motion preference and repaints once the fonts are in.
 *
 * The repaint matters. The canvas is painted on mount, which is usually before DM Mono
 * has arrived, and a canvas does not re-render itself when a font loads the way the DOM
 * does: left alone, every axis label stays in the fallback face until something else
 * moves the chart.
 */

import type { WrittenFigureOptions } from '../utils/format'
import { buildMetricOption, type MetricChartType, type MetricMark, type MetricSeries } from '../utils/metric-chart'

const props = withDefaults(
  defineProps<{
    categories: readonly string[]
    /** What the tooltip calls each category; the tick when absent. */
    titles?: readonly string[]
    series: readonly MetricSeries[]
    type?: MetricChartType
    /** Series names switched off in the legend. */
    hidden?: readonly string[]
    format?: WrittenFigureOptions
    /** In pixels. The table view takes the same, so switching does not move the card. */
    height?: number
    /** What a screen reader is told the picture is. The table is what it reads instead. */
    label?: string
    /** Moments and spans drawn behind the series. */
    marks?: readonly MetricMark[]
  }>(),
  {
    titles: undefined,
    type: 'line',
    hidden: () => [],
    format: () => ({}),
    height: 256,
    label: '',
    marks: () => []
  }
)

const palette = useMetricPalette()
const motion = usePreferredReducedMotion()

const option = computed<ECOption>(() =>
  buildMetricOption(
    {
      categories: props.categories,
      series: props.series,
      type: props.type,
      hidden: props.hidden,
      format: props.format,
      animate: motion.value !== 'reduce',
      marks: props.marks,
      ...(props.titles === undefined ? {} : { titles: props.titles })
    },
    palette.value
  )
)

const chart = useTemplateRef<{ resize: () => void }>('chart')

onMounted(() => {
  // `resize` runs a full update, which is what repaints the text in the loaded face.
  document.fonts?.ready.then(() => chart.value?.resize())
})
</script>

<template>
  <VChart
    ref="chart"
    :aria-label="label"
    :option="option"
    :style="{ height: `${height}px` }"
    autoresize
    class="metric-chart block w-full"
    role="img"
  />
</template>

<style>
/*
 * The tooltip is DOM that ECharts writes inside the chart's element, outside any
 * template, so a scoped block cannot reach it and these are global under one prefix.
 * ECharts is told to draw no box of its own (transparent, no border, no padding), and
 * the card in the markup carries the look: a menu's edge and shadow on the surface,
 * because a readout listing four series is a small card and not a label.
 *
 * The keys mirror MetricKey.vue, which draws the same two shapes in the legend and the
 * table's head; the two are kept side by side so a change to one is a change to both.
 */
.metric-tooltip {
  font-family: var(--v-font-body);
}

.metric-tooltip__card {
  background: rgb(var(--v-theme-surface));
  border: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: var(--ps-radius-small);
  box-shadow: 0 8px 24px -4px rgba(0, 0, 0, 0.24);
  color: rgb(var(--v-theme-on-surface));
  font-size: 0.75rem;
  line-height: 1rem;
  min-width: 11rem;
  padding: 0.5rem 0.625rem;
}

.metric-tooltip__title {
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-weight: 500;
  margin-bottom: 0.375rem;
}

.metric-tooltip__rows {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  list-style: none;
  margin: 0;
  padding: 0;
}

.metric-tooltip__row,
.metric-tooltip__total {
  align-items: center;
  display: flex;
  gap: 0.5rem;
}

.metric-tooltip__total {
  border-top: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
  margin-top: 0.375rem;
  padding-top: 0.375rem;
}

.metric-tooltip__key {
  flex: none;
}

.metric-tooltip__key--line {
  background: var(--metric-key);
  border-radius: 1px;
  height: 2px;
  width: 12px;
}

.metric-tooltip__key--dashed {
  background: repeating-linear-gradient(90deg, var(--metric-key) 0 3px, transparent 3px 5px);
}

.metric-tooltip__key--fill {
  background: var(--metric-key);
  border-radius: 2px;
  height: 8px;
  margin-inline: 2px;
  width: 8px;
}

.metric-tooltip__name {
  color: rgba(var(--v-theme-on-surface), 0.6);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The value leads: the reader has the series and wants the number. */
.metric-tooltip__value {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  margin-left: auto;
  padding-left: 1rem;
}
</style>
