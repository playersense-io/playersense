<script setup lang="ts">
import type { MetricOption, MetricView, SegmentedOption } from '../utils/components'
/**
 * One metric in full: its name, a sentence on what it counts, the figure for the period
 * and how it moved through it, drawn as a chart or laid out as a table.
 *
 * The title is a select when there are `options`, so one card can stand for a family of
 * metrics and the reader picks which; it is a heading otherwise. What sits top right is
 * the page's to decide and comes through the `actions` slot (the period, most often),
 * followed by the one control the card owns, the chart-or-table switch.
 *
 * The table is not a fallback. Every value the chart draws is in it, which is what makes
 * the chart readable without a pointer and by a screen reader, and the legend's toggles
 * apply to both views so the two never disagree about what is shown.
 *
 * The figure and its trend are the statistic tile's at a size up, and share its
 * arithmetic (`utils/trend.ts`) so the card and the tile agree on what a change of 4.1% is.
 * While the card is loading, everything that depends on the answer is drawn as bones
 * (`MetricSkeleton`) in its own place and at its own size: the figure and its trend, the
 * view switch, the legend when the series say there will be one, and the view itself at
 * the height of what is coming, which is what keeps the card one height across the swap;
 * the table is capped at the chart's height for the same reason. The controls are bones
 * rather than controls because the actions, the switch and the legend would each offer to
 * change, redraw or filter something that is not there yet. What stays live is what the
 * card is, its name and its sentence, which do not depend on the answer.
 */
import { type FigureFormat, formatFigure, type WrittenFigureOptions } from '../utils/format'
import { colorOf, type MetricChartType, type MetricSeries } from '../utils/metric-chart'
import { TREND_ICONS, TREND_TONES, trendOf } from '../utils/trend'

const props = withDefaults(
  defineProps<{
    /** The heading, when the card shows one metric. Unused when `options` is given. */
    title?: string
    /** Metrics the card can show one at a time; the title becomes a select over them. */
    options?: readonly MetricOption[]
    /** What the metric counts. The chosen option's own when it has one and this is empty. */
    description?: string
    /** The headline figure. Null is a figure not yet known; absent is a card without one. */
    value?: number | string | null
    prefix?: string
    unit?: string
    format?: FigureFormat
    compact?: boolean
    decimals?: number
    /** The same figure one period ago. Given, the card shows how far it moved. */
    previous?: number | null
    compare?: string
    /** Which direction is good news. Down, for anything that counts failures. */
    positive?: 'up' | 'down'
    categories: readonly string[]
    /** What the tooltip and the table call each category; the tick when absent. */
    titles?: readonly string[]
    series: readonly MetricSeries[]
    /** How every series is drawn unless it says otherwise. */
    type?: MetricChartType
    /** What one category is, for the table's first column: "Day", "Hour". */
    categoryLabel?: string
    /** The chart's height in pixels; the table takes the same. */
    height?: number
    loading?: boolean
  }>(),
  {
    title: '',
    options: undefined,
    description: '',
    value: undefined,
    prefix: '',
    unit: '',
    format: 'number',
    compact: false,
    decimals: 1,
    previous: null,
    compare: 'vs previous period',
    positive: 'up',
    titles: undefined,
    type: 'line',
    categoryLabel: 'Period',
    height: 256,
    loading: false
  }
)

/** Which of `options` is showing. */
const selected = defineModel<string>()
const view = defineModel<MetricView>('view', { default: 'chart' })

const titleId = useId()
const palette = useMetricPalette()

const heading = computed(
  () => props.options?.find(option => option.value === selected.value)?.label ?? props.title
)

const caption = computed(
  () =>
    props.description
    || props.options?.find(option => option.value === selected.value)?.description
    || ''
)

const figure = computed(() => {
  if (props.value === undefined || props.value === null) return null
  if (typeof props.value === 'string')
    return { digits: props.value, magnitude: '', unit: props.unit }
  const parts = formatFigure(props.value, {
    format: props.format,
    compact: props.compact,
    decimals: props.decimals
  })
  return { ...parts, unit: parts.unit || props.unit }
})

const trend = computed(() => {
  if (props.previous === null || typeof props.value !== 'number') return null
  return trendOf(props.value, props.previous, props.positive)
})

/** How a value is written wherever it is text: the axis, the tooltip, the table. */
const written = computed<WrittenFigureOptions>(() => ({
  format: props.format,
  decimals: props.decimals,
  prefix: props.prefix,
  unit: props.unit
}))

/* Written out in full so @nuxt/icon's scan finds them. The chart glyph follows the
   chart, so a card of bars is not switched to with a line. */
const VIEW_ICONS: Record<MetricChartType, string> = {
  line: 'material-symbols:show-chart',
  area: 'material-symbols:area-chart',
  bar: 'material-symbols:bar-chart'
}

const views = computed<readonly SegmentedOption[]>(() => [
  { value: 'chart', icon: VIEW_ICONS[props.type], title: 'Chart' },
  { value: 'table', icon: 'material-symbols:table-chart-outline', title: 'Table' }
])

/**
 * Series switched off in the legend. Kept by name rather than by position, so the set
 * survives the data underneath changing period.
 */
const hidden = ref<string[]>([])

function toggle(name: string) {
  hidden.value = hidden.value.includes(name)
    ? hidden.value.filter(entry => entry !== name)
    : [...hidden.value, name]
}

/* A legend only when there is something to tell apart: with one series the title
   already says what is plotted, and a box with one swatch restates it. */
const legend = computed(() =>
  props.series.length < 2
    ? []
    : props.series.map((entry, index) => ({
        entry,
        color: colorOf(entry, index, palette.value),
        kind: (entry.type ?? props.type) === 'line' ? ('line' as const) : ('fill' as const),
        off: hidden.value.includes(entry.name)
      }))
)

/**
 * Whether there is a picture to draw.
 *
 * Not only "were any series handed over": a card given a week of days that were all null
 * has a shape and nothing in it, which is the usual state of a meter on a project that has
 * not sent anything yet. That draws as a flat line along the axis, and a flat line at zero
 * is a measurement — it says the figure was taken and came back nothing, which is a
 * different claim from never having been taken at all. So a card whose every point is
 * absent says so in words instead.
 *
 * A zero that was actually recorded is data and is drawn. Only `null`, the point no row
 * was written for, counts as missing.
 */
const empty = computed(() => {
  if (props.series.length === 0 || props.categories.length === 0) return true
  return props.series.every(entry => entry.data.every(point => point === null))
})

/* The watermark's wave is seeded so it holds still between visits and differs between
   neighbours, which is what stops a row of empty cards reading as one picture repeated.
   Off the heading, because that is the one thing a card has that its neighbours do not. */
const seed = computed(() => {
  const name = heading.value ?? ''
  let total = 0
  for (let index = 0; index < name.length; index += 1) total += name.charCodeAt(index)
  return (total % 8) + 1
})

/* One announcement per card, and it names the card: several of these load at once and
   "Loading" three times says nothing about which of them the reader is waiting on. The
   figure's bones are hidden from a screen reader rather than announced again. */
const loadingLabel = computed(() => (heading.value ? `Loading ${heading.value}` : 'Loading'))

const chartLabel = computed(() => {
  const names = props.series.map(entry => entry.name).join(', ')
  return `${heading.value}: ${names} by ${props.categoryLabel.toLowerCase()}. The table view lists every value.`
})
</script>

<template>
  <v-card
    :aria-busy="loading"
    :aria-labelledby="titleId"
    border
    flat
    role="group"
  >
    <div class="flex flex-col gap-4 p-5">
      <header class="flex flex-col gap-1">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <GhostSelect
            v-if="options"
            :id="titleId"
            v-model="selected"
            :options="options"
            label="Metric"
          />
          <h2 v-else :id="titleId" class="m-0 text-sm leading-7 font-medium text-ink">
            {{ title }}
          </h2>

          <div v-if="!loading" class="ml-auto flex flex-wrap items-center gap-2">
            <slot name="actions" />
            <SegmentedControl v-model="view" :options="views" label="View" />
          </div>
          <MetricSkeleton v-else class="ml-auto" shape="control" />
        </div>

        <p v-if="caption || $slots.description" class="m-0 text-sm text-ink/60">
          <slot name="description">
            {{ caption }}
          </slot>
        </p>
      </header>

      <div
        v-if="value !== undefined || $slots.figure"
        class="flex flex-wrap items-center gap-x-2.5 gap-y-1"
      >
        <slot name="figure">
          <MetricSkeleton v-if="loading" :trend="previous !== null" shape="figure" />
          <span
            v-else-if="figure === null"
            aria-hidden="true"
            class="metric-card__figure font-mono text-ink/30"
          >
            &ndash;
          </span>
          <span v-else class="metric-card__figure font-mono text-ink">
            <span v-if="prefix" class="metric-card__affix text-ink/55">{{ prefix }}</span>
            <span>{{ figure.digits }}{{ figure.magnitude }}</span>
            <span v-if="figure.unit" class="metric-card__affix text-ink/55">
              {{ figure.unit }}
            </span>
          </span>

          <!-- Only where the last period gave something to measure against; the tile does
               the same, and for the same reason. -->
          <p v-if="trend?.comparable && !loading" class="flex items-center gap-1.5 text-xs">
            <span
              :class="TREND_TONES[trend.tone]"
              class="inline-flex items-center gap-0.5 font-mono font-medium tabular-nums"
            >
              <Icon :name="TREND_ICONS[trend.direction]" aria-hidden="true" class="text-sm" />
              {{ trend.text }}
            </span>
            <span class="text-ink/55">{{ compare }}</span>
          </p>
        </slot>
      </div>

      <div class="flex flex-col gap-3">
        <MetricSkeleton v-if="loading && legend.length" :count="legend.length" shape="legend" />
        <ul
          v-else-if="legend.length"
          aria-label="Series"
          class="m-0 flex list-none flex-wrap items-center gap-x-4 gap-y-1 p-0 sm:justify-end"
        >
          <li v-for="item in legend" :key="item.entry.name">
            <button
              :aria-pressed="!item.off"
              :class="item.off ? 'text-ink/35' : 'text-ink/70 hover:text-ink'"
              class="inline-flex items-center gap-1.5 rounded-(--ps-radius-x-small) py-0.5 text-xs transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              type="button"
              @click="toggle(item.entry.name)"
            >
              <MetricKey
                :color="item.color"
                :dashed="item.entry.dashed"
                :kind="item.kind"
                :muted="item.off"
              />
              {{ item.entry.name }}
            </button>
          </li>
        </ul>

        <MetricSkeleton v-if="loading" :height="height" :label="loadingLabel" />
        <!-- The same band the usage tiles draw, at the chart's own height: a watermark of
             the line that will be there and a pill saying there is none yet. A dashed box
             with a sentence in it was the one thing on these screens that looked like a
             control that had failed to load rather than a meter with nothing to report. -->
        <NoDataPlaceholder
          v-else-if="empty"
          :height="height"
          :seed="seed"
          label="No data yet"
        />
        <MetricChart
          v-else-if="view === 'chart'"
          :categories="categories"
          :format="written"
          :height="height"
          :hidden="hidden"
          :label="chartLabel"
          :series="series"
          :titles="titles"
          :type="type"
        />
        <MetricTable
          v-else
          :categories="categories"
          :category-label="categoryLabel"
          :format="written"
          :height="height"
          :hidden="hidden"
          :series="series"
          :titles="titles"
          :type="type"
        />
      </div>
    </div>
  </v-card>
</template>

<style scoped>
/* The statistic tile's figure a step up, in the tile's proportions: medium, because it
   is DM Mono's heaviest weight, and tracked in a touch because a mono face opens up at
   display size. The affixes sit at half size. MetricSkeleton draws the loading figure
   at this size and leading, so the two move together. */
.metric-card__figure {
  /* Centred and gapped like the statistic tile's, and for the same reasons: an affix at
     half the size sits in the middle of the digits it qualifies rather than at their foot,
     and one gap serves both ends. */
  align-items: center;
  display: inline-flex;
  gap: 0.2em;
  font-size: 1.75rem;
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: 1.15;
  white-space: nowrap;
}

.metric-card__affix {
  font-size: 0.5em;
  letter-spacing: 0;
}
</style>
