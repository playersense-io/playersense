<script setup lang="ts">
/**
 * The chart as a table: every value the canvas draws, reachable without a pointer and
 * readable by whatever a canvas is not. The same series in the same order, with the
 * legend's own key beside each name so the two views can be matched, and the values in
 * the mono face set right so a column of them lines up on the digit.
 *
 * It is capped at the chart's height and scrolls inside it, so a long table never grows
 * the card past what the chart took. The head stays put while the rows scroll, which is
 * why the borders are on the cells: a collapsed border scrolls away with the table.
 */

import { type WrittenFigureOptions, writeFigure } from '../utils/format'
import { colorOf, type MetricChartType, type MetricSeries } from '../utils/metric-chart'

const props = withDefaults(
  defineProps<{
    categories: readonly string[]
    /** What the row is called; the tick when absent. */
    titles?: readonly string[]
    series: readonly MetricSeries[]
    type?: MetricChartType
    hidden?: readonly string[]
    format?: WrittenFigureOptions
    height?: number
    /** The first column's head: what one row is. */
    categoryLabel?: string
  }>(),
  {
    titles: undefined,
    type: 'line',
    hidden: () => [],
    format: () => ({}),
    height: 256,
    categoryLabel: 'Period'
  }
)

const palette = useMetricPalette()

/* Keyed by position in the full list, so a series keeps its colour when one before it
   is hidden. */
const columns = computed(() =>
  props.series
    .map((entry, index) => ({
      entry,
      color: colorOf(entry, index, palette.value),
      kind: (entry.type ?? props.type) === 'line' ? ('line' as const) : ('fill' as const)
    }))
    .filter(({ entry }) => !props.hidden.includes(entry.name))
)

function cell(entry: MetricSeries, index: number): string {
  const value = entry.data[index] ?? null
  return value === null ? '–' : writeFigure(value, props.format)
}
</script>

<template>
  <div
    :style="{ maxHeight: `${height}px` }"
    class="overflow-auto rounded-(--ps-radius-small) border border-line"
  >
    <table class="w-full border-separate border-spacing-0 text-xs">
      <thead class="sticky top-0 z-10 bg-surface">
        <tr>
          <th
            class="border-b border-line px-3 py-2 text-left font-medium text-ink/60"
            scope="col"
          >
            {{ categoryLabel }}
          </th>
          <th
            v-for="column in columns"
            :key="column.entry.name"
            class="border-b border-line px-3 py-2 text-right font-medium text-ink/60"
            scope="col"
          >
            <span class="inline-flex items-center gap-1.5">
              <MetricKey :color="column.color" :dashed="column.entry.dashed" :kind="column.kind" />
              {{ column.entry.name }}
            </span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(category, index) in categories" :key="category" class="group">
          <th
            class="border-b border-line px-3 py-1.5 text-left font-normal text-ink/80 group-last:border-b-0"
            scope="row"
          >
            {{ titles?.[index] ?? category }}
          </th>
          <td
            v-for="column in columns"
            :key="column.entry.name"
            class="border-b border-line px-3 py-1.5 text-right font-mono text-ink tabular-nums group-last:border-b-0"
          >
            {{ cell(column.entry, index) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
